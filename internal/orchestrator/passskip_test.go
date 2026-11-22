package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// changingSources is what the check reads, as a test sets it: a digest for
// each kind, which stays "unchanged" until the test moves it, the kinds that
// could not be read, and how many health conditions stand.
type changingSources struct {
	digests    map[PassChangeKind]string
	unread     map[PassChangeKind]string
	conditions int
	scopes     []PassScope
}

func (s *changingSources) Fingerprints(_ context.Context, scope PassScope, kinds []PassChangeKind) PassFingerprints {
	s.scopes = append(s.scopes, scope)
	reading := PassFingerprints{Digests: map[PassChangeKind]string{}, Unread: map[PassChangeKind]string{}, HealthConditions: s.conditions}
	for _, kind := range kinds {
		if why, unread := s.unread[kind]; unread {
			reading.Unread[kind] = why
			continue
		}
		digest := s.digests[kind]
		if digest == "" {
			digest = "unchanged"
		}
		reading.Digests[kind] = digest
	}
	return reading
}

func (s *changingSources) move(kind PassChangeKind) {
	if s.digests == nil {
		s.digests = map[PassChangeKind]string{}
	}
	s.digests[kind] += "-moved"
}

// skipHarness is one recurring task whose role answers every turn, with the
// check's sources and a clock the test moves.
type skipHarness struct {
	trigger Trigger
	store   *runstate.SweepStore
	role    *wokenRole
	sources *changingSources
	clock   *movingRecurringClock
}

func newSkipHarness(t *testing.T, tasks map[string]config.RecurringTask) *skipHarness {
	t.Helper()
	store := sweepStore(t)
	h := &skipHarness{
		store:   store,
		role:    &wokenRole{},
		sources: &changingSources{},
		clock:   &movingRecurringClock{now: recurringNow},
	}
	h.trigger = Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: h.role, Changes: h.sources, Clock: h.clock}
	return h
}

func (h *skipHarness) fire(t *testing.T, at time.Time) RecurringSweep {
	t.Helper()
	h.clock.now = at
	h.role.answers = append(h.role.answers, scriptedTurn{result: complete("looked")})
	fired, err := h.trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() at %s error = %v", at.Format(time.RFC3339), err)
	}
	return fired
}

func (h *skipHarness) recorded(t *testing.T) []runstate.Sweep {
	t.Helper()
	recorded, _, err := h.store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	return recorded
}

func started(fired RecurringSweep) bool {
	return len(fired.Fired) == 1 && fired.Fired[0].Turns > 0 && fired.Fired[0].Skipped == ""
}

func skipped(fired RecurringSweep) bool {
	return len(fired.Fired) == 1 && fired.Fired[0].Turns == 0 && fired.Fired[0].Skipped != ""
}

// A scheduled firing with nothing new since the task's last completed pass is
// not started: the role is asked nothing, the firing is recorded as skipped
// naming that pass, and it is the one firing of its window.
func TestAScheduledPassWithNothingNewIsSkippedAndRecordedAsTheWindowsFiring(t *testing.T) {
	t.Parallel()

	h := newSkipHarness(t, hourlyTask("sweep"))
	if first := h.fire(t, recurringNow); !started(first) {
		t.Fatalf("first fired = %+v, want the first pass started, there being nothing to compare it with", first.Fired)
	}
	recorded := h.recorded(t)
	if len(recorded) != 1 || len(recorded[0].Fingerprints) == 0 {
		t.Fatalf("recorded = %+v, want the completed pass to keep what it was handed", recorded)
	}

	second := h.fire(t, recurringNow.Add(time.Hour))
	if !skipped(second) {
		t.Fatalf("second fired = %+v, want the pass skipped", second.Fired)
	}
	if len(h.role.messages) != 1 {
		t.Fatalf("the role was asked %d times, want once: a skipped pass asks nothing", len(h.role.messages))
	}
	if rendered := second.Render(); !strings.Contains(rendered, "the recurring task a-sweep was skipped: nothing new since the last completed pass") {
		t.Errorf("Render() = %q, want the skip said in words", rendered)
	}
	recorded = h.recorded(t)
	if len(recorded) != 2 {
		t.Fatalf("recorded = %+v, want the skipped firing on the record", recorded)
	}
	skip := recorded[1]
	if skip.Skipped == nil || skip.Turns != 0 || skip.Result != nil || skip.Problem != "" || skip.CostUSD != 0 {
		t.Fatalf("skipped record = %+v, want a skip that took no turn, spent nothing and is no failure", skip)
	}
	if !skip.Skipped.LastCompletedAt.Equal(recurringNow) || !strings.Contains(skip.Skipped.LastCompleted, "a-sweep") {
		t.Errorf("skipped = %+v, want it to name the completed pass at %s", skip.Skipped, recurringNow)
	}
	for _, said := range []string{"nothing new since the last completed pass", "the pass of a-sweep that started at", "changes landed on the target branch", "its list of stopped runs", "a full pass is taken by"} {
		if !strings.Contains(skip.Skipped.Reason, said) {
			t.Errorf("reason = %q, want it to say %q", skip.Skipped.Reason, said)
		}
	}
	if !skip.Skipped.FullPassBy.Equal(recurringNow.Add(config.DefaultFullPassEvery)) {
		t.Errorf("full pass by = %s, want four hours after the completed pass", skip.Skipped.FullPassBy)
	}
	// The skip took the window: nothing more fires until the cadence passes.
	if again := h.fire(t, recurringNow.Add(70*time.Minute)); len(again.Fired) != 0 {
		t.Fatalf("fired = %+v, want nothing more in the window the skip took", again.Fired)
	}
}

