package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run continued from a wait the forge has passed, and refused after it took
// its developer slot back, is put back to waiting rather than left recorded as
// running at its checks with nothing behind it; the next continuation then goes
// on to review and finishes. The refusal here is the item reading blocked, one
// of the refusals resumeRun makes only after the slot is taken.
func TestARefusedContinuationFromAPassedGitHubWaitGivesTheSlotBackAndTheNextGoesOnToReview(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)
	f.forge.setResult(publish.CheckPassed)

	claimed := f.tracker.Item.Status
	f.tracker.Item.Status = "blocked"
	_, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	var refused ItemStatusRefusal
	if !errors.As(err, &refused) {
		t.Fatalf("Run() error = %v, want the item's status refused after the slot was taken back", err)
	}
	state, err := f.store.Load(parked.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != runstate.StatusRunning || state.CIWait == nil || state.CIWait.SlotTaken {
		t.Fatalf("after the refusal status=%s wait=%#v, want the run still waiting on the forge with no slot taken", state.Status, state.CIWait)
	}
	if state.HoldsDeveloperSlot() || !state.AwaitsContinuation() {
		t.Fatal("the refused run still holds a developer slot, so no pull would continue it")
	}
	if state.ContinuationWait == nil || !strings.Contains(state.ContinuationWait.Why, "given back") || !strings.Contains(state.ContinuationWait.Why, "blocked") {
		t.Fatalf("continuation wait = %#v, want it to say the slot was given back and why the run was refused", state.ContinuationWait)
	}
	if f.reviewed() != 0 {
		t.Fatalf("review started %d times on a refused continuation", f.reviewed())
	}

	f.tracker.Item.Status = claimed
	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if outcome.Paused || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to review and finish", outcome)
	}
	if f.reviewed() != 1 || f.forge.pushed() != 1 {
		t.Fatalf("reviews=%d pushes=%d, want 1 and 1", f.reviewed(), f.forge.pushed())
	}
	finished, err := f.store.Load(parked.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.CIWaitEnd == nil || !finished.CIWaitEnd.Passed || finished.CIWaitEnd.Commit != parked.CIWait.Commit {
		t.Fatalf("wait ended as %#v, want it ended on the forge's pass for commit %s", finished.CIWaitEnd, parked.CIWait.Commit)
	}
}
