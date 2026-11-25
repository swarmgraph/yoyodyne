package orchestrator

// The paths that start a role's pass, the review of unmet goals, the reading of
// outside messages and the attempt to repair a handed back change, each asking
// the one decision on what may start under the named epics (internal/maystart).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// passEpics is epic mode on with one open epic, in a state root of its own.
type passEpics struct {
	t        *testing.T
	root     string
	operator *epicscope.Operator
}

func newPassEpics(t *testing.T) passEpics {
	t.Helper()
	e := passEpics{t: t, root: t.TempDir()}
	var err error
	if e.operator, err = epicscope.ForOperator(e.root, "yoyodyne"); err != nil {
		t.Fatal(err)
	}
	e.name(namedEpic)
	return e
}

func (e passEpics) name(epic string) {
	e.t.Helper()
	if _, err := e.operator.Start(epicscope.Naming{Epic: epic, From: "terminal"}, baseTime); err != nil {
		e.t.Fatal(err)
	}
}

func (e passEpics) end() {
	e.t.Helper()
	if _, err := e.operator.End(namedEpic, "", baseTime); err != nil {
		e.t.Fatal(err)
	}
}

func (e passEpics) off() {
	e.t.Helper()
	if _, err := e.operator.TurnOff(); err != nil {
		e.t.Fatal(err)
	}
}

func (e passEpics) ask(start maystart.Start) maystart.Decision {
	return maystart.Ask(e.root, "yoyodyne", start, baseTime)
}

func (e passEpics) would(start maystart.Start) maystart.Decision {
	return maystart.Decide(e.root, "yoyodyne", start)
}

func (e passEpics) wire(trigger *Trigger) {
	trigger.MayStart, trigger.WouldStart = e.ask, e.would
	trigger.EpicsChanged = func() time.Time { return epicscope.ChangedAt(e.root, "yoyodyne") }
}

// refused is each entry as "role agent path count".
func (e passEpics) refused() []string {
	e.t.Helper()
	record, err := maystart.ReadRefusals(e.root, "yoyodyne")
	if err != nil {
		e.t.Fatal(err)
	}
	var says []string
	for _, entry := range record.Passes {
		if entry.Why == "" {
			e.t.Fatalf("%+v keeps no reason", entry)
		}
		says = append(says, fmt.Sprintf("%s %s %s %d", entry.Role, entry.Agent, entry.Path, entry.Count))
	}
	slices.Sort(says)
	return says
}

func (e passEpics) wants(want ...string) {
	e.t.Helper()
	if got := e.refused(); !slices.Equal(got, want) {
		e.t.Fatalf("refused passes = %q, want %q", got, want)
	}
}

// snapshot is every file under a directory, so a test can say nothing changed. A lock file
// holds nothing.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.HasSuffix(path, ".lock") {
			return err
		}
		content, err := os.ReadFile(path)
		files[path] = string(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestThePassesOfTheRolesThatCarryOutAnEpicStartWhileOneIsOpen(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	started := func() []string {
		t.Helper()
		store := sweepStore(t)
		trigger := Trigger{Claims: store, Reports: store, Roles: &wokenRole{}, Clock: recurringClock{}, Tasks: map[string]config.RecurringTask{}, Instances: instance(time.Hour)}
		for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect} {
			trigger.Tasks[string(role)] = config.RecurringTask{Role: role, Every: config.Duration(time.Hour), Enabled: true, Prompt: "look", MaxTurns: 1}
		}
		e.wire(&trigger)
		start, err := trigger.Start(context.Background(), nil, MaxConcurrentFirings)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, firing := range start.Started {
			names = append(names, firing.Task)
		}
		slices.Sort(names)
		return names
	}
	carry := []string{"architect", "development-manager", "product-manager"}
	if got := started(); !slices.Equal(got, carry) {
		t.Fatalf("started %q while an epic is open, want %q", got, carry)
	}
	e.end()
	if got := started(); len(got) != 0 {
		t.Fatalf("started %q after the last epic ended", got)
	}
	if !e.ask(maystart.OperatorConversation()).MayStart() {
		t.Fatal("the operator's own conversation was refused")
	}
	e.name("yoyodyne-again")
	if got := started(); !slices.Equal(got, carry) {
		t.Fatalf("started %q once another epic is open, want %q", got, carry)
	}
	e.off()
	if got := started(); len(got) != 4 {
		t.Fatalf("started %q with epic mode off, want the program manager's too", got)
	}
}

