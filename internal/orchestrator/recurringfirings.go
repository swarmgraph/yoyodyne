package orchestrator

// Firings of different roles are taken side by side, and never inside the pull.
//
// Until 2026-09-30 a watching session made at most one firing per pull, tasks
// first, and took the firing's turns on the pull's own thread. Both halves cost
// the same thing in the end. The Lead Product Manager's sweep ran every 45
// minutes and often took several turns, so on 2026-09-29 the development
// manager's sweep and the factory-flow program manager's pass each went more
// than an hour unfired, four times in one day, because "the pass took its one
// firing for the recurring task product-manager-sweep". The miss was reported at
// critical, the critical woke the Lead Product Manager out of her cadence as a
// firing of her task, and that firing took the one slot the starved roles were
// waiting for — so the report of a starved pass starved them again. And on
// 2026-09-30 one pass that spanned a machine sleep held the pull, and with it
// every item ready to start, for three and a half hours.
//
// So a pull now claims the firings that are due and hands their turns to
// goroutines of their own. Each firing holds its own role's conversation and
// nothing else: the pull goes on choosing work, delivering stopped runs, and
// firing other roles while the turns are taken. Two firings of one conversation
// are never taken at once, because a conversation takes one turn at a time; the
// second waits for the first to end. At most MaxConcurrentFirings are in flight
// at once, and where that bound is reached the firing that has waited longest
// since it fell due is claimed next, whatever kind of firing it is. A critical
// report's delivery waits by the time its oldest report was filed, so a starved
// role's pass that fell due an hour ago goes ahead of it.
//
// The brake's summons of the development manager and the wake correcting a
// refused tracker block are taken on the same path, though neither is a firing:
// each holds its conversation as a firing does, so neither is made into a
// conversation a pass is using, and no pass is started into one while it runs;
// see launchTurn.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MaxConcurrentFirings bounds how many recurring firings a watching session has
// in flight at once. It is the harness's rather than the project's, like the
// read-retry window: it is a bound on how many role conversations one session
// takes turns in at the same moment, and four covers the Lead Product Manager,
// the development manager, the architect, and a program manager instance each
// taking a pass together.
const MaxConcurrentFirings = 4

// ScheduleRecurringConcurrent is a recurring schedule the pull claims firings
// from and takes their turns beside itself, rather than inside. It is satisfied
// by Trigger, and asked for by assertion, so a schedule that can only fire in
// place is fired in place as it always was.
//
// Busy is the conversations a firing of this session is already taking turns
// in, and free how many more firings the bound leaves room for.
type ScheduleRecurringConcurrent interface {
	Start(ctx context.Context, busy map[string]bool, free int) (RecurringStart, error)
}

// RecurringStart is what one pull claimed from the schedule.
type RecurringStart struct {
	// Started are the firings claimed whose turns are still to be taken.
	Started []StartedFiring
	// Settled are firings claimed and already recorded without a turn: the
	// provider answering nobody, so nothing was asked.
	Settled []Fired
	// Waiting are the firings due that this pull did not claim, and why.
	Waiting []WaitingFiring
	// Paused is the operator's pause, when one is what stopped this. Nothing was
	// claimed and every task keeps its cadence.
	Paused *runstate.OperatorHold
}

// StartedFiring is one firing claimed and waiting for its turns to be taken.
type StartedFiring struct {
	Task string
	Role domain.AgentRole
	// Conversation is what the firing holds while its turns are taken: its
	// role's conversation, or a program manager instance's own.
	Conversation string
	// Due is when the firing fell due, where that is known.
	Due  time.Time
	take func(context.Context) Fired
	// beside marks a turn that is not a recurring firing at all — the brake's
	// summons, or the wake correcting a refused tracker block — taken on the same
	// path so it holds its conversation the same way; see launchTurn.
	beside bool
}

// Take takes the firing's turns and records what they came to. It never
// returns an error, for the reason run does not.
func (f StartedFiring) Take(ctx context.Context) Fired {
	if f.take == nil {
		return Fired{Task: f.Task, Role: f.Role}
	}
	return f.take(ctx)
}

