package orchestrator

// Continuing a run paused on work its item waits on.
//
// A run that reaches a gate boundary while its item waits on unfinished work
// records the pause and exits, keeping its claim, its branch, its worktree, and
// its developer session. Its item stays claimed, so no pull ever offers it
// again, and until yoyodyne-ifd.428.51 the only thing that continued it was
// somebody typing `yoyo run` once the work it waited on closed — a hand step,
// and one nobody is told to take. So every pull reads the runs paused this way,
// asks the tracker what each item still waits on, and continues the ones whose
// dependencies have all closed, in the same way `yoyo run` would have.
//
// A run waiting for the forge's checks on the commit it pushed (cichecks.go) is
// continued here too, once the forge has decided or its wait limit has passed,
// and so is one whose program ended while it was being continued
// (ciWaitLostItsHost). A continuation is work on a change already written, so it
// goes ahead of new work: one turned away only because a developer slot was not
// free is attempted again at the next pull rather than left for
// continuationRetry, so a fresh pull never takes the slot it is waiting for
// while it sits out a retry. It takes the first free slot whichever slot it
// recorded: where that one is held by another run it is moved, and a run that
// keeps its slot's endpoint pair is switched to the new slot's own endpoints
// (runstate.Store.takeUpSlot, through Pipeline.withSlotPairs).
//
// Paused runs and the decisions that build on a kept change are both written
// work, and freed slots go to them in one order: work carrying a free slot's
// labels first, then the item's priority, then the longest wait
// (writtenWorkOrder). A run passed over says why on its
// own record (runstate.ContinuationWait), which status shows.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ScheduleContinuations is what a pull reads to continue runs paused on work
// their items wait on: the item as the tracker holds it now, and a stop somebody
// recorded on the run. It is optional, and a pull wired without one continues
// nothing, leaving such a run to `yoyo run` as before. The continuation is
// recorded on the item by the run itself, where its pause is actually lifted,
// so a continuation refused before it got that far writes nothing there.
type ScheduleContinuations interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
	StopRequested(runID string) (runstate.StopRequest, bool, error)
}

// continuationRetry is how long a continuation that came back without the run
// going on — declined, failed before the run was adopted, or paused again — is
// left before a pull attempts it again. It is the carry-out's own pacing for a
// gate shut for one item, and for the same reason: an unpaced retry would take
// a developer slot at every poll for as long as the cause stood, and crowd out
// everything behind it. A continuation turned away only because no developer
// slot was free is not paced (lostTheSlot): the slot freeing is the whole of
// what it waits for, and pacing it would hand that slot to new work.
const continuationRetry = 15 * time.Minute

// lostTheSlot reports a continuation turned away only because the developer
// slot it needed was not free when it came to take it.
func lostTheSlot(done completed) bool {
	var capacity runstate.CapacityError
	return done.err != nil && errors.As(done.err, &capacity) && !capacity.Landings
}

// refusedForUnreadyCheckout reports a continuation turned away because the
// primary checkout was not the harness's to work from. Like a lost slot it is
// not paced: the run was refused before it took a slot back
// (Pipeline.reclaimSlot), so trying again costs nothing but the look, and the
// pull that finds the checkout clean is the one that should continue it.
func refusedForUnreadyCheckout(done completed) bool {
	return done.err != nil && errors.Is(done.err, gitworktree.ErrPrimaryNotReady)
}

// refusedContinuation is what a session remembers of a continuation that did
// not go on: when it came back, and what it said.
type refusedContinuation struct {
	at  time.Time
	why string
}

// continuationRefused reads a finished continuation for whether the run went on,
// and says why not where it did not. A run that was adopted and then ended —
// integrated, stopped, or failed — is no longer paused, so no pull would read it
// again whatever this says; what is left is the continuation that never got the
// run going, and the one that brought it back paused.
func continuationRefused(done completed, started Started) (string, bool) {
	switch {
	case started.Declined != "":
		return started.Declined, true
	case done.err != nil:
		return done.err.Error(), true
	case done.outcome.Paused:
		return "it came back paused rather than going on", true
	}
	return "", false
}

