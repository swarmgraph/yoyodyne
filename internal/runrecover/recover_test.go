package runrecover

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var now = time.Date(2026, 10, 16, 9, 30, 0, 0, time.UTC)

const product = domain.ProductID("yoyodyne")

// harness is one product's run store and kept observations in a temporary
// state root, and a recoverer over them whose tracker and pipeline rules a
// test sets.
type harness struct {
	t            *testing.T
	root         string
	store        *runstate.Store
	observations *runstate.HealthObservationStore
	blockers     []string
	blockersErr  error
	continuable  error
	finishable   error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	store, err := runstate.NewStore(root, product)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	observations, err := runstate.NewHealthObservationStore(root, product)
	if err != nil {
		t.Fatalf("NewHealthObservationStore() error = %v", err)
	}
	return &harness{t: t, root: root, store: store, observations: observations}
}

func (h *harness) recoverer(runs Runs) Recoverer {
	return Recoverer{
		Runs:         runs,
		Observations: h.observations,
		Blockers: func(context.Context, string) ([]string, error) {
			return append([]string(nil), h.blockers...), h.blockersErr
		},
		Continuable: func(runstate.State) error { return h.continuable },
		Finishable:  func(runstate.State) error { return h.finishable },
		Waitable:    owedItsIntegration,
		Now:         func() time.Time { return now },
	}
}

// observe keeps an observation taken age ago and returns its identifier.
func (h *harness) observe(age time.Duration) string {
	h.t.Helper()
	id := "health-test-" + strings.ReplaceAll(age.String(), ".", "-")
	if err := h.observations.Save(id, now.Add(-age), json.RawMessage(`{}`)); err != nil {
		h.t.Fatalf("keep observation: %v", err)
	}
	return id
}

// preserved is a run a redeploy put down at its developer attempt: in flight,
// with nobody behind it, and with work, a session and counters to keep.
func (h *harness) preserved() runstate.State {
	h.t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		h.t.Fatalf("NewRunID() error = %v", err)
	}
	started := now.Add(-3 * time.Hour)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: product, RepositoryID: "yoyodyne",
		WorkItemID: "yoyodyne-06m", Backend: domain.BackendClaudeCode, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping,
		StartedAt: started, UpdatedAt: started,
		WorktreePath: "/state/worktrees/yoyodyne-06m", Branch: "yoyodyne/yoyodyne-06m/1a2b3c4d", BaseCommit: strings.Repeat("a", 40), TargetBranch: "main",
		ProviderSessionID: "7a04a848-4264-45f1-a310-0792bdcac02d", RepairAttempts: 2, ReviewRounds: 1, IntegrationRetries: 1,
		RedeployStop: &runstate.RedeployStop{At: now.Add(-time.Hour), Phase: runstate.PhaseDeveloping, BoundSeconds: 900, SessionID: "watch-1"},
	}
	if err := h.store.Create(state); err != nil {
		h.t.Fatalf("Create() error = %v", err)
	}
	return state
}

func (h *harness) read(runID string) runstate.State {
	h.t.Helper()
	state, err := h.store.Load(runID)
	if err != nil {
		h.t.Fatalf("Load(%s) error = %v", runID, err)
	}
	return state
}

func holdRequest(id string, state runstate.State, observation string) Request {
	return Request{ID: id, Run: state.RunID, Observation: observation, ExpectStatus: state.Status, ExpectPhase: state.Phase,
		Action: runstate.RecoverHold, Reason: "the tracker read it waited on was cancelled, and continuing needs the architect's answer",
		Hold: &HoldRequest{Owner: domain.RoleDevelopmentManager, NextAction: "ask the architect whether the read can be retried", FollowUp: "at the next scheduled pass"}}
}

