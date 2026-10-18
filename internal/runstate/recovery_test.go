package runstate

import (
	"testing"
	"time"
)

// A wait is written once in the same words, only on an outstanding request, and
// cleared when the request is taken up; a record carrying a wait on a request
// that was answered is refused.
func TestARecoveryContinuationWaitIsWrittenOnceAndClearedWhenTakenUp(t *testing.T) {
	t.Parallel()

	asked := time.Date(2026, 10, 17, 18, 56, 0, 0, time.UTC)
	state := State{RecoveryContinuation: &RecoveryContinuation{RequestID: "dm-finish", Action: RecoverFinishMerge, RequestedAt: asked}}
	if !state.WaitRecoveryContinuation(RecoverFinishMerge, "every developer slot is occupied: 3 active, limit 3", asked.Add(time.Minute)) {
		t.Fatal("the first wait was not written")
	}
	if state.WaitRecoveryContinuation(RecoverFinishMerge, "every developer slot is occupied: 3 active, limit 3", asked.Add(2*time.Minute)) {
		t.Fatal("the same wait was written twice")
	}
	if since := state.RecoveryContinuation.WaitingSince; since == nil || !since.Equal(asked.Add(time.Minute)) {
		t.Fatalf("waiting since %v, want the time it was first written", since)
	}
	if state.WaitRecoveryContinuation(RecoverContinue, "something else", asked.Add(3*time.Minute)) {
		t.Fatal("a wait was written onto a request for another action")
	}
	if err := state.RecoveryContinuation.Validate(); err != nil {
		t.Fatalf("Validate() of a waiting request = %v", err)
	}

	if !state.TakeUpRecoveryContinuation(RecoverFinishMerge, asked.Add(time.Hour)) {
		t.Fatal("the request was not taken up")
	}
	if taken := state.RecoveryContinuation; taken.Waiting != "" || taken.WaitingSince != nil {
		t.Fatalf("taken up request still waits: %#v", taken)
	}

	answered := *state.RecoveryContinuation
	since := asked
	answered.Waiting, answered.WaitingSince = "every developer slot is occupied", &since
	if err := answered.Validate(); err == nil {
		t.Fatal("Validate() accepted a wait on a request already taken up")
	}
}
