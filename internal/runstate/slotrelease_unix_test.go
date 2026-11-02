//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// exitedPID is the pid of a process that has run and been reaped.
func exitedPID(t *testing.T) int {
	t.Helper()
	command := exec.Command("true")
	if err := command.Run(); err != nil {
		t.Fatalf("run a process to exit: %v", err)
	}
	return command.Process.Pid
}

// vanishedRun is a run in flight in developer slot number whose worker's
// process has exited: what a scheduler that restarted leaves behind it.
func vanishedRun(t *testing.T, store *Store, workItem string, number int) State {
	t.Helper()
	state := testState(t, StatusRunning)
	state.WorkItemID = workItem
	state.Worker = &WorkerIdentity{PID: exitedPID(t), StartedAt: state.StartedAt, ProgramStartedAt: state.StartedAt, RunID: state.RunID, Attempt: 1}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	claimed, err := store.ClaimSlot(context.Background(), state, number, 3, routingAt)
	if err != nil {
		t.Fatalf("ClaimSlot() error = %v", err)
	}
	return claimed
}

// claimRefused is whether a fresh run is refused developer slot number.
func claimRefused(t *testing.T, store *Store, number int) bool {
	t.Helper()
	next := testState(t, StatusRunning)
	next.WorkItemID = "yoyodyne-next"
	// Written just now, so nothing reads it as a run gone quiet.
	next.UpdatedAt = time.Now()
	if err := store.Create(next); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err := store.ClaimSlot(context.Background(), next, number, 3, routingAt)
	var occupied SlotOccupiedError
	if errors.As(err, &occupied) {
		return true
	}
	if err != nil {
		t.Fatalf("ClaimSlot() error = %v", err)
	}
	return false
}

// A slot number recorded by a run whose worker has vanished keeps nobody out
// of it, and the next release frees it, records on the run what it freed and
// why, and does so once.
func TestASlotClaimLeftByAVanishedWorkerIsFreed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	gone := vanishedRun(t, store, "yoyodyne-gone", 3)

	released, err := store.ReleaseSlotsWithNoLiveWorker(ctx, time.Now())
	if err != nil {
		t.Fatalf("ReleaseSlotsWithNoLiveWorker() error = %v", err)
	}
	if len(released) != 1 || released[0].RunID != gone.RunID || released[0].Release.Slot != 3 {
		t.Fatalf("released = %+v, want run %s's slot 3", released, gone.RunID)
	}
	stored := load(t, store, gone.RunID)
	if stored.SlotRelease == nil || stored.SlotRelease.WorkerAttempt != 1 {
		t.Fatalf("SlotRelease = %+v, want the release recorded against worker attempt 1", stored.SlotRelease)
	}
	for _, want := range []string{"freed developer slot 3", "has exited", "takes a slot again"} {
		if !strings.Contains(stored.SlotRelease.Says, want) {
			t.Errorf("SlotRelease.Says = %q, want it to say %q", stored.SlotRelease.Says, want)
		}
	}
	if stored.Worker == nil || stored.Worker.Attempt != 1 || stored.Worker.PID != gone.Worker.PID {
		t.Fatalf("Worker = %+v, want the vanished worker kept as the record's", stored.Worker)
	}
	if store.HoldsDeveloperSlotNow(stored, time.Now()) {
		t.Fatal("HoldsDeveloperSlotNow() = true for a released run")
	}
	if claimRefused(t, store, 3) {
		t.Fatal("ClaimSlot(3) was refused for a number only a released run records")
	}
	again, err := store.ReleaseSlotsWithNoLiveWorker(ctx, time.Now())
	if err != nil || len(again) != 0 {
		t.Fatalf("a second release = %+v, %v, want nothing released again", again, err)
	}
}

// The number is free to claim even before any release has run: a claim asks
// whether the run recording the number holds a slot now, the way the count
// does, so a pull that counted a slot free is never refused it.
func TestASlotNumberIsNotKeptByAVanishedWorkerBeforeItIsReleased(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	vanishedRun(t, store, "yoyodyne-gone", 2)
	if claimRefused(t, store, 2) {
		t.Fatal("ClaimSlot(2) was refused for a number only a run with no worker records")
	}
}

