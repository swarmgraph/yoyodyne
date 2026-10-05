package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// recordedEvents is the product's streams as a test writes them: every event,
// read back by the window a pass asks for.
type recordedEvents struct {
	events []PassEvent
	// failing names a stream that cannot be read.
	failing string
}

func (r *recordedEvents) Events(_ context.Context, stream string, after, until time.Time) ([]PassEvent, error) {
	if stream == r.failing {
		return nil, errors.New("the stream is unreadable")
	}
	var kept []PassEvent
	for _, event := range r.events {
		if event.Stream == stream && event.At.After(after) && !event.At.After(until) {
			kept = append(kept, event)
		}
	}
	return kept, nil
}

func (r *recordedEvents) admit(at time.Time, count int) {
	for index := 1; index <= count; index++ {
		r.events = append(r.events, PassEvent{
			Stream:  runstate.PassStreamTracker,
			Class:   config.TriggerAdmissions,
			At:      at,
			Key:     fmt.Sprintf("yoyodyne-ifd.9%02d", index),
			Subject: fmt.Sprintf("yoyodyne-ifd.9%02d", index),
			Detail:  fmt.Sprintf("admitted item %d", index),
		})
	}
}

func (r *recordedEvents) land(at time.Time, item string) {
	r.events = append(r.events, PassEvent{Stream: runstate.PassStreamRuns, Class: config.TriggerLandings, At: at, Key: "landed/" + item, Subject: item, Detail: "run run-1 landed"})
}

func (r *recordedEvents) stop(at time.Time, item string) {
	r.events = append(r.events, PassEvent{Stream: runstate.PassStreamRuns, Class: config.TriggerStoppages, At: at, Key: "stopped/" + item, Subject: item, Detail: "run run-2 stopped"})
}

// busyConversations says a turn is in flight on the instances it names.
type busyConversations map[string]bool

func (b busyConversations) InFlight(agent string) (bool, error) { return b[agent], nil }

const instanceName = "reliability-pm"

func instance(every time.Duration, on ...config.TriggerEvent) map[string]config.AgentConfig {
	return map[string]config.AgentConfig{
		instanceName: {
			Role:     domain.RoleProgramManager,
			Model:    "opus",
			Lane:     "reliability",
			Triggers: config.Triggers{Every: config.Duration(every), On: on},
		},
	}
}

// passHarness is one instance, its cursor, its streams, and the sweep store its
// passes are claimed and recorded in.
type passHarness struct {
	trigger Trigger
	sweeps  *runstate.SweepStore
	cursors *runstate.PassCursorStore
	events  *recordedEvents
	role    *wokenRole
	clock   *movingRecurringClock
}

func newPassHarness(t *testing.T, instances map[string]config.AgentConfig) *passHarness {
	t.Helper()
	root := t.TempDir()
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	cursors, err := runstate.NewPassCursorStore(root, "example")
	if err != nil {
		t.Fatalf("NewPassCursorStore() error = %v", err)
	}
	harness := &passHarness{
		sweeps:  sweeps,
		cursors: cursors,
		events:  &recordedEvents{},
		role:    &wokenRole{},
		clock:   &movingRecurringClock{now: recurringNow},
	}
	harness.trigger = Trigger{
		Instances: instances,
		Claims:    sweeps,
		Reports:   sweeps,
		Roles:     harness.role,
		Cursors:   cursors,
		Events:    harness.events,
		Clock:     harness.clock,
	}
	return harness
}

func (h *passHarness) fire(t *testing.T, at time.Time) RecurringSweep {
	t.Helper()
	h.clock.now = at
	fired, err := h.trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() at %s error = %v", at.Format(time.RFC3339), err)
	}
	return fired
}

func (h *passHarness) recorded(t *testing.T) []runstate.Sweep {
	t.Helper()
	recorded, _, err := h.sweeps.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	return recorded
}

