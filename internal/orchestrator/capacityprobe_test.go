package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// probeInterval is the probe interval these tests configure, the shipped
// execution.usage_limit_unknown_reset_pause.
const probeInterval = 30 * time.Minute

// probeHarness is a pull over items on one opus developer whose provider refused
// it at refusedAt with no reset time, probed on the shipped interval.
func probeHarness(t *testing.T, now, refusedAt time.Time, items ...beads.WorkItem) (*scheduleHarness, *runstate.UsageLimitStore, *runstate.CapacityProbeStore) {
	t.Helper()
	harness := newScheduleHarness(items...)
	harness.now = now
	limits := recordLimit(t, "", "opus", refusedAt, nil)
	probes, err := runstate.NewCapacityProbeStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewCapacityProbeStore() error = %v", err)
	}
	harness.usageLimits = limits
	harness.capacityProbes = probes
	harness.unknownResetProbe = probeInterval
	harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "opus"}}
	return harness, limits, probes
}

// deferredReason is what a pass said of one item it passed over.
func deferredReason(schedule Schedule, id string) string {
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID == id {
			return deferred.Reason
		}
	}
	return ""
}

// A refusal that named no reset holds fresh dispatch to its model until the
// next probe is due, one interval after the refusal: every item waits, keeps its
// place, and is named as waiting for provider capacity with that probe time.
func TestARefusalWithNoResetHoldsDispatchUntilItsProbe(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	refused := now.Add(-5 * time.Minute)
	harness, _, _ := probeHarness(t, now, refused, readyItems("yoyodyne-one", "yoyodyne-two")...)

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 0 {
		t.Fatalf("started = %v, want nothing before the probe is due", order)
	}
	next := refused.Add(probeInterval).Format(time.RFC3339)
	said := deferredReason(schedule, "yoyodyne-one")
	if !strings.Contains(said, "waiting for provider capacity") || !strings.Contains(said, "no reset time") || !strings.Contains(said, next) {
		t.Fatalf("passed over yoyodyne-one with %q, want it waiting for provider capacity with the next probe at %s", said, next)
	}
}

// Once the probe is due, one run is sent as the probe and nothing else on the
// same model is sent beside it in that pull, or in the pulls after it, until
// the interval has passed again: one invocation per interval, never one per
// pull. The probe is recorded, with what was sent.
func TestOneProbeIsSentPerInterval(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	harness, _, probes := probeHarness(t, now, now.Add(-probeInterval-time.Minute), readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 2

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 1 || order[0] != "yoyodyne-one" {
		t.Fatalf("started = %v, want only the first item, sent as the probe", order)
	}
	if len(schedule.CapacityProbes) != 1 || schedule.CapacityProbes[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("probes sent = %+v, want the one run of yoyodyne-one", schedule.CapacityProbes)
	}
	recorded, err := probes.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Model != "opus" || !recorded[0].At.Equal(now) || !strings.Contains(recorded[0].What, "yoyodyne-one") {
		t.Fatalf("probe record = %+v, want opus probed at %s by the run of yoyodyne-one", recorded, now)
	}
	next := now.Add(probeInterval).Format(time.RFC3339)
	if said := deferredReason(schedule, "yoyodyne-two"); !strings.Contains(said, next) {
		t.Fatalf("passed over yoyodyne-two with %q, want it waiting for the next probe at %s", said, next)
	}
}

// A probe the provider serves is the window reopening: the served turn lifts
// the refusal, and the work that was waiting is pulled at once.
func TestASuccessfulProbeReleasesTheWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	harness, _, _ := probeHarness(t, now, now.Add(-probeInterval-time.Minute), readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	served, err := runstate.NewCapacityServedStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewCapacityServedStore() error = %v", err)
	}
	harness.capacityServed = served
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if err := served.Record(runstate.CapacityServed{Model: "opus", At: h.clock().Add(time.Second), What: "a developer attempt of " + id}); err != nil {
			t.Errorf("Record() error = %v", err)
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusSucceeded}, nil
	}

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 3 {
		t.Fatalf("started = %v, want the probe and then the rest of the queue once it was served", order)
	}
	if len(schedule.CapacityProbes) != 1 {
		t.Fatalf("probes sent = %+v, want exactly one", schedule.CapacityProbes)
	}
}

// A probe the provider refuses again extends the wait: its refusal is the new
// moment the next probe is paced from, and nothing more is sent before it.
func TestARefusedProbeExtendsTheWait(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	harness, limits, _ := probeHarness(t, now, now.Add(-probeInterval-time.Minute), readyItems("yoyodyne-one", "yoyodyne-two")...)
	refusedAgain := now.Add(2 * time.Minute)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if err := limits.Record(runstate.UsageLimitExhaustion{
			SchemaVersion: runstate.UsageLimitSchemaVersion, ProductID: "yoyodyne", At: refusedAgain,
			Waiting: "run run-" + id + " of " + id, WorkItemID: id, Model: "opus",
		}); err != nil {
			t.Errorf("Record() error = %v", err)
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
	}

	schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 1 {
		t.Fatalf("started = %v, want only the probe", order)
	}
	next := refusedAgain.Add(probeInterval).Format(time.RFC3339)
	if said := deferredReason(schedule, "yoyodyne-two"); !strings.Contains(said, next) {
		t.Fatalf("passed over yoyodyne-two with %q, want the wait extended to %s by the refused probe", said, next)
	}
}