// A run recorded failed that still names a slot number holds nothing: it is
// not in flight, and the number is claimed beside it.
func TestAFailedRunWithARecordedSlotDoesNotKeepTheNumber(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	failed := vanishedRun(t, store, "yoyodyne-failed", 3)
	failed.Status, failed.Phase = StatusFailed, PhaseDeveloping
	completed := failed.StartedAt
	failed.CompletedAt = &completed
	if err := store.Save(failed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if claimRefused(t, store, 3) {
		t.Fatal("ClaimSlot(3) was refused for a number only a failed run records")
	}
	released, err := store.ReleaseSlotsWithNoLiveWorker(context.Background(), time.Now())
	if err != nil || len(released) != 0 {
		t.Fatalf("released = %+v, %v, want nothing: a failed run holds no slot to release", released, err)
	}
}

// A provider attempt left open by a vanished worker, whose own process has
// stopped, is ended as interrupted by the release, and the release says so.
func TestAProviderAttemptLeftByAVanishedWorkerIsEnded(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := vanishedRun(t, store, "yoyodyne-gone", 2)
	state = route(t, store, state, func(r *RunRouting) (bool, error) { return r.RecordSnapshot(developerSnapshot(2, "cfg-0123abcd")) })
	state = route(t, store, state, openDevelop("op-1"))
	route(t, store, state, prepare("op-1", "att-1"))
	f := launchFixture{store: store, base: stateRootOf(store), runID: state.RunID}
	f.recordExecution(t, "att-1", exitedPID(t))

	holders := store.SlotHolders([]State{load(t, store, state.RunID)}, time.Now())
	if len(holders) != 1 || holders[0].Attempt != "att-1" || holders[0].Counted {
		t.Fatalf("SlotHolders() = %+v, want att-1 named under a run not counted", holders)
	}

	released, err := store.ReleaseSlotsWithNoLiveWorker(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("ReleaseSlotsWithNoLiveWorker() error = %v", err)
	}
	if len(released) != 1 || len(released[0].Release.EndedAttempts) != 1 || released[0].Release.EndedAttempts[0] != "att-1" {
		t.Fatalf("released = %+v, want att-1 ended", released)
	}
	attempt := f.attempt(t, "att-1")
	if attempt.Ended == nil || attempt.Ended.Classification != InterruptedClassification || attempt.Ended.Termination != TerminationConfirmed {
		t.Fatalf("att-1 = %+v, want it ended as interrupted with its stop confirmed", attempt)
	}
	if says := load(t, store, state.RunID).SlotRelease.Says; !strings.Contains(says, "ended provider attempt att-1") {
		t.Fatalf("SlotRelease.Says = %q, want it to name the attempt it ended", says)
	}
	if holders := store.SlotHolders([]State{load(t, store, state.RunID)}, time.Now()); len(holders) != 1 || holders[0].Attempt != "" {
		t.Fatalf("SlotHolders() after the release = %+v, want no attempt left executing", holders)
	}
}

// A run whose worker is alive and holds its lease is left exactly as it is,
// and keeps its number.
func TestAClaimWithALiveWorkerIsLeftAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	live := testState(t, StatusPending)
	live.WorkItemID = "yoyodyne-live"
	lease, err := store.Reserve(ctx, live, 3)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	if _, err := store.ClaimSlot(ctx, load(t, store, live.RunID), 1, 3, routingAt); err != nil {
		t.Fatalf("ClaimSlot() error = %v", err)
	}

	released, err := store.ReleaseSlotsWithNoLiveWorker(ctx, time.Now())
	if err != nil || len(released) != 0 {
		t.Fatalf("released = %+v, %v, want nothing released under a live worker", released, err)
	}
	if stored := load(t, store, live.RunID); stored.SlotRelease != nil {
		t.Fatalf("SlotRelease = %+v on a run with a live worker", stored.SlotRelease)
	}
	if !claimRefused(t, store, 1) {
		t.Fatal("ClaimSlot(1) took the number a live run holds")
	}
	holders := store.SlotHolders([]State{load(t, store, live.RunID)}, time.Now())
	if len(holders) != 1 || holders[0].Slot != 1 || !holders[0].Counted || !strings.HasPrefix(holders[0].Says, "counted: ") {
		t.Fatalf("SlotHolders() = %+v, want the live run counted in slot 1", holders)
	}
}