func (h *passHarness) cursor(t *testing.T, stream string) time.Time {
	t.Helper()
	cursor, found, err := h.cursors.Load(instanceName)
	if err != nil || !found {
		t.Fatalf("Load() = %v, %v; want the instance's cursor", found, err)
	}
	return cursor.Streams[stream]
}

// An instance with `every` fires as a recurring task: its pass wakes the
// instance by name on the agent's own model, is recorded under the instance's
// name with the model that served, and is paced by the cadence.
func TestAnInstanceWithEveryFiresAsARecurringTask(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(2*time.Hour))
	h.role.answers = []scriptedTurn{{result: complete("the lane is moving"), model: "claude-opus-5-5"}, {result: complete("still moving")}}

	first := h.fire(t, recurringNow)
	if len(first.Fired) != 1 || first.Fired[0].Task != instanceName || first.Fired[0].Role != domain.RoleProgramManager {
		t.Fatalf("fired = %+v, want the instance's scheduled pass", first.Fired)
	}
	if len(h.role.agents) != 1 || h.role.agents[0] != instanceName {
		t.Errorf("woke %v, want the instance woken by name", h.role.agents)
	}
	if h.role.models[0] != "" {
		t.Errorf("asked for model %q, want the agent's own model", h.role.models[0])
	}
	if h.role.passes[0] != instanceName+"#1" {
		t.Errorf("pass = %q, want the instance's first firing", h.role.passes[0])
	}
	if !strings.Contains(h.role.messages[0], `lane "reliability"`) || !strings.Contains(h.role.messages[0], "schedule is due") {
		t.Errorf("message = %q, want the lane and why it was woken", h.role.messages[0])
	}
	recorded := h.recorded(t)
	if len(recorded) != 1 || recorded[0].Task != instanceName || recorded[0].Model != "claude-opus-5-5" {
		t.Fatalf("recorded = %+v, want the pass under the instance's name with the model that served", recorded)
	}

	if again := h.fire(t, recurringNow.Add(10*time.Minute)); len(again.Fired) != 0 {
		t.Errorf("fired = %+v, want nothing before the cadence passes", again.Fired)
	}
	if due := h.fire(t, recurringNow.Add(2*time.Hour)); len(due.Fired) != 1 {
		t.Errorf("fired = %+v, want the pass once the cadence passed", due.Fired)
	}
}

// A product manager admitting thirty items in one turn wakes the instance
// once, and that one pass carries all thirty.
func TestThirtyAdmissionsInOneTurnAreOnePass(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(0, config.TriggerAdmissions))
	h.role.answers = []scriptedTurn{{result: complete("thirty admitted")}}

	// The first pull begins watching: nothing that happened before the
	// instance was watched is handed to it.
	h.events.admit(recurringNow.Add(-time.Hour), 5)
	if fired := h.fire(t, recurringNow); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want the first pull to begin watching and take nothing", fired.Fired)
	}
	h.events.admit(recurringNow.Add(time.Minute), 30)

	for _, at := range []time.Duration{time.Minute, 2 * time.Minute} {
		if fired := h.fire(t, recurringNow.Add(at)); len(fired.Fired) != 0 {
			t.Fatalf("fired = %+v at +%s, want the wake held while the burst settles", fired.Fired, at)
		}
	}
	fired := h.fire(t, recurringNow.Add(3*time.Minute))
	if len(fired.Fired) != 1 || fired.Fired[0].Events["admissions"] != 30 {
		t.Fatalf("fired = %+v, want one pass carrying thirty admissions", fired.Fired)
	}
	message := h.role.messages[0]
	if !strings.Contains(message, "Tracker — 30 admissions after") {
		t.Errorf("message = %q, want the thirty admissions counted under the tracker", message)
	}
	if count := strings.Count(message, "admitted item"); count != 30 {
		t.Errorf("message lists %d admissions, want the thirty and none of the five before watching began", count)
	}
	recorded := h.recorded(t)
	if len(recorded) != 1 || recorded[0].Events["admissions"] != 30 {
		t.Fatalf("recorded = %+v, want one pass carrying thirty admissions", recorded)
	}
	if later := h.fire(t, recurringNow.Add(time.Hour)); len(later.Fired) != 0 {
		t.Errorf("fired = %+v, want nothing once the burst is carried", later.Fired)
	}
	if len(h.role.messages) != 1 {
		t.Errorf("messages = %d, want the burst to have woken the instance once", len(h.role.messages))
	}
}

