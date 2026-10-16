package orchestrator

// Moving a queued change between the harness's queue and the forge's: nothing
// moves while the old queue's merge may still land, the change keeps its place
// and its history, its candidate is verified afresh, and a transfer interrupted
// part-way is finished by asking again rather than made twice.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// interruptedTransfer stops the readmission a transfer ends with, as a
// process dying after the release and before the new entry is written would.
type interruptedTransfer struct {
	MergeQueueLandingRecords
	fail int
}

func (q *interruptedTransfer) Transfer(ctx context.Context, key runstate.MergeQueueKey, entryID string, mode runstate.MergeQueueMode, evidence runstate.MergeQueueModeEvidence, at time.Time) (runstate.MergeQueueEntry, bool, error) {
	if q.fail > 0 {
		q.fail--
		return runstate.MergeQueueEntry{}, false, errStopped
	}
	return q.MergeQueueLandingRecords.Transfer(ctx, key, entryID, mode, evidence, at)
}

func (f *promotionFixture) transfer(mode runstate.MergeQueueMode) (runstate.MergeQueueEntry, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return f.promoter.Transfer(ctx, queueKey, f.entry.EntryID, mode)
}

func TestATransferMovesAChangeOnlyOnceItsQueuedMergeIsConfirmedWithdrawn(t *testing.T) {
	t.Parallel()

	f, withdrawer, generation := queuedMergeFixture(t)
	behind := f.admitAnother(queueKey, "behind", "behind.txt", "behind\n")
	harness := queuemode.Harness{PullRequests: true, CheckConfiguration: "the project's checks"}
	f.promoter.Forge, f.promoter.Harness = &nativeQueue{capabilities: qualifyingQueue(harness)}, harness

	// The forge does not say whether it took the merge back: it may still land,
	// so nothing moves.
	withdrawer.answer = func(int) error { return errors.New("the forge did not answer") }
	var refused MergeQueueTransferRefusal
	if _, err := f.transfer(runstate.MergeQueueForge); !errors.As(err, &refused) {
		t.Fatalf("Transfer() while the withdrawal is uncertain = %v, want it refused", err)
	}
	if entries, _ := f.queue.Entries(queueKey); len(entries) != 2 || f.landing().Handback != nil {
		t.Fatalf("entries = %d, handback %#v; want nothing admitted or released while the merge may land", len(entries), f.landing().Handback)
	}

	// Confirmed, released, and then interrupted before the readmission.
	withdrawer.answer = nil
	landings := f.promoter.Queue
	f.promoter.Queue = &interruptedTransfer{MergeQueueLandingRecords: landings, fail: 1}
	if _, err := f.transfer(runstate.MergeQueueForge); !errors.Is(err, errStopped) {
		t.Fatalf("Transfer() = %v, want the interruption reported", err)
	}
	released := f.landing().Handback
	if released == nil || released.Continuation != runstate.MergeQueueReleased || released.TransferTo != runstate.MergeQueueForge {
		t.Fatalf("handback = %#v, want the entry released for the move", released)
	}
	requests := len(f.forge.MergeRequests())

	// Asking again finishes the same transfer, and asking a third time finds it
	// made rather than making a second.
	f.promoter.Queue = landings
	moved, err := f.transfer(runstate.MergeQueueForge)
	if err != nil || moved.Mode != runstate.MergeQueueForge || moved.Supersedes != f.entry.EntryID || moved.WorkPlace() != f.entry.WorkPlace() {
		t.Fatalf("Transfer() = %#v, %v; want the change in the forge's queue in the place it had", moved, err)
	}
	again, err := f.transfer(runstate.MergeQueueForge)
	if err != nil || again.EntryID != moved.EntryID {
		t.Fatalf("Transfer() again = %#v, %v; want the same entry", again, err)
	}
	entries, err := f.queue.Entries(queueKey)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %#v, %v; want the original, the one behind it, and the moved change", entries, err)
	}
	ordered := runstate.MergeQueueWorkOrder(entries)
	if ordered[1].EntryID != moved.EntryID || ordered[2].EntryID != behind.EntryID {
		t.Fatalf("work order = %v, %v, %v; want the moved change ahead of the one admitted after it", ordered[0].EntryID, ordered[1].EntryID, ordered[2].EntryID)
	}
	// The history stays where it was, and the moved change is verified afresh.
	if kept := f.generations(); len(kept) != 1 || kept[0].Binding() != generation.Binding() {
		t.Fatalf("the original entry's candidate = %#v, want it kept", kept)
	}
	if fresh, err := f.queue.Generations(queueKey, moved.EntryID); err != nil || len(fresh) != 0 {
		t.Fatalf("the moved entry's candidates = %#v, %v; want none until it is verified afresh", fresh, err)
	}
	if len(f.forge.MergeRequests()) != requests {
		t.Fatal("finishing the transfer asked the forge for a merge")
	}
}

func TestATransferToAQueueThatCannotBeUsedMovesNothing(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	harness := queuemode.Harness{PullRequests: true, CheckConfiguration: "the project's checks"}
	f.promoter.Forge, f.promoter.Harness = &nativeQueue{capabilities: headBoundQueue(harness)}, harness
	var refused MergeQueueTransferRefusal
	if _, err := f.transfer(runstate.MergeQueueForge); !errors.As(err, &refused) {
		t.Fatalf("Transfer() to a forge queue that approves only heads = %v, want it refused", err)
	}
	if _, err := f.transfer(runstate.MergeQueueHarness); !errors.As(err, &refused) {
		t.Fatalf("Transfer() to the queue it is already in = %v, want it refused", err)
	}
	if f.landing().Handback != nil {
		t.Fatal("a refused transfer released the entry")
	}
}