// A hold is written with its request in one write, read back, and answered
// applied with the run.verify state that checks it; a repeat is answered from
// the record and writes nothing.
func TestAHoldIsAppliedReadBackAndARepeatIsAnsweredFromTheRecord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	recoverer := h.recoverer(h.store)

	result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-1", state, observation))
	if result.Outcome != Applied || result.Verify != runinspect.ExpectUnresolvedHoldRecorded {
		t.Fatalf("Recover() = %+v, want applied, checked by an unresolved hold", result)
	}
	recorded := h.read(state.RunID)
	if recorded.RecoveryHold == nil || recorded.RecoveryHold.RequestID != "dm-1" || recorded.RecoveryHold.Owner != domain.RoleDevelopmentManager {
		t.Fatalf("recorded hold = %+v, want the request's", recorded.RecoveryHold)
	}
	entry, found := recorded.RecoveryRequest("dm-1")
	if !found || entry.Outcome != runstate.RecoveryApplied || entry.SettledAt == nil || entry.By != domain.RoleDevelopmentManager {
		t.Fatalf("recorded request = %+v, found %t; want it applied and read back", entry, found)
	}
	if recorded.Phase != runstate.PhaseDeveloping || recorded.RepairAttempts != 2 || recorded.ProviderSessionID != state.ProviderSessionID {
		t.Fatalf("the hold changed what the run keeps: %+v", recorded)
	}

	again := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-1", state, observation))
	if again.Outcome != Applied || !again.Repeat {
		t.Fatalf("repeat = %+v, want the recorded answer", again)
	}
	if after := h.read(state.RunID); !after.UpdatedAt.Equal(recorded.UpdatedAt) || len(after.RecoveryRequests) != 1 {
		t.Fatalf("a repeat wrote to the run: %d requests, updated %s then %s", len(after.RecoveryRequests), recorded.UpdatedAt, after.UpdatedAt)
	}

	changed := holdRequest("dm-1", state, observation)
	changed.Hold.NextAction = "something else"
	if reused := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, changed); reused.Outcome != Refused || reused.Refusal != RefusedIdentifierReused {
		t.Fatalf("a reused identifier with other content = %+v, want refused as reused", reused)
	}
}

// A run that is not as the request expected, or a stop that a later stop has
// replaced, is refused as state moved and left exactly as it was.
func TestAMovedRunIsRefusedAndLeftAsItWas(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	recoverer := h.recoverer(h.store)

	moved := holdRequest("dm-1", state, observation)
	moved.ExpectPhase = runstate.PhaseChecking
	result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, moved)
	if result.Outcome != Refused || result.Refusal != RefusedStateMoved || len(result.Differs) != 1 {
		t.Fatalf("Recover() = %+v, want refused as state moved naming the phase", result)
	}
	replaced := holdRequest("dm-2", state, observation)
	replaced.Stop = &Stop{Attempt: 0, At: now.Add(-2 * time.Hour)}
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, replaced); result.Outcome != Refused || result.Refusal != RefusedStateMoved {
		t.Fatalf("an answer to a replaced stop = %+v, want refused as state moved", result)
	}
	answered := holdRequest("dm-3", state, observation)
	answered.Stop = &Stop{Attempt: 0, At: state.RedeployStop.At}
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, answered); result.Outcome != Applied {
		t.Fatalf("an answer to the run's own stop = %+v, want applied", result)
	}
}

func (h *harness) recordPath(runID string) string {
	return filepath.Join(h.store.Root(), runID+".json")
}

// An observation older than fifteen minutes, or one nobody keeps, is refused
// for any act, and nothing is written.
func TestAnOldOrMissingObservationIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	recoverer := h.recoverer(h.store)

	old := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-1", state, h.observe(16*time.Minute)))
	if old.Outcome != Refused || old.Refusal != RefusedObservationOld {
		t.Fatalf("an old observation = %+v, want refused as too old", old)
	}
	missing := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-2", state, "health-nobody-kept-this"))
	if missing.Outcome != Refused || missing.Refusal != RefusedObservationAbsent {
		t.Fatalf("a missing observation = %+v, want refused as not found", missing)
	}
	if after := h.read(state.RunID); len(after.RecoveryRequests) != 0 || after.RecoveryHold != nil {
		t.Fatalf("a refused request wrote to the run: %+v", after.RecoveryRequests)
	}
}