// An event inside the settle window does not take the wake; the schedule being
// due does, and the pass it takes carries the event.
func TestAnEventInsideTheSettleWindowWaitsUnlessTheScheduleIsDue(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(time.Hour, config.TriggerLandings))
	h.role.answers = []scriptedTurn{{result: complete("scheduled")}, {result: complete("landed")}, {result: complete("due")}}

	// The first pull is the schedule's first pass, and begins watching.
	if fired := h.fire(t, recurringNow); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the first scheduled pass", fired.Fired)
	}

	// Inside the window and not due: held.
	h.events.land(recurringNow.Add(30*time.Minute), "yoyodyne-ifd.1")
	if fired := h.fire(t, recurringNow.Add(31*time.Minute)); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want an event inside the settle window to wait", fired.Fired)
	}
	// Quiet for the window: taken.
	if fired := h.fire(t, recurringNow.Add(32*time.Minute)); len(fired.Fired) != 1 || fired.Fired[0].Events["landings"] != 1 {
		t.Fatalf("fired = %+v, want the settled wake taken", fired.Fired)
	}

	// Inside the window and due: taken now, carrying the event.
	h.events.land(recurringNow.Add(91*time.Minute+30*time.Second), "yoyodyne-ifd.2")
	fired := h.fire(t, recurringNow.Add(92*time.Minute))
	if len(fired.Fired) != 1 || fired.Fired[0].Events["landings"] != 1 {
		t.Fatalf("fired = %+v, want the due schedule to take the pass inside the window", fired.Fired)
	}
	if last := h.role.messages[len(h.role.messages)-1]; !strings.Contains(last, "yoyodyne-ifd.2") || !strings.Contains(last, "schedule is due") {
		t.Errorf("message = %q, want the event carried by the scheduled pass", last)
	}
}

// A pass that fails leaves the cursor where it was, the record says so, and the
// next pass carries the same events.
func TestAFailedPassLeavesTheCursorAndTheNextCarriesTheSameEvents(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(0, config.TriggerStoppages))
	h.fire(t, recurringNow)
	before := h.cursor(t, runstate.PassStreamRuns)

	h.events.stop(recurringNow.Add(time.Minute), "yoyodyne-ifd.7")
	h.role.answers = []scriptedTurn{{err: errors.New("the provider hung up")}}
	failed := h.fire(t, recurringNow.Add(4*time.Minute))
	if len(failed.Fired) != 1 || !strings.Contains(failed.Fired[0].Problem, "cursor was not moved") {
		t.Fatalf("fired = %+v, want a failed pass that says its cursor was not moved", failed.Fired)
	}
	if after := h.cursor(t, runstate.PassStreamRuns); !after.Equal(before) {
		t.Fatalf("cursor = %s, want it left at %s by a pass that failed", after, before)
	}

	// Not retried before the recurring minimum, however armed the wake is.
	if fired := h.fire(t, recurringNow.Add(6*time.Minute)); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want the retry paced by the recurring minimum", fired.Fired)
	}
	h.role.answers = []scriptedTurn{{result: complete("looked at the stoppage")}}
	retried := h.fire(t, recurringNow.Add(9*time.Minute))
	if len(retried.Fired) != 1 || retried.Fired[0].Events["stoppages"] != 1 || retried.Fired[0].Problem != "" {
		t.Fatalf("fired = %+v, want the next pass carrying the same stoppage", retried.Fired)
	}
	if last := h.role.messages[len(h.role.messages)-1]; !strings.Contains(last, "yoyodyne-ifd.7") {
		t.Errorf("message = %q, want the same event carried again", last)
	}
	if after := h.cursor(t, runstate.PassStreamRuns); !after.Equal(recurringNow.Add(9 * time.Minute)) {
		t.Errorf("cursor = %s, want it moved to the moment the completed pass was taken", after)
	}
}

