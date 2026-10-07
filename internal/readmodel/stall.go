package readmodel

// Why the harness is choosing nothing, derived once for every surface that says
// it.
//
// This derivation existed twice before it lived here: the standing status worked
// it out from the switches, the capacity, and the watch log, and the channel
// heartbeat worked it out again from the same three records a few packages away.
// Two readings of one machine is a disagreement only the operator can
// adjudicate, and these two already disagreed — a live session sitting idle over
// a queue it would not touch was reported by one as a session that had found
// nothing it could start, and by the other as no session running at all, which
// told the operator to start the session that was already there.
//
// A third copy is expected and is not on this line yet. The work item that
// consolidated these names an escalation path in the Slack sink — the push half
// of the heartbeat, which DMs the operators when the system is stopped — as
// carrying its own answer to the same question; that file was written on the
// branch for yoyodyne-ifd.68.20 and has never been merged here, so there was
// nothing in this tree to route through this. It is named here rather than left
// out because the omission is what would make it look considered: when that work
// lands it projects this, and does not derive the stopped state again. What it
// would otherwise inherit is a disagreement this change has just widened, since
// the wording and the idle-versus-absent answer both moved when they came here.
//
// # The taxonomy is closed
//
// Every state this can be in is a named Reason, and every Reason says whose move
// it is. That is the point of naming them rather than assembling a sentence at
// each call site: a reason nobody named is a reason nobody can act on, and the
// state an operator most needs is exactly the one no author thought to write a
// sentence for. A reading with no Reason at all is not a residual category — it
// is the harness saying it would start the next pullable item, which is a
// different answer and the one that makes a startable item's absence from the
// not-startable line mean something.
//
// What is not here is what each surface does about it. Whether a state is worth
// interrupting somebody for, how often it is repeated, and what else is said
// beside it are the surface's decisions, and the two surfaces here make them
// differently on purpose: a terminal answers when it is asked, and a channel
// speaks unprompted and has to be worth reading.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Reason is one member of the fixed set of reasons the harness is choosing no
// work. The set is closed: a state outside it cannot be reported, which is what
// makes an unnamed reason impossible rather than unlikely.
//
// The values are the tokens a durable cursor already holds — the sink names the
// state it is standing on by them, so that a different state re-arms its clock
// rather than inheriting the last one's. They are kept as they were found for
// that reason: renaming one would re-arm every state standing at the moment this
// landed, and the hour that costs is an hour of exactly the silence this exists
// to end.
type Reason string

