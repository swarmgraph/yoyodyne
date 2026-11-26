package runstate

import (
	"context"
	"strings"
	"testing"
	"time"
)

// stoppingStore records a run in flight and returns the store and the run as
// stored, ready to be stopped.
func stoppingStore(t *testing.T) (*Store, State) {
	t.Helper()
	store, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	running := testState(t, StatusRunning)
	running.Phase = PhaseChecking
	running.Branch = "yoyodyne/task/abc"
	running.WorktreePath = "/state/worktrees/task"
	running.BaseCommit = strings.Repeat("a", 40)
	running.HarnessCommit = strings.Repeat("c", 40)
	if err := store.Create(running); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stored, err := store.Load(running.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(stored.Stops) != 0 {
		t.Fatalf("stops = %#v, want none on a run in flight", stored.Stops)
	}
	return store, stored
}

func stopAt(state State, at time.Time, class StopClass) State {
	state.Status = StatusFailed
	state.StopClass = class
	state.UpdatedAt = at
	state.CompletedAt = &at
	state.Blocker = "Yoyodyne stopped this item: " + string(class)
	return state
}

func reload(t *testing.T, store *Store, runID string) State {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

// Every stop writes exactly one stop, carrying the attempt, the time, the
// step, the candidate commit, whether the change is kept, its evidence and
// its cause; the writes that settle the stop afterwards add none and change
// none, whatever their copy of the run says; and a later stop of the same run
// is a new stop decided afresh.
func TestEveryStopWritesOneStopWhoseCauseNothingLaterChanges(t *testing.T) {
	t.Parallel()

	store, running := stoppingStore(t)
	first := running.StartedAt.Add(time.Hour)
	stopped := stopAt(running, first, StopChecks)
	stopped.CheckFailure = &CheckFailure{Command: "make test", ExitCode: 2, Output: "FAIL"}
	if err := store.Save(stopped); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	stored := reload(t, store, running.RunID)
	if len(stored.Stops) != 1 {
		t.Fatalf("stops = %#v, want one", stored.Stops)
	}
	stop := stored.Stops[0]
	if !stop.StoppedAt.Equal(first) || stop.Step != PhaseChecking || stop.Candidate != running.HarnessCommit || !stop.Kept ||
		stop.Reason != StopChecks || stop.Cause != StopCauseWork || !stop.Charged || stop.Unrecognised ||
		stop.Evidence.CheckCommand != "make test" || stop.Evidence.CheckExitCode != 2 || !strings.Contains(stop.Evidence.Says, "checks") {
		t.Fatalf("stop = %#v, want the failing check's stop as it stood", stop)
	}
	// No process took this run's lease, so it carries no worker and its stop
	// names attempt zero; stopOf below is held to a worker's attempt.
	if stored.Worker != nil || stop.Attempt != 0 {
		t.Fatalf("stop attempt = %d, worker = %#v, want attempt zero for a run no process took", stop.Attempt, stored.Worker)
	}
	withWorker := stopped
	withWorker.Worker = &WorkerIdentity{Attempt: 3}
	if got := stopOf(withWorker).Attempt; got != 3 {
		t.Fatalf("stopOf().Attempt = %d, want the worker's attempt", got)
	}

	// A later step rewrites the run's stop reason and hands in a copy whose
	// stops say something else: the recorded stop stands, and no second is
	// written for the same stop time.
	later := stored
	later.StopClass = StopOperator
	later.Stops = []RunStop{{StoppedAt: first, Reason: StopOperator, Cause: StopCauseOutside, Evidence: StopEvidence{}}}
	later.UpdatedAt = first.Add(time.Minute)
	if err := store.Save(later); err != nil {
		t.Fatalf("Save() of the later step error = %v", err)
	}
	if again := reload(t, store, running.RunID); len(again.Stops) != 1 || again.Stops[0].Cause != StopCauseWork || again.Stops[0].Reason != StopChecks {
		t.Fatalf("stops = %#v, want the stop's cause as it was decided", again.Stops)
	}

	// A narrow update cannot change it either.
	if _, err := store.Update(context.Background(), running.RunID, stopsRewrite{}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if again := reload(t, store, running.RunID); len(again.Stops) != 1 || again.Stops[0].Cause != StopCauseWork {
		t.Fatalf("stops = %#v, want an update to leave the stop as decided", again.Stops)
	}

	// Picked up again and stopped again: a second stop, decided afresh.
	resumed := reload(t, store, running.RunID)
	resumed.StopClass, resumed.CompletedAt, resumed.Blocker, resumed.CheckFailure = "", nil, "", nil
	resumed.Phase, resumed.Status = PhaseReviewing, StatusRunning
	if err := store.ReserveLive(context.Background(), resumed, 4); err != nil {
		t.Fatalf("ReserveLive() error = %v", err)
	}
	resumed = reload(t, store, running.RunID)
	if err := store.Save(stopAt(resumed, first.Add(time.Hour), StopReviewBudget)); err != nil {
		t.Fatalf("Save() of the second stop error = %v", err)
	}
	final := reload(t, store, running.RunID)
	if len(final.Stops) != 2 || final.Stops[0].Cause != StopCauseWork || final.Stops[1].Cause != StopCauseOutside || final.Stops[1].Charged || final.Stops[1].Step != PhaseReviewing {
		t.Fatalf("stops = %#v, want the first kept and a second outside the work, uncharged, at the review", final.Stops)
	}
	if current, ok := final.CurrentStop(); !ok || current.Reason != StopReviewBudget {
		t.Fatalf("current stop = %#v, %t, want the second", current, ok)
	}
}

// stopsRewrite is an update that tries to replace the run's stops.
type stopsRewrite struct{}

func (stopsRewrite) apply(state *State) (bool, error) {
	state.Stops = nil
	state.StopClass = StopManager
	return true, nil
}

// A stop reason the code does not recognise is outside the work, as ruled on
// November 18 for stopped-run recovery: it charges no repair attempt and no
// review round, keeps the change, and is named as unrecognised; and the hold on
// new work, which reads the reason rather than the cause, still counts it.
func TestAnUnrecognisedStopReasonIsUnchargedNamedAndCountedTowardTheHold(t *testing.T) {
	t.Parallel()

	// This build refuses to store a reason it does not know, so the reason
	// reaches it only on a record a newer build wrote; the stop is decided as
	// that record is written, which is what this exercises.
	_, running := stoppingStore(t)
	invented := StopClass("a-reason-no-build-has-written")
	if StopRecognised(invented) {
		t.Fatalf("StopRecognised(%q) = true, want an invented reason unrecognised", invented)
	}
	stopped := stopAt(running, running.StartedAt.Add(time.Hour), invented)
	if cause := StopCauseOf(stopped); cause != StopCauseOutside || cause.Charges() {
		t.Fatalf("StopCauseOf() = %q, want outside the work and uncharged", cause)
	}
	recorded := stopped.withStopRecorded(stopHistory{})
	stop, ok := recorded.CurrentStop()
	if !ok {
		t.Fatalf("stops = %#v, want the stop recorded", recorded.Stops)
	}
	if stop.Cause != StopCauseOutside || stop.Charged || !stop.Unrecognised || stop.Reason != invented || !stop.Kept {
		t.Fatalf("stop = %#v, want it outside the work, uncharged, kept, and named as unrecognised", stop)
	}
	if err := stop.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if StopKeptOutOfHold(invented) || StopKeptOutOfHoldAfter(invented, nil) {
		t.Fatalf("the hold on new work leaves %q out, want it counted", invented)
	}
}

// The causes step 1 lists outside the work, and the harness's own failures
// around the run, are outside it and charged nothing; the change's own
// failures, and the budgets spent on them, are about it; and a run whose
// merge is in doubt is uncertain.
func TestAStopsCauseIsOneOfThree(t *testing.T) {
	t.Parallel()

	for class, want := range map[StopClass]StopCause{
		CauseCheckTimeLimit.StopClass(): StopCauseOutside,
		StopCheckTimeout:                StopCauseOutside,
		StopLostRaces:                   StopCauseOutside,
		CauseNoCheckRan.StopClass():     StopCauseOutside,
		StopReviewBudget:                StopCauseOutside,
		StopBaseFailing:                 StopCauseOutside,
		StopForgeQueueRequired:          StopCauseOutside,
		StopUsagePause:                  StopCauseOutside,
		StopOperator:                    StopCauseOutside,
		StopManager:                     StopCauseOutside,
		StopRedeploy:                    StopCauseOutside,
		StopHarness:                     StopCauseOutside,
		StopDeadClaim:                   StopCauseOutside,
		CauseMergeQueueFull.StopClass(): StopCauseOutside,
		StopChecks:                      StopCauseWork,
		StopReview:                      StopCauseWork,
		StopIntegration:                 StopCauseWork,
		StopRepairBudget:                StopCauseWork,
		StopIntegrationBudget:           StopCauseWork,
	} {
		state := stopAt(testState(t, StatusRunning), time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC), class)
		if got := StopCauseOf(state); got != want {
			t.Errorf("StopCauseOf(%q) = %q, want %q", class, got, want)
		}
		if got := StopCauseOf(state).Charges(); got != (want == StopCauseWork) {
			t.Errorf("a stop for %q charges = %t, want %t", class, got, want == StopCauseWork)
		}
	}
	doubt := stopAt(testState(t, StatusRunning), time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC), StopIntegration)
	doubt.MergeDisagreement = &MergeDisagreement{Cause: MergeCauseUncertain}
	if got := StopCauseOf(doubt); got != StopCauseUncertain || got.Charges() {
		t.Fatalf("StopCauseOf() = %q, want uncertain and uncharged", got)
	}
}

// The hold on new work keeps the four stops condition 5 names out of its
// count.
func TestTheHoldKeepsOutTheFourNamedStops(t *testing.T) {
	t.Parallel()

	for _, class := range []StopClass{CauseCheckTimeLimit.StopClass(), StopLostRaces, CauseNoCheckRan.StopClass(), StopReviewBudget} {
		if !StopKeptOutOfHold(class) {
			t.Errorf("StopKeptOutOfHold(%q) = false, want it kept out", class)
		}
	}
}

// A stop written after a check that failed following a review keeps the
// reviewer's findings, which the run's own fields no longer carry.
func TestAStopAfterACheckFailingAReviewedChangeKeepsTheFindings(t *testing.T) {
	t.Parallel()

	state := stopAt(testState(t, StatusRunning), time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC), StopChecks)
	state.CheckFailure = &CheckFailure{Command: "make test", ExitCode: 1, ReviewBefore: &ReviewBefore{
		Decision: ReviewRepair, Summary: "two things", Findings: []Finding{{Severity: "blocker", Message: "handle nil"}},
	}}
	stop := stopOf(state)
	if len(stop.Evidence.Findings) != 1 || stop.Evidence.Findings[0].Message != "handle nil" || stop.Evidence.ReviewSummary != "two things" {
		t.Fatalf("evidence = %#v, want the reviewer's findings kept", stop.Evidence)
	}
	if err := stop.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