// A change to any one thing the check reads starts the pass.
func TestEachThingTheCheckReadsStartsThePassWhenItChanges(t *testing.T) {
	t.Parallel()

	for _, kind := range []PassChangeKind{ChangeLanded, ChangeWork, ChangeAddressed, ChangeProposals, ChangeMessages, ChangeDocuments, ChangeFollowUps, ChangeStops} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			h := newSkipHarness(t, hourlyTask("sweep"))
			h.fire(t, recurringNow)
			h.sources.move(kind)
			if fired := h.fire(t, recurringNow.Add(time.Hour)); !started(fired) {
				t.Fatalf("fired = %+v, want a change to %s to start the pass", fired.Fired, kind)
			}
		})
	}
}

// What the development manager's pass reads, beside what every pass reads,
// is her list of stopped runs and the factory's health; a program manager's is
// its lane, and the health only where its lane covers it.
func TestWhatTheCheckReadsIsHeldForEachRole(t *testing.T) {
	t.Parallel()

	every := []PassChangeKind{ChangeLanded, ChangeWork, ChangeAddressed, ChangeProposals, ChangeMessages, ChangeDocuments, ChangeFollowUps}
	has := func(kinds []PassChangeKind, want PassChangeKind) bool {
		for _, kind := range kinds {
			if kind == want {
				return true
			}
		}
		return false
	}
	for _, scope := range []PassScope{{Role: domain.RoleArchitect}, {Role: domain.RoleProductManager}, {Role: domain.RoleDevelopmentManager, Health: true}, {Role: domain.RoleProgramManager, Agent: "pm", Lane: "factory-flow", Health: true}} {
		kinds := passChangeKinds(scope)
		for _, want := range every {
			if !has(kinds, want) {
				t.Errorf("%s reads %v, want %s among them", scope.Role, kinds, want)
			}
		}
		if got := has(kinds, ChangeStops); got != (scope.Role == domain.RoleDevelopmentManager) {
			t.Errorf("%s reads stopped runs = %v, want only the development manager to", scope.Role, got)
		}
		if got := has(kinds, ChangeHealth); got != scope.Health {
			t.Errorf("%s reads health = %v, want %v", scope.Role, got, scope.Health)
		}
	}
}