// A run whose worker can be shown neither alive nor gone keeps its slot while
// anything has moved it recently, and is released once nothing has held or
// written it for UncertainWorkerQuiet.
func TestAWorkerNothingCanShowEitherWayIsReleasedOnlyOnceTheRunHasGoneQuiet(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	// Recorded by a build that wrote down no worker identity.
	quiet := testState(t, StatusRunning)
	quiet.WorkItemID = "yoyodyne-quiet"
	if err := store.Create(quiet); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	recent := testState(t, StatusRunning)
	recent.WorkItemID = "yoyodyne-recent"
	recent.UpdatedAt = time.Now()
	if err := store.Create(recent); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	released, err := store.ReleaseSlotsWithNoLiveWorker(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("ReleaseSlotsWithNoLiveWorker() error = %v", err)
	}
	if len(released) != 1 || released[0].RunID != quiet.RunID {
		t.Fatalf("released = %+v, want only the quiet run %s", released, quiet.RunID)
	}
	if says := released[0].Release.Says; !strings.Contains(says, "cannot be shown") || !strings.Contains(says, "nothing has been written to it since") {
		t.Fatalf("Says = %q, want it to say why the worker counts as absent", says)
	}
	if !store.HoldsDeveloperSlotNow(load(t, store, recent.RunID), time.Now()) {
		t.Fatal("the recently written run lost its slot")
	}
}

// A released run taken up again by a new worker attempt holds a slot again.
func TestAReleasedRunTakenUpAgainHoldsASlot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	gone := vanishedRun(t, store, "yoyodyne-gone", 1)
	if _, err := store.ReleaseSlotsWithNoLiveWorker(ctx, time.Now()); err != nil {
		t.Fatalf("ReleaseSlotsWithNoLiveWorker() error = %v", err)
	}
	adopted, lease, err := store.AdoptRun(ctx, gone.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	if err := store.Save(adopted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	stored := load(t, store, gone.RunID)
	if stored.Worker == nil || stored.Worker.Attempt != 2 {
		t.Fatalf("Worker = %+v, want the new attempt recorded", stored.Worker)
	}
	if !store.HoldsDeveloperSlotNow(stored, time.Now()) {
		t.Fatal("HoldsDeveloperSlotNow() = false for a released run a live worker took up again")
	}
}

// liveInSlot is a run whose worker is this live process, holding its lease, in
// developer slot number.
func liveInSlot(t *testing.T, store *Store, workItem string, number int) State {
	t.Helper()
	ctx := context.Background()
	live := testState(t, StatusPending)
	live.WorkItemID = workItem
	lease, err := store.Reserve(ctx, live, 3)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	claimed, err := store.ClaimSlot(ctx, load(t, store, live.RunID), number, 3, routingAt)
	if err != nil {
		t.Fatalf("ClaimSlot() error = %v", err)
	}
	return claimed
}

// pinnedTo gives a run recorded in a slot the endpoint pair that slot is
// configured with, as a run reserved for a routed slot records it.
func pinnedTo(t *testing.T, store *Store, state State, number int) State {
	t.Helper()
	pinned, err := store.UpdateRouting(context.Background(), state, func(routing *RunRouting) (bool, error) {
		return routing.RecordSnapshot(developerSnapshot(number, "cfg-0123abcd"))
	})
	if err != nil {
		t.Fatalf("UpdateRouting() error = %v", err)
	}
	return pinned
}

// A released run whose slot number another run claimed meanwhile, and which
// keeps that slot's endpoint pair, is not taken up again on that number or on
// any other: it waits for the slot, saying which run holds it and why it waits,
// rather than run beside it on the same slot's endpoints. Once the number is
// free again it is taken up.
func TestAReleasedRunWhoseNumberWasClaimedWaitsRatherThanShareIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	pinnedTo(t, store, vanishedRun(t, store, "yoyodyne-gone", 1), 1)
	if _, err := store.ReleaseSlotsWithNoLiveWorker(ctx, time.Now()); err != nil {
		t.Fatalf("ReleaseSlotsWithNoLiveWorker() error = %v", err)
	}
	other := liveInSlot(t, store, "yoyodyne-other", 1)

	_, lease, err := store.AdoptWithin(ctx, "yoyodyne-gone", 3)
	var capacity CapacityError
	if !errors.As(err, &capacity) || capacity.Slot != 1 || capacity.HeldBy != other.RunID || !capacity.Pinned {
		if lease != nil {
			_ = lease.Release()
		}
		t.Fatalf("AdoptWithin() error = %v, want it to wait for slot 1, which run %s holds", err, other.RunID)
	}
	if says := err.Error(); !strings.Contains(says, "developer slot 1") || !strings.Contains(says, other.RunID) || !strings.Contains(says, "endpoint pair") || strings.Contains(says, "active run") {
		t.Fatalf("AdoptWithin() error = %q, want it to name the slot, the run holding it, and why this run waits for it, and no count of active runs", says)
	}

	moved := load(t, store, other.RunID)
	moved.Status = StatusSucceeded
	completed := time.Now()
	moved.CompletedAt = &completed
	if err := store.Save(moved); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	_, lease, err = store.AdoptWithin(ctx, "yoyodyne-gone", 3)
	if err != nil {
		t.Fatalf("AdoptWithin() once the number is free again error = %v", err)
	}
	_ = lease.Release()
}