// Each thing that can start a pass: refused at ten pulls, with a restart in the
// middle, it is counted once, nothing else changes, and it starts once allowed.
func TestARefusedPassIsCountedOnceAndLosesNothing(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		entry string
		// build opens the stores over the root again, as a restart does.
		build func(t *testing.T, root string) Trigger
		again func(t *testing.T, root string, trigger *Trigger)
	}{
		"schedule": {
			entry: "development-manager  recurring-pass",
			build: func(t *testing.T, root string) Trigger {
				return Trigger{Tasks: hourlyTask("look"), Clock: &movingRecurringClock{now: recurringNow}}
			},
			again: func(_ *testing.T, _ string, trigger *Trigger) {
				trigger.Clock.(*movingRecurringClock).now = recurringNow.Add(time.Hour)
			},
		},
		"schedule of a program manager": {
			entry: "program-manager reliability-pm recurring-pass",
			build: func(t *testing.T, root string) Trigger {
				return Trigger{Instances: instance(time.Hour), Clock: &movingRecurringClock{now: recurringNow}}
			},
			again: func(_ *testing.T, _ string, trigger *Trigger) {
				trigger.Clock.(*movingRecurringClock).now = recurringNow.Add(time.Hour)
			},
		},
		"event": {
			entry: "program-manager reliability-pm prompted-pass",
			build: func(t *testing.T, root string) Trigger {
				cursors, err := runstate.NewPassCursorStore(root, "example")
				if err != nil {
					t.Fatal(err)
				}
				if _, found, _ := cursors.Load(instanceName); !found {
					if _, err := cursors.Advance(context.Background(), instanceName, map[string]time.Time{runstate.PassStreamTracker: recurringNow.Add(-time.Hour)}, nil, recurringNow); err != nil {
						t.Fatal(err)
					}
				}
				events := &recordedEvents{}
				events.admit(recurringNow.Add(-30*time.Minute), 3)
				return Trigger{Instances: instance(0, config.TriggerAdmissions), Cursors: cursors, Events: events, Clock: recurringClock{}}
			},
		},
		"request": {
			entry: "program-manager reliability-pm prompted-pass",
			build: func(t *testing.T, root string) Trigger {
				requests, err := runstate.NewPassRequestStore(root, "example")
				if err != nil {
					t.Fatal(err)
				}
				if _, open, _ := requests.Open(instanceName); !open {
					if _, err := requests.Request(runstate.PassRequest{SchemaVersion: runstate.PassRequestSchemaVersion, ProductID: "example", ID: "pass-request-1",
						Agent: instanceName, AskedBy: runstate.PassAskerOperator, RequestedAt: recurringNow}); err != nil {
						t.Fatal(err)
					}
				}
				return Trigger{Instances: instance(0), Requests: requests, Clock: recurringClock{}}
			},
		},
		"label": {
			entry: "architect  prompted-pass",
			build: func(t *testing.T, root string) Trigger {
				wakes, err := runstate.NewNeedsDesignWakeStore(root, "example")
				if err != nil {
					t.Fatal(err)
				}
				items := &labelledItems{}
				items.label("yoyodyne-1", "One")
				return Trigger{NeedsDesign: items, NeedsDesignWakes: wakes, Clock: recurringClock{}}
			},
			again: func(_ *testing.T, _ string, trigger *Trigger) {
				items := trigger.NeedsDesign.(*labelledItems)
				items.unlabel("yoyodyne-1")
				items.label("yoyodyne-2", "Two")
			},
		},
		"critical reports": {
			entry: "product-manager  prompted-pass",
			build: func(t *testing.T, root string) Trigger {
				pile := &memoryPile{reports: []report.Report{{ID: "report-1", Severity: report.SeverityCritical, Role: domain.RoleDeveloper, Message: "Nothing lands.", RecordedAt: recurringNow}}}
				tasks := reportTriageTask()
				task := tasks["report-triage"]
				task.MaxTurns = 1
				tasks["report-triage"] = task
				return Trigger{Tasks: tasks, Pile: pile, Clock: recurringClock{}}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newPassEpics(t)
			e.end()
			root := t.TempDir()
			role := &wokenRole{answers: []scriptedTurn{{result: complete("looked")}, {result: complete("looked")}}}
			build := func() Trigger {
				trigger := test.build(t, root)
				store, err := runstate.NewSweepStore(root, "example")
				if err != nil {
					t.Fatal(err)
				}
				trigger.Claims, trigger.Reports, trigger.Roles = store, store, role
				e.wire(&trigger)
				return trigger
			}
			fire := func(trigger Trigger) int {
				t.Helper()
				fired, err := trigger.Fire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				return len(fired.Fired)
			}
			trigger := build()
			before := snapshot(t, root)
			for pull := range 10 {
				if pull == 5 {
					trigger = build()
				}
				if fire(trigger) != 0 {
					t.Fatalf("pull %d started the pass", pull)
				}
			}
			if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("a refusal changed the state:\n%v\nfrom\n%v", after, before)
			}
			e.wants(test.entry + " 1")
			if test.again != nil {
				// A schedule's first due time is known once it has fired.
				if name == "schedule" || name == "schedule of a program manager" {
					task := "a-sweep"
					if name != "schedule" {
						task = instanceName
					}
					if _, err := trigger.Claims.Claim(context.Background(), task, time.Hour, recurringNow.Add(-time.Hour)); err != nil {
						t.Fatal(err)
					}
					fire(trigger)
					e.wants(test.entry + " 2")
					test.again(t, root, &trigger)
					fire(trigger)
					restarted := build()
					restarted.Clock = trigger.Clock
					fire(restarted)
					fire(trigger)
					e.wants(test.entry + " 3")
				} else {
					test.again(t, root, &trigger)
					fire(trigger)
					fire(trigger)
					e.wants(test.entry + " 2")
				}
			}
			e.off()
			if fired := fire(trigger); fired != 1 || len(role.messages) != 1 {
				t.Fatalf("once allowed, %d firings and %d turns, want exactly one", fired, len(role.messages))
			}
			e.wants()
			if fire(trigger) != 0 {
				t.Fatal("the pass started twice")
			}
		})
	}
}

