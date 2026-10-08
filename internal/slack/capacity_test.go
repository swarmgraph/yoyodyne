package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The 2026-09-08 shape: five agents on one model, no alternates, the seven-day
// window closed with a reset five days off, and the development manager's
// sweep refused into the log twenty times a day.
var (
	septemberOpened = time.Date(2026, 9, 8, 7, 38, 40, 0, time.UTC)
	septemberResets = time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
)

// holdEveryRole gives the feed the September configuration and the sources the
// hold is read through, and records the first refusal at the moment the window
// closed.
func (h *testHarness) holdEveryRole(t *testing.T) {
	t.Helper()
	h.now = septemberOpened
	agents := []readmodel.AgentEndpoint{
		{Name: "architect", Provider: "claude-code", Model: "opus"},
		{Name: "developer", Provider: "claude-code", Model: "opus"},
		{Name: "development-manager", Provider: "claude-code", Model: "opus"},
		{Name: "product-manager", Provider: "claude-code", Model: "opus"},
		{Name: "reviewer", Provider: "claude-code", Model: "opus"},
	}
	h.feed.Standing = &readmodel.Sources{
		Runs:              h.runs,
		Conversations:     h.chats,
		Tracker:           standingTracker{},
		Directives:        h.directives,
		Amendments:        h.amend,
		OperatorHolds:     h.holds,
		IntakeHolds:       h.intake,
		Sessions:          h.watch,
		UsageLimits:       h.limits,
		Agents:            agents,
		UnknownResetPause: 30 * time.Minute,
		Capacity:          3,
		Now:               func() time.Time { return h.now },
	}
	h.sweepRefused(t, h.now)
}

// sweepRefused records the development manager's sweep being refused, exactly
// as the September log holds it: the seven-day limit, the reset, and no model.
func (h *testHarness) sweepRefused(t *testing.T, at time.Time) {
	t.Helper()
	resets := septemberResets
	if err := h.limits.Record(runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            at,
		Waiting:       "the development manager conversation chat-419cedb4a013b063f477e322a2a60466",
		Kind:          "seven_day",
		ResetsAt:      &resets,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

// capacity makes one pass and returns what it said about the hold, posting or
// not, so a test can read the cursor a silent pass wrote as well as a message.
func (h *testHarness) capacity(t *testing.T, cursors Cursors) (Delivery, bool) {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == capacityStream {
			return delivery, true
		}
	}
	return Delivery{}, false
}

// The September stoppage replayed reaches the operators on the first pass after
// the window closed — minutes, against the five days it took — naming the reset
// and the configuration that let one window hold every role.
func TestTheSeptemberStoppageReachesTheOperatorsOnTheFirstPass(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.holdEveryRole(t)
	harness.now = septemberOpened.Add(15 * time.Second)

	// The refusal itself is said as it always was; what is new is the hold.
	cursors := harness.poll(t, harness.start(), notify.KindUsageLimitExhausted, notify.KindCapacityHold)
	said, found := harness.capacity(t, harness.start())
	if !found || !said.Direct {
		t.Fatalf("delivery = %+v, found %v; want the hold taken to the operators directly the first time it is seen", said, found)
	}
	if severity := said.Notification.Event.Severity; severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a warning while the hold is young", severity)
	}
	message, err := notify.Render(said.Notification.Topic, said.Notification.Speaker, said.Notification.Event)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for _, fact := range []string{
		"Every role is paused on the provider's usage limit until 2026-09-13T03:00:00Z",
		"all 5 agents run on opus and none names an alternate",
		"Next: the operator's",
		"enabling failover",
	} {
		if !strings.Contains(message.Body, fact) {
			t.Fatalf("body %q does not carry %q", message.Body, fact)
		}
	}
	// And the four lines under it, without the banner said twice.
	if !strings.Contains(message.Body, "Running: nothing") {
		t.Fatalf("body %q does not carry the four lines", message.Body)
	}
	if strings.Count(message.Body, "Every role is paused") != 1 {
		t.Fatalf("body %q says the banner twice", message.Body)
	}
	if standing := cursors.Streams[capacityStream].Standing; standing != "capacity:2026-09-13T03:00:00Z" {
		t.Fatalf("standing = %q, want the hold marked by the reset the provider named", standing)
	}
}