// WaitingFiring is a firing that was due and not claimed. Full marks one the
// bound on firings in flight kept; otherwise its conversation was taken by
// another firing.
type WaitingFiring struct {
	Task         string
	Conversation string
	Due          time.Time
	Full         bool
}

// claimedFiring is one firing a candidate claimed: recorded already where
// settled is set, and turns still to take otherwise.
type claimedFiring struct {
	settled *Fired
	take    func(context.Context) Fired
}

// firingKind orders candidates that fell due at the same moment, and it is the
// order firings were made in before they were ordered by their wait: a critical
// report's delivery, then the tasks, then the program manager instances.
type firingKind int

const (
	firingCritical firingKind = iota
	firingTask
	firingInstance
)

// candidate is one firing a pull may claim.
type candidate struct {
	name         string
	role         domain.AgentRole
	conversation string
	kind         firingKind
	// due is when the firing fell due, and known whether that could be read. A
	// task that has never fired is due from the zero time, which is to say it has
	// waited longest of all.
	due   time.Time
	known bool
	claim func(ctx context.Context) (claimedFiring, bool, error)
}

// dueBy reports a candidate the schedule says is due at now.
func (c candidate) dueBy(now time.Time) bool {
	return c.known && !c.due.After(now)
}

// roleConversationKey keys the conversation a recurring task's firing takes turns
// in, which is its role's, and instanceConversationKey the one a program manager
// instance's pass takes turns in, which is the instance's own.
func roleConversationKey(role domain.AgentRole) string { return "role:" + string(role) }

func instanceConversationKey(agent string) string { return "agent:" + agent }

// Start claims the firings that are due, up to free of them and never two in one
// conversation or in a conversation busy already, in the order of how long each
// has waited since it fell due, and hands back their turns to be taken.
//
// The order the guarantees need is kept: the pause is read before anything is
// claimed, so a paused harness costs no task its cadence; every firing is
// claimed before its first turn is taken, so a process that dies between the two
// has recorded a firing that produced nothing rather than made one nothing
// paces; and each firing's account is written after its turns, because until the
// role has answered there is nothing to write.
func (t Trigger) Start(ctx context.Context, busy map[string]bool, free int) (RecurringStart, error) {
	if err := t.validate(); err != nil {
		return RecurringStart{}, err
	}
	hold, held, err := t.paused()
	if err != nil {
		return RecurringStart{}, err
	}
	if held {
		return RecurringStart{Paused: &hold}, nil
	}
	var problems []error
	// Recover a report or clearing whose write was interrupted after its pass
	// reached the sweep log, including a cancelled instance pass.
	if t.RecordFailures != nil {
		if err := t.RecordFailures(ctx); err != nil {
			problems = append(problems, fmt.Errorf("record product pass failure findings: %w", err))
		}
	}
	// Whether the provider is answering anybody is read once, before any task is
	// claimed. A firing made into a login nobody has renewed spends a claim on a
	// turn that cannot be served and records a turn that failed, which over three
	// days reads as a schedule that is broken rather than a provider that is away.
	outage, away, err := t.providerAway()
	if err != nil {
		problems = append(problems, err)
	}
	candidates, err := t.candidates(ctx, outage, away)
	if err != nil {
		problems = append(problems, err)
	}
	taken := make(map[string]bool, len(busy))
	for conversation, held := range busy {
		taken[conversation] = held
	}
	var start RecurringStart
	now := t.now()
	for _, next := range candidates {
		if taken[next.conversation] {
			if next.dueBy(now) {
				start.Waiting = append(start.Waiting, WaitingFiring{Task: next.name, Conversation: next.conversation, Due: next.due})
			}
			continue
		}
		if len(start.Started)+len(start.Settled) >= free {
			if next.dueBy(now) {
				start.Waiting = append(start.Waiting, WaitingFiring{Task: next.name, Conversation: next.conversation, Due: next.due, Full: true})
			}
			continue
		}
		claimed, took, err := next.claim(ctx)
		if err != nil {
			problems = append(problems, err)
		}
		if !took {
			continue
		}
		taken[next.conversation] = true
		if claimed.settled != nil {
			start.Settled = append(start.Settled, *claimed.settled)
			continue
		}
		start.Started = append(start.Started, StartedFiring{Task: next.name, Role: next.role, Conversation: next.conversation, Due: next.due, take: claimed.take})
	}
	return start, errors.Join(problems...)
}

