package orchestrator

// A change the harness's merge queue hands back only because the target moved
// under it while it waited.
//
// Such a change was checked and approved, and nothing about it has been judged
// wrong since: the queue could not build a candidate because the approved head
// no longer merges onto the target as it now stands. Stopping its run for the
// development manager would spend a decision and a repair attempt, or a fresh
// run, on what is one round of bringing the change up to date. So each of the
// first runstate.MaxReconciliations times it happens to a run, the hand-back
// gives the run a reconciliation attempt, as a run's own landing gets for a
// conflict (State.UseReconciliation, continueOnRebaseConflict in pipeline.go):
// the run is put back live at its developer attempt with the conflict recorded
// as that attempt, before any
// developer is invoked, and the reconciling sweep hosts it (ContinueUpdates).
// The resumed run moves the change onto the target with the disagreement left
// in the worktree, hands it to a new developer session (conflictresolution.go),
// checks it and has it reviewed again, and admits it to the queue again — all on the run's
// own record, charged to neither the repair attempts nor the run count.
//
// What comes first is the wait for entries ahead that touch the conflicting
// files, and a try at merging the change as it is
// (mergequeueconflictwait.go).
//
// Nothing about the checks or the review is loosened: the reconciled change
// earns both again exactly as a repaired one does. What follows the attempt is
// what follows any developer attempt: a failing check or a review that does
// not approve goes round the repair loop under the run's repair attempts, and a
// run with none left stops for the development manager as it always has. The
// conflict after the last attempt finds them all used and is handed back as
// before, with the stop saying so (catchUpRefusal).
//
// A change can wait in the queue long enough for the maintenance sweep to
// remove its run's checkout, and those long waits are the ones most likely to
// meet a moved target. The run's branch still holds the approved head, so the
// checkout is rebuilt from it (rebuildCheckout, through RestoreWorktree, the
// same restoration a resumed promotion uses) and the attempt goes ahead as it
// would have with the checkout in place. Only where the branch, the recorded
// head, or work the branch does not hold is missing does the hand-back get the
// stop, and the stop names which.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueCatchUpRuns is the run store a catch-up is made through: the run
// is adopted under its lease, and made live again in the same act that takes
// its developer slot. It is satisfied by *runstate.Store.
type MergeQueueCatchUpRuns interface {
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	ReserveLive(ctx context.Context, live runstate.State, maxConcurrent int) error
	Update(ctx context.Context, runID string, update runstate.RunUpdate) (runstate.State, error)
}

// MergeQueueCatchUpCheckouts rebuilds a run's checkout that the maintenance
// sweep removed, from the branch at the head the run recorded, so the run can be
// given its catch-up in it. It is satisfied by *gitworktree.Manager.
type MergeQueueCatchUpCheckouts interface {
	RestoreWorktree(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Worktree, error)
}

// MergeQueueCatchUpWaiting is a catch-up owed to a run that could not be made
// yet: the named epics hold it out, every developer slot is taken, another
// process holds the run, or the main checkout is not ready. The hand-back stays
// owed and the queue gives it again at a later pass
// (MergeQueueHandbackUndelivered); nothing is stopped and nothing is spent.
type MergeQueueCatchUpWaiting struct {
	RunID        string
	TargetBranch string
	Why          string
}

func (e MergeQueueCatchUpWaiting) Error() string {
	return fmt.Sprintf("run %s is owed its next attempt to bring its change up to date with %s, and %s, so the queue gives it at a later pass",
		e.RunID, e.TargetBranch, e.Why)
}