// The reports addressed to the role and the changes proposed to its documents
// are read through the trigger's own pile and amendment log: a new one starts
// the pass.
func TestANewReportOrProposalStartsThePass(t *testing.T) {
	t.Parallel()

	t.Run("report", func(t *testing.T) {
		t.Parallel()
		h := newSkipHarness(t, hourlyTask("sweep"))
		pile := &memoryPile{}
		h.trigger.Pile = pile
		h.fire(t, recurringNow)
		if fired := h.fire(t, recurringNow.Add(time.Hour)); !skipped(fired) {
			t.Fatalf("fired = %+v, want the pass skipped while no report is addressed to her", fired.Fired)
		}
		addressed := filedReport("report-1", domain.RoleArchitect, report.SeverityWarning, "look at this", recurringNow.Add(90*time.Minute))
		addressed.For = domain.RoleDevelopmentManager
		pile.reports = append(pile.reports, addressed)
		if fired := h.fire(t, recurringNow.Add(2*time.Hour)); !started(fired) {
			t.Fatalf("fired = %+v, want a report addressed to her to start the pass", fired.Fired)
		}
	})
	t.Run("proposal", func(t *testing.T) {
		t.Parallel()
		tasks := architectTask()
		task := tasks["architect-amendments"]
		task.Every = config.Duration(time.Hour)
		tasks["architect-amendments"] = task
		h := newSkipHarness(t, tasks)
		proposal := proposedToTheArchitect(1, time.Hour)
		amendments := &recurringAmendmentsLog{}
		h.trigger.Amendments = amendments
		h.fire(t, recurringNow)
		if fired := h.fire(t, recurringNow.Add(time.Hour)); !skipped(fired) {
			t.Fatalf("fired = %+v, want the pass skipped while nothing is proposed", fired.Fired)
		}
		amendments.records = append(amendments.records, amendment.Record{Proposal: &proposal})
		if fired := h.fire(t, recurringNow.Add(2*time.Hour)); !started(fired) {
			t.Fatalf("fired = %+v, want a change proposed to her documents to start the pass", fired.Fired)
		}
	})
}

// recurringAmendmentsLog is an amendment log a test adds to between firings.
type recurringAmendmentsLog struct{ records []amendment.Record }

func (a *recurringAmendmentsLog) List() ([]amendment.Record, error) { return a.records, nil }

// Every role has a full pass at least every full_pass_every, whatever the
// check finds, measured from the last completed pass.
func TestAFullPassIsTakenAtTheFloorWhateverTheCheckFinds(t *testing.T) {
	t.Parallel()

	h := newSkipHarness(t, hourlyTask("sweep"))
	h.fire(t, recurringNow)
	for hour := 1; hour < 4; hour++ {
		if fired := h.fire(t, recurringNow.Add(time.Duration(hour)*time.Hour)); !skipped(fired) {
			t.Fatalf("fired at +%dh = %+v, want the pass skipped inside the floor", hour, fired.Fired)
		}
	}
	if fired := h.fire(t, recurringNow.Add(4*time.Hour)); !started(fired) {
		t.Fatalf("fired at +4h = %+v, want the full pass the floor owes", fired.Fired)
	}
	if fired := h.fire(t, recurringNow.Add(5*time.Hour)); !skipped(fired) {
		t.Fatalf("fired at +5h = %+v, want the floor measured again from the full pass", fired.Fired)
	}

	tasks := hourlyTask("sweep")
	task := tasks["a-sweep"]
	task.FullPassEvery = config.Duration(2 * time.Hour)
	tasks["a-sweep"] = task
	h = newSkipHarness(t, tasks)
	h.fire(t, recurringNow)
	if fired := h.fire(t, recurringNow.Add(time.Hour)); !skipped(fired) {
		t.Fatalf("fired = %+v, want the pass skipped inside a two-hour floor", fired.Fired)
	}
	if fired := h.fire(t, recurringNow.Add(2*time.Hour)); !started(fired) {
		t.Fatalf("fired = %+v, want the full pass a two-hour floor owes", fired.Fired)
	}
}

