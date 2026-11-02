package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// While maintenance holds new runs for a tracker squash, a pull starts
// nothing and says why; once the hold is lifted, or has reached its own end,
// the next pull chooses again with nothing released.
func TestAPullStartsNothingWhileATrackerSquashHoldsNewRuns(t *testing.T) {
	t.Parallel()
	holds, err := runstate.NewTrackerSquashHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewTrackerSquashHoldStore() error = %v", err)
	}
	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.squashHolds = holds
	now := harness.clock()
	if err := holds.Place(runstate.TrackerSquashHold{Since: now, Until: now.Add(19 * time.Minute), Reason: "listing open items took 33 seconds"}); err != nil {
		t.Fatalf("Place() error = %v", err)
	}
	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}

	held, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 0 || len(held.Started) != 0 {
		t.Fatalf("pulled %v and started %d, want nothing started while new runs are held", order, len(held.Started))
	}
	if held.Stopped != ScheduleSquashHeld || held.SquashHold == nil || !strings.Contains(held.Render(), "no new run is started until it has finished or been skipped") {
		t.Fatalf("stopped = %q, hold %#v, want the drain stopped on the squash and the hold said:\n%s", held.Stopped, held.SquashHold, held.Render())
	}

	if err := holds.Lift(); err != nil {
		t.Fatalf("Lift() error = %v", err)
	}
	resumed, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) == 0 || resumed.Stopped == ScheduleSquashHeld || resumed.SquashHold != nil {
		t.Fatalf("pulled %v, stopped %q, want the line choosing again once the hold was lifted", order, resumed.Stopped)
	}
}

// A hold a pass left behind ends on its own: once past its end it holds
// nothing, though nobody lifted it.
func TestATrackerSquashHoldPastItsEndHoldsNothing(t *testing.T) {
	t.Parallel()
	holds, err := runstate.NewTrackerSquashHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewTrackerSquashHoldStore() error = %v", err)
	}
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.squashHolds = holds
	now := harness.clock()
	if err := holds.Place(runstate.TrackerSquashHold{Since: now.Add(-time.Hour), Until: now.Add(-time.Minute), Reason: "a pass that died"}); err != nil {
		t.Fatalf("Place() error = %v", err)
	}
	schedule, err := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 1 || schedule.Stopped == ScheduleSquashHeld {
		t.Fatalf("pulled %v, stopped %q, want the item pulled past a hold that has ended", order, schedule.Stopped)
	}
}

// A watching session waits the hold out poll after poll rather than stopping.
func TestAWatchingSessionWaitsOutATrackerSquashHold(t *testing.T) {
	t.Parallel()
	holds, err := runstate.NewTrackerSquashHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewTrackerSquashHoldStore() error = %v", err)
	}
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.squashHolds = holds
	now := harness.clock()
	if err := holds.Place(runstate.TrackerSquashHold{Since: now, Until: now.Add(24 * time.Hour), Reason: "listing open items took 33 seconds"}); err != nil {
		t.Fatalf("Place() error = %v", err)
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 0 {
		t.Fatalf("pulled %v, want nothing chosen while new runs are held", order)
	}
	if schedule.Polls < 3 || schedule.Stopped != ScheduleCancelled {
		t.Fatalf("polls = %d, stopped = %q; want the session waiting the hold out poll after poll", schedule.Polls, schedule.Stopped)
	}
}
