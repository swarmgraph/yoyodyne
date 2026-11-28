package orchestrator

// The passes events of a named epic start while epic mode is on, in place of the
// clock: docs/designs/epic-mode-second-part.md, "When each role runs: piece 13".

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicwake"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// countedRole answers every turn complete, at a cost, and keeps what it was asked.
type countedRole struct {
	roles    []domain.AgentRole
	messages []string
}

func (r *countedRole) Wake(_ context.Context, role domain.AgentRole, _, _, _, message string, _ RecurringTurnOptions) (Turn, error) {
	r.roles = append(r.roles, role)
	r.messages = append(r.messages, message)
	return Turn{ConversationID: "chat-" + string(role), CostUSD: 0.5, Result: complete("looked")}, nil
}

// wakeSource is the harness's events over a real record of them: the events owed
// are whatever the test says.
type wakeSource struct {
	store *epicwake.Store
	owed  []epicwake.Event
	off   bool
}

func (w *wakeSource) Owed(context.Context) (EpicWakeDue, error) {
	if w.off {
		return EpicWakeDue{}, nil
	}
	due, err := w.store.Owed(w.owed)
	return EpicWakeDue{On: true, Due: due}, err
}

func (w *wakeSource) Woken(role domain.AgentRole, pass string, events []epicwake.Kept) error {
	return w.store.Woken(role, pass, events)
}

func (w *wakeSource) Ended(pass string, fired Fired) error {
	return w.store.Ended(pass, fired.CostUSD, EpicWakeKnown(fired))
}

// wakeBench is epic mode on with one open epic, a task for each of the three
// roles and a program manager instance, each due every hour by the clock.
type wakeBench struct {
	passEpics
	clock      *movingRecurringClock
	role       *countedRole
	source     *wakeSource
	pile       *memoryPile
	trigger    Trigger
	detections int
}

var epicNaming = epicprogress.Naming{WorkItemID: namedEpic, NamedAt: baseTime}

func newWakeBench(t *testing.T) *wakeBench {
	t.Helper()
	b := &wakeBench{passEpics: newPassEpics(t), clock: &movingRecurringClock{now: recurringNow}, role: &countedRole{}, pile: &memoryPile{}}
	store, err := epicwake.Open(b.root, "yoyodyne", func() time.Time { return b.clock.now })
	if err != nil {
		t.Fatal(err)
	}
	b.source = &wakeSource{store: store}
	tasks := map[string]config.RecurringTask{}
	for _, role := range epicwake.Roles {
		tasks[string(role)] = config.RecurringTask{Role: role, Every: config.Duration(time.Hour), Enabled: true, Prompt: "look", MaxTurns: 1}
	}
	sweeps := sweepStore(t)
	b.trigger = Trigger{Tasks: tasks, Claims: sweeps, Reports: sweeps, Roles: b.role, Clock: b.clock, Instances: instance(time.Hour),
		EpicWakes: b.source, Pile: b.pile,
		RecordFailures: func(context.Context) error { b.detections++; return nil }}
	b.wire(&b.trigger)
	return b
}

// owe makes the events owed from now on.
func (b *wakeBench) owe(events ...epicwake.Event) { b.source.owed = events }

// pull takes one pull at the moment, takes every pass it starts, and answers the
// roles woken, in order.
func (b *wakeBench) pull(at time.Time) []domain.AgentRole {
	b.t.Helper()
	b.clock.now = at
	start, err := b.trigger.Start(context.Background(), nil, MaxConcurrentFirings)
	if err != nil {
		b.t.Fatal(err)
	}
	var woken []domain.AgentRole
	for _, started := range start.Started {
		started.Take(context.Background())
		woken = append(woken, started.Role)
	}
	slices.Sort(woken)
	return woken
}

// day pulls every ten minutes from the moment for the span and counts the passes
// of each role.
func (b *wakeBench) day(from time.Time, span time.Duration) map[domain.AgentRole]int {
	b.t.Helper()
	woken := map[domain.AgentRole]int{}
	for at := from; at.Before(from.Add(span)); at = at.Add(10 * time.Minute) {
		for _, role := range b.pull(at) {
			woken[role]++
		}
	}
	return woken
}

func event(key string, role domain.AgentRole) epicwake.Event {
	return epicwake.Event{Key: key, Role: role, Namings: []epicprogress.Naming{epicNaming}, Says: "epic " + namedEpic + ": " + key}
}