// For the development manager, a health problem still present an hour after
// her last completed pass is a change, though the problems are the same ones,
// so a stall is never left unexamined past an hour.
func TestAHealthProblemStillPresentAnHourAfterTheLastPassStartsIt(t *testing.T) {
	t.Parallel()

	tasks := hourlyTask("sweep")
	task := tasks["a-sweep"]
	task.Every = config.Duration(20 * time.Minute)
	tasks["a-sweep"] = task
	h := newSkipHarness(t, tasks)
	h.trigger.Health = func(context.Context, domain.AgentRole, string) (string, *runstate.SweepHealth) {
		return "## Factory health", nil
	}
	h.sources.conditions = 1
	h.fire(t, recurringNow)
	if fired := h.fire(t, recurringNow.Add(20*time.Minute)); !skipped(fired) {
		t.Fatalf("fired at +20m = %+v, want the same problem inside the hour skipped", fired.Fired)
	}
	if fired := h.fire(t, recurringNow.Add(40*time.Minute)); !skipped(fired) {
		t.Fatalf("fired at +40m = %+v, want the same problem inside the hour skipped", fired.Fired)
	}
	if fired := h.fire(t, recurringNow.Add(time.Hour)); !started(fired) {
		t.Fatalf("fired at +1h = %+v, want a problem standing an hour after the last pass to start it", fired.Fired)
	}
	if len(h.sources.scopes) == 0 || !h.sources.scopes[0].Health {
		t.Fatalf("scopes = %+v, want the development manager's pass read with the health", h.sources.scopes)
	}

	// With no problem standing, the hour is not a change.
	h = newSkipHarness(t, tasks)
	h.trigger.Health = func(context.Context, domain.AgentRole, string) (string, *runstate.SweepHealth) { return "", nil }
	h.fire(t, recurringNow)
	h.fire(t, recurringNow.Add(20*time.Minute))
	h.fire(t, recurringNow.Add(40*time.Minute))
	if fired := h.fire(t, recurringNow.Add(time.Hour)); !skipped(fired) {
		t.Fatalf("fired at +1h = %+v, want a healthy factory's pass skipped", fired.Fired)
	}
	// And a change to the problems is a change at once.
	h.sources.move(ChangeHealth)
	if fired := h.fire(t, recurringNow.Add(80*time.Minute)); !started(fired) {
		t.Fatalf("fired = %+v, want a change in the health problems to start the pass", fired.Fired)
	}
}

// Anything the check could not read is a change, and so is a last completed
// pass that kept nothing to compare, and a project that turned skipping off.
func TestThePassIsStartedWhereTheCheckCannotSayNothingChanged(t *testing.T) {
	t.Parallel()

	t.Run("unread", func(t *testing.T) {
		t.Parallel()
		h := newSkipHarness(t, hourlyTask("sweep"))
		h.fire(t, recurringNow)
		h.sources.unread = map[PassChangeKind]string{ChangeWork: "the tracker could not be read"}
		if fired := h.fire(t, recurringNow.Add(time.Hour)); !started(fired) {
			t.Fatalf("fired = %+v, want a kind that could not be read to start the pass", fired.Fired)
		}
	})
	t.Run("no fingerprints", func(t *testing.T) {
		t.Parallel()
		h := newSkipHarness(t, hourlyTask("sweep"))
		h.trigger.Changes = nil
		h.fire(t, recurringNow)
		h.trigger.Changes = h.sources
		if fired := h.fire(t, recurringNow.Add(time.Hour)); !started(fired) {
			t.Fatalf("fired = %+v, want a last pass with nothing to compare to start the next", fired.Fired)
		}
	})
	t.Run("skipping off", func(t *testing.T) {
		t.Parallel()
		tasks := hourlyTask("sweep")
		task := tasks["a-sweep"]
		off := false
		task.SkipWhenUnchanged = &off
		tasks["a-sweep"] = task
		h := newSkipHarness(t, tasks)
		h.fire(t, recurringNow)
		if fired := h.fire(t, recurringNow.Add(time.Hour)); !started(fired) {
			t.Fatalf("fired = %+v, want every pass started with skipping off", fired.Fired)
		}
	})
	t.Run("last pass failed", func(t *testing.T) {
		t.Parallel()
		h := newSkipHarness(t, hourlyTask("sweep"))
		h.role.answers = []scriptedTurn{{problem: "no account", missing: true}}
		h.clock.now = recurringNow
		if _, err := h.trigger.Fire(context.Background()); err != nil {
			t.Fatalf("Fire() error = %v", err)
		}
		h.role.answers = nil
		if fired := h.fire(t, recurringNow.Add(time.Hour)); !started(fired) {
			t.Fatalf("fired = %+v, want a task with no completed pass started", fired.Fired)
		}
	})
}

// A summons is not the schedule, and is never checked.
func TestASummonedPassIsNeverSkipped(t *testing.T) {
	t.Parallel()

	h := newSkipHarness(t, hourlyTask("sweep"))
	h.fire(t, recurringNow)
	h.clock.now = recurringNow.Add(10 * time.Minute)
	h.role.answers = append(h.role.answers, scriptedTurn{result: complete("summoned")})
	fired, err := h.trigger.Summon(context.Background(), BrakeSummons{Hold: runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion, ProductID: "example", HeldAt: recurringNow, HeldBy: runstate.IntakeHolderBrake,
		Reason: "3 run(s) blocked in a row with nothing landing between them",
	}})
	if err != nil {
		t.Fatalf("Summon() error = %v", err)
	}
	if fired.Skipped != "" || fired.Turns == 0 {
		t.Fatalf("summoned = %+v, want the summons started whatever the check would find", fired)
	}
}