func TestTwoInstancesAndTwoPathsOfOneRoleHaveEntriesOfTheirOwn(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	cursors, err := runstate.NewPassCursorStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	instances := instance(time.Hour, config.TriggerAdmissions)
	instances["cost-pm"] = config.AgentConfig{Role: domain.RoleProgramManager, Model: "opus", Lane: "cost", Triggers: config.Triggers{Every: config.Duration(time.Hour)}}
	h := newPassHarness(t, instances)
	h.trigger.Cursors = cursors
	e.wire(&h.trigger)
	h.fire(t, recurringNow)
	e.wants("program-manager cost-pm recurring-pass 1", "program-manager reliability-pm recurring-pass 1")
	// The same instance, woken by its events while its schedule is not due.
	if _, err := h.sweeps.Claim(context.Background(), instanceName, time.Hour, recurringNow); err != nil {
		t.Fatal(err)
	}
	h.events.admit(recurringNow.Add(time.Minute), 1)
	h.fire(t, recurringNow.Add(30*time.Minute))
	e.wants("program-manager cost-pm recurring-pass 1", "program-manager reliability-pm prompted-pass 1", "program-manager reliability-pm recurring-pass 1")
}

// A refused pass is not a missed one, and the next in the order starts.
func TestAPassTheNamedEpicsRefuseIsNotRecordedAsMissed(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	h := newPassHarness(t, instance(time.Hour))
	pile := &filedReports{}
	h.trigger.Breakage = pile
	h.trigger.Attribution = report.Attribution{ProductID: "example", RepositoryID: "example"}
	h.trigger.Tasks = hourlyTask("look")
	h.role.answers = []scriptedTurn{{result: complete("looked")}}
	e.wire(&h.trigger)
	for _, task := range []struct {
		name string
		at   time.Time
	}{{instanceName, recurringNow.Add(-3 * time.Hour)}, {"a-sweep", recurringNow.Add(-time.Hour)}} {
		if _, err := h.sweeps.Claim(context.Background(), task.name, time.Hour, task.at); err != nil {
			t.Fatal(err)
		}
	}
	watch := recurringWatch{opened: recurringNow.Add(-4 * time.Hour), missed: map[string]time.Time{}}
	look := func(watch *recurringWatch, at time.Time) {
		t.Helper()
		h.clock.now = at
		schedule := Schedule{}
		Scheduler{Now: func() time.Time { return at }}.missed(context.Background(), &schedule, Pull{Recurring: h.trigger}, watch)
		if schedule.RecurringProblem != "" {
			t.Fatal(schedule.RecurringProblem)
		}
	}
	missed := func() int {
		t.Helper()
		count := 0
		for _, recorded := range h.recorded(t) {
			if recorded.Missed != nil && recorded.Task == instanceName {
				count++
			}
		}
		return count + len(pile.filed)
	}
	look(&watch, recurringNow)
	// The instance's pass has waited longest, so it is first in the order.
	if fired := h.fire(t, recurringNow); len(fired.Fired) != 1 || fired.Fired[0].Task != "a-sweep" {
		t.Fatalf("fired = %+v, want the pass after the refused one", fired.Fired)
	}
	look(&watch, recurringNow.Add(30*time.Minute))
	if missed() != 0 {
		t.Fatalf("a refused pass was recorded as missed: %+v", h.recorded(t))
	}
	// Allowed, it is late only after a whole interval from the first pull that
	// allowed it; after a restart, from the session's start.
	e.off()
	h.trigger.Tasks = nil
	allowed := recurringNow.Add(40 * time.Minute)
	look(&watch, allowed)
	look(&watch, allowed.Add(59*time.Minute))
	if missed() != 0 {
		t.Fatalf("lateness was counted from before the pass was allowed: %+v", h.recorded(t))
	}
	h.trigger.EpicsChanged = func() time.Time { return allowed }
	restarted := recurringWatch{opened: allowed.Add(30 * time.Minute), missed: map[string]time.Time{}}
	look(&restarted, allowed.Add(89*time.Minute))
	if missed() != 0 {
		t.Fatalf("after a restart, lateness was counted from before its start: %+v", h.recorded(t))
	}
	look(&watch, allowed.Add(61*time.Minute))
	if missed() != 2 {
		t.Fatalf("a pass late since it was allowed was not recorded and reported: %+v, %+v", h.recorded(t), pile.filed)
	}
}