// The operator's pause stops a pass exactly as it stops a recurring task:
// nothing is claimed, nothing is said, and the cursor is not begun.
func TestAPausedHarnessPassesNoInstance(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(time.Hour, config.TriggerLandings))
	h.trigger.Holds = pausedHolds{hold: runstate.OperatorHold{HeldAt: recurringNow}, held: true}

	fired := h.fire(t, recurringNow)
	if fired.Paused == nil || len(fired.Fired) != 0 || len(h.role.messages) != 0 {
		t.Fatalf("fired = %+v, messages = %v, want a paused harness to pass nothing", fired, h.role.messages)
	}
	if _, found, err := h.sweeps.Find(instanceName); err != nil || found {
		t.Errorf("Find() = %v, %v, want no claim taken under a pause", found, err)
	}
	if _, found, err := h.cursors.Load(instanceName); err != nil || found {
		t.Errorf("Load() = %v, %v, want no cursor begun under a pause", found, err)
	}
}

// A turn in flight on the instance's conversation skips the pass, and the
// events wait past the cursor for the pass after.
func TestATurnInFlightStillDispatchesTheDuePassToItsConversation(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(time.Hour, config.TriggerLandings))
	h.trigger.Conversations = busyConversations{instanceName: true}

	if fired := h.fire(t, recurringNow); len(fired.Fired) != 1 || len(h.role.messages) != 1 {
		t.Fatalf("fired = %+v, want the due pass dispatched to queue for its conversation", fired.Fired)
	}
	if _, found, err := h.sweeps.Find(instanceName); err != nil || !found {
		t.Errorf("Find() = %v, %v, want the due pass claimed", found, err)
	}
	h.trigger.Conversations = busyConversations{}
	if fired := h.fire(t, recurringNow.Add(time.Minute)); len(fired.Fired) != 0 {
		t.Errorf("fired = %+v, want no second pass within the cadence", fired.Fired)
	}
}

// Tasks and instances with different conversations share a pull.
func TestATaskAndAnInstanceDueTogetherFireTogether(t *testing.T) {
	t.Parallel()
	h := newPassHarness(t, instance(time.Hour))
	h.trigger.Tasks = hourlyTask("sweep")
	first := h.fire(t, recurringNow)
	if len(first.Fired) != 2 {
		t.Fatalf("firings: %+v", first.Fired)
	}
	if second := h.fire(t, recurringNow.Add(time.Minute)); len(second.Fired) != 0 {
		t.Fatalf("second: %+v", second)
	}
}