// pausedContinuation is one paused run a pull continues: the run as its record
// stands, and whether it already holds a developer slot, so continuing it takes
// none of the free ones.
type pausedContinuation struct {
	state    runstate.State
	holdsOne bool
	reason   string
	// priority is the run's item's priority in the Lead Product Manager's order,
	// math.MaxInt where it could not be read, and named is the item by its title
	// with its identifier after it, for the sentences a person reads.
	priority int
	named    string
	// labels is the run's item's labels as the pull read them, and labelled is
	// whether a free slot at this pull prefers one of them, which puts the run
	// ahead of written work carrying none (writtenWorkOrder).
	labels   []string
	labelled bool
	// fullSlots is the pass-over reason for a run that is ready to be continued
	// but finds no developer slot free.
	fullSlots string
	// ready is when the forge's decision was first read for a run waiting on
	// it, and zero for a run paused on its dependencies (waitingSince).
	ready time.Time
}

// waitingSince is when a paused run became ready to be taken up, which is
// what its place among the written work waiting for a slot is measured from,
// as the decision on dedicated developer slots (decision 3) counts it: for a
// run waiting on the forge, when the scheduler first read the forge's decision
// (runstate.CIWait.Decided), or now where this pull is the first to read it;
// for a run paused on its dependencies, which records no time for their
// closing, the moment it paused. Nothing writes to a paused run's record's
// updated time until something continues it (the forge's decision and the
// reasons it is passed over are recorded without touching it), so that time is
// the moment it paused.
func (c pausedContinuation) waitingSince() time.Time {
	if !c.ready.IsZero() {
		return c.ready
	}
	return c.state.UpdatedAt
}

// writtenWorkOrder is the one order among written work waiting for a developer
// slot — paused runs to continue and decisions that build on a kept change
// alike: work whose item carries a free slot's labels first (labelled), so a
// freed slot takes written work on its own labels before written work on any
// other, as the dedicated-slots decision's first step for written work says;
// then the Lead Product Manager's priority; then the longest wait. Ties fall to
// the run identifier so the order is the same at every pull.
func writtenWorkOrder(labelledA bool, priorityA int, sinceA time.Time, runA string, labelledB bool, priorityB int, sinceB time.Time, runB string) int {
	if labelledA != labelledB {
		if labelledA {
			return -1
		}
		return 1
	}
	if c := cmp.Compare(priorityA, priorityB); c != 0 {
		return c
	}
	if c := sinceA.Compare(sinceB); c != 0 {
		return c
	}
	return strings.Compare(runA, runB)
}

// continuationsAhead names the paused runs in candidates that the written-work
// order puts ahead of a decision about an item at priority, decided at since,
// and labelled where its item carries a free slot's labels. A decision that
// does not build on a kept change is fresh work, and every paused run is ahead
// of it.
func continuationsAhead(candidates []pausedContinuation, kept, labelled bool, priority int, since time.Time, runID string) []string {
	var ahead []string
	for _, candidate := range candidates {
		if candidate.holdsOne {
			continue
		}
		if kept && writtenWorkOrder(candidate.labelled, candidate.priority, candidate.waitingSince(), candidate.state.RunID, labelled, priority, since, runID) > 0 {
			continue
		}
		ahead = append(ahead, candidate.named)
	}
	return ahead
}

// nextContinuations reads the runs paused on work their items wait on and
// returns the ones this pull continues, as far as the free developer slots and
// the session's --limit allow (continuationCandidates, then takeContinuations).
func (s Scheduler) nextContinuations(ctx context.Context, pull Pull, occupied map[string]runstate.State, mine map[string]int, refused map[string]refusedContinuation, free, started int, passOver func(workItemID, reason string)) ([]pausedContinuation, error) {
	candidates, err := s.continuationCandidates(ctx, pull, occupied, mine, refused, nil, passOver)
	return s.takeContinuations(ctx, pull, candidates, mine, free, started, nil, passOver), err
}

