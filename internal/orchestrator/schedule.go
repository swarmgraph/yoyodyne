package orchestrator

// The ready-work scheduler: what the harness starts when nobody names an item.
//
// Every run before this one was an operator typing an identifier, so choosing
// was not a thing the harness did and concurrency was not a thing it had. This
// is both at once, and the two are the same mechanism: the scheduler reads the
// backlog in the product manager's order, takes the items the tracker itself
// reports as pullable, and starts as many of them at once as the configured
// developer capacity leaves free.
//
// Almost nothing here enforces anything. Every constraint a scheduled run is
// held to is already enforced where it belongs — capacity and duplicate work at
// the reservation, the intake hold and the unresolved directives in the
// pipeline, integration order in the promotion lease, and a moved target branch
// in the promotion itself — and re-implementing any of them here would create a
// second account of the same rule that could disagree with the first. What this
// adds is the choosing: which items, in what order, how many at a time, and, for
// every one of them, the recorded reason it was chosen.
//
// Four things it does decide about an item itself, and all four are choosing
// rather than enforcing, which is why they are here. The first is whether the
// item is work at all: a container whose unfinished children carry its execution
// is a heading over the queue rather than an entry in it, and nothing downstream
// can tell — the tracker reports it as pullable, the reservation sees a different
// item from its child, and both runs then make the same change twice. The second
// is whether it is work to start now: an item and the epic a run is already
// over, or two items over the same files, still integrate correctly when they
// are raced, and what that costs is a replay, a fresh set of checks, and a fresh
// review on whichever loses. So they are sequenced instead. See conflict.go,
// which also says why being filed under one epic is not itself a race.
//
// The third is whether a developer run is what carries the work at all. An item
// admitted as conversation-executed — a promotion the architect makes to a
// document it owns, a decomposition settled in conversation — is passed over
// rather than started, and the backlog is where that is decided rather than
// here: an item nothing can run has to look the same to everything that reads
// the order. What is here is saying so against the item, because the alternative
// is silence. The cost of not doing it was measured: an architect's item was
// selected as ordinary developer work and spent a whole run and two review
// rounds producing a correctly refused empty diff — and those rounds count
// against the item's cap, so a second mis-selection would have escalated work
// nobody had ever started.
//
// Parked work is the same shape and was measured the same way. An item the
// product manager has deliberately taken out of reach is passed over rather than
// started, decided in the backlog for the same reason, and said out loud here
// for the same one. The cost that bought it: the parking used to be expressed as
// the bottom of the priority order, which is a reading nothing that pulls
// shares, and on 2026-08-27 a drained queue reached it and spent $34.38 on a run
// of deferred work that failed. Nothing about that selection was wrong. What was
// wrong was that the decision lived somewhere selection could not look, and a
// watch session drains a queue every day it is quiet.
//
// The fourth is whether the tree holds what the item says it needs. An item that
// pinpoints code the tree no longer has, or that states in its own words that
// something must land first, is passed over and put on the development manager's
// docket rather than started. It is here for the reason the other three are —
// nothing downstream can tell, because the tracker knows about dependency links
// and not about a sentence or a citation — and it is the only one that costs a
// read of the repository, which is why it is asked last, of an item everything
// else would have started. Four items in a fortnight were dispatched with an
// unmeetable prerequisite and cost a full run each to establish it; see the
// readiness package for which four and what the two readings are.
//
// # Work the queue never offers
//
// One kind of work reaches a developer through this loop without ever being in
// the queue: a stoppage the development manager has already decided about. Her
// decision is durable and spends the item's budget as she records it, and until
// yoyodyne-ifd.346 the only thing that acted on one was a person typing `yoyo
// triage repair` — which left thirty-three items decided and unfired, some for
// days. So the pull fires them, as many as it has slots for, against the same
// capacity everything else is chosen against and before the queue is read,
// because a stoppage already
// judged is work this harness has spent a run on and the queue's own entries have
// not been. Every gate that refused a carry-out typed by hand refuses this one,
// and every refusal is written onto the item where the development manager reads
// it. See carryout.go.
//
// # A pull re-reads the configuration
//
// The scheduler is the first thing in the harness that holds a configuration
// across time. Every command before it loaded the file, did one thing, and
// exited, so "when is a capacity change picked up?" had the answer "at the next
// command" without anybody deciding it. Here it is decided: the configuration is
// re-read at every pull, so a capacity raised or a priority reordered takes
// effect at the next selection. That keeps the answer the one the rest of the
// harness already gives, and it matches how the backlog is steered — a product
// manager reorders the queue and expects the next thing pulled to reflect it,
// not the next restart. Runs already in flight keep the configuration they
// started under, because a run's own parameters are fixed when it is reserved.
//
// # What it will not do
//
// It never withholds work because something upstream of it changed. Staleness is
// derived rather than stored and stops, closes, blocks, and reorders nothing, so
// an item whose goal was amended after it was admitted is pulled exactly as it
// would have been — and the fact is written into the run's recorded selection
// reason, where whoever reads what the harness chose can see it.
//
// # Draining and watching
//
// A pass either drains what is ready and returns, or stays open until somebody
// stops it. They are one loop with one difference: where a drain concludes the
// queue is empty and stops choosing, a watch waits out a configured interval and
// reads it again. Nothing else changes, and nothing else needed to — every pull
// already re-reads the configuration, re-reads the intake hold, takes the queue
// in the product manager's order, and records why it chose what it chose, so a
// reprioritization is honored at the next pull and an admission at the next poll
// without anything here detecting either.
//
// What watching adds is four guards, and each one is against a failure that
// only exists because the loop no longer ends. An item whose run failed before
// it ever started is left alone until something about the item changes, because
// a queue the harness cannot get past is one it would otherwise re-pull every
// interval for as long as it ran. Runs blocking one after another with nothing
// landing between them hold intake, because systemic breakage left overnight
// would otherwise put the whole backlog through a failed run each — and the
// hold is then worked rather than waited on: the development manager is
// summoned at once to decide it, and a probe run decides it on evidence if she
// does not, so a brake hold waits on a person only once it is escalated — by
// her, or by the harness after that summons-and-probe loop has gone round its
// configured number of times (see brake, workBrake, and settleProbe). And a
// session says what it is doing
// where somebody who is not at its terminal can read it, because an idle
// session and a dead one are the same silence.
//
// # A session that redeploys itself
//
// The fourth thing watching adds is the one failure the loop is itself. A
// session goes on choosing and dispatching from the binary it was started with
// while fixes land behind it, so the work it dispatches is spent against defects
// that were fixed hours before — which reads as agents failing rather than as a
// process nobody restarted. It cost three review rounds on 2026-08-30 against a
// bug that had been dead for a day, and on 2026-08-31 the session choosing work
// was found forty-three changes old.
//
// Nothing outside the process can close that. Killing a session cancels the run
// it is carrying, so an external job may only bounce it while nothing is
// running; with more than one developer slot and a deep queue the next run
// starts the moment one settles, and a poll at any interval never lands in that
// window. The session is the only thing standing in it.
//
// So the session takes the deploy up itself. When it finds that the binary it
// was started from has been replaced, it stops choosing, waits out the runs it
// already started, and stops with ScheduleRedeployed — which is the caller's
// signal to re-execute. A live run is never interrupted for it, and that is why
// this wins the race an external restart loses rather than being the same thing
// moved inside: the session declining to claim anything more is what makes the
// window exist at all, and nothing but the session can decline.
//
// A drain never does any of this. It is a command somebody is waiting on the
// return of, and restarting it would run the pass again from the beginning.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/developerslot"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readiness"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
)

// scheduledStatuses are the tracker slices the backlog is assembled from: work
// that has been admitted and is not finished. Claimed work has been pulled
// already and closed work has left, so neither is still queued.
var scheduledStatuses = []string{"open", "blocked"}

// claimedStatus is the tracker slice that has left the backlog by being pulled.
// The scheduler does not choose from it, and reads it for one thing: a child
// somebody is running right now is the strongest possible cover over its
// parent's execution, and it is precisely the child a queue reading alone cannot
// see. It is the backlog's name for that slice rather than a second one, so the
// surfaces that read coverage read the same slice this pass does.
const claimedStatus = backlog.StatusClaimed

// maxScheduleReasonBytes bounds one part of a recorded selection reason that
// came from a document rather than from this package. The reason as a whole has
// its own bound in the run state; this keeps a single amendment's prose from
// filling it.
const maxScheduleReasonBytes = 240

// Bound the cause alone, leaving the condition and remedy readable.
const maxBlockedDetailBytes = 100

// How a watch session rides through a reading of the harness that failed.
//
// The tracker is a database a reconcile and every settling run write to, so a
// reading that fails is contention far more often than it is a store that is
// broken. The one that ended a session on 2026-09-01 succeeded again in 0.4s a
// few minutes later, and what it cost was the session: it stopped on that single
// reading, and the queue sat until an external job noticed the process was gone
// and started another. A session that dies on one contended read defeats
// everything built on the session outliving the work it starts, the
// self-redeploy above first among it.
//
// So a watch waits and reads again, doubling the wait from the first delay to
// the longest, and stops only once the readings have gone on failing for the
// window. The window is the whole of what separates contention from breakage,
// which is why a session that stops on one says how long it tried rather than
// only what failed.
const (
	firstReadRetryDelay   = 2 * time.Second
	longestReadRetryDelay = 30 * time.Second
	readRetryWindow       = 5 * time.Minute
)

// Why the scheduler stopped pulling. Each is a different thing for an operator
// to do about it, which is why they are stated apart rather than folded into one
// "nothing more was started".
const (
	// ScheduleDrained reports a scheduler that ran out of work to pull: nothing
	// the tracker reports as ready is left that this pass has not already tried.
	ScheduleDrained = "nothing more is ready to pull"
	// ScheduleIntakeHeld reports intake being held. Nothing further was chosen;
	// whatever was already running carried on to its own end. Who is holding it
	// is on the hold this schedule carries rather than in this sentence: a
	// constant cannot know whether the operator or the harness's own brake
	// placed it, and one that named either would be wrong half the time.
	ScheduleIntakeHeld = "intake is held, so nothing more was chosen"
	// ScheduleLimitReached reports the requested number of runs having been
	// started, which is the operator bounding one pass rather than the harness
	// running out of anything.
	ScheduleLimitReached = "the requested number of runs was started"
	// ScheduleCapacityFull reports every developer slot occupied by runs this
	// scheduler does not own, so there was nothing to wait for and no room to
	// start anything.
	ScheduleCapacityFull = "every developer slot is held by a run this pass did not start"
	// ScheduleUnreadable reports a pull that could not be made at all. What
	// failed is on the schedule beside it; runs already started were waited out
	// rather than abandoned.
	ScheduleUnreadable = "the harness could not be read for another pull"
	ScheduleBlocked    = "no run can be started until what is stopping them is cleared"
	// ScheduleCancelled reports a scheduler whose context ended. Runs already
	// started see the same cancellation and are waited out.
	ScheduleCancelled = "the scheduler was cancelled"
	// ScheduleBudgetSpent reports a session that reached the spend the operator
	// bounded it to. It is the operator bounding one session rather than the
	// harness running out of anything, which is why it reads like the limit
	// above rather than like a failure.
	ScheduleBudgetSpent = "the session spent the budget it was given"
	// ScheduleRedeployed reports a session that stopped to take up the binary
	// deployed over the one it was started from. It is not the end of the line:
	// the caller re-executes, and the session that comes back is watching the
	// same queue from the build that was deployed.
	ScheduleRedeployed = "a build was deployed over the one this session was started from, and the session is restarting into it"
	// ScheduleProviderAway reports a drain that stopped because the provider is
	// answering nobody — a login nobody has renewed, an API nothing reaches. A
	// watch waits it out instead; a drain is a command somebody is waiting on the
	// return of, and one that slept through a login would be one that hung.
	ScheduleProviderAway = "the provider is answering nobody, so nothing more was chosen"
	// ScheduleProviderWindow reports a drain that stopped because a recorded
	// usage limit covers every model a developer's turn could end on, and the
	// provider named a reset that has not come. A watch waits it out instead.
	ScheduleProviderWindow = "the provider's usage limit is reached for every developer model, so nothing more was chosen"
	// ScheduleDivergedTarget reports a drain that stopped because a target branch
	// stands recorded as one the harness will not catch up to the remote's. A
	// watch waits it out instead, until the convergence sweep finds the branches
	// converged; every run a drain started into it would stop on the same
	// divergence after a whole development and review.
	ScheduleDivergedTarget = "a target branch will not catch up to the remote's, so nothing more was chosen"
	// ScheduleLaunchSettingsHeld reports a drain that stopped because the
	// developer's provider was found not to apply what a developer is
	// launched with. A watch waits it out instead; every developer a drain
	// started into it would be refused on the same check.
	ScheduleLaunchSettingsHeld = "the developer's provider did not apply its sandbox and guard, so nothing more was chosen"
	// ScheduleSpendUnreadable reports a bounded session that stopped because it
	// could not tell what it had spent. A budget measured against evidence
	// nobody can read is not a smaller budget, it is no budget at all, so the
	// session stops rather than carrying on inside a bound it has lost the
	// ability to hold. An unbounded pass is unaffected: nothing there was
	// spending against a number.
	ScheduleSpendUnreadable = "what this session had spent could not be read, and it was given a budget to stay inside"
)

// ScheduleTracker is the tracker access one pull needs: the work, by status, so
// the admitted part can be put in the product manager's order and the claimed
// part can say what is already being worked on, and the tracker's own account of
// what can be pulled now. Readiness is asked for rather than inferred, for the
// reason the backlog states — a listing carries dependencies without carrying
// whether they are finished, so only the tracker's dependency graph can answer
// it.
type ScheduleTracker interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
	Ready(ctx context.Context) ([]beads.WorkItem, error)
}

// ScheduleRuns is the durable run state one pull reads. It reads and never
// adopts: what is in flight is both what fills the configured capacity and what
// must not be started a second time, and answering either question is not acting
// on a run another process owns.
type ScheduleRuns interface {
	Incomplete() ([]runstate.State, error)
}

// ScheduleGates is the human gates a person has recorded passing. It is required
// rather than optional, and it is required of the pull rather than read from the
// tracker, because it is the one refusal the tracker cannot express: a step
// somebody reserved for themselves has no encoding there but an item to close,
// and machinery closing that item is how a reserved step was jumped once
// already. A pull that cannot read it schedules nothing rather than scheduling
// past a gate.
//
// It is satisfied by *runstate.Store.
type ScheduleGates interface {
	DischargedGates() (map[string][]string, error)
}

// ScheduleStaleness reports the admitted work something upstream of changed
// after it was admitted. It is optional and it decides nothing: a pull wired
// without one schedules exactly the same items in exactly the same order, and
// what is lost is a sentence in each run's recorded reason.
type ScheduleStaleness interface {
	Stale(ctx context.Context) ([]staleness.WorkItem, error)
}

// ScheduleBrake is how a failure storm stops the line: the same intake hold an
// operator places, placed by the harness when runs keep blocking with nothing
// landing between them. It is satisfied by runstate.IntakeHoldStore.
//
// What the harness releases is its own hold and never the operator's. A brake
// hold used to wait on a person exactly as the operator's does, and that was
// the stall the operator ended on 2026-09-19: five trips in seventeen days,
// each held until somebody noticed. Now the brake summons the development
// manager the moment it trips, releases on her decision, and — where she has
// decided nothing by the cooldown — probes the line with one run and releases
// on that run landing. A brake hold waits on a person only once it is
// escalated: by her, or by the harness itself once a bounded number of those
// summons-and-probe cycles have gone round with her not escalating it.
// ReleaseBrake is here for those two releases; ReviseBrake is how the summons,
// the decision's carry-out, the probe, and the harness's own escalation are
// written onto the hold's own record, so every surface reading the hold says
// what is deciding it. The operator's hold is never touched by any of them:
// ReleaseBrake and
// ReviseBrake both refuse any hold that is not the brake's own.
type ScheduleBrake interface {
	Brake(trip runstate.IntakeBrake, reason string, at time.Time) (runstate.IntakeHold, error)
	ReviseBrake(revise func(*runstate.IntakeBrake) error) (runstate.IntakeHold, error)
	ReleaseBrake(by string, at time.Time) (runstate.IntakeHold, bool, error)
}

// ScheduleSummons is how the brake puts its trip in front of the development
// manager at once: her sweep fired out of its cadence, with the runs that
// blocked and the reason each did in the message that wakes her. It is
// satisfied by *Trigger.
//
// It is optional, and a session wired without one still brakes and still
// releases itself: what is lost is the summons, so the hold is decided by the
// cooldown's probe rather than by her, and the hold's record says she could not
// be summoned.
type ScheduleSummons interface {
	Summon(ctx context.Context, summons BrakeSummons) (Fired, error)
}

// ScheduleSpend prices what a session has spent, from the same recorded run
// evidence `yoyo cost` reads.
//
// It is optional exactly as far as the budget is. A pass with no budget runs the
// same items without one and simply prices nothing; a pass given a budget is
// refused without one, because a bound nothing can measure is not a bound and
// must not be reported as one. The same rule holds once a session is running: a
// run whose evidence will not price stops a bounded session rather than being
// counted as free.
//
// It is satisfied by runstate.Store.
type ScheduleSpend interface {
	Price(workItemID string) (runstate.ItemPrice, error)
}

// ScheduleDeployment reports whether the binary this session is executing has
// been deployed over since it started. It is satisfied by redeploy.Binary.
//
// It is optional, and a session wired without one behaves exactly as every
// session did before: it goes on running what it was started with until somebody
// restarts it, which is the state the package comment above describes the cost
// of. It is asked once per pull and answers from one file reading, so an idle
// session pays for it what it pays for reading the queue.
type ScheduleDeployment interface {
	Replaced() (bool, error)
}

// SessionState is one transition a watch session records about itself: what it
// entered, when, why, and whether the stop it is recording is a session coming
// straight back rather than a line going down.
type SessionState struct {
	State  runstate.WatchState
	At     time.Time
	Reason string
	// Running is how many developer runs the pass could see in flight when it
	// recorded this, and Executor is the conversation that carries the work it
	// passed over where one does. They travel with the reason because they are the
	// two facts a reader of an idle line was missing: whether the harness is
	// nonetheless working, and who has to act before the answer changes.
	Running  int
	Executor domain.WorkItemExecutor
	// PassedOver is what the poll left where it was, in the classes the read model
	// groups them into. The reason says the same account in prose for whoever is
	// reading the log; this is the same account for whatever has to answer a
	// question about it, and it is what keeps the stall alarm from deriving its own
	// answer beside a session that had already worked one out.
	PassedOver runstate.PassedOver
	// Unreadable marks the poll that chose nothing because the harness could not
	// be read at all. It travels for the same reason the two above do: nothing a
	// person admits, releases, or opens changes the answer while the store will
	// not answer, so a reader told to admit work would be told to do the one thing
	// that cannot help.
	Unreadable bool
	// ProviderWindow marks the poll made while the provider is refusing the harness
	// for want of capacity, and ProviderWindowResetsAt is when the provider said
	// that window lifts. They travel for the same reason again, and they close the
	// case that had nothing at all saying it: a session waiting out a window looks
	// from every record like a session finding nothing to start, so ninety minutes
	// of it on 2026-09-05 was read as a line that had stopped and woke somebody.
	ProviderWindow         bool
	ProviderWindowResetsAt *time.Time
	// Restarting marks the one stop that is not an ending — the session waiting
	// out its runs to be re-executed into a build deployed over it. It is what
	// keeps every surface from telling the operator to start a session that is
	// already on its way back, which is the standing chore this whole mechanism
	// exists to end rather than reproduce once per deploy.
	Restarting bool
	// Mover is whose move follows a braked poll, worded by the hold's own record
	// where the hold carries one. It travels for the reason the executor does:
	// the clause a channel closes a braked message on used to name the operator
	// whatever held the line, and a brake hold is the development manager's or
	// the harness's until she escalates it.
	Mover string
	// DispatchWait is a dispatch this session started waiting out a tracker failure
	// before it has claimed anything. An entry carrying one is a note about that
	// dispatch rather than a transition of the session, and is written from the
	// dispatch's own goroutine as the wait is taken; see
	// runstate.WatchTransition.Note.
	DispatchWait *runstate.DispatchWait
	// WorktreeCrossing is a Git command a dispatch this session started ran again
	// because it crossed another worktree's creation or removal. It is a note
	// about that dispatch in the same way a dispatch wait is; see
	// runstate.WatchTransition.WorktreeCrossing.
	WorktreeCrossing *runstate.WorktreeCrossing
	// RecurringPass is a recurring pass the session has begun, beside its poll
	// or, for a schedule that can only fire there, inside it, written as the pass
	// starts. It is a note in the same way a dispatch wait
	// is; see runstate.WatchTransition.RecurringPass.
	RecurringPass *runstate.WatchPass
	// Draining marks every transition the session makes while it waits out the
	// runs it hosts to restart into a build deployed over it, with the bound on
	// that wait. It is beside the state rather than a state because draining is
	// about the runs the session hosts and not about what it is otherwise doing:
	// a draining session polls, pulls into free seats, and fires its recurring
	// tasks exactly as it did, and each of those lines is owed the drain beside
	// it.
	Draining *runstate.WatchDrain
}

// ScheduleEnvironment reports whether the machine can start a run at all. It is
// the same readiness a run checks for itself, asked before anything is chosen
// rather than after — satisfied by *gitworktree.Manager.
//
// Asking it here changes nothing about what is enforced, exactly as the intake
// hold read below changes nothing: the run would refuse for itself either way.
// What it changes is what the refusal is a fact about. Met inside a run, a dirty
// primary checkout arrives as that item's failed start, and a watching session
// records the item as one it has tried and moves down the queue doing the same to
// every other item in it — after which a backlog full of ready work reads as an
// exhausted one, and the operator has a silent machine with no line anywhere
// saying why. Met here it is a state of the line, said in words that name the
// file and the move that ends it.
//
// It is optional, and a pull wired without one chooses exactly what it would have
// chosen. What is lost is the naming, not the choosing.
type ScheduleEnvironment interface {
	ValidateReady(ctx context.Context) error
}

// WatchSessions is where a watch session says what it is doing, for the reader
// who is not at its terminal. It is optional: a session wired without one
// behaves identically and is simply invisible between the runs it starts, which
// is the state this exists to end.
//
// Record is called from the dispatches a session starts as well as from the
// session itself, so it is called concurrently.
type WatchSessions interface {
	Record(SessionState) error
}

// ScheduleTree is the repository as it stands, which is what an item's stated
// prerequisites are checked against before a slot is spent on it. It is
// satisfied by *readiness.Repository.
//
// It is optional, and a pull wired without one chooses exactly what it chose
// before this existed: no item is held back for a prerequisite nobody read, and
// what is lost is the catching rather than the choosing.
type ScheduleTree interface {
	readiness.Tree
}

// ScheduleTriage routes an item the tree is not ready for to the development
// manager's docket, naming what is unmet. It is satisfied by Docketer.
//
// It is optional and separate from the reading, deliberately. A pull that can
// read the tree and cannot write the docket still passes the item over rather
// than dispatching it — the refusal is the useful half, and it is on the pass's
// own report either way — and what it loses is the durable record. Withholding
// the refusal because the record could not be made would spend a run to avoid
// losing a line.
//
// It also records the dispatch that never became a run, which is the other
// thing this pass knows and nothing else does. A run that dies after it is
// reserved leaves a record for a sweep to find; one that dies before that leaves
// nothing anywhere, so the only process that can say it happened is the one that
// tried it.
//
// And it takes the unready entries off again. An entry is a reading of the item
// at one pull, and the pull is the only thing that reads the item again, so it
// is the only thing that can say the reading no longer holds; see
// Docketer.SettleUnreadyItems.
type ScheduleTriage interface {
	RecordUnreadyItem(item beads.WorkItem, unmet []readiness.Unmet) (bool, error)
	SettleUnreadyItems(reread func(workItemID string) UnreadyReading) (int, error)
	RecordUnstartedAttempt(attempt UnstartedAttempt) (bool, error)
}

// ScheduleEscalations puts stopped work in front of the development manager,
// once per docketed stoppage. It is satisfied by Escalator.
//
// It is optional, and a pull wired without one pulls exactly the same work: what
// is lost is the delivery, so a run that stopped waits on somebody carrying it
// to her, which is what every pass did before this existed.
type ScheduleEscalations interface {
	Escalate(ctx context.Context) (EscalationSweep, error)
}

// ScheduleCarryOut fires the triage decisions the development manager recorded
// and nobody has acted on, as many per pull as there are developer slots for
// them, and writes down every one it did not reach. It is satisfied by CarryOut.
//
// It is optional, and a pull wired without one pulls exactly the same work: what
// is lost is the firing, so a recorded decision waits on somebody typing `yoyo
// triage repair` or `yoyo triage rerun`, which is what every one of them waited
// on before this existed — thirty-three of them at once, for days.
//
// It is split in two because a carry-out is a run rather than a turn. The pass
// asks what is outstanding, which reads records and starts nothing, and then
// starts the ones it chose in goroutines against developer slots, exactly as it
// starts an item the queue offered. A sweep that did both inside the pull would
// hold the queue closed for the length of a whole run.
//
// The third half is the account of what the pass did not attempt. A decision
// the pass never hands to an action is one no gate refuses, so nothing else
// writes a word of it: RecordUnattempted is given the offered decisions the pull
// passed over, with why, and writes onto the item every decision standing a
// poll interval after it was recorded with nothing attempted.
type ScheduleCarryOut interface {
	Outstanding() ([]CarryOutTask, error)
	Carry(ctx context.Context, task CarryOutTask) (CarriedOut, Outcome, error)
	RecordUnattempted(ctx context.Context, poll time.Duration, passed map[string]string) ([]CarriedOut, error)
}

// ScheduleRearms fires the re-arms decided about publications — a merge nothing
// ever asked the forge for, or one the forge dropped. A ScheduleCarryOut that also satisfies it has them
// fired on every pull; it is satisfied by *CarryOut, and asked for by assertion
// so a carry-out that fires only runs is wired exactly as it was.
type ScheduleRearms interface {
	CarryRearms(ctx context.Context, intakeHeld bool) ([]CarriedOut, error)
}

// ScheduleCarryOutNotes delivers pending tracker notes without retrying a
// refused action or occupying a developer slot. It is satisfied by *CarryOut.
type ScheduleCarryOutNotes interface {
	DeliverNotes(context.Context) error
}

// ScheduleRecurring fires the configured recurring tasks, at most one per pass,
// taking the firing's turns inside the pull. It is satisfied by Trigger, which
// also satisfies ScheduleRecurringConcurrent, and a pull whose schedule does is
// fired through that instead: its turns are taken beside the pull, and firings
// of different roles side by side.
//
// It is optional, and a pull wired without one pulls exactly the same work: what
// is lost is the schedule, so standing work waits on a person remembering to
// start it, which is what it waited on before this existed.
type ScheduleRecurring interface {
	Fire(ctx context.Context) (RecurringSweep, error)
}

// ScheduleCorrections wakes the role whose tracker block the harness refused, at
// most one per pass. It is satisfied by Corrector.
//
// It is optional, and a pull wired without one pulls exactly the same work: what
// is lost is the self-correction, so a refused block waits on somebody opening
// that role's conversation, which is what it waited on before this existed.
type ScheduleCorrections interface {
	Correct(ctx context.Context) (CorrectionSweep, error)
}

// ScheduleLandings closes the items a conversation carries whose landing is
// already in the repository — a revision of a document the conversation's role
// owns, opening with the item's identifier. It is satisfied by
// ConversationLander.
//
// It is optional, and a pull wired without one pulls exactly the same work:
// what is lost is the closing, so a design-only item waits on the product
// manager closing it on evidence after the fact, which is what every one of
// them waited on before this existed — and what two of them cost a turn each
// for, one of them after a developer run had been spent on it.
type ScheduleLandings interface {
	Settle(ctx context.Context, entries []backlog.Entry) (LandingSweep, error)
}

// ScheduleClaims audits the items the tracker says are claimed against the runs
// the harness actually has, and gives back the ones with nothing alive behind
// them. It is satisfied by ClaimAuditor.
//
// It is optional, and a pull wired without one pulls exactly the same work and
// spends exactly what it spent before — the tracker reading the audit needs is
// made where the audit is, so a pull with no audit makes it where it always did.
// What is lost is the only thing that ever frees an item whose run died, which is
// why every session the harness builds is given one.
type ScheduleClaims interface {
	Audit(ctx context.Context, claimed []beads.WorkItem) (ClaimSweep, error)
}

// Starter runs one chosen item to its end. It is a function rather than the
// pipeline itself because a pull hands each run the configuration that pull
// read, and because what the scheduler needs from a run is only its outcome.
type Starter func(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error)

// Pull is one reading of the harness: the configuration in force, the durable
// state built from it, and the way a chosen item is run. It is assembled fresh
// for every pull rather than once for the command, which is what makes a
// configuration edit take effect at the next selection.
type Pull struct {
	Tracker ScheduleTracker
	Runs    ScheduleRuns
	// Stoppages is the harness's own record of the work it stopped and nobody has
	// decided about, which is what tells a dependency block that has since cleared
	// from a stoppage somebody still has to release. It is optional, and a pull
	// wired without it holds every blocked item rather than choosing work whose
	// hold it could not read; see backlog.Holds for why that is the safe
	// direction. It is satisfied by *runstate.Store.
	Stoppages readmodel.Stoppages
	// Decisions is what triage has already decided about the items those
	// stoppages belong to, which is what separates a held item waiting on the
	// development manager from one waiting on the harness carrying her decision
	// out. It is optional, and a pull wired without it passes every held item over
	// as one nobody has decided about — which is where the answer went before the
	// two were told apart. It is satisfied by *runstate.TriageStore.
	Decisions readmodel.Decisions
	// Remains is what the repository actually holds of each stopped run's
	// change, which is what a hold on a preserved change is decided from rather
	// than the run's own removal flags. It is optional, and a pull wired without
	// it decides from the record and says so in the hold. It is satisfied by
	// *gitworktree.Manager.
	Remains readmodel.Remains
	Intake  IntakeHolds
	// Holds is the operator's pause over everything the harness spends. The pass
	// enforces nothing with it — the actions it fires read the same switch and
	// refuse under it — and reads it for one thing: whether a decision the pause
	// already stopped once is worth attempting again this pull. Optional, and a
	// pull wired without it attempts such a decision on every pull the pause
	// stands, which is what a pass that cannot see a switch has to do.
	Holds      OperatorHolds
	Directives Directives
	// Gates is the human gates a person has passed; see ScheduleGates for why a
	// pull without one is refused rather than run.
	Gates ScheduleGates
	// Staleness is optional; see ScheduleStaleness for what a pull without one
	// loses, which is a sentence rather than a constraint.
	Staleness ScheduleStaleness
	// Environment is the readiness a run also checks before reserving.
	Environment ScheduleEnvironment
	// Capacity is execution.max_concurrent_developers as this pull read it. It
	// bounds how many runs the scheduler starts; the reservation enforces the
	// same number across every process, and this only keeps the scheduler from
	// walking into a refusal it can see coming.
	Capacity int
	// Slots is execution.developer_slots as this pull read it: what each of the
	// Capacity developer slots prefers, in slot order, with the slots the list
	// does not name preferring nothing. A slot that prefers a label pulls the
	// ready work carrying it first and the rest of the backlog only when none is
	// ready; see developerslot for how the free slots are read off what is in
	// flight. Empty is every slot pulling in the product manager's order, which
	// is what every pull did before slots could prefer anything.
	Slots []domain.DeveloperSlot
	// Poll is execution.work_poll as this pull read it: how long a watch session
	// waits before reading the queue again. It is read per pull like everything
	// else here, so an interval changed under a running session takes effect at
	// the next wait rather than at the next restart. A drain never waits and
	// never reads it.
	Poll time.Duration
	// BlockedRunsBeforeIntakeHold is execution.blocked_runs_before_intake_hold as
	// this pull read it: how many runs may block in a row, with nothing landing
	// between them, before the brake holds intake. Zero never brakes.
	BlockedRunsBeforeIntakeHold int
	// BrakeCooldown is execution.brake_cooldown as this pull read it: how long a
	// tripped brake waits for the development manager's decision before it
	// probes the line by itself. Zero waits for her summoned turn and no longer,
	// because the summons is taken before the cooldown is read.
	BrakeCooldown time.Duration
	// BrakeEscalationCycles is execution.brake_escalation_cycles as this pull
	// read it: how many summons-and-probe cycles the brake goes round before it
	// escalates the hold to the operator itself. Zero never escalates on its own.
	BrakeEscalationCycles int
	// Brake places that hold and works it. It is optional, and a session wired
	// without one counts the storm and reports it without stopping the line,
	// because a brake nothing can apply must not be reported as applied.
	Brake ScheduleBrake
	// Summons puts the trip in front of the development manager the moment the
	// brake trips. Optional; see ScheduleSummons.
	Summons ScheduleSummons
	// Spend prices what the session has spent. It is required of a pass that was
	// given a budget and of no other; see ScheduleSpend.
	Spend ScheduleSpend
	// Escalations delivers stopped work to the development manager. Optional; see
	// ScheduleEscalations.
	Escalations ScheduleEscalations
	// CarryOut fires the decisions she recorded about it. Optional; see
	// ScheduleCarryOut. It is re-read at every pull like everything else here, so a
	// decision recorded at any hour is fired at the next interval rather than at
	// the next time somebody looks.
	CarryOut ScheduleCarryOut
	// Tree is the repository an item's stated prerequisites are read against, and
	// Triage is where an item that does not meet them is routed. Both optional;
	// see ScheduleTree and ScheduleTriage. They are re-read at every pull like
	// everything else here, so an item held back for a pinpoint the tree does not
	// have is pulled at the first pull after the code lands.
	Tree   ScheduleTree
	Triage ScheduleTriage
	// Recurring fires what the configuration schedules on a cadence. Optional;
	// see ScheduleRecurring.
	Recurring ScheduleRecurring
	// Corrections wakes a role whose tracker block was refused, so the actions it
	// lost are re-issued without a person prompting it. Optional; see
	// ScheduleCorrections.
	Corrections ScheduleCorrections
	// Outages is the product's record of the provider answering nobody, and
	// Provider is what a watch asks whether the login has been renewed. Both
	// optional; see ScheduleOutages. A pull wired without them counts a dispatch
	// the provider turned away toward nothing all the same — that is decided from
	// the dispatch's own error — and loses only the wait between pulls.
	Outages  ScheduleOutages
	Provider ScheduleProvider
	// OutageProbe is how long a provider nobody can reach is left before a pull
	// is made into it again to find out whether it answers. It is
	// execution.usage_limit_unknown_reset_pause as this pull read it, because
	// that is the one interval the configuration states for "ask again rather
	// than being told when". Zero reads as the login's interval: a pull every
	// poll.
	OutageProbe time.Duration
	// UsageLimits is the product's record of the provider refusing the harness for
	// want of capacity, and Developers is every endpoint a developer's turn can
	// end on. Both optional; see usageWindow. A pull wired without them learns a
	// window only from a run that came back parked on one, which is what a session
	// restarted inside a window never has.
	UsageLimits readmodel.UsageLimits
	Developers  []readmodel.AgentEndpoint
	// CapacityServed and Conversations are what the refusals above are read
	// against before they hold anything: a refusal the provider has since served
	// its account and model through, or a refusal of a conversation its role has
	// replaced, holds no intake. Both optional; a pull wired without them holds on
	// every refusal until its quoted reset, as it did before the record existed.
	CapacityServed readmodel.CapacityServedRecord
	Conversations  readmodel.Conversations
	// Claims gives back the claims with nothing alive behind them. Optional; see
	// ScheduleClaims.
	Claims ScheduleClaims
	// Landings closes conversation-carried work whose landing is in the
	// repository. Optional; see ScheduleLandings. It is re-read at every pull
	// like everything else here, so an item whose design lands is closed at the
	// first pull after the revision is in the tree.
	Landings ScheduleLandings
	// Continuations is what a pull reads to continue a run paused on work its
	// item waits on once that work has closed. Optional; see
	// ScheduleContinuations.
	Continuations ScheduleContinuations
	// Divergences is the product's record of the target branches the harness
	// will not catch up to the remote's. Optional; see ScheduleDivergences. A
	// pull wired without it chooses into a wedged target exactly as it did, and
	// each run it starts stops on the divergence itself.
	Divergences ScheduleDivergences
	// LaunchSettings is the product's record of a developer's provider that did
	// not put in force what a developer is launched with, and Build the harness
	// revision this pull runs, which a hold another build placed is lifted on.
	// Optional; see launchSettingsHeld. A pull wired without the record chooses
	// as it did, and each dispatch it makes is refused on the check itself.
	LaunchSettings ScheduleLaunchSettings
	Build          string
	// RedeployDrainLimit is execution.redeploy_drain_limit as this pull read it:
	// how long a session that has found a build deployed over it waits out the
	// runs it hosts before restarting anyway, with those runs stopped and
	// preserved for the session that comes back. It is re-read at every pull like
	// everything else here, so a bound lengthened under a draining session moves
	// its deadline at the next pull. A drain never reads it.
	RedeployDrainLimit time.Duration
	Start              Starter
}

