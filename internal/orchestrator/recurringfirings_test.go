package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// harnessClock reads a scheduling harness's clock, so a trigger and the
// session driving it agree on the time.
type harnessClock func() time.Time

func (c harnessClock) Now() time.Time { return c() }

// sideBySideRoles is three roles' conversations as a test reaches them from
// several goroutines at once. A role with a gate waits on it at every turn; each
// role says on woke the first time it is woken.
type sideBySideRoles struct {
	mu    sync.Mutex
	turns map[domain.AgentRole]int
	// more is how many turns a role answers as having more to do before it
	// answers complete.
	more  map[domain.AgentRole]int
	gates map[domain.AgentRole]chan struct{}
	woke  map[domain.AgentRole]chan struct{}
}

func newSideBySideRoles(roles ...domain.AgentRole) *sideBySideRoles {
	r := &sideBySideRoles{
		turns: map[domain.AgentRole]int{},
		more:  map[domain.AgentRole]int{},
		gates: map[domain.AgentRole]chan struct{}{},
		woke:  map[domain.AgentRole]chan struct{}{},
	}
	for _, role := range roles {
		r.woke[role] = make(chan struct{})
	}
	return r
}

func (r *sideBySideRoles) Wake(ctx context.Context, role domain.AgentRole, _, _, _, _ string, _ RecurringTurnOptions) (Turn, error) {
	r.mu.Lock()
	r.turns[role]++
	turn, more, gate := r.turns[role], r.more[role], r.gates[role]
	if turn == 1 {
		close(r.woke[role])
	}
	r.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return Turn{}, ctx.Err()
		}
	}
	if turn <= more {
		return Turn{ConversationID: "chat-" + string(role), Result: &sweep.Result{Status: sweep.StatusMore, Summary: "more to do"}}, nil
	}
	return Turn{ConversationID: "chat-" + string(role), Result: complete("done")}, nil
}

func (r *sideBySideRoles) turnsOf(role domain.AgentRole) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turns[role]
}

func hourly(role domain.AgentRole, turns int) config.RecurringTask {
	return config.RecurringTask{Role: role, Every: config.Duration(time.Hour), Enabled: true, Prompt: "look", MaxTurns: turns}
}

// The three roles of 2026-09-29, due together, with the Lead Product Manager's
// sweep taking many turns and the first of them held. The development manager's
// sweep and the architect's pass are both woken within the poll that found them
// due, beside hers rather than behind it, and the queue is pulled from and
// started while her pass is still going: no role's pass holds another role's,
// and none holds the pull.
func TestOneRolesLongPassHoldsNeitherTheOtherRolesNorThePull(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.1", "yoyodyne-ifd.2")...)
	opened := harness.now
	store := sweepStore(t)
	roles := newSideBySideRoles(domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect)
	release := make(chan struct{})
	roles.gates[domain.RoleProductManager] = release
	roles.more[domain.RoleProductManager] = 5
	harness.recurring = Trigger{
		Tasks: map[string]config.RecurringTask{
			"product-manager-sweep":     hourly(domain.RoleProductManager, 8),
			"development-manager-sweep": hourly(domain.RoleDevelopmentManager, 4),
			"architect-amendments":      hourly(domain.RoleArchitect, 4),
		},
		Claims: store, Reports: store, Roles: roles, Clock: harnessClock(harness.clock),
	}
	// The session's own idle poll is where the test looks. The wait it makes for
	// the next cadence while a run is in flight is left to be ended by the run,
	// so the clock moves only by the poll.
	released := false
	polls := 0
	sleep := func(ctx context.Context, interval time.Duration) bool {
		if interval != harness.poll {
			<-ctx.Done()
			return false
		}
		polls++
		if released {
			return false
		}
		// The first poll the session sleeps out: all three roles have been woken,
		// her pass is still held on its first turn, and both items were started.
		for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect} {
			select {
			case <-roles.woke[role]:
			case <-time.After(30 * time.Second):
				t.Errorf("the %s was not woken within the poll that found it due", role)
			}
		}
		if got := roles.turnsOf(domain.RoleProductManager); got != 1 {
			t.Errorf("the product manager's pass is on turn %d, want it still held on its first", got)
		}
		harness.mu.Lock()
		order := append([]string(nil), harness.order...)
		harness.mu.Unlock()
		if len(order) != 2 {
			t.Errorf("started %v while the product manager's pass was held, want both ready items pulled beside it", order)
		}
		if polls != 1 {
			t.Errorf("reached this after %d poll(s), want the first", polls)
		}
		released = true
		close(release)
		return false
	}

	sessions := &recordedSessions{}
	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: sleep, Sessions: sessions, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !released {
		t.Fatalf("the session never slept out a poll: %s", schedule.Render())
	}
	if len(schedule.Fired) != 3 {
		t.Fatalf("fired %+v, want all three passes on the record once the session ended", schedule.Fired)
	}
	if got := roles.turnsOf(domain.RoleProductManager); got != 6 {
		t.Errorf("the product manager's pass took %d turns, want its six once released", got)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 3 {
		t.Fatalf("recorded = %+v (%v), want the three passes", recorded, err)
	}
	// Each pass the session began is said as it begins, and says the poll goes
	// on pulling while it runs, since it was taken beside the poll.
	notes := 0
	for _, transition := range sessions.recorded() {
		if transition.pass == nil {
			continue
		}
		notes++
		if !transition.pass.Beside || !strings.Contains(transition.reason, "goes on pulling while it runs") {
			t.Errorf("pass note = %#v, want it marked as taken beside the poll", transition)
		}
	}
	if notes != 3 {
		t.Errorf("recorded %d pass note(s), want one for each of the three passes", notes)
	}
	for _, pass := range recorded {
		if !pass.StartedAt.Equal(opened) {
			t.Errorf("%s started at %s, want it taken at the pull that found it due, %s", pass.Task, pass.StartedAt, opened)
		}
	}
}