// takeContinuations gives the free developer slots to the paused runs ready to
// be continued, in the written-work order (writtenWorkOrder): the highest
// priority first, then the run that has waited longest. Until
// yoyodyne-ifd.428.188 they went oldest pause first whatever their priority,
// and on 2026-11-21 a priority 0 run whose checks had passed was passed over at
// two freed slots for older priority 1 work. firedAhead names the decisions this
// pull already gave slots to, which the written-work order put ahead of these
// (Scheduler.Schedule), so a run left without one says what took them.
func (s Scheduler) takeContinuations(ctx context.Context, pull Pull, candidates []pausedContinuation, mine map[string]int, free, started int, firedAhead []string, passOver func(workItemID, reason string)) []pausedContinuation {
	ordered := slices.Clone(candidates)
	// Two runs at one priority that became ready at the same moment, as two
	// whose forge decisions this pull is the first to read, go earlier pause
	// first.
	slices.SortStableFunc(ordered, func(a, b pausedContinuation) int {
		if a.labelled == b.labelled && a.priority == b.priority && a.waitingSince().Equal(b.waitingSince()) {
			if c := a.state.UpdatedAt.Compare(b.state.UpdatedAt); c != 0 {
				return c
			}
		}
		return writtenWorkOrder(a.labelled, a.priority, a.waitingSince(), a.state.RunID, b.labelled, b.priority, b.waitingSince(), b.state.RunID)
	})
	var continuing []pausedContinuation
	var taken []string
	for _, candidate := range ordered {
		state := candidate.state
		// A decision this pull carried out about the same item is in flight under
		// its own dispatch, and one item is never given two runs at once.
		if _, dispatched := mine[state.WorkItemID]; dispatched {
			continue
		}
		if s.Limit > 0 && started+len(continuing) >= s.Limit {
			why := fmt.Sprintf("%s; the session was bounded to %d run(s) by its --limit, and they have been started", candidate.fullSlots, s.Limit)
			if ahead := append(slices.Clone(firedAhead), taken...); len(ahead) > 0 {
				why += fmt.Sprintf("; the written work this pull took ahead of it, taken where it carries a free developer slot's own labels first, then by priority and then by longest wait, was %s", listedNames(ahead))
			}
			s.continuationWaits(ctx, pull, state, why, passOver)
			continue
		}
		if !candidate.holdsOne {
			if free < 1 {
				why := candidate.fullSlots
				if ahead := append(slices.Clone(firedAhead), taken...); len(ahead) > 0 {
					why += fmt.Sprintf("; the developer slots free at this pull went to written work ahead of it, taken where it carries a free developer slot's own labels first, then by priority and then by longest wait: %s", listedNames(ahead))
				}
				s.continuationWaits(ctx, pull, state, why, passOver)
				continue
			}
			free--
		}
		taken = append(taken, candidate.named)
		continuing = append(continuing, candidate)
	}
	return continuing
}

// continuationWaits says why a paused run was not continued at this pull: on
// the pass's report, on a continuation the development manager asked for, and
// on the run's own record, where status reads it (runstate.ContinuationWait).
// A write that fails is said in the reason; the next pull writes it again.
func (s Scheduler) continuationWaits(ctx context.Context, pull Pull, state runstate.State, reason string, passOver func(workItemID, reason string)) {
	if err := s.askedContinuationWaits(ctx, pull, state, reason); err != nil {
		reason += "; " + err.Error()
	}
	if err := s.recordContinuationWait(ctx, pull, state, reason); err != nil {
		reason += "; " + err.Error()
	}
	passOver(state.WorkItemID, reason)
}

// recordContinuationWait writes onto a paused run why this pull did not
// continue it (runstate.ContinuationPassedOver), where the runs take such a
// write and the record does not already say it.
func (s Scheduler) recordContinuationWait(ctx context.Context, pull Pull, state runstate.State, why string) error {
	why = strings.TrimSpace(why)
	if why == "" || (state.ContinuationWait != nil && state.ContinuationWait.Why == why) {
		return nil
	}
	updates, ok := pull.Runs.(ScheduleRunUpdates)
	if !ok {
		return nil
	}
	if _, err := updates.Update(ctx, state.RunID, runstate.ContinuationPassedOver{Why: why, At: s.now()}); err != nil {
		return fmt.Errorf("record on run %s why it was not continued: %w", state.RunID, err)
	}
	return nil
}

