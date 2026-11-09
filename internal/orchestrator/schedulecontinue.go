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

import (
	"context"
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
// everything behind it.
const continuationRetry = 15 * time.Minute

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
	var paused []runstate.State
	for _, state := range incomplete {
		if !pausedForDependency(state) {
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
				state.RunID, state.DependencyPause.Summary(), request.StoppedBy()))
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
		continuing = append(continuing, pausedContinuation{
			state:    state,
			holdsOne: holdsOne,
			reason: fmt.Sprintf("run %s paused at %s because %s waited on %s, which has since closed, and the harness is continuing it in its own worktree and developer session",
				state.RunID, state.UpdatedAt.UTC().Format(time.RFC3339), state.WorkItemID, state.DependencyPause.Summary()),
		})
	}
	return continuing, nil
}