// A hold is said again while it stands, once per heartbeat, in the channel: the
// first message is hours stale by the time anybody reads it, and silence has to
// keep meaning nothing to do. The repetitions are not taken to the operators
// while the hold is young — once was the interruption — and every refusal the
// sweep adds in the meantime changes nothing about when it is said.
func TestAStandingHoldIsSaidAgainEveryHeartbeat(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.holdEveryRole(t)
	harness.feed.Heartbeat = time.Hour

	cursors := harness.poll(t, harness.start(), notify.KindUsageLimitExhausted, notify.KindCapacityHold)

	harness.now = harness.now.Add(20 * time.Minute)
	harness.sweepRefused(t, harness.now)
	cursors = harness.poll(t, cursors, notify.KindUsageLimitExhausted)

	harness.now = harness.now.Add(time.Hour)
	again, found := harness.capacity(t, cursors)
	if !found || again.Silent() {
		t.Fatal("an hour on, the standing hold was not said again")
	}
	if again.Direct {
		t.Fatal("the operators were interrupted a second time for a hold that is an hour old")
	}
	if severity := again.Notification.Event.Severity; severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want the repetition at the same severity while the hold is young", severity)
	}
}

// Past the escalation bar the hold is said as critical and taken to the
// operators with every repetition. A hold nothing but a person ends early is the
// one state where getting quieter as it stands is the wrong shape.
func TestAHoldPastTheBarIsCriticalAndReachesTheOperatorsEachTime(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.holdEveryRole(t)
	harness.feed.Heartbeat = time.Hour
	harness.feed.CapacityEscalation = 6 * time.Hour

	cursors := harness.poll(t, harness.start(), notify.KindUsageLimitExhausted, notify.KindCapacityHold)
	for hour := 1; hour <= 2; hour++ {
		harness.now = septemberOpened.Add(time.Duration(hour) * time.Hour)
		said, found := harness.capacity(t, cursors)
		if !found || said.Direct || said.Notification.Event.Severity != report.SeverityWarning {
			t.Fatalf("hour %d: delivery = %+v, want a warning in the channel alone", hour, said)
		}
		cursors.Streams[capacityStream] = said.Cursor
	}
	for hour := 6; hour <= 8; hour++ {
		harness.now = septemberOpened.Add(time.Duration(hour) * time.Hour)
		said, found := harness.capacity(t, cursors)
		if !found || !said.Direct || said.Notification.Event.Severity != report.SeverityCritical {
			t.Fatalf("hour %d: delivery = %+v, want critical and taken to the operators", hour, said)
		}
		cursors.Streams[capacityStream] = said.Cursor
	}
}

// The reset passing clears the hold. Nothing is said about that — the turn that
// is served says it — and the cursor forgets the hold so the next one is said
// afresh rather than inheriting this one's clock.
func TestAHoldThatLiftedIsForgotten(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.holdEveryRole(t)

	cursors := harness.poll(t, harness.start(), notify.KindUsageLimitExhausted, notify.KindCapacityHold)
	harness.now = septemberResets.Add(time.Minute)
	cleared, found := harness.capacity(t, cursors)
	if !found || !cleared.Silent() {
		t.Fatalf("delivery = %+v, found %v; want the cursor cleared silently", cleared, found)
	}
	if cleared.Cursor.Standing != "" {
		t.Fatalf("standing = %q, want the hold forgotten once the reset passed", cleared.Cursor.Standing)
	}
	cursors.Streams[capacityStream] = cleared.Cursor
	if _, found := harness.capacity(t, cursors); found {
		t.Fatal("a pass with no hold and nothing to forget wrote a cursor anyway")
	}
}

// parkRunOnTheLimit records one developer run asleep on the September reset,
// as the pipeline leaves it, and returns it so a later probe can move its
// record.
func (h *testHarness) parkRunOnTheLimit(t *testing.T, since time.Time) runstate.State {
	t.Helper()
	state := h.run(t, runstate.StatusRunning)
	resets := septemberResets
	state.StartedAt = since.Add(-time.Hour)
	state.UpdatedAt = since
	state.ProviderModel = "opus"
	state.UsageLimitResetsAt = &resets
	state.UsageLimitPausedSince = &since
	state.UsageLimitKind = "seven_day"
	state.UsageLimitModel = "opus"
	state.PauseCause = runstate.PauseUsageLimit
	h.record(t, state)
	return state
}