// continuationCandidates reads the runs paused on work their items wait on and
// returns the ones that may be continued now, before any developer slot is
// given out; takeContinuations gives the slots. A run whose dependency is
// still open is passed over naming what it waits on, and so is a run with a stop
// recorded on it: a stop is honoured before any continuation, and the sweep ends
// such a run at once, so this never picks it up. itemOf, where given, is the
// pull's reading of the work items, which says each run's item's priority; a
// run whose item it does not hold is ordered after every run whose priority was
// read.
//
// A run paused this way gave its developer slot back as it recorded the pause
// (runstate.State.HoldsDeveloperSlot), so the pull's occupied runs do not count
// it and continuing it takes a free slot exactly as a pulled item does. A run
// that is counted there already holds one and takes no other. Either way the
// store's own capacity check is what finally decides, when the continued run is
// adopted and takes its slot back (Store.ReclaimSlot).
//
// The candidates are read before the decisions are carried out, so the
// written-work order can keep a slot for a paused run that outranks a decision
// (continuationsAhead).
func (s Scheduler) continuationCandidates(ctx context.Context, pull Pull, occupied map[string]runstate.State, mine map[string]int, refused map[string]refusedContinuation, itemOf func(string) (beads.WorkItem, bool), passOver func(workItemID, reason string)) ([]pausedContinuation, error) {
	if pull.Continuations == nil || pull.Runs == nil {
		return nil, nil
	}
	incomplete, err := pull.Runs.Incomplete()
	if err != nil {
		return nil, fmt.Errorf("read the runs paused on work their items wait on: %w", err)
	}
	now := s.now()
	var paused []runstate.State
	for _, state := range incomplete {
		// A run that resolves a conflict with the team's branch is taken up by the
		// update it is for (teambranchresolve.go).
		if state.TeamConflict != nil {
			continue
		}
		if !pausedForDependency(state) && !pausedForEpic(state) && !state.CIWait.Waiting() && !ciWaitLostItsHost(pull.Runs, state, now) {
			continue
		}
		// A run this session is already continuing is in flight here under its
		// own dispatch, and the record has not caught up with it yet.
		if _, dispatched := mine[state.WorkItemID]; dispatched {
			continue
		}
		paused = append(paused, state)
	}
	var candidates []pausedContinuation
	// candidate is a run ready to be continued, with its item's priority and
	// name as the pull read them.
	candidate := func(state runstate.State, holdsOne bool, reason, fullSlots string, item *beads.WorkItem) pausedContinuation {
		found := pausedContinuation{state: state, holdsOne: holdsOne, reason: reason, fullSlots: fullSlots, priority: math.MaxInt, named: state.WorkItemID, labels: state.WorkItemLabels}
		if item == nil && itemOf != nil {
			if read, known := itemOf(state.WorkItemID); known {
				item = &read
			}
		}
		if item != nil {
			found.priority = item.Priority
			if len(item.Labels) > 0 {
				found.labels = item.Labels
			}
			if title := strings.TrimSpace(item.Title); title != "" {
				found.named = fmt.Sprintf("%s (%s)", title, state.WorkItemID)
			}
		}
		return found
	}
	for _, state := range paused {
		// What is said about a run passed over is written onto the run and onto a
		// continuation the development manager asked for as well, so the run and
		// her request say why it has not started rather than reading as carried out
		// while nothing runs.
		passOver := func(state runstate.State, reason string) {
			s.continuationWaits(ctx, pull, state, reason, passOver)
		}
		// Waiting for the forge's checks: the forge is read first, which takes no
		// slot, and its decision is recorded on the run the first time it has made
		// one (readCIWait), whatever becomes of the run below.
		var decided bool
		var why string
		var readErr error
		if state.DependencyPause == nil && state.CIWait != nil {
			decided, why, readErr = s.readCIWait(ctx, pull, state)
		}
		if last, tried := refused[state.RunID]; tried {
			again := last.at.Add(continuationRetry)
			if s.now().Before(again) {
				passOver(state, fmt.Sprintf("continuing run %s was refused at %s: %s; it is attempted again from %s",
					state.RunID, last.at.UTC().Format(time.RFC3339), last.why, again.UTC().Format(time.RFC3339)))
				continue
			}
		}
		request, stopped, err := pull.Continuations.StopRequested(state.RunID)
		if err != nil {
			return candidates, fmt.Errorf("read whether run %s was asked to stop: %w", state.RunID, err)
		}
		if stopped {
			passOver(state, fmt.Sprintf("run %s is paused waiting on %s and %s asked for it to stop, so it is not continued; the next `yoyo reconcile` ends it",
				state.RunID, waitSummary(state), request.StoppedBy()))
			continue
		}
		if refused := pull.MayStart.refuseContinuing(state); refused != nil {
			passOver(state, refused.Error())
			continue
		}
		// Nothing but its epic: whatever else its item waits on is asked as the run
		// is taken up.
		if state.DependencyPause == nil && state.CIWait == nil {
			_, holdsOne := occupied[state.WorkItemID]
			candidates = append(candidates, candidate(state, holdsOne,
				fmt.Sprintf("run %s waited for its epic since %s and may go on now, so the harness is continuing it in its own worktree and developer session", state.RunID, state.EpicWait.Since.UTC().Format(time.RFC3339))+ownSlotMove(pull.Runs, state, incomplete, now),
				fmt.Sprintf("run %s waited for its epic, may go on now, and is continued when a developer slot is free", state.RunID), nil))
			continue
		}
		if state.DependencyPause == nil {
			// Continued once the forge has decided (cichecks.go).
			if readErr != nil {
				passOver(state, fmt.Sprintf("run %s is waiting for %s and the forge could not be read: %v", state.RunID, waitSummary(state), readErr))
				continue
			}
			if !decided {
				passOver(state, fmt.Sprintf("run %s is waiting for %s (pushed at %s): %s; it is continued at the first pull after the forge decides or the wait limit passes", state.RunID, waitSummary(state), state.CIWait.PushedAt.Local().Format("2006-01-02 15:04 MST"), why))
				continue
			}
			// A run whose program ended after it took its slot back holds none now
			// (ciWaitLostItsHost), so it takes a free one like a run still waiting.
			_, holdsOne := occupied[state.WorkItemID]
			holdsOne = holdsOne && !ciWaitLostItsHost(pull.Runs, state, now)
			fullSlots := fmt.Sprintf("run %s's forge checks are decided (%s)%s, and it is continued at the first pull with a developer slot free, ahead of any new work", state.RunID, why, overdueSince(*state.CIWait, now))
			reason := fmt.Sprintf("run %s waited for %s since %s and the forge has decided (%s), so the harness is continuing it in its own worktree and developer session", state.RunID, waitSummary(state), state.CIWait.PushedAt.UTC().Format(time.RFC3339), why)
			if ciWaitLostItsHost(pull.Runs, state, now) {
				reason = fmt.Sprintf("run %s waited for %s since %s and was being continued when the program carrying it ended; the forge has decided (%s), so the harness is continuing it in its own worktree and developer session", state.RunID, waitSummary(state), state.CIWait.PushedAt.UTC().Format(time.RFC3339), why)
			}
			reason += ownSlotMove(pull.Runs, state, incomplete, now)
			// A continuation the development manager asked for on this run is
			// carried out by this one start (askedContinuationLeftToThePull), and
			// the run marks her request taken up as it goes on.
			if asked := state.RecoveryContinuation; state.RecoveryContinuationOutstanding(runstate.RecoverContinue) {
				reason += fmt.Sprintf("; this carries out the development manager's request %s to continue it", asked.RequestID)
			}
			found := candidate(state, holdsOne, reason, fullSlots, nil)
			found.ready = now
			if state.CIWait != nil && state.CIWait.Decided != nil {
				found.ready = state.CIWait.Decided.At
			}
			candidates = append(candidates, found)
			continue
		}
		item, err := pull.Continuations.Show(ctx, state.WorkItemID)
		if err != nil {
			return candidates, fmt.Errorf("read what %s waits on: %w", state.WorkItemID, err)
		}
		met, err := pull.teamMergeMet(ctx)
		if err != nil {
			return candidates, err
		}
		if blockers := blockingDependencies(withoutMetDependencies(item, met)); len(blockers) > 0 {
			passOver(state, fmt.Sprintf("run %s is paused because %s waits on unfinished work: %s; it is continued at the first pull after that closes",
				state.RunID, state.WorkItemID, strings.Join(blockers, ", ")))
			continue
		}
		_, holdsOne := occupied[state.WorkItemID]
		fullSlots := fmt.Sprintf("run %s was paused waiting on %s, which has closed, and is continued when a developer slot is free",
			state.RunID, state.DependencyPause.Summary())
		reason := fmt.Sprintf("run %s paused at %s because %s waited on %s, which has since closed, and the harness is continuing it in its own worktree and developer session",
			state.RunID, state.UpdatedAt.UTC().Format(time.RFC3339), state.WorkItemID, state.DependencyPause.Summary()) + ownSlotMove(pull.Runs, state, incomplete, now)
		candidates = append(candidates, candidate(state, holdsOne, reason, fullSlots, &item))
	}
	return candidates, nil
}