// The pass's first message carries the events since the cursor, grouped by
// stream, each stream saying what it holds and from when.
func TestThePassMessageGroupsTheEventsByStream(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(0, config.TriggerLandings, config.TriggerAdmissions, config.TriggerStoppages))
	h.role.answers = []scriptedTurn{{result: complete("read it")}}
	h.fire(t, recurringNow)

	h.events.admit(recurringNow.Add(time.Minute), 2)
	h.events.land(recurringNow.Add(time.Minute), "yoyodyne-ifd.1")
	h.events.stop(recurringNow.Add(2*time.Minute), "yoyodyne-ifd.2")
	fired := h.fire(t, recurringNow.Add(10*time.Minute))
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want one pass", fired.Fired)
	}
	message := h.role.messages[0]
	runs := strings.Index(message, "Run records — 1 landing, 1 stoppage after "+recurringNow.Format(time.RFC3339))
	tracker := strings.Index(message, "Tracker — 2 admissions after "+recurringNow.Format(time.RFC3339))
	if runs < 0 || tracker < 0 || tracker < runs {
		t.Fatalf("message = %q, want the run records and then the tracker, each with its counts", message)
	}
	if landing := strings.Index(message, "landing: yoyodyne-ifd.1"); landing < runs || landing > tracker {
		t.Errorf("message = %q, want the landing under the run records", message)
	}
	// The pass ends in a lane report and a summary a person reads, so the
	// message tells the instance to name work by what it is.
	if !strings.Contains(message, terms.ItemNaming) {
		t.Errorf("message = %q, want the rule for naming work items", message)
	}
	// The pass decides things inside its lane, so the message tells the
	// instance to make those decisions and report them rather than route them
	// to the operator for approval.
	if !strings.Contains(message, terms.StandingGoals) {
		t.Errorf("message = %q, want the standing goals applied to the pass output and decisions", message)
	}
	if !strings.Contains(message, terms.DecideAndReport) {
		t.Errorf("message = %q, want the rule against routing approvals to the operator", message)
	}
	if rendered := (RecurringSweep{Fired: fired.Fired}).Render(); !strings.Contains(rendered, "carried 1 landing, 2 admissions, 1 stoppage") {
		t.Errorf("rendered = %q, want what the pass carried said on its line", rendered)
	}
}

// A stream that cannot be read is said, keeps its cursor, and costs the other
// stream nothing.
func TestAnUnreadableStreamKeepsItsCursorAndCostsTheOtherNothing(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(0, config.TriggerLandings, config.TriggerAdmissions))
	h.role.answers = []scriptedTurn{{result: complete("read what there was")}}
	h.fire(t, recurringNow)
	tracker := h.cursor(t, runstate.PassStreamTracker)

	h.events.land(recurringNow.Add(time.Minute), "yoyodyne-ifd.1")
	h.events.failing = runstate.PassStreamTracker
	fired := h.fire(t, recurringNow.Add(10*time.Minute))
	if len(fired.Fired) != 1 || fired.Fired[0].Events["landings"] != 1 || !strings.Contains(fired.Fired[0].Problem, "tracker stream could not be read") {
		t.Fatalf("fired = %+v, want the landing carried and the unreadable stream said", fired.Fired)
	}
	if after := h.cursor(t, runstate.PassStreamTracker); !after.Equal(tracker) {
		t.Errorf("tracker cursor = %s, want it left at %s", after, tracker)
	}
	if after := h.cursor(t, runstate.PassStreamRuns); !after.Equal(recurringNow.Add(10 * time.Minute)) {
		t.Errorf("runs cursor = %s, want it moved past the pass", after)
	}
}

// An entry that appears in its stream after a pass whose window already covers
// the moment it says it happened — an item the tracker's export wrote down late
// — is carried by the next pass, and what the first pass carried is not carried
// again.
func TestAnEventThatArrivesLateIsCarriedByTheNextPass(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(0, config.TriggerAdmissions))
	h.role.answers = []scriptedTurn{{result: complete("one admitted")}, {result: complete("the late one")}}
	h.fire(t, recurringNow)

	h.events.events = append(h.events.events, PassEvent{Stream: runstate.PassStreamTracker, Class: config.TriggerAdmissions, At: recurringNow.Add(time.Minute), Key: "yoyodyne-ifd.1", Subject: "yoyodyne-ifd.1"})
	if fired := h.fire(t, recurringNow.Add(10*time.Minute)); len(fired.Fired) != 1 || fired.Fired[0].Events["admissions"] != 1 {
		t.Fatalf("fired = %+v, want the first pass carrying the one admission", fired.Fired)
	}

	// Created before that pass was taken, and only in the stream after it.
	h.events.events = append(h.events.events, PassEvent{Stream: runstate.PassStreamTracker, Class: config.TriggerAdmissions, At: recurringNow.Add(9 * time.Minute), Key: "yoyodyne-ifd.2", Subject: "yoyodyne-ifd.2"})
	fired := h.fire(t, recurringNow.Add(20*time.Minute))
	if len(fired.Fired) != 1 || fired.Fired[0].Events["admissions"] != 1 {
		t.Fatalf("fired = %+v, want the late admission carried by the next pass", fired.Fired)
	}
	last := h.role.messages[len(h.role.messages)-1]
	if !strings.Contains(last, "yoyodyne-ifd.2") || strings.Contains(last, "yoyodyne-ifd.1") {
		t.Errorf("message = %q, want the late admission and not the one already carried", last)
	}
	if again := h.fire(t, recurringNow.Add(40*time.Minute)); len(again.Fired) != 0 {
		t.Errorf("fired = %+v, want nothing carried a second time", again.Fired)
	}
}