const (
	// ReasonOperatorHold is the switch over everything the harness would spend.
	ReasonOperatorHold Reason = "hold"
	// ReasonIntakeHold is the switch over the work the harness chooses for itself.
	ReasonIntakeHold Reason = "intake"
	// ReasonNoCapacity is a machine with every developer slot taken. It is the one
	// reason here that is the harness working rather than the harness stopped.
	ReasonNoCapacity Reason = "capacity"
	// ReasonProviderAway is the provider answering nobody: a login nobody has
	// renewed, or an API nothing reaches. It is distinct from the window below
	// because no clock ends it — a person logging in or the network returning is
	// what does — and distinct from the intake hold because no switch lifts it.
	// It is read ahead of a full machine because the runs holding the slots are
	// waiting on the same provider.
	ReasonProviderAway Reason = "provider-away"
	// ReasonDivergedTarget is a target branch the harness will not catch up to
	// the remote's, recorded by the run whose promotion was refused on it. It is
	// distinct from the intake hold because nobody placed it and `yoyo release`
	// does not lift it, and distinct from the brake because it counts nothing: a
	// person settling the branches is what ends it, and the convergence sweep
	// that finds them settled lifts it with nothing to release. It is read ahead
	// of a full machine because every run holding a slot will stop on it too.
	ReasonDivergedTarget Reason = "diverged-target"
	// ReasonProviderWindow is a live session waiting out the provider's usage
	// window. It is distinct from an idle session because an operator does nothing
	// at all about it: the window lifts on the provider's clock, and a surface that
	// reported this as a session finding nothing to start would be sending somebody
	// to look at a queue that is fine.
	ReasonProviderWindow Reason = "provider"
	// ReasonTrackerWait is a dispatch a live session started that is waiting out a
	// tracker failure before it has claimed anything. It is distinct from an idle
	// session for the reason the window is: the session found work and started it,
	// and the dispatch asks the tracker again on its own clock, so a reader told
	// the session had found nothing to start would be sent to look at a queue that
	// is fine.
	ReasonTrackerWait Reason = "tracker"
	// ReasonStoreUnreadable is a live session whose last poll could not read the
	// harness's store at all and is reading it again. It is distinct from an idle
	// session because the queue was never read: a reader told the session had found
	// nothing to start would take an outage for an empty queue, and on 2026-09-01
	// that is how a store outage was voiced for its whole length. It is the
	// harness's to clear, by reading again until the store answers or the session
	// gives up on it and stops.
	ReasonStoreUnreadable Reason = "unreadable"
	// ReasonSessionIdle is a live session that is choosing nothing. It is distinct
	// from having no session at all because an operator does an entirely different
	// thing about it, and because telling them to start a session they are already
	// running is worse than telling them nothing.
	ReasonSessionIdle Reason = "idle"
	// ReasonSessionBlocked is a machine refusing starts, with its own cause.
	ReasonSessionBlocked Reason = "blocked"
	// ReasonRedeploying is a session restarting into a build deployed over it: it
	// has found the deploy, its bounded drain has run out with runs still going,
	// and it is stopping and preserving them, or it has already stopped and is
	// being re-executed. It is distinct from an idle session and from no session
	// because an operator does nothing at all about it — the session comes back
	// on its own within a minute and re-adopts what it stopped — and a surface
	// that reported it as either would send somebody to start a session that is
	// already on its way back.
	ReasonRedeploying Reason = "redeploying"
	// ReasonDrainOverrun is a session whose drain bound ran out more than
	// DrainOverrunAfter ago and which has recorded no restart since. Past the
	// bound the session stops the runs it hosts and restarts at once, waiting
	// only on a run at its promotion, so one still there this long and not
	// saying it is waiting on a promotion is a session that stopped restarting
	// rather than one on its way back: it pulls no new work until it does. It is the harness's, because nothing anybody
	// configured keeps it there. On 2026-10-05 a session sat like this for
	// forty minutes, pulling nothing, while every surface read it as working.
	ReasonDrainOverrun Reason = "drain-overrun"
	// ReasonNoWatchSession is a product that was being watched and is not any more.
	ReasonNoWatchSession Reason = "stopped"
	// ReasonUnwatched is a product no session has ever watched. It is not a line
	// that stopped: nothing was choosing work here, so nothing is failing to, and
	// an operator running items by name has a queue by choice.
	ReasonUnwatched Reason = "unwatched"
)

// Reasons is the whole taxonomy, in the order an operator acts on it. A caller
// that has to cover every reason reads it from here rather than repeating the
// list.
func Reasons() []Reason {
	return []Reason{
		ReasonOperatorHold,
		ReasonIntakeHold,
		ReasonProviderAway,
		ReasonDivergedTarget,
		ReasonNoCapacity,
		ReasonProviderWindow,
		ReasonTrackerWait,
		ReasonStoreUnreadable,
		ReasonSessionBlocked,
		ReasonSessionIdle,
		ReasonDrainOverrun,
		ReasonRedeploying,
		ReasonNoWatchSession,
		ReasonUnwatched,
	}
}