func TestARefusedGoalReviewChangesNothingAndTheOrdinaryPassStarts(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	r := newDailyReview(t)
	e.wire(&r.trigger)
	// Her ordinary pass is due as well.
	task := r.trigger.Tasks["lead-pm"]
	task.Every = config.Duration(time.Hour)
	r.trigger.Tasks["lead-pm"] = task
	if _, _, err := r.sweeps.Adopt(context.Background(), "lead-pm", time.Hour, novemberDay(20, 8, 0)); err != nil {
		t.Fatal(err)
	}
	fired := r.pull(t, novemberDay(20, 10, 0))
	if len(fired.Fired) != 1 || fired.Fired[0].Summoned != "" {
		t.Fatalf("fired = %+v, want her ordinary pass", fired.Fired)
	}
	if fired = r.pull(t, novemberDay(20, 10, 1)); len(fired.Fired) != 0 || r.source.reads != 0 {
		t.Fatalf("fired = %+v after %d readings of the goals, want neither", fired.Fired, r.source.reads)
	}
	if record, found, err := r.reviews.Load(); err != nil || found || len(record.Goals) != 0 {
		t.Fatalf("reviews = %+v, %t, %v; want the store unchanged", record, found, err)
	}
	e.wants()
	e.off()
	if fired = r.pull(t, novemberDay(20, 10, 2)); len(fired.Fired) != 1 || fired.Fired[0].Summoned != goalReviewSummons {
		t.Fatalf("fired = %+v, want the review once it may start", fired.Fired)
	}
}

