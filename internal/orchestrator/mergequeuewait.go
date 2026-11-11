package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReconcileMergeQueues is what the sweep reads of the harness's merge queues:
// whether the entry a run's approved change was admitted as still stands, and,
// where it does not, whether the queue still lists it. It reads and never
// writes; the queue's own driver is the only thing that moves an entry.
type ReconcileMergeQueues interface {
	Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error)
	Standing(key runstate.MergeQueueKey, runID string) (runstate.MergeQueueEntry, bool, error)
}

var _ ReconcileMergeQueues = (*runstate.MergeQueueStore)(nil)

// waitsOnMergeQueue reports a run whose approved change was admitted to the
// harness's merge queue and has not landed: nothing about it is the sweep's to
// finish, because the queue lands the change and records the landing on the run
// as its integration, or hands the change back and records that.
//
// Such a run ends succeeded in its cleaning-up phase with no integration, which
// is not outstanding, so the sweep meets one only when it listed the run while
// its process was still recording the admission and adopted it once that
// process had ended — the listing is a snapshot, and a slow sweep reads it
// minutes later. Every other reading below would then call it interrupted with
// nothing integrated, which is how approved changes waiting in the queue were
// stopped as runs nothing could finish.
//
// The record keeps its admission after the queue hands the change back, and a
// repair of the change makes the run live again with the admission still on
// it. Such a run is the repair's, not the queue's, so a run still in flight is
// treated as waiting only where the queue still holds the very entry its record
// names (settleMergeQueueWait).
func waitsOnMergeQueue(state runstate.State) bool {
	return state.MergeQueue != nil && state.Integration == nil
}

// settleMergeQueueWait decides a run waiting on the merge queue from the queue
// itself, and reports whether it decided the run at all. While the entry stands
// the run is left exactly as it is, and a run whose process died after writing
// the admission and before ending is ended waiting, as that process would have
// ended it. A finished run whose entry is gone, or that the queue gave up
// without landing and without telling the run, is stopped with that as its
// reason, so the development manager is asked about a change that is still on
// its branch. A run in flight whose entry is not standing is not decided here:
// it is a handed-back change under repair, which the rest of the sweep reads.
func (r Reconciler) settleMergeQueueWait(ctx context.Context, state runstate.State) (Reconciliation, bool, error) {
	admitted := *state.MergeQueue
	waiting := fmt.Sprintf("its approved change was admitted to the %s merge queue for %s as entry %d",
		modeName(admitted.Mode), admitted.TargetBranch, admitted.Order)
	inFlight := !state.Status.Terminal()
	// A run the queue already handed back records that itself, with its blocker
	// and its reason; settling it again would put the sweep's words over the
	// queue's.
	if !inFlight && state.Status != runstate.StatusSucceeded {
		result := reconciliationOf(state, ActionBlocked)
		if strings.TrimSpace(state.Blocker) == "" {
			result.Action = ActionFailed
		}
		result.Detail = fmt.Sprintf("%s, and the run has already ended %s; it is left as it recorded itself", waiting, state.Status)
		return result, true, nil
	}
	if r.MergeQueues == nil {
		if inFlight {
			return Reconciliation{}, false, nil
		}
		// A sweep that cannot read the queue cannot say the entry is gone, and
		// the run's own record says it stands, so the run is left waiting.
		result := reconciliationOf(state, ActionQueued)
		result.Detail = waiting + "; this sweep cannot read the queue, so the run is left waiting on it"
		return result, true, nil
	}
	entry, standing, err := r.MergeQueues.Standing(admitted.Key(), state.RunID)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), true, fmt.Errorf("read the merge queue entry of run %s: %w", state.RunID, err)
	}
	if inFlight && (!standing || entry.EntryID != admitted.EntryID) {
		return Reconciliation{}, false, nil
	}
	if standing {
		if inFlight {
			// The admission is durable and the queue holds it, so the one step the
			// dead process had left was ending the run as waiting.
			ended, err := r.Store.Update(ctx, state.RunID, runstate.QueuedRunCompleted{At: r.clock().Now()})
			if err != nil {
				return reconciliationOf(state, ActionUnsettled), true, fmt.Errorf("end run %s as waiting on the merge queue: %w", state.RunID, err)
			}
			state = ended
		}
		result := reconciliationOf(state, ActionQueued)
		result.Detail = fmt.Sprintf("the run is waiting on the merge queue: its approved change is entry %d in the %s merge queue for %s, which lands it or hands it back to this run",
			entry.Order, modeName(entry.Mode), entry.TargetBranch)
		return result, true, nil
	}
	entries, err := r.MergeQueues.Entries(admitted.Key())
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), true, fmt.Errorf("read the merge queue for %s: %w", admitted.TargetBranch, err)
	}
	reason := waiting + ", and the queue no longer has that entry and landed nothing; the change is still on the run's branch"
	for _, listed := range entries {
		if listed.RunID == state.RunID {
			reason = waiting + ", and the queue let the entry go without landing it and without handing the change back to this run; the change is still on the run's branch"
			break
		}
	}
	result, err := r.stopLeftByMergeQueue(ctx, state, reason)
	return result, true, err
}

// cleanupCommit is the commit a landed run's branch and worktree are proven
// against before they are removed. A run's own promotion fast-forwards the
// target onto the run's branch, so that is its integration's source commit. The
// merge queue lands a candidate it built elsewhere — a merge commit whose second
// parent is the approved head — and records that candidate as the integration,
// so the run's own branch and worktree stand at the approved head, which the
// landed commit contains. Cleanup still proves that head is on the target before
// it removes anything. The approved head is taken only while it is still the
// run's own last commit: a run repaired after a handback and promoted some other
// way has moved on from the head it once queued.
func cleanupCommit(state runstate.State) string {
	if queued := state.MergeQueue; queued != nil && queued.ApprovedHead != "" && queued.ApprovedHead == state.HarnessCommit &&
		state.Integration.SourceCommit != queued.ApprovedHead {
		return state.MergeQueue.ApprovedHead
	}
	return state.Integration.SourceCommit
}

// stopLeftByMergeQueue stops a run whose queue entry is gone, the way the
// queue's own handback stops one: the item is blocked naming why, the run is
// failed with the same reason and its blocker, and the run is put in front of
// the development manager. The branch and worktree are left where they are.
func (r Reconciler) stopLeftByMergeQueue(ctx context.Context, state runstate.State, reason string) (Reconciliation, error) {
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), err
	}
	observation, err := r.observeArtifacts(ctx, state)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), err
	}
	notes, err := r.recordBlocker(ctx, state, itemStatus, observation, reason)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), err
	}
	stopped, err := r.Store.Update(ctx, state.RunID, runstate.MergeQueueEntryLost{
		Blocker: runstate.RecordBlocker(notes),
		Failure: runstate.RecordFailure(reason),
		At:      r.clock().Now(),
	})
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the stop of run %s: %w", state.RunID, err)
	}
	state = stopped
	result := reconciliationOf(state, ActionBlocked)
	result.Detail = reason
	result.DocketProblem = r.docketStoppedRun(state)
	return result, nil
}
