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
// It is then taken beside the pull, as her pass was: the pull that made it
// returns while she is still answering, her conversation is held against every
// firing until the summons is collected, and the cooldown running out probes
// nothing meanwhile. A hold lifted while a summons waits drops it.
func TestABrakeSummonsWaitsForTheDevelopmentManagersOwnPass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	entered := make(chan struct{}, 1)
	answer := make(chan struct{})
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		entered <- struct{}{}
		<-answer
		return Fired{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Turns: 1, CostUSD: 0.25, Summoned: "the intake brake"}, nil
	}
	held, err := harness.Brake(runstate.IntakeBrake{Blocked: []runstate.BrakeBlockedRun{{WorkItemID: "yoyodyne-ifd.1", Reason: "the checks failed"}}, CooldownEndsAt: harness.clock()}, "3 run(s) blocked in a row", harness.clock())
	if err != nil {
		t.Fatalf("Brake() error = %v", err)
	}
	firings := newRecurringFirings(context.Background())
	defer firings.cancel()
	dm := roleConversationKey(domain.RoleDevelopmentManager)
	pass := make(chan struct{})
	firings.launch(StartedFiring{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Conversation: dm, take: func(context.Context) Fired {
		<-pass
		return Fired{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Turns: 2}
	}}, harness.clock)
	scheduler := Scheduler{Now: harness.clock}
	var schedule Schedule
	pull := Pull{Intake: harness, Brake: harness}

	scheduler.summon(context.Background(), &schedule, firings, harness, harness, held)
	if firings.summoning == nil || firings.taking(summonsTask) {
		t.Fatalf("waiting = %v, taking = %v; want no summons made while her pass holds her conversation, and one waiting", firings.summoning != nil, firings.taking(summonsTask))
	}
	if schedule.BrakeProblem != "" {
		t.Fatalf("brake problem = %q, want a waiting summons to be no failure", schedule.BrakeProblem)
	}
	// The cooldown has run out, and the line is not probed while she has not
	// been asked.
	if action := scheduler.workBrake(&schedule, pull, *harness.held, nil, firings.summonsPending()); action != brakeWaiting {
		t.Fatalf("brake action = %v with her summons waiting, want the hold waited on", action)
	}

	close(pass)
	scheduler.collectFirings(&schedule, firings, &recurringWatch{}, true)
	scheduler.summonWaiting(context.Background(), &schedule, firings, pull)
	// summonWaiting has returned, and her summoned turn is still being taken:
	// the pull is not held by it.
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the summons was not made once her pass ended")
	}
	if firings.summoning != nil || !firings.taking(summonsTask) || !firings.busy()[dm] {
		t.Fatalf("waiting = %v, busy = %v; want the summons taken beside the pull, holding her conversation", firings.summoning != nil, firings.busy())
	}
	if action := scheduler.workBrake(&schedule, pull, *harness.held, nil, firings.summonsPending()); action != brakeWaiting {
		t.Fatalf("brake action = %v while her summoned turn is taken, want the hold waited on", action)
	}
	// A firing of hers claimed now would be refused the conversation, so the
	// schedule is told it is busy and her task waits.
	claims := sweepStore(t)
	trigger := Trigger{
		Tasks:  map[string]config.RecurringTask{"development-manager-sweep": hourly(domain.RoleDevelopmentManager, 2)},
		Claims: claims, Reports: claims, Roles: newSideBySideRoles(domain.RoleDevelopmentManager), Clock: harnessClock(harness.clock),
	}
	start, err := trigger.Start(context.Background(), firings.busy(), MaxConcurrentFirings-len(firings.inFlight))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(start.Started) != 0 || len(start.Waiting) != 1 {
		t.Fatalf("started %+v, waiting %+v; want her own task waiting on the summons", start.Started, start.Waiting)
	}
	if why := waitingReason(start.Waiting[0], firings); !strings.Contains(why, summonsTask) {
		t.Fatalf("waiting reason = %q, want the summons named as what holds her conversation", why)
	}

	close(answer)
	scheduler.collectFirings(&schedule, firings, &recurringWatch{}, true)
	if harness.held.Brake.SummonedAt == nil || schedule.BrakeProblem != "" {
		t.Fatalf("hold = %+v, problem = %q; want the summons recorded on the hold once collected", harness.held.Brake, schedule.BrakeProblem)
	}
	if len(schedule.Fired) != 2 || schedule.Fired[1].Summoned == "" || schedule.SpentUSD != 0.25 {
		t.Fatalf("fired = %+v, spent $%.2f; want her pass and then the summons on the schedule, and the summons counted", schedule.Fired, schedule.SpentUSD)
	}
	if !firings.idle() || firings.summonsPending() {
		t.Fatal("the summons is still in flight after it was collected")
	}

	// A summons still waiting when the hold is lifted is dropped.
	firings.inFlight[dm] = StartedFiring{Task: "development-manager-sweep", Conversation: dm}
	scheduler.summon(context.Background(), &schedule, firings, harness, harness, *harness.held)
	delete(firings.inFlight, dm)
	harness.held = nil
	scheduler.summonWaiting(context.Background(), &schedule, firings, pull)
	if len(harness.summonses) != 1 || firings.summoning != nil || !firings.idle() {
		t.Fatalf("summonses = %d, waiting = %v; want a summons over a lifted hold dropped", len(harness.summonses), firings.summoning != nil)
	}
}