// waitSummary says what a paused run waits for, whichever kind of wait it is.
func waitSummary(state runstate.State) string {
	if state.DependencyPause != nil {
		return state.DependencyPause.Summary()
	}
	if state.CIWait != nil {
		return state.CIWait.Summary()
	}
	if state.EpicWait != nil {
		return state.EpicWait.Summary()
	}
	return "nothing recorded"
}

// ciWaitLostItsHost reports a run that took a developer slot back to read the
// forge for its wait (runstate.CIWait.SlotTaken) and whose program ended before
// it read the forge and ended or renewed the wait — a restart stopping the
// session part-way through the continuation, or the process dying. Nothing else
// takes such a run up: it carries no stop for a restart to re-adopt, and its
// record still says it holds a slot, so it reads as neither waiting nor
// stopped. Its worker being gone (holdsDeveloperSlot) is what says nobody is
// behind it, and the pull continues it as it would a run still waiting. Where
// the runs cannot say whether a worker is alive, nothing is read as lost.
func ciWaitLostItsHost(runs ScheduleRuns, state runstate.State, now time.Time) bool {
	if state.CIWait == nil || !state.CIWait.SlotTaken || state.RedeployStop != nil || !pausedForCIWait(state) {
		return false
	}
	return !holdsDeveloperSlot(runs, state, now)
}