// ScheduleOutages is the product's record of the provider answering nobody, as
// a watch reads and clears it. It is satisfied by *runstate.ProviderOutageStore.
//
// A watch reads it before every pull. While it stands the session chooses
// nothing and says why — the brake never counts a dispatch the provider turned
// away, and a dispatch made into a login nobody has renewed would be turned
// away again — and it is the watch that finds the login renewed: a login is
// asked about cheaply, and a session polling every minute is what makes
// re-authentication resume the line without anybody releasing anything.
type ScheduleOutages interface {
	Standing() (runstate.ProviderOutage, bool, error)
	Clear() (runstate.ProviderOutage, bool, error)
}

// ScheduleProvider is the developer's provider, as a watch asks it one thing:
// whether the machine is logged in. It is satisfied by backend.Backend.
type ScheduleProvider interface {
	CheckAvailability(ctx context.Context) (backend.Availability, error)
}

// pullNeeds is what this pass will actually ask of a pull, which is not the same
// for every pass. A watch waits, so it needs an interval; a session spending
// against a number needs a way to read what it has spent. A drain asks for
// neither and is not refused for lacking either.
type pullNeeds struct {
	waits   bool
	bounded bool
}

func (p Pull) validate(needs pullNeeds) error {
	var problems []error
	if needs.waits && p.Poll <= 0 {
		problems = append(problems, fmt.Errorf("a watch interval of %s reads the queue with nothing between the readings", p.Poll))
	}
	// A drain with no bound is a session that waits on whatever its hosted run
	// happens to be doing, which is the two-hour silence the bound exists to end.
	if needs.waits && p.RedeployDrainLimit <= 0 {
		problems = append(problems, fmt.Errorf("a redeploy drain limit of %s never restarts a session that is hosting a run", p.RedeployDrainLimit))
	}
	// A budget with nothing to measure it against is the one refusal here that
	// is about the operator rather than about the harness: they asked for a
	// bound, and a pass that ran anyway would be reporting a bound it never had.
	if needs.bounded && p.Spend == nil {
		problems = append(problems, errors.New("a pull given a budget requires a way to price what it has spent"))
	}
	if p.Tracker == nil {
		problems = append(problems, errors.New("a pull requires a work tracker"))
	}
	if p.Runs == nil {
		problems = append(problems, errors.New("a pull requires the durable run state"))
	}
	if p.Intake == nil {
		problems = append(problems, errors.New("a pull requires the intake hold"))
	}
	if p.Directives == nil {
		problems = append(problems, errors.New("a pull requires the recorded directives"))
	}
	// A pull with no way to read the recorded human acts cannot tell a gate
	// somebody passed from one nobody has, so it would either hold every gated
	// item forever or start past all of them. Refusing here is the only reading
	// of that which is neither.
	if p.Gates == nil {
		problems = append(problems, errors.New("a pull requires the human gates a person has passed"))
	}
	if p.Start == nil {
		problems = append(problems, errors.New("a pull requires a way to start a run"))
	}
	if p.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which schedules nothing", p.Capacity))
	}
	return errors.Join(problems...)
}

// Scheduler starts the work the harness chooses for itself, up to the
// configured developer capacity, and waits for every run it started.
type Scheduler struct {
	// Open assembles one pull. It is called once per pull rather than once per
	// scheduler, which is the whole of how a configuration change is picked up.
	Open func(ctx context.Context) (Pull, error)
	// Limit bounds how many runs one pass starts. Zero means no bound on the
	// count, which is what an unattended scheduler wants; an operator sitting in
	// front of one wants a number. What ends an unbounded pass is what it was
	// asked for: an empty queue for a drain, and the operator for a watch.
	Limit int
	// Watching keeps the pass open when it runs out of work instead of
	// returning: the queue is read again after the configured interval, and the
	// pass ends only when the operator stops it, when a bound it was given is
	// reached, or when the harness can no longer be read.
	Watching bool
	// Budget caps what one session may spend, in the provider's own reported
	// dollars, over everything the session spends on: the runs it starts, and the
	// turns it takes itself putting stopped work in front of the development
	// manager. Zero is unbounded, which is what a drain has always been. The bound
	// is checked between pulls rather than during a run or a turn: what is already
	// spent is spent, and what stopping part way would lose is the work it bought.
	Budget float64
	// Sessions is where the session's state transitions are recorded. Optional;
	// see WatchSessions.
	Sessions WatchSessions
	// SessionID names this session where a run it stops for its own redeploy
	// records which session stopped it. Optional; a run stopped by a session
	// that gave none records none.
	SessionID string
	// Deployment is how a watching session finds out that the binary it is
	// executing has been deployed over. Optional; see ScheduleDeployment. It is
	// consulted only while watching, because a drain is a command somebody is
	// waiting on the return of rather than a process that outlives a deploy.
	Deployment ScheduleDeployment
	// stoppedRunGrace is how long a session restarting past its drain bound
	// waits for the runs it stopped to report back; zero is stoppedRunGrace's
	// default. It is a field only so a test can shorten it.
	stoppedRunGrace time.Duration
	// Watchdog notices that this product has started nothing at all while work
	// was ready, and records it where every surface reads it back. It is called
	// once per pull, from this loop's own goroutine and before anything is
	// chosen, and it decides nothing about the pass: it cannot stop it, cannot
	// fail it, and is never consulted about what to start.
	//
	// It is here because this is the harness's own loop — the one process that is
	// running whenever the harness is choosing work at all — and a watchdog that
	// hung off an optional process was no watchdog for the products that never
	// started one. What it catches from here is the session that is alive and has
	// stopped starting anything; the session that died writes nothing and is
	// caught by `yoyo reconcile`, which is the other invoker for exactly that
	// reason.
	//
	// It is optional, and a pass wired without one runs precisely as it did
	// before this existed. A drain is wired without one deliberately: it is a
	// command somebody is waiting on the return of rather than a process that
	// keeps running, which is the same reason the redeploy check is a watch's
	// alone. What it costs is the caller's to bound — see the gate in the command
	// that wires it, because this loop polls in seconds and the reading behind it
	// spawns a tracker process.
	Watchdog func(ctx context.Context)
	// Sleep waits out one poll interval and reports false when the context ended
	// first. It is injected so a test does not have to spend real seconds, and
	// defaults to a timer.
	Sleep func(ctx context.Context, interval time.Duration) bool
	// Interval is the poll interval a watching session with a developer slot free
	// waits on beside a run of its own, which a completion may cut short. It is
	// injected apart from Sleep because it is raced against the run rather than
	// waited out whole, and defaults to a timer.
	Interval func(interval time.Duration) <-chan time.Time
	// Now stamps the recorded transitions. It defaults to the wall clock.
	Now func() time.Time
}

// Started is one item this pass chose, and what became of the run for it.
type Started struct {
	WorkItemID string `json:"work_item_id"`
	// Reason is what was recorded on the run as why this item was chosen. It is
	// repeated here so a schedule read on its own accounts for every run in it,
	// which is the same reason the run records it at all.
	Reason  string  `json:"reason"`
	Outcome Outcome `json:"outcome"`
	// Declined reports a start that never became a run because the slot or the
	// item went to another process between this pull and the reservation. It is
	// not a failure: the scheduler asked for something that had just stopped
	// being available, which is the ordinary outcome of two schedulers running.
	//
	// A triage decision the harness fired and a gate stopped is recorded here too,
	// and for the same reason rather than by analogy: nothing was reserved, claimed
	// or spent, so what happened is a start that never became a run. What the gate
	// said is the whole of the text, and the same account is on the item's own
	// triage record where it outlives this pass.
	Declined string `json:"declined,omitempty"`
	Failure  string `json:"failure,omitempty"`
	// Probe marks the run the brake started under its own hold to find out
	// whether the line is fine. It is on the record because what became of it
	// decided the hold, and a reader of the schedule has to be able to see which
	// run that was.
	Probe bool `json:"probe,omitempty"`
	// Slot is the developer slot the item was pulled into, counted from 1 as the
	// configuration counts them. It is on the record beside the reason, which
	// says why that slot took it, so a reader checking a slot's preference against
	// what it actually pulled has both in one place.
	Slot int `json:"developer_slot,omitempty"`
	// Readopted names the run this start continued rather than began: one the
	// session before this one stopped for its own redeploy, picked up from
	// durable state at its recorded phase with every counter as it was. It is
	// on the record because a reader of the schedule is owed the difference
	// between a run this session chose and one it was handed.
	Readopted string `json:"readopted,omitempty"`
	// Readoptions counts how many times this session tried to pick that run up.
	// A re-adoption the pipeline never took — a lease another process held at
	// that moment, a tracker that would not answer — leaves the run's record
	// carrying its stop, and the session tries again at its next pull rather
	// than leaving a run whose note promises a re-adoption nobody is going to
	// make. The count is how a reader tells one such refusal from a run that
	// keeps being refused.
	Readoptions int `json:"readoptions,omitempty"`
	// awayCause is set when Failure is the provider turning the dispatch away —
	// a login nobody has renewed, an API nothing reaches — which the settle reads
	// to count the start toward nothing. It is not on the record because the
	// failure already is, in words that say the same thing.
	awayCause domain.ProviderOutageCause
	// settingsHeld is set when Failure is the developer's provider found not to
	// put in force what a developer is launched with, which the settle counts
	// toward nothing for the same reason. See LaunchSettingsError.
	settingsHeld bool
	// environmental is set when the run stopped for a cause the environment
	// answers for rather than a verdict on the change — a dirty checkout, a
	// transport that did not answer, a sandbox that would not spawn, a target
	// branch that diverged from the remote's — which the settle reads to count
	// the stop toward nothing. It is not on the record because the outcome
	// already carries the classification.
	environmental bool
}

// Deferred is one pullable item this pass declined to start, and why.
//
// Six things land here: an unresolved directive, an item whose unfinished
// children already carry its execution, an item that would have raced work
// already in flight over the epic it was broken out of or over the same files,
// an item whose
// executor is a persona conversation rather than a developer run, an item
// somebody parked, and an item every free developer slot walked past for the
// label it prefers — left for another slot, which is a wait on capacity rather
// than on anything about the item. Each is named
// against the item rather than counted, because each is a fact about that item
// that nothing else in the harness would report — the first needs a person, and
// the rest are the scheduler passing over something the tracker called ready.
// The third is a wait rather than a refusal: the item is pulled at the
// first pull where what it would have raced has ended, and the run that pulls it
// records having waited. The last two are the opposite of a wait, and say so: no
// pull will ever take either, and what moves them is somebody opening the
// conversation the item names, or the product manager releasing the parking.
// The other reasons an
// item is not started — the tracker not calling it ready, a run for it already
// being in flight, no free developer slot — are facts about the pass rather than
// about any one item, and the counts on the schedule report them at that grain.
// A line per unready item would be a line per backlog entry on every pass, which
// is how a listing stops being read at all.
//
// An item is one line however many pulls passed it over, and the line says what
// the last of those pulls found rather than the first: an item held behind
// three runs in turn over a session names the third, with the run itself named
// so the reader can check it against `yoyo status`.
type Deferred struct {
	WorkItemID string `json:"work_item_id"`
	Reason     string `json:"reason"`
}

// Schedule is what one pass did.
type Schedule struct {
	Started  []Started  `json:"started,omitempty"`
	Deferred []Deferred `json:"deferred,omitempty"`
	// IntakeHeld is the operator's hold, when one is what stopped the choosing.
	IntakeHeld *runstate.IntakeHold `json:"intake_held,omitempty"`
	// Capacity and Occupied are what the last pull read: the configured limit,
	// and how many developer slots were already taken when it read it.
	Capacity int `json:"capacity"`
	Occupied int `json:"occupied"`
	// Admitted and Pullable are the size of the backlog and how much of it the
	// tracker called ready, as of the last pull that got far enough to read the
	// queue — a pull that found every slot taken stops before that, and leaves
	// these where the pull before it put them.
	//
	// They are the pass-level answer to why an item was not started, and they are
	// counts rather than a list on purpose: "nothing more is ready to pull" is a
	// different fact when the backlog is empty and when forty admitted items are
	// all waiting on something, and naming those forty every pass would bury the
	// deferrals that actually need reading.
	Admitted int `json:"admitted"`
	Pullable int `json:"pullable"`
	// BacklogRead reports that some pull got as far as reading the queue. Without
	// it, counts of zero would be indistinguishable from a pass that stopped
	// before it ever looked — a held intake, or a machine already full — and
	// "0 admitted items" over a backlog nobody read is the confident emptiness
	// every report in this harness is written to avoid.
	BacklogRead bool `json:"backlog_read"`
	// Stopped says why the scheduler stopped pulling, in the words of one of the
	// Schedule* reasons above.
	Stopped string `json:"stopped"`
	// Blocked is what refuses every run, as of the last pull.
	Blocked string `json:"blocked,omitempty"`
	// StalenessProblem names a staleness reading that failed. It costs the
	// recorded reasons a sentence and costs the schedule nothing else, so it is
	// reported beside the pass rather than failing it.
	StalenessProblem string `json:"staleness_problem,omitempty"`
	// AttemptProblem names a dispatch that never became a run and could not be
	// recorded where anything outside this session would find it. It costs the pass
	// nothing it was doing, so it is reported beside the pull rather than stopping
	// it — but never left unsaid: an attempt that failed into no record at all is
	// the silence the record exists to end, and a record that failed to be made is
	// the same silence one step further back.
	AttemptProblem string `json:"attempt_problem,omitempty"`
	// ReadinessProblem names a reading of the tree that failed, or an unready item
	// that could not be routed to the development manager. Neither stops the pass:
	// the first leaves the item chosen exactly as it would have been, and the
	// second leaves the refusal on this report rather than on the docket. Both are
	// said out loud, because a guard that silently stopped guarding is the failure
	// the guard was written against, one level up.
	ReadinessProblem string `json:"readiness_problem,omitempty"`
	// Watched reports a pass that stayed open rather than draining, and Polls
	// counts the intervals it waited out. A session that started nothing and
	// polled four hundred times is a session that was alive, which is the fact a
	// schedule read afterwards would otherwise be missing.
	Watched bool `json:"watched,omitempty"`
	Polls   int  `json:"polls,omitempty"`
	// Escalated is the stopped work this pass put in front of the development
	// manager, and what she recorded about it. It is on the schedule for the
	// reason the started runs are: a pass that woke a role and spent a turn doing
	// it is a pass that did something, and an operator reading what the session
	// did must not have to infer it from her conversation.
	Escalated []Escalated `json:"escalated,omitempty"`
	// EscalationProblem names a delivery that did not reach her. It costs the
	// pass nothing it was doing, so it is reported beside the pull rather than
	// stopping it — but never left unsaid, because stopped work nobody was told
	// about is exactly what the delivery exists to prevent.
	EscalationProblem string `json:"escalation_problem,omitempty"`
	// CarriedOut is the decisions of hers this pass fired. Each is a run as well,
	// and appears among the started runs above like any other: this is the account
	// of why that run exists, which the queue-level report cannot give because the
	// item was never in the queue.
	CarriedOut []CarriedOut `json:"carried_out,omitempty"`
	// CarryOutProblem names a decision the harness tried to fire and a gate
	// stopped. It costs the pass nothing it was doing, so it is reported beside the
	// pull rather than stopping it — and it is never left unsaid, because a decision
	// that quietly fails to fire is the exact condition this pass exists to end. The
	// same account is on the item's own triage record and on the docket entry the
	// development manager reads, which is where it survives the session.
	CarryOutProblem string `json:"carry_out_problem,omitempty"`
	// CarryOutReadProblem names a reading of the recorded decisions that failed,
	// which is a different fact from a gate refusing one and is kept apart from it
	// for that reason. A pass can read part of the record, fire what it could read,
	// and still have a decision nobody could read at all — and folding the two into
	// one line meant the successful attempt erased the account of the item nothing
	// ever looked at.
	CarryOutReadProblem string `json:"carry_out_read_problem,omitempty"`
	// CarryOutNoteProblem says a permanent refusal's tracker note is still
	// pending. It is independent of reading or attempting the refused action.
	CarryOutNoteProblem string `json:"carry_out_note_problem,omitempty"`
	// CarryOutUnattempted is the decisions of hers this pass found standing a poll
	// interval or more after they were recorded with no pass having attempted
	// them, each with why, and wrote onto the item. A decision the pass attempts
	// is fired or refused, and both say so; this is the ending that said nothing
	// until yoyodyne-ifd.428.39, and two re-runs sat in it for a week.
	CarryOutUnattempted []CarriedOut `json:"carry_out_unattempted,omitempty"`
	// ContinuationProblem names a run paused on work its item waits on that the
	// pass could not read or could not record continuing. It costs the pass
	// nothing it was doing, so it is reported beside the pull rather than stopping
	// it, and the paused run is read again at the next pull.
	ContinuationProblem string `json:"continuation_problem,omitempty"`
	// Fired is the recurring tasks this pass woke a role for, and what came back.
	// It is on the schedule for the reason the escalations are: a pass that woke a
	// role and spent turns doing it is a pass that did something, and an operator
	// reading what the session did must not have to infer it from a conversation.
	Fired []Fired `json:"fired,omitempty"`
	// RecurringProblem names a firing that failed or a schedule that could not be
	// read. Like the escalation's, it costs the pass nothing it was doing and is
	// reported beside the pull rather than stopping it.
	RecurringProblem string `json:"recurring_problem,omitempty"`
	// Corrected is the refused tracker blocks this pass woke a role to re-issue,
	// and what came back. It is on the schedule for the reason the firings are: a
	// pass that woke a role and spent a turn doing it is a pass that did something.
	Corrected []Corrected `json:"corrected,omitempty"`
	// CorrectionProblem names a wakeup that failed or a set of conversations that
	// could not be read. Like the two above, it costs the pass nothing it was doing
	// and is reported beside the pull rather than stopping it — but never left
	// unsaid, because a refusal nobody woke for is exactly the loss the wakeup
	// exists to prevent.
	CorrectionProblem string `json:"correction_problem,omitempty"`
	// ProviderAway counts the dispatches this pass made that the provider turned
	// away because nobody was logged into it or nobody could reach it, and
	// ProviderOutage is the outage as the last pull found it standing, nil once
	// the provider answered. Neither counts toward the brake: the item is exactly
	// as startable as it was, and a brake that tripped on it would prescribe
	// `yoyo release`, which lifts nothing here.
	ProviderAway   int                      `json:"provider_away,omitempty"`
	ProviderOutage *runstate.ProviderOutage `json:"provider_outage,omitempty"`
	// OutageProblem names an outage that could not be read or cleared. It costs
	// the pass its wait between pulls and nothing else, so it is reported beside
	// the pull rather than stopping it.
	OutageProblem string `json:"outage_problem,omitempty"`
	// DivergedTargets is every target branch the last pull found recorded as one
	// the harness will not catch up to the remote's, empty once the convergence
	// sweep has lifted them. While any stands the session chooses nothing.
	// DivergedTargetProblem names a record that could not be read, which the pull
	// read past as though none stood.
	DivergedTargets       []runstate.DivergedTarget `json:"diverged_targets,omitempty"`
	DivergedTargetProblem string                    `json:"diverged_target_problem,omitempty"`
	// LaunchSettingsHold is the hold on developers the last pull found standing,
	// nil once it is lifted or a pull was let through to check again.
	// LaunchSettingsProblem names a record that could not be read or lifted.
	LaunchSettingsHold    *runstate.LaunchSettingsHold `json:"launch_settings_hold,omitempty"`
	LaunchSettingsProblem string                       `json:"launch_settings_problem,omitempty"`
	// UsageWindowResetsAt is when the provider said the recorded window that held
	// this pass's last pull lifts, nil once no recorded window holds it.
	// UsageWindowProblem names a record that could not be read, which the pull
	// read past as though no window stood.
	UsageWindowResetsAt *time.Time `json:"usage_window_resets_at,omitempty"`
	UsageWindowProblem  string     `json:"usage_window_problem,omitempty"`
	// ReleasedClaims is the claims this pass audited against the runs the harness
	// has, found nothing alive behind, and gave back to the queue. It is on the
	// schedule for the reason the started runs are: a pass that freed work somebody
	// would otherwise have found stuck in the morning is a pass that did something.
	// It is named apart from Released below, which is the brake's own hold this
	// session lifted: two different things are given back, and one key for both
	// would have a reader of the JSON take a lifted hold for an unstuck item.
	//
	// ClaimProblem names the claims it found dead and could not give back. It costs
	// the pass nothing it was doing, so it is reported beside the pull rather than
	// stopping it — and never left unsaid, because an item nothing will ever pull
	// is exactly what the audit exists to stop being invisible.
	ReleasedClaims []runstate.ReleasedClaim `json:"released_claims,omitempty"`
	ClaimProblem   string                   `json:"claim_problem,omitempty"`
	// Landed is the conversation-carried items this pass found landed in the
	// repository and closed. It is on the schedule for the reason the released
	// claims are: a pass that closed work somebody would otherwise have closed by
	// hand some turns later is a pass that did something, and a reader must not
	// have to infer it from the item's own record.
	//
	// LandingProblem names a landing the pass found and could not close, or a
	// reading of the homes that failed. It costs the pass nothing it was doing,
	// so it is reported beside the pull rather than stopping it — and never left
	// unsaid, because an item whose work is done and which nothing will close is
	// exactly the ceremony this exists to end.
	Landed         []LandedConversation `json:"landed,omitempty"`
	LandingProblem string               `json:"landing_problem,omitempty"`
	// Braked is the intake hold this session's own failure-storm brake placed,
	// and BlockedInARow is what tripped it. What lifts it is on the hold's own
	// record: the development manager's decision, or a probe run that lands.
	Braked        *runstate.IntakeHold `json:"braked,omitempty"`
	BlockedInARow int                  `json:"blocked_in_a_row,omitempty"`
	// BrakeProblem names a brake that could not be applied or worked — no way to
	// place the hold, a hold that would not be written, a summons that did not
	// reach the development manager, a probe that could not be recorded. The
	// storm is still counted and still reported, because a brake that failed is
	// exactly the thing an operator must not find out about by inferring it from
	// the silence.
	BrakeProblem string `json:"brake_problem,omitempty"`
	// Released is the brake's own hold this session lifted, and why: the
	// development manager decided to, or the probe run landed. It is on the
	// schedule for the reason the brake is — a session that stopped the line and
	// started it again did both, and a reader must not have to infer the second
	// from the runs that followed.
	Released []BrakeRelease `json:"released,omitempty"`
	// BrakeEscalated is the brake's hold this session handed to the operator
	// itself, at the cycle bound, with the cycles it spent and what stopped the
	// last probe. It is on the schedule for the reason a release is: a session
	// that stopped summoning anybody over a hold did something, and a reader must
	// not have to infer it from the summonses that stopped.
	BrakeEscalated *runstate.BrakeEscalation `json:"brake_escalated,omitempty"`
	// SpentUSD is what this pass spent, as the provider reported it: the runs it
	// started, and the turns it took itself putting stopped work in front of the
	// development manager. Budget is what it was allowed. Both are absent from a
	// pass nobody bounded and nothing priced.
	SpentUSD float64 `json:"spent_usd,omitempty"`
	Budget   float64 `json:"budget,omitempty"`
	// SpendProblem names run evidence that could not be priced. On an unbounded
	// pass it is a note beside a total that is a floor rather than an exact
	// number; on a bounded one it is why the session stopped, because a budget
	// measured against evidence nobody can read is no budget at all.
	SpendProblem string `json:"spend_problem,omitempty"`
	// RedeployProblem names a reading of the session's own binary that failed, so
	// whether a build has been deployed over it is a question nobody answered. It
	// costs the session its self-redeployment and nothing else, so it is reported
	// beside the pass rather than stopping it: a session that stopped choosing
	// work because it could not stat a file would be a worse failure than the
	// staleness it is guarding against.
	RedeployProblem string `json:"redeploy_problem,omitempty"`
	// Drain is the account of the session having found a build deployed over it
	// and waited out the runs it hosted: since when, for how long at most, and
	// what became of the runs still going when that bound ran out. It is absent
	// from a session nothing was deployed over.
	Drain *ScheduleDrain `json:"drain,omitempty"`
	// SessionProblem names a transition that could not be recorded. It costs the
	// session its visibility rather than its work, so it is reported beside the
	// pass rather than failing it — the alternative is a session that stops
	// working because nobody could be told it was working.
	SessionProblem string `json:"session_problem,omitempty"`
	// ReadsRetried counts the readings of the harness that failed and were made
	// again rather than stopping the session, and ReadProblem names the last of
	// them. Both are for the reader afterwards: a reading that succeeded on the
	// second attempt leaves nothing at all behind, so a session that rode out a
	// store outage overnight would otherwise be a session nobody could tell had
	// met one.
	ReadsRetried int    `json:"reads_retried,omitempty"`
	ReadProblem  string `json:"read_problem,omitempty"`
	// ReadFailure is the reading the session finally stopped on. A watch also
	// records how long the harness had gone on being unreadable when it did.
	//
	// It is a field of its own rather than the last value of ReadProblem because
	// the two are facts about different moments, and only this one is why the pass
	// ended. A pass stops as unreadable for a second reason — a pull that
	// assembles and is unusable — and a session that had ridden through a
	// contended reading an hour earlier would otherwise report that reading as
	// what stopped it, in the log this exists to make worth trusting.
	ReadFailure string `json:"read_failure,omitempty"`
}