// A run whose lease another process holds is never acted on, however old its
// record is: a live or uncertain worker keeps it.
func TestARunSomebodyHoldsIsNeverActedOn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	_, lease, err := h.store.AdoptRun(context.Background(), state.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	defer lease.Release()

	result := h.recoverer(h.store).Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-1", state, observation))
	if result.Outcome != Refused || result.Refusal != RefusedWorkerPresent {
		t.Fatalf("Recover() on a held run = %+v, want refused for its worker", result)
	}
	if after := h.read(state.RunID); len(after.RecoveryRequests) != 0 {
		t.Fatalf("a held run was written: %+v", after.RecoveryRequests)
	}
}

// The dependency wait keeps the interrupted phase exactly, gives back the
// developer slot only because it is written, and keeps the run's identity,
// branch, worktree, session and counters; the item's dependencies are read as
// the harness acts, and a run whose item waits on nothing is refused a wait
// and refused a continuation while it does.
func TestADependencyWaitKeepsTheInterruptedPhaseAndGivesTheSlotBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	recoverer := h.recoverer(h.store)
	if !state.HoldsDeveloperSlot() {
		t.Fatal("the preserved run should hold its slot before the wait")
	}

	wait := Request{ID: "dm-wait", Run: state.RunID, Observation: observation, ExpectStatus: state.Status, ExpectPhase: state.Phase,
		Action: runstate.RecoverWait, Reason: "a dependency was added while the developer worked", Wait: &WaitRequest{On: WaitOnDependencies}}
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, wait); result.Outcome != Refused || result.Refusal != RefusedNotAllowed {
		t.Fatalf("a wait with nothing to wait on = %+v, want refused", result)
	}

	h.blockers = []string{"yoyodyne-e7c"}
	continueRequest := Request{ID: "dm-continue", Run: state.RunID, Observation: observation, ExpectStatus: state.Status, ExpectPhase: state.Phase,
		Action: runstate.RecoverContinue, Reason: "the redeploy is over"}
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, continueRequest); result.Outcome != Refused || !strings.Contains(result.Next, "dependency wait") {
		t.Fatalf("a continuation while the item waits = %+v, want refused pointing at the wait", result)
	}

	result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, wait)
	if result.Outcome != Applied || result.Verify != runinspect.ExpectWaitRecorded {
		t.Fatalf("Recover(wait) = %+v, want applied", result)
	}
	waiting := h.read(state.RunID)
	if waiting.DependencyPause == nil || waiting.DependencyPause.Summary() != "yoyodyne-e7c" {
		t.Fatalf("recorded wait = %+v, want one on yoyodyne-e7c", waiting.DependencyPause)
	}
	if waiting.Phase != runstate.PhaseDeveloping {
		t.Fatalf("the wait moved the run to %s; an interrupted developer resumes at developing", waiting.Phase)
	}
	if waiting.HoldsDeveloperSlot() {
		t.Fatal("a run waiting on its dependencies still holds a developer slot")
	}
	if waiting.RunID != state.RunID || waiting.Branch != state.Branch || waiting.WorktreePath != state.WorktreePath || waiting.ProviderSessionID != state.ProviderSessionID ||
		waiting.RepairAttempts != state.RepairAttempts || waiting.ReviewRounds != state.ReviewRounds || waiting.IntegrationRetries != state.IntegrationRetries ||
		waiting.RedeployStop == nil {
		t.Fatalf("the wait lost something the run keeps: %+v", waiting)
	}
	verified := runinspect.Inspector{Runs: h.store, Now: func() time.Time { return now }}.Verify(context.Background(), state.RunID, runinspect.ExpectWaitRecorded)
	if verified.Verdict != runinspect.Met {
		t.Fatalf("run.verify(wait recorded) = %+v, want met", verified)
	}

	h.blockersErr = errors.New("bd show timed out")
	unread := wait
	unread.ID = "dm-wait-2"
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, unread); result.Outcome != Refused || result.Refusal != RefusedUnreadable {
		t.Fatalf("a wait whose dependencies could not be read = %+v, want refused as unreadable", result)
	}
}

