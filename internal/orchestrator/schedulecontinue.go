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

import (
	"context"
	"errors"
	"fmt"
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
}

// nextContinuations reads the runs paused on work their items wait on and
// returns the ones this pull continues, oldest pause first, as far as the free
// developer slots and the session's --limit allow. A run whose dependency is
// still open is passed over naming what it waits on, and so is a run with a stop
// recorded on it: a stop is honoured before any continuation, and the sweep ends
// such a run at once, so this never picks it up.
//
// A run paused this way gave its developer slot back as it recorded the pause
// (runstate.State.HoldsDeveloperSlot), so the pull's occupied runs do not count
// it and continuing it takes a free slot exactly as a pulled item does. A run
// that is counted there already holds one and takes no other. Either way the
// store's own capacity check is what finally decides, when the continued run is
// adopted and takes its slot back (Store.ReclaimSlot).
//
// free and started are this pull's free slots and how many runs the session has
// started; both are what the carry-outs above left of them.
func (s Scheduler) nextContinuations(ctx context.Context, pull Pull, occupied map[string]runstate.State, mine map[string]int, refused map[string]refusedContinuation, free, started int, passOver func(workItemID, reason string)) ([]pausedContinuation, error) {
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
		if !pausedForDependency(state) && !state.CIWait.Waiting() && !ciWaitLostItsHost(pull.Runs, state, now) {
			continue
		}
		// A run this session is already continuing is in flight here under its
		// own dispatch, and the record has not caught up with it yet.
		if _, dispatched := mine[state.WorkItemID]; dispatched {
			continue
		}
		paused = append(paused, state)
	}
	// Oldest pause first. Nothing writes to a paused run until something
	// continues it, so the record's last write is the moment it paused.
	slices.SortStableFunc(paused, func(a, b runstate.State) int {
		if c := a.UpdatedAt.Compare(b.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.RunID, b.RunID)
	})
	var continuing []pausedContinuation
	for _, state := range paused {
		// What is said about a run passed over is written onto a continuation the
		// development manager asked for as well, so her request says why it has
		// not started rather than reading as carried out while nothing runs.
		passOver := func(state runstate.State, reason string) {
			if err := s.askedContinuationWaits(ctx, pull, state, reason); err != nil {
				reason += "; " + err.Error()
			}
			passOver(state.WorkItemID, reason)
		}
		// Waiting for the forge's checks: the forge is read first, which takes no
		// slot, and its decision is recorded on the run the first time it has made
		// one (readCIWait), whatever becomes of the run below.
		var decided bool
		var why string
		var readErr error
		if state.DependencyPause == nil {
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
			return continuing, fmt.Errorf("read whether run %s was asked to stop: %w", state.RunID, err)
		}
		if stopped {
			passOver(state, fmt.Sprintf("run %s is paused waiting on %s and %s asked for it to stop, so it is not continued; the next `yoyo reconcile` ends it",
				state.RunID, waitSummary(state), request.StoppedBy()))
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
			if s.Limit > 0 && started+len(continuing) >= s.Limit {
				break
			}
			if !holdsOne {
				if free < 1 {
					passOver(state, fmt.Sprintf("run %s's forge checks are decided (%s)%s, and it is continued at the first pull with a developer slot free, ahead of any new work", state.RunID, why, overdueSince(*state.CIWait, now)))
					continue
				}
				free--
			}
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
			continuing = append(continuing, pausedContinuation{state: state, holdsOne: holdsOne, reason: reason})
			continue
		}
		item, err := pull.Continuations.Show(ctx, state.WorkItemID)
		if err != nil {
			return continuing, fmt.Errorf("read what %s waits on: %w", state.WorkItemID, err)
		}
		met, err := pull.teamMergeMet(ctx)
		if err != nil {
			return continuing, err
		}
		if blockers := blockingDependencies(withoutMetDependencies(item, met)); len(blockers) > 0 {
			passOver(state, fmt.Sprintf("run %s is paused because %s waits on unfinished work: %s; it is continued at the first pull after that closes",
				state.RunID, state.WorkItemID, strings.Join(blockers, ", ")))
			continue
		}
		_, holdsOne := occupied[state.WorkItemID]
		if s.Limit > 0 && started+len(continuing) >= s.Limit {
			break
		}
		if !holdsOne {
			if free < 1 {
				passOver(state, fmt.Sprintf("run %s was paused waiting on %s, which has closed, and is continued when a developer slot is free",
					state.RunID, state.DependencyPause.Summary()))
				continue
			}
			free--
		}
		continuing = append(continuing, pausedContinuation{
			state:    state,
			holdsOne: holdsOne,
			reason: fmt.Sprintf("run %s paused at %s because %s waited on %s, which has since closed, and the harness is continuing it in its own worktree and developer session",
				state.RunID, state.UpdatedAt.UTC().Format(time.RFC3339), state.WorkItemID, state.DependencyPause.Summary()) + ownSlotMove(pull.Runs, state, incomplete, now),
		})
	}
	return continuing, nil
}

// waitSummary says what a paused run waits for, whichever kind of wait it is.
func waitSummary(state runstate.State) string {
	if state.DependencyPause != nil {
		return state.DependencyPause.Summary()
	}
	if state.CIWait != nil {
		return state.CIWait.Summary()
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
// A development manager's request to continue such a run is told why it waits.
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
		if err := s.askedContinuationWaits(ctx, pull, state, fmt.Sprintf("run %s is waiting for %s, and %s", state.RunID, waitSummary(state), why)); err != nil {
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