// ScheduleDrain is what a session did about a build deployed over it. It is on
// the schedule for the reason the started runs are: a session that stopped two
// runs on its own clock did something to them, and an operator reading what the
// session did must not have to infer it from the runs' own records.
type ScheduleDrain struct {
	// Since is when the session found the deploy and stopped waiting on nothing
	// but the runs it hosted, and Bound is how long it was willing to wait.
	Since time.Time     `json:"since"`
	Bound time.Duration `json:"bound"`
	// BoundReached reports the wait having run out with runs still going, and
	// Stopped names the work items whose runs were stopped and preserved for the
	// session that comes back. A session whose runs all ended inside the bound
	// reports neither.
	BoundReached bool     `json:"bound_reached,omitempty"`
	Stopped      []string `json:"stopped,omitempty"`
	// Landings names the work items whose runs had already landed and were
	// running their landing checks when the bound ran out. Those checks were
	// stopped, so each landing is recorded as unverified; nothing is left for
	// the session that comes back.
	Landings []string `json:"landings,omitempty"`
	// ChecksWaited is retained for records from sessions that waited out running
	// check stages past the drain bound. New sessions stop those stages at the
	// bound and leave this list empty.
	ChecksWaited []string `json:"checks_waited,omitempty"`
	// Unreported names the work items whose runs the bound stopped and which had
	// not reported back within stoppedRunGrace when the session restarted. The
	// restart does not wait on them: a stopped run has no process left to wait
	// for, and the session that comes back reads each run's own record.
	Unreported []string `json:"unreported,omitempty"`
	// Skipped counts the pulls the session declined to make into a free seat
	// because the bound was closer than one poll interval away, and Problem
	// names a reading of the hosted runs that failed while the bound was being
	// applied. Both are said because a pull the session did not make is
	// otherwise indistinguishable from one it found nothing for.
	Skipped int    `json:"skipped,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// Chosen is how many runs this pass chose and began, which is what a `--limit`
// counts. A run re-adopted from the session before this one was chosen by that
// session and counted there; counting it again here would spend the operator's
// bound on a continuation every deploy hands over.
func (s Schedule) Chosen() int {
	chosen := 0
	for _, started := range s.Started {
		if started.Readopted == "" {
			chosen++
		}
	}
	return chosen
}

// Schedule pulls ready work and runs it, up to the capacity the configuration
// allows. A drain returns once every run it started has ended; a watch stays
// open, waiting out the configured interval whenever it finds nothing to start,
// and returns when the operator stops it or a bound it was given is reached.
//
// It returns an error only for something that stopped it pulling. A run that
// failed is on the schedule as a failed run, because one item failing is not a
// reason for the pass to stop choosing others — that decision belongs to the
// operator holding intake, or to the development manager replanning the item.
func (s Scheduler) Schedule(ctx context.Context) (Schedule, error) {
	if s.Open == nil {
		return Schedule{}, errors.New("a scheduler requires a way to open a pull")
	}
	if s.Limit < 0 {
		return Schedule{}, fmt.Errorf("a scheduler limit of %d starts nothing", s.Limit)
	}
	if s.Budget < 0 {
		return Schedule{}, fmt.Errorf("a session budget of %.2f spends nothing", s.Budget)
	}

	schedule := Schedule{Watched: s.Watching, Budget: s.Budget}
	session := s.session(&schedule)
	// completions carries each started run back to this goroutine, which is the
	// only one that touches the schedule. The runs themselves never share
	// anything: each has its own worktree, its own reservation, and its own
	// pipeline built from the configuration its pull read.
	completions := make(chan completed)
	// deliver is how a run's goroutine hands its ending to completions. A session
	// restarting past its drain bound stops waiting for runs it stopped that have
	// not reported back, and a delivery made after that is dropped rather than
	// left blocked on a channel nobody reads any more.
	abandoned := make(chan struct{})
	defer close(abandoned)
	deliver := func(done completed) {
		select {
		case completions <- done:
		case <-abandoned:
		}
	}
	// mine is the items this pass has started and not yet collected, by work
	// item. It exists because a run does not appear in the durable state until it
	// reserves, which is several steps after it is started, and a pull that
	// counted only the recorded runs would start the same slot twice.
	mine := make(map[string]int)
	// elsewhere is the runs another process had in flight at the last pull, by run
	// identifier, and ended is the runs of this session collected since then. The
	// next pull reads both to say which slots freed and whose runs had held them:
	// a run of another process leaves no completion here, so the only way this
	// session learns one ended is that a pull no longer finds it in flight.
	elsewhere := map[string]string{}
	var ended []freedSlot
	// cadence is what this pass knows about why a recurring task might not have
	// fired when it fell due; see recurringWatch.
	cadence := recurringWatch{opened: s.now(), missed: map[string]time.Time{}, waiting: map[string]recurringHold{}}
	// firings is the recurring firings this session has in flight, each taking
	// its turns in a goroutine of its own and holding its own conversation and
	// nothing else; see recurringfirings.go. They are collected at the top of
	// every pull and waited out when the session ends, like its runs.
	firings := newRecurringFirings(session.passingBeside(ctx))
	defer firings.cancel()
	// tried is every item this pass has already started, against the item as it
	// read at the time and what became of the start. A drain never looks at that
	// reading: nothing is ever removed, because a run that ends without moving the
	// item out of the ready queue would otherwise be chosen again on the next pull.
	//
	// A watch cannot afford that rule in either direction. Keeping an item out
	// for the life of a session that never ends is a queue the session can never
	// get back to; letting it back in unconditionally is the failure that
	// provoked this guard — an item whose run fails before it starts leaves the
	// item exactly as ready as it was, and a loop with no memory would re-pull it
	// every interval until somebody noticed. So a watch remembers what the item
	// looked like and tries it again when that changes, which is the same thing a
	// person means by "nothing has changed, don't try again".
	//
	// What each exclusion is for travels with it, because the exclusion outlives
	// every other account of the start that made it. A run this session started and
	// finished has a record anybody can read; a start that failed before a run was
	// reserved has none at all, so an item excluded by one is an item nothing
	// anywhere accounts for. That is the shape that idled a queue of seventy-four
	// on 2026-09-13, and it is why the reason is carried rather than derived by
	// whoever asks later.
	tried := make(map[string]attempt)
	// deferred is the items already named on the schedule as passed over — paused
	// by a directive, or covered by children carrying their execution — against
	// where on the schedule each is named. It bounds the report rather than the
	// choosing: both are re-read at every pull, so an item stops being deferred
	// the moment somebody resolves what paused it or closes what covered it.
	deferred := make(map[string]int)
	// passOver names an item on the schedule as passed over, once: an item held
	// across a hundred polls is one line in the report. A later pull that passes
	// the same item over for a later reason rewrites that line rather than leaving
	// the first, because the report is rendered when the session ends and the
	// reason it carries has to be the last pull's. On 2026-09-18 a report rendered
	// with each sibling's first reason named a run that had failed two days
	// earlier as what they were all still waiting on, and was read as the guard
	// holding a developer slot on a dead run.
	passOver := func(workItemID, reason string) {
		if index, named := deferred[workItemID]; named {
			schedule.Deferred[index].Reason = reason
			return
		}
		deferred[workItemID] = len(schedule.Deferred)
		schedule.Deferred = append(schedule.Deferred, Deferred{WorkItemID: workItemID, Reason: reason})
	}
	// sequencedEarlier is the items this pass passed over because starting them
	// would have raced work already in flight, against the conflict that held
	// each one. It
	// decides nothing — every pull re-reads what is actually in flight — and is
	// remembered so that the selection which eventually pulls one can say it
	// waited, which is the only place that fact survives the pass.
	sequencedEarlier := make(map[string]conflict)
	// waitingOn is the decisions this session has attempted and a gate shut for
	// everything at once stopped, against the gate that stopped each. The record
	// does not pace those refusals, so it offers the same decision on every pull
	// the gate stands; this is what keeps the pass from attempting every offer.
	// It is read against the switches the pull can see, and a decision is dropped
	// from it by any attempt that ended some other way.
	waitingOn := make(map[string]string)
	// blockedInARow counts the runs that ended blocked with nothing landing
	// between them. It is the storm the brake watches for, and it is reset by any
	// run that finishes: one item failing is not a systemic failure, and the
	// per-item guard above is what that case is for. storm is those same runs
	// by name, with the reason each blocked, because a brake that trips puts
	// them in front of the development manager rather than telling her a number.
	blockedInARow := 0
	var storm []runstate.BrakeBlockedRun
	// spend is how the runs this session started are priced, taken from the last
	// pull that could be opened. It is held outside the loop because a run
	// collected after the final pull still cost what it cost.
	var spend ScheduleSpend
	var environment ScheduleEnvironment
	refused := ""
	// A refusal cools only until the next poll, never until the item changes.
	pollPassed := func() {
		forget(tried)
		refused = ""
	}
	// docket is where a dispatch that never became a run is recorded, taken from
	// the same pull and held outside the loop for the same reason: a start that
	// fails after the final pull failed just as much, and the record of it is the
	// only thing that will ever say so.
	var docket ScheduleTriage
	// brake is the hold the brake works, and summons how it reaches the
	// development manager, both taken from the same pull and held outside the
	// loop for the same reason again: the probe run is collected wherever it
	// ends, and what it decides about the hold has to be written when it does.
	var brake ScheduleBrake
	var summons ScheduleSummons
	var cooldown time.Duration
	var cycleBound int
	// drain is the session having found a build deployed over the one it is
	// executing. From that point it restarts the moment it hosts nothing, and
	// until then carries on — polling, pulling into free seats, firing its
	// recurring tasks — for at most the bound the configuration puts on the wait.
	// Past the bound the runs it hosts are stopped where they are and preserved
	// for the session that comes back. Draining is about the runs the session
	// hosts and not about the scheduler's other duties: the two-hour silence of
	// 2026-09-19 was a session that had stopped everything to wait on one run's
	// checks under load, with the second seat empty and two recurring passes
	// missed.
	var drain redeployDrain
	// hosted is how each run this pass started is stopped for the drain bound, by
	// its index on the schedule. Each run has a context of its own rather than
	// sharing one so that a run at its promotion can be left to finish while the
	// ones at their checks are stopped.
	hosted := make(map[int]context.CancelCauseFunc)
	// landings is which of those runs are over and in their landing checks, as
	// each run's pipeline says so on the context it is hosted under. A run in its
	// landing has no in-flight record to read a phase off, and is told apart
	// from one still before its claim by this alone.
	landings := &hostedLandings{}
	// boundSaid is the bound running out having been said in the watch log,
	// which is done once however many looks at the top of the loop apply it —
	// except while the restart waits on a promotion, which is said again; see
	// promotionResayEvery. boundSaidAt is when it was last said, and
	// promotingSaid how many promotions that line was waiting on.
	boundSaid := false
	var boundSaidAt time.Time
	promotingSaid := 0
	// runs is the durable run state as the last pull read it, held outside the
	// loop because the drain bound is applied at the top of the loop, before a
	// pull is opened, and applying it means reading which phase each hosted run
	// is at.
	var runs ScheduleRuns
	// readopted is every run this session has tried to pick up from a session
	// before it, by run id, against its entry on the schedule. The entry is kept
	// so a re-adoption made again is one line reporting its latest attempt rather
	// than a line per poll: what decides whether it is made again is the run's
	// own record, which carries the stop until a pipeline consumes it.
	readopted := make(map[string]int)
	// retries is the run of harness readings that have failed with none
	// succeeding between them. See readRetries: it is what lets a watch ride
	// through the store contention a reconcile or a settling run makes, and what
	// stops the session once the failures have gone on too long to be contention.
	var retries readRetries
	// window is the provider's usage window this session is inside, learned from a
	// run that came back parked on one. It is held across pulls because that is how
	// long it lasts: the run that met the limit is one invocation, and every other
	// one the session would make meets the same refusal until the window rolls.
	//
	// It is what the session had no way to say before. The refusal was durable in
	// the run's own state and nowhere a surface reading the product would look, so
	// a session waiting out ninety minutes of it on 2026-09-05 was indistinguishable
	// from one that had died — and the watchdog said exactly that.
	var window providerWindow
	running := 0
	// continuing is the started entries that are continuations of a run paused on
	// work its item waited on, against that run, and refusedContinuations is the
	// continuations this session attempted that did not get the run going, so a
	// pull leaves each for continuationRetry rather than attempting it every poll.
	continuing := map[int]string{}
	refusedContinuations := map[string]refusedContinuation{}

	// settle takes one finished run into the schedule: what became of it, what it
	// cost, and what it does to the storm the brake is counting.
	settle := func(done completed) {
		started := &schedule.Started[done.index]
		if runID, continuation := continuing[done.index]; continuation {
			delete(continuing, done.index)
			record := *started
			record.record(done)
			if why, refused := continuationRefused(done, record); refused {
				refusedContinuations[runID] = refusedContinuation{at: s.now(), why: why}
			} else {
				delete(refusedContinuations, runID)
			}
		}
		// A carry-out of the development manager's decision accounts for itself
		// before anything else is decided about it, because whether it is a run at
		// all is what it answers. One that fired is a run like any other from here
		// on; one a gate stopped started nothing, so it is neither priced, nor
		// counted toward the failure storm, nor reported as a run that failed.
		if carried := done.carriedOut; carried != nil {
			if !carried.Carried && carried.Waiting {
				waitingOn[carried.WorkItemID] = carried.Gate
			} else {
				delete(waitingOn, carried.WorkItemID)
			}
			if !s.settleCarryOut(&schedule, started, *carried) {
				return
			}
		}
		started.record(done)
		refusal, byTheMachine := "", false
		if started.Declined == "" {
			refusal, byTheMachine = refusedByTheMachine(ctx, environment, done)
		}
		if byTheMachine {
			started.environmental = true
			refused, schedule.Blocked = refusal, refusal
		}
		// The run's own context is released with it, whether or not the drain
		// bound cancelled it first.
		if cancel, live := hosted[done.index]; live {
			cancel(nil)
			delete(hosted, done.index)
		}
		// What became of the start is written into the exclusion it made, before
		// anything else is decided about it. A start this session made is the only
		// thing keeping the item out of the pulls that follow, so an exclusion that
		// could not say why is the whole of what a reader gets.
		excluded, held := tried[started.WorkItemID]
		if held {
			excluded.reason = excludedBecause(*started)
			// A run the provider's usage window stopped holds its item until the
			// window resets and no longer. That is a wait with an end rather than a
			// memory of having tried: the item is pulled again the moment the reset
			// passes, and a run started before then is refused the same way.
			excluded.until = time.Time{}
			if resetsAt, windowed := usageWindowReset(started.Outcome); windowed {
				excluded.until = resetsAt
			}
		}
		// And the one ending that leaves no record anywhere else. A dispatch that
		// failed before a run was reserved wrote nothing to the run store, so every
		// surface downstream of that store reads it as though the item was never
		// tried — which is what it looked like on 2026-09-13, four hours into a
		// capacity window, over a queue of seventy-four.
		// A dispatch the provider turned away — nobody logged in, nobody able to
		// reach it — is the one ending that leaves everything as it was. Nothing is
		// docketed, because the item has no stoppage; nothing is excluded, because
		// the item is exactly as startable as it was and is started when the
		// provider answers; and nothing is counted toward the brake below, because
		// the brake's remedy lifts nothing here. What is recorded is the wait, so
		// the next pull reads it before dispatching into the same refusal.
		if started.Declined != "" || byTheMachine {
			// No work was attempted. Keep only the backoff, with no item fingerprint
			// and no docket entry, so the next poll can retry the same work.
			excluded = attempt{title: excluded.title, reason: excluded.reason, retryAtPoll: true}
		} else if unstartedAttempt(*started) && started.providerAway() {
			schedule.ProviderAway++
			delete(tried, started.WorkItemID)
			held = false
		} else if unstartedAttempt(*started) && started.Readopted == "" {
			// A re-adoption the pipeline never took is not a dispatch that never
			// became a run: the run exists, its record says so, and the next pull
			// tries it again.
			recorded, problem := recordAttempt(docket, *started, excluded, s.Watching)
			excluded.reason = recorded
			if problem != "" && schedule.AttemptProblem == "" {
				schedule.AttemptProblem = problem
			}
		}
		if held {
			tried[started.WorkItemID] = excluded
		}
		// What the provider said, taken from the run that was told it. A later
		// refusal replaces an earlier one rather than being merged with it: the
		// deadline a run was just given is the provider's current answer, and an
		// older one is a window that has already been superseded.
		if met := windowFrom(started.Outcome); met.waiting {
			window = met
		}
		switch {
		case started.Declined != "":
			// The work went to another process, which is two schedulers doing
			// exactly what they should. It says nothing about the machine and
			// nothing about the item, so it neither counts nor clears.
		case started.providerAway():
			// The provider turned the dispatch away. It says nothing about the
			// item and nothing about the machine that a run could fix, so it
			// neither counts nor clears: three of these in a row on 2026-09-17 are
			// what tripped the brake over a login.
		case started.environmental:
			// The environment stopped the run — a dirty checkout, a transport that
			// did not answer, a sandbox that would not spawn, a target branch the
			// harness will not catch up onto the remote's — which is a verdict on
			// nothing. It neither counts nor clears, for the reason the provider
			// turning a dispatch away does not: two of the three stops that tripped
			// the brake on 2026-09-19 were of this class, all three on 2026-09-21
			// were the one diverged target, and a brake tripped on them summons a
			// decision about a change nobody judged.
		case started.Readopted != "" && unstartedAttempt(*started):
			// A re-adoption that never reached the run says nothing about the
			// machine either, and is tried again at the next pull.
		case started.blockedRun():
			blockedInARow++
			if blockedInARow > schedule.BlockedInARow {
				schedule.BlockedInARow = blockedInARow
			}
			storm = append(storm, started.blockedEntry())
		case started.Outcome.Paused:
			// A parked run is owed a continuation rather than having failed.
		default:
			blockedInARow = 0
			storm = nil
		}
		if cost, problem := priceRun(spend, started.Outcome); problem != "" {
			schedule.SpendProblem = problem
		} else {
			schedule.SpentUSD += cost
		}
		// What became of the probe decides the hold, and it is decided here
		// because here is where the probe's ending is in hand: a landing releases
		// the hold, a blocking keeps it and puts the question to the development
		// manager again, and anything else leaves the cooldown to decide.
		if started.Probe {
			s.settleProbe(ctx, &schedule, firings, brake, summons, cooldown, cycleBound, *started)
		}
	}

	// finish takes one run of this session's off the books once it has ended.
	// Every wait that can collect a run collects it through here, so none of them
	// can take a run back differently from the others.
	finish := func(done completed) {
		running--
		delete(mine, schedule.Started[done.index].WorkItemID)
		settle(done)
		// Only a start that became a run held a slot anybody could see; one that
		// was declined before it reserved anything frees nothing worth naming.
		if started := schedule.Started[done.index]; started.Outcome.RunID != "" {
			ended = append(ended, freedSlot{runID: started.Outcome.RunID, workItemID: started.WorkItemID, ours: true})
		}
	}

	// poll is the interval the last pull read, held outside the loop because
	// collect below wakes on it before a pull is opened.
	var poll time.Duration
	// collect takes one finished run, or one poll interval passing with none
	// finished. It reports false when the context ended first, which stops the
	// pulling; the runs still in flight are waited out below either way.
	//
	// The interval is why a session hosting a run is not a session doing nothing
	// else. Until 2026-09-19 this waited on the run alone, so a session whose
	// seats were full, or whose queue had nothing more to start beside the run
	// it hosted, fired no recurring task and noticed no deploy until that run
	// ended — which for a race suite under load was two hours. Waking once a
	// poll costs what an idle poll costs, and it is what lets the loop's top
	// look at the deploy and the cadences again.
	collect := func(wake time.Duration) bool {
		var woken <-chan time.Time
		if wake > 0 {
			timer := time.NewTimer(wake)
			defer timer.Stop()
			woken = timer.C
		}
		select {
		case done := <-completions:
			finish(done)
			return true
		case <-woken:
			pollPassed()
			return ctx.Err() == nil
		case <-drain.due:
			// The drain bound ran out while the session was waiting on a run. The
			// wait is over: the loop applies the bound at its top, and a session
			// that went on waiting here would be the unbounded drain this closes.
			drain.reached()
			return true
		case <-ctx.Done():
			return false
		}
	}

	// recheckSoon marks a drain bound applied with a hosted run still before its
	// claim, which the next look at the top of the loop stops. The wait below
	// comes back sooner than a poll while it is set.
	recheckSoon := false
	// wake is how long a wait on a hosted run lasts before the loop looks again.
	wake := func() time.Duration {
		if recheckSoon {
			return min(poll, drainRecheck)
		}
		return poll
	}

	// collectUntilDue is collect bounded by the recurring schedule as well as by
	// the poll: it takes one finished run, or returns when the next recurring
	// task falls due or the poll interval passes, whichever comes first, so the
	// pass goes back round to fire it. A run is not a bound on a cadence. On
	// 2026-09-13 this session's wait was on the run alone, one run of its own
	// took twenty hours, and the development manager's hourly task fired nothing
	// in any of them — the session was live throughout and the cadence resumed
	// seven seconds after the run ended.
	//
	// A pull whose schedule cannot say when it is next due is waited on exactly
	// as collect waits.
	collectUntilDue := func(pull Pull) bool {
		due, bounded := s.nextFiring(ctx, pull)
		if !bounded {
			return collect(wake())
		}
		var woken <-chan time.Time
		if w := wake(); w > 0 {
			timer := time.NewTimer(w)
			defer timer.Stop()
			woken = timer.C
		}
		waiting, stopWaiting := context.WithCancel(ctx)
		defer stopWaiting()
		interval := make(chan bool, 1)
		go func() {
			interval <- s.sleep(waiting, due)
		}()
		// The wait is stopped and then waited for whichever way it ends early, so
		// nothing this pass started is still sleeping once it has moved on. What it
		// answers then is not read: it was stopped here, and a stop this pass made
		// is not the operator's.
		stop := func() {
			stopWaiting()
			<-interval
		}
		select {
		case done := <-completions:
			stop()
			finish(done)
			return true
		case <-woken:
			stop()
			pollPassed()
			return ctx.Err() == nil
		case <-drain.due:
			// The drain bound ran out while the session was waiting on a run; see
			// collect.
			stop()
			drain.reached()
			return true
		case slept := <-interval:
			if slept {
				pollPassed()
			}
			return slept && ctx.Err() == nil
		case <-ctx.Done():
			return false
		}
	}

	// wait is what a watch does instead of concluding the queue is empty: it
	// collects a run of its own if one is still going, because a run finishing
	// changes the answer sooner than any interval would, and otherwise sleeps out
	// the interval this pull read. It reports false when the context ended, which
	// is the operator stopping the session.
	wait := func(pull Pull, state runstate.WatchState, said account) bool {
		session.enter(state, said)
		if running > 0 {
			return collectUntilDue(pull)
		}
		schedule.Polls++
		if !s.sleep(ctx, pull.Poll) {
			return false
		}
		pollPassed()
		return true
	}

	// awaitRunOrPoll waits for whichever comes first of a run of this session
	// ending and the poll interval this pull read, and reports false when the
	// context ended. It is what a watch waits on whenever something other than its
	// own runs can change the answer: a slot it could not fill, or a slot another
	// process holds, which that process's run ending frees without this session
	// hearing of it until it reads the runs in flight again.
	awaitRunOrPoll := func(pull Pull) bool {
		select {
		case done := <-completions:
			finish(done)
			return true
		case <-s.interval(pull.Poll):
			pollPassed()
			schedule.Polls++
			return true
		case <-ctx.Done():
			return false
		}
	}

	// refill is the wait of a watching session with a developer slot free that
	// this poll could not fill. With no run of its own going it is wait. With one
	// going it is whichever comes first of that run ending and the poll interval
	// this pull read: a run ending still wakes the pass at once, because it frees
	// a slot and may close what something else was waiting on, and the interval
	// wakes it whether or not anything has ended. It reports false when the
	// context ended, which is the operator stopping the session.
	//
	// wait above collects a completion and nothing else whenever a run of this
	// session is going, and that is the wait that idled two of three slots for
	// over an hour on 2026-09-26: two runs ended early, the pulls they woke found
	// nothing they could start just then, and the session went back to waiting on
	// the third run's completion while the queue behind it stood ready. Nothing
	// about a slot that is free waits on a run that is not in it, so a free slot
	// is refilled at the next poll, whatever else is still in flight. A session
	// whose slots are all taken by its own runs still waits on a completion,
	// because a run ending is then the only thing that can change the answer; one
	// whose slots are all taken with another process holding any of them waits on
	// the interval as well, for the same reason a free slot does.
	refill := func(pull Pull, said account) bool {
		if running == 0 {
			return wait(pull, runstate.WatchIdle, said)
		}
		session.enter(runstate.WatchIdle, said)
		return awaitRunOrPoll(pull)
	}

	// awaitProvider waits out one interval of the provider answering nobody. It
	// differs from wait in one way: a run of this session ending mid-wait is
	// collected and the interval is waited out regardless, rather than the run
	// ending being what wakes the pass. The runs in flight are each waiting on
	// the same provider, on their own probes, and a login renewed while they wait
	// has to be noticed at the next interval rather than when the last of them
	// finishes.
	awaitProvider := func(pull Pull, said account) bool {
		session.enter(runstate.WatchIdle, said)
		schedule.Polls++
		if running == 0 {
			return s.sleep(ctx, pull.Poll)
		}
		interval := make(chan struct{})
		go func() {
			defer close(interval)
			s.sleep(ctx, pull.Poll)
		}()
		for {
			select {
			case done := <-completions:
				finish(done)
			case <-drain.due:
				// The drain bound ran out while the provider was away. The runs in
				// flight are each asleep on the same provider, and the bound stops
				// them exactly as it stops a run at its checks.
				drain.reached()
				return ctx.Err() == nil
			case <-interval:
				return ctx.Err() == nil
			case <-ctx.Done():
				return false
			}
		}
	}

	var failure error
	// unreadable takes a reading of the harness that failed and says whether the
	// pass carries on. It sets what stopped the pass, and the failure it stopped
	// with, wherever it does not — so a caller handed false has only to break.
	//
	// A drain stops on the first one, exactly as it always has: it is a command
	// somebody is waiting on the return of, and one that slept through a store
	// outage would be one that hung. A watch waits and reads again, and stops only
	// once the readings have gone on failing past the window. See the retry
	// constants for what that is worth and what it cost to find out.
	unreadable := func(err error) bool {
		if !s.Watching {
			schedule.Stopped = ScheduleUnreadable
			schedule.ReadFailure = err.Error()
			failure = err
			return false
		}
		retries.failed = true
		now := s.now()
		if retries.since.IsZero() {
			retries.since = now
			retries.delay = firstReadRetryDelay
		}
		retries.attempts++
		cadence.hold(recurringHold{why: fmt.Sprintf("the watch session could not read the harness and was reading it again rather than firing anything: %v", err), at: now})
		if tried := now.Sub(retries.since); tried >= readRetryWindow {
			schedule.Stopped = ScheduleUnreadable
			schedule.ReadFailure = fmt.Sprintf(
				"the harness went on being unreadable for %s across %d reading(s), which is longer than a store somebody else is writing to takes to come back, so the session stopped rather than polling something it cannot read: %v",
				tried.Round(time.Second), retries.attempts, err)
			failure = errors.New(schedule.ReadFailure)
			return false
		}
		schedule.ReadsRetried++
		schedule.ReadProblem = fmt.Sprintf(
			"reading %d, failing for %s, read again in %s: %v",
			retries.attempts, now.Sub(retries.since).Round(time.Second), retries.delay, err)
		// What the session says about itself leaves the changing numbers of the
		// outage out — which attempt this is, how long it has been failing — so an
		// outage is one line in the log rather than one per attempt. What they are
		// is on the schedule, which is where a reader afterwards looks.
		//
		// The runs it already has going are not one of those numbers. They are the
		// difference between a line that has stopped and a line that is working
		// while one reading fails, and they are named for the same reason the idle
		// account below names them.
		outage := fmt.Sprintf(
			"the harness could not be read and is being read again for up to %s before the session gives up on it: %v",
			readRetryWindow, err)
		if running > 0 {
			outage = fmt.Sprintf("%s in flight; %s", plural(running, "run", "runs"), outage)
		}
		session.enter(runstate.WatchIdle, account{reason: outage, running: running, unreadable: true})
		if !s.sleep(ctx, retries.delay) {
			schedule.Stopped = ScheduleCancelled
			return false
		}
		retries.delay = min(retries.delay*2, longestReadRetryDelay)
		return true
	}

	session.enter(runstate.WatchWatching, account{reason: s.opening()})
pulling:
	for {
		// A pull that read everything it needed clears the failures behind it. What
		// the window measures is a store that has gone on being unreadable, rather
		// than a count of the contended readings one session met over a long life.
		if !retries.failed {
			retries = readRetries{}
		}
		retries.failed = false
		// Firings whose turns ended since the last pull are taken into the schedule
		// here, before anything reads what the session has spent or whether it
		// still hosts anything.
		s.collectFirings(&schedule, firings, &cadence, false)
		if s.Limit > 0 && schedule.Chosen() >= s.Limit {
			schedule.Stopped = ScheduleLimitReached
			break
		}
		if s.Budget > 0 && schedule.SpentUSD >= s.Budget {
			schedule.Stopped = ScheduleBudgetSpent
			break
		}
		// A bounded session that has lost the ability to measure itself stops
		// here rather than at the end. Carrying on would be spending against a
		// number nothing is comparing anything to, and the operator would find
		// out in the morning — from a field on a schedule this session does not
		// return until it is over.
		if s.Budget > 0 && schedule.SpendProblem != "" {
			schedule.Stopped = ScheduleSpendUnreadable
			break
		}
		if ctx.Err() != nil {
			schedule.Stopped = ScheduleCancelled
			break
		}
		// Whether anything is happening at all, asked before anything is chosen
		// and answered from records rather than from this pass's own account of
		// itself. A session that has stopped choosing would report nothing about
		// that however carefully this loop described itself, which is the whole
		// reason the reading is of the durable records instead.
		if s.Watchdog != nil {
			s.Watchdog(ctx)
		}
		// Whether a build has been deployed over this one, asked before anything
		// is chosen and before the configuration is even read. It is asked here
		// rather than after a run settles because those are the same moment seen
		// from either side — this is the top of the loop a settled run returns to,
		// and it is the last point before the next item is claimed.
		if !drain.active && s.Watching && s.Deployment != nil {
			replaced, err := s.Deployment.Replaced()
			switch {
			case err != nil:
				if schedule.RedeployProblem == "" {
					schedule.RedeployProblem = fmt.Sprintf("whether a build has been deployed over this session could not be read, so it goes on running what it was started with: %v", err)
				}
			case replaced:
				drain.begin(s.now())
				schedule.Drain = &ScheduleDrain{Since: drain.since, Bound: drain.limit}
				session.draining(drain.record(running))
				session.note(drain.found(running), running)
			}
		}
		if drain.active {
			// The restart is made the moment the session hosts nothing: the window
			// an external restart could never find is one this makes rather than
			// waits for, and nothing is pulled into it first. A firing still taking
			// its turns is hosted too — a restart under it would cut its pass off
			// part-way — so it is waited out, and past the bound it is stopped, which
			// records it as a missed pass the next session owes.
			if running == 0 && firings.idle() {
				schedule.Stopped = ScheduleRedeployed
				break
			}
			if running == 0 && drain.expired(s.now()) {
				firings.cancel()
			}
			// Past the bound the session hosts nothing more. The runs still going
			// are stopped where they are — each at a phase the session that comes
			// back can continue from — and preserved. A run at its promotion is one
			// exception: it holds the target branch's lease, and stopping it
			// would leave a promotion only the repository could say the outcome of,
			// so it is waited out. A check stage still running is stopped at the
			// drain bound, however far load has scaled the stage's own bound. A
			// stage that has just ended gets a brief grace to record its verdict
			// — see stopHosted. Either wait is the session's ordinary loop,
			// not a silence: the pull below is still opened, its recurring tasks
			// still fire on their cadence, and only new starts are declined. A
			// forge outage can hold a promotion for hours, and a session that
			// stopped everything to wait on it would be the 07:35Z silence again
			// with a different run at the bottom of it.
			if drain.expired(s.now()) {
				// Marked as reached before it is said, so the lines that say it
				// carry the mark `yoyo status` names the restart from — whichever
				// of the timer and the clock found the bound out.
				drain.reached()
				// A run still before its claim has no phase to read and nothing yet
				// to preserve, so it is left to reach one and stopped at the next
				// look rather than cancelled into a dispatch that never became a
				// run — and the look comes sooner than a poll. The look comes
				// before the line is said, so it names the runs stopped for the
				// restart.
				recheckSoon = s.stopHosted(&schedule, &drain, hosted, landings, mine, runs) > 0
				reached := drain.record(running)
				session.draining(reached)
				// Said once, as it happens, in whatever state the session is in:
				// the runs it stops go on holding their seats in flight, so no
				// pull that follows need write a line of its own, and `yoyo status`
				// would otherwise go on reading a drain inside its bound.
				//
				// A wait on a promotion is said again at intervals, and when what it
				// waits on changes, so a reader can tell a session waiting out a
				// long promotion from one that died while it waited; see
				// promotionResayEvery.
				resay := reached.Promoting > 0 &&
					(reached.Promoting != promotingSaid || !s.now().Before(boundSaidAt.Add(promotionResayEvery)))
				if !boundSaid || resay {
					boundSaid, boundSaidAt, promotingSaid = true, s.now(), reached.Promoting
					session.note("the watch session is "+reached.Says(), running)
				}
			}
			// Past the bound, a run the session has stopped is not one it hosts:
			// its process is gone and what is left is a record, so the restart
			// follows in this same step once nothing it hosts is still live. It is
			// not left to a later look. On 2026-10-05 three runs stopped at the
			// bound had ended within a second, but this loop went straight on into
			// two recurring passes before it read their endings, and the session
			// went on pulling nothing for forty minutes until it was restarted by
			// hand. The stopped runs are collected below, after the loop.
			if drain.boundReached && liveHosted(mine, hosted) == 0 {
				schedule.Stopped = ScheduleRedeployed
				break
			}
		}
		pull, err := s.Open(ctx)
		if err != nil {
			if !unreadable(fmt.Errorf("open a pull: %w", err)) {
				break
			}
			continue
		}
		// A pull that was assembled and is not usable is a decision somebody made
		// about the configuration rather than a reading that failed, so it stops the
		// pass at once. Waiting out a window for a capacity of zero to become
		// something else is a session that hangs, not one that rides out contention.
		if err := pull.validate(pullNeeds{waits: s.Watching, bounded: s.Budget > 0}); err != nil {
			failure = fmt.Errorf("open a pull: %w", err)
			schedule.Stopped = ScheduleUnreadable
			break
		}
		spend = pull.Spend
		environment = pull.Environment
		docket = pull.Triage
		brake, summons, cooldown, cycleBound = pull.Brake, pull.Summons, pull.BrakeCooldown, pull.BrakeEscalationCycles
		runs = pull.Runs
		poll = pull.Poll
		// The bound is read at every pull like everything else, so a session
		// draining under a bound somebody lengthens moves its deadline at the next
		// pull rather than at the next deploy.
		drain.arm(pull.RedeployDrainLimit, s.now())
		if drain.active {
			schedule.Drain.Bound = drain.limit
			session.draining(drain.record(running))
		}
		// Stopped work reaches the development manager here, rather than by
		// somebody carrying it to her. It is done before the brake and before the
		// hold, because it chooses nothing and starts nothing: what it produces is
		// her judgment about work that has already stopped, which is exactly what a
		// held or braked queue is usually waiting on. What it delivers is bounded to
		// one stoppage per pass; see Escalator.
		// A delivery is a turn in the development manager's conversation, so it
		// waits while a firing of hers is taking turns there, and is made at the
		// first pull after that ends.
		if _, busy := firings.holding(roleConversationKey(domain.RoleDevelopmentManager)); !busy {
			s.escalate(ctx, &schedule, pull)
			s.summonWaiting(ctx, &schedule, firings, pull)
		}
		// The schedule is fired here for the same reasons: it chooses nothing and
		// starts no run, so neither the brake nor the hold below stops it. It is
		// placed after the escalation, which goes first because stopped work is a
		// specific thing that has already gone wrong and is waiting on a
		// judgment, where a recurring pass is the standing look that runs whether
		// or not anything happened.
		//
		// A task that went a whole interval unfired is recorded as missed first,
		// with what kept it, before the firing that resumes it; see missed.
		//
		// The two are not bounded alike. The escalation delivers at most one
		// stoppage per pass and takes its turn inside the pull. A schedule that
		// can fire beside the pull does: the firings due are claimed here, in the
		// order of how long each has waited, and their turns are taken in
		// goroutines of their own, one per conversation and at most
		// MaxConcurrentFirings at once, so no role's pass holds another role's
		// and none holds the pull — the queue below is read and started from
		// while they run. A schedule that cannot is fired in place, one firing
		// per pass, as it always was. A session draining to restart that hosts
		// no run starts no new firing, so the restart is not put off by one.
		s.missed(ctx, &schedule, pull, &cadence)
		if concurrent, beside := pull.Recurring.(ScheduleRecurringConcurrent); beside {
			if !drain.active || running > 0 {
				cadence.hold(s.startFirings(ctx, &schedule, concurrent, firings, &cadence))
			}
		} else {
			cadence.hold(s.fire(session.passing(ctx), &schedule, pull))
		}
		// And a role whose tracker block the harness refused is woken here, last of
		// the three. It is placed after the other two because it is the cheapest to
		// be late with: the refusal is already in that conversation's next turn
		// whenever one happens, so what a later pass costs it is an interval, where
		// a stoppage nobody delivers and a cadence nobody fires cost their whole
		// pass. Like both of them it takes at most one turn; see Corrector.
		s.correct(ctx, &schedule, pull)
		// The claims the tracker holds are audited here, for the reason the three
		// above it run here: it chooses nothing and starts nothing, so none of what
		// follows is allowed to stop it happening. It is last of the four because it
		// spends no turn — it is a reading and, on the rare pass, a tracker write —
		// so nothing it does bears on how much waking the pass has done.
		//
		// Each of the four things below would, and each is a state the audit is most
		// needed in. A provider answering nobody is a wait this session sits in for
		// as long as a login takes to renew, and a run killed before the outage
		// began is exactly as dead through it — the runs that are waiting hold their
		// claims by their own pause, so the audit reads nothing wrong in them. A
		// held intake is often this session's own brake, which is placed exactly
		// when runs are failing one after another — the pass where a claim is most
		// likely to have just died is the pass a hold would have silenced. A full
		// machine is the state a dead claim produces: two slots held by runs nobody
		// is running, and no pull that stops at the capacity check ever gets as far
		// as the queue. And the queue read below is the last of them, which is why
		// the claimed items are read here and handed down to it rather than read
		// twice.
		//
		// It costs a waiting, held, or full session one tracker read a poll it did
		// not pay before, and buys the only thing that ever frees those slots. A
		// pull wired without an audit spends nothing at all: the reading is the
		// audit's, so that pull makes it where it always did, in the queue below.
		claimed, claimedRead := s.audit(ctx, &schedule, pull)
		// A provider answering nobody is read before the brake and before anything
		// is chosen. It is read here rather than folded into the hold below because
		// it is the opposite kind of stop: nothing a person placed and nothing
		// `yoyo release` lifts, and a session that dispatched into it would count
		// the refusals as blocked runs and place exactly that hold. While it stands
		// the session chooses nothing and says why, and it is the session that
		// finds the login renewed — which is what makes re-authentication resume
		// the line without anybody releasing anything.
		if outage, away := s.providerAway(ctx, &schedule, pull); away {
			if !s.Watching {
				schedule.Stopped = ScheduleProviderAway
				break
			}
			cadence.provider(outage.Says())
			if !awaitProvider(pull, account{reason: outage.Says(), running: running}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}
		// A usage window the record says is closed over every model a developer's
		// turn could end on is read here, for the reason the outage above is: it is
		// nothing a person placed and nothing `yoyo release` lifts, and a pull made
		// into it starts runs the provider refuses. It is read from the durable
		// record at every pull rather than remembered, which is what makes a
		// restarted session honour it from its first poll: on 2026-09-23 a session
		// restarted over and over inside a seven_day window pulled fresh items into
		// the same refusal each time, and one limit became twelve failed runs. It
		// lifts at the reset the provider named, with nothing to release.
		if closed, standing := s.usageWindow(&schedule, pull); standing {
			if !s.Watching {
				schedule.Stopped = ScheduleProviderWindow
				break
			}
			said := account{reason: windowReason(closed.window, closed.found), running: running, window: closed.window}
			cadence.provider(said.reason)
			if !awaitProvider(pull, said) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}
		// A target branch the harness will not catch up to the remote's is read
		// here, for the reason the two above are: nothing a person placed and
		// nothing `yoyo release` lifts, and every run a pull started into it would
		// spend a whole development and review before stopping on the same
		// divergence. The brake does not count those stops, so without this the line
		// went on paying for them one item at a time until somebody unwedged the
		// branch. It is read from the durable record the refusing run wrote, and it
		// lifts when the convergence sweep finds the branches converged — the next
		// pull after that chooses again, with nothing to release.
		if diverged, standing := s.divergedTarget(&schedule, pull); standing {
			if !s.Watching {
				schedule.Stopped = ScheduleDivergedTarget
				break
			}
			if !awaitProvider(pull, account{reason: divergedTargetReason(diverged), running: running}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}
		// A developer's provider found not to put its launch settings in force is
		// read here, for the reason the divergence above is: nothing a person
		// placed and nothing `yoyo release` lifts, and every developer a pull
		// started into it would be refused on the same check. It lifts on a check
		// that finds the settings in force, which this reading lets one dispatch
		// make once the probe interval has passed, or on a new harness build.
		if held, standing := s.launchSettingsHeld(&schedule, pull); standing {
			if !s.Watching {
				schedule.Stopped = ScheduleLaunchSettingsHeld
				break
			}
			if !awaitProvider(pull, account{reason: held.Says(), running: running}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}
		// The brake is applied before the hold is read, so the reading that
		// follows is what stops the choosing whether the operator held intake or
		// this session did. Nothing else in the loop knows the difference, which
		// is the point: a brake that stopped the line by its own separate path
		// would be a second account of a rule that already has one.
		if s.Watching && blockedInARow > 0 && pull.BlockedRunsBeforeIntakeHold > 0 && blockedInARow >= pull.BlockedRunsBeforeIntakeHold {
			s.brake(ctx, &schedule, firings, pull, session, running, blockedInARow, storm)
			blockedInARow = 0
			storm = nil
		}
		// The intake hold is read before anything is chosen, because choosing is
		// the whole of what it holds. It is asked again on every pull rather than
		// once for the pass: the hold that matters is the one the operator places
		// while the scheduler is running, and a pass that answered from its first
		// reading would keep choosing work for as long as it lasted.
		//
		// It is read here, ahead of the carry-out below, and acted on after it. The
		// reading chooses nothing; what it is for at this point is telling the
		// carry-out whether a decision the hold already stopped once is worth
		// attempting again, which it is not while the hold still stands.
		hold, held, err := pull.Intake.Held()
		if err != nil {
			if !unreadable(fmt.Errorf("read whether intake is held: %w", err)) {
				break
			}
			continue
		}
		paused, err := pull.paused()
		if err != nil {
			if !unreadable(err) {
				break
			}
			continue
		}
		// What is in flight is read before the hold is acted on rather than after.
		// It chooses nothing — it is two counts taken from the durable records — and
		// reading it here is what lets the carry-out below be attempted whatever
		// the hold turns out to say.
		occupied, err := occupiedItems(pull.Runs)
		if err != nil {
			if !unreadable(err) {
				break
			}
			continue
		}
		// A run paused on work its item waits on holds no developer slot, so it is
		// taken out of what counts against the capacity here and kept only for the
		// race guard below: its change is still in its worktree, and work started
		// beside it that shares its files would conflict with it when it lands.
		waitingRuns := waitingOnDependencies(occupied)
		// The runs a session before this one stopped for its own redeploy are
		// picked up here, before anything new is chosen and before the intake hold
		// is acted on: each already holds a seat and a claim, so continuing it
		// chooses no work — a held intake lets what is running finish, and a
		// stopped run is running work put down for a moment — and it is exactly the
		// work the restart was made to preserve. Nothing is pulled into a seat a
		// re-adopted run holds, because the seat was never free.
		//
		// A draining session re-adopts nothing, and no session re-adopts a run it
		// stopped itself. The runs a draining session finds carrying a stop are the
		// ones it put down for the session that comes back, and picking one up
		// again would resume it only to stop it at the next look — once a poll for
		// as long as a promotion beside it is waited out, each round re-running the
		// run's checks and noting another redeploy stop on its item.
		if s.Watching && !drain.active {
			for id, state := range occupied {
				if _, ours := mine[id]; ours || state.RedeployStop == nil {
					continue
				}
				if s.SessionID != "" && state.RedeployStop.SessionID == s.SessionID {
					continue
				}
				selection := runstate.Selection{
					By:     runstate.SelectedByScheduler,
					Reason: readoptionReason(state),
				}
				// A run this session already tried and the pipeline never took is
				// tried again on the same entry. The record still carrying its stop
				// is what says the pipeline never took it: a resumed run clears the
				// stop as it is picked up.
				index, tried := readopted[state.RunID]
				if tried {
					schedule.Started[index] = Started{WorkItemID: id, Reason: selection.Reason, Readopted: state.RunID, Readoptions: schedule.Started[index].Readoptions + 1}
				} else {
					index = len(schedule.Started)
					schedule.Started = append(schedule.Started, Started{WorkItemID: id, Reason: selection.Reason, Readopted: state.RunID, Readoptions: 1})
					readopted[state.RunID] = index
				}
				mine[id] = index
				running++
				s.host(session.dispatching(ctx, id), pull, id, index, selection, hosted, landings, deliver)
			}
		}
		for id := range mine {
			// A run this session started and that has not reserved yet is in flight
			// with no identifier to name; one that has reserved is already here under
			// the identifier the store gave it.
			if _, recorded := occupied[id]; !recorded {
				occupied[id] = runstate.State{WorkItemID: id}
			}
		}
		free := pull.Capacity - len(occupied)
		// freed is the slots that came free since the last pull and whose runs had
		// held them, which the line a filling poll writes says; heldElsewhere is how
		// many slots another process's runs hold now, which decides what a session
		// at full capacity waits on below.
		var freed []freedSlot
		freed, ended, elsewhere = freedSince(elsewhere, ended, occupied, mine, schedule.Started)
		heldElsewhere := len(elsewhere)
		// freeAtPoll and filledByCarryOut are what the line a filling poll writes
		// says about the slots: how many this poll found free, and how many of them
		// a carried-out decision took before the queue was read.
		freeAtPoll, filledByCarryOut := max(free, 0), 0

		// A decision the development manager recorded is fired here, against the same
		// capacity the queue's own work is chosen against and before any of it at
		// its item's priority or below: a stoppage she has already judged is work
		// that was chosen once and stopped, and leaving it behind the queue would be
		// the harness preferring fresh work to work it has already spent a run on.
		// Ready work of a higher priority is the exception, below.
		//
		// It takes a slot and is waited out exactly as a chosen item is. What it is
		// not is a queue entry: the item is blocked or claimed rather than pullable,
		// so nothing below would ever have reached it, and the started entry it
		// leaves is what accounts for the run.
		//
		// It is attempted before the hold and before the capacity check below are
		// acted on, and that placement is the whole of what keeps those two gates
		// from being silent. Both stop the pass here, so a carry-out placed after
		// either would never be reached while either was closed — and a decision
		// that cannot be carried out with nothing anywhere saying why is the one
		// outcome this mechanism exists to end. Nothing is chosen by attempting it:
		// the hold and the capacity are read again inside the action, which is where
		// they refuse and where the refusal is written onto the item as a finding the
		// development manager reads. So the harness claims nothing under a hold and
		// records why it did not, which is what the hold is for and what she was
		// missing.
		//
		// Once, though, per closing of the gate. A refusal by a gate shut for
		// everything at once is not paced in the record, because pacing it would leave
		// a lifted hold unnoticed for the whole delay — so the record offers the same
		// decision again on every pull the gate stands, and a pass that attempted
		// every offer would append a started entry and rewrite the item's record once
		// per poll interval for the length of a pause. What bounds that is the pass's
		// own memory of which gate stopped which decision, read against the switches
		// this pull can see: a decision the hold stopped is left alone while the hold
		// is up and attempted on the first pull it is down, which is the latency the
		// unpaced record exists to keep. See nextCarryOut.
		// A re-arm she decided about a publication — a merge nothing ever asked the
		// forge for, or one the forge dropped — is fired first, and in the pull's
		// own thread: it is one merge request rather than a run, so it takes no slot
		// and leaves nothing to wait out.
		s.carryOutNotes(ctx, &schedule, pull)
		s.carryOutRearms(ctx, &schedule, pull, held)
		//
		// A draining session that has stopped pulling into free seats does not
		// carry a decision out either: the run it would start is one the drain
		// stops at its next look, and the record offers the decision again to the
		// session that comes back.
		closed := closedGates{intake: held, pause: paused, capacity: free < 1}
		carrying := false
		// A session bounded by --limit is bounded here too: every decision fired is
		// a run started, and the bound is on runs started.
		remaining := 0
		if s.Limit > 0 {
			remaining = s.Limit - len(schedule.Started)
		}
		// A decision goes ahead of the queue only where nothing ready in the Lead
		// Product Manager's order outranks the item it is about. Until
		// yoyodyne-ifd.428.58 every decision took a free slot before the queue was
		// read, whatever the priority of its item, and on 2026-09-29 the slot a run
		// freed at 13:42 PDT went twenty-five seconds later to a re-run of the
		// concurrent tracker access item (yoyodyne-ifd.271) at priority 3 while
		// two priority-0 items stood ready. A decision outranked that way is held
		// for the walk below, which fires it into a slot at the point in the order
		// its item's priority puts it; a decision about an item at the same priority
		// as the best ready work still goes first, for the reason above.
		//
		// Knowing that takes the queue, so the queue is read here, once, and only
		// where a decision asks — the pull below reads it again otherwise. A read
		// that failed ranks nothing and the decision goes first as it always did,
		// because a decision held back on a queue nobody could read is held on no
		// reason at all. Under the hold or the pause nothing is held back either:
		// the queue is not pulled while they stand, so the attempt is what records
		// the gate's refusal on the item.
		//
		// A pull that found no slot free asks too. It attempts a decision only so
		// that the capacity gate's refusal is recorded, and the action reads the
		// runs again: one that ended since this pull read them leaves the gate a
		// slot to give, and an outranked decision attempted then took it ahead of
		// the work that outranks it.
		//
		// A decision about a stopped run whose change is still there — its branch or
		// its worktree found standing, by the same look the hold and the docket take —
		// is outranked by nothing (yoyodyne-ifd.428.81). Finishing reviewed, preserved
		// work is cheaper than starting new work, the decision to continue it is
		// already made, and a preserved worktree waiting behind the order ages into
		// conflicts: on 2026-10-05 three such decisions on priority 1 and 2 items
		// waited behind sixty-odd ready items, one for thirty-six hours. So it takes
		// the first free slot ahead of fresh pulls of any priority, oldest decision
		// first, under every gate it asked before; the queue is still read for it, so
		// the run's reason can name the ready work it went ahead of.
		var early *pulled
		earlyAsked := false
		preserved := preservedWork(ctx, pull, s.now)
		outranked := func(task CarryOutTask) (outrankedCarryOut, bool) {
			if task.Decision == DecisionContinueStall {
				return outrankedCarryOut{}, false
			}
			kept := preserved(task)
			if held || paused {
				return outrankedCarryOut{task: task, preserved: kept}, false
			}
			if !earlyAsked {
				earlyAsked = true
				if read, err := pull.queue(ctx, claimed, claimedRead); err == nil {
					early = &read
				}
			}
			if early == nil {
				return outrankedCarryOut{task: task, preserved: kept}, false
			}
			ranked, outranks := outranking(task, *early, occupied)
			ranked.preserved = kept
			return ranked, outranks && !kept
		}
		var tasks []CarryOutTask
		var passedCarryOuts map[string]string
		var pendingCarryOuts []outrankedCarryOut
		_, declining := drain.declinesStarts(pull.Poll, s.now())
		if !declining {
			tasks, passedCarryOuts, pendingCarryOuts = s.nextCarryOuts(&schedule, pull, occupied, mine, waitingOn, closed, free, remaining, outranked)
		}
		// With no slot free this pull never walks the queue, so a decision held back
		// for outranking work is written up now, beside the ones passed over.
		if free < 1 && len(pendingCarryOuts) > 0 {
			for _, pending := range pendingCarryOuts {
				passedCarryOuts[pending.task.RunID] = pending.reason()
			}
			pendingCarryOuts = nil
		}
		firedCarryOuts := 0
		fireCarryOut := func(task CarryOutTask) {
			reusesSlot := task.Recover && occupied[task.WorkItemID].RunID == task.RunID && occupied[task.WorkItemID].RepairDispatchPending()
			index := len(schedule.Started)
			schedule.Started = append(schedule.Started, Started{
				WorkItemID: task.WorkItemID,
				Reason:     carryingOutReason(task),
			})
			// Both, for the two different questions below: occupied is what stops the
			// queue scan choosing the same item beside this, and mine is what stops the
			// next pull counting the slot free before the run has reserved it.
			occupied[task.WorkItemID] = runstate.State{WorkItemID: task.WorkItemID}
			mine[task.WorkItemID] = index
			running++
			// Only where there was one to take. An attempt made with no slot free is
			// one the action's own capacity gate refuses before it reserves anything,
			// and a count driven negative here would offer the queue below room the
			// harness does not have.
			if free > 0 && !reusesSlot {
				free--
				filledByCarryOut++
			}
			carrying = true
			firedCarryOuts++
			// Hosted like any run this session starts, so a drain bound that runs
			// out stops a carried-out run at its checks exactly as it stops a chosen
			// one, rather than waiting it out past the bound.
			runCtx, cancel := context.WithCancelCause(session.dispatching(ctx, task.WorkItemID))
			runCtx = withLandingNotice(runCtx, func() { landings.begun(index) })
			hosted[index] = cancel
			go func(task CarryOutTask) {
				carried, outcome, err := pull.CarryOut.Carry(runCtx, task)
				deliver(completed{index: index, outcome: outcome, err: err, carriedOut: &carried})
			}(task)
		}
		for _, task := range tasks {
			fireCarryOut(task)
		}
		// A declining drain attempted nothing and is within a poll of its restart,
		// so a decision it left standing is the returning session's to fire or to
		// record as unattempted, rather than this one's to write up as passed over.
		if !declining {
			s.recordUnattempted(ctx, &schedule, pull, passedCarryOuts)
		}

		// A run paused on work its item waits on is continued here once that work
		// has closed, beside the decisions above and for the same reason: its item
		// is claimed rather than pullable, so nothing below would ever reach it, and
		// before this the only thing that did was somebody typing `yoyo run`. It is
		// not attempted under the operator's pause, which the continued run would
		// only meet again at its first step, nor while intake is held: picking a
		// paused run back up is the harness choosing what to spend a slot on, as
		// carrying out a decision is, so it waits for the hold to lift exactly as a
		// recorded repair does. A declining drain continues nothing either, for the
		// reason it carries no decision out: the run would be one the drain stops
		// at its next look, and the returning session continues it instead.
		if !paused && !held && !declining {
			continuations, err := s.nextContinuations(ctx, pull, occupied, mine, refusedContinuations, free, len(schedule.Started), passOver)
			if err != nil {
				schedule.ContinuationProblem = err.Error()
			} else {
				schedule.ContinuationProblem = ""
			}
			for _, continuation := range continuations {
				workItemID := continuation.state.WorkItemID
				delete(deferred, workItemID)
				index := len(schedule.Started)
				continuing[index] = continuation.state.RunID
				selection := runstate.Selection{By: runstate.SelectedByScheduler, Reason: continuation.reason}
				schedule.Started = append(schedule.Started, Started{WorkItemID: workItemID, Reason: selection.Reason})
				occupied[workItemID] = continuation.state
				mine[workItemID] = index
				running++
				if !continuation.holdsOne {
					free--
				}
				// Hosted like any run this session starts, so a drain bound that runs
				// out stops a continued run exactly as it stops a chosen one.
				s.host(session.dispatching(ctx, workItemID), pull, workItemID, index, selection, hosted, landings, deliver)
			}
		}

		// probing is this pull starting the brake's probe run under the hold: one
		// item, chosen exactly as any other would be, and named on the hold's own
		// record before it starts so the pipeline lets it through the hold.
		probing := false
		if held {
			schedule.IntakeHeld = &hold
			if !s.Watching {
				schedule.Stopped = ScheduleIntakeHeld
				break
			}
			// The brake's own hold is worked rather than waited on: released where
			// its record says to release it, probed where its record says a probe
			// is due, and waited on otherwise — which is the development manager
			// deciding, a probe in flight, or her having escalated it.
			switch s.workBrake(&schedule, pull, hold, mine) {
			case brakeReleased:
				schedule.IntakeHeld = nil
				session.resume("the brake's hold was released on the development manager's decision")
				continue
			case brakeProbing:
				probing = true
			default:
				// A held intake is a brake rather than a stop: the session keeps
				// polling and chooses nothing, and resumes in place when it is
				// released. That is what makes holding intake something an operator
				// can do to a session they are not sitting at.
				if !wait(pull, runstate.WatchBraked, account{
					reason: brakedReason(hold),
					// This session's own runs: a held intake stops the choosing and
					// interrupts nothing, and the reader has to be able to tell those apart.
					// What another process has going is not read until after the hold is.
					running: running,
					mover:   brakedMover(hold),
				}) {
					schedule.Stopped = ScheduleCancelled
					break pulling
				}
				continue
			}
		} else {
			schedule.IntakeHeld = nil
		}

		if stopped := pull.blocked(ctx); stopped != "" {
			schedule.Blocked = stopped
			if !s.Watching {
				schedule.Stopped = ScheduleBlocked
				break
			}
			if !wait(pull, runstate.WatchBlocked, account{reason: stopped, running: running}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}
		schedule.Blocked = refused

		schedule.Capacity = pull.Capacity
		schedule.Occupied = len(occupied)
		// A probe is one run, whatever the capacity leaves free: what it is for is
		// finding out whether the line is fine, and two of them would be two runs
		// spent on one question.
		if probing && free > 1 {
			free = 1
		}
		if free < 1 {
			// A watch with a slot another process holds waits on the interval as
			// well as on its own runs: that process's run ending frees a slot this
			// session hears of only by reading the runs in flight again, so a wait on
			// its own completions alone left the slot empty until one of them ended.
			// With every slot its own, a run of its own ending is still the only
			// thing that can change the answer, and it waits on that as it did.
			if running > 0 && s.Watching && heldElsewhere > 0 {
				if !awaitRunOrPoll(pull) {
					schedule.Stopped = ScheduleCancelled
					break
				}
				continue
			}
			if running > 0 {
				if !collectUntilDue(pull) {
					schedule.Stopped = ScheduleCancelled
					break
				}
				continue
			}
			if !s.Watching {
				schedule.Stopped = ScheduleCapacityFull
				break
			}
			if !wait(pull, runstate.WatchIdle, account{
				reason:  "every developer slot is held by a run this session did not start",
				running: len(occupied),
			}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}

		// A draining session pulls into its free seats right up to the restart,
		// with two exceptions it says out loud. A pull made with the bound closer
		// than one poll away would start a run only to stop it, so the seat is
		// left for the session that comes back; and past the bound, while a run
		// at its promotion or recording a finished check stage is waited out,
		// a run started now would be stopped at
		// the next look. Skipped rather than found empty, and said as skipped,
		// because the two are otherwise the same silence — and marked on the
		// drain as well as said, so the read model names the poll as the session
		// restarting rather than as an idle session over a queue with work in it.
		if remaining, declined := drain.declinesStarts(pull.Poll, s.now()); declined {
			schedule.Drain.Skipped++
			skipped := drain.record(running)
			skipped.PullSkipped = true
			session.draining(skipped)
			reason := fmt.Sprintf("nothing more is pulled into the %d free seat(s): the drain bound is %s away, which is less than one poll, so the session that comes back pulls them; %s",
				free, remaining.Round(time.Second), skipped.Says())
			if drain.boundReached {
				reason = fmt.Sprintf("nothing more is pulled into the %d free seat(s): the drain bound has run out and %s still going at its promotion, recording a finished check stage, or still being stopped, is waited out rather than joined, so the session that comes back pulls them; %s",
					free, plural(running, "run", "runs"), skipped.Says())
			}
			if !wait(pull, runstate.WatchIdle, account{reason: reason, running: len(occupied)}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}

		// The queue a decision was ranked against is the one this pull chooses from,
		// so the order that held a decision back is the order that decides when it
		// fires.
		var read pulled
		if early != nil {
			read = *early
		} else {
			read, err = pull.queue(ctx, claimed, claimedRead)
			if err != nil {
				if !unreadable(err) {
					break
				}
				continue
			}
		}
		queue := read.queue
		// Work a conversation carries whose landing is already in the repository
		// is closed here, off the queue this pull just read, before anything is
		// chosen from it. It is done here rather than beside the four sweeps above
		// because it needs the queue — it reads the entries' executors — and
		// because the entries it closes must not then be passed over on this same
		// pull as work still waiting on somebody opening a conversation.
		queue.Entries = s.land(ctx, &schedule, pull, queue.Entries)
		schedule.Admitted = len(queue.Entries)
		schedule.Pullable = queue.Ready()
		schedule.BacklogRead = true
		// Every item the docket holds as unready is read again off the backlog this
		// pull just read, before anything is chosen, so an item amended since the
		// last pull stands on the docket in the words it now carries or not at all.
		if problem := pull.settleUnready(read); problem != "" && schedule.ReadinessProblem == "" {
			schedule.ReadinessProblem = problem
		}
		stale, stalenessProblem := pull.stale(ctx)
		if stalenessProblem != "" {
			schedule.StalenessProblem = stalenessProblem
		}

		// What the runs already going have taken, so nothing that would race one
		// is started beside it. It is seeded from every item in flight anywhere —
		// this pass's own runs and another process's alike — and grows as this
		// pull starts things.
		flight := newInFlight()
		for id, run := range occupied {
			flight.take(read.items[id], run.RunID)
		}
		for id, run := range waitingRuns {
			flight.take(read.items[id], run.RunID)
		}
		// Which developer slots are free, in the order this pull fills them: the
		// slots that prefer a label first, so labelled work is pulled into the slot
		// configured for it before a slot with no preference reaches it. It is
		// read from what is in flight rather than remembered, like everything
		// else here, and it is the same reading the standing status makes.
		freeSlots := pull.slotsOf(occupied, read.items).Free()
		if len(freeSlots) > free {
			freeSlots = freeSlots[:free]
		}
		// sequenced names the items this pull has held back for a conflict so
		// far, in the order the product manager set. An item started after one of
		// them was pulled ahead of it, and its recorded reason says so.
		var sequenced []string
		// What this pull passed over and why, assembled as it goes so that a poll
		// which starts nothing can say what it actually found rather than only that
		// it found nothing. It is this pull's own reading and is discarded with it.
		poll := idlePoll{}

		// eligible is whether one entry may be started this pull, decided once per
		// entry however many slots ask about it, with what passed it over recorded
		// at the first asking. Every question but the conflict one is asked here:
		// the conflict depends on what this pull has already started, so it is
		// asked again at every attempt, where the others hold for the whole pull.
		//
		// It is asked lazily — of the entries a slot actually reaches — rather than
		// of the whole queue up front, because the last of its questions reads the
		// repository and a pull with one free slot has no business reading the tree
		// for forty items to fill it.
		decided := make(map[string]eligibility)
		eligible := func(entry backlog.Entry) (eligibility, error) {
			if answer, asked := decided[entry.ID]; asked {
				return answer, nil
			}
			answer, err := s.eligibility(entry, eligibilityReading{
				pull: pull, read: read, tried: tried, occupied: occupied, waiting: waitingRuns,
				schedule: &schedule, poll: &poll, passOver: passOver,
			})
			if err == nil {
				decided[entry.ID] = answer
			}
			return answer, err
		}
		// start spends one free slot on one entry. It reports false where the
		// probe's record would not take the item, which ends the choosing for this
		// pull exactly as it did before slots had preferences.
		started := 0
		startedNow := make(map[string]bool)
		start := func(entry backlog.Entry, slot developerslot.Slot, into pulledInto) bool {
			delete(deferred, entry.ID)
			// The exclusion is made as the start is, and says what it is for from the
			// first poll that meets it. A start in flight is the one state here nobody
			// has to be told about afterwards, and it is still said: an exclusion whose
			// reason appeared only once the run ended would be blank for exactly as long
			// as the run took.
			tried[entry.ID] = attempt{
				fingerprint: fingerprint(read.items[entry.ID]),
				title:       read.items[entry.ID].Title,
				reason:      "this session started it at an earlier poll and that run has not ended yet",
			}

			// Only an item that was actually held back carries the first half of
			// this; the second is whatever this pull passed over ahead of it.
			ordering := sequencing{after: sequencedEarlier[entry.ID], ahead: append([]string(nil), sequenced...)}
			delete(sequencedEarlier, entry.ID)

			index := len(schedule.Started)
			selection := runstate.Selection{
				By:     runstate.SelectedByScheduler,
				Reason: scheduleReason(entry, queue, free, pull.Capacity, stale[entry.ID], ordering) + into.reason(pull.Slots),
			}
			if probing {
				// The probe is named on the hold's own record before it starts,
				// because that record is what the pipeline reads to let this one run
				// through the hold: a selection that merely said it was the probe
				// would be a hold any caller could talk its way past. A record that
				// will not take it starts nothing, and says so.
				selection = runstate.Selection{By: runstate.SelectedByBrake, Reason: probeReason(hold, selection.Reason)}
				if !s.recordProbe(&schedule, pull, entry.ID) {
					delete(tried, entry.ID)
					return false
				}
			}
			schedule.Started = append(schedule.Started, Started{WorkItemID: entry.ID, Reason: selection.Reason, Probe: probing, Slot: slot.Number})
			flight.take(read.items[entry.ID], "")
			mine[entry.ID] = index
			startedNow[entry.ID] = true
			running++
			started++
			// The slot travels with the run, so a slot with an endpoint pair starts
			// the run on that pair rather than on whichever slot the run would
			// otherwise be read into (developerrouting.go).
			s.host(withDeveloperSlot(session.dispatching(ctx, entry.ID), slot.Number), pull, entry.ID, index, selection, hosted, landings, deliver)
			return true
		}
		// firedInWalk is the slots the walk gave to a decision held back above for
		// ready work that outranked it, which fill a slot as a start does.
		firedInWalk := 0
		bounded := func() bool {
			return started+firedInWalk == len(freeSlots) || (s.Limit > 0 && schedule.Chosen() >= s.Limit)
		}
		// firePending gives the slot being filled to the decision held back for
		// outranked work, where the walk has reached work its item's priority
		// matches or beats — or, with any, reached the end of the queue with the
		// slot still empty. The highest-priority decision goes first, and the
		// docket's order between two at one priority.
		firePending := func(priority int, any bool) (string, bool) {
			chosen := -1
			for index, pending := range pendingCarryOuts {
				if (any || pending.priority <= priority) && (chosen < 0 || pending.priority < pendingCarryOuts[chosen].priority) {
					chosen = index
				}
			}
			if chosen < 0 {
				return "", false
			}
			task := pendingCarryOuts[chosen].task
			pendingCarryOuts = append(pendingCarryOuts[:chosen:chosen], pendingCarryOuts[chosen+1:]...)
			fireCarryOut(task)
			flight.take(read.items[task.WorkItemID], "")
			firedInWalk++
			return task.WorkItemID, true
		}

		// racedNow is the entries this pull found racing work in flight. The
		// flight only grows within a pull, so an entry that raced once races at
		// every later asking, and it is accounted for at the first.
		racedNow := make(map[string]bool)
		// fill walks the queue in the product manager's order for one free slot
		// and starts the first entry the slot may take. A slot asking for its
		// preferred label walks past everything not carrying it without asking
		// anything else about it, and where it then takes something, what it
		// walked past on the way is remembered: an entry ranked above the one it
		// took, that no slot then reaches, is what the pass reports as left for
		// another slot. A walk that takes nothing leaves nothing for anyone — the
		// slot falls back over the same entries, or the pass's bound stops it.
		var leftBehind []walkedPast
		fill := func(slot developerslot.Slot, labelled bool) (walk, error) {
			var past []walkedPast
			for _, entry := range queue.Entries {
				if startedNow[entry.ID] || racedNow[entry.ID] {
					continue
				}
				if labelled && !slot.Prefers(read.items[entry.ID].Labels) {
					past = append(past, walkedPast{entry: entry, slot: slot})
					continue
				}
				if took, fired := firePending(entry.Priority, false); fired {
					for index := range past {
						past[index].took = took
					}
					leftBehind = append(leftBehind, past...)
					return walkStarted, nil
				}
				answer, err := eligible(entry)
				if err != nil {
					return walkUnreadable, err
				}
				if answer != startable {
					continue
				}
				// Work that would race something already going is sequenced behind it
				// rather than started beside it. This enforces nothing — the promotion
				// lease and the replay still do all of that — and buys the difference
				// between a wait and a replayed, re-checked, re-reviewed run. It is
				// re-read at every pull like everything else here, so an item held back
				// now is pulled at the first pull where the run it would have raced has
				// ended. The line it leaves on the schedule names the run this pull found
				// it behind, so a session that held an item behind three runs in turn
				// reports the last of them rather than the first.
				if racing, races := flight.against(read.items[entry.ID]); races {
					racedNow[entry.ID] = true
					passOver(entry.ID, racing.reason())
					sequencedEarlier[entry.ID] = racing
					sequenced = append(sequenced, entry.ID)
					poll.pass(entry.ID, runstate.PassedOverSequencedBehindWork, "")
					continue
				}
				if !start(entry, slot, pulledInto{slot: slot, labelled: labelled}) {
					return walkStopped, nil
				}
				for index := range past {
					past[index].took = entry.ID
				}
				leftBehind = append(leftBehind, past...)
				return walkStarted, nil
			}
			// A slot the queue left empty goes to a decision the order held back,
			// since what outranked it was not startable after all. A slot walking
			// only its label's work leaves it for the fallback walk, which asks the
			// whole order first.
			if !labelled {
				if _, fired := firePending(0, true); fired {
					return walkStarted, nil
				}
			}
			return walkNothing, nil
		}

		// The slots are filled in three passes. First every free preferring slot
		// pulls its own label's work, wherever that sits in the order. Then every
		// free slot with no preference pulls what is left, in the order. Last, a
		// preferring slot that found none of its label's work ready falls back to
		// the rest of the backlog — which is the difference between a slot that
		// prefers a label and one reserved for it: it never idles on an empty label.
		filled := make(map[int]bool, len(freeSlots))
		passes := []struct{ labelled, preferring bool }{{true, true}, {false, false}, {false, true}}
	filling:
		for _, pass := range passes {
			for _, slot := range freeSlots {
				if filled[slot.Number] || slot.Preferring() != pass.preferring || bounded() {
					continue
				}
				walked, err := fill(slot, pass.labelled)
				switch walked {
				case walkStarted:
					filled[slot.Number] = true
				case walkStopped:
					break filling
				case walkUnreadable:
					if !unreadable(err) {
						break pulling
					}
					// The items this pull already started keep running and are collected
					// like any other; the pull that follows re-reads the queue and passes
					// over them because they are in flight.
					continue pulling
				}
			}
		}
		// What a preferring slot walked past for want of its label on the way to
		// what it took, and that no slot then reached, is accounted for here. Each
		// is asked the same questions a slot would have asked it — these are the
		// entries a walk with no preference would have reached, so this reads no
		// more of the harness than that walk did — and one that could have been
		// started is left for another slot, while one that could not keeps the
		// account those questions gave it. An entry some slot did reach has its
		// own account already.
		for _, past := range leftBehind {
			if _, asked := decided[past.entry.ID]; asked || startedNow[past.entry.ID] || racedNow[past.entry.ID] {
				continue
			}
			answer, err := eligible(past.entry)
			if err != nil {
				if !unreadable(err) {
					break pulling
				}
				continue pulling
			}
			if answer != startable {
				continue
			}
			if racing, races := flight.against(read.items[past.entry.ID]); races {
				racedNow[past.entry.ID] = true
				passOver(past.entry.ID, racing.reason())
				sequencedEarlier[past.entry.ID] = racing
				poll.pass(past.entry.ID, runstate.PassedOverSequencedBehindWork, "")
				continue
			}
			passOver(past.entry.ID, leftForAnotherSlotReason(past.slot, past.took))
			poll.pass(past.entry.ID, runstate.PassedOverLeftForAnotherSlot, "")
		}
		// A decision the walk never reached a slot for is written onto its item as
		// unattempted, with the ready work that outranked it named, which is where the
		// development manager reads why a decision of hers is standing.
		if len(pendingCarryOuts) > 0 {
			outrankedBy := make(map[string]string, len(pendingCarryOuts))
			for _, pending := range pendingCarryOuts {
				outrankedBy[pending.task.RunID] = pending.reason()
			}
			pendingCarryOuts = nil
			s.recordUnattempted(ctx, &schedule, pull, outrankedBy)
		}
		if probing && started == 0 {
			// Nothing was startable under the hold, so the probe found nothing to
			// probe with. That is said on the hold and the cooldown is restarted,
			// so a queue with nothing pullable is asked again once per cooldown
			// rather than once per poll — and it is not a landing, because nothing
			// landed. The session is still braked, and waits as one.
			s.recordNoProbe(&schedule, pull, readmodel.IdleLine(poll.passedOver(queue), len(occupied)))
			unprobed, stillHeld, err := pull.Intake.Held()
			if err != nil || !stillHeld {
				unprobed = hold
			}
			if !wait(pull, runstate.WatchBraked, account{reason: brakedReason(unprobed), running: running, mover: brakedMover(unprobed)}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}
		if started > 0 || carrying {
			// A run the provider accepted is the provider serving again, whatever
			// deadline it last named. Keeping the window would have the session go on
			// reporting itself held while it works, which is the false half of the same
			// misreading this exists to end.
			window = providerWindow{}
			if probing {
				// The line is still held: what started is the probe, and the session
				// says so rather than saying it is choosing again. The hold is read
				// back rather than remembered, because recording the probe changed it.
				if probed, held, err := pull.Intake.Held(); err == nil && held {
					session.enter(runstate.WatchBraked, account{reason: brakedReason(probed), running: running, mover: brakedMover(probed)})
				}
				continue
			}
			// A pass that started nothing from the queue and fired a decision is a pass
			// that started work, and it says which: the item it took is not in the
			// backlog counts beside it, so a line about the queue alone would report a
			// session that pulled nothing while a run of its own was starting.
			//
			// Every poll that fills a slot says how many it filled, of how many it
			// found free, and how many of its own runs were already going. That is
			// said on every such poll rather than only the first after a wait,
			// because a slot left empty beside a run in flight is what a reader of
			// this log has to be able to see.
			//
			// It also says which slots freed since the last poll and whose runs had
			// held them, because a slot another process's run freed is refilled here
			// with nothing of this session's having ended, and a reader who sees only
			// the count cannot tell where the room came from.
			slots := filledLine(filledByCarryOut+started, freeAtPoll, running-firedCarryOuts-started)
			if started == 0 {
				session.filled(fmt.Sprintf("%s; a triage decision the development manager recorded was carried out; nothing was pulled from a backlog of %d admitted, %d of them ready",
					slots, len(queue.Entries), queue.Ready()) + freedLine(freed))
				continue
			}
			session.filled(fmt.Sprintf("%s; %d item(s) pulled from a backlog of %d admitted, %d of them ready", slots, started, len(queue.Entries), queue.Ready()) + freedLine(freed))
			continue
		}
		// Nothing was startable this pull. With runs of ours still going, one of
		// them finishing changes the answer — it frees a slot, and it may close the
		// item something else was waiting on — so the pass waits rather than
		// concluding the queue is empty.
		if running == 0 && !s.Watching {
			schedule.Stopped = ScheduleDrained
			break
		}
		// What this poll actually found, rather than the bare fact that it started
		// nothing: the runs already going, the items passed over and why, the
		// conversation that has to act where one does, and the provider's usage
		// window where the answer is that nothing would be served anyway.
		//
		// The window is said the moment the session enters it rather than at some
		// later poll, because that is the whole of what makes it worth recording: an
		// account that arrived an hour into the wait would arrive after the watchdog
		// had already woken somebody over the silence.
		passed := poll.passedOver(queue)
		said := account{
			reason:     readmodel.IdleLine(passed, len(occupied)),
			running:    len(occupied),
			executor:   readmodel.Carrier(passed),
			passedOver: passed,
		}
		if window.standing(s.now()) {
			said.window = window
			said.reason = windowReason(window, said.reason)
		}
		// A slot is free here — the pull got past the capacity check — so a watch
		// waits on the interval rather than on a run of its own: work that becomes
		// startable while that run goes, admitted, released, or no longer racing
		// anything, is started at the next poll. A drain waits on the completion,
		// because it is a command somebody is waiting on and a run ending is the
		// only thing that ends it.
		var waited bool
		if refused != "" {
			said.reason = refused
			waited = wait(pull, runstate.WatchBlocked, said)
		} else if s.Watching {
			waited = refill(pull, said)
		} else {
			waited = wait(pull, runstate.WatchIdle, said)
		}
		if !waited {
			schedule.Stopped = ScheduleCancelled
			break
		}
	}

	// Every run this pass started is waited out, including when the pass stopped
	// pulling because something failed or the context ended. A scheduler that
	// returned with runs still going would leave work in flight that nothing in
	// the schedule accounts for, which is the state this whole package exists to
	// keep the harness out of.
	//
	// A session restarting past its drain bound is the one exception, and only
	// for the runs it stopped: each records its stop and reports back within
	// seconds, and it is waited for so that what it recorded is on the schedule
	// and its stop is durable before the process is replaced. One that has not
	// reported back within the grace is named and left. Its process is gone, and
	// its own record is what the session that comes back reads, so the restart
	// does not wait on it.
	var grace <-chan time.Time
	if schedule.Stopped == ScheduleRedeployed && drain.boundReached && running > 0 {
		timer := time.NewTimer(s.stopGrace())
		defer timer.Stop()
		grace = timer.C
	}
collecting:
	for running > 0 {
		select {
		case done := <-completions:
			running--
			delete(mine, schedule.Started[done.index].WorkItemID)
			settle(done)
		case <-grace:
			for id := range mine {
				schedule.Drain.Unreported = append(schedule.Drain.Unreported, id)
			}
			slices.Sort(schedule.Drain.Unreported)
			break collecting
		}
	}
	// And every firing it started, for the same reason: its pass is recorded by
	// the goroutine taking it, and what it cost is this session's spend. A
	// session restarting past its drain bound stops the firings still taking
	// their turns first, as it stopped its runs, so the restart waits only for
	// each to record itself as a missed pass rather than for its turns to end.
	if schedule.Stopped == ScheduleRedeployed && drain.boundReached {
		firings.cancel()
	}
	s.collectFirings(&schedule, firings, &cadence, true)
	drain.stop()
	// The last line, and whether it is an ending. A session stopping to be
	// restarted into the build deployed over it is waiting on nothing and nobody,
	// and a reader told otherwise is being handed the chore this exists to end.
	session.draining(drain.record(0))
	session.stop(stopping(schedule), schedule.Redeploying())
	return schedule, failure
}

// stopping is what the session's last recorded line says: why it stopped, and
// the detail behind it where the reason alone would not be actionable. A session
// that stopped because it could not price itself is read in a channel rather
// than in this process's terminal, so the run nothing could price has to travel
// with the reason rather than staying on a schedule nobody there sees.
//
// A session that stopped because the harness would not be read travels the same
// way and for a sharper reason: what tells an operator whether they are looking
// at a broken store or at one bad minute is how long it went on failing, and the
// reason alone says neither.
func stopping(schedule Schedule) string {
	switch {
	case schedule.Stopped == ScheduleSpendUnreadable && schedule.SpendProblem != "":
		return schedule.Stopped + ": " + schedule.SpendProblem
	case schedule.Stopped == ScheduleUnreadable && schedule.ReadFailure != "":
		return schedule.Stopped + ": " + schedule.ReadFailure
	case schedule.Stopped == ScheduleRedeployed && schedule.Drain != nil && schedule.Drain.BoundReached:
		// A restart made with runs stopped for it says so, and names them: the
		// session that comes back re-adopts them, and a reader of this line is
		// owed what that session is about to pick up. A bound that ran out over
		// nothing but promotions stopped nothing, and says that
		// instead.
		said := schedule.Stopped
		switch {
		case len(schedule.Drain.Stopped) > 0:
			said = fmt.Sprintf("%s; the drain bound of %s ran out with %d run(s) still going, which were stopped and preserved for the session that comes back: %s",
				said, schedule.Drain.Bound, len(schedule.Drain.Stopped), strings.Join(schedule.Drain.Stopped, ", "))
		case len(schedule.Drain.Landings) == 0 && len(schedule.Drain.ChecksWaited) == 0:
			return fmt.Sprintf("%s; the drain bound of %s ran out with only a run at its promotion still going, which was waited out rather than stopped",
				said, schedule.Drain.Bound)
		default:
			said = fmt.Sprintf("%s; the drain bound of %s ran out", said, schedule.Drain.Bound)
		}
		if len(schedule.Drain.ChecksWaited) > 0 {
			said += fmt.Sprintf("; the check stage of %d run(s) was waited out to its end rather than stopped: %s",
				len(schedule.Drain.ChecksWaited), strings.Join(schedule.Drain.ChecksWaited, ", "))
		}
		if len(schedule.Drain.Landings) > 0 {
			said += fmt.Sprintf("; the landing checks of %d run(s) that had already landed were stopped, so each landing is recorded as unverified: %s",
				len(schedule.Drain.Landings), strings.Join(schedule.Drain.Landings, ", "))
		}
		if len(schedule.Drain.Unreported) > 0 {
			said += fmt.Sprintf("; %d stopped run(s) had not reported back in the time a stop takes to record and were not waited for, so the session that comes back reads what each one's own record says: %s",
				len(schedule.Drain.Unreported), strings.Join(schedule.Drain.Unreported, ", "))
		}
		return said
	}
	return schedule.Stopped
}

// readRetries is the run of harness readings that have failed with none
// succeeding between them: when the first of them was, how many there have been,
// and how long the next wait is.
//
// It is cleared by a pull that read everything it needed rather than by a single
// successful reading, because what the window measures is a store that has gone
// on being unreadable — a pull whose queue read succeeds and whose directive read
// fails is not a store that came back.
type readRetries struct {
	since    time.Time
	attempts int
	delay    time.Duration
	// failed marks the pull being made now as having had a reading fail, which is
	// what keeps a pull that failed from clearing itself at the top of the loop.
	failed bool
}

// cooling reports an item this pass has already started and should not start
// again. A drain never starts one twice at all. A watch starts one again when
// the item itself has changed, and not otherwise: what a failed start leaves
// behind is an item exactly as it was, and re-reading it will produce exactly
// the same failure until somebody edits the work, reprioritizes it, or unblocks
// what it depends on.
//
// It covers every item the session has started rather than only the starts that
// failed, and that is deliberate. The case it must not miss is the one that
// leaves an item pullable and unclaimed with nothing recorded anywhere — a run
// the operator's hold stopped before it claimed anything — where starting again
// produces the same non-result immediately and with no wait in between. Narrowed
// to failed starts, that case spins.
func (s Scheduler) cooling(tried map[string]attempt, item beads.WorkItem) bool {
	recorded, attempted := tried[item.ID]
	if !attempted {
		return false
	}
	if recorded.retryAtPoll {
		return true
	}
	if !recorded.until.IsZero() {
		return s.now().Before(recorded.until)
	}
	return !s.Watching || recorded.fingerprint == fingerprint(item)
}

// attempt is one item this pass has already started: the item as it read when
// the start was made, what it is called, and what the exclusion it produced is
// for.
//
// The last two are carried rather than looked up because neither survives the
// start. A pull reads the queue afresh every interval and an item excluded by
// this session is one no later pull dispatches, so by the time anybody asks why,
// the only thing that still holds the answer is this.
type attempt struct {
	// retryAtPoll is a backoff for a start that attempted no work.
	retryAtPoll bool
	fingerprint string
	title       string
	reason      string
	// until is when the exclusion lifts by itself, and zero for every exclusion
	// but one: a run the provider's usage window stopped, which is held until the
	// window resets and is then pulled again whatever became of the item.
	until time.Time
}

// forget releases starts the environment refused after a poll interval.
func forget(tried map[string]attempt) {
	for id, recorded := range tried {
		if recorded.retryAtPoll {
			delete(tried, id)
		}
	}
}

// refusedByTheMachine reports a failed start that the machine refused rather
// than the work failing, and says what refused it.
//
// It answers two ways, and it needs both. The first is what the refusal itself
// says: a step that refuses before the work is ever attempted marks its failure
// as the machine's, and that mark travels with the error. This is the half that
// covers what nothing here can see — a shell the sandbox will not spawn, a state
// store that will not open, an invariants directory that will not read — because
// the site that met the condition is the only place that knows the condition was
// not about the item. The second is asking the machine: where nothing declared
// anything, a start that failed over a checkout that is still refusing work was
// refused by that checkout, whatever else it looked like.
//
// Neither alone is the rule. A readiness read is blind to every way a machine can
// be unusable that leaves the repository clean, and it was the whole of this
// function once — which had a sandbox refusal recorded as the item's own failure,
// held out until somebody edited work that was never the problem, and counted
// toward a brake that then held intake over it. A declaration alone would miss a
// refusal raised outside the pipeline, which is why the probe stayed.
//
// A start that got as far as reserving already has its own durable record, so
// neither question is asked here. Its environmental classification comes from
// that record rather than from a later readiness read.
//
// A session that has been stopped is not asked at all. Every run it started sees
// the same cancellation, so a readiness read taken while it is unwinding reports
// the stop rather than the machine — and a session's last recorded line must not
// be an invented account of why it could not work.
func refusedByTheMachine(ctx context.Context, environment ScheduleEnvironment, done completed) (string, bool) {
	if done.err == nil || ctx.Err() != nil || strings.TrimSpace(done.outcome.RunID) != "" {
		return "", false
	}
	var refused EnvironmentRefusedError
	if errors.As(done.err, &refused) {
		return blockedReason(done.err), true
	}
	if _, environmental := environmentalCauseOf(done.err); environmental {
		return blockedReason(done.err), true
	}
	if environment == nil {
		return "", false
	}
	err := environment.ValidateReady(ctx)
	if err == nil {
		return "", false
	}
	return blockedReason(err), true
}

// blocked is what the machine refuses every run for, in the words the operator
// will read, and the empty string where it refuses nothing. A readiness that
// cannot be established at all is itself a refusal to choose under: a machine
// nothing can say is ready is not one to start work on.
func (p Pull) blocked(ctx context.Context) string {
	if p.Environment == nil {
		return ""
	}
	if err := p.Environment.ValidateReady(ctx); err != nil {
		return blockedReason(err)
	}
	return ""
}

// blockedReason is what a condition that refuses every run says: what cannot
// happen, what is causing it, and, for a dirty checkout, the move that ends it.
// A message that stops at the diagnosis leaves the reader where the silence did.
//
// The dirty checkout has its own sentence because it is the one this was written
// for and because the file is the whole of the remedy. It is looked for first and
// through whatever wrapped it, so the sentence an operator acts on is the same
// whether the refusal was declared by a pipeline step or found by the readiness
// read. Everything else is carried in the words of whatever refused — the
// condition it named where it named one — which is more useful than a paraphrase
// and is the only honest thing to say about a refusal nothing here anticipated.
func blockedReason(err error) string {
	var dirty gitworktree.PrimaryDirtyError
	if errors.As(err, &dirty) {
		return fmt.Sprintf("runs cannot start: uncommitted changes in the primary checkout (%s); commit or stash to release",
			singleLine(strings.Join(dirty.Paths, ", "), maxBlockedDetailBytes))
	}
	var refused EnvironmentRefusedError
	if errors.As(err, &refused) {
		// describe bounds the cause itself; bounding it again here is what would
		// spend the budget on the condition and cut the reason off the end.
		return "runs cannot start: " + refused.describe()
	}
	return "runs cannot start: " + singleLine(err.Error(), maxBlockedDetailBytes)
}

// usageWindowReset is when the provider's usage window that stopped a run
// resets, where that is what stopped it.
func usageWindowReset(outcome Outcome) (time.Time, bool) {
	if !stoppedByUsageWindow(outcome.Environmental) || outcome.Environmental.ResetsAt == nil {
		return time.Time{}, false
	}
	return outcome.Environmental.ResetsAt.UTC(), true
}

// unstartedAttempt reports a start that failed with no run behind it: the
// dispatch died before anything reserved a run, so the run store holds nothing
// about it and neither does anything built on the run store.
//
// It is the one failure in this loop that is invisible everywhere else. A run
// that fails after it is reserved has a record, and the sweep, the docket, the
// status surfaces and the stall alarm are all built on that record; this one has
// none, and until it was recorded the only trace it left was the item quietly
// dropping out of the session's own choosing.
func unstartedAttempt(started Started) bool {
	return started.Failure != "" && strings.TrimSpace(started.Outcome.RunID) == ""
}

// excludedBecause is what the exclusion this start made says about itself. It is
// derived once, as the start settles, rather than by whoever reads the exclusion
// later: the outcome is in hand here and nowhere afterwards.
func excludedBecause(started Started) string {
	if stoppedByUsageWindow(started.Outcome.Environmental) {
		return fmt.Sprintf("run %s was stopped by the provider's usage limit rather than by anything about the work, and the item was given back to the queue: %s",
			started.Outcome.RunID, started.Outcome.Environmental.ResetSays())
	}
	switch {
	case started.Declined != "":
		return "this session started it and the work went to another process: " + started.Declined
	case unstartedAttempt(started):
		return "this session tried it and the dispatch failed before any run was recorded: " + started.Failure
	case strings.TrimSpace(started.Outcome.RunID) == "":
		if started.Outcome.Paused {
			return "this session tried it and the dispatch stopped before a run was recorded; the work is paused and owed a continuation"
		}
		return "this session tried it and the dispatch returned without recording a run"
	case started.Failure != "":
		return fmt.Sprintf("run %s failed: %s", started.Outcome.RunID, started.Failure)
	case started.Outcome.Blocked:
		return fmt.Sprintf("run %s stopped on a durable blocker and its change is preserved", started.Outcome.RunID)
	case started.Outcome.Paused:
		return fmt.Sprintf("run %s is paused and owed a continuation", started.Outcome.RunID)
	default:
		return fmt.Sprintf("run %s ended %s", started.Outcome.RunID, started.Outcome.Status)
	}
}

// recordAttempt dockets one dispatch that never became a run, and reports what
// the exclusion behind it now says and what stopped the record where something
// did.
//
// The record is made where the failure happened because there is nowhere else it
// could be made from: no run was reserved, so no sweep will ever walk past this
// and re-derive it. A docket that refuses the write leaves the session's own
// account as the whole of what says the attempt happened, and says so rather
// than reading like a record that was made.
//
// What the exclusion says puts the record ahead of the failure, deliberately:
// the reason is cut to a line wherever it is read, and the failure is the half
// that is on the docket in full where the record was made — so where anything is
// lost to the cut it is the tail of something a reader can find, never the fact
// of whether they can.
func recordAttempt(docket ScheduleTriage, started Started, excluded attempt, watching bool) (string, string) {
	if docket == nil {
		return "this session tried it and the dispatch failed before any run was recorded; nothing was wired to record that durably, so this is the only account of it: " + started.Failure,
			fmt.Sprintf("the dispatch of %s failed before any run was recorded and nothing was wired to docket it, so nothing outside this session's log says it happened: %s",
				started.WorkItemID, started.Failure)
	}
	if _, err := docket.RecordUnstartedAttempt(UnstartedAttempt{
		WorkItemID:    started.WorkItemID,
		WorkItemTitle: excluded.title,
		// The selection this pass recorded, which is the reason that would have gone
		// onto the run record had one been written.
		SelectedBecause: started.Reason,
		Failure:         started.Failure,
		// A drain excludes it for the rest of a pass that is about to end anyway; a
		// watch excludes it until somebody edits the item, which is the state worth
		// putting in front of a person.
		ExcludedForTheSession: watching,
	}); err != nil {
		return "this session tried it and the dispatch failed before any run was recorded; it could not be recorded on the docket, so this is the only account of it: " + started.Failure,
			fmt.Sprintf("record that the dispatch of %s never became a run: %v", started.WorkItemID, err)
	}
	return "this session tried it and the dispatch failed before any run was recorded; it is on the development manager's docket as an attempt that never became a run: " + started.Failure, ""
}

// fingerprint is what "something about the item changed" means: every part of
// the item a person steers with — what the work says, what it is for, where it
// sits in the queue, what it waits on — and the notes, which is where the
// harness records what became of a run.
//
// The notes are the half that is not obvious, and they are what makes the
// ordinary recovery work. A run that stops on a blocker takes the item out of
// the ready queue and writes the blocker into its notes; a development manager
// who then unblocks it without editing anything leaves every other field exactly
// as this session first read it, and a fingerprint that ignored the notes would
// have the session refusing to pull work somebody had just deliberately
// released. Reading them costs the guard nothing, because the one case it exists
// for — a start that fails before a run claims anything — writes to the tracker
// not at all: the harness only ever appends to the notes of an item it has
// claimed, blocked, or closed, and none of those is an item still sitting
// pullable in the queue. A change that made the harness write to a pullable
// item's notes would put that spin back.
func fingerprint(item beads.WorkItem) string {
	parts := []string{
		item.Status,
		fmt.Sprint(item.Priority),
		item.Title,
		item.Description,
		item.Design,
		item.AcceptanceCriteria,
		item.Notes,
		item.Assignee,
		item.Parent,
	}
	// A dependency is named rather than counted, because one swapped for another
	// is a different piece of work with the same arithmetic.
	for _, dependency := range item.Dependencies {
		parts = append(parts, dependency.ID, dependency.Type)
	}
	return strings.Join(parts, "\x00")
}

// brake holds intake because runs kept blocking with nothing landing between
// them, and summons the development manager to decide what happens to it. What
// it places is the operator's own switch, which is deliberate: an operator
// arriving at a stopped line finds one thing to understand and one thing to
// lift, rather than a second mechanism that stops work in a way only this
// package knows how to undo.
// What it records as the reason is the cause alone — who placed it is the
// holder beside it — because every surface that prints a hold composes those
// two itself, and a reason that also named the holder is what stacked three
// accounts of one hold into a line nobody could read. The runs that blocked
// ride the hold's own record, which is what the summons puts in front of her.
func (s Scheduler) brake(ctx context.Context, schedule *Schedule, firings *recurringFirings, pull Pull, session *watchSession, running, blocked int, storm []runstate.BrakeBlockedRun) {
	reason := fmt.Sprintf("%d run(s) blocked in a row with nothing landing between them, which is the configured brake at %d",
		blocked, pull.BlockedRunsBeforeIntakeHold)
	if pull.Brake == nil {
		schedule.BrakeProblem = fmt.Sprintf(
			"runs kept blocking and nothing was wired to hold intake, so the line was left choosing work: %s", reason)
		return
	}
	at := s.now().UTC()
	trip := runstate.IntakeBrake{Blocked: storm, CooldownEndsAt: at.Add(pull.BrakeCooldown), CycleBound: pull.BrakeEscalationCycles}
	held, err := pull.Brake.Brake(trip, reason, at)
	if err != nil {
		schedule.BrakeProblem = fmt.Sprintf("intake could not be held after %d run(s) blocked in a row, so the line is still choosing work: %v", blocked, err)
		return
	}
	// Holding what is already held leaves the hold that was there, so this is
	// this session's brake only when this call is what placed it. A pass that
	// took an operator's standing hold for its own would report the line as
	// braked to a reader whose queue was stopped for an entirely different
	// reason — and would summon the development manager over a hold that is
	// the operator's to lift.
	if !held.HeldAt.Equal(at) || held.HeldBy != runstate.IntakeHolderBrake {
		return
	}
	schedule.Braked = &held
	// The trip is said before the summons is made, so the log carries the line
	// stopping — which is what a channel wakes somebody over — whether or not
	// her turn releases it a moment later.
	session.enter(runstate.WatchBraked, account{reason: brakedReason(held), running: running, mover: brakedMover(held)})
	s.summon(ctx, schedule, firings, pull.Brake, pull.Summons, held)
}

// summon puts the brake's hold in front of the development manager at once,
// and writes onto the hold whether it reached her. Nothing here waits on the
// answer: what she decides is written by her conversation onto the same
// record, and the next poll reads it there.
//
// A summons that could not be made is recorded on the hold and does not stop
// anything. The cooldown is what makes that safe: a hold she was never asked
// about is probed by the harness exactly as one she left undecided, so the line
// is released or kept on evidence either way, and what is lost is her judgment
// rather than the release.
//
// A summons is a turn in the development manager's conversation, so one that
// finds a recurring pass of hers still taking turns there is not made into it:
// the summons claims her task's firing before it asks her anything, and a turn
// her own pass holds the conversation for would be refused after that claim,
// spending it on a failed firing and leaving the hold to the cooldown's probe
// for nothing. It waits instead, and is made at the first pull after her pass
// ends while the brake's hold still stands; see summonWaiting.
func (s Scheduler) summon(ctx context.Context, schedule *Schedule, firings *recurringFirings, brake ScheduleBrake, summons ScheduleSummons, held runstate.IntakeHold) {
	if brake == nil {
		return
	}
	if summons != nil && firings != nil {
		if _, busy := firings.holding(roleConversationKey(domain.RoleDevelopmentManager)); busy {
			firings.summoning = &waitingSummons{brake: brake, summons: summons}
			return
		}
	}
	var problem string
	if summons == nil {
		problem = "nothing was wired to summon the development manager, so the hold is decided by the cooldown's probe rather than by her"
	} else {
		fired, err := summons.Summon(ctx, BrakeSummons{Hold: held})
		// What the summons cost is the session's spend, exactly as a firing's is
		// and for the same reason.
		schedule.SpentUSD += fired.CostUSD
		switch {
		case err != nil:
			problem = fmt.Sprintf("the development manager could not be summoned, so the hold is decided by the cooldown's probe rather than by her: %v", err)
		case fired.Turns == 0:
			problem = fmt.Sprintf("the summons did not reach the development manager, so the hold is decided by the cooldown's probe rather than by her: %s", fired.Problem)
			schedule.Fired = append(schedule.Fired, fired)
		default:
			schedule.Fired = append(schedule.Fired, fired)
		}
	}
	at := s.now().UTC()
	// A hold lifted while her turn was being taken — by her own decision, or by
	// `yoyo release` — has no record to write onto, and that is not a failure:
	// the summons did what it was for.
	if _, err := brake.ReviseBrake(func(trip *runstate.IntakeBrake) error {
		if problem != "" {
			trip.SummonProblem = runstate.BoundBrakeText(problem)
			return nil
		}
		trip.SummonedAt = &at
		trip.SummonProblem = ""
		return nil
	}); err != nil && !errors.Is(err, runstate.ErrNoBrakeHold) {
		problem = appendProblem(problem, fmt.Sprintf("and whether the development manager was summoned could not be recorded on the hold: %v", err))
	}
	if problem != "" {
		schedule.BrakeProblem = appendProblem(schedule.BrakeProblem, problem)
	}
}

// What one poll under the brake's hold does.
type brakeAction int

const (
	// brakeWaiting is the hold standing: the development manager deciding, a
	// probe in flight, or the hold escalated to the operator. The session waits
	// as it always did under a held intake.
	brakeWaiting brakeAction = iota
	// brakeReleased is the hold lifted by this poll, on her decision to release
	// it. The session chooses work again at once.
	brakeReleased
	// brakeProbing is a probe due: on her decision, or on the cooldown running
	// out with none. The poll chooses one item and starts it under the hold.
	brakeProbing
)

// workBrake reads what the brake's own record says to do about its hold on
// this poll. The operator's hold, and a brake hold from before the brake
// worked its own holds, are waited on: nothing here decides about a hold the
// harness did not place, and nothing here revises a record it did not write.
//
// A probe another process recorded in flight and no process is running is a
// session that died holding it. It is settled here as a probe that ended
// without a record, which restarts the cooldown rather than starting a second
// probe on the spot: the ending is unknown, and an unknown ending is not a
// landing.
func (s Scheduler) workBrake(schedule *Schedule, pull Pull, hold runstate.IntakeHold, mine map[string]int) brakeAction {
	if !hold.Braked() || pull.Brake == nil {
		return brakeWaiting
	}
	trip := *hold.Brake
	now := s.now().UTC()
	// Her release is read ahead of an escalation because the two can stand
	// together: a hold the harness escalated at the bound is still hers to
	// release if she finds the line is fine, and that release must not wait on
	// the operator the harness handed it to.
	switch {
	case trip.Decision == runstate.BrakeDecisionRelease:
		by := "the harness, on the development manager's decision to release it"
		if decidedBy := strings.TrimSpace(trip.DecidedBy); decidedBy != "" {
			by += " (" + decidedBy + ")"
		}
		if _, released, err := pull.Brake.ReleaseBrake(by, now); err != nil {
			schedule.BrakeProblem = appendProblem(schedule.BrakeProblem, fmt.Sprintf(
				"the development manager decided to release the brake's hold and it could not be lifted: %v", err))
			return brakeWaiting
		} else if released {
			schedule.Released = append(schedule.Released, BrakeRelease{
				At:     now,
				Reason: "the development manager decided to release it: " + singleLine(trip.DecisionReason, maxScheduleReasonBytes),
			})
		}
		return brakeReleased
	case trip.Escalated():
		return brakeWaiting
	case trip.Probing():
		if _, ours := mine[trip.Probe.WorkItemID]; ours {
			return brakeWaiting
		}
		occupied, err := occupiedItems(pull.Runs)
		if err != nil {
			return brakeWaiting
		}
		if _, running := occupied[trip.Probe.WorkItemID]; running {
			return brakeWaiting
		}
		s.settleLostProbe(schedule, pull, now)
		return brakeWaiting
	case trip.ProbeDue(now):
		return brakeProbing
	default:
		return brakeWaiting
	}
}

// settleLostProbe closes a probe whose session died before its ending was
// written: the record says a probe is in flight, and nothing is running it.
func (s Scheduler) settleLostProbe(schedule *Schedule, pull Pull, now time.Time) {
	if _, err := pull.Brake.ReviseBrake(func(trip *runstate.IntakeBrake) error {
		if trip.Probe == nil || !trip.Probe.InFlight() {
			return nil
		}
		ended := now
		trip.Probe.EndedAt = &ended
		trip.Probe.Reason = "its ending was never recorded, and no run of it is in flight; the session that started it died holding it"
		trip.CooldownEndsAt = now.Add(pull.BrakeCooldown)
		return nil
	}); err != nil {
		schedule.BrakeProblem = appendProblem(schedule.BrakeProblem, fmt.Sprintf(
			"a probe nothing is running could not be settled on the hold: %v", err))
	}
}

// recordProbe names the item about to be started as the brake's probe on the
// hold's own record, and reports whether it was recorded. The record is what
// the pipeline reads to let the run through the hold, so a record that will
// not take it is a probe that does not start.
func (s Scheduler) recordProbe(schedule *Schedule, pull Pull, workItemID string) bool {
	now := s.now().UTC()
	if _, err := pull.Brake.ReviseBrake(func(trip *runstate.IntakeBrake) error {
		trip.Probe = &runstate.IntakeProbe{WorkItemID: workItemID, StartedAt: now}
		trip.Probes++
		// A decision on a probe is carried out by this, and cleared so the next
		// poll does not read it as a second probe owed.
		if trip.Decision == runstate.BrakeDecisionProbe {
			trip.Decision = ""
			trip.DecidedAt = nil
		}
		return nil
	}); err != nil {
		schedule.BrakeProblem = appendProblem(schedule.BrakeProblem, fmt.Sprintf(
			"the probe run of %s could not be recorded on the hold, so it was not started: %v", workItemID, err))
		return false
	}
	return true
}

// recordNoProbe says on the hold that a probe was due and nothing was startable,
// and restarts the cooldown so the question is asked again once per cooldown.
func (s Scheduler) recordNoProbe(schedule *Schedule, pull Pull, found string) {
	now := s.now().UTC()
	if _, err := pull.Brake.ReviseBrake(func(trip *runstate.IntakeBrake) error {
		if trip.Decision == runstate.BrakeDecisionProbe {
			trip.Decision = ""
			trip.DecidedAt = nil
		}
		trip.CooldownEndsAt = now.Add(pull.BrakeCooldown)
		return nil
	}); err != nil {
		schedule.BrakeProblem = appendProblem(schedule.BrakeProblem, fmt.Sprintf(
			"a probe was due and nothing was startable, and the cooldown could not be restarted on the hold: %v", err))
	}
	schedule.BrakeProblem = appendProblem(schedule.BrakeProblem, fmt.Sprintf(
		"a probe run was due and nothing was startable under the hold, so none was made and the cooldown was restarted: %s", found))
}

// settleProbe decides the hold on what became of the probe. A landing releases
// it: the line is fine. A blocking keeps it, restarts the cooldown, and puts the
// question to the development manager again with the probe's own stoppage in
// front of her — so a machine that is still broken is probed once per cooldown
// and decided by her each time, rather than by nobody. Every other ending — the
// work going to another process, the run parked on the provider, the
// environment stopping it — is a verdict on nothing, and leaves the cooldown to
// ask again.
//
// The blocking is also the one place the loop is counted, because a blocked
// probe is what makes a cycle: it is what summons her again and starts another
// cooldown. The cycle that reaches the bound is not summoned over. The harness
// escalates the hold to the operator instead, on its own record, and the sink
// says so once to them directly — a loop that costs one of her turns and one
// run per cooldown must not go round all night on a machine that stays broken,
// with nothing getting louder because the one role who could escalate it has
// not.
func (s Scheduler) settleProbe(ctx context.Context, schedule *Schedule, firings *recurringFirings, brake ScheduleBrake, summons ScheduleSummons, cooldown time.Duration, cycleBound int, started Started) {
	if brake == nil {
		return
	}
	now := s.now().UTC()
	// revise rewrites the probe on the hold's record and reports what stopped
	// it, except a hold lifted while the probe ran — by the development
	// manager's decision, or by `yoyo release` — which has no record to write
	// onto and is not a failure: the probe was a run like any other, and the
	// hold it was for is gone.
	revise := func(what string, change func(*runstate.IntakeBrake)) (runstate.IntakeHold, bool) {
		revised, err := brake.ReviseBrake(func(trip *runstate.IntakeBrake) error {
			if trip.Probe == nil {
				trip.Probe = &runstate.IntakeProbe{WorkItemID: started.WorkItemID, StartedAt: now}
			}
			change(trip)
			return nil
		})
		switch {
		case errors.Is(err, runstate.ErrNoBrakeHold):
			return revised, false
		case err != nil:
			schedule.BrakeProblem = appendProblem(schedule.BrakeProblem, fmt.Sprintf("%s could not be recorded on the hold: %v", what, err))
			return revised, false
		}
		return revised, true
	}
	switch {
	case started.Declined != "":
		// Another process took the item, so the probe never ran. The record is
		// cleared rather than settled, and the next poll makes another.
		revise("the declined probe", func(trip *runstate.IntakeBrake) { trip.Probe = nil })
	case started.landed():
		revise("the landed probe", func(trip *runstate.IntakeBrake) {
			trip.Probe.EndedAt = &now
			trip.Probe.RunID = started.Outcome.RunID
			trip.Probe.Landed = true
		})
		if _, released, err := brake.ReleaseBrake(fmt.Sprintf("the harness, because the probe run%s of %s landed", namedRun(started.Outcome.RunID), started.WorkItemID), now); err != nil {
			schedule.BrakeProblem = appendProblem(schedule.BrakeProblem, fmt.Sprintf(
				"the probe run of %s landed and the brake's hold could not be lifted: %v", started.WorkItemID, err))
		} else if released {
			schedule.Released = append(schedule.Released, BrakeRelease{
				At:     now,
				Reason: fmt.Sprintf("the probe run%s of %s landed", namedRun(started.Outcome.RunID), started.WorkItemID),
			})
		}
	case started.blockedRun() && !started.environmental && !started.providerAway():
		revised, recorded := revise("the blocked probe", func(trip *runstate.IntakeBrake) {
			trip.Probe.EndedAt = &now
			trip.Probe.RunID = started.Outcome.RunID
			trip.Probe.Blocked = true
			trip.Probe.Reason = runstate.BoundBrakeText(started.blockedEntry().Reason)
			trip.Decision, trip.DecidedAt, trip.DecidedBy, trip.DecisionReason = "", nil, "", ""
			trip.SummonedAt, trip.SummonProblem = nil, ""
			trip.Cycles++
			// The bound is the pull's, re-read like the cooldown beside it, so an
			// operator who tightens it under a standing loop is heard at the next
			// probe rather than at the next trip.
			trip.CycleBound = cycleBound
			if trip.CycleBoundReached() {
				trip.Escalation = &runstate.BrakeEscalation{
					At:     now,
					Cycles: trip.Cycles,
					Probe:  started.WorkItemID,
					Reason: trip.Probe.Reason,
				}
				return
			}
			trip.CooldownEndsAt = now.Add(cooldown)
		})
		if !recorded {
			return
		}
		if revised.Brake != nil && revised.Brake.EscalatedByHarness() {
			schedule.BrakeEscalated = revised.Brake.Escalation
			return
		}
		s.summon(ctx, schedule, firings, brake, summons, revised)
	default:
		revise("the probe's ending", func(trip *runstate.IntakeBrake) {
			trip.Probe.EndedAt = &now
			trip.Probe.RunID = started.Outcome.RunID
			trip.Probe.Reason = runstate.BoundBrakeText("it ended neither landed nor blocked, which decides nothing about the line: " + started.ending())
			if trip.Decision == runstate.BrakeDecisionProbe {
				trip.Decision = ""
				trip.DecidedAt = nil
			}
			trip.CooldownEndsAt = now.Add(cooldown)
		})
	}
}

// namedRun is " <run id>" where the run recorded one and nothing otherwise, so
// a sentence about a run the record does not name does not carry a hole.
func namedRun(runID string) string {
	if strings.TrimSpace(runID) == "" {
		return ""
	}
	return " " + strings.TrimSpace(runID)
}

// BrakeRelease is one release of the brake's own hold this session made, and
// why.
type BrakeRelease struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// landed reports a run whose work reached the target branch, which is the one
// ending of a probe that says the line is fine.
func (s Started) landed() bool {
	return s.Failure == "" && s.Declined == "" && !s.Outcome.Blocked && !s.Outcome.Paused &&
		s.Outcome.Status == runstate.StatusSucceeded
}

// ending is one line saying how a run that neither landed nor blocked ended.
func (s Started) ending() string {
	switch {
	case s.Failure != "":
		return s.Failure
	case s.Outcome.Paused:
		return "the run is paused and owed a continuation"
	default:
		return fmt.Sprintf("the run ended %s", s.Outcome.Status)
	}
}

// blockedEntry is this run as the brake's record names it: the run, the item,
// and the reason it blocked in the run's own words.
func (s Started) blockedEntry() runstate.BrakeBlockedRun {
	reason := s.Failure
	if strings.TrimSpace(reason) == "" {
		reason = s.Outcome.Failure
	}
	if strings.TrimSpace(reason) == "" && s.Outcome.ReviewDecision != "" {
		reason = fmt.Sprintf("independent review ended %s: %s", s.Outcome.ReviewDecision, s.Outcome.ReviewSummary)
	}
	if strings.TrimSpace(reason) == "" {
		reason = "stopped on a durable blocker the run recorded on the item"
	}
	return runstate.BrakeBlockedRun{
		RunID:      strings.TrimSpace(s.Outcome.RunID),
		WorkItemID: s.WorkItemID,
		Reason:     runstate.BoundBrakeText(reason),
	}
}

// probeReason is the recorded reason the brake's probe exists: that it is the
// probe, whose hold it runs under, and the selection the queue would have made
// anyway, so the run's own account says why this item and not another.
func probeReason(hold runstate.IntakeHold, selected string) string {
	return fmt.Sprintf("the intake brake's probe run: intake is held since %s (%s), and this is the one run started under it to find out whether the line is fine — a landing reopens intake, a blocking keeps it held. %s",
		hold.HeldAt.UTC().Format(time.RFC3339), hold.Says(), selected)
}

// providerAway reads whether the provider is answering nobody, and reports the
// outage where one stands. A pull wired without the record reads none, and a
// record that cannot be read is said on the schedule and read past: a session
// that stopped choosing work because it could not open one file would be a
// worse failure than dispatching into a refusal.
//
// The two causes are asked about differently, because what ends them is
// different. A login is asked about cheaply — the provider's own availability
// check reads its local record of being signed in — so while that stands the
// session asks at every pull and clears the outage the moment the answer is
// yes. Nothing cheaper than an invocation says whether the network is back, so
// a provider nobody can reach is left the probe interval and then pulled into
// again: the dispatch is the probe, and a run it starts waits on its own if the
// answer is still no.
func (s Scheduler) providerAway(ctx context.Context, schedule *Schedule, pull Pull) (runstate.ProviderOutage, bool) {
	if pull.Outages == nil {
		return runstate.ProviderOutage{}, false
	}
	outage, standing, err := pull.Outages.Standing()
	if err != nil {
		schedule.OutageProblem = fmt.Sprintf("whether the provider is answering could not be read, so the pull was made as though it were: %v", err)
		return runstate.ProviderOutage{}, false
	}
	if !standing {
		schedule.ProviderOutage = nil
		return runstate.ProviderOutage{}, false
	}
	if outage.Cause == domain.ProviderUnauthenticated && pull.Provider != nil {
		availability, err := pull.Provider.CheckAvailability(ctx)
		if err == nil && availability.Installed && availability.Authenticated {
			if _, _, err := pull.Outages.Clear(); err != nil {
				schedule.OutageProblem = fmt.Sprintf("the provider is logged in again and the outage could not be cleared: %v", err)
			}
			schedule.ProviderOutage = nil
			return runstate.ProviderOutage{}, false
		}
		schedule.ProviderOutage = &outage
		return outage, true
	}
	// A provider nobody can reach, or a login nothing here can ask about: the
	// pull is the probe, once the interval has passed since the provider was
	// last met refusing.
	if !s.now().Before(outage.LastSeen.Add(pull.OutageProbe)) {
		schedule.ProviderOutage = nil
		return runstate.ProviderOutage{}, false
	}
	schedule.ProviderOutage = &outage
	return outage, true
}

// recordedWindow is a usage window the durable record says is closed over
// every developer model: the window itself, and what the session says it found.
type recordedWindow struct {
	window providerWindow
	found  string
}

// usageWindow reads whether a recorded usage limit with a reset still to come
// covers the model every developer's turn ends on, and reports the window where
// one stands.
//
// It is the same reading the capacity hold takes of the same two records — the
// usage-limit log and the runs parked on a limit — narrowed to the developer's
// endpoints, and narrowed to refusals the provider named a reset for: a limit
// with no reset is the unknown-reset probe's business, and a dispatch is how
// that one is asked about. An endpoint whose turn can end on a model the record
// does not refuse is one a run could be served on, so a single such endpoint
// holds nothing.
//
// A pull wired without the log or the endpoints reads none, and a record that
// cannot be read is said on the schedule and read past, for the reason the
// outage reading gives: a session that stopped choosing work because it could
// not open one file would be a worse failure than dispatching into a refusal.
func (s Scheduler) usageWindow(schedule *Schedule, pull Pull) (recordedWindow, bool) {
	schedule.UsageWindowResetsAt = nil
	if pull.UsageLimits == nil || len(pull.Developers) == 0 {
		return recordedWindow{}, false
	}
	refusals, err := pull.UsageLimits.List()
	if err != nil {
		schedule.UsageWindowProblem = fmt.Sprintf("whether a provider usage limit is reached could not be read, so work was picked as though none were: %v", err)
		return recordedWindow{}, false
	}
	var runs []runstate.State
	if pull.Runs != nil {
		if incomplete, err := pull.Runs.Incomplete(); err == nil {
			runs = incomplete
		}
	}
	// Only refusals of a model a developer's turn can end on, or of no model
	// named, are read: the reset said is the latest of these, and a refusal of
	// some other role's model would otherwise lend the window its later reset.
	developerModels := map[string]bool{}
	for _, endpoint := range pull.Developers {
		developerModels[strings.TrimSpace(endpoint.Model)] = true
		developerModels[strings.TrimSpace(endpoint.Alternate)] = true
	}
	relevant := func(all []runstate.UsageLimitExhaustion) []runstate.UsageLimitExhaustion {
		var kept []runstate.UsageLimitExhaustion
		for _, refusal := range all {
			if model := strings.TrimSpace(refusal.Model); model == "" || developerModels[model] {
				kept = append(kept, refusal)
			}
		}
		return kept
	}
	parked := relevant(readmodel.ParkedRunRefusals(runs))
	now := s.now()
	// The refusals are read against the same evidence every surface reads them
	// against, so intake is never held on a window a served turn has disproved.
	// Evidence that could not be read clears nothing, and holding on is the
	// direction that costs only time.
	evidence, _ := readmodel.ReadCapacityEvidence(pull.CapacityServed, pull.Conversations)
	// No unknown-reset pause: only a reset the provider named makes a refusal
	// stand here.
	hold := readmodel.ReadCapacityHold(pull.Developers, nil, append(parked, relevant(refusals)...), now, 0, evidence)
	if !hold.Holding || hold.ResetsAt.IsZero() || !now.Before(hold.ResetsAt) {
		return recordedWindow{}, false
	}
	resetsAt := hold.ResetsAt.UTC()
	schedule.UsageWindowResetsAt = &resetsAt
	limit := "a usage limit"
	if hold.Kind != "" {
		limit = "the " + hold.Kind + " usage limit"
	}
	models := hold.Models
	if len(hold.Alternates) > 0 {
		models = hold.Alternates
	}
	return recordedWindow{
		window: providerWindow{waiting: true, resetsAt: resetsAt},
		found: fmt.Sprintf("the usage-limit record holds %s closed on %s until %s, which every developer turn ends on, so nothing is chosen until it lifts",
			limit, strings.Join(models, " and "), resetsAt.Format(time.RFC3339)),
	}, true
}

// escalate puts the oldest stopped run the development manager has not been
// shown in front of her, and records what came back on the schedule.
//
// Nothing here stops the pass. A delivery that failed costs the pass nothing it
// was doing — no work was chosen by it and none is withheld for it — so it is
// reported beside the pull rather than in place of it, exactly as a staleness
// reading or a brake that could not be placed is. What must not happen is
// silence: a stoppage that reached nobody is the state this exists to end.
//
// What it does cost is the pull's own thread while the turn is taken, which is
// the one thing worth knowing before reading further: runs already going are
// untouched, but one that finishes mid-delivery is collected when the delivery
// returns rather than at once, so a free slot is refilled a turn later than it
// would have been. That is bounded by the turn and by one delivery per pass, and
// it is the trade the placement was chosen for — a run that delivered its own
// stoppage would hold a developer slot instead, which is the same wait taken out
// of the scarcer thing.
func (s Scheduler) escalate(ctx context.Context, schedule *Schedule, pull Pull) {
	if pull.Escalations == nil {
		return
	}
	sweep, err := pull.Escalations.Escalate(ctx)
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("stopped work could not be put to the development manager, so it is waiting on somebody carrying it to her: %v", err))
	}
	delivered := false
	for _, escalated := range sweep.Escalated {
		// What the delivery cost is the session's spend, exactly as a run's is. It
		// is counted whichever way the turn went and before anything else is
		// decided about it, because the provider charged for it either way — and a
		// session bounded by a budget that spent past it on turns nothing counted
		// would be the operator's cap disappearing quietly, which is the one thing
		// a bound must not do. The bound itself is read at the top of the next
		// pull, like every other spend this session makes.
		schedule.SpentUSD += escalated.CostUSD
		// A delivery that happened is kept, because it is one of the things this
		// pass did and there are as many of them as there were stoppages. One that
		// did not happen is a problem rather than an event, and the problems are
		// this sweep's rather than every sweep's: a session polling all night
		// against a conversation nothing can open would otherwise report the same
		// failure a thousand times.
		if escalated.Delivered {
			schedule.Escalated = append(schedule.Escalated, escalated)
			delivered = true
		}
		if escalated.Problem != "" {
			problems = append(problems, escalated.Problem)
		}
	}
	// What the pass says is replaced by what this sweep found, and cleared only by
	// a delivery that actually happened. A sweep that found nothing to say is not
	// evidence that the failure before it was resolved — the stoppage may simply
	// be waiting out its retry delay — and a pass that erased its own account of
	// having failed to reach her would end reporting nothing at all about stopped
	// work, which is the silence this exists to end. A failure that is still
	// happening is restated by the sweep that meets it again, so what stands at
	// the end of a pass is what is still true.
	switch {
	case len(problems) > 0:
		schedule.EscalationProblem = strings.Join(problems, "; ")
	case delivered:
		schedule.EscalationProblem = ""
	}
}

// fire wakes whichever recurring task is due, and records what came back on the
// schedule.
//
// Nothing here stops the pass, for the reason the escalation beside it does not:
// a firing that failed costs the pass nothing it was doing, so it is reported
// beside the pull rather than in place of it. What it costs is the pull's own
// thread while the turns are taken, bounded by the task's turn bound and by one
// firing per pass — the same trade the delivery above was placed for, and made
// once for both.
//
// What it returns is what this pass's firing says would keep a due task from
// firing — the schedule failing, the operator's pause, or another task taking
// the pass's one firing — and empty where nothing did. It is what a missed
// cadence found at a later pass is attributed to.
func (s Scheduler) fire(ctx context.Context, schedule *Schedule, pull Pull) recurringHold {
	if pull.Recurring == nil {
		return recurringHold{}
	}
	sweep, err := pull.Recurring.Fire(ctx)
	var problems []string
	held := recurringHold{at: s.now()}
	if err != nil {
		problems = append(problems, fmt.Sprintf("the recurring schedule could not be fired, so standing work is waiting on somebody starting it: %v", err))
		held.why = fmt.Sprintf("the harness could not fire its recurring schedule: %v", err)
	}
	if sweep.Paused != nil {
		held.why = fmt.Sprintf("the operator paused harness activity at %s", sweep.Paused.HeldAt.UTC().Format(time.RFC3339))
		held.quiet = true
	}
	fired := false
	for _, task := range sweep.Fired {
		switch {
		case held.why != "":
		case task.Turns == 0 && strings.TrimSpace(task.Problem) != "":
			// A firing that reached nobody — the provider out of capacity or
			// answering nobody, the role's conversation held — says why in the
			// words the refusal came with, the provider's reset among them. It is
			// the provider's or the lease's rather than the harness's own, so a
			// miss it leads to is said as a warning; the refusal itself has its own
			// notice already.
			held.why = strings.TrimSpace(task.Problem)
			held.refused = true
		default:
			held.why = fmt.Sprintf("the pass took its one firing for the recurring task %s", task.Task)
			held.fired = task.Task
		}
		// What the firing cost is the session's spend, exactly as a delivery's is
		// and for the same reason: the provider charged for the turns either way,
		// and a session bounded by a budget must not spend past it on turns nothing
		// counted.
		schedule.SpentUSD += task.CostUSD
		if task.Turns > 0 {
			schedule.Fired = append(schedule.Fired, task)
			fired = true
		}
		if task.Problem != "" {
			problems = append(problems, task.Problem)
		}
	}
	// What the pass says is replaced by what this firing found, and cleared only
	// by a firing that actually took a turn — for the reason the escalation's
	// problem is kept the same way: a pass that fired nothing is not evidence that
	// the failure before it is resolved, since the task may simply not be due.
	switch {
	case len(problems) > 0:
		schedule.RecurringProblem = strings.Join(problems, "; ")
	case fired:
		schedule.RecurringProblem = ""
	}
	return held
}

// recurringHold is what kept a pass from firing a due task, and when the pass
// found it. Quiet marks the operator's own pause, which a missed cadence
// records and says to nobody: a stop somebody placed on purpose is not
// breakage. Refused marks a firing the provider or the role's lease turned
// away, which is said as a warning rather than as the harness's own breakage.
type recurringHold struct {
	why     string
	at      time.Time
	quiet   bool
	refused bool
	// fired is the task the pass's one firing went to, where that is the hold. It
	// kept every other due task and never the one it fired.
	fired string
}

// said is the severity a miss this hold kept is reported at.
func (h recurringHold) said() report.Severity {
	switch {
	case h.quiet:
		return ""
	case h.refused:
		return report.SeverityWarning
	default:
		return report.SeverityCritical
	}
}

// recurringWatch is what a pass knows about why a recurring task might not have
// fired when it fell due. The claim store knows only when each task last fired;
// what the session was doing instead is known here or nowhere, and a gap
// recorded without it is the twenty hours of 2026-09-13 again — a cadence that
// stopped, with a session live throughout and nothing saying why.
type recurringWatch struct {
	// opened is when this session began. A task that fell due before it did fell
	// due with no session of this one's running to fire it.
	opened time.Time
	// held is the most recent thing that kept the pass from firing a due task,
	// newest wins, and cleared by a pass that reached the schedule and found
	// nothing in its way.
	held recurringHold
	// missed is each task's due time already recorded as missed, keyed by the
	// task and the trigger that owed it, so one gap is recorded once however many
	// passes find it standing.
	missed map[string]time.Time
	// waiting is what kept each task that was due and not claimed at the last
	// pull that reached the schedule: its conversation taking another firing's
	// turns, or every firing the session takes at once in flight. It is replaced
	// at every such pull, so it says what is true now rather than what once was.
	waiting map[string]recurringHold
}

func (w *recurringWatch) hold(held recurringHold) {
	held.why = strings.TrimSpace(held.why)
	w.held = held
}

// provider adds what the session read of the provider — a usage window closed
// with its reset, or the provider answering nobody — to a firing the provider
// turned away, so a miss it leads to says when the wait lifts even where the
// refusal's own words did not. A hold of any other kind is left as it is: the
// session's reading is about developer turns, and it says nothing about a task
// the pass had some other reason not to fire.
func (w *recurringWatch) provider(said string) {
	said = strings.TrimSpace(said)
	if !w.held.refused || said == "" || strings.Contains(w.held.why, said) {
		return
	}
	w.held.why += "; the provider was still refusing when the session last read it: " + said
}

// nextFiring is how long a session waiting on its own runs waits before going
// back round to the schedule: until the soonest enabled task falls due, and
// never less than the pull's interval, so a task that is due and not firing —
// paused, or refused by a schedule that cannot be claimed — is looked at once an
// interval rather than spun on. It reports false for a pull with no schedule, or
// one that cannot say when it is due, and the session then waits on the run.
func (s Scheduler) nextFiring(ctx context.Context, pull Pull) (time.Duration, bool) {
	cadence, readable := pull.Recurring.(RecurringCadence)
	if !readable {
		return 0, false
	}
	dues, err := cadence.Cadence(ctx)
	if err != nil {
		// A schedule that cannot be read is one the pass has to reach to say so.
		return pull.Poll, true
	}
	if len(dues) == 0 {
		return 0, false
	}
	wake := time.Duration(0)
	for i, due := range dues {
		until := due.At.Sub(s.now())
		if i == 0 || until < wake {
			wake = until
		}
	}
	return max(wake, pull.Poll), true
}

// missed records every task that has gone a whole interval past the time it
// fell due without firing, once per gap, with what kept it.
//
// A whole interval is the threshold because anything shorter is the ordinary
// shape of a cadence: a task waits while its conversation is taking another
// pass's turns, or while every firing the session takes at once is in flight. A
// task that has gone a whole interval unfired is a firing that should have
// happened and did not, which is the thing the operator's own maintenance job
// found on 2026-09-14 and the harness never said.
//
// What kept it comes from OS sleep history, scheduler presence and the other
// passes recorded during the gap, alongside current holds this session found
// and the firing this session kept it behind. An operator's pause is still
// quiet. The previous firing's failure is not evidence about this gap.
// The harness holding its own schedule is breakage and is
// said at critical, which is what puts it in front of the operator; no session
// running is a warning, since whoever stopped the harness knows; and the
// operator's pause is recorded against the cadence and said to nobody.
func (s Scheduler) missed(ctx context.Context, schedule *Schedule, pull Pull, watch *recurringWatch) {
	cadence, readable := pull.Recurring.(RecurringCadence)
	if !readable {
		return
	}
	dues, err := cadence.Cadence(ctx)
	if err != nil {
		schedule.RecurringProblem = fmt.Sprintf("when the recurring tasks are due could not be read, so a missed scheduled run may go unrecorded: %v", err)
	}
	now := s.now()
	var problems []string
	for _, due := range dues {
		if due.At.IsZero() || due.Every <= 0 || now.Before(due.At.Add(due.Every)) {
			continue
		}
		if recorded, found := watch.missed[due.key()]; found && recorded.Equal(due.At) {
			continue
		}
		miss := RecurringMiss{Task: due.Task, Role: due.Role, Every: due.Every, Due: due.At, Trigger: due.Trigger, Instance: due.Instance, LastFired: due.LastFired, ScheduleNote: due.ScheduleNote}
		// A hold this session found at or after the task fell due is what kept it,
		// even where the session opened after that: a session that opened late and
		// then could not fire for hours was kept by that, not by the gap before it.
		//
		// Only such a hold. One found before the task fell due is the state of the
		// last pass before the gap rather than what caused it — the commonest being
		// the task's own firing, an interval earlier — and naming it would be a
		// confident wrong reason where the honest one is that nothing was recorded.
		// A pass's one firing is never what kept the task that took it.
		held := watch.held.why != "" && !watch.held.at.Before(due.At) && watch.held.fired != due.Task
		observed := readmodel.GapCause{}
		if reader, ok := cadence.(interface {
			MissCause(time.Time, time.Time, string) readmodel.GapCause
		}); ok {
			observed = reader.MissCause(due.At, now, due.Task)
		}
		// What kept this task behind another firing is the task's own, and it is
		// the harness holding its cadence, so it is said at critical.
		behind, waited := watch.waiting[due.Task]
		waited = waited && behind.why != "" && !behind.at.Before(due.At)
		switch {
		case held:
			miss.Why = watch.held.why
			miss.Severity = watch.held.said()
			if observed.Why != "" {
				miss.Why += "; " + observed.Why
			}
			if observed.Waiting && !watch.held.quiet {
				miss.Severity = report.SeverityCritical
			}
		case waited:
			miss.Why = behind.why
			miss.Severity = behind.said()
			if observed.Why != "" {
				miss.Why += "; " + observed.Why
			}
		case observed.Why != "":
			miss.Why = observed.Why
			miss.Severity = report.SeverityWarning
			if observed.Waiting {
				miss.Severity = report.SeverityCritical
			}
		case observed.Checked:
			miss.Why = "the watch session did not reach its schedule while the task was due; no machine sleep, harness downtime or wait behind another pass was established for this gap"
			miss.Severity = report.SeverityCritical
		case watch.opened.After(due.At):
			miss.Why = fmt.Sprintf("no watch session was running to fire it; this one opened at %s", watch.opened.UTC().Format(time.RFC3339))
			miss.Severity = report.SeverityWarning
		default:
			miss.Why = "the watch session did not reach its schedule while the task was due, and recorded nothing that kept it"
			miss.Severity = report.SeverityCritical
		}
		if observed.Problem != "" {
			miss.Why += "; machine observations incomplete: " + observed.Problem
		}
		// Marked before it is written, so a record that failed is said once on the
		// pass rather than attempted again at every poll of a gap still standing.
		watch.missed[due.key()] = due.At
		if err := cadence.Missed(ctx, miss); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		schedule.RecurringProblem = strings.Join(problems, "; ")
	}
}

// correct wakes whichever role is owed a correction, and records what came back
// on the schedule.
//
// Nothing here stops the pass, for the reason the firing and the delivery beside
// it do not: a wakeup that failed costs the pass nothing it was doing, so it is
// reported beside the pull rather than in place of it.
func (s Scheduler) correct(ctx context.Context, schedule *Schedule, pull Pull) {
	if pull.Corrections == nil {
		return
	}
	sweep, err := pull.Corrections.Correct(ctx)
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("a role whose tracker block was refused could not be woken to re-issue it, so those actions are waiting on somebody prompting it: %v", err))
	}
	woken := false
	for _, corrected := range sweep.Corrected {
		// What the wakeup cost is the session's spend, exactly as a firing's is and
		// for the same reason: the provider charged for the turn either way, and a
		// session bounded by a budget must not spend past it on turns nothing
		// counted.
		schedule.SpentUSD += corrected.CostUSD
		if corrected.Woken {
			schedule.Corrected = append(schedule.Corrected, corrected)
			woken = true
		}
		if corrected.Problem != "" {
			problems = append(problems, corrected.Problem)
		}
	}
	// What the pass says is replaced by what this sweep found, and cleared only by
	// a wakeup that actually took a turn — for the reason the two above are kept
	// the same way: a pass that woke nobody is not evidence that the failure before
	// it is resolved, since there may simply have been no refusal to wake for.
	switch {
	case len(problems) > 0:
		schedule.CorrectionProblem = strings.Join(problems, "; ")
	case woken:
		schedule.CorrectionProblem = ""
	}
}

// nextCarryOuts is the decisions of the development manager's this pull fires,
// and the ones it offered and passed over, by run, with why.
//
// As many as there are developer slots free for them — and as the session's
// --limit leaves, since each is a run started — and one where none is: a
// decision is attempted on every pull it could be, whatever its place in the
// docket. Until yoyodyne-ifd.428.39 a pull fired one, which put every decision
// but the oldest behind it however many slots stood empty; a decision attempted
// with no slot free is refused by the action's own capacity gate, and that
// refusal is written onto the item where the development manager reads it. The
// decisions left over once the slots are spent are not attempted, and what the
// pull says about each is handed to RecordUnattempted, which writes it onto the
// item once the decision has stood a poll interval.
//
// The oldest decision goes first, by when it was recorded, and the docket's own
// order — the order the stoppages were recorded in — between two recorded at
// one moment.
//
// A reading that failed is reported and starts nothing. That is the same
// direction every other optional part of a pull fails in: the queue's own work is
// untouched, and a decision fired on a record nobody could read would be the one
// thing worse than one that waits.
//
// An item this pass has already started is passed over, exactly as the queue scan
// passes one over. The reading behind the outstanding decisions is of the durable
// records, and a run does not appear in those until it reserves — several steps
// after the pass started it — so a pull that did not ask this would fire the same
// decision again on the very next pull and put two developers on one item.
//
// And a decision this session already attempted, which a gate shut for everything
// at once stopped, is passed over for as long as this pull can see that gate
// still shut. The record offers it on every pull on purpose, so that the first
// pull after the gate opens fires it; attempting every offer would append a
// started entry and rewrite the item's record once per poll interval for the
// whole length of a pause. The gates the pass can see are the three that stop
// everything — the intake hold, the operator's pause, and a full harness — which
// is exactly the set the record leaves unpaced. A gate it cannot see is attempted,
// because the alternative is a decision this session never fires. Such a
// decision was attempted, and its refusal is on the item, so it is not among
// the ones passed over.
//
// A decision about an item that ready work in the Lead Product Manager's order
// outranks is not fired here at all, and not passed over either: it is returned
// as pending, for the walk of the queue to fire at the point in the order its
// item's priority puts it. outranked says which decisions those are; nil says
// none are, which is every decision before yoyodyne-ifd.428.58. A decision about
// a stopped run whose change is still there is never one of them: it is fired
// here, marked Preserved, with the ready work it went ahead of in AheadOf.
func (s Scheduler) nextCarryOuts(schedule *Schedule, pull Pull, occupied map[string]runstate.State, mine map[string]int, waitingOn map[string]string, closed closedGates, free, remaining int, outranked func(CarryOutTask) (outrankedCarryOut, bool)) ([]CarryOutTask, map[string]string, []outrankedCarryOut) {
	if pull.CarryOut == nil {
		return nil, nil, nil
	}
	outstanding, err := pull.CarryOut.Outstanding()
	// Kept apart from what a gate said about the attempt this pass then makes.
	// Reading part of the record and firing what could be read are compatible, and
	// a pass that reported them in one line had the successful attempt erase the
	// account of the decision nothing could look at.
	schedule.CarryOutReadProblem = ""
	if err != nil {
		schedule.CarryOutReadProblem = fmt.Sprintf(
			"what the development manager has decided and the harness has not carried out could not be read in full, so a decision may be waiting that nothing here fired: %v", err)
	}
	// Oldest decision first. The docket's order is the order the stoppages were
	// recorded in, and a decision recorded late about an early stoppage is not
	// older than one recorded early about a later one; a decision nobody recorded
	// — the harness's own continuation of a check stage — keeps its place ahead.
	slices.SortStableFunc(outstanding, func(a, b CarryOutTask) int {
		return a.DecidedAt.Compare(b.DecidedAt)
	})
	slots := free
	if slots < 1 {
		slots = 1
	}
	var chosen []CarryOutTask
	var pending []outrankedCarryOut
	passed := make(map[string]string)
	taken := make(map[string]string)
	newSlots := make(map[string]bool)
	for _, task := range outstanding {
		if run, ours := taken[task.WorkItemID]; ours {
			passed[task.RunID] = fmt.Sprintf("this pull was already carrying out a decision about run %s of the same item, and one item is never given two runs at once", run)
			continue
		}
		state, busy := occupied[task.WorkItemID]
		reusesSlot := task.Recover && state.RunID == task.RunID && state.RepairDispatchPending()
		// A carry-out this session has already dispatched still owns the item,
		// including the gap before the pipeline acquires the run lease.
		_, ours := mine[task.WorkItemID]
		if busy && (!reusesSlot || ours) {
			passed[task.RunID] = "this session had already started a run of the item that had not yet reserved, and one item is never given two runs at once"
			continue
		}
		if gate, stopped := waitingOn[task.WorkItemID]; stopped && closed.stillShut(gate) {
			if task.Decision == DecisionContinueChecks {
				passed[task.RunID] = fmt.Sprintf("the continuation is still waiting on %s; lifting that gate lets the next pull attempt it", gate)
			}
			continue
		}
		if remaining > 0 && len(chosen) >= remaining {
			passed[task.RunID] = fmt.Sprintf("the session was bounded to %d more run(s) by its --limit, and %d decision(s) ahead of it on the docket took them", remaining, len(chosen))
			continue
		}
		if !reusesSlot && len(newSlots) >= slots {
			passed[task.RunID] = fmt.Sprintf("every developer slot this pull had was spent on %d decision(s) ahead of it on the docket", len(newSlots))
			continue
		}
		// An unserved continuation already holds its slot. Recovering it does
		// not compete with ready work for another one.
		if outranked != nil && !reusesSlot {
			held, outranks := outranked(task)
			if outranks {
				pending = append(pending, held)
				continue
			}
			if held.preserved {
				task.Preserved = true
				task.AheadOf = held.named
			}
		}
		chosen = append(chosen, task)
		if !reusesSlot {
			occupied[task.WorkItemID] = runstate.State{WorkItemID: task.WorkItemID}
			newSlots[task.WorkItemID] = true
		}
		taken[task.WorkItemID] = task.RunID
	}
	// The occupancy marked above is only for the choosing: the caller marks each
	// fired decision again as it starts it, and must see the rest as it was.
	for id := range newSlots {
		delete(occupied, id)
	}
	return chosen, passed, pending
}

// outrankedCarryOut is a decision held back from going ahead of the queue
// because ready work in the Lead Product Manager's order outranks its item: the
// decision, its item's priority, and the ready items ahead of it.
type outrankedCarryOut struct {
	task     CarryOutTask
	priority int
	ahead    []string
	// named is the same ready work by what it is, each title with its
	// identifier after it, for the sentences a person reads on the item.
	named []string
	// preserved says the stopped run's change is still there, so the decision is
	// held back by nothing in the order and ahead is the work it goes ahead of.
	preserved bool
}

// preservedWork answers whether a decision is about a stopped run whose branch
// or worktree is still there, asked of the repository through the same look the
// hold and the docket take. The runs are read once, and only where a decision
// asks. A pull wired with no record of its stopped runs, a run it cannot find,
// and a look that failed all answer no, which leaves the decision where the
// order puts it — the ordering it had before, rather than a slot taken ahead of
// the queue on a preservation nobody established.
func preservedWork(ctx context.Context, pull Pull, now func() time.Time) func(CarryOutTask) bool {
	var runs map[string]runstate.State
	read := false
	return func(task CarryOutTask) bool {
		if pull.Stoppages == nil {
			return false
		}
		if !read {
			read = true
			if recorded, err := pull.Stoppages.Recorded(); err == nil {
				runs = make(map[string]runstate.State, len(recorded))
				for _, run := range recorded {
					runs[run.RunID] = run
				}
			}
		}
		run, known := runs[task.RunID]
		if !known {
			return false
		}
		found := readmodel.Looking(ctx, pull.Remains, now)(run)
		return !found.Unknown && (found.BranchThere || found.WorktreeThere)
	}
}

// outranking is whether ready work in the order outranks the item a decision is
// about, read off the queue the pull chooses from. Work that is in flight, or is
// the decision's own item, outranks nothing: it is not what a slot would go to.
// An item the reading does not hold is outranked by nothing, because a decision
// held back on a priority nobody read is held on no reason at all.
func outranking(task CarryOutTask, read pulled, occupied map[string]runstate.State) (outrankedCarryOut, bool) {
	item, known := read.items[task.WorkItemID]
	if !known {
		return outrankedCarryOut{}, false
	}
	held := outrankedCarryOut{task: task, priority: item.Priority}
	for _, entry := range read.queue.Entries {
		if entry.Priority >= item.Priority {
			break
		}
		if !entry.Ready || entry.ID == task.WorkItemID {
			continue
		}
		if _, busy := occupied[entry.ID]; busy {
			continue
		}
		held.ahead = append(held.ahead, entry.ID)
		named := entry.ID
		if title := strings.TrimSpace(read.items[entry.ID].Title); title != "" {
			named = fmt.Sprintf("%s (%s)", title, entry.ID)
		}
		held.named = append(held.named, named)
	}
	return held, len(held.ahead) > 0
}

// reason is what the item's record says of a decision the walk never reached a
// slot for: the ready work that outranked it, and what fires it.
func (o outrankedCarryOut) reason() string {
	named := o.ahead
	if len(named) > readmodel.MaxPassedOverNamed {
		named = named[:readmodel.MaxPassedOverNamed]
	}
	listed := strings.Join(named, ", ")
	if further := len(o.ahead) - len(named); further > 0 {
		listed += fmt.Sprintf(", and %d further", further)
	}
	return fmt.Sprintf("its item is at priority %d and %s of higher priority in the Lead Product Manager's order stood ready (%s), so a free developer slot goes to that work first; a decision goes ahead of the queue only where nothing ready outranks its item, and this one takes the first slot left once the walk of the order reaches priority %d",
		o.priority, plural(len(o.ahead), "item", "items"), listed, o.priority)
}

// carryOutRearms fires the re-arms the development manager decided about
// publications, whether nothing ever asked the forge to merge them or the forge
// dropped the merge, and says on the pass what
// each came to. A failure to read is said beside the pass rather than stopping
// it, like every other account the carry-out keeps.
func (s Scheduler) carryOutRearms(ctx context.Context, schedule *Schedule, pull Pull, intakeHeld bool) {
	rearms, fires := pull.CarryOut.(ScheduleRearms)
	if pull.CarryOut == nil || !fires {
		return
	}
	carried, err := rearms.CarryRearms(ctx, intakeHeld)
	for _, attempt := range carried {
		if attempt.Carried {
			schedule.CarriedOut = append(schedule.CarriedOut, attempt)
		}
		problems := make([]string, 0, 2)
		if !attempt.Carried {
			problems = append(problems, attempt.Problem)
		}
		if attempt.RecordProblem != "" {
			problems = append(problems, attempt.RecordProblem)
		}
		if len(problems) > 0 {
			schedule.CarryOutProblem = joinProblem(schedule.CarryOutProblem, strings.Join(problems, "; "))
		}
	}
	if err != nil {
		schedule.CarryOutReadProblem = joinProblem(schedule.CarryOutReadProblem,
			fmt.Sprintf("the re-arms the development manager decided about publications could not be read in full, so one may be waiting that nothing here fired: %v", err))
	}
}

func (s Scheduler) carryOutNotes(ctx context.Context, schedule *Schedule, pull Pull) {
	schedule.CarryOutNoteProblem = ""
	notes, delivers := pull.CarryOut.(ScheduleCarryOutNotes)
	if !delivers {
		return
	}
	if err := notes.DeliverNotes(ctx); err != nil {
		schedule.CarryOutNoteProblem = fmt.Sprintf("pending carry-out notes remain for a later pull: %v", err)
	}
}

// joinProblem adds one account to a pass's line of them.
func joinProblem(line, problem string) string {
	if line == "" {
		return problem
	}
	return line + "; " + problem
}

// recordUnattempted writes onto the items every decision of hers standing a
// poll interval with no pass having attempted it, and says so on the pass. A
// failure to write is said beside the pass rather than stopping it, like every
// other account the carry-out keeps: it costs the pass nothing it was doing.
func (s Scheduler) recordUnattempted(ctx context.Context, schedule *Schedule, pull Pull, passed map[string]string) {
	if pull.CarryOut == nil {
		return
	}
	poll := pull.Poll
	if poll <= 0 {
		poll = time.Minute
	}
	written, err := pull.CarryOut.RecordUnattempted(ctx, poll, passed)
	schedule.CarryOutUnattempted = append(schedule.CarryOutUnattempted, written...)
	if err != nil {
		problem := fmt.Sprintf("whether every decision the development manager recorded has been attempted could not be read in full, so a decision may stand unattempted that nothing here wrote down: %v", err)
		if schedule.CarryOutReadProblem == "" {
			schedule.CarryOutReadProblem = problem
		} else {
			schedule.CarryOutReadProblem += "; " + problem
		}
	}
}

// closedGates is what this pull can see of the switches that stop everything at
// once, read before the carry-out is chosen. It decides nothing about whether a
// decision may fire — the action reads each switch again and refuses under it —
// and is only what says whether attempting a decision one of them already stopped
// would find the same switch still shut.
type closedGates struct {
	intake   bool
	pause    bool
	capacity bool
}

// stillShut reports the named gate being one this pull can see, and shut. A gate
// the pass cannot see reads as open, so the decision it stopped is attempted and
// the action answers — the direction that costs an attempt rather than a decision
// this session never fires.
func (c closedGates) stillShut(gate string) bool {
	switch gate {
	case runstate.TriageGateIntakeHold:
		return c.intake
	case runstate.TriageGateSpendingPause:
		return c.pause
	case runstate.TriageGateCapacity:
		return c.capacity
	default:
		return false
	}
}

// paused reports the operator's pause as this pull can see it. A pull with no way
// to read it reports it open, which is what lets a decision the pause stopped be
// attempted again rather than never; see closedGates.
func (p Pull) paused() (bool, error) {
	if p.Holds == nil {
		return false, nil
	}
	_, held, err := p.Holds.Held()
	if err != nil {
		return false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return held, nil
}

// carryingOutReason is what the started entry says about a run the harness fired
// from a recorded decision, before the action itself has said anything.
//
// It is replaced by the action's own reason the moment the run starts, and that
// is the one the record keeps: the run's selection reason cites the decision, who
// recorded it, in which conversation and on which turn, and none of that is this
// package's to assert. What this is for is the pass that never gets that far — a
// gate stopped it, or the process died — where a started entry with no reason at
// all would be the only thing in the report that could not say what it was doing.
func carryingOutReason(task CarryOutTask) string {
	if task.Decision == DecisionContinueChecks {
		return fmt.Sprintf("the check stage bound stopped run %s under load and the harness is continuing it at its checks", task.RunID)
	}
	if task.Decision == DecisionContinueStall {
		return fmt.Sprintf("the harness stopped the AI session of run %s itself, because it went silent or its total budget ran out, and is continuing the run itself, once, at the phase it stopped in", task.RunID)
	}
	reason := fmt.Sprintf("the development manager recorded a %q about the stoppage of run %s and the harness is carrying it out",
		task.Decision, task.RunID)
	if ahead := aheadOfQueue(task); ahead != "" {
		reason += ". " + ahead
	}
	return reason
}

// settleCarryOut takes one fired decision into the schedule and reports whether
// what came back is a run to settle like any other.
//
// An attempt a gate stopped is not. Nothing was reserved, nothing was claimed and
// nothing was spent, so it is recorded as a start that never became a run — which
// is what Declined already means — rather than as a run that failed: counting it
// toward the failure storm would have the brake hold intake because a decision was
// waiting on the intake hold, and pricing it would charge the session for a run
// that does not exist.
//
// The account is kept whichever way it went, and the pass-level problem is
// cleared only by an attempt that fired, for the reason the sweeps beside it keep
// theirs: a pass that found nothing to fire is not evidence that the gate that
// refused the last one has opened.
func (s Scheduler) settleCarryOut(schedule *Schedule, started *Started, carried CarriedOut) bool {
	problems := make([]string, 0, 2)
	if !carried.Carried {
		problems = append(problems, carried.Problem)
	}
	if carried.RecordProblem != "" {
		problems = append(problems, carried.RecordProblem)
	}
	switch {
	case len(problems) > 0:
		schedule.CarryOutProblem = strings.Join(problems, "; ")
	case carried.Carried:
		schedule.CarryOutProblem = ""
	}
	if !carried.Carried {
		if carried.Problem == "" {
			// A carry-out that neither fired nor said what stopped it is not a gate
			// this can report, and it must not be swallowed as one: what the action
			// returned is left to be recorded as the failure it is.
			return true
		}
		started.Declined = carried.Problem
		return false
	}
	schedule.CarriedOut = append(schedule.CarriedOut, carried)
	if reason := strings.TrimSpace(carried.Reason); reason != "" {
		started.Reason = reason
	}
	return true
}

// audit gives back the claims with nothing alive behind them, and records on the
// schedule what it freed and what it could not.
//
// Nothing here stops the pass, for the reason nothing in escalate does: an audit
// that failed costs the pass nothing it was doing, and a pass that stopped
// choosing work because it could not read a claim would be a worse failure than
// the stuck item it was looking for. What must not happen is silence, which is
// the whole of what this exists to end.
//
// What it does cost is the pull's own thread while the records are read — the
// runs and the log of what has already been given back, once per pass — and one
// tracker write per stuck item, which on the ordinary pass is none.
//
// It returns what the tracker holds as claimed and whether that reading was made,
// because the queue below needs the same listing: a pull makes it once, here,
// and a pull with no audit wired makes it there instead and pays nothing for this.
//
// A reading that fails here is reported rather than retried, which is the one
// place in this loop that is true of a reading. Every other one is something the
// pass cannot go on without; this is only the audit's, and routing it through the
// retry would make a held intake — which is answered from a switch and needs no
// tracker at all — stop being answerable on a machine whose tracker is down. The
// queue below still meets the same store and still rides it out, so a session in
// contention behaves exactly as it did.
func (s Scheduler) audit(ctx context.Context, schedule *Schedule, pull Pull) ([]beads.WorkItem, bool) {
	if pull.Claims == nil {
		return nil, false
	}
	claimed, err := pull.claimed(ctx)
	if err != nil {
		schedule.ClaimProblem = fmt.Sprintf("what the tracker holds as claimed could not be read, so an item whose run died stays claimed and nothing will pull it: %v", err)
		return nil, false
	}
	sweep, err := pull.Claims.Audit(ctx, claimed)
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("the claims the tracker holds could not be audited against the runs the harness has, so an item whose run died stays claimed and nothing will pull it: %v", err))
	}
	schedule.ReleasedClaims = append(schedule.ReleasedClaims, sweep.Released...)
	problems = append(problems, sweep.Problems...)
	// What the pass says is replaced by what this sweep found, and cleared by a
	// sweep that found nothing wrong. Unlike a stopped delivery there is no retry
	// delay here — every pull audits every claim — so on most passes a sweep with
	// no problems means the problem before it is gone. The one exception is a run
	// the audit settled whose release the tracker then refused: the settling dated
	// the run's record from the audit's own write, so the claim is not read as
	// dead again until the threshold has passed over that, and the problem line
	// goes quiet for that long before the release is tried again and said again.
	// The item is still claimed through it, so an empty line here is not proof
	// that every claim is pullable — only that this sweep found nothing it could
	// still act on.
	schedule.ClaimProblem = strings.Join(problems, "; ")
	return claimed, true
}

// land closes the conversation-carried entries whose landing is in the
// repository, records what it did on the schedule, and returns the queue with
// the closed entries taken out of it.
//
// Nothing here stops the pass, for the reason the audit above does not: a
// close that failed costs the pass nothing it was doing, so it is reported
// beside the pull rather than in place of it. What the pass says is replaced by
// what this sweep found, and cleared by a sweep that found nothing wrong —
// every pull reads every landing, so a sweep with no problems means the
// problem before it is gone or the item it was about is closed.
func (s Scheduler) land(ctx context.Context, schedule *Schedule, pull Pull, entries []backlog.Entry) []backlog.Entry {
	if pull.Landings == nil {
		return entries
	}
	sweep, err := pull.Landings.Settle(ctx, entries)
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("the landings of conversation-carried work could not be read, so an item whose design has landed stays open until somebody closes it: %v", err))
	}
	problems = append(problems, sweep.Problems...)
	schedule.LandingProblem = strings.Join(problems, "; ")
	if len(sweep.Landed) == 0 {
		return entries
	}
	schedule.Landed = append(schedule.Landed, sweep.Landed...)
	closed := make(map[string]bool, len(sweep.Landed))
	for _, landed := range sweep.Landed {
		closed[landed.WorkItemID] = true
	}
	remaining := make([]backlog.Entry, 0, len(entries))
	for _, entry := range entries {
		if !closed[entry.ID] {
			remaining = append(remaining, entry)
		}
	}
	return remaining
}

// priceRun is what one finished run cost, from the recorded evidence. A run that
// never got as far as a record is priced at nothing because there is nothing to
// price, which is the truth about a start that failed before it began; evidence
// that exists and cannot be read is reported rather than assumed to be free, and
// under a budget that report is what stops the session.
//
// A pass with nothing to price with reaches here only when nothing is bounded,
// because a budget without a way to measure it is refused at the pull. So the
// nil is silence about a pass nobody asked to bound rather than a bound quietly
// dropped.
func priceRun(spend ScheduleSpend, outcome Outcome) (float64, string) {
	if spend == nil || strings.TrimSpace(outcome.RunID) == "" {
		return 0, ""
	}
	price, err := spend.Price(outcome.WorkItemID)
	if err != nil {
		return 0, fmt.Sprintf("what run %s cost could not be read, so the session's spend is a floor rather than a total: %v", outcome.RunID, err)
	}
	for _, run := range price.Runs {
		if run.RunID != outcome.RunID {
			continue
		}
		if !run.Known() {
			return 0, fmt.Sprintf("run %s left no evidence to price, so the session's spend is a floor rather than a total: %s", run.RunID, run.Unknown)
		}
		return run.CostUSD, ""
	}
	return 0, fmt.Sprintf("run %s is not among the recorded runs of %s, so the session's spend is a floor rather than a total", outcome.RunID, outcome.WorkItemID)
}

// blockedRun reports a run that ended without getting its work anywhere: it
// failed outright, or it stopped on a durable blocker. Both are the storm the
// brake counts, and neither is a run somebody is owed a continuation of.
func (s Started) blockedRun() bool {
	return s.Failure != "" || s.Outcome.Blocked
}

// providerAway reports a start the provider turned away before any run was
// recorded, because nobody was logged into it or nobody could reach it. It is
// read from the failure the dispatch reported rather than from anything the
// scheduler remembers, so a pull that met it counts it the same whether or not
// an outage store was wired.
//
// A dispatch refused because the provider did not put the developer's launch
// settings in force is the same ending: the item is as startable as it was, and
// the record of the hold is what the next pull reads.
func (s Started) providerAway() bool {
	return s.awayCause != "" || s.settingsHeld
}

// idlePoll is what one pull found while it started nothing, in the product
// manager's own order: every item it met, and the class it met it in.
//
// It is this pull's reading rather than the session's memory, and that is the
// whole of what makes it worth recording. What it produces is read at the moment
// somebody is deciding whether the harness is working at all, so a count carried
// over from an earlier poll would be an account of a queue that has since
// changed.
//
// The classes, the grouping, and the words are the read model's rather than this
// package's. Two surfaces answering "why is nothing happening" from their own
// readings is the disagreement one derivation exists to prevent, and it cost a
// page on 2026-09-06: see internal/readmodel/passedover.go.
type idlePoll struct {
	passed []readmodel.PassedOverItem
}

// pass records one item this poll left where it was. The role is empty for every
// class but the conversation-carried one, which is the only class whose answer
// is a person rather than a wait.
func (p *idlePoll) pass(id string, class runstate.PassedOverClass, role domain.AgentRole) {
	p.passed = append(p.passed, readmodel.PassedOverItem{ID: id, Class: class, Role: role})
}

// passTried records one item this session has already started and will not start
// again, with what excluded it. It is one of the two classes that carry a
// reason, and both carry one because their cause is not somewhere a reader can go
// and look: every other exclusion names a state of the item, the queue, or the
// machine, and these name a run only this process remembers.
func (p *idlePoll) passTried(id, reason string) {
	p.passed = append(p.passed, readmodel.PassedOverItem{
		ID:     id,
		Class:  runstate.PassedOverAlreadyTried,
		Reason: reason,
	})
}

// passWindow records one item this session is holding until the provider's
// usage window that stopped its run resets. It carries its reason for the reason
// passTried does — only this process remembers the run — and it is a class of
// its own because nothing about the item was tried and found wanting.
func (p *idlePoll) passWindow(id, reason string) {
	p.passed = append(p.passed, readmodel.PassedOverItem{
		ID:     id,
		Class:  runstate.PassedOverWaitingOnUsageWindow,
		Reason: reason,
	})
}

// passedOver is what this poll left where it was, grouped as every reader of it
// reads it and counted against the queue it was read from. It is what the
// session records, and what the idle line and the stall alarm are both rendered
// from.
func (p idlePoll) passedOver(queue backlog.Queue) runstate.PassedOver {
	return readmodel.GroupPassedOver(p.passed, len(queue.Entries))
}

// plural counts something in words, so a line an operator reads says "1 run"
// rather than "1 run(s)". The parenthesised plural is what a status line looks
// like when nobody read it out loud.
func plural(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}

// conversationExecutedReason says that the work was admitted for something other
// than a developer run, and that this is not a wait. Every other deferral here
// ends when something clears — a directive resolved, a child closed, a run
// finished — and this one ends when somebody opens the conversation the item
// names, which is why it says so rather than reading like a queue that will move
// on its own.
func conversationExecutedReason(executor domain.WorkItemExecutor) string {
	return fmt.Sprintf("its executor is %q rather than a developer run, so nothing a run can do would carry it out; the item is done in the conversation it names, and it is passed over here rather than waiting for anything", executor)
}

// passedOverReason says why an unready entry is being named rather than counted,
// and says nothing for the entries that are counted. Four kinds are named: work
// no run can carry, work somebody parked, work somebody is holding, and work held
// by a step only a person can take. None of the four is a wait on anything the
// harness will finish, and the order between them is the order the queue's own
// account gives — an item in more than one is told the thing that would still
// hold once the others lifted.
func passedOverReason(entry backlog.Entry) (string, bool) {
	switch {
	case !entry.Executor.DeveloperRun():
		return conversationExecutedReason(entry.Executor), true
	case entry.Parking.Parked():
		return parkedReason(entry.Parking), true
	case entry.Awaiting != "" && !entry.AwaitingLanding:
		return heldReason(entry.Awaiting, entry.AwaitingCarryOut), true
	case entry.HumanGates.Holds():
		return entry.Hold(), true
	default:
		return "", false
	}
}

// unreadyClass is which class an unready entry is passed over in, and it makes
// the same distinction passedOverReason does and in the same order: work no run
// can carry, then work somebody parked, then work somebody is holding, then work
// held by a step only a person can take, then everything that is genuinely
// waiting for something. An item in more than one is told the thing that would
// still hold once the others lifted.
//
// A held item is one of two classes rather than one, because the two have
// different next movers: a stoppage nobody has decided about waits on the
// development manager, and a decision she recorded waits on the harness. The
// queue already knows which — the hold says so — and reporting both as one class
// is what made thirty-three carried-out-shaped items read as a decision backlog.
// A gated item is a third mover again — the person whose step it is — and is its
// own class for the same reason.
func unreadyClass(entry backlog.Entry) runstate.PassedOverClass {
	switch {
	case !entry.Executor.DeveloperRun():
		return runstate.PassedOverCarriedInConversation
	case entry.Parking.Parked():
		return runstate.PassedOverParked
	case entry.AwaitingCarryOut:
		return runstate.PassedOverAwaitingCarryOut
	case entry.Awaiting != "" && !entry.AwaitingLanding:
		return runstate.PassedOverAwaitingDecision
	case entry.HumanGates.Holds():
		return runstate.PassedOverWaitingOnAPerson
	default:
		// A child waiting on its parent's change to land is waiting on other work,
		// exactly as one waiting on a dependency is: it clears by itself.
		return runstate.PassedOverWaitingOnOtherWork
	}
}

// heldReason says that the item is somebody's to release, and what they have to
// decide or carry out. Like the parking above it is not a wait, and it is named
// against the item for the same reason: the count it would otherwise disappear
// into is work that becomes pullable on its own, and this never does.
//
// Naming it is the whole of what the status field could not do. A blocked status
// says one word about a stoppage nobody has decided and about work whose every
// blocker closed months ago, so a queue that reported both as unready reported
// nothing anybody could act on — which is how 41 items, two of them p0, went a
// morning without one line saying which of them were waiting on a person.
//
// It opens on which of the two waits this is, because that is what says who to
// go to: a stoppage nobody has decided about is the development manager's, and a
// decision she recorded is the harness's to act on. The words after it are the
// hold's own account, which says the same thing at length.
func heldReason(awaiting string, carryOut bool) string {
	held := "it is held for a decision nobody has made and this is not a wait for anything"
	if carryOut {
		held = "it is held for the carry-out of a decision already recorded and this is not a wait for anything"
	}
	return fmt.Sprintf("%s: %s. Until that is settled, it is passed over at every pull",
		held, singleLine(awaiting, maxScheduleReasonBytes))
}

// parkedReason says that the work was deliberately taken out of reach, and says
// what decided it. Like the executor above it is not a wait, and unlike every
// other deferral here nothing clears it but a person releasing the item.
//
// It says the queue depth outright, because that is the misreading it exists to
// end. Parking used to be expressed as the bottom of the order, which reads as
// "last" to everything that pulls — so a queue with nothing else left took it,
// and the run cost $34.38 to fail at work somebody had already decided to defer.
// A pull that reaches a parked item now says it will never take it, however
// little else there is.
func parkedReason(parking domain.WorkItemParking) string {
	return fmt.Sprintf("it is parked, so no pull selects it however far the queue drains and this is not a wait for anything: %s. Releasing it is the Lead Product Manager's, and until they do it is passed over at every pull",
		singleLine(parking.Reason(), maxScheduleReasonBytes))
}

// unreadyReason says that the item asks for something the tree does not have,
// and names each unmet prerequisite with the read that found it and who releases
// it. Like the parking and the hold above it is not a wait: the pinpoint half
// clears when the code lands, and the stated half never clears until somebody
// acts, so the reason says who.
//
// It is named against the item rather than counted, for the reason those two are:
// the count it would otherwise disappear into is work that becomes pullable on
// its own, and an item nothing in the tracker holds back and nothing in the tree
// serves is exactly the item a reader would otherwise expect to see start.
func unreadyReason(unmet []readiness.Unmet) string {
	return fmt.Sprintf("the tree does not meet what it asks for, so it is passed over at every pull rather than dispatched: %s",
		singleLine(readiness.Describe(unmet), maxScheduleReasonBytes))
}

// brakedReason says which brake stopped the line, because what an operator does
// about it depends entirely on which one it was. It reads that off the hold
// rather than off whether this session happens to have braked: a session that
// answered from its own state named the operator for a hold its own brake had
// placed, and named its brake for a hold the operator had placed before it
// tripped.
func brakedReason(hold runstate.IntakeHold) string {
	// What the surfaces reading this already say is that the session is choosing
	// nothing, so this adds the one thing they do not: what lifts the hold. The
	// operator's does not clear itself; the brake's says who is deciding it and
	// what the harness does if nobody does.
	return hold.Says() + ", and " + hold.Standing()
}

// brakedMover is whose move a braked poll is, where the hold's own record says.
// It is empty for the operator's hold and for a brake hold from before the
// brake worked its own holds, so the surfaces reading it fall back to what they
// said about a held intake before: that it is the operator's.
func brakedMover(hold runstate.IntakeHold) string {
	if !hold.Braked() {
		return ""
	}
	return hold.Whose()
}

// opening says what the session was started to do, which is the first thing its
// log holds and the only line a session that dies immediately will ever write.
func (s Scheduler) opening() string {
	if !s.Watching {
		return "draining what is ready"
	}
	opening := "watching the backlog until stopped"
	if s.Limit > 0 {
		opening += fmt.Sprintf(", stopping after %d run(s)", s.Limit)
	}
	if s.Budget > 0 {
		opening += fmt.Sprintf(", within a budget of $%.2f", s.Budget)
	}
	return opening
}

// sleep waits out one interval between readings of the queue.
func (s Scheduler) sleep(ctx context.Context, interval time.Duration) bool {
	if s.Sleep != nil {
		return s.Sleep(ctx, interval)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// interval is the tick a free slot is refilled on while a run of this session
// goes on beside it.
func (s Scheduler) interval(interval time.Duration) <-chan time.Time {
	if s.Interval != nil {
		return s.Interval(interval)
	}
	return time.After(interval)
}

func (s Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// session records what the pass is doing, once per change rather than once per
// poll. A session idling all night writes one line, which is what makes the log
// worth reading and what makes a line appearing in it mean something.
//
// A drain records nothing at all: it is a command somebody is waiting on the
// return of, and the schedule it returns is the account of it.
func (s Scheduler) session(schedule *Schedule) *watchSession {
	if !s.Watching || s.Sessions == nil {
		return &watchSession{}
	}
	return &watchSession{to: s.Sessions, now: s.now, schedule: schedule}
}

// account is what a session says about the state it is entering: why it is in
// it, how many developer runs it can see in flight, and the conversation that
// has to act before its answer changes where there is one.
//
// The last two are on the account rather than folded into the prose because
// something other than a person reads them. The reason is a sentence, and whose
// move follows an idle poll is a fact a channel closes its message on — derived
// from these fields rather than parsed back out of the words.
type account struct {
	reason  string
	running int
	// executor is the conversation the session is waiting on, and unreadable marks
	// the poll that chose nothing because the harness itself could not be read.
	// They are the two states whose next move is not an admission, and they are
	// carried as facts rather than left in the prose because the clause a channel
	// closes on is derived from them.
	executor   domain.WorkItemExecutor
	unreadable bool
	// window is the provider's usage window this poll was made inside, where the
	// session is inside one. It is a value rather than a pointer because an account
	// is compared with the one before it to decide whether anything is news, and a
	// pointer would make two identical accounts two different ones.
	window providerWindow
	// passedOver is the same account the reason states, in the classes every
	// reader of it reads rather than in prose. It travels so that a surface which
	// has to answer a question about the queue reads the answer this poll already
	// came to, rather than deriving a second one from the silence around it.
	passedOver runstate.PassedOver
	// mover is whose move a braked poll is, in the hold's own words, where the
	// hold carries them. It is empty everywhere else.
	mover string
	// drain is the session's drain as this account was said under it, filled in
	// by the session rather than the caller: a bound reached is news even where
	// nothing else about the poll changed.
	drain runstate.WatchDrain
}

// same reports two accounts as the same account, which is what makes a poll that
// found what the poll before it found no news. It is a method rather than the
// comparison the caller used to make because the classes carry a slice, and a
// slice is not something Go will compare for us.
func (a account) same(other account) bool {
	if a.reason != other.reason || a.running != other.running ||
		a.executor != other.executor || a.unreadable != other.unreadable ||
		a.window != other.window || a.mover != other.mover || a.drain != other.drain {
		return false
	}
	if a.passedOver.Admitted != other.passedOver.Admitted ||
		len(a.passedOver.Groups) != len(other.passedOver.Groups) {
		return false
	}
	for index, group := range a.passedOver.Groups {
		against := other.passedOver.Groups[index]
		if group.Class != against.Class || group.Role != against.Role ||
			group.Count != against.Count || !slices.Equal(group.Items, against.Items) {
			return false
		}
	}
	return true
}

// providerWindow is the provider refusing this session for want of capacity: that
// it is, and when the provider said it lifts.
//
// It is what a run brings back rather than anything this package asks for. A run
// that meets an exhausted limit writes the deadline into its own durable state
// and hands the session an outcome carrying it, so the session knows the window
// without making a provider call of its own — and the moment it knows is the
// moment it can say so.
type providerWindow struct {
	waiting  bool
	resetsAt time.Time
}

// standing reports a window that has not lifted yet. A window whose deadline has
// passed accounts for nothing: the session goes back to saying what it actually
// found, and the watchdog goes back to being able to catch a session that has
// stopped working.
func (w providerWindow) standing(now time.Time) bool {
	return w.waiting && (w.resetsAt.IsZero() || now.Before(w.resetsAt))
}

// windowFrom is the provider window a finished run came back inside, or none.
//
// Only an exhausted usage limit counts. A transient server overload lifts in
// seconds and is waited out inside the run, so a session that reported itself
// held by one would be describing a wait that was already over; and a run parked
// for an operator hold, a directive, or work it depends on is waiting on
// something the surfaces already say.
func windowFrom(outcome Outcome) providerWindow {
	// A run the window stopped rather than parked brought the same answer back,
	// and the item it gave up waits on exactly that reset.
	if resetsAt, windowed := usageWindowReset(outcome); windowed {
		return providerWindow{waiting: true, resetsAt: resetsAt}
	}
	if !outcome.Paused || outcome.PauseCause != runstate.PauseUsageLimit {
		return providerWindow{}
	}
	window := providerWindow{waiting: true}
	if outcome.UsageLimitResetsAt != nil {
		window.resetsAt = outcome.UsageLimitResetsAt.UTC()
	}
	return window
}

// windowReason is what the session says about a poll made inside a provider's
// usage window: the window first, because it is the whole of why nothing
// started, and what the poll otherwise found behind it, because the queue is
// still what will be pulled when the window lifts.
func windowReason(window providerWindow, found string) string {
	said := readmodel.ProviderWindow{Waiting: true, ResetsAt: window.resetsAt}.Says()
	if strings.TrimSpace(found) == "" {
		return said
	}
	return said + "; " + found
}

// drainRecheck is how long a session whose drain bound has run out waits before
// looking again at a hosted run that had not reached its claim when the bound
// was applied. Such a run is seconds from a phase it can be stopped at, so the
// look is cheap to repeat and expensive to make into a cancelled dispatch.
const drainRecheck = 5 * time.Second

// redeployDrain is the session's wait to restart into a build deployed over it:
// since when, under what bound, and whether the bound has run out.
//
// The bound is watched two ways, and both are needed. The timer is what ends a
// wait the session is inside — a collect blocked on a run in its second hour —
// and the clock is what catches a bound that ran out while the session was busy
// with something else, a recurring firing say, and only now got back to the top
// of its loop.
type redeployDrain struct {
	active       bool
	since        time.Time
	limit        time.Duration
	boundReached bool
	// promoting is how many hosted runs the last look past the bound left going
	// at their promotion, and promotingSince when the session began waiting on
	// them; see promoted.
	promoting      int
	promotingSince time.Time
	// due fires when the bound runs out. It is nil until the drain is armed with
	// a bound, and a nil channel is one a select never chooses.
	due   <-chan time.Time
	timer *time.Timer
}

// begin is the deploy having been found. The bound the last pull read is what
// the drain starts under; a session that finds the deploy before its first pull
// is armed by that pull instead.
func (d *redeployDrain) begin(now time.Time) {
	d.active = true
	d.since = now
	limit := d.limit
	d.limit = 0
	d.arm(limit, now)
}

// arm gives the drain its bound, or a new one where the configuration moved
// under it. Before the deploy is found it only remembers the bound the pull
// read. The timer runs from now for what is left of the bound rather than for
// the whole of it, so a bound read a poll after the deploy was found does not
// extend the wait by a poll.
func (d *redeployDrain) arm(limit time.Duration, now time.Time) {
	if limit <= 0 || limit == d.limit {
		return
	}
	d.limit = limit
	if !d.active {
		return
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.NewTimer(max(d.deadline().Sub(now), 0))
	d.due = d.timer.C
}

func (d redeployDrain) deadline() time.Time { return d.since.Add(d.limit) }

// reached marks the bound as having run out, however that was found.
func (d *redeployDrain) reached() { d.boundReached = true }

// promoted records how many hosted runs a look past the bound left going at
// their promotion. The wait is dated from the first look that found one and
// keeps that date while any remains, so a reader is told how long the restart
// has been waiting on a promotion rather than when it was last looked at.
func (d *redeployDrain) promoted(count int, now time.Time) {
	switch {
	case count == 0:
		d.promotingSince = time.Time{}
	case d.promoting == 0 || d.promotingSince.IsZero():
		d.promotingSince = now
	}
	d.promoting = count
}

// stop releases the timer, for a session ending however it ends.
func (d *redeployDrain) stop() {
	if d.timer != nil {
		d.timer.Stop()
	}
}

// expired reports the bound having run out: the timer fired, or the clock has
// passed the deadline. A drain not yet armed with a bound has not expired,
// whatever the clock says, because a bound of nothing is not a bound of zero.
func (d redeployDrain) expired(now time.Time) bool {
	if !d.active || d.limit <= 0 {
		return false
	}
	return d.boundReached || !now.Before(d.deadline())
}

// declinesStarts reports the drain being too close to its bound, or past it,
// for a pull into a free seat to be worth making — a run started now would only
// be stopped — and how far off the bound is, which is nothing once it has run
// out.
func (d redeployDrain) declinesStarts(poll time.Duration, now time.Time) (time.Duration, bool) {
	if !d.active || d.limit <= 0 {
		return 0, false
	}
	if d.boundReached {
		return 0, true
	}
	remaining := d.deadline().Sub(now)
	if remaining > poll {
		return remaining, false
	}
	return max(remaining, 0), true
}

// found is what the session says the moment it finds the deploy: what it will
// do, and under what bound.
func (d redeployDrain) found(hosting int) string {
	said := "a build was deployed over the one this session was started from; it restarts into it the moment it hosts no run"
	if recorded := d.record(hosting); recorded != nil {
		return said + ", and is " + recorded.Says()
	}
	return said + ", and is draining under the bound its next pull reads"
}

// record is the drain as the watch log carries it, against the runs the session
// is hosting at that moment. A drain not yet armed with a bound records the
// bound it will be given at the next pull as nothing, which the log refuses, so
// it is recorded only once armed; the transition before that carries the
// deploy in its reason.
func (d redeployDrain) record(hosting int) *runstate.WatchDrain {
	if !d.active || d.limit <= 0 {
		return nil
	}
	recorded := &runstate.WatchDrain{
		Since:        d.since,
		BoundSeconds: int64(d.limit / time.Second),
		Until:        d.deadline(),
		Hosting:      hosting,
		BoundReached: d.boundReached,
	}
	if d.boundReached && d.promoting > 0 {
		recorded.Promoting, recorded.PromotingSince = d.promoting, d.promotingSince
	}
	return recorded
}

// host starts one run under a context of its own, so the drain bound can stop
// it without stopping the session, and carries what became of it back to the
// scheduling goroutine.
func (s Scheduler) host(ctx context.Context, pull Pull, workItemID string, index int, selection runstate.Selection, hosted map[int]context.CancelCauseFunc, landings *hostedLandings, deliver func(completed)) {
	runCtx, cancel := context.WithCancelCause(ctx)
	runCtx = withLandingNotice(runCtx, func() { landings.begun(index) })
	hosted[index] = cancel
	go func() {
		outcome, err := pull.Start(runCtx, workItemID, selection)
		deliver(completed{index: index, outcome: outcome, err: err})
	}()
}

// liveHosted is how many of the session's runs are still live: started and
// not yet collected, and not stopped by the drain bound. stopHosted takes a run
// it stops out of hosted, so a run in mine and no longer in hosted is one the
// session stopped and is only waiting to hear back from.
func liveHosted(mine map[string]int, hosted map[int]context.CancelCauseFunc) int {
	live := 0
	for _, index := range mine {
		if _, hosting := hosted[index]; hosting {
			live++
		}
	}
	return live
}

// promotionResayEvery is how often a session past its drain bound says again
// that it is waiting out a run at its promotion. A promotion has no bound of
// its own — a forge outage can hold one for hours — and the session otherwise
// writes nothing while the wait is unchanged, so the readers of the watch log
// take the latest line's age as the evidence the session is still alive: one
// whose line is older than readmodel.DrainOverrunGrace is read as stuck. This
// is a third of that, so a session looking at least once a poll is not read as
// stuck while it waits.
const promotionResayEvery = 10 * time.Minute

// stoppedRunGrace is how long a session restarting past its drain bound waits
// for the runs it stopped to report back. A stopped run records its stop and
// returns within seconds — the slowest part, the commit a developer attempt
// is given of what it left, is bounded at redeployCommitTimeout — so this is
// a backstop against a run that never reports rather than a wait anybody
// ordinarily sees.
const stoppedRunGrace = 2 * time.Minute

func (s Scheduler) stopGrace() time.Duration {
	if s.stoppedRunGrace > 0 {
		return s.stoppedRunGrace
	}
	return stoppedRunGrace
}

// stopHosted applies the drain bound to the runs this session hosts: each one
// at a phase the session that comes back can continue from is cancelled with
// the drain as the cause, which its pipeline reads and records as a stop rather
// than a failure. It reports how many hosted runs were left going for want of a
// phase to read — runs still before their claim, or a moment past a check stage
// that has just ended — so the caller can look again rather than wait on them.
//
// A run at its promotion or past it is left to finish. It holds the target
// branch's lease and is minutes from its end, and a promotion interrupted
// part-way is the one boundary durable state cannot describe.
//
// A run at its checks is stopped at the drain bound too. The stage's own bound
// scales with machine load and can be hours away, so waiting for it would keep
// the session on the old build for hours. A stage that has just ended gets a
// brief grace for the pipeline to record its verdict and move to the next
// phase; a running stage gets no grace and is re-run by the next session.
//
// A run that is over and in its landing checks is stopped too. Nothing about
// the run is at stake by then — it has landed, its item is settled, and a
// landing check stopped short judged nothing, so the landing is recorded as
// unverified and files nothing — while the suite it is running is the one the
// landing budget allows hours for, which is exactly the wait this bound exists
// to refuse.
func (s Scheduler) stopHosted(schedule *Schedule, drain *redeployDrain, hosted map[int]context.CancelCauseFunc, landings *hostedLandings, mine map[string]int, runs ScheduleRuns) int {
	drain.reached()
	schedule.Drain.BoundReached = true
	if runs == nil {
		return 0
	}
	inFlight, err := occupiedItems(runs)
	if err != nil {
		// The runs cannot be told apart, so none is stopped on this look: a
		// promotion cancelled because a listing failed would be worse than one
		// more interval of the wait. It is said, and the next look asks again.
		schedule.Drain.Problem = fmt.Sprintf("which phase each hosted run is at could not be read, so none was stopped on this look: %v", err)
		return len(mine)
	}
	unstopped, promoting := 0, 0
	now := s.now()
	defer func() { drain.promoted(promoting, now) }()
	for id, index := range mine {
		cancel, live := hosted[index]
		if !live {
			continue
		}
		state, recorded := inFlight[id]
		if !recorded {
			if landings.landingAt(index) {
				cancel(RedeployDrain{At: now, Bound: drain.limit, SessionID: s.SessionID})
				delete(hosted, index)
				schedule.Drain.Landings = append(schedule.Drain.Landings, id)
				continue
			}
			unstopped++
			continue
		}
		switch state.Phase {
		case runstate.PhaseIntegrating, runstate.PhaseCompleting, runstate.PhaseCleaningUp, runstate.PhaseComplete:
			promoting++
			continue
		case runstate.PhaseChecking:
			if checkStageFinishing(state, now) {
				unstopped++
				continue
			}
		}
		cancel(RedeployDrain{At: now, Bound: drain.limit, SessionID: s.SessionID})
		delete(hosted, index)
		schedule.Drain.Stopped = append(schedule.Drain.Stopped, id)
	}
	slices.Sort(schedule.Drain.Stopped)
	slices.Sort(schedule.Drain.Landings)
	slices.Sort(schedule.Drain.ChecksWaited)
	return unstopped
}

// checkStageDrainGrace is how long past the stage's recorded end a run still
// reading as at its checks is waited on. The pipeline records the verdict and
// moves the run on within seconds, so a run still at its checks a minute later
// is stopped like any other.
const checkStageDrainGrace = time.Minute

// checkStageFinishing reports a stage that has just ended with the run not yet
// moved on from it. A drain looks again shortly rather than throwing away the
// verdict the stage has reached. Running stages are stopped at the drain bound.
func checkStageFinishing(state runstate.State, now time.Time) bool {
	stage := state.CheckStage
	if stage == nil || stage.StartedAt.IsZero() || stage.BoundSeconds <= 0 {
		return false
	}
	return !stage.Running() && now.Before(stage.FinishedAt.Add(checkStageDrainGrace))
}

// readoptionReason is what a run picked up from the session before this one
// records as why it was chosen: it was not chosen, it was handed over, and the
// reason says by what and from where.
func readoptionReason(state runstate.State) string {
	return fmt.Sprintf("re-adopted by the watch session that restarted into the deployed build: run %s was stopped at its %s phase at %s by the session before this one, after that session had drained for its bound of %s to restart; the run continues from durable state with every counter as it was",
		state.RunID, state.RedeployStop.Phase, state.RedeployStop.At.UTC().Format(time.RFC3339), state.RedeployStop.Bound())
}

type watchSession struct {
	to       WatchSessions
	now      func() time.Time
	schedule *Schedule
	state    runstate.WatchState
	said     account
	// drain is the session's wait to restart into a build deployed over it, as
	// every transition carries it while it lasts. It is nil for a session nothing
	// was deployed over, which is most of them.
	drain *runstate.WatchDrain
}

// draining sets what every transition from here on carries about the drain. A
// drain that has not changed writes nothing on its own; what writes a line is
// the next transition, or note below.
func (w *watchSession) draining(drain *runstate.WatchDrain) {
	w.drain = drain
}

// note records the session saying something in the state it is already in,
// which is what the deploy being found is: nothing about what the session is
// doing changed, and a reader is still owed the line.
func (w *watchSession) note(reason string, running int) {
	if w.to == nil {
		return
	}
	state := w.state
	if state == "" {
		state = runstate.WatchWatching
	}
	w.state, w.said = state, account{reason: reason, running: running}
	w.record(SessionState{State: state, Reason: reason, Running: running, Draining: w.drain})
}

// enter records the session arriving in a state. The same state said the same
// way is the session still being in it, which is not news.
//
// "The same way" is the whole account rather than the reason alone, so a poll
// that finds what the poll before it found writes nothing however many times it
// is made — a session idling all night over an unchanging queue is still one
// line. What does write a second line is the account changing: an item passed
// over for a different reason, or a run starting or finishing. Both are news to
// somebody reading an idle line, and both are bounded by the poll interval,
// because a pass records at most one transition per poll.
func (w *watchSession) enter(state runstate.WatchState, said account) {
	if w.drain != nil {
		said.drain = *w.drain
	}
	if w.to == nil || (w.state == state && w.said.same(said)) {
		return
	}
	w.state, w.said = state, said
	transition := SessionState{
		State:      state,
		Reason:     said.reason,
		Running:    said.running,
		Executor:   said.executor,
		Unreadable: said.unreadable,
		PassedOver: said.passedOver,
		Mover:      said.mover,
		Draining:   w.drain,
	}
	if said.window.waiting {
		transition.ProviderWindow = true
		if !said.window.resetsAt.IsZero() {
			resetsAt := said.window.resetsAt.UTC()
			transition.ProviderWindowResetsAt = &resetsAt
		}
	}
	w.record(transition)
}

// stop records the session's last line, and whether the stop is the session
// being restarted into a build deployed over it rather than the line going down.
// The two read identically in the log and mean opposite things to whoever is
// waiting: one is a session somebody has to start again, and the other is a
// session that is already on its way back.
func (w *watchSession) stop(reason string, restarting bool) {
	if w.to == nil {
		return
	}
	w.state, w.said = runstate.WatchStopped, account{reason: reason}
	w.record(SessionState{State: runstate.WatchStopped, Reason: reason, Restarting: restarting, Draining: w.drain})
}

// resume records the session choosing work again after a wait. It leaves the
// session in the watching state rather than a resumed one, so a queue that goes
// quiet and busy all day reads as alternating rather than as a session
// restarting every hour.
func (w *watchSession) resume(reason string) {
	if w.to == nil || w.state == runstate.WatchWatching {
		return
	}
	w.state, w.said = runstate.WatchWatching, account{reason: reason}
	w.record(SessionState{State: runstate.WatchResumed, Reason: reason, Draining: w.drain})
}

// filled records a poll that started work. After a wait it is the session
// choosing again, said exactly as resume says it; while the session is already
// choosing it is one more line in the same state, because what each filling
// poll found and took is the account of the slots, and a session that said it
// only on the first poll after a wait would hide every refill that followed.
func (w *watchSession) filled(reason string) {
	if w.to == nil {
		return
	}
	if w.state != runstate.WatchWatching {
		w.resume(reason)
		return
	}
	w.said = account{reason: reason}
	w.record(SessionState{State: runstate.WatchWatching, Reason: reason})
}

// filledLine is what a filling poll says about the developer slots: how many it
// filled of those it found free, and how many runs of this session were
// already going beside them.
func filledLine(filled, free, alreadyRunning int) string {
	line := fmt.Sprintf("filled %d of %s", filled, plural(free, "free developer slot", "free developer slots"))
	if alreadyRunning > 0 {
		line += fmt.Sprintf(", beside %s this session already had in flight", plural(alreadyRunning, "run", "runs"))
	}
	return line
}

// freedSlot is a developer slot that came free between two pulls: the run that
// had held it, the item that run was over, and whether the run was this
// session's or another process's.
type freedSlot struct {
	runID      string
	workItemID string
	ours       bool
}

// freedSince reads which slots freed since the last pull. The runs of this
// session that ended are the ones it collected, handed in as ended; the runs of
// another process that ended are the ones the last pull found in flight,
// handed in as elsewhere, that this pull does not. It returns those together,
// ended emptied for the next pull, and the runs another process has in flight
// now, which the next pull reads the same way.
//
// A run is judged this session's by its identifier against every run the
// session has started, as well as by the item it is over, so a run of its own
// the store still lists for a moment after it was collected is never counted
// again as another process's.
func freedSince(elsewhere map[string]string, ended []freedSlot, occupied map[string]runstate.State, mine map[string]int, started []Started) ([]freedSlot, []freedSlot, map[string]string) {
	ours := make(map[string]bool, len(started))
	for _, run := range started {
		if run.Outcome.RunID != "" {
			ours[run.Outcome.RunID] = true
		}
	}
	inFlight := make(map[string]bool, len(occupied))
	now := make(map[string]string)
	for id, state := range occupied {
		if state.RunID == "" {
			continue
		}
		inFlight[state.RunID] = true
		if _, mineToo := mine[id]; mineToo || ours[state.RunID] {
			continue
		}
		now[state.RunID] = id
	}
	freed := ended
	var gone []freedSlot
	for runID, id := range elsewhere {
		if !inFlight[runID] {
			gone = append(gone, freedSlot{runID: runID, workItemID: id})
		}
	}
	slices.SortFunc(gone, func(a, b freedSlot) int { return strings.Compare(a.runID, b.runID) })
	return append(freed, gone...), nil, now
}

// freedLine is what a filling poll adds about the slots that freed since the
// last poll, and nothing where none did.
func freedLine(freed []freedSlot) string {
	if len(freed) == 0 {
		return ""
	}
	named := make([]string, 0, len(freed))
	for _, slot := range freed {
		whose := "another process's run"
		if slot.ours {
			whose = "this session's run"
		}
		named = append(named, fmt.Sprintf("%s over %s, %s", slot.runID, slot.workItemID, whose))
	}
	return fmt.Sprintf("; %s since the last poll: %s",
		plural(len(freed), "developer slot freed", "developer slots freed"), strings.Join(named, "; "))
}

// dispatching is the context a dispatch this session starts runs under, carrying
// where the dispatch records a wait it takes before it has claimed anything. Such
// a wait holds a developer slot with no run record, for up to the recovery
// window, and this log is the one place that outlives both the dispatch and the
// session that can say so.
//
// The note is written from the dispatch's goroutine rather than handed back to
// this one, because this one is asleep between polls for exactly as long as the
// wait is worth saying. It touches nothing of the session's but the log: the
// state the session is in is unchanged by it, and a note that could not be
// written costs that wait its visibility and nothing else, so it is not put on
// the schedule the session goroutine owns.
//
// The same holds for a Git command the dispatch ran again because it crossed
// another worktree's creation or removal: the re-run absorbs it, so this log is
// the only place a crossing is ever said, and it is said against the item the
// dispatch was for.
func (w *watchSession) dispatching(ctx context.Context, workItemID string) context.Context {
	if w.to == nil {
		return ctx
	}
	to, now := w.to, w.now
	ctx = gitworktree.WithCrossings(ctx, func(crossed gitworktree.Crossing) {
		crossing := runstate.WorktreeCrossing{
			WorkItemID: workItemID,
			Command:    crossed.Command,
			Attempt:    crossed.Attempt,
			Attempts:   crossed.Attempts,
			At:         now(),
			Refusal:    boundedFailureDetail(crossed.Refusal),
		}
		_ = to.Record(SessionState{
			State:            runstate.WatchWatching,
			At:               crossing.At,
			Reason:           crossing.Says(),
			WorktreeCrossing: &crossing,
		})
	})
	return withDispatchWaits(ctx, func(wait runstate.DispatchWait) {
		_ = to.Record(SessionState{
			State:        runstate.WatchWatching,
			At:           wait.At,
			Reason:       readmodel.DispatchWait{DispatchWait: wait}.Says(),
			DispatchWait: &wait,
		})
	})
}

// passing hands a firing the means to say, as each recurring pass begins,
// which pass the session is inside and since when. It is for a schedule that
// can only fire inside the poll — one that fires beside it is given
// passingBeside — and such a session fires its passes inside its poll, so for as long as one runs the session pulls nothing and,
// until this, wrote nothing either: a pass that spanned the machine's sleep on
// 2026-09-29 left the log silent for twelve hours
// (docs/diagnoses/yoyodyne-ifd-433-20-tracker-listing-timeouts.md). The line is
// written from the firing's own call, on the poll's goroutine, as a note rather
// than a transition, so every fold of the log into the session's state reads
// past it.
func (w *watchSession) passing(ctx context.Context) context.Context {
	if w.to == nil {
		return ctx
	}
	return withPassStarting(ctx, func(pass runstate.WatchPass) {
		w.record(SessionState{
			State:         runstate.WatchWatching,
			Reason:        pass.Says(),
			RecurringPass: &pass,
		})
	})
}

// passingBeside is passing for the passes a session takes beside its poll
// rather than inside it: each pass's note is written from the pass's own
// goroutine as it begins, the way a dispatch's wait is, so it touches nothing
// of the session's but the log, and it says the poll goes on pulling while the
// pass runs.
func (w *watchSession) passingBeside(ctx context.Context) context.Context {
	if w.to == nil {
		return ctx
	}
	to := w.to
	return withPassStarting(ctx, func(pass runstate.WatchPass) {
		pass.Beside = true
		_ = to.Record(SessionState{
			State:         runstate.WatchWatching,
			At:            pass.At,
			Reason:        pass.Says(),
			RecurringPass: &pass,
		})
	})
}

// record writes one transition. A transition that cannot be written costs the
// session its visibility and not its work: what is reported is a session nobody
// can see, which is worth saying out loud and is not worth stopping the work
// for.
func (w *watchSession) record(transition SessionState) {
	transition.At = w.now()
	if err := w.to.Record(transition); err != nil && w.schedule.SessionProblem == "" {
		w.schedule.SessionProblem = fmt.Sprintf("the session could not record that it was %s, so what it is doing is not readable from anywhere but here: %v", transition.State, err)
	}
}

// completed is one finished run on its way back to the scheduling goroutine.
type completed struct {
	index   int
	outcome Outcome
	err     error
	// carriedOut is the account of a triage decision this entry was the harness
	// firing, where it was one. It is a pointer because its absence is the answer
	// for every ordinary run: the queue chose those, and there is no decision behind
	// them to account for.
	carriedOut *CarriedOut
}

// record takes a finished run into its schedule entry. A refusal that means the
// work went to somebody else is recorded as declined rather than failed: two
// schedulers racing for the last slot is the design working, and reporting it as
// a failure would make ordinary concurrency look like breakage.
func (s *Started) record(done completed) {
	s.Outcome = done.outcome
	s.environmental = environmentalStop(done.outcome)
	if done.err == nil {
		return
	}
	var capacity runstate.CapacityError
	var existing ExistingRunError
	switch {
	case errors.As(done.err, &capacity):
		s.Declined = fmt.Sprintf("the last free developer slot went to another run before this one was reserved (%d active, limit %d)",
			capacity.Active, capacity.Limit)
	case errors.As(done.err, &existing):
		s.Declined = fmt.Sprintf("another process is already running %s as %s", existing.State.WorkItemID, existing.State.RunID)
	default:
		s.Failure = done.err.Error()
		var away ProviderOutageError
		if errors.As(done.err, &away) {
			s.awayCause = away.Cause
		}
		var held LaunchSettingsError
		if errors.As(done.err, &held) {
			s.settingsHeld = true
		}
		// A dispatch the environment turned away before any run recorded it — a
		// worktree that could not be cut from a dirty checkout, a process the
		// machine would not start — is the environment's stop as much as one a
		// run classified, and it is read off the error because the error is all
		// there is.
		if _, environmental := environmentalCauseOf(done.err); environmental {
			s.environmental = true
		}
	}
}

// environmentalStop reports a run that stopped for a cause the environment
// answers for rather than a verdict on its change: a round the settle refused
// as environmental, a round nothing of ran, an approved change the environment
// stopped short of its promotion, or a promotion refused because the target
// branch and the remote's have diverged. It is the class the brake does not
// count, because a brake tripped on it summons a decision about a change
// nobody judged and prescribes a release that fixes nothing.
//
// The diverged target is in the class whether or not it stopped an approved
// change. A catch-up the harness will not make is a stop the harness made, and
// every run reaching integration meets the same one until a person settles the
// branches — which the item's own blocker already asks of them, in the words
// docs/operations.md gives for unwedging it, and after which an approved change
// it stopped is resumed as an integration stop. Three identical refusals tripped
// the brake on 2026-09-21, and what that bought was a second hold on the line
// over one cause a person had already been told about once.
func environmentalStop(outcome Outcome) bool {
	if outcome.IntegrationStop != nil || outcome.DivergedTarget != nil {
		return true
	}
	refusal := outcome.Environmental
	return refusal != nil && (refusal.Refused || refusal.NothingRan)
}

// occupiedItems names the work items with a run in flight anywhere, each
// against the run that is in flight over it. It is both halves of what a pull
// needs from the durable state: how many developer slots are taken, and which
// items must not be started again or raced.
//
// In flight is runstate.Status.InFlight — pending or running — which is the one
// predicate the status surface's running count and the store's own listing are
// both built on, so what this refuses an item for is a run `yoyo status` lists.
// The phase does not enter into it: a run integrating is in flight, and holds
// its epic until the promotion settles. A failed run over an item — one that
// stopped on a replay conflict, say, with its branch and pull request preserved
// and a decision about it still owed — is a record of that item, and a record
// holds neither a slot nor an epic. The store's listing already answers in these
// terms, and the predicate is applied here as well so that the guard's reading
// is the status's own rather than whatever the listing it was handed returns.
//
// Each item is held against the run's whole record rather than only its id,
// because the developer slot a run occupies is read off what the record says the
// item carried when the run started — see developerslot for the derivation.
func occupiedItems(runs ScheduleRuns) (map[string]runstate.State, error) {
	incomplete, err := runs.Incomplete()
	if err != nil {
		return nil, fmt.Errorf("read what is already in flight: %w", err)
	}
	occupied := make(map[string]runstate.State, len(incomplete))
	for _, state := range incomplete {
		if !state.Status.InFlight() {
			continue
		}
		occupied[state.WorkItemID] = state
	}
	return occupied, nil
}

// waitingOnDependencies takes the runs paused on work their items wait on out of
// occupied and returns them. Such a run is in flight and holds no developer slot
// (runstate.State.HoldsDeveloperSlot), so what is left in occupied is what the
// capacity is counted against.
func waitingOnDependencies(occupied map[string]runstate.State) map[string]runstate.State {
	waiting := map[string]runstate.State{}
	for id, state := range occupied {
		if !state.HoldsDeveloperSlot() {
			waiting[id] = state
			delete(occupied, id)
		}
	}
	return waiting
}

// slotsOf reads which developer slot each run in flight occupies and which are
// free, from the same derivation the standing status reads. A run whose record
// carries no labels — one this session started and that has not reserved yet,
// or one recorded before labels were written onto records — is read as its item
// reads now, so a labelled item this pull just started into a preferring slot is
// counted in that slot at the next pull rather than in one preferring nothing.
func (p Pull) slotsOf(occupied map[string]runstate.State, items map[string]beads.WorkItem) developerslot.Assignment {
	inFlight := make([]developerslot.Run, 0, len(occupied))
	for id, state := range occupied {
		labels := state.WorkItemLabels
		if len(labels) == 0 {
			labels = items[id].Labels
		}
		inFlight = append(inFlight, developerslot.Run{
			RunID:      state.RunID,
			WorkItemID: id,
			Labels:     labels,
			StartedAt:  state.StartedAt,
			Slot:       state.RecordedSlot(),
		})
	}
	return developerslot.Assign(p.Capacity, p.Slots, inFlight)
}

// pulled is one reading of the queue: the admitted work in the product
// manager's order, and the items it was assembled from. The items are kept
// because the order alone does not carry what the work says, and a watch has to
// be able to tell an item that has changed since it was tried from one that has
// not.
type pulled struct {
	queue backlog.Queue
	// items is every work item this pull read, by identifier: the admitted ones
	// the queue was assembled from, and the claimed ones that have left it. The
	// claimed half is there because what a run in flight is going to change is
	// what says whether something else may be started beside it, and an item
	// somebody has already pulled is not in the queue to be read from.
	items map[string]beads.WorkItem
	// coverage names the unfinished children of each item in this reading — the
	// ones still queued, and the ones somebody has already pulled — in the
	// product manager's order. A child that has closed is not here, which is
	// exactly when its parent stops being covered by it. It is the backlog's
	// derivation rather than this package's, so what a pass will not pull and
	// what the surfaces report as not startable are one answer.
	coverage backlog.Coverage
}

// claimed is the tracker slice that has left the backlog by being pulled. It is
// read on its own because two things in one pull need it and neither is the
// other's: the claim audit reads it before the machine's capacity is even
// consulted, and the queue below reads it for coverage. One reading serves both,
// so a pull spawns `bd` for it once.
func (p Pull) claimed(ctx context.Context) ([]beads.WorkItem, error) {
	items, err := p.Tracker.List(ctx, claimedStatus)
	if err != nil {
		return nil, fmt.Errorf("list %s work items: %w", claimedStatus, err)
	}
	return items, nil
}

// queue assembles the admitted work into the product manager's order. It is the
// same assembly a conversation's backlog uses and the same one a development
// manager reads, built from the tracker every pull rather than stored, so what
// the scheduler pulls can never drift from the priorities actually set.
//
// The claimed slice is passed in rather than read here because the pull has
// already read it for the claim audit; see Pull.claimed. Where that reading did
// not happen — the audit's read failed, and it is reported rather than retried —
// this makes it, because the coverage below is not optional the way the audit is
// and a pull that skipped it would start a parent beside the child running it.
func (p Pull) queue(ctx context.Context, claimed []beads.WorkItem, read bool) (pulled, error) {
	var admitted []beads.WorkItem
	for _, status := range scheduledStatuses {
		items, err := p.Tracker.List(ctx, status)
		if err != nil {
			return pulled{}, fmt.Errorf("list %s work items: %w", status, err)
		}
		admitted = append(admitted, items...)
	}
	ready, err := p.Tracker.Ready(ctx)
	if err != nil {
		return pulled{}, fmt.Errorf("list the work items the tracker reports as ready: %w", err)
	}
	pullable := make([]string, 0, len(ready))
	for _, item := range ready {
		pullable = append(pullable, item.ID)
	}
	items := make(map[string]beads.WorkItem, len(admitted))
	for _, item := range admitted {
		items[item.ID] = item
	}
	// What the harness is holding for a person, which is what a status of blocked
	// cannot say: it is written when work stops and never rewritten when what
	// stopped it clears. A pull wired without the records reads no holds and the
	// queue then holds every blocked item, which is what this did before the
	// records were consulted at all.
	var held backlog.Holds
	if p.Stoppages != nil {
		held, err = readmodel.HeldForAPerson(ctx, p.Stoppages, p.Decisions, p.Remains)
		if err != nil {
			return pulled{}, fmt.Errorf("read what the harness is holding back after stopped runs: %w", err)
		}
	}
	// The gates a person has recorded passing come from the harness's own store,
	// because the tracker cannot answer the question: the only completion it
	// records is an item being closed, and an item's closure passing a step the
	// operator reserved for themselves is what these exist to stop. A pull that
	// could not read them treats every declared gate as still holding, which
	// stops work rather than starting it past somebody's step.
	discharged, err := p.Gates.DischargedGates()
	if err != nil {
		return pulled{}, fmt.Errorf("read the human gates a person has passed: %w", err)
	}
	queue := backlog.Order(admitted, pullable, held, discharged)
	// Coverage is read one status wider than the backlog. A claimed child has left
	// the queue and is never chosen from here, but it is a run in flight over the
	// same work, so its parent is the last thing that should be started beside it.
	if !read {
		claimed, err = p.claimed(ctx)
		if err != nil {
			return pulled{}, err
		}
	}
	for _, item := range claimed {
		items[item.ID] = item
	}
	return pulled{queue: queue, items: items, coverage: backlog.Cover(queue, admitted, claimed)}, nil
}

// unready is the prerequisites this item states that the tree does not meet, and
// the reading that failed where one did. A pull with no tree wired reads nothing
// and refuses nothing, which is what every pass did before this existed.
func (p Pull) unready(item beads.WorkItem) ([]readiness.Unmet, string) {
	if p.Tree == nil || strings.TrimSpace(item.ID) == "" {
		return nil, ""
	}
	unmet, err := readiness.Check(item, p.Tree)
	if err == nil {
		return unmet, ""
	}
	// Whatever could be read is still acted on. A tree that answered two of an
	// item's three citations answered two of them, and the one it could not is
	// reported rather than turned into either a refusal or a clean bill.
	return unmet, fmt.Sprintf("what %s asks of the tree could not be read in full, so it was judged on what could: %v", item.ID, err)
}

// route puts an item the tree is not ready for on the development manager's
// docket. A pull with nowhere to route it says so and passes the item over
// anyway; see ScheduleTriage for why that is the direction.
func (p Pull) route(item beads.WorkItem, unmet []readiness.Unmet) error {
	if p.Triage == nil {
		return fmt.Errorf("%s states prerequisites the tree does not meet and nothing was wired to docket that, so the finding is on this pass and nowhere durable", item.ID)
	}
	if _, err := p.Triage.RecordUnreadyItem(item, unmet); err != nil {
		return fmt.Errorf("route %s to triage for an unmet prerequisite: %w", item.ID, err)
	}
	return nil
}

// settleUnready reads again every item the docket holds as unready, from the
// items this pull read, and takes off the entries the reading no longer
// supports. A pull with no tree or no docket wired settles nothing, because it
// read nothing and docketed nothing; a reading that failed is reported beside
// the pass, like the readiness reading it repeats.
func (p Pull) settleUnready(read pulled) string {
	if p.Tree == nil || p.Triage == nil {
		return ""
	}
	_, err := p.Triage.SettleUnreadyItems(func(workItemID string) UnreadyReading {
		item, present := read.items[workItemID]
		if !present {
			return UnreadyReading{}
		}
		unmet, problem := p.unready(item)
		return UnreadyReading{Present: true, Unmet: unmet, Unreadable: problem != ""}
	})
	if err != nil {
		return fmt.Sprintf("the items the docket holds as unready could not all be read again, so some may still stand there under words their item no longer carries: %v", err)
	}
	return ""
}

// stale reads what changed upstream of the admitted work after it was admitted,
// keyed by work item. It withholds nothing and reorders nothing; what it
// produces goes into the recorded reason a run was chosen. A reading that failed
// is described rather than raised, because losing it costs a sentence.
func (p Pull) stale(ctx context.Context) (map[string][]staleness.Change, string) {
	if p.Staleness == nil {
		return nil, ""
	}
	items, err := p.Staleness.Stale(ctx)
	if err != nil {
		return nil, fmt.Sprintf("what changed upstream of the admitted work could not be read, so no recorded reason names it: %v", err)
	}
	changes := make(map[string][]staleness.Change, len(items))
	for _, item := range items {
		changes[item.ID] = item.Changes
	}
	return changes, ""
}

// scheduleReason is what the run records as why the harness chose this item. It
// is prose rather than a code because what makes a choice defensible is the
// argument for it: an operator reading a run months later needs where the item
// sat in the order, what else was pullable, and how much of the machine was
// already busy.
//
// Staleness goes in the same sentence and is stated as not having decided
// anything, because that is exactly what would otherwise be misread: an item
// pulled with a change named beside it looks like an item pulled in spite of a
// warning, and it is neither.
//
// Sequencing goes in the same reason for the opposite purpose: it did decide
// something. Where conflict-avoidance moved what was started, the run that was
// started says so, because an order departed from silently is one nobody can
// account for afterwards.
func scheduleReason(entry backlog.Entry, queue backlog.Queue, free, capacity int, stale []staleness.Change, ordering sequencing) string {
	reason := fmt.Sprintf(
		"the scheduler pulled %s from the backlog: position %d of %d admitted item(s) at priority %d, one of the %d the tracker reports as ready, with %d of %d developer slot(s) free",
		entry.ID, entry.Position, len(queue.Entries), entry.Priority, queue.Ready(), free, capacity)
	if len(stale) > 0 {
		change := stale[0]
		reason += fmt.Sprintf(
			". %s was %s by the %s after this item was admitted (%s), and %d further change(s) upstream of it; staleness is reported rather than acted on, so it held nothing back",
			change.ArtifactID, change.Action, change.By,
			singleLine(change.Reason, maxScheduleReasonBytes), len(stale)-1)
	}
	return reason + "." + ordering.reason()
}

// Render describes a pass for an operator: what it started, what became of each
// one, and why it stopped choosing. What it will not do is print a line per
// quiet outcome — a pass that started nothing says so in one line, because that
// is the whole of what happened.
func (s Schedule) Render() string {
	var rendered strings.Builder
	if len(s.Started) == 0 {
		fmt.Fprintf(&rendered, "nothing was started: %s\n", s.Stopped)
	} else {
		fmt.Fprintf(&rendered, "%d run(s) started, %d of %d developer slot(s) taken at the last pull\n",
			len(s.Started), s.Occupied, s.Capacity)
	}
	// What the queue looked like is the pass-level answer to why an item was not
	// started, so it is said whichever way the pass went — and said as counts,
	// because the alternative is a line for every admitted item on every pass. A
	// pass that never got as far as the queue says nothing here rather than
	// printing zeroes it did not read.
	if s.BacklogRead {
		fmt.Fprintf(&rendered, "backlog at the last pull: %d admitted item(s), %d of them ready to pull\n",
			s.Admitted, s.Pullable)
	}
	for _, started := range s.Started {
		fmt.Fprintf(&rendered, "%s: %s\n", started.WorkItemID, started.state())
		if started.Probe {
			fmt.Fprintln(&rendered, "  the intake brake's probe run, started under its hold")
		}
		fmt.Fprintf(&rendered, "  chosen because %s\n", started.Reason)
		if started.Readoptions > 1 {
			fmt.Fprintf(&rendered, "  re-adopted %d times this session: the run's record still carried its stop after each earlier try, and this line is the latest\n", started.Readoptions)
		}
		if started.Outcome.Integration != nil {
			fmt.Fprintf(&rendered, "  integrated into %s: %s\n",
				started.Outcome.Integration.TargetBranch, started.Outcome.Integration.TargetCommit)
		}
		if started.Declined != "" {
			fmt.Fprintf(&rendered, "  not started: %s\n", started.Declined)
		}
		if started.Failure != "" {
			fmt.Fprintf(&rendered, "  failed: %s\n", started.Failure)
		}
	}
	for _, deferred := range s.Deferred {
		fmt.Fprintf(&rendered, "%s was not pulled: %s\n", deferred.WorkItemID, deferred.Reason)
	}
	// What the pass put in front of the development manager, said beside what it
	// pulled: a run stopped hours ago reaching her now is the other half of what
	// this session did with its time.
	rendered.WriteString(EscalationSweep{Escalated: s.Escalated}.Render())
	if s.EscalationProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.EscalationProblem)
	}
	// And what the pass fired of what she has already decided, said beside what it
	// put to her: the two are the same loop seen at its two ends, and a decision
	// that could not be fired is the half nobody used to be told about at all.
	for _, carried := range s.CarriedOut {
		rendered.WriteString(carried.Render())
	}
	for _, unattempted := range s.CarryOutUnattempted {
		rendered.WriteString(unattempted.Render())
	}
	if s.CarryOutProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.CarryOutProblem)
	}
	if s.CarryOutReadProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.CarryOutReadProblem)
	}
	if s.CarryOutNoteProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.CarryOutNoteProblem)
	}
	// A paused run the pass could not read for continuing is said beside the
	// decisions, for the same reason: a continuation that quietly never happens
	// is the stall it exists to end.
	if s.ContinuationProblem != "" {
		fmt.Fprintf(&rendered, "a run paused on work its item waits on was not continued: %s\n", s.ContinuationProblem)
	}
	// And what the pass woke on a cadence, said beside both: a session that spent
	// turns on a sweep is a session that did something, and the whole account of
	// it is in the durable report `yoyo sweeps` reads.
	rendered.WriteString(RecurringSweep{Fired: s.Fired}.Render())
	if s.RecurringProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.RecurringProblem)
	}
	// And what the pass woke to put a refused block right, said beside both: the
	// actions a role lost are actions an operator may be expecting, and a pass that
	// spent a turn getting them re-issued is a pass that did something.
	rendered.WriteString(CorrectionSweep{Corrected: s.Corrected}.Render())
	if s.CorrectionProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.CorrectionProblem)
	}
	// What the pass gave back, said beside what it pulled: an item the harness
	// unstuck is work that will be started again, and a reader who is not told
	// would find a second run for it with nothing accounting for the first.
	for _, released := range s.ReleasedClaims {
		fmt.Fprintf(&rendered, "%s was claimed with nothing working on it and was given back to the queue: %s\n",
			released.WorkItemID, released.Because)
	}
	if s.ClaimProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.ClaimProblem)
	}
	// What the pass closed because its landing was already in the tree, said
	// beside what it pulled: an item closed by the harness rather than by the
	// product manager is one she would otherwise go looking for.
	rendered.WriteString(LandingSweep{Landed: s.Landed}.Render())
	if s.LandingProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.LandingProblem)
	}
	if s.Blocked != "" {
		fmt.Fprintln(&rendered, s.Blocked)
	}
	if s.IntakeHeld != nil {
		fmt.Fprintf(&rendered, "intake has been held since %s: %s\n",
			s.IntakeHeld.HeldAt.UTC().Format("2006-01-02 15:04:05Z"), s.IntakeHeld.Account())
	}
	// A session that waited says how long it was alive for, because the whole
	// point of watching is that nothing happening is not the same as nothing
	// running.
	if s.Watched && s.Polls > 0 {
		fmt.Fprintf(&rendered, "waited out %d poll interval(s) with nothing to start\n", s.Polls)
	}
	// A session that rode through readings that failed says so. The reading that
	// succeeded afterwards leaves nothing behind, so an outage nobody was told
	// about is one that gets diagnosed from scratch the next time it happens. The
	// last of them is named except where the session went on to stop on a reading
	// too, which is said once in the failure it stopped with rather than twice.
	if s.ReadsRetried > 0 {
		fmt.Fprintf(&rendered, "%d reading(s) of the harness failed and were made again rather than stopping the session\n", s.ReadsRetried)
	}
	if s.ReadProblem != "" && s.ReadFailure == "" {
		fmt.Fprintf(&rendered, "the last of them: %s\n", s.ReadProblem)
	}
	if s.ReadFailure != "" {
		fmt.Fprintf(&rendered, "%s\n", s.ReadFailure)
	}
	// The provider answering nobody is said with what ends it rather than with
	// a command, because there is none: the brake's remedy is the one an operator
	// reaches for, and it lifts nothing here.
	if s.ProviderOutage != nil {
		fmt.Fprintf(&rendered, "%s\n", s.ProviderOutage.Says())
	}
	if s.ProviderAway > 0 {
		fmt.Fprintf(&rendered, "%d dispatch(es) were turned away by the provider and counted toward nothing; the items are started when it answers\n", s.ProviderAway)
	}
	if s.OutageProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.OutageProblem)
	}
	// A diverged target is said with its recovery, because it is the operator's
	// and nothing the harness does ends it except finding the branches settled.
	for _, diverged := range s.DivergedTargets {
		fmt.Fprintf(&rendered, "%s\n", divergedTargetReason(diverged))
	}
	if s.DivergedTargetProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.DivergedTargetProblem)
	}
	if s.LaunchSettingsHold != nil {
		fmt.Fprintf(&rendered, "%s\n", s.LaunchSettingsHold.Says())
	}
	if s.LaunchSettingsProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.LaunchSettingsProblem)
	}
	if s.Braked != nil {
		// The brake places a hold nobody chose, so the line that reports it says
		// what lifts it: the development manager's decision, or the probe run the
		// harness makes if she records none. `yoyo release` still lifts it from a
		// terminal, for whoever would rather not wait for either.
		fmt.Fprintf(&rendered, "this session's own brake held intake after %d run(s) blocked in a row and summoned the development manager; the hold is released on her decision or on a probe run that lands, and `yoyo release` lifts it sooner\n", s.BlockedInARow)
	}
	for _, released := range s.Released {
		fmt.Fprintf(&rendered, "the brake's hold was released at %s: %s\n", released.At.UTC().Format(time.RFC3339), released.Reason)
	}
	if escalated := s.BrakeEscalated; escalated != nil {
		fmt.Fprintf(&rendered, "the brake's hold was escalated to the operator at %s, after %d summons-and-probe cycle(s) with the development manager not escalating it; the last probe run, of %s, blocked: %s\n",
			escalated.At.UTC().Format(time.RFC3339), escalated.Cycles, escalated.Probe, singleLine(escalated.Reason, maxScheduleReasonBytes))
	}
	if s.BrakeProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.BrakeProblem)
	}
	if s.SpentUSD > 0 || s.Budget > 0 {
		fmt.Fprintf(&rendered, "spent $%.2f", s.SpentUSD)
		if s.Budget > 0 {
			fmt.Fprintf(&rendered, " of the $%.2f this session was given", s.Budget)
		}
		rendered.WriteString("\n")
	}
	if s.SpendProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.SpendProblem)
	}
	if s.SessionProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.SessionProblem)
	}
	if s.RedeployProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.RedeployProblem)
	}
	// What the session did about a build deployed over it: the drain, its bound,
	// and what became of the runs still going when the bound ran out. A session
	// whose runs all ended inside the bound says the drain and nothing more.
	if s.Drain != nil {
		fmt.Fprintf(&rendered, "a build was deployed over this session at %s; it drained to restart into it, bounded at %s\n",
			s.Drain.Since.UTC().Format("2006-01-02 15:04:05Z"), s.Drain.Bound)
		switch {
		case s.Drain.BoundReached && len(s.Drain.Stopped) == 0 && len(s.Drain.Landings) == 0:
			rendered.WriteString("the drain bound ran out with only a run at its promotion still going, which was waited out rather than stopped\n")
		case s.Drain.BoundReached && len(s.Drain.Stopped) > 0:
			fmt.Fprintf(&rendered, "the drain bound ran out with %d run(s) still going, stopped and preserved for the session that comes back: %s\n",
				len(s.Drain.Stopped), strings.Join(s.Drain.Stopped, ", "))
		}
		if s.Drain.BoundReached && len(s.Drain.Landings) > 0 {
			fmt.Fprintf(&rendered, "the landing checks of %d run(s) that had already landed were stopped at the drain bound, so each landing is recorded as unverified: %s\n",
				len(s.Drain.Landings), strings.Join(s.Drain.Landings, ", "))
		}
		if len(s.Drain.Unreported) > 0 {
			fmt.Fprintf(&rendered, "%d stopped run(s) had not reported back when the session restarted and were not waited for: %s\n",
				len(s.Drain.Unreported), strings.Join(s.Drain.Unreported, ", "))
		}
		if s.Drain.Skipped > 0 {
			fmt.Fprintf(&rendered, "%d poll(s) pulled nothing into a free seat because the bound was less than one poll away\n", s.Drain.Skipped)
		}
		if s.Drain.Problem != "" {
			fmt.Fprintf(&rendered, "%s\n", s.Drain.Problem)
		}
	}
	if s.StalenessProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.StalenessProblem)
	}
	if s.ReadinessProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.ReadinessProblem)
	}
	if s.AttemptProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.AttemptProblem)
	}
	if len(s.Started) > 0 {
		fmt.Fprintf(&rendered, "stopped pulling: %s\n", s.Stopped)
	}
	return rendered.String()
}

// state is the one-line account of what became of a started run. A declined
// start is neither a success nor a failure and is named as itself.
//
// A run that reached an ending is named by that ending, in the read model's own
// vocabulary, rather than by the error the start came back with. Every stoppage
// returns one: a run handed to a person with its branch and worktree intact
// comes back to the scheduler as an error exactly as a run that broke does, so
// reading the error first put "failed" over both. Which of them it was is the
// pass's to report and not to work out — Ending is the same derivation `yoyo
// status` and the channel read.
func (s Started) state() string {
	switch {
	case s.Declined != "":
		return "declined"
	case endedWithoutSucceeding(s.Outcome.Status):
		return string(s.Outcome.Ending())
	case s.Failure != "":
		// No run reached a status, so there is no ending to name: the start itself
		// is what failed.
		return "failed"
	case s.Outcome.Paused && s.Outcome.RedeployStop != nil:
		// Stopped by this session for its own restart rather than by anything
		// about the run, and said as that: a reader of "paused" goes looking for
		// a provider window or a directive, and there is neither.
		return "stopped for the redeploy and preserved"
	case s.Outcome.Paused:
		return "paused"
	case s.Outcome.Integration != nil:
		return "integrated"
	default:
		return string(s.Outcome.Status)
	}
}

// endedWithoutSucceeding reports a run that reached a terminal status other than
// success, which is the whole of where the four-way vocabulary applies.
func endedWithoutSucceeding(status runstate.Status) bool {
	return status.Terminal() && status != runstate.StatusSucceeded
}

// Redeploying reports a session that stopped in order to be restarted into the
// build deployed over it, with every run it started waited out first.
//
// It is read from why the session stopped rather than from a flag of its own,
// because the two can disagree and only one of them is the truth: a session that
// had decided to redeploy and was then stopped by its operator stopped for the
// operator, and restarting it would be this loop overriding the person who
// closed it.
func (s Schedule) Redeploying() bool {
	return s.Stopped == ScheduleRedeployed
}

// Failed reports a pass with something in it an operator has to act on. A
// declined start is deliberately not one: the work went to another process,
// which is two schedulers doing exactly what they should.
func (s Schedule) Failed() bool {
	for _, started := range s.Started {
		if started.Failure != "" {
			return true
		}
	}
	return false
}