// candidates is every firing the schedule might owe at this pull, in the order
// they are claimed: the one that has waited longest since it fell due first, and
// one that cannot say when it fell due after every one that can. Nothing here
// claims anything.
func (t Trigger) candidates(ctx context.Context, outage runstate.ProviderOutage, away bool) ([]candidate, error) {
	var problems []error
	now := t.now()
	dues := map[string]time.Time{}
	known := map[string]bool{}
	if _, readable := t.Claims.(recurringFinder); readable {
		// What could not be read is said by the miss check, which reads the same
		// cadence at the same pull; here it only leaves that firing unordered.
		read, _ := t.Cadence(ctx)
		for _, due := range read {
			if at, seen := dues[due.Task]; seen && !due.At.Before(at) {
				continue
			}
			dues[due.Task] = due.At
			known[due.Task] = true
		}
	}
	var candidates []candidate
	// A critical report nobody has put in front of the Lead Product Manager is
	// delivered as a firing of her task, in place of her task's own firing: the
	// delivery is her pass, and it moves her cadence as one. A provider answering
	// nobody leaves it undelivered rather than recorded as delivered into a
	// refusal, so it goes the first pull the provider answers.
	delivering := ""
	if !away {
		name, task, pending, err := t.criticalsWaiting()
		if err != nil {
			problems = append(problems, err)
		}
		if len(pending) > 0 {
			delivering = name
			due := pending[0].RecordedAt
			if taskDue, dueKnown := dues[name]; dueKnown && taskDue.Before(due) {
				due = taskDue
			}
			candidates = append(candidates, candidate{
				name: name, role: task.Role, conversation: roleConversationKey(task.Role), kind: firingCritical,
				due: due, known: true,
				claim: func(ctx context.Context) (claimedFiring, bool, error) {
					claimed, err := t.deliverCriticals(ctx, name, task, pending, pending[0].RecordedAt)
					return claimed, err == nil, err
				},
			})
		}
	}
	for _, name := range t.names() {
		task := t.Tasks[name]
		if !task.Enabled || name == delivering {
			continue
		}
		name, task := name, task
		due := dues[name]
		candidates = append(candidates, candidate{
			name: name, role: task.Role, conversation: roleConversationKey(task.Role), kind: firingTask,
			due: due, known: known[name],
			claim: func(ctx context.Context) (claimedFiring, bool, error) {
				return t.claimTask(ctx, name, task, due, outage, away)
			},
		})
	}
	// The architect's wake over the needs-design label holds her conversation as
	// a recurring task of hers does, so the two never take turns in it at once.
	if t.needsDesignWakeWired() {
		due := dues[NeedsDesignWakeTask]
		candidates = append(candidates, candidate{
			name: NeedsDesignWakeTask, role: domain.RoleArchitect, conversation: roleConversationKey(domain.RoleArchitect), kind: firingTask,
			due: due, known: known[NeedsDesignWakeTask],
			claim: func(ctx context.Context) (claimedFiring, bool, error) {
				return t.needsDesignWake(ctx, outage, away)
			},
		})
	}
	for _, agent := range t.instanceNames() {
		agent, instance := agent, t.Instances[agent]
		due := dues[agent]
		candidates = append(candidates, candidate{
			name: agent, role: domain.RoleProgramManager, conversation: instanceConversationKey(agent), kind: firingInstance,
			due: due, known: known[agent],
			claim: func(ctx context.Context) (claimedFiring, bool, error) {
				return t.pass(ctx, agent, instance, outage, away, due)
			},
		})
	}
	sort.SliceStable(candidates, func(first, second int) bool {
		a, b := candidates[first], candidates[second]
		aDue, bDue := a.due, b.due
		if !a.known {
			aDue = now
		}
		if !b.known {
			bDue = now
		}
		if !aDue.Equal(bDue) {
			return aDue.Before(bDue)
		}
		return a.kind < b.kind
	})
	return candidates, errors.Join(problems...)
}