// catchUpRefusal says why a hand-back of a head that would not merge does not
// get the run's next attempt to catch up with the target, and is empty where
// it does. It reads only the run's record and the hand-back; the item and the
// slot are asked where the attempt is made. rebuildable is whether a checkout
// the sweep removed can be rebuilt here; where it can, a missing checkout is
// refused only for what the rebuilding needs and does not have.
func catchUpRefusal(state runstate.State, handback runstate.MergeQueueHandback, rebuildable bool) string {
	switch {
	case handback.Conflict == nil:
		return "the queue handed it back for something other than the target moving under it"
	case state.ReconciliationsSpent():
		return fmt.Sprintf("this run already used its %d attempts to bring its change up to date%s", runstate.MaxReconciliations, reconciliationWhen(state.Reconciliation))
	case state.HarnessAuthored() || state.Document != nil:
		return "the run has no developer to bring the change up to date"
	case state.Status != runstate.StatusSucceeded:
		return fmt.Sprintf("the run is %s rather than waiting on the queue", state.Status)
	case (strings.TrimSpace(state.Branch) == "" || state.BranchRemoved) && checkoutGone(state):
		return "the run's worktree is gone, and so is the branch it would be rebuilt from"
	case strings.TrimSpace(state.Branch) == "" || state.BranchRemoved:
		return "the run's branch is gone"
	case checkoutGone(state) && !rebuildable:
		return "the run's worktree is gone"
	case checkoutGone(state) && strings.TrimSpace(state.WorktreePath) == "":
		return "the run's worktree is gone and the run records no path to rebuild it at"
	case checkoutGone(state) && strings.TrimSpace(state.HarnessCommit) == "":
		return fmt.Sprintf("the run's worktree is gone and the run records no approved head on its branch %s to rebuild it from", state.Branch)
	case checkoutGone(state) && strings.TrimSpace(handback.Head()) != "" && handback.Head() != state.HarnessCommit:
		return fmt.Sprintf("the run's worktree is gone, and the head it records (%s) is not the approved head the queue held (%s), so a checkout rebuilt from its branch would not be the approved change",
			shortCommit(state.HarnessCommit), shortCommit(handback.Head()))
	case checkoutGone(state) && strings.TrimSpace(state.PreservedWorkRef) != "":
		return fmt.Sprintf("the run's worktree is gone, and the sweep that removed it kept uncommitted work on %s that its branch does not hold", state.PreservedWorkRef)
	case strings.TrimSpace(state.BaseCommit) == "" || strings.TrimSpace(state.TargetBranch) == "":
		return "the run records no base to bring up to date from"
	case strings.TrimSpace(state.ProviderSessionID) == "":
		return "the run records no developer session"
	case state.ResumptionsLeft() == 0:
		return fmt.Sprintf("its promotion has been resumed %d times, which is the record's bound", runstate.MaxIntegrationResumptions)
	}
	return ""
}

// checkoutGone reports a run whose record says its checkout is no longer
// there, or never names one.
func checkoutGone(state runstate.State) bool {
	return strings.TrimSpace(state.WorktreePath) == "" || state.WorktreeRemoved
}

func reconciliationWhen(used *runstate.Reconciliation) string {
	if used == nil || used.UsedAt.IsZero() {
		return ""
	}
	return " (at " + used.UsedAt.UTC().Format(time.RFC3339) + ")"
}

// catchUpNotTakenUp reports a run the merge queue put back for an attempt
// to catch up with its target that no process has taken up yet: the conflict
// is recorded as its reconciliation and not yet moved onto the target.
func catchUpNotTakenUp(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseDeveloping || !state.CatchingUpWithMergeQueueTarget() {
		return false
	}
	return state.ReplayConflict != nil && state.ReplayConflict.Reconciliation && !state.ReplayConflict.Moved
}

// mergeQueueCatchUpNotes is what the item is told when its run is given the
// attempt to catch up.
func mergeQueueCatchUpNotes(entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback, marker string) string {
	conflict := handback.Conflict
	return strings.Join([]string{
		fmt.Sprintf("The merge queue handed this change back because %s moved while it waited as entry %d, and the approved change no longer merges onto it (conflicting: %s).",
			entry.TargetBranch, entry.Order, nonEmpty(conflictPathsSay(conflict.Paths, conflict.Omitted), "no paths named")),
		fmt.Sprintf("Run %s gets one of its %d attempts to bring the change up to date with %s: a new developer session reconciles the conflict on top of %s, the change is checked and reviewed again, and it rejoins the queue by itself. This attempt is not a repair attempt and not a fresh run. If it does not pass its checks and review, the run goes on as any run does, and stops for the development manager once its repair attempts are spent. A conflict after its last attempt stops the run for the development manager.",
			entry.RunID, runstate.MaxReconciliations, entry.TargetBranch, entry.TargetBranch),
		marker,
	}, "\n")
}