func TestOutsideMessagesAreReadOnlyWhereTheyMayBe(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	intake := &fakeIntake{due: []string{"bugs"}, started: map[string]int{}, release: make(chan struct{})}
	close(intake.release)
	runs := newIntakeRuns(context.Background())
	defer runs.cancel()
	runs.mayStart = e.ask
	var schedule Schedule
	Scheduler{}.startIntakeRuns(&schedule, intake, runs)
	if schedule.IntakeRuns != 0 || len(runs.inFlight) != 0 || len(intake.started) != 0 {
		t.Fatalf("%d runs started, want none", schedule.IntakeRuns)
	}
	e.off()
	Scheduler{}.startIntakeRuns(&schedule, intake, runs)
	collectIntakeRuns(&schedule, runs, true)
	if schedule.IntakeRuns != 1 || intake.started["bugs"] != 1 {
		t.Fatalf("%d runs started once allowed, want one", schedule.IntakeRuns)
	}
}

func TestARefusedCorrectionTurnStaysOutstanding(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	e.end()
	root := t.TempDir()
	store := conversationStore(t, root)
	refused := refusedConversation(t, store, "product-manager", 4, correctionNow.Add(-time.Minute), "unreadable")
	role := &correctedRole{turns: []CorrectionTurn{{ConversationID: refused.ConversationID, Actions: 3}}}
	before := snapshot(t, root)
	correct := func() int {
		t.Helper()
		store := conversationStore(t, root)
		sweep, err := Corrector{Conversations: store, Claims: store, Roles: role, Clock: correctionClock{}, MayStart: e.ask}.Correct(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return len(sweep.Corrected)
	}
	for range 10 {
		if correct() != 0 {
			t.Fatal("a refused correction turn started")
		}
	}
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("a refusal changed the conversation's record")
	}
	e.wants("product-manager  prompted-pass 1")
	e.off()
	if correct() != 1 || len(role.woken) != 1 || correct() != 0 {
		t.Fatalf("woken %d times once allowed, want exactly once", len(role.woken))
	}
	e.wants()
}