// A continuation is recorded for the work session to take up, only on a run
// the pipeline can take up from its record, and only while the run is in
// flight; the hold it replaces is cleared.
func TestAContinuationIsRecordedForTheWorkSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	recoverer := h.recoverer(h.store)
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-hold", state, observation)); result.Outcome != Applied {
		t.Fatalf("hold = %+v", result)
	}

	h.continuable = errors.New("its record names no developer session to continue in")
	request := Request{ID: "dm-continue", Run: state.RunID, Observation: observation, ExpectStatus: state.Status, ExpectPhase: state.Phase,
		Action: runstate.RecoverContinue, Reason: "the redeploy is over and nothing blocks it"}
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, request); result.Outcome != Refused || !strings.Contains(result.Says, "no developer session") {
		t.Fatalf("a run the pipeline cannot take up = %+v, want refused saying why", result)
	}

	h.continuable = nil
	result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, request)
	if result.Outcome != Applied || result.Verify != runinspect.ExpectContinuationStarted {
		t.Fatalf("Recover(continue) = %+v, want applied", result)
	}
	recorded := h.read(state.RunID)
	if !recorded.RecoveryContinuationOutstanding(runstate.RecoverContinue) || recorded.RecoveryHold != nil {
		t.Fatalf("recorded continuation %+v, hold %+v; want an outstanding continuation and no hold", recorded.RecoveryContinuation, recorded.RecoveryHold)
	}
	verified := runinspect.Inspector{Runs: h.store, Now: func() time.Time { return now }}.Verify(context.Background(), state.RunID, runinspect.ExpectContinuationStarted)
	if verified.Verdict != runinspect.Unknown {
		t.Fatalf("run.verify(continuation started) before the work session took it up = %+v, want unknown", verified)
	}
}

// The actions stopped-run recovery builds are refused as not yet built, the
// release of a run that has ended names why nothing is released, and a role
// without the grant is refused.
func TestWhatIsNotBuiltOrNotAllowedIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	recoverer := h.recoverer(h.store)
	for _, action := range []runstate.RecoveryAction{runstate.RecoverRepair, runstate.RecoverRerun, runstate.RecoverGiveBack, runstate.RecoverAsk} {
		request := Request{ID: "dm-" + strings.ReplaceAll(string(action), " ", "-"), Run: state.RunID, Observation: observation, ExpectStatus: state.Status, ExpectPhase: state.Phase, Action: action, Reason: "try it"}
		if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, request); result.Outcome != Refused || result.Refusal != RefusedNotBuilt || !strings.Contains(result.Says, "yoyodyne-ifd.428.99") {
			t.Errorf("Recover(%s) = %+v, want refused as not yet built, naming the work that builds it", action, result)
		}
	}
	if result := recoverer.Recover(context.Background(), domain.RoleArchitect, holdRequest("arch-1", state, observation)); result.Outcome != Refused || result.Refusal != RefusedNoGrant {
		t.Errorf("Recover() by the architect = %+v, want refused for want of the grant", result)
	}
	release := Request{ID: "dm-release", Run: state.RunID, Observation: observation, ExpectStatus: state.Status, ExpectPhase: state.Phase, Action: runstate.RecoverRelease, Reason: "free the slot"}
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, release); result.Outcome != Refused || result.Refusal != RefusedNotAllowed {
		t.Errorf("releasing a run in flight = %+v, want refused", result)
	}
	if after := h.read(state.RunID); len(after.RecoveryRequests) != 0 {
		t.Fatalf("a refused request wrote to the run: %+v", after.RecoveryRequests)
	}
}

// faultyRuns is the real store with its writes and reads made to fail as a
// file system can: a save that replaced the record and then reported an
// error, a save refused before anything was written, and reads that fail.
type faultyRuns struct {
	*runstate.Store
	mu sync.Mutex
	// saveThenFail makes the next save write and then report an error;
	// refuseSave makes it report one without writing; failLoads makes that many
	// strict reads fail.
	saveThenFail, refuseSave bool
	failLoads                int
}

func (f *faultyRuns) Save(state runstate.State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.refuseSave:
		f.refuseSave = false
		return errors.New("create temporary run state: no space left on device")
	case f.saveThenFail:
		f.saveThenFail = false
		if err := f.Store.Save(state); err != nil {
			return err
		}
		return errors.New("sync run state directory: input/output error")
	}
	return f.Store.Save(state)
}

