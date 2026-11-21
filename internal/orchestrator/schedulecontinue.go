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
// continuationRetry, and one that has to wait for its own slot is passed over
// naming that slot rather than started to be refused, so a fresh pull never
// takes the slot it is waiting for while it sits out a retry.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
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
	// pinnedSlots is the slots the continuations chosen so far in this pass will
	// take up, by the run that takes each. A run pinned to its slot's endpoint
	// pair is taken up only in that slot, so a second one chosen for the same
	// slot in this pass would be refused at its start, having spent a free slot
	// that another continuation or fresh work could have used.
	pinnedSlots := map[int]string{}
	pinnedBeside := func(state runstate.State) (int, string) {
		if number := state.RecordedSlot(); number != 0 && state.Routing.Developer != nil {
			if holder, claimed := pinnedSlots[number]; claimed {
				return number, holder
			}
		}
		return 0, ""
	}
	claimSlot := func(state runstate.State) {
		if number := state.RecordedSlot(); number != 0 && state.Routing.Developer != nil {
			pinnedSlots[number] = state.RunID
		}
	}
	for _, state := range paused {
		if last, tried := refused[state.RunID]; tried {
			again := last.at.Add(continuationRetry)
			if s.now().Before(again) {
				passOver(state.WorkItemID, fmt.Sprintf("continuing run %s was refused at %s: %s; it is attempted again from %s",
					state.RunID, last.at.UTC().Format(time.RFC3339), last.why, again.UTC().Format(time.RFC3339)))
				continue
			}
		}
		request, stopped, err := pull.Continuations.StopRequested(state.RunID)
		if err != nil {
			return continuing, fmt.Errorf("read whether run %s was asked to stop: %w", state.RunID, err)
		}
		if stopped {
			passOver(state.WorkItemID, fmt.Sprintf("run %s is paused waiting on %s and %s asked for it to stop, so it is not continued; the next `yoyo reconcile` ends it",
				state.RunID, waitSummary(state), request.StoppedBy()))
			continue
		}
		if state.DependencyPause == nil {
			// Waiting for the forge's checks: read the forge, which takes no slot,
			// and continue once it has decided (cichecks.go).
			decided, why, err := s.ciWaitDecided(ctx, pull, state)
			if err != nil {
				passOver(state.WorkItemID, fmt.Sprintf("run %s is waiting for %s and the forge could not be read: %v", state.RunID, waitSummary(state), err))
				continue
			}
			if !decided {
				passOver(state.WorkItemID, fmt.Sprintf("run %s is waiting for %s (waited %s): %s", state.RunID, waitSummary(state), state.CIWait.Waited(now).Round(time.Second), why))
				continue
			}
			if slot, holder := slotWaitedFor(pull.Runs, state, incomplete, now); slot != 0 {
				passOver(state.WorkItemID, fmt.Sprintf("run %s's forge checks are decided (%s)%s, and it is continued at the first pull after developer slot %d is free: it keeps the endpoint pair that slot is configured with, and run %s holds the slot now",
					state.RunID, why, overdueSince(*state.CIWait, now), slot, holder))
				continue
			}
			if slot, holder := pinnedBeside(state); slot != 0 {
				passOver(state.WorkItemID, fmt.Sprintf("run %s's forge checks are decided (%s)%s, and it is continued at the first pull after developer slot %d is free: it keeps the endpoint pair that slot is configured with, and run %s is being continued into the slot in this pass",
					state.RunID, why, overdueSince(*state.CIWait, now), slot, holder))
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
					passOver(state.WorkItemID, fmt.Sprintf("run %s's forge checks are decided (%s)%s, and it is continued at the first pull with a developer slot free, ahead of any new work", state.RunID, why, overdueSince(*state.CIWait, now)))
					continue
				}
				free--
			}
			reason := fmt.Sprintf("run %s waited for %s since %s and the forge has decided (%s), so the harness is continuing it in its own worktree and developer session", state.RunID, waitSummary(state), state.CIWait.PushedAt.UTC().Format(time.RFC3339), why)
			if ciWaitLostItsHost(pull.Runs, state, now) {
				reason = fmt.Sprintf("run %s waited for %s since %s and was being continued when the program carrying it ended; the forge has decided (%s), so the harness is continuing it in its own worktree and developer session", state.RunID, waitSummary(state), state.CIWait.PushedAt.UTC().Format(time.RFC3339), why)
			}
			claimSlot(state)
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
			passOver(state.WorkItemID, fmt.Sprintf("run %s is paused because %s waits on unfinished work: %s; it is continued at the first pull after that closes",
				state.RunID, state.WorkItemID, strings.Join(blockers, ", ")))
			continue
		}
		if slot, holder := slotWaitedFor(pull.Runs, state, incomplete, now); slot != 0 {
			passOver(state.WorkItemID, fmt.Sprintf("run %s was paused waiting on %s, which has closed, and it is continued at the first pull after developer slot %d is free: it keeps the endpoint pair that slot is configured with, and run %s holds the slot now",
				state.RunID, state.DependencyPause.Summary(), slot, holder))
			continue
		}
		if slot, holder := pinnedBeside(state); slot != 0 {
			passOver(state.WorkItemID, fmt.Sprintf("run %s was paused waiting on %s, which has closed, and it is continued at the first pull after developer slot %d is free: it keeps the endpoint pair that slot is configured with, and run %s is being continued into the slot in this pass",
				state.RunID, state.DependencyPause.Summary(), slot, holder))
			continue
		}
		_, holdsOne := occupied[state.WorkItemID]
		if s.Limit > 0 && started+len(continuing) >= s.Limit {
			break
		}
		if !holdsOne {
			if free < 1 {
				passOver(state.WorkItemID, fmt.Sprintf("run %s was paused waiting on %s, which has closed, and is continued when a developer slot is free",
					state.RunID, state.DependencyPause.Summary()))
				continue
			}
			free--
		}
		claimSlot(state)
		continuing = append(continuing, pausedContinuation{
			state:    state,
			holdsOne: holdsOne,
			reason: fmt.Sprintf("run %s paused at %s because %s waited on %s, which has since closed, and the harness is continuing it in its own worktree and developer session",
				state.RunID, state.UpdatedAt.UTC().Format(time.RFC3339), state.WorkItemID, state.DependencyPause.Summary()),
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

// slotWaitedFor is the developer slot a continued run would have to wait for,
// and the run holding it now. A run that keeps the endpoint pair of the slot it
// recorded is taken up only in that slot (runstate.Store.takeUpSlot), so while
// another run holds the slot a continuation would only be dispatched to be
// refused. It is zero where the run could be taken up now. The store makes the
// final decision when the run takes its slot back; this is the same reading
// made from the records the pull already holds, so the pull neither spends a
// free slot on a run that cannot use it nor reads the tracker for it.
func slotWaitedFor(runs ScheduleRuns, state runstate.State, incomplete []runstate.State, now time.Time) (int, string) {
	number := state.RecordedSlot()
	if number == 0 || state.Routing.Developer == nil {
		return 0, ""
	}
	for _, other := range incomplete {
		if other.RunID == state.RunID || other.RecordedSlot() != number {
			continue
		}
		if holdsDeveloperSlot(runs, other, now) {
			return number, other.RunID
		}
	}
	return 0, ""
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