// cancellingRole is a session stopped under a pass: the turn is cancelled
// while it is being asked.
type cancellingRole struct{ cancel context.CancelFunc }

func (r cancellingRole) Wake(ctx context.Context, _ domain.AgentRole, _, _, _, _ string, _ RecurringTurnOptions) (Turn, error) {
	r.cancel()
	return Turn{ConversationID: "chat-1"}, ctx.Err()
}

// A pass cancelled under its turn — the watch session carrying it stopped — is
// recorded as a missed pass of the trigger that took it, with the cause, and
// not as a turn the role failed.
func TestAnInstancePassCancelledBeforeItCompletesIsRecordedAsMissed(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.trigger.Roles = cancellingRole{cancel: cancel}
	h.clock.now = recurringNow
	fired, err := h.trigger.Fire(ctx)
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the instance's scheduled pass taken", fired.Fired)
	}
	recorded := h.recorded(t)
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the cancelled pass recorded", recorded)
	}
	missed := recorded[0]
	if missed.Task != instanceName || missed.Missed == nil || missed.Missed.Trigger != runstate.PassTriggerSchedule || missed.Missed.How != runstate.MissCancelled {
		t.Fatalf("recorded = %+v, want a scheduled pass of the instance recorded missed as cancelled", missed)
	}
	if !strings.Contains(missed.Problem, "cancelled before it completed") || !strings.Contains(missed.Problem, "context canceled") {
		t.Errorf("problem = %q, want the cancellation and its cause", missed.Problem)
	}
}

// A pass whose process died carrying it records nothing at all; the next pull
// that considers the instance finds the claim with no ending and records the
// pass as cancelled — once — before it claims the next.
func TestAnInstancePassClaimedAndNeverEndedIsRecordedAsCancelled(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(time.Hour, config.TriggerLandings))
	h.trigger.Conversations = busyConversations{}
	// The killed session's claim: taken by an event wake, and nothing after it.
	if _, err := h.sweeps.Summon(context.Background(), instanceName, recurringNow); err != nil {
		t.Fatalf("Summon() error = %v", err)
	}

	// Inside the grace a claim with no ending may still be a pass being carried.
	h.fire(t, recurringNow.Add(time.Minute))
	if recorded := h.recorded(t); len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want nothing inside the grace", recorded)
	}

	h.role.answers = []scriptedTurn{{result: complete("looked")}}
	h.fire(t, recurringNow.Add(time.Hour))
	recorded := h.recorded(t)
	if len(recorded) != 2 {
		t.Fatalf("recorded = %+v, want the cancelled pass and then the scheduled one", recorded)
	}
	missed := recorded[0]
	if missed.Missed == nil || missed.Missed.Trigger != runstate.PassTriggerEvents || missed.Missed.How != runstate.MissCancelled || !missed.StartedAt.Equal(recurringNow) {
		t.Fatalf("recorded = %+v, want the event wake's pass taken at %s recorded cancelled", missed, recurringNow)
	}
	if !strings.Contains(missed.Problem, "nothing recorded how it ended") {
		t.Errorf("problem = %q, want the cause", missed.Problem)
	}
	if recorded[1].Result == nil || recorded[1].Missed != nil {
		t.Errorf("recorded = %+v, want the next pass taken as usual", recorded[1])
	}

	h.fire(t, recurringNow.Add(2*time.Hour+10*time.Minute))
	cancelled := 0
	for _, entry := range h.recorded(t) {
		if entry.Missed != nil {
			cancelled++
		}
	}
	if cancelled != 1 {
		t.Errorf("recorded %d cancelled passes, want the one", cancelled)
	}
}