func (f *faultyRuns) Load(runID string) (runstate.State, error) {
	f.mu.Lock()
	if f.failLoads > 0 {
		f.failLoads--
		f.mu.Unlock()
		return runstate.State{}, errors.New("open run state: input/output error")
	}
	f.mu.Unlock()
	return f.Store.Load(runID)
}

// A write the store reported failing is never taken at its word: it is read
// back. One that reads back is applied; one that reads back absent changed
// nothing; and one that cannot be read back is unknown, reported on the run,
// and settled by the next request under the lease before it does anything.
func TestAnUncertainWriteIsReadBackBeforeAnythingIsClaimed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	runs := &faultyRuns{Store: h.store}
	recoverer := h.recoverer(runs)

	runs.refuseSave = true
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-1", state, observation)); result.Outcome != Refused || result.Refusal != RefusedNotWritten {
		t.Fatalf("a save refused before writing = %+v, want refused as not written", result)
	}
	if after := h.read(state.RunID); len(after.RecoveryRequests) != 0 || after.RecoveryHold != nil {
		t.Fatalf("a refused write left something on the run: %+v", after)
	}

	runs.saveThenFail = true
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-2", state, observation)); result.Outcome != Applied || !strings.Contains(result.Says, "reads back") {
		t.Fatalf("a save that wrote and then reported an error = %+v, want applied once read back", result)
	}

	// The write lands, the store says it failed, and the read back fails too.
	runs.saveThenFail, runs.failLoads = true, 1
	third := holdRequest("dm-3", state, observation)
	third.Hold.NextAction = "look again after the tracker answers"
	unknown := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, third)
	if unknown.Outcome != Unknown || unknown.NextOwner != "development-manager" {
		t.Fatalf("a write that could not be read back = %+v, want unknown, owned by the development manager", unknown)
	}
	pending := h.read(state.RunID).PendingRecoveryRequests()
	if len(pending) != 1 || pending[0].ID != "dm-3" {
		t.Fatalf("pending requests = %+v, want dm-3 standing on the run", pending)
	}

	// The repeat reads it back under the lease and answers from what it holds,
	// without acting again.
	again := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, third)
	if again.Outcome != Applied || !again.Repeat {
		t.Fatalf("the repeat of an unknown request = %+v, want the recorded answer", again)
	}
	settled := h.read(state.RunID)
	if len(settled.PendingRecoveryRequests()) != 0 {
		t.Fatalf("the repeat left the request unsettled: %+v", settled.RecoveryRequests)
	}
	if settled.RecoveryHold == nil || settled.RecoveryHold.RequestID != "dm-3" {
		t.Fatalf("hold after the repeat = %+v, want dm-3's, made once", settled.RecoveryHold)
	}
}

// Requests made at once on one run are carried out one at a time under its
// lease: the same request made twice at once is written once, and every answer
// is either the one write, its recorded repeat, or a refusal that changed
// nothing.
func TestConcurrentRequestsWriteOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	recoverer := h.recoverer(h.store)

	const callers = 6
	results := make([]Result, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-same", state, observation))
		}(i)
	}
	wg.Wait()
	written := 0
	for _, result := range results {
		switch {
		case result.Outcome == Applied && !result.Repeat:
			written++
		case result.Outcome == Applied && result.Repeat:
		case result.Outcome == Refused && result.Refusal == RefusedWorkerPresent:
		default:
			t.Errorf("concurrent answer = %+v, want applied, its repeat, or refused for the lease", result)
		}
	}
	if written != 1 {
		t.Fatalf("%d answers wrote the request, want exactly one", written)
	}
	if after := h.read(state.RunID); len(after.RecoveryRequests) != 1 {
		t.Fatalf("%d requests recorded, want one", len(after.RecoveryRequests))
	}
}