// The feedback loop of 2026-09-29, closed. The development manager's sweep fell
// due an hour ago and went unfired; the miss was reported at critical; and the
// critical is waiting to be delivered to the Lead Product Manager. Both fire,
// each in its own conversation. And where only one firing can be taken, the
// development manager's goes first, because it has waited longest since it fell
// due: the report of a starved pass never takes the starved pass's place.
func TestACriticalDeliveryNeverTakesAnotherRolesDueFiring(t *testing.T) {
	t.Parallel()

	const critical = "report-00000000000000000000000000000c02"
	setup := func(t *testing.T) (Trigger, *runstate.SweepStore, *sideBySideRoles) {
		store := sweepStore(t)
		claimed(t, store, "report-triage")
		if _, err := store.Claim(context.Background(), "development-manager-sweep", time.Hour, recurringNow.Add(-2*time.Hour)); err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
		if _, err := store.Settle(context.Background(), "development-manager-sweep", ""); err != nil {
			t.Fatalf("Settle() error = %v", err)
		}
		pile := &memoryPile{reports: []report.Report{
			filedReport(critical, report.HarnessReporter, report.SeverityCritical,
				"The recurring task development-manager-sweep has not fired since it fell due.", recurringNow.Add(-time.Minute)),
		}}
		roles := newSideBySideRoles(domain.RoleProductManager, domain.RoleDevelopmentManager)
		tasks := reportTriageTask()
		tasks["development-manager-sweep"] = hourly(domain.RoleDevelopmentManager, 2)
		return Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: roles, Pile: pile, Clock: recurringClock{}}, store, roles
	}

	t.Run("both fire", func(t *testing.T) {
		trigger, store, roles := setup(t)
		start, err := trigger.Start(context.Background(), nil, MaxConcurrentFirings)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		var names []string
		for _, started := range start.Started {
			names = append(names, started.Task)
		}
		if strings.Join(names, ",") != "development-manager-sweep,report-triage" {
			t.Fatalf("started %v, want the development manager's missed pass first and the critical's delivery beside it", names)
		}
		var wg sync.WaitGroup
		for _, started := range start.Started {
			wg.Add(1)
			go func(started StartedFiring) {
				defer wg.Done()
				started.Take(context.Background())
			}(started)
		}
		wg.Wait()
		if roles.turnsOf(domain.RoleDevelopmentManager) == 0 || roles.turnsOf(domain.RoleProductManager) == 0 {
			t.Fatalf("turns = %v, want both roles woken", roles.turns)
		}
		recorded, _, err := store.List()
		if err != nil || len(recorded) != 2 {
			t.Fatalf("recorded = %+v (%v), want both passes", recorded, err)
		}
		for _, pass := range recorded {
			waited, known := pass.Waited()
			switch pass.Task {
			case "development-manager-sweep":
				if !known || waited != time.Hour {
					t.Errorf("the development manager's pass waited %s (known %v), want the hour it stood due", waited, known)
				}
			case "report-triage":
				if len(pass.Criticals) != 1 || pass.Criticals[0] != critical {
					t.Errorf("the delivery carried %v, want the critical", pass.Criticals)
				}
				if !known || waited != time.Minute {
					t.Errorf("the delivery waited %s (known %v), want the minute since the critical was filed", waited, known)
				}
			}
		}
	})

	t.Run("one slot goes to the longest wait", func(t *testing.T) {
		trigger, _, _ := setup(t)
		start, err := trigger.Start(context.Background(), nil, 1)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if len(start.Started) != 1 || start.Started[0].Task != "development-manager-sweep" {
			t.Fatalf("started %+v, want the development manager's missed pass ahead of the critical's delivery", start.Started)
		}
		if len(start.Waiting) != 1 || start.Waiting[0].Task != "report-triage" || !start.Waiting[0].Full {
			t.Fatalf("waiting = %+v, want the delivery waiting on the bound", start.Waiting)
		}
	})

	t.Run("a busy conversation keeps only its own role's firing", func(t *testing.T) {
		trigger, _, _ := setup(t)
		busy := map[string]bool{roleConversationKey(domain.RoleProductManager): true}
		start, err := trigger.Start(context.Background(), busy, MaxConcurrentFirings)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if len(start.Started) != 1 || start.Started[0].Task != "development-manager-sweep" {
			t.Fatalf("started %+v, want the development manager's pass while hers is in flight", start.Started)
		}
		if len(start.Waiting) != 1 || start.Waiting[0].Task != "report-triage" || start.Waiting[0].Full {
			t.Fatalf("waiting = %+v, want her delivery waiting on her own conversation", start.Waiting)
		}
	})
}