// An instance's triggers are read with the recurring tasks, keyed by instance
// and trigger: its schedule as a task's is, and its events from when a wake
// past its cursor could first be taken. A miss of either is recorded as the
// instance's missed pass under the trigger that owed it, and said as the
// harness's report.
func TestAnInstancesTriggersAreCoveredByTheMissedCadence(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(time.Hour, config.TriggerLandings))
	filed := &filedReports{}
	h.trigger.Breakage = filed
	h.trigger.Attribution = report.Attribution{ProductID: "example", RepositoryID: "example"}
	h.role.answers = []scriptedTurn{{result: complete("first")}}
	h.fire(t, recurringNow)
	h.events.land(recurringNow.Add(10*time.Minute), "yoyodyne-ifd.1")

	h.clock.now = recurringNow.Add(20 * time.Minute)
	dues, err := h.trigger.Cadence(context.Background())
	if err != nil {
		t.Fatalf("Cadence() error = %v", err)
	}
	byTrigger := map[runstate.PassTrigger]RecurringDue{}
	for _, due := range dues {
		if !due.Instance || due.Task != instanceName {
			t.Fatalf("due = %+v, want only the instance's", due)
		}
		byTrigger[due.Trigger] = due
	}
	if schedule := byTrigger[runstate.PassTriggerSchedule]; !schedule.At.Equal(recurringNow.Add(time.Hour)) || schedule.Every != time.Hour {
		t.Errorf("schedule due = %+v, want an hour after the last pass", schedule)
	}
	events := byTrigger[runstate.PassTriggerEvents]
	if !events.At.Equal(recurringNow.Add(10*time.Minute+PassSettleWindow)) || events.Every != PassEventMissAfter {
		t.Fatalf("events due = %+v, want from when the landing had settled", events)
	}

	h.clock.now = recurringNow.Add(2 * time.Hour)
	for _, due := range []RecurringDue{byTrigger[runstate.PassTriggerSchedule], events} {
		if err := h.trigger.Missed(context.Background(), RecurringMiss{
			Task: due.Task, Role: due.Role, Every: due.Every, Due: due.At, Trigger: due.Trigger, Instance: due.Instance,
			Why: "no watch session was running to fire it", Severity: report.SeverityWarning,
		}); err != nil {
			t.Fatalf("Missed() error = %v", err)
		}
	}
	var misses []runstate.Sweep
	for _, entry := range h.recorded(t) {
		if entry.Missed != nil {
			misses = append(misses, entry)
		}
	}
	if len(misses) != 2 || misses[0].Missed.Trigger != runstate.PassTriggerSchedule || misses[1].Missed.Trigger != runstate.PassTriggerEvents {
		t.Fatalf("misses = %+v, want one per trigger", misses)
	}
	for _, missed := range misses {
		if missed.Missed.How != runstate.MissUnfired || !strings.Contains(missed.Problem, "missed pass of the program manager instance "+instanceName) || !strings.Contains(missed.Problem, "no watch session was running") {
			t.Errorf("miss = %+v, want the instance's unfired pass with its cause", missed)
		}
	}
	if len(filed.filed) != 2 || !strings.Contains(filed.filed[0].Message, "program manager instance "+instanceName) {
		t.Errorf("filed = %+v, want each miss said as the harness's report", filed.filed)
	}
}