// Each role an event owes is woken by it, as the role's own task out of its
// cadence and told what waits on it; with no event, a day of pulls starts nobody,
// a program manager's pass included, and no pass starts by the clock.
func TestAnEventOfAnOpenEpicStartsThePassOfTheRoleThatOwesIt(t *testing.T) {
	t.Parallel()
	for _, role := range epicwake.Roles {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			b := newWakeBench(t)
			if woken := b.day(recurringNow, 24*time.Hour); len(woken) != 0 {
				t.Fatalf("with nothing owed a day of pulls woke %v", woken)
			}
			at := recurringNow.Add(24 * time.Hour)
			b.owe(event("the step "+string(role)+" owes", role))
			if woken := b.pull(at); len(woken) != 0 {
				t.Fatalf("woke %v before the ten minutes of gathering", woken)
			}
			if woken := b.pull(at.Add(epicwake.Gather)); !slices.Equal(woken, []domain.AgentRole{role}) {
				t.Fatalf("woke %v, want the %s once", woken, role)
			}
			message := b.role.messages[len(b.role.messages)-1]
			if !strings.Contains(message, "the step "+string(role)+" owes") || !strings.Contains(message, "not by the clock") {
				t.Fatalf("the pass was told:\n%s", message)
			}
			if slices.Contains(b.role.roles, domain.RoleProgramManager) {
				t.Fatal("a program manager's pass started")
			}
		})
	}
}

// Events that come within ten minutes cost one pass; one a pass leaves standing
// comes back after an hour; after two such passes it goes to the Lead Product
// Manager, and after two of hers to status, where nothing more starts for it.
func TestEventsArePacedGatheredRepeatedAndMovedUp(t *testing.T) {
	t.Parallel()
	b := newWakeBench(t)
	b.owe(event("a stopped member", domain.RoleDevelopmentManager))
	b.pull(recurringNow)
	b.owe(event("a stopped member", domain.RoleDevelopmentManager), event("a piece to write", domain.RoleDevelopmentManager))
	woken := b.day(recurringNow.Add(5*time.Minute), 6*time.Hour)
	if woken[domain.RoleDevelopmentManager] != 2 || woken[domain.RoleProductManager] != 2 || len(woken) != 2 {
		t.Fatalf("passes over six hours = %v, want two of the development manager's and then two of the Lead Product Manager's", woken)
	}
	first := b.role.messages[0]
	if !strings.Contains(first, "a stopped member") || !strings.Contains(first, "a piece to write") {
		t.Fatalf("the first pass did not carry both events gathered together:\n%s", first)
	}
	if moved := b.role.messages[2]; !strings.Contains(moved, "it waited on the development manager through 2 passes") {
		t.Fatalf("the Lead Product Manager was not told the event moved up to her:\n%s", moved)
	}
	record, err := epicwake.Read(b.root, "yoyodyne")
	if err != nil || len(record.Raised()) != 2 {
		t.Fatalf("raised = %+v, %v; want both events put on status", record.Raised(), err)
	}
	if more := b.day(recurringNow.Add(7*time.Hour), 6*time.Hour); len(more) != 0 {
		t.Fatalf("after the problem went to status, passes = %v", more)
	}
	// Settled, it is gone.
	b.owe()
	b.pull(recurringNow.Add(14 * time.Hour))
	if record, err = epicwake.Read(b.root, "yoyodyne"); err != nil || len(record.Events) != 0 {
		t.Fatalf("events = %+v, %v; want none once settled", record.Events, err)
	}
}

