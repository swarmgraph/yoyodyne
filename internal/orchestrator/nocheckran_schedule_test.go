package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stoppedWithNoCheckRan is a run every configured check of which could not run,
// because the machine refused to start make: the pipeline blocked the item and
// docketed it, which is the one account of the cause.
func stoppedWithNoCheckRan(h *scheduleHarness, id string) (Outcome, error) {
	h.retire(id)
	return Outcome{
		RunID:      "run-" + id,
		WorkItemID: id,
		Status:     runstate.StatusFailed,
		Blocked:    true,
		StopClass:  runstate.CauseNoCheckRan.StopClass(),
		Failure:    "no check verified the change, because every configured check could not run",
		Environmental: &runstate.EnvironmentalRefusal{
			Cause:      runstate.CauseNoCheckRan,
			Detail:     "no check verified the change, because every configured check could not run: make test could not run: the machine refused to start it: You have not agreed to the Xcode license agreements.",
			Settled:    true,
			Refused:    true,
			NothingRan: false,
		},
	}, nil
}

// Two ready items meet the same machine cause. The first run stops on it and
// is the one account; the second is not started, the session says why, and the
// stop counts toward no hold on new work. A drain stops there. A watch chooses
// nothing until the probe interval has passed, then starts one run to find out.
func TestChecksTheMachineCouldNotRunHoldNewWorkAfterTheFirstRun(t *testing.T) {
	t.Parallel()

	t.Run("a drain", func(t *testing.T) {
		t.Parallel()
		items := readyItems("yoyodyne-one", "yoyodyne-two")
		items[0].Title, items[1].Title = "Pin the charting library", "Report shared provider cost"
		harness := newScheduleHarness(items...)
		var started []string
		harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
			started = append(started, id)
			return stoppedWithNoCheckRan(h, id)
		}
		scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
		schedule, err := scheduler.Schedule(context.Background())
		if err != nil {
			t.Fatalf("Schedule() error = %v", err)
		}
		if strings.Join(started, ",") != "yoyodyne-one" {
			t.Fatalf("started %v, want only the first run", started)
		}
		if schedule.Stopped != ScheduleBlocked || !strings.Contains(schedule.Blocked, "on run run-yoyodyne-one of Pin the charting library (yoyodyne-one),") ||
			!strings.Contains(schedule.Blocked, "You have not agreed to the Xcode license agreements") {
			t.Fatalf("stopped %q, blocked %q; want the session to say once what the machine refused", schedule.Stopped, schedule.Blocked)
		}
		if schedule.BlockedInARow != 0 || schedule.Braked != nil {
			t.Fatalf("blocked in a row = %d, braked = %#v; want the machine's stop counted toward nothing", schedule.BlockedInARow, schedule.Braked)
		}
	})

	t.Run("a watch", func(t *testing.T) {
		t.Parallel()
		harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
		var started []string
		var startedAtSleep []int
		harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
			h.mu.Lock()
			started, startedAtSleep = append(started, id), append(startedAtSleep, h.sleeps)
			h.mu.Unlock()
			return stoppedWithNoCheckRan(h, id)
		}
		var blockedSaid string
		harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			if sleeps == 5 && len(started) != 1 {
				blockedSaid = "a second run was started inside the probe interval"
			}
			return sleeps < 40
		}
		scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
		schedule, err := scheduler.Schedule(context.Background())
		if err != nil {
			t.Fatalf("Schedule() error = %v", err)
		}
		if blockedSaid != "" {
			t.Fatal(blockedSaid)
		}
		if strings.Join(started, ",") != "yoyodyne-one,yoyodyne-two" {
			t.Fatalf("started %v, want the first run and then one probe once the interval passed", started)
		}
		// The harness's poll is a minute and its sleep advances the clock by it,
		// so the probe is the first pull at or past thirty minutes.
		if startedAtSleep[1] < int(defaultMachineChecksProbe/time.Minute) {
			t.Fatalf("the probe started after %d polls, want no sooner than the %s probe interval", startedAtSleep[1], defaultMachineChecksProbe)
		}
		if schedule.BlockedInARow != 0 || schedule.Braked != nil {
			t.Fatalf("blocked in a row = %d, braked = %#v; want the machine's stops counted toward nothing", schedule.BlockedInARow, schedule.Braked)
		}
	})
}

// With two developer places, once the interval has passed the session starts
// one run and only one, and starts nothing else while it is going. That run
// meeting the cause again holds again for another interval; the next one
// finding the checks run clears the hold, and the session fills both places.
func TestOnlyOneRunFindsOutWhetherTheChecksCanRunAgain(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-a", "yoyodyne-b", "yoyodyne-c", "yoyodyne-d", "yoyodyne-e", "yoyodyne-f")...)
	harness.capacity = 2
	type start struct {
		id    string
		sleep int
	}
	var starts []start
	var problems []string
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		starts = append(starts, start{id, h.sleeps})
		h.mu.Unlock()
		if id == "yoyodyne-c" || id == "yoyodyne-d" {
			// The run that finds out takes a while, and nothing else may start
			// while it is going.
			time.Sleep(200 * time.Millisecond)
			h.mu.Lock()
			if len(starts) != map[string]int{"yoyodyne-c": 3, "yoyodyne-d": 4}[id] {
				problems = append(problems, fmt.Sprintf("%d runs had started while %s was finding out, want none beside it", len(starts), id))
			}
			h.mu.Unlock()
		}
		if id == "yoyodyne-d" || id == "yoyodyne-e" || id == "yoyodyne-f" {
			return h.complete(id), nil
		}
		return stoppedWithNoCheckRan(h, id)
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 100 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	if _, err := scheduler.Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
	var order []string
	for _, started := range starts {
		order = append(order, started.id)
	}
	if len(starts) != 6 || order[2] != "yoyodyne-c" || order[3] != "yoyodyne-d" {
		t.Fatalf("started %v, want a and b, then c alone, then d alone, then e and f", order)
	}
	interval := int(defaultMachineChecksProbe / time.Minute)
	if starts[2].sleep < interval || starts[3].sleep < starts[2].sleep+interval {
		t.Fatalf("c started after %d polls and d after %d, want each a whole %s interval after the run before it met the cause", starts[2].sleep, starts[3].sleep, defaultMachineChecksProbe)
	}
	if starts[4].sleep != starts[5].sleep {
		t.Fatalf("e started after %d polls and f after %d, want both places filled together once the hold cleared", starts[4].sleep, starts[5].sleep)
	}
}

// The waiting message names the work item by its title with its identifier
// after it, and by the identifier alone only where no title was read.
func TestTheChecksHoldNamesTheItemByItsTitle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 24, 17, 0, 0, 0, time.UTC)
	outcome, _ := stoppedWithNoCheckRan(newScheduleHarness(), "yoyodyne-ani")
	for title, want := range map[string]string{
		"Report shared provider cost": "of Report shared provider cost (yoyodyne-ani),",
		"":                            "of yoyodyne-ani,",
	} {
		held, holds := machineChecksHeldBy(outcome, title, now)
		if !holds {
			t.Fatal("machineChecksHeldBy() placed no hold for a run no check could run on")
		}
		said, state := held.state(now, time.Minute)
		if state != machineChecksWaiting || !strings.Contains(said, want) {
			t.Fatalf("state = %d, said %q; want the session waiting and naming the item as %q", state, said, want)
		}
	}
}