// Whose is whose move it is, and what settles it. It is half of what a reason is
// for: a surface that says work is held without saying who by has told the
// reader something they can do nothing with.
//
// Every reason answers. One that did not would be a state named and then left
// unattributed, which is the hole the taxonomy exists to close, so the zero
// answer belongs to no reason and a test holds the set to it.
func (r Reason) Whose() string {
	switch r {
	case ReasonOperatorHold:
		return "the operator's — nothing runs until `yoyo resume` lifts it"
	case ReasonIntakeHold:
		// The vocabulary cannot see the hold, so it says what holds for both
		// holders: the operator's own hold is theirs, and the brake's is the
		// development manager's or the harness's until she escalates it. The
		// attention line reads the hold itself and says which.
		return "the operator's for a hold they placed, and the development manager's or the harness's for one the brake placed — nothing new is chosen until it is released, and `yoyo release` lifts either"
	case ReasonProviderAway:
		return "the operator's — log in to the provider, or wait for the network; the harness resumes on its own once it answers, and nothing is released or restarted"
	case ReasonDivergedTarget:
		return "the operator's — " + runstate.DivergedTargetRecovery
	case ReasonNoCapacity:
		return "nobody's — a slot frees as a run in flight finishes"
	case ReasonProviderWindow:
		return "nobody's — the harness asks again when the provider's usage window lifts"
	case ReasonTrackerWait:
		return "nobody's — the dispatch asks the tracker again on its own, and puts the item on the development manager's docket only once the recovery window is spent"
	case ReasonStoreUnreadable:
		return "the harness's — the queue could not be read, and it is read again until it answers or the session gives up on it"
	case ReasonSessionBlocked:
		return "the harness's — runs are retried at the next poll once the refusing condition clears"
	case ReasonSessionIdle:
		return "the operator's — a queue with ready work and an idle session is a stall rather than a rest"
	case ReasonRedeploying:
		return "nobody's — the session restarts into the deployed build on its own, and the session that comes back re-adopts the runs it stopped"
	case ReasonDrainOverrun:
		return "the harness's — the session should have restarted when its drain bound ran out, and nothing new starts until it does; restarting the watch session takes up the deployed build, and the session that comes back picks up the runs it stopped"
	case ReasonNoWatchSession, ReasonUnwatched:
		return "the operator's — nothing pulls the queue until `yoyo work --watch` starts a session"
	default:
		return ""
	}
}

// Conditions are the records one reading of the stall is derived from. They are
// passed in rather than read here because both callers have already read them
// for something else, and a second reading is a second chance for one pass to
// report a state two ways.
type Conditions struct {
	OperatorHold runstate.OperatorHold
	OperatorHeld bool
	IntakeHold   runstate.IntakeHold
	IntakeHeld   bool
	// ProviderOutage is the provider answering nobody, and ProviderAway whether
	// one stands. A caller that never read the record leaves both zero and gets
	// the rest of the answer.
	ProviderOutage runstate.ProviderOutage
	ProviderAway   bool
	// Diverged is every target branch recorded as one the harness will not catch
	// up to the remote's. A caller that never read the record leaves it empty.
	Diverged []runstate.DivergedTarget
	// Running is how many developer runs are in flight, read against Capacity. A
	// caller that has already decided a run in flight is not a stalled line leaves
	// both at zero and gets the rest of the answer.
	Running  int
	Capacity int
	// Sessions is the watch log, asked for only if the question reaches it: the
	// switches and the machine's own capacity are answered from records the caller
	// already holds, and a caller that would have to spend a read to answer this
	// spends it only when nothing before it has. A caller with the log in hand
	// returns it and never fails.
	Sessions func() ([]runstate.WatchTransition, error)
	// Now is when the reading was taken, which is what says whether a provider's
	// usage window a session recorded is still standing or has already lifted. It
	// defaults to the wall clock, so a caller with no particular moment in mind
	// passes none.
	Now time.Time
}

func (c Conditions) now() time.Time {
	if c.Now.IsZero() {
		return time.Now().UTC()
	}
	return c.Now.UTC()
}

// Stall is why nothing is being chosen: the named reason, what it says, when it
// became true, and what could not be read where nothing could be answered. A
// stall with no reason is the harness saying it would start the next pullable
// item.
type Stall struct {
	Reason Reason `json:"reason,omitempty"`
	// Says is the state as a clause, with no remedy in it, because it is read
	// inside sentences the surfaces write around it.
	//
	// ReasonSessionBlocked carries the watch's own cause and remedy verbatim.
	// ReasonProviderWindow is another exception, and it is the operator's rather
	// than an inconsistency: he asked that when the harness is paused on a usage
	// window the cause be the first words of any message that reaches him, so that
	// one is a whole sentence and the surfaces open with it instead of writing
	// around it. See ProviderWindow.Says.
	Says string `json:"says,omitempty"`
	// Clears is what settles the state, where a command settles it. It is separate
	// from Says so a surface can say the state without the instruction.
	Clears string `json:"clears,omitempty"`
	// Since is when it became this way, which is what makes a standing state worth
	// saying again: the state does not change and its age does.
	Since time.Time `json:"since,omitempty"`
	// Problem is why the question could not be answered. A reading that carries one
	// names no reason: a stall invented over a record nobody could read is the
	// confident emptiness every answer in this package is written to avoid.
	Problem string `json:"problem,omitempty"`
}