// Every epic waiting on the operator owes no role anything, so a day starts no
// session; his answer, a critical report or an automatic hold starts one. The
// operator's own conversation is never held, and the harness's own detection
// runs at every pull.
func TestADayWaitingOnTheOperatorStartsNothingUntilSomethingIsOwed(t *testing.T) {
	t.Parallel()
	b := newWakeBench(t)
	pulls := 0
	for at := recurringNow; at.Before(recurringNow.Add(24 * time.Hour)); at = at.Add(10 * time.Minute) {
		b.pull(at)
		pulls++
	}
	if len(b.role.roles) != 0 {
		t.Fatalf("a day waiting on the operator woke %v", b.role.roles)
	}
	if b.detections != pulls {
		t.Fatalf("the harness's own detection ran %d times in %d pulls", b.detections, pulls)
	}
	if !b.ask(maystart.OperatorConversation()).MayStart() {
		t.Fatal("the operator's own conversation was held")
	}
	if err := b.trigger.MaySummon(BrakeSummons{}); err != nil {
		t.Fatalf("an automatic hold may not summon the development manager: %v", err)
	}
	// A critical report waiting on a program manager, naming no member, is
	// delivered to the Lead Product Manager at the next pull.
	at := recurringNow.Add(24 * time.Hour)
	b.pile.reports = []report.Report{{ID: "report-lane", Role: domain.RoleProgramManager, For: domain.RoleProgramManager, Severity: report.SeverityCritical, Message: "the lane stopped", RecordedAt: at}}
	if woken := b.pull(at); !slices.Equal(woken, []domain.AgentRole{domain.RoleProductManager}) {
		t.Fatalf("a critical report woke %v, want the Lead Product Manager only", woken)
	}
	// His answer leaves her a step, which is an event.
	b.owe(event("the operator answered: a step of hers", domain.RoleProductManager))
	b.pull(at.Add(time.Minute))
	if woken := b.pull(at.Add(time.Minute + epicwake.Gather)); !slices.Equal(woken, []domain.AgentRole{domain.RoleProductManager}) {
		t.Fatalf("the answer woke %v, want the Lead Product Manager", woken)
	}
}

// While epic mode is on a critical report waiting on the development manager or
// the architect is that role's event, and is not delivered to the Lead Product
// Manager as well; with it off, it is delivered to her as always.
func TestACriticalReportForAnotherRoleIsLeftToThatRole(t *testing.T) {
	t.Parallel()
	pending := []report.Report{
		{ID: "report-dm", For: domain.RoleDevelopmentManager, Severity: report.SeverityCritical},
		{ID: "report-arch", For: domain.RoleArchitect, Severity: report.SeverityCritical},
		{ID: "report-pm", For: domain.RoleProgramManager, Severity: report.SeverityCritical},
		{ID: "report-lead", Severity: report.SeverityCritical},
	}
	ids := func(reports []report.Report) []string {
		var ids []string
		for _, reported := range reports {
			ids = append(ids, reported.ID)
		}
		return ids
	}
	if got := ids(leftToTheirRoles(pending, true)); !slices.Equal(got, []string{"report-pm", "report-lead"}) {
		t.Fatalf("with epic mode on she is delivered %v", got)
	}
	if got := ids(leftToTheirRoles(pending, false)); len(got) != 4 {
		t.Fatalf("with epic mode off she is delivered %v", got)
	}
	b := newWakeBench(t)
	b.pile.reports = pending[:1]
	b.owe(epicwake.Event{Key: "report:report-dm", Role: domain.RoleDevelopmentManager, Says: "report report-dm"})
	if woken := b.pull(recurringNow); len(woken) != 0 {
		t.Fatalf("woke %v at once, want her critical left to the development manager", woken)
	}
	if woken := b.pull(recurringNow.Add(epicwake.Gather)); !slices.Equal(woken, []domain.AgentRole{domain.RoleDevelopmentManager}) {
		t.Fatalf("woke %v, want the development manager once", woken)
	}
}

// What a pass an event started cost is written against its epics, shared equally
// where it was for two.
func TestAPassAnEventWokeIsChargedToItsEpics(t *testing.T) {
	t.Parallel()
	b := newWakeBench(t)
	other := epicprogress.Naming{WorkItemID: "yoyodyne-other", NamedAt: baseTime}
	shared := event("a report about both", domain.RoleArchitect)
	shared.Namings = append(shared.Namings, other)
	b.owe(shared)
	b.pull(recurringNow)
	b.pull(recurringNow.Add(epicwake.Gather))
	record, err := epicwake.Read(b.root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	for _, naming := range []epicprogress.Naming{epicNaming, other} {
		if usd, passes, unknown := record.Cost(naming); passes != 1 || unknown != 0 || usd != 0.25 {
			t.Fatalf("%s: $%.2f over %d passes, %d unknown; want half of $0.50 over one", naming.WorkItemID, usd, passes, unknown)
		}
	}
}

// Turning epic mode off brings the passes by the clock back.
func TestWithEpicModeOffTheClockStartsThePassesAgain(t *testing.T) {
	t.Parallel()
	b := newWakeBench(t)
	if woken := b.pull(recurringNow); len(woken) != 0 {
		t.Fatalf("woke %v by the clock with epic mode on", woken)
	}
	b.off()
	b.source.off = true
	if woken := b.pull(recurringNow.Add(time.Minute)); len(woken) != 4 {
		t.Fatalf("woke %v with epic mode off, want the three roles and the program manager", woken)
	}
}
