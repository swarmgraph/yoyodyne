//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A run that keeps slot 2's endpoint pair, waiting on GitHub's checks while
// another run holds slot 2, is taken up in free slot 1 when it continues — the
// first free slot rather than its own — and is pinned to slot 1's own pair, so
// slot 1 never runs slot 2's pair. The move is recorded on the run: the slot it
// left and the slot it took, the earlier and the new pair with the
// configuration revision of each, when, and why. Its developer operation is
// kept as it stood. Without the slots' pairs handed over it still waits for
// its own slot, as before.
func TestARunKeepingItsSlotsPairIsMovedToAFreeSlotOnThatSlotsOwnPair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	waiting := pinnedTo(t, store, vanishedRun(t, store, "yoyodyne-ifd.429.127.2", 2), 2)
	waiting, err := store.UpdateRouting(ctx, waiting, func(r *RunRouting) (bool, error) {
		return r.OpenOperation(OperationRequest{ID: "op-develop", Kind: OperationDevelop, Budget: OperationBudget{RepairAttempts: intPointer(2), ReviewRounds: intPointer(3)}}, routingAt)
	})
	if err != nil {
		t.Fatalf("OpenOperation() error = %v", err)
	}
	waiting.CIWait = &CIWait{Commit: "afee2181", Branch: "b", PushedAt: time.Now().Add(-2 * time.Hour), Required: []string{"test"}}
	if err := store.Save(waiting); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	holder := liveInSlot(t, store, "yoyodyne-ifd.428.181", 2)
	before := load(t, store, waiting.RunID)

	if _, err := store.ReclaimSlot(ctx, before, 3); !errors.As(err, new(CapacityError)) {
		t.Fatalf("ReclaimSlot() with no slot pairs handed over error = %v, want it to wait for slot 2", err)
	}

	asked := 0
	pairs := func(state State, slot int) (RoutingSnapshot, error) {
		asked++
		if state.RunID != waiting.RunID {
			t.Errorf("resolved a pair for run %s, want %s", state.RunID, waiting.RunID)
		}
		return developerSnapshot(slot, "cfg-89abcdef"), nil
	}
	reclaimed, err := store.ReclaimSlot(WithSlotPairs(ctx, pairs), load(t, store, waiting.RunID), 3)
	if err != nil {
		t.Fatalf("ReclaimSlot() error = %v, want the run taken up in free slot 1", err)
	}
	if asked != 1 || reclaimed.RecordedSlot() != 1 || reclaimed.CIWait == nil || !reclaimed.CIWait.SlotTaken {
		t.Fatalf("taken up in slot %d (pairs asked %d times), wait %#v; want slot 1, asked once, the wait kept holding the slot", reclaimed.RecordedSlot(), asked, reclaimed.CIWait)
	}
	stored := load(t, store, waiting.RunID)
	routing := stored.Routing
	want := developerSnapshot(1, "cfg-89abcdef")
	if routing.Developer == nil || routing.Developer.Slot != 1 || routing.Developer.Digest != want.Digest || routing.Developer.Origin != RoutingReconfigured {
		t.Fatalf("pinned to %#v, want slot 1's own pair", routing.Developer)
	}
	if routing.Slot.MovedFrom != 2 || routing.Slot.Number != 1 || routing.Slot.RecordedAt.IsZero() {
		t.Fatalf("slot = %#v, want a move from slot 2 to slot 1 with its time", routing.Slot)
	}
	for _, says := range []string{"developer slot 2 was held by run " + holder.RunID, "free developer slot 1 ahead of new work", "slot 1's own endpoints", "configuration revision cfg-89abcdef", "instead of slot 2's", "configuration revision cfg-0123abcd"} {
		if !strings.Contains(routing.Slot.MovedBecause, says) {
			t.Fatalf("moved because %q, want it to say %q", routing.Slot.MovedBecause, says)
		}
	}
	last := routing.Reconfigurations[len(routing.Reconfigurations)-1]
	if last.FromDigest != before.Routing.Developer.Digest || last.ToDigest != want.Digest || last.FromConfig != "cfg-0123abcd" || last.ToConfig != "cfg-89abcdef" || last.Reason != routing.Slot.MovedBecause {
		t.Fatalf("reconfiguration = %#v, want the earlier and the new pair and the revision of each", last)
	}
	if len(routing.Operations) != len(before.Routing.Operations) || routing.Operations[0].ID != "op-develop" || routing.Operations[0].SnapshotDigest != before.Routing.Operations[0].SnapshotDigest {
		t.Fatalf("operations = %#v, want the developer operation kept as it stood", routing.Operations)
	}
	if says := stored.SlotMoveSays(); !strings.Contains(says, "moved from developer slot 2 to developer slot 1") {
		t.Fatalf("SlotMoveSays() = %q", says)
	}
}
