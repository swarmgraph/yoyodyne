package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
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

// A window closed on one model holds nothing for the whole pull while a
// developer turn can end on another the provider still serves: a label mapped to
// sonnet is work that can run, and a hold over it would be the harness idling on
// its own misreading. What the window does hold is the work whose run would ask
// for the closed model — even work ahead of it in the order — which keeps its
// place and is named as waiting for provider capacity until the reset.
func TestARecordedWindowHoldsOnlyTheWorkOnTheClosedModel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	lifts := now.Add(72 * time.Hour)
	onOpus := beads.WorkItem{ID: "yoyodyne-ifd.401", Title: "on opus", Status: "open", Priority: 0}
	onSonnet := beads.WorkItem{ID: "yoyodyne-ifd.402", Title: "docs", Status: "open", Priority: 2, Labels: []string{"docs"}}
	harness := newScheduleHarness(onOpus, onSonnet)
	harness.now = now
	harness.usageLimits = recordSevenDay(t, now.Add(-time.Hour), lifts)
	harness.developers = []readmodel.AgentEndpoint{
		{Name: "developer", Provider: "claude-code", Model: "opus"},
		{Name: "developer (docs)", Provider: "claude-code", Model: "sonnet"},
	}
	harness.developerModels = []config.DeveloperModelRule{{Label: "docs", Model: "sonnet"}}

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped == ScheduleProviderWindow || schedule.UsageWindowResetsAt != nil {
		t.Fatalf("schedule = stopped %q, window %v; want no whole-pull hold while sonnet is served", schedule.Stopped, schedule.UsageWindowResetsAt)
	}
	if order := harness.pullOrder(); len(order) != 1 || order[0] != onSonnet.ID {
		t.Fatalf("started = %v, want only the item mapped to sonnet", order)
	}
	var said string
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID == onOpus.ID {
			said = deferred.Reason
		}
	}
	if !strings.Contains(said, "waiting for provider capacity") || !strings.Contains(said, "opus") ||
		!strings.Contains(said, "seven_day") || !strings.Contains(said, lifts.Format(time.RFC3339)) {
		t.Fatalf("passed over %s with %q, want it named as waiting for provider capacity on opus until %s", onOpus.ID, said, lifts.Format(time.RFC3339))
	}
	// The reason is kept on the item itself, under the usage-limit class, and the
	// item's priority is untouched: it keeps its place in the order.
	harness.mu.Lock()
	defer harness.mu.Unlock()
	for _, item := range harness.Items {
		if item.ID != onOpus.ID {
			continue
		}
		wait := item.SchedulingWait
		if wait == nil || wait.Class != string(runstate.PassedOverWaitingOnUsageWindow) || !strings.Contains(wait.Reason, lifts.Format(time.RFC3339)) {
			t.Fatalf("kept wait on %s = %+v, want the usage-limit wait with its reset", onOpus.ID, wait)
		}
		if item.Priority != onOpus.Priority {
			t.Fatalf("priority of %s = %d, want %d kept", onOpus.ID, item.Priority, onOpus.Priority)
		}
	}
}

// A developer that fails over to an alternate the window has not closed is
// served there, so its work is not held.
func TestARecordedWindowHoldsNothingAnAlternateServes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.401")...)
	harness.now = now
	harness.usageLimits = recordSevenDay(t, now.Add(-time.Hour), now.Add(72*time.Hour))
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus", Alternate: "sonnet"}}

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the item pulled while its alternate is served", schedule.Started)
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

// windowStops is the run store as the pull reads it for runs the provider's
// usage window ended: every run recorded, written to as runs end.
type windowStops struct {
	mu   sync.Mutex
	runs []runstate.State
}

func (w *windowStops) Recorded() ([]runstate.State, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]runstate.State(nil), w.runs...), nil
}

func (w *windowStops) Escalated() ([]runstate.Escalation, error) { return nil, nil }

func (w *windowStops) add(run runstate.State) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.runs = append(w.runs, run)
}

// windowEndedRun is the record a developer run leaves when the provider's
// seven_day window refused it with a reset past what the run may wait: ended
// cancelled, its claim given back, and the reset kept on its refusal.
func windowEndedRun(id, model string, at, resetsAt time.Time) runstate.State {
	return runstate.State{
		RunID: "run-" + id, WorkItemID: id, ProductID: "yoyodyne",
		Status: runstate.StatusCancelled, Phase: runstate.PhaseDeveloping,
		ProviderModel: model, UsageLimitModel: model, UsageLimitKind: "seven_day",
		Environmental: &runstate.EnvironmentalRefusal{
			Cause: runstate.CauseUsageWindow, RecordedAt: at, ResetsAt: &resetsAt,
			Settled: true, Refused: true,
		},
	}
}