// Stopped reports whether anything at all is stopping the choosing.
func (s Stall) Stopped() bool { return s.Reason != "" }

// intakeClause is the one clause every surface here says about a held intake,
// which is the hold's own account of itself: who placed it and why, and — for
// a hold the brake is working itself — what the harness does about it next.
func intakeClause(hold runstate.IntakeHold) string {
	return hold.Account()
}

// Refusal is the stall as the one line a status prints against an item nothing
// will pull: what stopped it, and what lifts it.
//
// It says nothing about how long the state has stood, and that is a division of
// labour rather than an omission. Every stall that is waiting on a person is on
// the attention line as well — the switches in their own right, the two session
// states through Waiting below — and that line already carries since-when beside
// whose move it is. A refusal that repeated it would say one timestamp twice in
// one reading of four lines whose whole value is that they are read at a glance.
func (s Stall) Refusal() string {
	if !s.Stopped() {
		return ""
	}
	if s.Clears == "" {
		return s.Says
	}
	return s.Says + "; " + s.Clears
}

// Waiting is the stall as one thing waiting on a person, where it is one. It
// carries since-when, in the shape the attention line's other entries already
// say it: how long something has been waiting on somebody is half of what makes
// it worth acting on.
//
// Two reasons are excluded and neither is an oversight. The switches are already
// on the attention line in their own right — a hold waits on the operator whether
// or not it is currently what stops the choosing — and listing them again from
// here would be one state said twice in one reading. A full machine and a product
// nobody has ever watched wait on nobody: the first is the harness working, and
// the second is an operator who runs items by name getting told they have a
// problem they chose.
func (s Stall) Waiting() (Attention, bool) {
	switch s.Reason {
	case ReasonSessionIdle, ReasonNoWatchSession, ReasonProviderAway, ReasonDivergedTarget:
		// All three are the operator's: the two session states because a queue
		// with ready work and nothing pulling it is a stall rather than a rest,
		// and the provider answering nobody because it is waiting on a person in
		// the one way a window is not — it is the wait the attention line exists
		// for, and the only thing that fired on it in September was a brake
		// naming the wrong remedy. Reason.Whose words the same three the same
		// way, and a test holds the two together.
		stall := s
		return Attention{Kind: AttentionStall, ID: string(s.Reason), Mover: MoverOperator, Stall: &stall}, true
	case ReasonDrainOverrun:
		// A session that stopped restarting is a factory problem rather than a
		// wait: nobody configured it, and it is the harness's to fix.
		stall := s
		return Attention{Kind: AttentionStall, ID: string(s.Reason), Mover: MoverHarness, Stall: &stall}, true
	default:
		return Attention{}, false
	}
}

// Mark names the stall durably, so a surface that repeats a standing state can
// say which one it is standing on and re-arm its clock when a different one
// takes over.
func (s Stall) Mark() string {
	if !s.Stopped() {
		return ""
	}
	return string(s.Reason) + ":" + s.Since.UTC().Format(time.RFC3339Nano)
}

// RestartGrace is how long a stop the session recorded as a restart is read as
// a session on its way back. The re-execution is given a minute and happens in
// milliseconds when it happens at all, and a restart that the operating system
// refuses writes a second stop that is an ending — but a new build that exits
// during its own startup, after the exec, writes nothing, and that is exactly
// the build a self-developing harness deploys over itself. Past this the stop
// is read as the ending it turned out to be, and the reader is told to start a
// session.
const RestartGrace = 2 * time.Minute

