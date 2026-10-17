package runinspect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var observed = time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC)

// fakeRuns is a run store whose answers a test sets.
type fakeRuns struct {
	state     runstate.State
	readErr   error
	holder    *runstate.LeaseHolder
	holderErr error
	worker    runstate.WorkerReading
	active    []runstate.State
	activeErr error
}

func (f fakeRuns) Read(string) (runstate.State, error) { return f.state, f.readErr }
func (f fakeRuns) Holder(string) (*runstate.LeaseHolder, error) {
	return f.holder, f.holderErr
}
func (f fakeRuns) Worker(runstate.State, time.Time) runstate.WorkerReading { return f.worker }
func (f fakeRuns) Incomplete() ([]runstate.State, error)                   { return f.active, f.activeErr }

func inspector(runs Runs) Inspector {
	return Inspector{Runs: runs, Now: func() time.Time { return observed }}
}

func runningState() runstate.State {
	started := observed.Add(-time.Hour)
	return runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: "run-1a2b3c4d", ProductID: domain.ProductID("yoyodyne"),
		RepositoryID: "yoyodyne", Backend: domain.BackendClaudeCode, WorkItemID: "yoyodyne-test", Status: runstate.StatusRunning, StartedAt: started, UpdatedAt: started}
}

// A run record longer than the bound is cut at it, on a whole character, with
// the cut declared and the whole size named.
func TestInspectCutsALongRecordAndDeclaresTheCut(t *testing.T) {
	t.Parallel()

	state := runningState()
	state.Failure = strings.Repeat("界", MaxRecordBytes)
	inspection := inspector(fakeRuns{state: state}).Inspect(context.Background(), state.RunID)
	if !inspection.RecordCut || len(inspection.Record) > MaxRecordBytes || inspection.RecordBytes <= MaxRecordBytes {
		t.Fatalf("record cut=%t kept=%d whole=%d, want a declared cut at %d", inspection.RecordCut, len(inspection.Record), inspection.RecordBytes, MaxRecordBytes)
	}
	if !strings.HasPrefix(inspection.Record, `{"`) || strings.ContainsRune(inspection.Record, '�') {
		t.Fatalf("record was not cut on a whole character: %q", inspection.Record[len(inspection.Record)-20:])
	}
	short := inspector(fakeRuns{state: runningState()}).Inspect(context.Background(), state.RunID)
	if short.RecordCut || short.RecordBytes != len(short.Record) {
		t.Fatalf("a short record was cut: %+v", short)
	}
}

// What an inspection reports beside the record: the holder, the worker, the
// reservation, the wait and its release, and the last continuation and
// refusal. Secrets are removed from all of it.
func TestInspectReportsWhatSurroundsTheRun(t *testing.T) {
	t.Parallel()

	state := runningState()
	resets := observed.Add(time.Hour)
	state.UsageLimitResetsAt = &resets
	state.PauseCause = runstate.PauseUsageLimit
	state.SweepContinuations = []runstate.SweepContinuation{{ContinuedAt: observed.Add(-30 * time.Minute), Reason: "waited out, token hunter2"}}
	state.CheckStageContinuations = []runstate.CheckStageContinuation{{ContinuedAt: observed.Add(-45 * time.Minute), Reason: "earlier"}}
	state.StallContinuationRefused = "somebody has been in the worktree"
	holder := &runstate.LeaseHolder{PID: 42, HeldAt: observed}
	runs := fakeRuns{state: state, holder: holder, worker: runstate.WorkerReading{Liveness: runstate.WorkerLive, Says: "live"}}
	i := inspector(runs)
	i.RedactValues = []string{"hunter2"}
	inspection := i.Inspect(context.Background(), state.RunID)
	if inspection.LeaseHolder == nil || inspection.LeaseHolder.PID != 42 || inspection.Worker.Liveness != runstate.WorkerLive {
		t.Fatalf("holder=%+v worker=%+v", inspection.LeaseHolder, inspection.Worker)
	}
	if inspection.Reservation == nil || !inspection.Reservation.Held || !inspection.Reservation.DeveloperSlot {
		t.Fatalf("reservation = %+v, want the item and a slot held", inspection.Reservation)
	}
	if inspection.Wait == nil || !strings.Contains(inspection.Wait.Release, resets.Format(time.RFC3339)) {
		t.Fatalf("wait = %+v, want the reset time as its release", inspection.Wait)
	}
	if inspection.LastContinuation == nil || inspection.LastContinuation.Reason != "waited out, token [REDACTED]" {
		t.Fatalf("last continuation = %+v, want the latest, redacted", inspection.LastContinuation)
	}
	if inspection.LastRefusal == nil || inspection.LastRefusal.Says != state.StallContinuationRefused {
		t.Fatalf("last refusal = %+v", inspection.LastRefusal)
	}
	if strings.Contains(inspection.Record, "hunter2") {
		t.Fatal("the record handed back carries a secret")
	}
	if inspection.Remains == nil || inspection.Remains.Recorded() {
		t.Fatalf("remains = %+v, want nothing recorded to look for", inspection.Remains)
	}
	if !inspection.ObservedAt.Equal(observed) {
		t.Fatalf("observed at %v", inspection.ObservedAt)
	}
}