// catchUp gives a run whose queued head would not merge onto its moved target
// its next attempt to bring the change up to date, and reports whether it did.
// false with no error is a hand-back that does not get it, which goes on to
// the stop it always got; why not is returned for that stop to say, and is
// empty where no catch-up was ever on offer.
//
// The run is written live before the item is told, so a refused save never
// leaves a note claiming an attempt nobody is making; the note is written once,
// and a hand-back given again after the run was written only finishes it.
func (h MergeQueueRunHandback) catchUp(ctx context.Context, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback, state runstate.State, marker string) (bool, string, error) {
	if h.CatchUp == nil || h.Capacity <= 0 || handback.Conflict == nil {
		return false, "", nil
	}
	notes := mergeQueueCatchUpNotes(entry, handback, marker)
	if h.Returns != nil {
		// A run admitted again since this hand-back is past it, whatever became
		// of the new entry; only its record and its item may still be owed.
		rejoined, found, err := h.Returns.Rejoined(entry)
		if err != nil {
			return false, "", fmt.Errorf("read whether run %s is back in the merge queue: %w", entry.RunID, err)
		}
		if found {
			return true, "", h.recordRejoined(ctx, entry, handback, rejoined, runstate.ConflictWait{}, marker)
		}
	}
	// A catch-up already made for this hand-back is only finished: the item is
	// told, if it was not.
	if conflict := state.ReplayConflict; conflict != nil && conflict.Reconciliation && strings.Contains(conflict.Detail, marker) && state.CatchingUpWithMergeQueueTarget() {
		return true, "", h.noteCatchUp(ctx, entry, notes)
	}
	if refusal := catchUpRefusal(state, handback, h.Checkouts != nil); refusal != "" {
		return false, refusal, nil
	}
	item, err := h.Tracker.Show(ctx, entry.WorkItemID)
	if err != nil {
		return false, "", fmt.Errorf("read %s: %w", entry.WorkItemID, err)
	}
	if item.Status != "in_progress" {
		// An item somebody blocked or closed while the change waited is not one
		// the harness puts a developer back to work on by itself.
		return false, fmt.Sprintf("its item is %s rather than in progress", nonEmpty(item.Status, "of no recorded status")), nil
	}
	var wait runstate.ConflictWait
	if h.Returns != nil {
		if wait, err = h.waitForAhead(ctx, entry, handback, state); err != nil {
			return false, "", err
		}
	}
	// Asked before the run is taken up. The hand-back stays owed, so the attempt
	// is made at the first pass the decision allows it.
	if refused := h.MayStart.refuseRun(maystart.BringUpToDate, entry.WorkItemID); refused != nil {
		return false, "", MergeQueueCatchUpWaiting{RunID: entry.RunID, TargetBranch: entry.TargetBranch, Why: refused.Error()}
	}
	adopted, lease, err := h.CatchUp.AdoptRun(ctx, entry.RunID)
	if errors.Is(err, runstate.ErrRunHeld) {
		return false, "", MergeQueueCatchUpWaiting{RunID: entry.RunID, TargetBranch: entry.TargetBranch, Why: "another process holds the run"}
	}
	if err != nil {
		return false, "", fmt.Errorf("take run %s to bring its change up to date: %w", entry.RunID, err)
	}
	defer func() { _ = lease.Release() }()
	// Read again under the lease: what was read before it is only a snapshot.
	if refusal := catchUpRefusal(adopted, handback, h.Checkouts != nil); refusal != "" {
		return false, refusal, nil
	}
	if h.Returns != nil {
		if rejoined, err := h.rejoin(ctx, entry, handback, adopted, wait, marker); rejoined || err != nil {
			return rejoined, "", err
		}
	}
	// The main checkout is asked before a cleared checkout is rebuilt, because
	// rebuilding asks it too and a refusal there would stop the run for what
	// is only a wait.
	if checkoutReadyToTakeUp(ctx, h.Checkout, entry.RunID) != nil {
		return false, "", MergeQueueCatchUpWaiting{RunID: entry.RunID, TargetBranch: entry.TargetBranch, Why: "the main checkout has uncommitted changes, so the run is not made live and holds no developer slot"}
	}
	if checkoutGone(adopted) {
		rebuilt, refusal, err := h.rebuildCheckout(ctx, adopted)
		if refusal != "" || err != nil {
			return false, refusal, err
		}
		adopted = rebuilt
	}
	live := catchUpState(adopted, entry, handback, marker, h.now())
	if err := h.CatchUp.ReserveLive(withPairs(ctx, h.SlotPairs), live, h.Capacity); err != nil {
		if capacity, taken := slotTakenMeanwhile(err); taken {
			// One waiting for the single slot it records takes no other.
			if h.Returns != nil && capacity.Slot == 0 {
				h.askForSlot(ctx, entry, wait)
			}
			return false, "", MergeQueueCatchUpWaiting{RunID: entry.RunID, TargetBranch: entry.TargetBranch, Why: slotWentElsewhere(capacity)}
		}
		return false, "", fmt.Errorf("put run %s back to bring its change up to date with %s: %w", entry.RunID, entry.TargetBranch, err)
	}
	return true, "", h.noteCatchUp(ctx, entry, notes)
}

