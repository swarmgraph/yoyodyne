package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// opusRoles is every role asking for opus with no alternate.
func opusRoles(domain.AgentRole, string) (readmodel.AgentEndpoint, bool) {
	return readmodel.AgentEndpoint{Name: "agent", Provider: "claude-code", Model: "opus"}, true
}

// conversationRefusal records the refusal a role's conversation turn leaves in
// the usage-limit log, with the reset the provider named or none.
func conversationRefusal(t *testing.T, at time.Time, resetsAt *time.Time) *runstate.UsageLimitStore {
	t.Helper()
	store, err := runstate.NewUsageLimitStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	if err := store.Record(runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion, ProductID: "example", At: at,
		Waiting: "the development manager conversation chat-1", Kind: "seven_day_opus",
		ResetsAt: resetsAt, ConversationID: "chat-1", Model: "opus",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return store
}

// The recurrence this was written for: a role's scheduled turn was refused with
// a reset the provider named, and the turn was attempted again every couple of
// minutes until it. A turn refused with a stated reset is not attempted before
// that reset: nothing is claimed, the role is asked nothing, and the firing is
// shown waiting for provider capacity with the time it is next tried.
func TestARecurringTurnRefusedWithAStatedResetWaitsForIt(t *testing.T) {
	t.Parallel()

	resets := recurringNow.Add(11 * time.Hour)
	store := sweepStore(t)
	role := &wokenRole{}
	trigger := Trigger{
		Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{},
		UsageLimits: conversationRefusal(t, recurringNow.Add(-2*time.Minute), &resets),
		Endpoint:    opusRoles, UnknownResetProbe: 30 * time.Minute,
	}

	start, err := trigger.Start(context.Background(), nil, MaxConcurrentFirings)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(start.Started) != 0 || len(start.Settled) != 0 || len(role.messages) != 0 {
		t.Fatalf("start = %+v, want no turn attempted before the reset", start)
	}
	if len(start.Waiting) != 1 || !start.Waiting[0].NextTry.Equal(resets) ||
		!strings.Contains(start.Waiting[0].Capacity, "waiting for provider capacity") ||
		!strings.Contains(start.Waiting[0].Capacity, resets.Format(time.RFC3339)) {
		t.Fatalf("waiting = %+v, want the firing shown waiting for provider capacity until %s", start.Waiting, resets)
	}
	if said := waitingReason(start.Waiting[0], newRecurringFirings(context.Background())); !strings.Contains(said, resets.Format(time.RFC3339)) {
		t.Fatalf("waiting reason = %q, want the next try named", said)
	}
	if recorded, _, err := store.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("recorded = %+v (err %v), want nothing claimed or recorded while it waits", recorded, err)
	}
}

// A turn refused with no reset is retried on the paced probe interval: not
// before it, and, once it has passed, as one probe — which is recorded, so a
// second role on the same model is not sent into the window beside it.
func TestARecurringTurnRefusedWithNoResetIsRetriedOnTheProbeInterval(t *testing.T) {
	t.Parallel()

	probes, err := runstate.NewCapacityProbeStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewCapacityProbeStore() error = %v", err)
	}
	refused := recurringNow.Add(-10 * time.Minute)
	limits := conversationRefusal(t, refused, nil)
	clock := &movingRecurringClock{now: recurringNow}
	store := sweepStore(t)
	role := &wokenRole{}
	trigger := Trigger{
		Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: clock,
		UsageLimits: limits, CapacityProbes: probes, Endpoint: opusRoles, UnknownResetProbe: 30 * time.Minute,
	}

	early, err := trigger.Start(context.Background(), nil, MaxConcurrentFirings)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(early.Started) != 0 || len(early.Waiting) != 1 || !early.Waiting[0].NextTry.Equal(refused.Add(30*time.Minute)) {
		t.Fatalf("start before the interval = %+v, want the firing waiting until %s", early, refused.Add(30*time.Minute))
	}

	clock.now = refused.Add(31 * time.Minute)
	due, err := trigger.Start(context.Background(), nil, MaxConcurrentFirings)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(due.Started) != 1 {
		t.Fatalf("start past the interval = %+v, want the one turn sent as the probe", due)
	}
	recorded, err := probes.List()
	if err != nil || len(recorded) != 1 || recorded[0].Model != "opus" || !recorded[0].At.Equal(clock.now) {
		t.Fatalf("probe record = %+v (err %v), want opus probed at %s", recorded, err, clock.now)
	}
}