// Evidence that could not be read answers unknown, for every expected state,
// and never met.
func TestVerifyAnswersUnknownWhereTheEvidenceCannotBeRead(t *testing.T) {
	t.Parallel()

	unreadable := inspector(fakeRuns{readErr: errors.New("decode run state: unexpected end of JSON input")})
	for _, expect := range ExpectedStates() {
		verification := unreadable.Verify(context.Background(), "run-1a2b3c4d", expect)
		if verification.Verdict != Unknown || !strings.Contains(strings.Join(verification.Evidence, " "), "could not be read") || !verification.ObservedAt.Equal(observed) {
			t.Errorf("Verify(%s) with an unreadable record = %+v, want unknown naming what could not be read", expect, verification)
		}
	}
	uncertain := inspector(fakeRuns{state: runningState(), worker: runstate.WorkerReading{Liveness: runstate.WorkerUncertain, Says: "the stamp could not be read"}})
	if verification := uncertain.Verify(context.Background(), "run-1a2b3c4d", ExpectWorkerLive); verification.Verdict != Unknown {
		t.Errorf("Verify(worker live) with an uncertain worker = %+v, want unknown", verification)
	}
	listing := inspector(fakeRuns{state: runningState(), activeErr: errors.New("scan refused")})
	if verification := listing.Verify(context.Background(), "run-1a2b3c4d", ExpectRerunHoldsItem); verification.Verdict != Unknown {
		t.Errorf("Verify(rerun holds the item) with an unreadable listing = %+v, want unknown", verification)
	}
	pending := runningState()
	pending.RepairContinuations = []runstate.RepairContinuation{{GrantedAttempts: 1, DispatchPending: true, ContinuedAt: observed}}
	for _, expect := range []Expected{ExpectRepairStarted, ExpectContinuationStarted} {
		if verification := inspector(fakeRuns{state: pending}).Verify(context.Background(), "run-1a2b3c4d", expect); verification.Verdict != Unknown {
			t.Errorf("Verify(%s) for a repair not yet taken up = %+v, want unknown", expect, verification)
		}
	}
}

// Each state this build records is met where the record shows it and not met
// where it does not; the ones only later recovery work records are unknown, and
// a continuation asked for and not yet taken up is unknown too.
func TestVerifyDecidesEachStateFromTheRecord(t *testing.T) {
	t.Parallel()

	running := runningState()
	finished := runningState()
	finished.Status = runstate.StatusSucceeded
	finished.Integration = &runstate.Integration{TargetBranch: "main", TargetCommit: "abcdef1"}
	finished.RepairContinuations = []runstate.RepairContinuation{{GrantedAttempts: 2, ContinuedAt: observed.Add(-time.Minute)}}
	rerun := runningState()
	rerun.RunID, rerun.StartedAt = "run-9f8e7d6c", observed
	paused := runningState()
	paused.DependencyPause = &runstate.DependencyPause{Blockers: []string{"yoyodyne-other"}}
	held := runningState()
	held.RecoveryHold = &runstate.RecoveryHold{RequestID: "dm-1", Owner: domain.RoleDevelopmentManager, NextAction: "ask the architect", FollowUp: "at the next pass", At: observed}
	asked := runningState()
	asked.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: "dm-2", Action: runstate.RecoverContinue, Reason: "the redeploy is over", RequestedAt: observed}
	takenAt := observed.Add(time.Minute)
	taken := asked
	taken.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: "dm-2", Action: runstate.RecoverContinue, Reason: "the redeploy is over", RequestedAt: observed, TakenUpAt: &takenAt}
	cases := []struct {
		runs   fakeRuns
		expect Expected
		want   Verdict
	}{
		{fakeRuns{state: running, worker: runstate.WorkerReading{Liveness: runstate.WorkerLive}}, ExpectWorkerLive, Met},
		{fakeRuns{state: running, worker: runstate.WorkerReading{Liveness: runstate.WorkerAbsent}}, ExpectWorkerLive, NotMet},
		{fakeRuns{state: paused}, ExpectWaitRecorded, Met},
		{fakeRuns{state: running}, ExpectWaitRecorded, NotMet},
		{fakeRuns{state: finished}, ExpectReservationReleased, Met},
		{fakeRuns{state: running}, ExpectReservationReleased, NotMet},
		{fakeRuns{state: finished}, ExpectContinuationStarted, Met},
		{fakeRuns{state: running}, ExpectContinuationStarted, NotMet},
		{fakeRuns{state: finished}, ExpectRepairStarted, Met},
		{fakeRuns{state: running}, ExpectRepairStarted, NotMet},
		{fakeRuns{state: running, active: []runstate.State{running, rerun}}, ExpectRerunHoldsItem, Met},
		{fakeRuns{state: running, active: []runstate.State{running}}, ExpectRerunHoldsItem, NotMet},
		{fakeRuns{state: finished}, ExpectIntegrationCompleted, Met},
		{fakeRuns{state: running}, ExpectIntegrationCompleted, NotMet},
		{fakeRuns{state: finished}, ExpectItemGivenBack, Unknown},
		{fakeRuns{state: finished}, ExpectQuestionRecorded, Unknown},
		{fakeRuns{state: held}, ExpectUnresolvedHoldRecorded, Met},
		{fakeRuns{state: running}, ExpectUnresolvedHoldRecorded, NotMet},
		{fakeRuns{state: asked}, ExpectContinuationStarted, Unknown},
		{fakeRuns{state: taken}, ExpectContinuationStarted, Met},
	}
	covered := map[Expected]bool{}
	for _, c := range cases {
		covered[c.expect] = true
		verification := inspector(c.runs).Verify(context.Background(), "run-1a2b3c4d", c.expect)
		if verification.Verdict != c.want || len(verification.Evidence) == 0 {
			t.Errorf("Verify(%s) = %+v, want %s with evidence", c.expect, verification, c.want)
		}
	}
	for _, expect := range ExpectedStates() {
		if !covered[expect] {
			t.Errorf("no case checks %s", expect)
		}
	}
}

