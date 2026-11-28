package orchestrator

// Taking a waiting run up again, and giving its developer slot back when the
// take-up goes no further.
//
// A run waiting on work its item waits on, on its epic, or on GitHub's checks
// holds no developer slot. Run and Continue take the slot back for it
// (reclaimSlot) before resumeRun reads the item, the checkout and the provider
// again, and any of those can still refuse: the item reads blocked or closed,
// the checkout is not the harness's to work from, the provider is logged out.
// giveBackCIWaitSlot gives the slot back on such a refusal for a run waiting on
// GitHub's checks only; any other was left with the wait lifted and the slot
// taken with nobody working the run. Nothing gave that slot back but the harness's
// release of runs with no live worker, which waits for the run to go unwritten
// for a while once nothing holds its lease, and a run refused again at each
// later pull was counted taken for most of every hour (run e77ea157 on
// November 27, 2026). So a take-up that is refused before its run is under way
// puts back the wait the run was taken up from, and the slot with it
// (giveSlotBack).
//
// A take-up can also go silent between taking the slot and getting under way:
// a tracker read, a Git command or a provider check that does not return. It
// holds the run's lease all the while, so the release above never reaches it.
// That stretch is bounded (takeUpLimit): past it the take-up is ended, the run
// is left with its branch, worktree and developer session as they were, and
// the slot is given back the same way.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultTakeUpLimit is how long a run taken up again may go from taking its
// developer slot back to being under way before the take-up is ended. What
// happens in that stretch is a few tracker reads, Git's readiness check and
// the provider's checks; one tracker read is allowed two minutes
// (beads.DefaultTimeout), so ten minutes outlasts several that each run to
// that limit. It is the same bound the harness waits before it frees the slot
// of a run nothing holds (runstate.UnheldRunQuiet).
const DefaultTakeUpLimit = 10 * time.Minute

// takeUpLimit is the configured bound, or the default where none is set.
func (p Pipeline) takeUpLimit() time.Duration {
	if p.TakeUpLimit > 0 {
		return p.TakeUpLimit
	}
	return DefaultTakeUpLimit
}

// TakeUpTimeoutError is a take-up ended because it had not got its run under
// way within the limit. It unwraps to what the step it was stuck in returned.
type TakeUpTimeoutError struct {
	RunID      string
	WorkItemID string
	Limit      time.Duration
	Cause      error
}

func (e TakeUpTimeoutError) Error() string {
	return fmt.Sprintf("run %s of %s was taken up again but was not under way %s after it took its developer slot back, so the harness stopped taking it up; the run keeps its branch, worktree and developer session as they were, gives the slot back, and is taken up again at a later pull: %v",
		e.RunID, e.WorkItemID, e.Limit, e.Cause)
}

func (e TakeUpTimeoutError) Unwrap() error { return e.Cause }

// takeUp takes a run Run or Continue adopted up again: it takes the run's
// developer slot back where its wait gave it up, and resumes it. A refusal
// before the run is under way gives the slot back (giveSlotBack), whatever the
// run waited on; a refusal reclaimSlot itself makes took no slot. The caller
// holds the run's lease.
func (p Pipeline) takeUp(ctx context.Context, adopted runstate.State, item beads.WorkItem, publishing bool, skipped string, waitedForEpic bool) (Outcome, error) {
	reclaimed, err := p.reclaimSlot(ctx, adopted)
	if err != nil {
		return Outcome{}, err
	}
	outcome, err := p.resumeRun(ctx, reclaimed, item, publishing, skipped, waitedForEpic)
	// A wait on the forge is given back first, by the update that also records
	// on the run why it was not taken up (giveBackCIWaitSlot); giveSlotBack then
	// changes nothing on it, and gives back every other wait.
	outcome, err = p.refusedAfterTakeUp(ctx, reclaimed.RunID, outcome, err)
	if err != nil && !outcome.continuationAccepted {
		if giveErr := p.giveSlotBack(ctx, adopted); giveErr != nil {
			err = errors.Join(err, giveErr)
		}
	}
	return outcome, err
}

// gaveUpItsSlot reports a run recorded waiting in a way that holds no
// developer slot, which a take-up then took back: paused on its item's
// dependencies, waiting for its epic, or waiting on GitHub's checks — including
// one an earlier take-up marked as holding the slot to read the forge and then
// lost (ciWaitLostItsHost).
func gaveUpItsSlot(state runstate.State) bool {
	return state.AwaitsContinuation() || (state.CIWait != nil && state.CIWait.SlotTaken)
}

// giveSlotBack puts back the wait a refused take-up lifted, so the run holds
// no developer slot again and the next pull offers it exactly as before.
// waiting is the run as it was adopted, before its slot was taken back. It is
// the run store's narrow update (runstate.SlotGivenBack), made on the record as
// stored, because a refusal on the way may have written to it
// (refuseDispatchEnvironmentally); only a run still in flight and holding a
// slot by its record is changed, so one the refusal retired, or one already
// back to waiting, is left as it is.
func (p Pipeline) giveSlotBack(ctx context.Context, waiting runstate.State) error {
	if !gaveUpItsSlot(waiting) {
		return nil
	}
	// The slot is given back even where the caller's context has ended: a
	// take-up stopped by a shutdown is one more that should not hold a slot.
	_, err := p.Store.Update(context.WithoutCancel(ctx), waiting.RunID, runstate.SlotGivenBack{
		DependencyPause:  waiting.DependencyPause,
		EpicWait:         waiting.EpicWait,
		ContinuationWait: waiting.ContinuationWait,
		CIWait:           waiting.CIWait,
		At:               p.clock().Now(),
	})
	if err != nil {
		return fmt.Errorf("give back the developer slot of run %s after taking it up again was refused: %w", waiting.RunID, err)
	}
	return nil
}

// boundTakeUp is ctx bounded by the take-up limit, for the steps resumeRun
// takes before the run is under way. ended turns what one of those steps
// returned into the take-up's own refusal once the bound has passed and the
// caller's context has not ended for a reason of its own.
func (p Pipeline) boundTakeUp(ctx context.Context, state runstate.State) (bounded context.Context, cancel context.CancelFunc, ended func(error) error) {
	limit := p.takeUpLimit()
	bounded, cancel = context.WithTimeout(ctx, limit)
	ended = func(err error) error {
		if err == nil || ctx.Err() != nil || !errors.Is(bounded.Err(), context.DeadlineExceeded) {
			return err
		}
		return TakeUpTimeoutError{RunID: state.RunID, WorkItemID: state.WorkItemID, Limit: limit, Cause: err}
	}
	return bounded, cancel, ended
}