// claimTask claims one recurring task's firing where it is due. The claim is the
// due check: asking first and claiming after would be two reads and a write with
// a window between them, which is exactly the window two concurrent sessions
// land in. A task that is not due is the ordinary answer on almost every pull,
// and so is one another process claimed a moment ago; neither is this pass's to
// report.
func (t Trigger) claimTask(ctx context.Context, name string, task config.RecurringTask, due time.Time, outage runstate.ProviderOutage, away bool) (claimedFiring, bool, error) {
	claimed, err := t.Claims.Claim(ctx, name, task.Every.Duration(), t.now())
	if err != nil {
		if errors.Is(err, runstate.ErrSweepNotDue) {
			return claimedFiring{}, false, nil
		}
		return claimedFiring{}, false, fmt.Errorf("claim the firing of the recurring task %s: %w", name, err)
	}
	if away {
		fired := t.refuse(ctx, name, task, outage)
		return claimedFiring{settled: &fired}, true, nil
	}
	return claimedFiring{take: func(ctx context.Context) Fired {
		batch := t.amendmentBatch(task)
		return t.run(ctx, firing{
			name: name, pass: passName(claimed), task: task, trigger: runstate.PassTriggerSchedule,
			message: wakeMessage(name, task, "", t.overdueFor(task), t.addressedTo(task), batch), batch: batch, due: due,
		})
	}}, true, nil
}

// firingDone is one firing whose turns are over, carried back to the session
// that started it, which is the only goroutine that touches the schedule.
type firingDone struct {
	started StartedFiring
	fired   Fired
	at      time.Time
	// settle is what a turn taken beside the firings writes once it is
	// collected, in place of a firing's account; see launchTurn.
	settle func(*Schedule)
}

// recurringFirings is a watching session's firings in flight, by the
// conversation each holds.
type recurringFirings struct {
	inFlight map[string]StartedFiring
	done     chan firingDone
	// cancel stops every firing in flight, which the redeploy drain does once
	// its bound has run out, with the RedeployDrain as the cause. A firing's
	// turns can be carrying a document publication (internal/chat,
	// PublishDocuments), and that run reads the cause to record the restart as
	// what stopped it rather than an unexplained cancellation.
	ctx    context.Context
	cancel context.CancelCauseFunc
	// taken, where set, counts the turns launchTurn has in flight until each is
	// handed back; see Scheduler.besideTurns.
	taken *sync.WaitGroup
	// summoning is the brake's summons of the development manager, where one
	// found a pass of hers, or another turn, taking turns in her conversation and
	// is waiting for it to end. It is made by summonWaiting at the first pull
	// after that.
	summoning *waitingSummons
}

// waitingSummons is a summons of the development manager held back while her
// own pass holds her conversation: how the hold it is for is revised, and how
// she is reached.
type waitingSummons struct {
	brake   ScheduleBrake
	summons ScheduleSummons
}

func newRecurringFirings(ctx context.Context) *recurringFirings {
	firingCtx, cancel := context.WithCancelCause(ctx)
	return &recurringFirings{
		inFlight: map[string]StartedFiring{},
		// Buffered to the bound and the turns taken beside it, so a firing that
		// ends never waits on the session collecting it: the session may be asleep
		// for a poll interval.
		done:   make(chan firingDone, MaxConcurrentFirings+besideTurns),
		ctx:    firingCtx,
		cancel: cancel,
	}
}

func (f *recurringFirings) idle() bool { return len(f.inFlight) == 0 }

func (f *recurringFirings) busy() map[string]bool {
	busy := make(map[string]bool, len(f.inFlight))
	for conversation := range f.inFlight {
		busy[conversation] = true
	}
	return busy
}

