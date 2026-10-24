package orchestrator

import (
	"context"
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
		harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
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
		if schedule.Stopped != ScheduleBlocked || !strings.Contains(schedule.Blocked, "on run run-yoyodyne-one of yoyodyne-one") ||
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