// A program manager's scheduled pass is checked like a task's, with events in
// its lane as a change; a pass its events woke is never checked, and a skipped
// pass moves no cursor.
func TestAProgramManagersScheduledPassIsCheckedAndItsEventsPassIsNot(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(time.Hour, config.TriggerLandings))
	sources := &changingSources{}
	h.trigger.Changes = sources
	h.role.answers = []scriptedTurn{{result: complete("first")}, {result: complete("landed")}, {result: complete("due with a landing")}}

	if fired := h.fire(t, recurringNow); len(fired.Fired) != 1 || fired.Fired[0].Turns == 0 {
		t.Fatalf("fired = %+v, want the first scheduled pass started", fired.Fired)
	}
	quiet := h.fire(t, recurringNow.Add(time.Hour))
	if len(quiet.Fired) != 1 || quiet.Fired[0].Skipped == "" {
		t.Fatalf("fired = %+v, want a quiet lane's scheduled pass skipped", quiet.Fired)
	}
	if !strings.Contains(quiet.Fired[0].Skipped, "events in its lane") {
		t.Errorf("skipped = %q, want it to say the lane was read", quiet.Fired[0].Skipped)
	}
	if len(sources.scopes) == 0 || sources.scopes[0].Agent != instanceName || sources.scopes[0].Lane != "reliability" {
		t.Errorf("scopes = %+v, want the instance read with its lane", sources.scopes)
	}

	// An event wakes a pass of its own, which nothing checks.
	h.events.land(recurringNow.Add(70*time.Minute), "yoyodyne-ifd.1")
	woken := h.fire(t, recurringNow.Add(72*time.Minute))
	if len(woken.Fired) != 1 || woken.Fired[0].Turns == 0 || woken.Fired[0].Events["landings"] != 1 {
		t.Fatalf("fired = %+v, want the events' pass started", woken.Fired)
	}

	// A scheduled pass that would carry an event is started. The events' pass
	// moved the schedule, so it is next due an hour after that pass.
	h.events.land(recurringNow.Add(131*time.Minute+30*time.Second), "yoyodyne-ifd.2")
	due := h.fire(t, recurringNow.Add(132*time.Minute))
	if len(due.Fired) != 1 || due.Fired[0].Turns == 0 {
		t.Fatalf("fired = %+v, want a scheduled pass carrying a landing started", due.Fired)
	}

	// An instance with skipping off starts every scheduled pass.
	off := false
	instances := instance(time.Hour)
	pm := instances[instanceName]
	pm.Triggers.SkipWhenUnchanged = &off
	instances[instanceName] = pm
	h = newPassHarness(t, instances)
	h.trigger.Changes = &changingSources{}
	h.role.answers = []scriptedTurn{{result: complete("first")}, {result: complete("second")}}
	h.fire(t, recurringNow)
	if fired := h.fire(t, recurringNow.Add(time.Hour)); len(fired.Fired) != 1 || fired.Fired[0].Turns == 0 {
		t.Fatalf("fired = %+v, want the pass started with skipping off", fired.Fired)
	}
}

// A skipped record is neither a failure nor a pass that reached nobody: it
// validates without a problem or an account, and refuses the marks of one.
func TestASkippedRecordValidatesAsNoFailure(t *testing.T) {
	t.Parallel()

	skip := runstate.Sweep{
		SchemaVersion: runstate.SweepSchemaVersion, ProductID: "example", Task: "a-sweep", Role: domain.RoleDevelopmentManager,
		StartedAt: recurringNow, EndedAt: recurringNow,
		Skipped: &runstate.SkippedPass{LastCompleted: "the pass of a-sweep that started at 09:00", LastCompletedAt: recurringNow.Add(-time.Hour), Reason: "nothing new"},
	}
	if err := skip.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a skipped record valid", err)
	}
	turned := skip
	turned.Turns = 1
	turned.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: "x"}
	if err := turned.Validate(); err == nil {
		t.Fatal("a skipped record that took a turn validated")
	}
	unnamed := skip
	unnamed.Skipped = &runstate.SkippedPass{Reason: "nothing new"}
	if err := unnamed.Validate(); err == nil {
		t.Fatal("a skipped record naming no completed pass validated")
	}
}