// The same stoppage with nothing in the log: no recurring task, no open
// conversation, and the one developer run parked on the reset. The hold is read
// from the run's own record and reaches the operators on the first pass, is said
// again while it stands without interrupting them, and is critical past the
// bar — the same rule, from the other record. The run probing while it waits
// moves its record and not the hold: it is one hold on one reset, said once.
func TestARunParkedOnTheLimitReachesTheOperatorsOnceFromTheRunsAlone(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.holdEveryRole(t)
	harness.feed.Heartbeat = time.Hour
	harness.feed.CapacityEscalation = 6 * time.Hour
	// The log the September configuration recorded a refusal in is replaced with
	// an empty one: what holds here is the run.
	harness.limits, _ = runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	harness.feed.Standing.UsageLimits = harness.limits
	harness.feed.UsageLimits = harness.limits
	parked := harness.parkRunOnTheLimit(t, septemberOpened)

	// The run's own start and park are said as they always were; what is new is
	// the hold read off the same record.
	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindRunParked, notify.KindCapacityHold)
	said, found := harness.capacity(t, harness.start())
	if !found || !said.Direct || said.Notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("delivery = %+v, found %v; want the hold taken to the operators directly the first time it is seen, as a warning", said, found)
	}
	message, err := notify.Render(said.Notification.Topic, said.Notification.Speaker, said.Notification.Event)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for _, fact := range []string{
		"Every role is paused on the provider's usage limit until 2026-09-13T03:00:00Z",
		"1 run parked since 2026-09-08T07:38:40Z",
	} {
		if !strings.Contains(message.Body, fact) {
			t.Fatalf("body %q does not carry %q", message.Body, fact)
		}
	}
	if standing := cursors.Streams[capacityStream].Standing; standing != "capacity:2026-09-13T03:00:00Z" {
		t.Fatalf("standing = %q, want the hold marked by the reset the run is parked on", standing)
	}

	// The run probes every half hour, re-recording its wait each time. The hold
	// is said again an hour on, in the channel alone.
	for hour := 1; hour <= 2; hour++ {
		harness.now = septemberOpened.Add(time.Duration(hour) * time.Hour)
		parked.UpdatedAt = harness.now.Add(-10 * time.Minute)
		harness.save(t, parked)
		again, found := harness.capacity(t, cursors)
		if !found || again.Silent() || again.Direct || again.Notification.Event.Severity != report.SeverityWarning {
			t.Fatalf("hour %d: delivery = %+v, found %v; want the standing hold said again in the channel alone", hour, again, found)
		}
		cursors.Streams[capacityStream] = again.Cursor
	}
	// And past the bar it is critical and taken to them again, measured from
	// when the run parked rather than from its latest probe.
	harness.now = septemberOpened.Add(6 * time.Hour)
	parked.UpdatedAt = harness.now.Add(-10 * time.Minute)
	harness.save(t, parked)
	critical, found := harness.capacity(t, cursors)
	if !found || !critical.Direct || critical.Notification.Event.Severity != report.SeverityCritical {
		t.Fatalf("delivery = %+v, found %v; want critical and taken to the operators six hours on", critical, found)
	}
}

// A refusal something served through is not a hold, however many times it is
// recorded: failover working is exactly the state this exists to tell a
// stoppage from.
func TestFailoverServingIsNotAHold(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.holdEveryRole(t)
	agents := harness.feed.Standing.Agents
	for index := range agents {
		agents[index].Alternate, agents[index].AlternateProvider = "sonnet", "claude-code"
	}
	// The one recorded stoppage predates the alternate; every turn since was
	// served. The unnamed refusal is read as the chain every agent shares, which
	// now ends on sonnet, and a refusal of sonnet is a hold — so the log is
	// replaced with the substitutions failover actually writes.
	resets := septemberResets
	harness.limits, _ = runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	harness.feed.Standing.UsageLimits = harness.limits
	if err := harness.limits.Record(runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            septemberOpened,
		Waiting:       "the development manager conversation chat-419cedb4a013b063f477e322a2a60466",
		Kind:          "seven_day",
		ResetsAt:      &resets,
		Model:         "opus",
		ServedBy:      "sonnet",
		Substitution:  runstate.SubstitutedForCapacity,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	harness.feed.UsageLimits = harness.limits
	harness.now = septemberOpened.Add(time.Hour)
	harness.poll(t, harness.start(), notify.KindModelSubstituted)
}