// holding names the task whose firing holds a conversation, where one does.
func (f *recurringFirings) holding(conversation string) (string, bool) {
	started, held := f.inFlight[conversation]
	return started.Task, held
}

// launch takes one firing's turns in a goroutine of its own.
func (f *recurringFirings) launch(started StartedFiring, now func() time.Time) {
	f.inFlight[started.Conversation] = started
	go func() {
		fired := started.Take(f.ctx)
		f.done <- firingDone{started: started, fired: fired, at: now()}
	}()
}

// besideTurns is how many turns that are not recurring firings a session can
// have in flight at once: one summons of the development manager by the brake,
// and one wake correcting a refused tracker block.
const besideTurns = 2

// summonsTask and correctionTask name the two turns taken beside the firings,
// where a firing waiting behind one says what holds its conversation.
const (
	summonsTask    = "the brake's summons of the development manager"
	correctionTask = "the wake correcting a refused tracker block"
)

// launchTurn takes a turn that is not a recurring firing on the same path the
// firings take theirs: in a goroutine of its own, holding its conversation
// against every firing and every other such turn until it is collected, while
// the pull goes on. The brake's summons and the wake correcting a refused
// tracker block are taken this way, because either taken inside the pull held
// the pull for the length of a turn, and either made into a conversation a
// firing of this session was taking turns in was refused.
//
// What the turn came to is written on the session's goroutine, by the settle
// that take returns, when the turn is collected — the schedule and the hold's
// record are written by nobody else. These turns are not counted against
// MaxConcurrentFirings when they start, so neither waits on a slot, but each
// holds one while it runs, so the firings yield to them.
func (f *recurringFirings) launchTurn(started StartedFiring, take func(context.Context) func(*Schedule), now func() time.Time) {
	started.beside = true
	f.inFlight[started.Conversation] = started
	taken := f.taken
	if taken != nil {
		taken.Add(1)
	}
	go func() {
		settle := take(f.ctx)
		f.done <- firingDone{started: started, settle: settle, at: now()}
		if taken != nil {
			taken.Done()
		}
	}()
}

// taking reports whether a turn of task taken beside the firings is in flight.
func (f *recurringFirings) taking(task string) bool {
	for _, started := range f.inFlight {
		if started.beside && started.Task == task {
			return true
		}
	}
	return false
}

// summonsPending reports a summons of the development manager in flight, or
// held back for her own pass to end.
func (f *recurringFirings) summonsPending() bool {
	return f.summoning != nil || f.taking(summonsTask)
}

// startFirings claims what the schedule has due, takes the turns of each
// firing beside the pull, and records on the schedule what was settled without
// a turn. What it returns is what kept a due task from firing at all — the
// schedule failing or the operator's pause — for the miss a later pass may
// find; what kept one task behind another is recorded per task on the watch.
func (s Scheduler) startFirings(ctx context.Context, schedule *Schedule, recurring ScheduleRecurringConcurrent, firings *recurringFirings, watch *recurringWatch) recurringHold {
	free := MaxConcurrentFirings - len(firings.inFlight)
	start, err := recurring.Start(ctx, firings.busy(), free)
	held := recurringHold{at: s.now()}
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("the recurring schedule could not be fired, so standing work is waiting on somebody starting it: %v", err))
		held.why = fmt.Sprintf("the harness could not fire its recurring schedule: %v", err)
	}
	if start.Paused != nil {
		held.why = fmt.Sprintf("the operator paused harness activity at %s", start.Paused.HeldAt.UTC().Format(time.RFC3339))
		held.quiet = true
	}
	for _, started := range start.Started {
		firings.launch(started, s.now)
	}
	fired := false
	for _, settled := range start.Settled {
		schedule.SpentUSD += settled.CostUSD
		if settled.Turns > 0 {
			schedule.Fired = append(schedule.Fired, settled)
			fired = true
		}
		if settled.Problem != "" {
			problems = append(problems, settled.Problem)
		}
		if held.why == "" && settled.Turns == 0 && strings.TrimSpace(settled.Problem) != "" {
			held.why = strings.TrimSpace(settled.Problem)
			held.refused = true
		}
	}
	watch.waiting = map[string]recurringHold{}
	for _, waiting := range start.Waiting {
		watch.waiting[waiting.Task] = recurringHold{why: waitingReason(waiting, firings), at: held.at}
	}
	switch {
	case len(problems) > 0:
		schedule.RecurringProblem = strings.Join(problems, "; ")
	case fired:
		schedule.RecurringProblem = ""
	}
	return held
}