// rebuildCheckout puts back the checkout the sweep removed from a run waiting on
// the queue, from its branch at the head the run recorded, and records on the
// run that it is there again, through the run store's narrow update
// (runstate.WorktreeRebuilt), so nothing another writer changed meanwhile is
// overwritten. It is called under the run's lease, after every
// other refusal and before the run is made live, and the record is written at
// once rather than with the put-back: a slot that went elsewhere meanwhile then
// leaves a waiting run whose record says, truly, that its checkout is back, and
// the next pass finds it there and rebuilds nothing.
//
// A checkout that cannot be rebuilt is a refusal, not an error: the branch or
// its head is not what the run recorded, and the hand-back's stop says so.
func (h MergeQueueRunHandback) rebuildCheckout(ctx context.Context, state runstate.State) (runstate.State, string, error) {
	if _, err := h.Checkouts.RestoreWorktree(ctx, worktreeOf(state)); err != nil {
		return state, fmt.Sprintf("the run's worktree is gone and could not be rebuilt from its branch %s at its approved head %s: %v",
			state.Branch, shortCommit(state.HarnessCommit), err), nil
	}
	rebuilt, err := h.CatchUp.Update(ctx, state.RunID, runstate.WorktreeRebuilt{At: h.now()})
	if err != nil {
		return state, "", fmt.Errorf("record that the worktree of run %s was rebuilt at %s: %w", state.RunID, state.WorktreePath, err)
	}
	return rebuilt, "", nil
}

// catchUpState is the run put back live at its developer attempt with the
// conflict recorded as its next reconciliation, ahead of any developer being
// invoked, so an interrupted attempt is still the one the run had. The move
// itself is the run store's (runstate.State.PutBackToCatchUpWithMergeQueue);
// this says what the conflict was and why.
func catchUpState(state runstate.State, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback, marker string, now time.Time) runstate.State {
	conflict := handback.Conflict
	paths, omitted := append([]string(nil), conflict.Paths...), conflict.Omitted
	recorded := runstate.ReplayConflict{
		TargetBranch: entry.TargetBranch, TargetCommit: conflict.TargetBase, Paths: paths, Omitted: omitted,
		Detail: boundedTail(marker+"\n"+conflict.Detail, runstate.MaxConflictDetailBytes),
		Phase:  runstate.PhaseIntegrating, RecordedAt: now.UTC(),
		Handback: state.RecordConflictHandback(queueConflictHandback(entry, *conflict, now)),
	}
	reason := fmt.Sprintf("the merge queue could not merge entry %d onto %s at %s because %s moved under it (conflicting: %s), so the run was put back for an attempt to bring the change up to date, checked and reviewed again, and admitted to the queue again; it is not a repair attempt and not a fresh run",
		entry.Order, entry.TargetBranch, shortCommit(conflict.TargetBase), entry.TargetBranch, nonEmpty(conflictPathsSay(paths, omitted), "no paths named"))
	return state.PutBackToCatchUpWithMergeQueue(recorded, boundedReason(reason), now)
}

// queueConflictHandback is the run's record of the queue handing its change
// back for a conflict, named by the entry, which is handed back once.
func queueConflictHandback(entry runstate.MergeQueueEntry, conflict runstate.MergeQueueConflict, now time.Time) runstate.ConflictHandback {
	return runstate.ConflictHandback{
		ID: entry.EntryID, From: runstate.ConflictFromMergeQueue, At: now.UTC(),
		TargetBranch: entry.TargetBranch, TargetCommit: conflict.TargetBase,
		Paths: conflict.Paths, Omitted: conflict.Omitted,
		Lines: conflict.Lines, LinesUnknown: conflict.LinesUnknown,
	}
}

// conflictPathsSay is the conflicting files in one line, with how many more
// the record does not name.
func conflictPathsSay(paths []string, omitted int) string {
	said := strings.Join(paths, ", ")
	if omitted > 0 {
		said += fmt.Sprintf(", and %d more", omitted)
	}
	return said
}

// noteCatchUp tells the item once that its run is catching up with the target.
func (h MergeQueueRunHandback) noteCatchUp(ctx context.Context, entry runstate.MergeQueueEntry, notes string) error {
	item, err := h.Tracker.Show(ctx, entry.WorkItemID)
	if err != nil {
		return fmt.Errorf("read %s: %w", entry.WorkItemID, err)
	}
	if strings.Contains(item.Notes, notes) {
		return nil
	}
	if _, err := h.Tracker.RecordOutcome(ctx, entry.WorkItemID, notes); err != nil {
		return fmt.Errorf("tell %s its run is bringing the change up to date: %w", entry.WorkItemID, err)
	}
	return nil
}