// A released run whose slot number another run claimed meanwhile, and which
// keeps no endpoint pair — its slot has no routing, or is kept only for a
// label — is taken up again in the lowest free slot and records it, so it does
// not wait while another slot is free. Told no capacity, Adopt cannot choose a
// slot within it and leaves the run waiting for its own.
func TestAReleasedRunWhoseNumberWasClaimedIsTakenUpInAFreeSlot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	gone := vanishedRun(t, store, "yoyodyne-gone", 3)
	if _, err := store.ReleaseSlotsWithNoLiveWorker(ctx, time.Now()); err != nil {
		t.Fatalf("ReleaseSlotsWithNoLiveWorker() error = %v", err)
	}
	occupant := liveInSlot(t, store, "yoyodyne-other", 3)
	liveInSlot(t, store, "yoyodyne-first", 1)

	if _, lease, err := store.Adopt(ctx, "yoyodyne-gone"); err == nil {
		_ = lease.Release()
		t.Fatal("Adopt() told no capacity took the run up in a slot it cannot know is within it")
	}
	adopted, lease, err := store.AdoptWithin(ctx, "yoyodyne-gone", 3)
	if err != nil {
		t.Fatalf("AdoptWithin() error = %v, want the run taken up in free slot 2", err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	if adopted.RecordedSlot() != 2 || adopted.Routing.Slot.MovedFrom != 3 {
		t.Fatalf("taken up in slot %d (moved from %d), want slot 2, moved from 3", adopted.RecordedSlot(), adopted.Routing.Slot.MovedFrom)
	}
	if because := adopted.Routing.Slot.MovedBecause; !strings.Contains(because, "developer slot 3 was held by run "+occupant.RunID) || !strings.Contains(because, "endpoint pair") {
		t.Fatalf("recorded the move because %q, want it to name slot 3, the run holding it, and that this run keeps no endpoint pair", because)
	}
	if says := adopted.SlotMoveSays(); !strings.Contains(says, "moved from developer slot 3 to developer slot 2") || !strings.Contains(says, occupant.RunID) {
		t.Fatalf("SlotMoveSays() = %q, want the slot left, the slot taken, and why", says)
	}
	if !adopted.SlotMoveUnrecorded() {
		t.Fatal("SlotMoveUnrecorded() = false for a move no event log has said yet")
	}
	if stored := load(t, store, gone.RunID); stored.RecordedSlot() != 2 {
		t.Fatalf("the record names slot %d, want the slot the run was taken up in", stored.RecordedSlot())
	}
	if err := store.Save(adopted); err != nil {
		t.Fatalf("Save() of the adopted run error = %v, want its copy to hold the routing written", err)
	}
}

// A stopped run made live again by a repair or continuation, whose slot
// number another run holds and which keeps no endpoint pair, is made live in a
// free slot; one that keeps its slot's pair waits for that slot.
func TestARunMadeLiveAgainTakesAFreeSlotUnlessItKeepsItsPair(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		pinned bool
	}{
		{name: "no endpoint pair"},
		{name: "keeps its slot's endpoint pair", pinned: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := newTestStore(t)
			stopped := testState(t, StatusFailed)
			stopped.WorkItemID = "yoyodyne-stopped"
			completed := time.Now()
			stopped.CompletedAt = &completed
			if err := store.Create(stopped); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			stopped, err := store.ClaimSlot(ctx, stopped, 3, 3, routingAt)
			if err != nil {
				t.Fatalf("ClaimSlot() error = %v", err)
			}
			if test.pinned {
				stopped = pinnedTo(t, store, stopped, 3)
			}
			other := liveInSlot(t, store, "yoyodyne-other", 3)

			live := stopped
			live.Status = StatusRunning
			live.CompletedAt = nil
			err = store.ReserveLive(ctx, live, 3)
			stored := load(t, store, stopped.RunID)
			if test.pinned {
				var capacity CapacityError
				if !errors.As(err, &capacity) || capacity.Slot != 3 || capacity.HeldBy != other.RunID || !capacity.Pinned {
					t.Fatalf("ReserveLive() error = %v, want it to wait for slot 3, which run %s holds", err, other.RunID)
				}
				if stored.Status != StatusFailed || stored.RecordedSlot() != 3 {
					t.Fatalf("the refused run is %s in slot %d, want it left as it was", stored.Status, stored.RecordedSlot())
				}
				return
			}
			if err != nil {
				t.Fatalf("ReserveLive() error = %v, want the run made live in a free slot", err)
			}
			if stored.Status != StatusRunning || stored.RecordedSlot() != 1 || stored.Routing.Slot.MovedFrom != 3 {
				t.Fatalf("the run is %s in slot %d (moved from %d), want it running in slot 1, moved from 3", stored.Status, stored.RecordedSlot(), stored.Routing.Slot.MovedFrom)
			}
		})
	}
}