// waitingReason is what kept one due firing behind the others.
func waitingReason(waiting WaitingFiring, firings *recurringFirings) string {
	if waiting.Full {
		var holding []string
		for _, started := range firings.inFlight {
			holding = append(holding, started.Task)
		}
		sort.Strings(holding)
		return fmt.Sprintf("all %d of the firings a session takes at once were in flight (%s), and the firings that had waited longer since falling due went first",
			MaxConcurrentFirings, strings.Join(holding, ", "))
	}
	if started, held := firings.inFlight[waiting.Conversation]; held {
		if started.beside {
			return fmt.Sprintf("its conversation was taking the turn of %s, which had not ended", started.Task)
		}
		return fmt.Sprintf("its conversation was taking the turns of the recurring task %s, which had not ended", started.Task)
	}
	return "its conversation was taking the turns of another firing, which had not ended"
}

// collectFirings takes every firing whose turns have ended into the schedule,
// and with block waits for every one still in flight. It is the only place a
// firing's result reaches the schedule, so the session's goroutine is the only
// one that touches it.
func (s Scheduler) collectFirings(schedule *Schedule, firings *recurringFirings, watch *recurringWatch, block bool) {
	for !firings.idle() {
		var done firingDone
		if block {
			done = <-firings.done
		} else {
			select {
			case done = <-firings.done:
			default:
				return
			}
		}
		delete(firings.inFlight, done.started.Conversation)
		if done.settle != nil {
			done.settle(schedule)
			continue
		}
		fired := done.fired
		// What the firing cost is the session's spend, exactly as a delivery's is:
		// the provider charged for the turns either way, and a session bounded by a
		// budget must not spend past it on turns nothing counted. It is counted at
		// the pull after the turns end, which is when the budget is next read.
		schedule.SpentUSD += fired.CostUSD
		if fired.Turns > 0 {
			schedule.Fired = append(schedule.Fired, fired)
		}
		switch {
		case fired.Problem != "":
			schedule.RecurringProblem = fired.Problem
		case fired.Turns > 0:
			schedule.RecurringProblem = ""
		}
		// A firing that reached nobody — the provider out of capacity or answering
		// nobody, the role's conversation held — says why in the words the refusal
		// came with. It is the provider's or the lease's rather than the harness's
		// own, so a miss it leads to is said as a warning.
		if fired.Turns == 0 && strings.TrimSpace(fired.Problem) != "" {
			watch.hold(recurringHold{why: fired.Problem, at: done.at, refused: true})
		}
	}
}

// summonWaiting makes the brake's summons that waited on the development
// manager's conversation, once the turn holding it has ended, and only while the brake's hold
// it was for still stands and is still the harness's to work. A hold she or the
// operator lifted in the meantime, or one escalated past her, has
// nothing left to summon her over, so the waiting summons is dropped. A hold
// that cannot be read is left for the next pull.
func (s Scheduler) summonWaiting(ctx context.Context, schedule *Schedule, firings *recurringFirings, pull Pull) {
	waiting := firings.summoning
	if waiting == nil || pull.Intake == nil {
		return
	}
	hold, held, err := pull.Intake.Held()
	if err != nil {
		return
	}
	firings.summoning = nil
	if !held || !hold.Braked() || hold.WaitsOnAPerson() {
		return
	}
	s.summon(ctx, schedule, firings, waiting.brake, waiting.summons, hold)
}