// windowEndedOutcome is what that run hands back to the session that started it.
func windowEndedOutcome(id string, at, resetsAt time.Time) Outcome {
	return Outcome{
		RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusCancelled, UsageLimitKind: "seven_day",
		Failure: "this run was refused by an exhausted seven_day usage limit and the harness will not wait for it",
		Environmental: &runstate.EnvironmentalRefusal{
			Cause: runstate.CauseUsageWindow, RecordedAt: at, ResetsAt: &resetsAt,
			Settled: true, Refused: true,
		},
	}
}

// The recurrence this was written for: a developer run met a seven_day window
// whose reset lay days past what a run may wait, so the run ended rather than
// parked, and each following pull started the next item in the order into the
// same refusal. The ended run's record now holds every fresh dispatch until the
// reset, however many polls go by.
func TestARunTheWindowEndedHoldsFreshDispatchAcrossPulls(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	lifts := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.now = now
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}
	stops := &windowStops{}
	harness.stoppages = stops
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		stops.add(windowEndedRun(id, "opus", h.clock(), lifts))
		return windowEndedOutcome(id, h.clock(), lifts), nil
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 5 }

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: &recordedSessions{}, Now: harness.clock,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 1 || order[0] != "yoyodyne-one" {
		t.Fatalf("started = %v, want only the first item, and nothing more into the window it found", order)
	}
	if schedule.UsageWindowResetsAt == nil || !schedule.UsageWindowResetsAt.Equal(lifts) {
		t.Fatalf("schedule window reset = %v, want %s", schedule.UsageWindowResetsAt, lifts)
	}
}

// A session restarted inside the window remembers nothing of the run that met
// it, and reads the run's record from its first poll: it starts nothing, says
// the window and its reset, and at the first poll past the reset pulls the
// queue again in its order, with nothing released and nothing reprioritized.
func TestARestartedSessionHoldsOnAnEndedRunUntilItsReset(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	lifts := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	first := beads.WorkItem{ID: "yoyodyne-first", Title: "first", Status: "open", Priority: 1}
	later := beads.WorkItem{ID: "yoyodyne-later", Title: "later", Status: "open", Priority: 3}
	harness := newScheduleHarness(later, first)
	harness.now = now
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}
	harness.stoppages = &windowStops{runs: []runstate.State{windowEndedRun("yoyodyne-earlier", "opus", now.Add(-2*time.Hour), lifts)}}
	var beforeReset []string
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		switch sleeps {
		case 3:
			beforeReset = h.pullOrder()
			h.mu.Lock()
			h.now = lifts.Add(time.Minute)
			h.mu.Unlock()
		case 4:
			return false
		}
		return true
	}
	sessions := &recordedSessions{}

	if _, err := (Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock,
	}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(beforeReset) != 0 {
		t.Fatalf("started before the reset = %v, want nothing", beforeReset)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded || !idle.window || idle.windowResetsAt == nil || !idle.windowResetsAt.Equal(lifts) {
		t.Fatalf("idle = %+v, want the session shown waiting on the provider's window until %s", idle, lifts)
	}
	if order := harness.pullOrder(); len(order) == 0 || order[0] != first.ID {
		t.Fatalf("started = %v, want %s pulled first once the window reset", order, first.ID)
	}
}

// A run the window ended with no reset named leaves the provider's probe time in
// the reset's place, which is not a time the provider quoted, so it holds nothing
// here; and a run the provider has served the same model since is a window the
// provider has said is open. Both pull at once.
func TestAnEndedRunHoldsNothingWithoutANamedResetOrAfterAServedTurn(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	lifts := now.Add(40 * time.Hour)

	unknown := windowEndedRun("yoyodyne-earlier", "opus", now.Add(-time.Hour), now.Add(29*time.Minute))
	unknown.Environmental.ResetUnknown = true
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.401")...)
	harness.now = now
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}
	harness.stoppages = &windowStops{runs: []runstate.State{unknown}}
	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want a run with no named reset to hold nothing", schedule.Started)
	}

	served, err := runstate.NewCapacityServedStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewCapacityServedStore() error = %v", err)
	}
	if err := served.Record(runstate.CapacityServed{Model: "opus", At: now.Add(-time.Minute), What: "a turn of the product manager conversation"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	harness = newScheduleHarness(readyItems("yoyodyne-ifd.401")...)
	harness.now = now
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}
	harness.stoppages = &windowStops{runs: []runstate.State{windowEndedRun("yoyodyne-earlier", "opus", now.Add(-time.Hour), lifts)}}
	harness.capacityServed = served
	schedule, err = Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want a window a served turn disproved to hold nothing", schedule.Started)
	}
}
