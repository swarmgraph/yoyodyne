package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A re-run the development manager decided about a stopped run whose branch is
// still there is carried out by the run it starts. Where that run then stops in a
// way no stoppage rule holds — here a check that ran out of time before the run's
// record said which limit stopped it — the hold falls back to the decided run,
// whose branch is still there. It must not read the spent decision as one the
// harness still owes: the carry-out has nothing left to start, so it neither
// starts nor refuses it, and the item sits held for the harness indefinitely.
// This is the shape yoyodyne-ifd.435.10 and yoyodyne-ifd.433.25 were in.
func TestACarriedOutRerunIsNotHeldAsTheHarnessesMove(t *testing.T) {
	t.Parallel()

	const item = "yoyodyne-ifd.435.10"
	decidedAt := time.Date(2026, 10, 10, 13, 29, 17, 0, time.UTC)
	stopped := preservedRun("run-07feebbe", item, decidedAt.Add(-200*time.Hour))
	later := runstate.State{
		RunID:           "run-de59f8a6",
		WorkItemID:      item,
		Status:          runstate.StatusTimedOut,
		Phase:           runstate.PhaseChecking,
		StartedAt:       decidedAt.Add(15 * time.Hour),
		UpdatedAt:       decidedAt.Add(17 * time.Hour),
		Branch:          "yoyodyne/" + item + "/de59f8a6",
		WorktreePath:    "/state/worktrees/run-de59f8a6",
		WorktreeRemoved: true,
		CheckStage:      &runstate.CheckStage{Command: "make race"},
		Failure:         "verification timed out: make race ran for 31m9s and was stopped at its 30m0s execution.check_timeout budget",
	}
	if stoppage(later) {
		t.Fatalf("the fixture's later run reads as a stoppage, want the shape no stoppage rule holds")
	}
	rerun := decisions(map[string]runstate.TriageCounters{
		item: {WorkItemID: item, Reruns: 1, Decisions: []runstate.TriageDecision{
			{Decision: runstate.TriageDecisionRerun, RunID: stopped.RunID, DecidedAt: decidedAt},
		}},
	})

	before := heldForAPerson([]runstate.State{stopped}, nil, rerun, asRecorded)
	if !before.Decided(item) {
		t.Fatalf("before the re-run started the hold names the development manager, want the harness carrying her re-run out")
	}

	held := heldForAPerson([]runstate.State{stopped, later}, nil, rerun, asRecorded)
	reason := heldReason(t, held, item)
	if held.Decided(item) {
		t.Fatalf("held for %q as the harness's move, want the re-run read as carried out by %s", reason, later.RunID)
	}
	for _, want := range []string{"has been carried out", later.RunID, "ended timed out", "she decides what happens next"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("held for %q, want it to say %q", reason, want)
		}
	}
}

// The yoyodyne-ifd.429.68 shape: the re-run's own run died before it made its
// checkout, so nothing of it is left, and the development manager decided to
// wait on named work about that run. The hold falls back to the decided run's
// preserved branch, and says the wait rather than a carry-out.
func TestACarriedOutRerunWhoseRunSheWaitedOnSaysTheWait(t *testing.T) {
	t.Parallel()

	const item = "yoyodyne-ifd.429.68"
	decidedAt := time.Date(2026, 10, 10, 11, 8, 30, 0, time.UTC)
	stopped := preservedRun("run-5d62f75e", item, decidedAt.Add(-time.Hour))
	later := runstate.State{
		RunID:           "run-91d8485f",
		WorkItemID:      item,
		Status:          runstate.StatusFailed,
		StartedAt:       decidedAt.Add(time.Minute),
		UpdatedAt:       decidedAt.Add(3 * time.Minute),
		Branch:          "yoyodyne/" + item + "/91d8485f",
		WorktreePath:    "/state/worktrees/run-91d8485f",
		BranchRemoved:   true,
		WorktreeRemoved: true,
		Failure:         "harness create isolated worktree: bd export failed with status timed_out",
	}
	recorded := decisions(map[string]runstate.TriageCounters{
		item: {WorkItemID: item, Reruns: 1, Decisions: []runstate.TriageDecision{
			{Decision: runstate.TriageDecisionRerun, RunID: stopped.RunID, DecidedAt: decidedAt},
			{Decision: runstate.TriageDecisionWait, RunID: later.RunID, DecidedAt: decidedAt.Add(20 * time.Minute), WaitsOn: "yoyodyne-ifd.428.41"},
		}},
	})

	held := heldForAPerson([]runstate.State{stopped, later}, nil, recorded, asRecorded)
	reason := heldReason(t, held, item)
	if held.Decided(item) {
		t.Fatalf("held for %q as the harness's move, want the re-run read as carried out by %s", reason, later.RunID)
	}
	if !strings.Contains(reason, later.RunID) || !strings.Contains(reason, "wait on yoyodyne-ifd.428.41") {
		t.Fatalf("held for %q, want the run that carried the re-run out and the work she waits on named", reason)
	}
}