// A task kept behind another firing is recorded as missed with what kept it,
// at critical, because it is the harness holding its own cadence.
func TestAMissSaysWhichFiringKeptTheTaskBehindIt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 29, 17, 44, 0, 0, time.UTC)
	due := now.Add(-61 * time.Minute)
	tasks := &cadencedTasks{clock: func() time.Time { return now }, every: time.Hour, firedAt: due.Add(-time.Hour)}
	firings := newRecurringFirings(context.Background())
	defer firings.cancel()
	firings.inFlight[roleConversationKey(domain.RoleDevelopmentManager)] = StartedFiring{Task: "development-manager-sweep-deep"}
	why := waitingReason(WaitingFiring{Task: "development-manager-sweep", Conversation: roleConversationKey(domain.RoleDevelopmentManager), Due: due}, firings)
	watch := recurringWatch{
		opened:  due.Add(-time.Hour),
		missed:  map[string]time.Time{},
		waiting: map[string]recurringHold{"development-manager-sweep": {why: why, at: now.Add(-time.Minute)}},
	}
	var schedule Schedule
	Scheduler{Now: func() time.Time { return now }}.missed(context.Background(), &schedule, Pull{Recurring: tasks}, &watch)
	if len(tasks.misses) != 1 {
		t.Fatalf("misses = %+v, want the one", tasks.misses)
	}
	miss := tasks.misses[0]
	if miss.Severity != report.SeverityCritical || !strings.Contains(miss.Why, "development-manager-sweep-deep") {
		t.Fatalf("miss = %+v, want it at critical naming the firing that held its conversation", miss)
	}

	full := waitingReason(WaitingFiring{Task: "architect-amendments", Full: true}, firings)
	if !strings.Contains(full, "firings that had waited longer") {
		t.Fatalf("a firing kept by the bound says %q, want the bound and the order named", full)
	}
}

// The brake summoning the development manager while her own scheduled pass is
// still taking turns in her conversation. The summons is not made into it —
// it would claim her task's firing and then be refused the conversation — and
// is made at the first pull after her pass ends, while the brake's hold stands.
// A hold lifted in the meantime drops it.
func TestABrakeSummonsWaitsForTheDevelopmentManagersOwnPass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Turns: 1}, nil
	}
	held, err := harness.Brake(runstate.IntakeBrake{Blocked: []runstate.BrakeBlockedRun{{WorkItemID: "yoyodyne-ifd.1", Reason: "the checks failed"}}, CooldownEndsAt: harness.clock().Add(30 * time.Minute)}, "3 run(s) blocked in a row", harness.clock())
	if err != nil {
		t.Fatalf("Brake() error = %v", err)
	}
	firings := newRecurringFirings(context.Background())
	defer firings.cancel()
	dm := roleConversationKey(domain.RoleDevelopmentManager)
	firings.inFlight[dm] = StartedFiring{Task: "development-manager-sweep", Conversation: dm}
	scheduler := Scheduler{Now: harness.clock}
	var schedule Schedule

	scheduler.summon(context.Background(), &schedule, firings, harness, harness, held)
	if len(harness.summonses) != 0 || firings.summoning == nil {
		t.Fatalf("summonses = %d, waiting = %v; want no summons made while her pass holds her conversation, and one waiting", len(harness.summonses), firings.summoning != nil)
	}
	if schedule.BrakeProblem != "" {
		t.Fatalf("brake problem = %q, want a waiting summons to be no failure", schedule.BrakeProblem)
	}

	delete(firings.inFlight, dm)
	pull := Pull{Intake: harness}
	scheduler.summonWaiting(context.Background(), &schedule, firings, pull)
	if len(harness.summonses) != 1 || firings.summoning != nil {
		t.Fatalf("summonses = %d, waiting = %v; want the summons made once her pass ended", len(harness.summonses), firings.summoning != nil)
	}
	if harness.held.Brake.SummonedAt == nil {
		t.Fatalf("the hold does not record the summons: %+v", harness.held.Brake)
	}

	// A summons still waiting when the hold is lifted is dropped.
	firings.inFlight[dm] = StartedFiring{Task: "development-manager-sweep", Conversation: dm}
	scheduler.summon(context.Background(), &schedule, firings, harness, harness, *harness.held)
	delete(firings.inFlight, dm)
	harness.held = nil
	scheduler.summonWaiting(context.Background(), &schedule, firings, pull)
	if len(harness.summonses) != 1 || firings.summoning != nil {
		t.Fatalf("summonses = %d, waiting = %v; want a summons over a lifted hold dropped", len(harness.summonses), firings.summoning != nil)
	}
}
