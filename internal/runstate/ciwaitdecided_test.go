package runstate

import (
	"strings"
	"testing"
	"time"
)

// GitHub's decision on a waited commit is recorded once, on the wait that names
// that commit, and recording it leaves the wait and the run's updated time as
// they were.
func TestCIWaitDecidedRecordsTheFirstDecisionOnTheWaitedCommitOnly(t *testing.T) {
	at := time.Date(2026, 11, 20, 22, 58, 0, 0, time.UTC)
	updated := at.Add(-time.Hour)
	state := State{RunID: "run-e63fbad1", Status: StatusRunning, UpdatedAt: updated,
		CIWait: &CIWait{Commit: "afee2181", Branch: "b", PushedAt: at.Add(-time.Hour), Required: []string{"test"}}}

	if _, changed, err := ApplyUpdate(state, CIWaitDecided{Commit: "b197f2e5", Decision: CIWaitDecision{At: at, Says: "test ended failed on the forge"}}); err != nil || changed {
		t.Fatalf("decision on another commit changed = %v, err = %v; want nothing recorded", changed, err)
	}
	next, changed, err := ApplyUpdate(state, CIWaitDecided{Commit: "afee2181", Decision: CIWaitDecision{At: at, Passed: true, Says: "every required check passed on the commit pushed"}})
	if err != nil || !changed || next.CIWait.Decided == nil || !next.CIWait.Decided.Passed || !next.UpdatedAt.Equal(updated) || next.CIWait.Commit != "afee2181" {
		t.Fatalf("recorded = %#v, changed = %v, err = %v", next.CIWait, changed, err)
	}
	if state.CIWait.Decided != nil {
		t.Fatalf("the copy handed in was changed: %#v", state.CIWait)
	}
	if err := next.CIWait.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if says := next.CIWait.DecidedSays(); !strings.HasPrefix(says, "every required check passed on the commit pushed (read at ") {
		t.Fatalf("DecidedSays() = %q", says)
	}
	if _, changed, _ := ApplyUpdate(next, CIWaitDecided{Commit: "afee2181", Decision: CIWaitDecision{At: at.Add(time.Minute), Says: "later"}}); changed {
		t.Fatalf("a second decision replaced the first")
	}
	if _, _, err := ApplyUpdate(state, CIWaitDecided{Commit: "afee2181", Decision: CIWaitDecision{At: at}}); err == nil {
		t.Fatalf("a decision that says nothing was recorded")
	}
}

// What a development manager's outstanding continuation waits on is written by
// a writer that does not hold the run, once for the same words, and never on a
// request already taken up.
func TestRecoveryContinuationWaitedWritesOnAnOutstandingRequestOnce(t *testing.T) {
	at := time.Date(2026, 11, 20, 22, 58, 0, 0, time.UTC)
	state := State{RunID: "run-e63fbad1", Status: StatusRunning,
		RecoveryContinuation: &RecoveryContinuation{RequestID: "dm-1120-1427-429127", Action: RecoverContinue, Reason: "r", RequestedAt: at.Add(-time.Hour)}}
	why := "developer slot 2 is held by run run-a15c973e"

	next, changed, err := ApplyUpdate(state, RecoveryContinuationWaited{Action: RecoverContinue, Why: why, At: at})
	if err != nil || !changed || next.RecoveryContinuation.Waiting != why || !next.RecoveryContinuation.WaitingSince.Equal(at) {
		t.Fatalf("waited = %#v, changed = %v, err = %v", next.RecoveryContinuation, changed, err)
	}
	if _, changed, _ := ApplyUpdate(next, RecoveryContinuationWaited{Action: RecoverContinue, Why: why, At: at.Add(time.Minute)}); changed {
		t.Fatalf("the same wait was written again")
	}
	taken := next
	taken.TakeUpRecoveryContinuation(RecoverContinue, at)
	if _, changed, _ := ApplyUpdate(taken, RecoveryContinuationWaited{Action: RecoverContinue, Why: "something else", At: at}); changed {
		t.Fatalf("a wait was written on a request already taken up")
	}
}