// The list of expected states is closed, and a block naming any other is
// refused whole before anything is read.
func TestABlockNamingAnExpectedStateOffTheListIsRefused(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		`{"requests":[{"action":"verify","run":"run-1a2b3c4d","expect":"recovered"}]}`,
		`{"requests":[{"action":"verify","run":"run-1a2b3c4d"}]}`,
		`{"requests":[{"action":"inspect","run":"run-1a2b3c4d","expect":"worker live"}]}`,
		`{"requests":[{"action":"inspect","run":"../run-1a2b3c4d"}]}`,
		`{"requests":[{"action":"recover","run":"run-1a2b3c4d"}]}`,
		`{"requests":[{"action":"inspect","run":"run-1a2b3c4d","path":"/etc"}]}`,
		`{"requests":[{"action":"verify","run":"run-1a2b3c4d","expect":"worker live"}]} trailing`,
	} {
		if _, err := Decode(payload); err == nil {
			t.Errorf("Decode(%s) accepted it", payload)
		}
	}
	requests, err := Decode(`{"requests":[{"action":"inspect","run":"run-1a2b3c4d"},{"action":"verify","run":"run-1a2b3c4d","expect":"rerun holds the item"}]}`)
	if err != nil || len(requests) != 2 || requests[1].Expect != ExpectRerunHoldsItem {
		t.Fatalf("Decode() = %+v, %v", requests, err)
	}
	if verification := inspector(fakeRuns{state: runningState()}).Verify(context.Background(), "run-1a2b3c4d", Expected("recovered")); verification.Verdict != Unknown {
		t.Fatalf("Verify() of a state off the list = %+v, want unknown", verification)
	}
}

// Against a real store: a run this process reserved is inspected with its
// stamp and a live worker, and is not reported released.
func TestInspectAndVerifyReadARealStoreWithoutTakingTheLease(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	state := runningState()
	state.Status = runstate.StatusPending
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	state.RunID = runID
	lease, err := store.Reserve(context.Background(), state, 1)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	defer lease.Release()
	i := Inspector{Runs: store}
	inspection := i.Inspect(context.Background(), runID)
	if inspection.RecordProblem != "" || inspection.LeaseHolder == nil || inspection.LeaseHolder.Worker == nil || inspection.LeaseHolder.Worker.Attempt != 1 {
		t.Fatalf("inspection = %+v, want the record and this process's stamp", inspection)
	}
	if inspection.Worker.Liveness != runstate.WorkerLive {
		t.Fatalf("worker = %+v, want live", inspection.Worker)
	}
	if !strings.Contains(inspection.Record, `"worker":{"pid":`) {
		t.Fatalf("record carries no worker: %s", inspection.Record)
	}
	if verification := i.Verify(context.Background(), runID, ExpectWorkerLive); verification.Verdict != Met {
		t.Fatalf("Verify(worker live) = %+v", verification)
	}
	if verification := i.Verify(context.Background(), runID, ExpectReservationReleased); verification.Verdict != NotMet {
		t.Fatalf("Verify(reservation released) = %+v", verification)
	}
	// Reading took nothing: the lease is still this process's to let go of.
	if held, err := store.Held(runID); err != nil || !held {
		t.Fatalf("Held() after inspecting = %v, %v", held, err)
	}
}