// The hold is placed whatever the answer, the summons is counted once for it,
// and comes round at each pull until it may start.
func TestARefusedSummonsComesRoundWhileItsHoldStands(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	e.end()
	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	harness.cooldown = time.Hour
	harness.run = blockedStorm
	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("looked")}}}
	trigger := Trigger{Tasks: hourlyTask("look"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
	e.wire(&trigger)
	harness.summon = func(h *scheduleHarness, summons BrakeSummons, _ int) (Fired, error) {
		return trigger.Summon(context.Background(), summons)
	}
	session := func(pulls int, atSleep func(sleeps int)) Schedule {
		t.Helper()
		from := -1
		harness.onSleep = func(_ *scheduleHarness, sleeps int) bool {
			if from < 0 {
				from = sleeps - 1
			}
			if atSleep != nil {
				atSleep(sleeps - from)
			}
			return sleeps-from < pulls
		}
		schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}.Schedule(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return schedule
	}
	schedule := session(8, func(sleeps int) {
		if sleeps != 5 {
			return
		}
		if len(role.messages) != 0 {
			t.Error("a refused summons was made")
		}
		e.wants("development-manager  prompted-pass 1")
		e.off()
	})
	hold, held, err := harness.Held()
	if err != nil || !held || schedule.Braked == nil || schedule.BrakeProblem != "" {
		t.Fatalf("hold = %+v, %t, %v, problem %q; want it placed and its summons made", hold, held, err, schedule.BrakeProblem)
	}
	if len(role.messages) != 1 {
		t.Fatalf("summoned %d times once allowed, want exactly once", len(role.messages))
	}
	if hold.Brake.SummonedAt == nil {
		t.Fatalf("hold = %+v, want the summons recorded on it", hold.Brake)
	}
	e.wants()
}

// The session asks before it takes her conversation or launches a turn.
func TestARefusedSummonsTakesNoConversation(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	e.end()
	trigger := Trigger{Tasks: hourlyTask("look")}
	e.wire(&trigger)
	firings := newRecurringFirings(context.Background())
	brake := &heldBrake{hold: runstate.IntakeHold{HeldAt: baseTime, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{}}}
	var schedule Schedule
	for range 10 {
		Scheduler{Now: func() time.Time { return baseTime }}.summon(context.Background(), &schedule, firings, brake, trigger, brake.hold)
	}
	if !firings.idle() || !firings.summonsPending() || brake.revised != 0 {
		t.Fatalf("in flight %v, the hold revised %d times to %+v; want nothing in flight, the hold as it was and the summons waiting", firings.inFlight, brake.revised, brake.hold.Brake)
	}
	e.wants("development-manager  prompted-pass 1")
	if err := trigger.MaySummon(BrakeSummons{Hold: brake.hold}); !errors.Is(err, ErrSummonsOutsideNamedEpics) {
		t.Fatalf("MaySummon() = %v", err)
	}
}

// A restart loses the summons the session was keeping, and the next session
// finds it on the hold: once refused it is counted no second time, and it is
// made exactly once when it may be, though epic mode went off in between.
func TestARefusedSummonsComesRoundAfterARestart(t *testing.T) {
	t.Parallel()
	for name, offWhileDown := range map[string]bool{"allowed under the session": false, "allowed between sessions": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newPassEpics(t)
			e.end()
			h := newScheduleHarness()
			h.held = &runstate.IntakeHold{SchemaVersion: runstate.IntakeHoldSchemaVersion, HeldAt: baseTime, HeldBy: runstate.IntakeHolderBrake, Reason: "blocked in a row",
				Brake: &runstate.IntakeBrake{Blocked: []runstate.BrakeBlockedRun{{RunID: "run-1", WorkItemID: "yoyodyne-one", Reason: "stopped"}}, CooldownEndsAt: baseTime.Add(time.Hour)}}
			store := sweepStore(t)
			role := &wokenRole{answers: []scriptedTurn{{result: complete("looked")}, {result: complete("looked")}}}
			trigger := Trigger{Tasks: hourlyTask("look"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
			e.wire(&trigger)
			var schedule Schedule
			s := Scheduler{Now: func() time.Time { return baseTime }}
			firings := newRecurringFirings(context.Background())
			pull := func() {
				t.Helper()
				s.summonWaiting(context.Background(), &schedule, firings, Pull{Intake: h, Brake: h, Summons: trigger})
				if !firings.idle() {
					s.collectFirings(&schedule, firings, &recurringWatch{}, true)
				}
			}
			restart := func() { firings = newRecurringFirings(context.Background()) }
			s.summon(context.Background(), &schedule, firings, h, trigger, *h.held)
			if offWhileDown {
				e.off()
				// As the command that turns it off does.
				if err := maystart.Settle(e.root, "yoyodyne"); err != nil {
					t.Fatal(err)
				}
				restart()
			} else {
				restart()
				pull()
				pull()
				if !firings.summonsPending() || len(role.messages) != 0 || h.held.Brake.SummonedAt != nil {
					t.Fatalf("pending %t after %d turns, hold %+v; want it waiting still", firings.summonsPending(), len(role.messages), h.held.Brake)
				}
				e.wants("development-manager  prompted-pass 1")
				e.off()
			}
			pull()
			pull()
			restart()
			pull()
			if len(role.messages) != 1 || h.held.Brake.SummonedAt == nil || firings.summonsPending() {
				t.Fatalf("%d turns, hold %+v; want her summoned exactly once", len(role.messages), h.held.Brake)
			}
			e.wants()
		})
	}
}

// Where no epic was ever named a hold with no summons on it is left as it is.
func TestAProductThatNamedNoEpicIsNotSummonedAgainAfterARestart(t *testing.T) {
	t.Parallel()
	h := newScheduleHarness()
	h.held = &runstate.IntakeHold{HeldAt: baseTime, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{}}
	firings := newRecurringFirings(context.Background())
	var schedule Schedule
	Scheduler{Now: func() time.Time { return baseTime }}.summonWaiting(context.Background(), &schedule, firings, Pull{Intake: h, Brake: h, Summons: Trigger{Tasks: hourlyTask("look")}})
	if firings.summonsPending() {
		t.Fatal("the development manager was summoned again")
	}
}

type heldBrake struct {
	ScheduleBrake
	hold    runstate.IntakeHold
	revised int
}

func (b *heldBrake) ReviseBrake(revise func(*runstate.IntakeBrake) error) (runstate.IntakeHold, error) {
	b.revised++
	return b.hold, revise(b.hold.Brake)
}

func TestAPassAlreadyClaimedTakesAllItsTurns(t *testing.T) {
	t.Parallel()
	e := newPassEpics(t)
	e.off()
	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{
		{result: &sweep.Result{Status: sweep.StatusMore, Summary: "half"}, during: func() {
			e.name("yoyodyne-again")
			for _, epic := range []string{namedEpic, "yoyodyne-again"} {
				if _, err := e.operator.End(epic, "", baseTime); err != nil {
					t.Error(err)
				}
			}
		}},
		{result: complete("the rest")},
	}}
	trigger := Trigger{Tasks: hourlyTask("look"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
	e.wire(&trigger)
	fired, err := trigger.Fire(context.Background())
	if err != nil || len(fired.Fired) != 1 || fired.Fired[0].Turns != 2 {
		t.Fatalf("fired = %+v, %v; want both turns taken", fired.Fired, err)
	}
	if e.ask(maystart.Pass(maystart.RecurringPass, domain.RoleDevelopmentManager)).MayStart() {
		t.Fatal("epic mode was not on under the pass")
	}
}

// Outside the members the attempt does not start, with the run's record exactly
// as it was, and the hand-back stays owed until it may.
func TestAHandedBackChangeOutsideTheEpicsIsNotBroughtUpToDate(t *testing.T) {
	t.Parallel()
	decide, add, kept := epicModeKept(t)
	r, _ := newCatchUpRun(t, resolveBothSides)
	admitted := r.run(t)
	if admitted.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want it queued", admitted)
	}
	key := admitted.MergeQueue.Key()
	conflictOnTarget(t, r.repository, "something else\n")
	before, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	notes := r.tracker.Record().Notes
	pass := func() {
		t.Helper()
		driver := r.catchUpDriver()
		repair := driver.Promoter.Repair.(MergeQueueRunHandback)
		repair.MayStart = decide
		driver.Promoter.Repair = repair
		driver.Pass(context.Background(), key)
	}
	for range 3 {
		pass()
		after, err := r.runs.Load(admitted.RunID)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("run = %+v, %v; want it as it was:\n%+v", after, err, before)
		}
	}
	if record := r.tracker.Record(); record.Blocked || record.Notes != notes {
		t.Fatalf("blocked %t, notes %q; want the item as it was", record.Blocked, record.Notes)
	}
	kept(maystart.BringUpToDate, before.WorkItemID)
	landing, _, err := r.queue.Landing(key, admitted.MergeQueue.EntryID)
	if err != nil || landing.Handback == nil {
		t.Fatalf("landing = %+v, %v; want the hand-back kept", landing, err)
	}
	add(before.WorkItemID)
	pass()
	put, err := r.runs.Load(admitted.RunID)
	if err != nil || !catchUpNotTakenUp(put) || put.RepairAttempts != 0 {
		t.Fatalf("run = %+v, %v; want it put back once it is a member", put, err)
	}
	kept("", "")
}
