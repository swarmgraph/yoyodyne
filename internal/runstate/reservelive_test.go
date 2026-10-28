package runstate

import (
	"context"
	"errors"
	"testing"
)

// stoppedAndLive is a run that stopped, recorded in store, and the record a
// recovery path builds to make it live again.
func stoppedAndLive(t *testing.T, store *Store, workItemID string) (State, State) {
	t.Helper()
	stopped := testState(t, StatusFailed)
	stopped.WorkItemID = workItemID
	if err := store.Create(stopped); err != nil {
		t.Fatalf("Create() stopped run error = %v", err)
	}
	live := stopped
	live.Status = StatusRunning
	live.Phase = PhaseIntegrating
	live.CompletedAt = nil
	return stopped, live
}

// The interleaving the recovery paths used to allow, played in order: the
// recovery reads a slot free, a fresh start takes it, and the recovery then
// makes its run live. Making it live is where the slot is taken, so it is
// refused and writes nothing, rather than running two developers in one slot.
func TestARecoveryThatReadASlotFreeIsRefusedWhenAFreshStartTookItFirst(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	stopped, live := stoppedAndLive(t, store, "yoyodyne-stopped")

	active, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if HoldingDeveloperSlots(active) != 0 {
		t.Fatalf("the recovery's reading found %d slot(s) held, want the one slot free", HoldingDeveloperSlots(active))
	}

	fresh := testState(t, StatusPending)
	fresh.WorkItemID = "yoyodyne-fresh"
	lease, err := store.Reserve(ctx, fresh, 1)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	defer lease.Release()

	var capacity CapacityError
	if err := store.ReserveLive(ctx, live, 1); !errors.As(err, &capacity) || capacity.Active != 1 || capacity.Limit != 1 {
		t.Fatalf("ReserveLive() after the slot was taken error = %v, want the reservation's CapacityError", err)
	}
	loaded, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Status != StatusFailed || loaded.CompletedAt == nil {
		t.Fatalf("refused run = %s, want it left stopped as it was recorded", loaded.Status)
	}
	active, err = store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if held := HoldingDeveloperSlots(active); held != 1 {
		t.Fatalf("developer slots held = %d, want exactly the fresh run's", held)
	}
}

// A fresh start and a recovery race, from two store instances as two processes
// would, for the one free slot. Whichever reaches the reservation first takes
// it and the other is refused for capacity; neither order leaves two runs in
// one slot.
func TestAFreshStartAndARecoveryRacingForTheLastSlotLeaveExactlyOneHoldingIt(t *testing.T) {
	t.Parallel()

	for round := range 20 {
		root := t.TempDir()
		first, err := NewStore(root, "yoyodyne")
		if err != nil {
			t.Fatalf("NewStore() first error = %v", err)
		}
		second, err := NewStore(root, "yoyodyne")
		if err != nil {
			t.Fatalf("NewStore() second error = %v", err)
		}
		ctx := context.Background()
		_, live := stoppedAndLive(t, first, "yoyodyne-stopped")
		fresh := testState(t, StatusPending)
		fresh.WorkItemID = "yoyodyne-fresh"

		// The lock is held while both callers start, so neither has read the
		// slots before the other is waiting on the same lock.
		release, err := first.lockReservations(ctx)
		if err != nil {
			t.Fatalf("lockReservations() error = %v", err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var leases = make(chan *Lease, 1)
		go func() {
			<-start
			lease, err := first.Reserve(ctx, fresh, 1)
			leases <- lease
			results <- err
		}()
		go func() {
			<-start
			results <- second.ReserveLive(ctx, live, 1)
		}()
		close(start)
		release()

		var took, refused int
		for range 2 {
			err := <-results
			var capacity CapacityError
			switch {
			case err == nil:
				took++
			case errors.As(err, &capacity):
				refused++
			default:
				t.Fatalf("round %d: unexpected error = %v", round, err)
			}
		}
		if lease := <-leases; lease != nil {
			defer lease.Release()
		}
		if took != 1 || refused != 1 {
			t.Fatalf("round %d: took = %d, refused = %d, want exactly one start to get the slot", round, took, refused)
		}
		active, err := first.Incomplete()
		if err != nil {
			t.Fatalf("Incomplete() error = %v", err)
		}
		if held := HoldingDeveloperSlots(active); held != 1 {
			t.Fatalf("round %d: developer slots held = %d, want exactly one", round, held)
		}
	}
}

// A record already live is saved again without being counted against itself,
// and a record that holds no developer slot is saved under the lock without a
// slot being asked for.
func TestReserveLiveCountsEveryOtherRunAndNeverTheRunItself(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	_, live := stoppedAndLive(t, store, "yoyodyne-stopped")
	if err := store.ReserveLive(ctx, live, 1); err != nil {
		t.Fatalf("ReserveLive() with the slot free error = %v", err)
	}
	if err := store.ReserveLive(ctx, live, 1); err != nil {
		t.Fatalf("ReserveLive() of the run already holding the slot error = %v, want it saved again", err)
	}
	stopped := live
	stopped.Status = StatusFailed
	if err := store.ReserveLive(ctx, stopped, 1); err == nil {
		t.Fatal("ReserveLive() of a stopped record succeeded, want it refused as not live")
	}

	_, paused := stoppedAndLive(t, store, "yoyodyne-paused")
	paused.DependencyPause = &DependencyPause{Blockers: []string{"yoyodyne-blocker"}}
	if err := store.ReserveLive(ctx, paused, 1); err != nil {
		t.Fatalf("ReserveLive() of a run holding no slot on a full harness error = %v, want it saved", err)
	}
}

// Making a stopped run live is taking a developer slot, so a plain Save that
// would do it is refused and writes nothing: the next path that reaches for Save
// cannot reopen the race ReserveLive closes. A run that holds no slot, or one
// already in flight, is saved as before.
func TestAPlainSaveCannotMakeAStoppedRunLive(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	stopped, live := stoppedAndLive(t, store, "yoyodyne-stopped")
	if err := store.Save(live); !errors.Is(err, ErrMadeLiveWithoutASlot) {
		t.Fatalf("Save() of a stopped run made live error = %v, want ErrMadeLiveWithoutASlot", err)
	}
	loaded, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Status != StatusFailed {
		t.Fatalf("refused save left the run %s, want it stopped", loaded.Status)
	}

	paused := live
	paused.DependencyPause = &DependencyPause{Blockers: []string{"yoyodyne-blocker"}}
	if err := store.Save(paused); err != nil {
		t.Fatalf("Save() of a run made live holding no slot error = %v", err)
	}

	if err := store.ReserveLive(ctx, live, 1); err != nil {
		t.Fatalf("ReserveLive() error = %v", err)
	}
	live.Phase = PhaseChecking
	if err := store.Save(live); err != nil {
		t.Fatalf("Save() of a run already in flight error = %v, want it saved", err)
	}
}

// No limit configured is a refusal of its own, not a full harness: a caller that
// tells an item another run took the last slot must never be told so here.
func TestReserveLiveWithNoLimitIsNotACapacityRefusal(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	_, live := stoppedAndLive(t, store, "yoyodyne-stopped")
	err := store.ReserveLive(context.Background(), live, 0)
	var capacity CapacityError
	if err == nil || errors.As(err, &capacity) {
		t.Fatalf("ReserveLive() with no limit error = %v, want a plain refusal", err)
	}
}
