package runstate

import (
	"strings"
	"testing"
	"time"
)

func unfinishedAtItsLimit(elapsed, limit int64) State {
	return State{
		RunID: "run-" + strings.Repeat("a", 32), WorkItemID: "yoyodyne-task", Status: StatusTimedOut, Phase: PhaseChecking,
		WorktreePath: "/tmp/w", Branch: "b", BaseCommit: "c", TargetBranch: "main", ProviderSessionID: "session",
		HarnessCommit: "abc", RepairAttempts: 1,
		CheckStage: &CheckStage{StartedAt: time.Now(), BoundSeconds: 10800, Command: "make race",
			CheckStoppedAtLimit: true, CheckLimitSeconds: limit, CheckElapsedSeconds: elapsed},
	}
}

// The time allowance is fixed at three times the limit the check had when it
// first stopped, so a limit the load lengthened afterwards spends it sooner
// rather than buying more: here one rerun under a doubled limit reaches it
// with a rerun still uncounted.
func TestAnUnfinishedCheckStopsAtItsTimeAllowanceBeforeItsRerunCount(t *testing.T) {
	t.Parallel()
	state := unfinishedAtItsLimit(1800, 1800)
	stop, ok := state.UnfinishedCheck()
	if !ok || stop.AllowanceSeconds != 5400 || stop.Commit != "abc" || stop.Attempt != 1 || !stop.CheckLimit {
		t.Fatalf("UnfinishedCheck() = %#v, %t; want make race on attempt 1 at abc with a 90m allowance", stop, ok)
	}
	if !state.HarnessContinuesCheckStage() {
		t.Fatal("a first stop at the check's limit is not run again")
	}
	stop.ContinuedAt, stop.Reason = time.Now(), "rerun"
	state.CheckStageContinuations = []CheckStageContinuation{stop}
	if remaining, bounded := state.CheckRerunRemaining("make race"); !bounded || remaining != time.Hour {
		t.Fatalf("CheckRerunRemaining() = %s, %t; want an hour left", remaining, bounded)
	}
	state.CheckStage.CheckLimitSeconds, state.CheckStage.CheckElapsedSeconds = 3600, 3600
	if state.HarnessContinuesCheckStage() {
		t.Fatal("a check that spent its whole allowance is run again")
	}
	if spent := state.CheckRerunSpent(); !strings.Contains(spent, "allowance of 90m (3 times its 30m limit)") {
		t.Fatalf("CheckRerunSpent() = %q, want the allowance named", spent)
	}
	if says := state.CheckStageStopSays(); !strings.Contains(says, "did not finish") || !strings.Contains(says, "development manager's decision") {
		t.Fatalf("CheckStageStopSays() = %q", says)
	}
}

// Reruns of an earlier candidate do not count against this one, and a rerun
// written before reruns named their candidate counts against whatever
// candidate the run holds, so an upgrade cannot reset a bound.
func TestCheckRerunsCountOnlyAgainstTheirCandidate(t *testing.T) {
	t.Parallel()
	state := unfinishedAtItsLimit(1800, 1800)
	earlier := CheckStageContinuation{Command: "make race", ContinuedAt: time.Now(), Reason: "rerun", Attempt: 0, Commit: "old", ElapsedSeconds: 1800, LimitSeconds: 1800}
	state.CheckStageContinuations = []CheckStageContinuation{earlier, earlier}
	if len(state.CheckReruns()) != 0 || !state.HarnessContinuesCheckStage() {
		t.Fatalf("CheckReruns() = %#v, want none on this candidate", state.CheckReruns())
	}
	if err := state.Validate(); err != nil && strings.Contains(err.Error(), "check reruns") {
		t.Fatalf("Validate() = %v, want reruns of another candidate allowed", err)
	}
	legacy := CheckStageContinuation{Command: "make race", ContinuedAt: time.Now(), Reason: "continued"}
	state.CheckStageContinuations = []CheckStageContinuation{legacy, legacy}
	if len(state.CheckReruns()) != 2 || state.HarnessContinuesCheckStage() {
		t.Fatalf("CheckReruns() = %#v, want unnamed reruns counted against this candidate", state.CheckReruns())
	}
}