// A record this build cannot read strictly — one written by a newer build — is
// refused rather than written back without what it could not read.
func TestARecordThisBuildCannotReadStrictlyIsLeftAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	path := h.recordPath(state.RunID)
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	fields["field_from_a_later_build"] = true
	newer, _ := json.Marshal(fields)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}

	result := h.recoverer(h.store).Recover(context.Background(), domain.RoleDevelopmentManager, holdRequest("dm-1", state, observation))
	if result.Outcome != Refused || result.Refusal != RefusedUnreadable {
		t.Fatalf("Recover() on a newer record = %+v, want refused as unreadable", result)
	}
	if after, _ := os.ReadFile(path); string(after) != string(newer) {
		t.Fatal("a record this build could not read was rewritten")
	}
}

// The steps after an approved change's stopped integration are recorded for
// the work session only where the pipeline's rule says they can be finished.
func TestFinishingAMergeIsRecordedWhereItCanBeFinished(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	state := h.preserved()
	observation := h.observe(time.Minute)
	recoverer := h.recoverer(h.store)
	request := Request{ID: "dm-finish", Run: state.RunID, Observation: observation, ExpectStatus: state.Status, ExpectPhase: state.Phase,
		Action: runstate.RecoverFinishMerge, Reason: "the checkout is clean again"}

	h.finishable = errors.New("it is not an approved change the environment stopped short of its promotion")
	if result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, request); result.Outcome != Refused || result.Refusal != RefusedNotAllowed {
		t.Fatalf("finishing what cannot be finished = %+v, want refused", result)
	}
	h.finishable = nil
	result := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, request)
	if result.Outcome != Applied || result.Verify != runinspect.ExpectIntegrationCompleted {
		t.Fatalf("Recover(finish the merge) = %+v, want applied, checked by integration completed", result)
	}
	if recorded := h.read(state.RunID); !recorded.RecoveryContinuationOutstanding(runstate.RecoverFinishMerge) {
		t.Fatalf("recorded continuation = %+v, want an outstanding finish", recorded.RecoveryContinuation)
	}
}

// A block is refused whole for a request that will not validate, and decodes
// the closed list of actions.
func TestDecodeRefusesAnInvalidBlockWhole(t *testing.T) {
	t.Parallel()
	valid := `{"requests":[{"request":"dm-1","run":"run-1a2b3c4d","observation":"health-x","expect_status":"running","expect_phase":"developing","action":"wait","reason":"waits on e7c","wait":{"on":"dependencies"}}]}`
	if requests, err := Decode(valid); err != nil || len(requests) != 1 {
		t.Fatalf("Decode(valid) = %v, %v", requests, err)
	}
	for name, payload := range map[string]string{
		"unknown action":   strings.Replace(valid, `"action":"wait"`, `"action":"cancel"`, 1),
		"unknown field":    strings.Replace(valid, `"reason"`, `"shell":"rm -rf","reason"`, 1),
		"wait on a person": strings.Replace(valid, `"on":"dependencies"`, `"on":"the operator"`, 1),
		"hold by operator": `{"requests":[{"request":"dm-1","run":"run-1a2b3c4d","observation":"health-x","expect_status":"running","expect_phase":"","action":"record an unresolved hold","reason":"r","hold":{"owner":"operator","next_action":"n","follow_up":"f"}}]}`,
		"duplicate id":     `{"requests":[` + valid[13:len(valid)-2] + `,` + valid[13:len(valid)-2] + `]}`,
	} {
		if _, err := Decode(payload); err == nil {
			t.Errorf("Decode(%s) accepted it", name)
		}
	}
}

// owedItsIntegration stands in for orchestrator.RecoveryWaitable, which this
// package's tests cannot import; that rule is tested where it is declared.
func owedItsIntegration(state runstate.State) error {
	if state.Phase == runstate.PhaseIntegrating || state.Integration != nil {
		return errors.New("its integration is begun or done, which reconciliation settles")
	}
	return nil
}