// The probe schedule is durable: a session started inside the interval after
// another session's probe sends nothing until the interval has passed, and then
// sends exactly one.
func TestTheProbeScheduleSurvivesARestart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	first, limits, probes := probeHarness(t, now, now.Add(-probeInterval-time.Minute), readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	if _, err := (Scheduler{Open: first.open, Now: first.clock}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := first.pullOrder(); len(order) != 1 {
		t.Fatalf("first session started %v, want only the probe", order)
	}

	// A fresh session, remembering nothing, ten minutes later.
	restarted := func(at time.Time) *scheduleHarness {
		harness := newScheduleHarness(readyItems("yoyodyne-two", "yoyodyne-three")...)
		harness.now = at
		harness.usageLimits = limits
		harness.capacityProbes = probes
		harness.unknownResetProbe = probeInterval
		harness.developers = first.developers
		harness.capacity = 2
		return harness
	}
	early := restarted(now.Add(10 * time.Minute))
	schedule, err := Scheduler{Open: early.open, Now: early.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := early.pullOrder(); len(order) != 0 {
		t.Fatalf("restarted session started %v inside the interval, want nothing", order)
	}
	next := now.Add(probeInterval).Format(time.RFC3339)
	if said := deferredReason(schedule, "yoyodyne-two"); !strings.Contains(said, next) {
		t.Fatalf("restarted session passed over yoyodyne-two with %q, want the next probe at %s read from the record", said, next)
	}

	late := restarted(now.Add(probeInterval + time.Minute))
	if _, err := (Scheduler{Open: late.open, Now: late.clock}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := late.pullOrder(); len(order) != 1 {
		t.Fatalf("restarted session started %v once the interval passed, want exactly one probe", order)
	}
}

// A limit the provider names as one over the whole account — five_hour,
// seven_day — is shared by every model on it: a refusal met on opus holds the
// work mapped to sonnet too, and both are named as waiting.
func TestAnAccountWideWindowHoldsEveryModelInIt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	onOpus := beads.WorkItem{ID: "yoyodyne-ifd.401", Title: "on opus", Status: "open", Priority: 0}
	onSonnet := beads.WorkItem{ID: "yoyodyne-ifd.402", Title: "docs", Status: "open", Priority: 2, Labels: []string{"docs"}}
	for _, tc := range []struct {
		name     string
		resetsAt *time.Time
		until    time.Time
	}{
		{name: "named reset", resetsAt: timePointer(now.Add(3 * time.Hour)), until: now.Add(3 * time.Hour)},
		{name: "no reset", until: now.Add(-5 * time.Minute).Add(probeInterval)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness := newScheduleHarness(onOpus, onSonnet)
			harness.now = now
			harness.capacity = 2
			harness.usageLimits = recordLimit(t, "five_hour", "opus", now.Add(-5*time.Minute), tc.resetsAt)
			harness.unknownResetProbe = probeInterval
			harness.developers = []readmodel.AgentEndpoint{
				{Name: "developer", Provider: "claude-code", Model: "opus"},
				{Name: "developer (docs)", Provider: "claude-code", Model: "sonnet"},
			}
			harness.developerModels = []config.DeveloperModelRule{{Label: "docs", Model: "sonnet"}}

			schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if order := harness.pullOrder(); len(order) != 0 {
				t.Fatalf("started = %v, want nothing while the account's window is closed", order)
			}
			said := deferredReason(schedule, onSonnet.ID)
			if !strings.Contains(said, "waiting for provider capacity") || !strings.Contains(said, "sonnet") || !strings.Contains(said, tc.until.Format(time.RFC3339)) {
				t.Fatalf("passed over %s with %q, want it waiting on the shared window until %s", onSonnet.ID, said, tc.until.Format(time.RFC3339))
			}
		})
	}
}

func timePointer(at time.Time) *time.Time { return &at }

// A developer slot that routes to an endpoint pair of its own is held by a
// refusal recorded against that pair's account and model, and only by such a
// refusal: the same model refused on another account holds nothing there.
func TestASlotRoutedEndpointIsHeldByItsOwnRefusal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	lifts := now.Add(4 * time.Hour)
	for _, tc := range []struct {
		name    string
		account string
		held    bool
	}{
		{name: "refused on the slot's account", account: "second", held: true},
		{name: "refused on another account", account: "first", held: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatalf("NewUsageLimitStore() error = %v", err)
			}
			if err := store.Record(runstate.UsageLimitExhaustion{
				SchemaVersion: runstate.UsageLimitSchemaVersion, ProductID: "yoyodyne", At: now.Add(-time.Hour),
				Waiting: "run run-1 of yoyodyne-ifd.400", Kind: "seven_day_opus", ResetsAt: &lifts,
				WorkItemID: "yoyodyne-ifd.400", Model: "opus", AccountAlias: tc.account,
			}); err != nil {
				t.Fatalf("Record() error = %v", err)
			}
			harness := newScheduleHarness(readyItems("yoyodyne-ifd.401")...)
			harness.now = now
			harness.usageLimits = store
			harness.developers = []readmodel.AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: "sonnet"}}
			harness.slots = []domain.DeveloperSlot{{Routing: &domain.EndpointPair{
				Enabled: true,
				Primary: &domain.EndpointSpec{Provider: "claude-code", Model: "opus", Account: "second"},
			}}}

			schedule, err := Scheduler{Open: harness.open, Now: harness.clock}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			started := len(harness.pullOrder()) == 1
			if started == tc.held {
				t.Fatalf("started = %v, want held %v", harness.pullOrder(), tc.held)
			}
			if said := deferredReason(schedule, "yoyodyne-ifd.401"); tc.held && !strings.Contains(said, lifts.Format(time.RFC3339)) {
				t.Fatalf("passed over the item with %q, want it waiting on the slot's window until %s", said, lifts.Format(time.RFC3339))
			}
		})
	}
}