// ownSlotMove is a clause for the reason a continued run is started with,
// where the developer slot it recorded is held by another run: the run is
// taken up in a free slot instead, ahead of new work, and where it keeps its
// slot's endpoint pair it is switched to the new slot's own endpoints
// (runstate.Store.takeUpSlot, which makes the move and records it). It is
// empty where the run's own slot is free or it records none.
func ownSlotMove(runs ScheduleRuns, state runstate.State, incomplete []runstate.State, now time.Time) string {
	number := state.RecordedSlot()
	if number == 0 {
		return ""
	}
	for _, other := range incomplete {
		if other.RunID == state.RunID || other.RecordedSlot() != number || !holdsDeveloperSlot(runs, other, now) {
			continue
		}
		if state.Routing.Developer != nil {
			return fmt.Sprintf("; developer slot %d, which it recorded, is held by run %s, so it is taken up in the free slot and switched to that slot's own endpoints, with the move recorded on the run", number, other.RunID)
		}
		return fmt.Sprintf("; developer slot %d, which it recorded, is held by run %s, so it is taken up in the free slot, with the move recorded on the run", number, other.RunID)
	}
	return ""
}

// overdueSince is a clause saying when a wait for the forge's checks passed
// its limit, for a pass-over line about a run that is decided and still not
// taken up; empty for a wait still inside it.
func overdueSince(wait runstate.CIWait, now time.Time) string {
	if !wait.Overdue(now) {
		return ""
	}
	limit := time.Duration(wait.WaitLimitSeconds) * time.Second
	return fmt.Sprintf("; its wait passed its limit of %s at %s", limit, wait.PushedAt.Add(limit).UTC().Format(time.RFC3339))
}

// ScheduleRunUpdates makes one narrow change to a run record the pull does not
// hold, under the run's write lock (runstate.Store.Update). A pull asks it of
// its runs where they satisfy it, which *runstate.Store does: it is how the
// forge's decision on a waiting run and what a development manager's
// continuation waits on are written while no process carries the run. Runs that
// do not satisfy it have neither written, and are continued exactly as before.
type ScheduleRunUpdates interface {
	Update(ctx context.Context, runID string, update runstate.RunUpdate) (runstate.State, error)
}

// readCIWait asks whether the forge has decided a run waiting for its checks
// (ciWaitDecided) and, the first time it has, records the decision on the run
// (runstate.CIWait.Decided). The decision is recorded whether or not the run can
// be continued now: a run left waiting for a slot, or past a restart, then says
// on its record that GitHub has answered rather than reading as still waiting
// on checks that finished long ago. A record that cannot be written does not
// stop the run being continued: the failure is said in why, so the pass-over
// line or the continuation's reason carries it, and the next pull reads the
// forge and writes it again.
func (s Scheduler) readCIWait(ctx context.Context, pull Pull, state runstate.State) (bool, string, error) {
	decided, why, err := s.ciWaitDecided(ctx, pull, state)
	if err != nil || !decided || state.CIWait == nil || state.CIWait.Decided != nil {
		return decided, why, err
	}
	if updates, ok := pull.Runs.(ScheduleRunUpdates); ok {
		if _, err := updates.Update(ctx, state.RunID, runstate.CIWaitDecided{
			Commit:   state.CIWait.Commit,
			Decision: runstate.CIWaitDecision{At: s.now(), Passed: why == ciAllPassed, Says: why},
		}); err != nil {
			why += fmt.Sprintf(" (recording that decision on run %s failed, and the next pull tries again: %v)", state.RunID, err)
		}
	}
	return decided, why, nil
}

