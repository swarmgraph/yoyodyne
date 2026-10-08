package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordSevenDay writes the refusal the 2026-09-23 window left in the log: a
// seven_day limit on opus, with the reset the provider named.
func recordSevenDay(t *testing.T, at, resetsAt time.Time) *runstate.UsageLimitStore {
	t.Helper()
	store, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	if err := store.Record(runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            at,
		Waiting:       "run run-1 of yoyodyne-ifd.400",
		Kind:          "seven_day",
		ResetsAt:      &resetsAt,
		WorkItemID:    "yoyodyne-ifd.400",
		Model:         "opus",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return store
}

// The 2026-09-23 window, replayed from a session started after it opened. The
// maintenance pass restarted the watch over and over inside a seven_day window,
// and each fresh session — remembering nothing — pulled new items into the same
// refusal. A session started over a recorded window chooses nothing from its
// first poll, and says it is the provider's window rather than a hold.
func TestAFreshSessionInsideARecordedWindowPullsNothingAndSaysTheWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	lifts := time.Date(2026, 9, 26, 13, 43, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.401", "yoyodyne-ifd.402")...)
	harness.now = now
	harness.usageLimits = recordSevenDay(t, now.Add(-time.Hour), lifts)
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }
	sessions := &recordedSessions{}

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 || len(harness.order) != 0 {
		t.Fatalf("started = %#v, want nothing pulled into a window the record already holds", schedule.Started)
	}
	if schedule.UsageWindowResetsAt == nil || !schedule.UsageWindowResetsAt.Equal(lifts) {
		t.Fatalf("schedule window reset = %v, want %s", schedule.UsageWindowResetsAt, lifts)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded, so nothing said the session was inside a window")
	}
	if !idle.window || idle.windowResetsAt == nil || !idle.windowResetsAt.Equal(lifts) {
		t.Fatalf("idle = %+v, want the poll marked as one made inside the provider's window until %s", idle, lifts)
	}
	if !strings.HasPrefix(idle.reason, "Paused on the provider's usage limit until 13:43Z") {
		t.Fatalf("idle reason = %q, want it to lead with the provider's window", idle.reason)
	}
	if !strings.Contains(idle.reason, "seven_day") || !strings.Contains(idle.reason, "2026-09-26T13:43:00Z") {
		t.Fatalf("idle reason = %q, want the limit and the full reset named", idle.reason)
	}
	for _, transition := range sessions.recorded() {
		if transition.state == runstate.WatchBraked {
			t.Fatalf("transitions = %+v, want the window never reported as a hold somebody placed", sessions.recorded())
		}
	}
	// The same poll said once rather than once per interval.
	idles := 0
	for _, transition := range sessions.recorded() {
		if transition.state == runstate.WatchIdle {
			idles++
		}
	}
	if idles != 1 {
		t.Fatalf("idle transitions = %d, want one line for the whole wait", idles)
	}
}

// The window lifts at the reset the provider named, with nothing to release:
// the first poll after it pulls again.
func TestARecordedWindowLiftsAtItsResetWithNothingReleased(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 26, 13, 40, 0, 0, time.UTC)
	lifts := time.Date(2026, 9, 26, 13, 43, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.401")...)
	harness.now = now
	harness.usageLimits = recordSevenDay(t, now.Add(-72*time.Hour), lifts)
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			h.mu.Lock()
			h.now = lifts.Add(time.Minute)
			h.mu.Unlock()
			return true
		}
		return false
	}

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: &recordedSessions{}, Now: harness.clock,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-ifd.401" {
		t.Fatalf("started = %#v, want the item pulled at the first poll past the reset", schedule.Started)
	}
	if schedule.UsageWindowResetsAt != nil {
		t.Fatalf("schedule window reset = %v, want none once the window lifted", schedule.UsageWindowResetsAt)
	}
}

// A turn served on the refused model after the refusal is the provider saying
// the window lifted, whatever reset it quoted: on 2026-09-24 the operator added
// capacity a day into a seven_day window, and intake held on that window would
// have held until 09-27 over a provider serving every turn. The session pulls
// at its first poll, with nothing released.
func TestARecordedWindowAServedTurnDisprovedHoldsNothing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	refused := time.Date(2026, 9, 23, 6, 53, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.401")...)
	harness.now = now
	harness.usageLimits = recordSevenDay(t, refused, time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC))
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}
	served, err := runstate.NewCapacityServedStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewCapacityServedStore() error = %v", err)
	}
	if err := served.Record(runstate.CapacityServed{Model: "opus", At: refused.Add(26 * time.Hour), What: "a turn of the product manager conversation"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	harness.capacityServed = served

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped == ScheduleProviderWindow || len(schedule.Started) != 1 {
		t.Fatalf("schedule = stopped %q, started %#v; want the item pulled over a window a served turn disproved", schedule.Stopped, schedule.Started)
	}
	if schedule.UsageWindowResetsAt != nil {
		t.Fatalf("schedule window reset = %v, want none", schedule.UsageWindowResetsAt)
	}
}

// A window closed on one model holds nothing while a developer turn can end on
// another the provider still serves: a label mapped to sonnet is work that can
// run, and a hold over it would be the harness idling on its own misreading.
func TestARecordedWindowHoldsNothingWhileAnotherDeveloperModelIsServed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.401")...)
	harness.now = now
	harness.usageLimits = recordSevenDay(t, now.Add(-time.Hour), now.Add(72*time.Hour))
	harness.developers = []readmodel.AgentEndpoint{
		{Name: "developer", Provider: "claude-code", Model: "opus"},
		{Name: "developer (docs)", Provider: "claude-code", Model: "sonnet"},
	}

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the item pulled while a developer model is still served", schedule.Started)
	}
}

// A drain is a command somebody is waiting on the return of, so it stops on a
// recorded window rather than waiting it out, and says which stop it was.
func TestADrainStopsOnARecordedWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.401")...)
	harness.now = now
	harness.usageLimits = recordSevenDay(t, now.Add(-time.Hour), now.Add(72*time.Hour))
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleProviderWindow || len(schedule.Started) != 0 {
		t.Fatalf("schedule = stopped %q, started %#v; want a drain stopped on the window with nothing started", schedule.Stopped, schedule.Started)
	}
}
