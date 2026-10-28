package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// releasingRuns is a run store that frees the slots of the runs the test names
// as having no live worker when a pull asks it to, and from then on counts
// them free.
type releasingRuns struct {
	goneWorkers
	vanished map[string]bool
	asked    *int
}

func (r releasingRuns) ReleaseSlotsWithNoLiveWorker(context.Context, time.Time) ([]runstate.ReleasedSlot, error) {
	*r.asked++
	var released []runstate.ReleasedSlot
	for id, state := range r.inFlight {
		if r.vanished[state.RunID] && !r.gone[state.RunID] {
			r.gone[state.RunID] = true
			released = append(released, runstate.ReleasedSlot{RunID: state.RunID, WorkItemID: id,
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
			asked := 0
			runs := releasingRuns{
				goneWorkers: goneWorkers{scheduleHarness: harness, gone: map[string]bool{}},
				vanished:    map[string]bool{held: test.vanished},
				asked:       &asked,
			}
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.Runs = runs
				return pull, err
			}
			schedule, err := (Scheduler{Open: open}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
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