// DrainOverrunGrace is how long past its latest line a session whose drain
// bound has run out is read as on its way back, for as long as the bound ran
// out less than DrainOverrunAfter ago, or for as long as it is waiting out a
// promotion; past that it is read as stuck. Past the bound the session
// restarts the moment it hosts nothing, and what it can still be hosting is a
// promotion being waited out — minutes, ordinarily, hours in a forge outage —
// or a run a moment short of its claim. It says a promotion wait again at
// intervals and writes nothing else while the wait is unchanged, so this is
// longer than a restart's grace; but a session killed while it waited writes
// nothing either, and past this it is read as whatever its latest line
// otherwise says rather than as a restart nobody is making.
const DrainOverrunGrace = 30 * time.Minute

// DrainOverrunAfter is how long past its drain bound a session that has not
// restarted is read as stuck rather than on its way back. Past the bound the
// restart follows at once unless a run is at its promotion, so a session still
// draining this long after its bound is reported as a factory problem unless
// its latest line, under DrainOverrunGrace old, says it is waiting out a
// promotion; see drainOverrun.
const DrainOverrunAfter = 10 * time.Minute

// WhyNothingStarts is the one derivation of what has stopped the choosing.
//
// The order is the order an operator acts in: the switch that stops everything,
// then the one that stops the choosing, then the machine being full, then the
// sessions that do the choosing or are not there to do it. It is also the cost
// order — the watch log is read last and only where nothing before it answered.
func WhyNothingStarts(conditions Conditions) Stall {
	switch {
	case conditions.OperatorHeld:
		return Stall{
			Reason: ReasonOperatorHold,
			Says:   "all harness activity is held by the operator",
			Clears: "`yoyo resume` lifts it",
			Since:  conditions.OperatorHold.HeldAt,
		}
	case conditions.IntakeHeld:
		// Who placed it is part of the standing state rather than a detail below
		// it: a line about a stopped queue that named the wrong holder would be
		// wrong every time it was said.
		return Stall{
			Reason: ReasonIntakeHold,
			Says:   "intake is held, and " + singleLine(intakeClause(conditions.IntakeHold), maxRefusalBytes),
			Clears: "`yoyo release` lifts it",
			Since:  conditions.IntakeHold.HeldAt,
		}
	case conditions.ProviderAway:
		// Said whole rather than as a clause, as the window is and for the same
		// reason: the operator asked that when the harness is paused on the
		// provider the cause be the first words of any message that reaches him.
		return Stall{
			Reason: ReasonProviderAway,
			Says:   conditions.ProviderOutage.Says(),
			Since:  conditions.ProviderOutage.Since,
		}
	case len(conditions.Diverged) > 0:
		// The first recorded is the one said; the attention line names every
		// other beside it.
		return divergedTargetStall(conditions.Diverged[0])
	case conditions.Capacity > 0 && conditions.Running >= conditions.Capacity:
		// A session stuck past its drain bound is said even over a full machine,
		// because the runs it stopped keep their seats in flight: on 2026-10-05
		// the slots read as taken by runs that had no process behind them. The
		// watch log is read here only when the machine is full, and a log that
		// cannot be read leaves the answer the capacity gives.
		if conditions.Sessions != nil {
			if sessions, err := conditions.Sessions(); err == nil {
				if overrun, over := DrainOverrunOf(sessions, conditions.now()); over {
					return overrun
				}
			}
		}
		return Stall{
			Reason: ReasonNoCapacity,
			Says: fmt.Sprintf("every developer slot is taken: %d of %d in flight",
				conditions.Running, conditions.Capacity),
		}
	case conditions.Sessions == nil:
		return Stall{Problem: "nothing was wired to read the sessions that choose work"}
	}
	sessions, err := conditions.Sessions()
	if err != nil {
		return Stall{Problem: fmt.Sprintf("what the harness is choosing work with could not be read: %v", err)}
	}
	return whichSession(sessions, conditions.now())
}