// A run interrupted inside its integration is never put on a wait, however long
// its item now waits on other work: what it is owed is the settlement of its
// integration, which a wait would hide from reconciliation. The request is
// refused and nothing is written. A run whose promotion is recorded is refused
// by the same rule, which orchestrator's tests hold to both cases.
func TestAWaitIsRefusedForARunOwedItsIntegration(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(*runstate.State){
		"interrupted inside its integration": func(s *runstate.State) { s.Phase = runstate.PhaseIntegrating },
	} {
		h := newHarness(t)
		created := h.preserved()
		state := h.read(created.RunID)
		change(&state)
		if err := h.store.Save(state); err != nil {
			t.Fatalf("%s: Save() error = %v", name, err)
		}
		h.blockers = []string{"yoyodyne-e7c"}
		observation := h.observe(time.Minute)
		request := Request{ID: "dm-wait", Run: state.RunID, Observation: observation, ExpectStatus: state.Status, ExpectPhase: state.Phase,
			Action: runstate.RecoverWait, Reason: "a dependency was added", Wait: &WaitRequest{On: WaitOnDependencies}}
		result := h.recoverer(h.store).Recover(context.Background(), domain.RoleDevelopmentManager, request)
		if result.Outcome != Refused || result.Refusal != RefusedNotAllowed || !strings.Contains(result.Says, "integration") {
			t.Fatalf("%s: Recover(wait) = %+v, want refused naming the integration", name, result)
		}
		if after := h.read(state.RunID); after.DependencyPause != nil || len(after.RecoveryRequests) != 0 {
			t.Fatalf("%s: the refused wait wrote to the run: pause %#v, requests %d", name, after.DependencyPause, len(after.RecoveryRequests))
		}
	}
}

// Whatever a run ended as, it holds no developer slot and no files another run
// is kept away from, because both are counted only from runs still in flight;
// releasing its reservation is refused saying so, naming its item's claim as
// what giving the item back releases, and nothing is written.
func TestReleasingAFinishedRunsReservationShowsItHoldsNothingToRelease(t *testing.T) {
	t.Parallel()

	for _, status := range []runstate.Status{runstate.StatusSucceeded, runstate.StatusFailed, runstate.StatusCancelled, runstate.StatusTimedOut} {
		h := newHarness(t)
		runID, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		ended := now.Add(-time.Hour)
		state := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: product, RepositoryID: "yoyodyne",
			WorkItemID: "yoyodyne-06m", Backend: domain.BackendClaudeCode, Status: status, Phase: runstate.PhaseReviewing,
			StartedAt: ended.Add(-time.Hour), UpdatedAt: ended, CompletedAt: &ended}
		if err := h.store.Create(state); err != nil {
			t.Fatalf("%s: Create() error = %v", status, err)
		}
		if state.Status.InFlight() || state.HoldsDeveloperSlot() || state.HoldsDocumentLanding() {
			t.Fatalf("a run that ended %s still counts as in flight or holding a slot", status)
		}
		incomplete, err := h.store.Incomplete()
		if err != nil || len(incomplete) != 0 {
			t.Fatalf("%s: Incomplete() = %d runs, %v; the scheduler's slots and file reservations are counted from these", status, len(incomplete), err)
		}
		request := Request{ID: "dm-release", Run: runID, Observation: h.observe(time.Minute), ExpectStatus: status, ExpectPhase: runstate.PhaseReviewing,
			Action: runstate.RecoverRelease, Reason: "free what it holds"}
		result := h.recoverer(h.store).Recover(context.Background(), domain.RoleDevelopmentManager, request)
		if result.Outcome != Refused || result.Refusal != RefusedNotAllowed || !strings.Contains(result.Says, "no developer slot and no files") || !strings.Contains(result.Says, "claim") {
			t.Fatalf("%s: Recover(release) = %+v, want refused naming the slot, the files and the item's claim", status, result)
		}
		verified := runinspect.Inspector{Runs: h.store, Now: func() time.Time { return now }}.Verify(context.Background(), runID, runinspect.ExpectReservationReleased)
		if verified.Verdict != runinspect.Met {
			t.Fatalf("%s: run.verify(reservation released) = %+v, want met", status, verified)
		}
		if after := h.read(runID); len(after.RecoveryRequests) != 0 || !after.UpdatedAt.Equal(ended) {
			t.Fatalf("%s: a refused release wrote to the run", status)
		}
	}
}