// gatedCorrections is roles' conversations as a correction reaches them from a
// goroutine of its own: each wake says it began on entered and waits on gate.
type gatedCorrections struct {
	mu      sync.Mutex
	woken   []runstate.ConversationIdentity
	entered chan struct{}
	gate    chan struct{}
}

func (r *gatedCorrections) Wake(ctx context.Context, identity runstate.ConversationIdentity, _ string) (CorrectionTurn, error) {
	r.mu.Lock()
	r.woken = append(r.woken, identity)
	r.mu.Unlock()
	r.entered <- struct{}{}
	select {
	case <-r.gate:
	case <-ctx.Done():
		return CorrectionTurn{}, ctx.Err()
	}
	return CorrectionTurn{CostUSD: 0.1, Actions: 2}, nil
}

func (r *gatedCorrections) wokenOf() []runstate.ConversationIdentity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runstate.ConversationIdentity(nil), r.woken...)
}

// The wake correcting a refused tracker block, arriving while the development
// manager's recurring pass holds her conversation. Her refusal is not claimed —
// a wake made into her conversation would be refused it — and waits for the
// pass to end, while another role's refusal is woken for beside it. Each wake is
// taken beside the pull: the pull that made it returns while the role is still
// answering, one is in flight at a time, and what it came to is on the schedule
// once it is collected.
func TestACorrectionWaitsForAPassHoldingItsConversationAndIsTakenBesideThePull(t *testing.T) {
	t.Parallel()

	store := conversationStore(t, t.TempDir())
	refusedConversationOf(t, store, domain.RoleDevelopmentManager, "development-manager", 3, correctionNow.Add(-time.Hour), "her older refusal")
	refusedConversation(t, store, "product-manager", 5, correctionNow.Add(-time.Minute), "his newer refusal")
	roles := &gatedCorrections{entered: make(chan struct{}, 2), gate: make(chan struct{})}
	pull := Pull{Corrections: Corrector{Conversations: store, Claims: store, Roles: roles, Clock: correctionClock{}}}
	firings := newRecurringFirings(context.Background())
	defer firings.cancel()
	dm := roleConversationKey(domain.RoleDevelopmentManager)
	firings.inFlight[dm] = StartedFiring{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Conversation: dm}
	scheduler := Scheduler{Now: func() time.Time { return correctionNow }}
	var schedule Schedule
	entered := func(who string) {
		t.Helper()
		select {
		case <-roles.entered:
		case <-time.After(30 * time.Second):
			t.Fatalf("the %s was not woken", who)
		}
	}

	// Her refusal is the older, and her pass holds her conversation, so his is
	// woken for — and the pull has returned while his turn is being taken.
	scheduler.correct(context.Background(), &schedule, pull, firings, true)
	entered("product manager")
	if woken := roles.wokenOf(); len(woken) != 1 || woken[0].Role != domain.RoleProductManager {
		t.Fatalf("woken = %+v, want only the product manager while her pass holds her conversation", woken)
	}
	if !firings.taking(correctionTask) || !firings.busy()[roleConversationKey(domain.RoleProductManager)] {
		t.Fatalf("busy = %v, want his wake taken beside the pull and holding his conversation", firings.busy())
	}
	// One wake is in flight at a time.
	scheduler.correct(context.Background(), &schedule, pull, firings, true)
	if woken := roles.wokenOf(); len(woken) != 1 {
		t.Fatalf("woken = %+v, want no second wake while one is in flight", woken)
	}

	// His turn ends and is collected; her pass goes on.
	close(roles.gate)
	delete(firings.inFlight, dm)
	scheduler.collectFirings(&schedule, firings, &recurringWatch{}, true)
	firings.inFlight[dm] = StartedFiring{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Conversation: dm}
	if len(schedule.Corrected) != 1 || schedule.Corrected[0].Role != domain.RoleProductManager || schedule.SpentUSD != 0.1 {
		t.Fatalf("corrected = %+v, spent $%.2f; want his wake on the schedule and counted", schedule.Corrected, schedule.SpentUSD)
	}

	// Her pass still holds her conversation: her refusal is left unclaimed.
	scheduler.correct(context.Background(), &schedule, pull, firings, true)
	if woken := roles.wokenOf(); len(woken) != 1 || firings.taking(correctionTask) {
		t.Fatalf("woken = %+v, want her refusal left while her pass holds her conversation", woken)
	}
	recorded, err := store.Load(runstate.ConversationIdentity{Agent: "development-manager", Role: domain.RoleDevelopmentManager})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.RefusedBlock == nil || recorded.RefusedBlock.Attempts != 0 {
		t.Fatalf("refusal = %+v, want her wakeup unclaimed and unspent while her pass ran", recorded.RefusedBlock)
	}

	// Her pass ends, and the next pull wakes her beside it.
	delete(firings.inFlight, dm)
	scheduler.correct(context.Background(), &schedule, pull, firings, true)
	entered("development manager")
	if !firings.busy()[dm] {
		t.Fatalf("busy = %v, want her wake holding her conversation", firings.busy())
	}
	scheduler.collectFirings(&schedule, firings, &recurringWatch{}, true)
	if len(schedule.Corrected) != 2 || schedule.Corrected[1].Role != domain.RoleDevelopmentManager || schedule.Corrected[1].Actions != 2 {
		t.Fatalf("corrected = %+v, want her wake on the schedule once her pass ended", schedule.Corrected)
	}
	if schedule.CorrectionProblem != "" {
		t.Fatalf("correction problem = %q, want none", schedule.CorrectionProblem)
	}
}