// whichSession is the stall as the watch log has it. A session choosing work
// settles it whatever else is in the log; otherwise a live session polling an
// idle queue is the state, and a log whose every session has ended is nobody
// choosing at all — which is the state the overnight was in and the one nothing
// else says.
//
// A session that recorded itself waiting out the provider's usage window, or one
// whose dispatch is waiting out the tracker, is answered ahead of the plain idle
// one, because each looks identical to it from every other record and means the
// opposite: one is a queue nobody is pulling, and the others are a queue the
// provider will not let anybody pull yet or one already being pulled from.
func whichSession(sessions []runstate.WatchTransition, now time.Time) Stall {
	if len(sessions) == 0 {
		return Stall{
			Reason: ReasonUnwatched,
			Says:   "no watch session has ever run on this product, so nothing pulls the queue",
			Clears: "`yoyo work --watch` starts one",
		}
	}
	// Live is newest first, so the first idle session it holds is the latest one.
	live := Live(sessions)
	// A session whose drain ran out long enough ago that it should have
	// restarted is answered first of all: every other record reads it as a live
	// session, and the one thing that must be said about it is that it is stuck.
	if overrun, over := DrainOverrunOf(sessions, now); over {
		return overrun
	}
	// A session whose drain has run out has stopped the runs it hosts and is
	// restarting as soon as it hosts nothing, and one within a poll of that bound
	// has declined to pull into a free seat on purpose. Both are answered ahead
	// of everything else the log says, because from every other record each is a
	// live session choosing nothing, and the one thing that must not be said
	// about either is that it wants looking at. Neither is read that way for
	// ever: a skip only while the bound it was declined for is near, and a bound
	// that has run out only for DrainOverrunGrace past the session's latest line,
	// and never past DrainOverrunAfter beyond the bound itself, where it is read
	// as stuck above. Past those the session either stopped — which its own later
	// lines say — or died, and a dead session must not go on reading as one on
	// its way back.
	// A session waiting out a check stage past its bound names the latest moment
	// that stage can run to, and is read as on its way back until then as well.
	if len(live) > 0 && live[0].Draining != nil &&
		((live[0].Draining.BoundReached && now.Before(live[0].At.Add(DrainOverrunGrace))) ||
			(live[0].Draining.BoundReached && live[0].Draining.Checking > 0 && now.Before(live[0].Draining.ChecksUntil.Add(RestartGrace))) ||
			(live[0].Draining.PullSkipped && now.Before(live[0].Draining.Until.Add(RestartGrace)))) {
		// A restart waiting on a promotion is dated from when that wait began,
		// which is the age a reader wants of it, rather than from the deploy.
		since := live[0].Draining.Since
		if waitingOnPromotion(live[0], now) {
			since = live[0].Draining.PromotingSince
		}
		return Stall{
			Reason: ReasonRedeploying,
			Says:   "the watch session is " + live[0].Draining.Says(),
			Since:  since,
		}
	}
	for _, transition := range live {
		if transition.State != runstate.WatchIdle && transition.State != runstate.WatchBlocked {
			// Watching, braked, or resumed: a session is alive and either choosing or
			// stopped by a hold, which was read before this.
			return Stall{}
		}
	}
	for _, transition := range live {
		if transition.State == runstate.WatchBlocked {
			return Stall{Reason: ReasonSessionBlocked, Says: transition.Reason, Since: transition.At}
		}
	}
	if len(live) > 0 {
		// A poll that could not read the store never reached the queue, so nothing
		// the log says about the queue is what stopped the choosing. It answers
		// first, as it does in the cause the stall alarm names.
		if live[0].RetryingRead() {
			return Stall{
				Reason: ReasonStoreUnreadable,
				Says:   "the watch session could not read the harness's store and is reading it again",
				Since:  live[0].At,
			}
		}
		if window := WaitingOnProvider(sessions); window.Standing(now) {
			return Stall{
				Reason: ReasonProviderWindow,
				Says:   window.Says(),
				Since:  window.Since,
			}
		}
		// A session idle because the slot it would fill is held by its own dispatch,
		// waiting out the tracker before it claims anything, has found work rather
		// than none. The oldest wait is the one said, because it is the one that has
		// held its slot longest.
		if waiting := WaitingOnTracker(sessions, now); len(waiting) > 0 {
			return Stall{
				Reason: ReasonTrackerWait,
				Says:   waiting[0].Says(),
				Since:  waiting[0].At,
			}
		}
		return Stall{
			Reason: ReasonSessionIdle,
			Says:   "the watch session has found nothing it can start",
			Since:  live[0].At,
		}
	}
	var stopped runstate.WatchTransition
	for _, transition := range sessions {
		if transition.State == runstate.WatchStopped && transition.At.After(stopped.At) {
			stopped = transition
		}
	}
	// A stop the session recorded as a restart is a session on its way back
	// rather than a line that went down — for as long as a restart takes. A
	// restart that then did not happen writes a second, later stop that says
	// so, and that one is read here instead; a new build that died in its own
	// startup writes nothing, and is read past the grace as the ending it was.
	if stopped.Restarting && now.Before(stopped.At.Add(RestartGrace)) {
		return Stall{
			Reason: ReasonRedeploying,
			Says:   "the watch session stopped to restart into the build deployed over it and is on its way back",
			Since:  stopped.At,
		}
	}
	return Stall{
		Reason: ReasonNoWatchSession,
		Says:   "no watch session is running, so nothing pulls the queue",
		Clears: "`yoyo work --watch` starts one",
		Since:  stopped.At,
	}
}

