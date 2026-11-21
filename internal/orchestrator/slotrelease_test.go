package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// releasingRuns is a run store that frees the slots of the runs the test names
// as having no live worker when a pull asks it to, and from then on counts
// them free. A pull reads it from more than one goroutine, so what it frees is
// kept under its own lock, and the harness's runs are read under the harness's.
type releasingRuns struct {
	*scheduleHarness
	vanished map[string]bool
	freed    *freedRuns
}

type freedRuns struct {
	sync.Mutex
	runs  map[string]bool
	asked int
}

func (r releasingRuns) HoldsDeveloperSlotNow(state runstate.State, _ time.Time) bool {
	r.freed.Lock()
	defer r.freed.Unlock()
	return state.HoldsDeveloperSlot() && !r.freed.runs[state.RunID]
}

func (r releasingRuns) ReleaseSlotsWithNoLiveWorker(context.Context, time.Time) ([]runstate.ReleasedSlot, error) {
	states, err := r.Incomplete()
	if err != nil {
		return nil, err
	}
	r.freed.Lock()
	defer r.freed.Unlock()
	r.freed.asked++
	var released []runstate.ReleasedSlot
	for _, state := range states {
		if r.vanished[state.RunID] && !r.freed.runs[state.RunID] {
			r.freed.runs[state.RunID] = true
			released = append(released, runstate.ReleasedSlot{RunID: state.RunID, WorkItemID: state.WorkItemID,
				Release: runstate.SlotRelease{Slot: 1, Says: "The harness freed developer slot 1."}})
		}
	}
	return released, nil
}

// The first pull a scheduler makes as it starts frees the slot of a run whose
// worker vanished before it counts what is free, so ready work starts in it
// and the pull says what it freed. A run whose worker is alive is left in its
// slot, and the slot is counted full.
func TestAPullFreesTheSlotOfARunWithNoLiveWorkerBeforeCountingTheSlots(t *testing.T) {
	t.Parallel()

	held := "run-aaaabbbbccccddddeeeeffff00003333"
	for _, test := range []struct {
		name     string
		vanished bool
	}{
		{name: "its worker vanished", vanished: true},
		{name: "its worker is alive", vanished: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
			harness.capacity = 1
			harness.inFlight["yoyodyne-ifd.400"] = runningState(held, "yoyodyne-ifd.400")
			freed := &freedRuns{runs: map[string]bool{}}
			runs := releasingRuns{scheduleHarness: harness, vanished: map[string]bool{held: test.vanished}, freed: freed}
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.Runs = runs
				return pull, err
			}
			schedule, err := (Scheduler{Open: open}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			freed.Lock()
			asked := freed.asked
			freed.Unlock()
			if asked == 0 {
				t.Fatal("the pull never asked the run store to free slots with no live worker")
			}
			started := false
			for _, run := range schedule.Started {
				started = started || run.WorkItemID == "yoyodyne-ifd.500"
			}
			if started != test.vanished {
				t.Fatalf("started = %#v, stopped = %q; want the ready item started %t", schedule.Started, schedule.Stopped, test.vanished)
			}
			if test.vanished && (len(schedule.ReleasedSlots) != 1 || schedule.ReleasedSlots[0].RunID != held) {
				t.Fatalf("ReleasedSlots = %#v, want the vanished worker's run named", schedule.ReleasedSlots)
			}
			if !test.vanished && (len(schedule.ReleasedSlots) != 0 || schedule.Stopped != ScheduleCapacityFull) {
				t.Fatalf("ReleasedSlots = %#v, stopped = %q; want nothing freed and the live run's slot counted full", schedule.ReleasedSlots, schedule.Stopped)
			}
		})
	}
}

// storeRuns is the schedule harness with its runs read from a real run store,
// so the pull frees slots by the store's own rule.
type storeRuns struct {
	*scheduleHarness
	store *runstate.Store
}

func (r storeRuns) Incomplete() ([]runstate.State, error) { return r.store.Incomplete() }

func (r storeRuns) HoldsDeveloperSlotNow(state runstate.State, now time.Time) bool {
	return r.store.HoldsDeveloperSlotNow(state, now)
}