// A run paused on work its item waits on keeps nobody out of its number, so a
// continuation whose number another run now holds, and which keeps that slot's
// endpoint pair, waits for it the same way.
func TestAPausedRunWhoseNumberWasClaimedWaitsToContinue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	paused := pinnedTo(t, store, vanishedRun(t, store, "yoyodyne-paused", 2), 2)
	paused.DependencyPause = &DependencyPause{Blockers: []string{"yoyodyne-z"}}
	if err := store.Save(paused); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	other := liveInSlot(t, store, "yoyodyne-other", 2)

	_, err := store.ReclaimSlot(ctx, load(t, store, paused.RunID), 3)
	var capacity CapacityError
	if !errors.As(err, &capacity) || capacity.Slot != 2 || capacity.HeldBy != other.RunID {
		t.Fatalf("ReclaimSlot() error = %v, want it to wait for slot 2, which run %s holds", err, other.RunID)
	}
	if stored := load(t, store, paused.RunID); stored.DependencyPause == nil {
		t.Fatal("the refused continuation lifted the pause")
	}
}

// The same continuation, where the paused run keeps no endpoint pair, goes on
// in a free slot and records it.
func TestAPausedRunWhoseNumberWasClaimedContinuesInAFreeSlot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	paused := vanishedRun(t, store, "yoyodyne-paused", 2)
	paused.DependencyPause = &DependencyPause{Blockers: []string{"yoyodyne-z"}}
	if err := store.Save(paused); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	liveInSlot(t, store, "yoyodyne-other", 2)

	reclaimed, err := store.ReclaimSlot(ctx, load(t, store, paused.RunID), 3)
	if err != nil {
		t.Fatalf("ReclaimSlot() error = %v, want the continuation to go on in a free slot", err)
	}
	if reclaimed.RecordedSlot() != 1 || reclaimed.DependencyPause != nil {
		t.Fatalf("continued in slot %d with pause %+v, want slot 1 and the pause lifted", reclaimed.RecordedSlot(), reclaimed.DependencyPause)
	}
	if stored := load(t, store, paused.RunID); stored.RecordedSlot() != 1 || stored.DependencyPause != nil {
		t.Fatalf("the record names slot %d with pause %+v, want slot 1 and the pause lifted", stored.RecordedSlot(), stored.DependencyPause)
	}
}