// DrainOverrunOf is the latest live session in the watch log having drained
// past its bound without restarting, where it has: the reading every surface
// takes of a session stuck there, as a factory problem and as why nothing
// starts.
func DrainOverrunOf(sessions []runstate.WatchTransition, now time.Time) (Stall, bool) {
	live := Live(sessions)
	if len(live) == 0 {
		return Stall{}, false
	}
	return drainOverrun(live[0], now)
}

// drainOverrun is a live session's drain having run out DrainOverrunAfter or
// more ago with no restart recorded since. An older record waiting out a check
// stage past the bound is left to the reading of it below until its deadline.
//
// A session waiting out a run at its promotion is not stuck however long past
// the bound that runs: a promotion is never stopped part-way and has no bound
// of its own, and the session restarts the moment it ends. It is read that way
// for as long as its line saying so is under DrainOverrunGrace old — the
// session says it again at intervals while it waits — and past that it is a
// session that died waiting, read as stuck like any other.
func drainOverrun(latest runstate.WatchTransition, now time.Time) (Stall, bool) {
	drain := latest.Draining
	if drain == nil || !drain.BoundReached || drain.Checking > 0 || now.Before(drain.Until.Add(DrainOverrunAfter)) {
		return Stall{}, false
	}
	if waitingOnPromotion(latest, now) {
		return Stall{}, false
	}
	return Stall{
		Reason: ReasonDrainOverrun,
		Says: fmt.Sprintf("the watch session %s has been draining past its bound since %s: the bound of %s ran out with the runs it hosted stopped and preserved, and it has not restarted into the build deployed over it, so it pulls no new work",
			latest.SessionID, localMoment(drain.Until), drain.Bound()),
		Since: drain.Until,
	}, true
}

// waitingOnPromotion is a live session's latest line saying, recently enough
// to be believed, that the session is past its drain bound and waiting out a
// run at its promotion.
func waitingOnPromotion(latest runstate.WatchTransition, now time.Time) bool {
	drain := latest.Draining
	return drain != nil && drain.BoundReached && drain.Promoting > 0 && now.Before(latest.At.Add(DrainOverrunGrace))
}

// divergedTargetStall is a diverged target as the stall says it: the record's
// own sentence, with the recovery as what clears it.
func divergedTargetStall(diverged runstate.DivergedTarget) Stall {
	return Stall{
		Reason: ReasonDivergedTarget,
		Says:   diverged.Says(),
		Clears: runstate.DivergedTargetRecovery,
		Since:  diverged.Since,
	}
}

// divergedTargetAttention is a diverged target as the attention line carries it
// where it is not what stops the choosing: the same stall entry, so the line
// says it in the words it would have used either way.
func divergedTargetAttention(diverged runstate.DivergedTarget) Attention {
	stall := divergedTargetStall(diverged)
	return Attention{Kind: AttentionStall, ID: divergedTargetAttentionID(diverged), Mover: MoverOperator, Stall: &stall}
}

// divergedTargetAttentionID keys a diverged target's attention entry to its
// branch, so two branches diverged at once are two entries a surface reading
// the structured line can tell apart rather than two carrying one id.
func divergedTargetAttentionID(diverged runstate.DivergedTarget) string {
	return string(ReasonDivergedTarget) + ":" + diverged.TargetBranch
}