// recordCIDecisions reads the forge for every run waiting on its checks and
// records each decision it finds (readCIWait), for a pull that continues
// nothing — under the operator's pause, a held intake, or a session stepping
// down to restart. Reading GitHub spends nothing and takes no slot, so what it
// has decided is on the record whether or not anything may be continued yet.
// The run's own record, and a development manager's request to continue such a
// run, are told why it waits.
// What could not be read or written is returned, for the pass to say as its
// continuation problem; nothing here stops the pass.
func (s Scheduler) recordCIDecisions(ctx context.Context, pull Pull, why string) error {
	if pull.Runs == nil {
		return nil
	}
	incomplete, err := pull.Runs.Incomplete()
	if err != nil {
		return fmt.Errorf("read the runs waiting on the forge's checks: %w", err)
	}
	now := s.now()
	var problems []error
	for _, state := range incomplete {
		if !state.CIWait.Waiting() && !ciWaitLostItsHost(pull.Runs, state, now) {
			continue
		}
		if _, _, err := s.readCIWait(ctx, pull, state); err != nil {
			problems = append(problems, fmt.Errorf("read the forge for run %s: %w", state.RunID, err))
		}
		waits := fmt.Sprintf("run %s is waiting for %s, and %s", state.RunID, waitSummary(state), why)
		if err := s.askedContinuationWaits(ctx, pull, state, waits); err != nil {
			problems = append(problems, err)
		}
		if err := s.recordContinuationWait(ctx, pull, state, waits); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// askedContinuationWaits writes onto a continuation the development manager
// asked for on this run what the pull found it waiting on, so her request, the
// run's inspection, and the status say why it has not started instead of the
// request reading as carried out while nothing runs. A wait already written in
// the same words is left alone (runstate.RecoveryContinuationWaited). A write
// that fails is returned; the next pull writes it again.
func (s Scheduler) askedContinuationWaits(ctx context.Context, pull Pull, state runstate.State, why string) error {
	if !state.RecoveryContinuationOutstanding(runstate.RecoverContinue) || state.RecoveryContinuation.Waiting == strings.TrimSpace(why) {
		return nil
	}
	updates, ok := pull.Runs.(ScheduleRunUpdates)
	if !ok {
		return nil
	}
	if _, err := updates.Update(ctx, state.RunID, runstate.RecoveryContinuationWaited{Action: runstate.RecoverContinue, Why: why, At: s.now()}); err != nil {
		return fmt.Errorf("record on run %s what the development manager's request %s waits on: %w", state.RunID, state.RecoveryContinuation.RequestID, err)
	}
	return nil
}

// askedContinuationLeftToThePull reports a run the development manager asked to
// have continued that this pull's continuations take up rather than the
// re-adoption of runs with nobody behind them: one waiting on the forge's
// checks, or whose program ended while it was being continued from that wait.
// Such a run holds no developer slot, so re-adopting it would start it whether
// or not a slot is free — and refused for want of one, at every pull, with
// nothing written about why. The continuations read the forge for it, wait for
// the slot it can take, say on her request what it waits on, and start it once,
// which also answers a request and a decision of the forge that both name it.
func askedContinuationLeftToThePull(runs ScheduleRuns, state runstate.State, now time.Time) bool {
	return state.CIWait.Waiting() || ciWaitLostItsHost(runs, state, now)
}

// ciWaitDecided asks whether the forge has decided a waiting run. Where no
// reader is wired, only a wait past its limit is continued, and the run then
// runs the full checks on this machine.
func (s Scheduler) ciWaitDecided(ctx context.Context, pull Pull, state runstate.State) (bool, string, error) {
	if pull.CIWaits == nil {
		if state.CIWait.Overdue(s.now()) {
			return true, "the wait limit has passed", nil
		}
		return false, "no forge is wired to read the checks", nil
	}
	return pull.CIWaits.Decided(ctx, state)
}