func (r storeRuns) ReleaseSlotsWithNoLiveWorker(ctx context.Context, now time.Time) ([]runstate.ReleasedSlot, error) {
	return r.store.ReleaseSlotsWithNoLiveWorker(ctx, now)
}

// slotReleaseNotesKept keeps every note a pull appends to a work item.
type slotReleaseNotesKept struct {
	sync.Mutex
	byItem map[string][]string
}

func (n *slotReleaseNotesKept) RecordOutcome(_ context.Context, id, notes string) (beads.WorkItem, error) {
	n.Lock()
	defer n.Unlock()
	n.byItem[id] = append(n.byItem[id], notes)
	return beads.WorkItem{ID: id}, nil
}

// A continuation refused after it made its run live leaves the run in flight,
// naming this very scheduler's program as its worker, with nothing holding its
// lease. The next pull gives that slot back while the scheduler keeps running,
// records it on the run and on the item, and starts ready work in it; the run
// stays in flight for a repair or continuation to take up. A run whose worker
// holds its lease keeps its slot, and the slot is counted full.
func TestAPullGivesBackTheSlotOfARefusedContinuationWhileTheSchedulerRuns(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		held bool
	}{
		{name: "nothing holds the run's lease"},
		{name: "the run's worker holds its lease", held: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatalf("runstate.NewStore() error = %v", err)
			}
			runID, err := runstate.NewRunID()
			if err != nil {
				t.Fatalf("NewRunID() error = %v", err)
			}
			continued := runningState(runID, "yoyodyne-ifd.400")
			continued.Status = runstate.StatusPending
			continued.UpdatedAt = time.Now().Add(-runstate.UnheldRunQuiet - time.Minute)
			lease, err := store.Reserve(ctx, continued, 1)
			if err != nil {
				t.Fatalf("Reserve() error = %v", err)
			}
			if test.held {
				t.Cleanup(func() { _ = lease.Release() })
			} else if err := lease.Release(); err != nil {
				t.Fatalf("Release() error = %v", err)
			}

			harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
			harness.capacity = 1
			runs := storeRuns{scheduleHarness: harness, store: store}
			notes := &slotReleaseNotesKept{byItem: map[string][]string{}}
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.Runs = runs
				pull.SlotReleaseNotes = notes
				return pull, err
			}
			schedule, err := (Scheduler{Open: open}).Schedule(ctx)
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			started := false
			for _, run := range schedule.Started {
				started = started || run.WorkItemID == "yoyodyne-ifd.500"
			}
			stored, err := store.Load(runID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			notes.Lock()
			said := notes.byItem["yoyodyne-ifd.400"]
			notes.Unlock()
			if test.held {
				if started || len(schedule.ReleasedSlots) != 0 || schedule.Stopped != ScheduleCapacityFull || stored.SlotRelease != nil || len(said) != 0 {
					t.Fatalf("started %t, released %#v, stopped %q, release on run %+v, notes %q; want the held run's slot kept and counted full",
						started, schedule.ReleasedSlots, schedule.Stopped, stored.SlotRelease, said)
				}
				return
			}
			if !started {
				t.Fatalf("started = %#v, stopped = %q; want the ready item started in the slot given back", schedule.Started, schedule.Stopped)
			}
			if len(schedule.ReleasedSlots) != 1 || schedule.ReleasedSlots[0].RunID != runID || schedule.SlotReleaseProblem != "" {
				t.Fatalf("ReleasedSlots = %#v, problem %q; want run %s named", schedule.ReleasedSlots, schedule.SlotReleaseProblem, runID)
			}
			if !stored.Status.InFlight() || stored.SlotRelease == nil {
				t.Fatalf("the run is %s with release %+v, want it kept in flight with the release recorded", stored.Status, stored.SlotRelease)
			}
			if len(said) != 1 || !strings.Contains(said[0], runID) || !strings.Contains(said[0], stored.SlotRelease.Says) || !strings.Contains(said[0], "nothing holds the run's lease") {
				t.Fatalf("item notes = %q, want one note naming the run and saying when and why its slot was given back", said)
			}
		})
	}
}
