package orchestrator

// A change the harness's merge queue hands back only because the target moved
// under it while it waited.
//
// Such a change was checked and approved, and nothing about it has been judged
// wrong since: the queue could not build a candidate because the approved head
// no longer merges onto the target as it now stands. Stopping its run for the
// development manager would spend a decision and a repair attempt, or a fresh
// run, on what is one round of bringing the change up to date. So the first
// time it happens to a run, the hand-back gives the run the one reconciliation
// attempt a run's own landing gets for a conflict (State.Reconciliation,
// continueOnRebaseConflict in pipeline.go): the run is put back live at its
// developer attempt with the conflict recorded as that attempt, before any
// developer is invoked, and the reconciling sweep hosts it (ContinueUpdates).
// The resumed run moves the change onto the target with the disagreement left
// in the worktree, hands its developer the reconciliation prompt, checks it and
// has it reviewed again, and admits it to the queue again — all on the run's
// own record, charged to neither the repair attempts nor the run count.
//
// Nothing about the checks or the review is loosened: the reconciled change
// earns both again exactly as a repaired one does. What follows the attempt is
// what follows any developer attempt: a failing check or a review that does
// not approve goes round the repair loop under the run's repair attempts, and a
// run with none left stops for the development manager as it always has. A
// second conflict in the queue finds the reconciliation used and is handed back
// as before, with the stop saying so (catchUpRefusal).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueCatchUpRuns is the run store a catch-up is made through: the run
// is adopted under its lease, and made live again in the same act that takes
// its developer slot. It is satisfied by *runstate.Store.
type MergeQueueCatchUpRuns interface {
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	ReserveLive(ctx context.Context, live runstate.State, maxConcurrent int) error
}

// MergeQueueCatchUpWaiting is a catch-up owed to a run that could not be made
// yet: every developer slot is taken, or another process holds the run. The
// hand-back stays owed and the queue gives it again at a later pass
// (MergeQueueHandbackUndelivered); nothing is stopped and nothing is spent.
type MergeQueueCatchUpWaiting struct {
	RunID        string
	TargetBranch string
	Why          string
}

func (e MergeQueueCatchUpWaiting) Error() string {
	return fmt.Sprintf("run %s is owed its one attempt to bring its change up to date with %s, and %s, so the queue gives it at a later pass",
		e.RunID, e.TargetBranch, e.Why)
}

// catchUpRefusal says why a hand-back of a head that would not merge does not
// get the run's one attempt to catch up with the target, and is empty where
// it does. It reads only the run's record and the hand-back; the item and the
// slot are asked where the attempt is made.
func catchUpRefusal(state runstate.State, handback runstate.MergeQueueHandback) string {
	switch {
	case handback.Conflict == nil:
		return "the queue handed it back for something other than the target moving under it"
	case state.ReconciliationUsed():
		return fmt.Sprintf("this run already used its one attempt to bring its change up to date%s", reconciliationWhen(state.Reconciliation))
	case state.HarnessAuthored() || state.Document != nil:
		return "the run has no developer to bring the change up to date"
	case state.Status != runstate.StatusSucceeded:
		return fmt.Sprintf("the run is %s rather than waiting on the queue", state.Status)
	case strings.TrimSpace(state.Branch) == "" || state.BranchRemoved:
		return "the run's branch is gone"
	case strings.TrimSpace(state.WorktreePath) == "" || state.WorktreeRemoved:
		return "the run's worktree is gone"
	case strings.TrimSpace(state.BaseCommit) == "" || strings.TrimSpace(state.TargetBranch) == "":
		return "the run records no base to bring up to date from"
	case strings.TrimSpace(state.ProviderSessionID) == "":
		return "the run records no developer session"
	case state.ResumptionsLeft() == 0:
		return fmt.Sprintf("its promotion has been resumed %d times, which is the record's bound", runstate.MaxIntegrationResumptions)
	}
	return ""
}

func reconciliationWhen(used *runstate.Reconciliation) string {
	if used == nil || used.UsedAt.IsZero() {
		return ""
	}
	return " (at " + used.UsedAt.UTC().Format(time.RFC3339) + ")"
}

// catchUpNotTakenUp reports a run the merge queue put back for its one attempt
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
			entry.TargetBranch, entry.Order, nonEmpty(strings.Join(conflict.Paths, ", "), "no paths named")),
		fmt.Sprintf("Run %s gets its one attempt to bring the change up to date with %s: its developer reconciles the conflict on top of %s, the change is checked and reviewed again, and it rejoins the queue by itself. This attempt is not a repair attempt and not a fresh run. If it does not pass its checks and review, the run goes on as any run does, and stops for the development manager once its repair attempts are spent.",
			entry.RunID, entry.TargetBranch, entry.TargetBranch),
		marker,
	}, "\n")
}

// catchUp gives a run whose queued head would not merge onto its moved target
// its one attempt to bring the change up to date, and reports whether it did.
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
	// A catch-up already made for this hand-back is only finished: the item is
	// told, if it was not.
	if conflict := state.ReplayConflict; conflict != nil && conflict.Reconciliation && strings.Contains(conflict.Detail, marker) && state.CatchingUpWithMergeQueueTarget() {
		return true, "", h.noteCatchUp(ctx, entry, notes)
	}
	if refusal := catchUpRefusal(state, handback); refusal != "" {
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
	adopted, lease, err := h.CatchUp.AdoptRun(ctx, entry.RunID)
	if errors.Is(err, runstate.ErrRunHeld) {
		return false, "", MergeQueueCatchUpWaiting{RunID: entry.RunID, TargetBranch: entry.TargetBranch, Why: "another process holds the run"}
	}
	if err != nil {
		return false, "", fmt.Errorf("take run %s to bring its change up to date: %w", entry.RunID, err)
	}
	defer func() { _ = lease.Release() }()
	// Read again under the lease: what was read before it is only a snapshot.
	if refusal := catchUpRefusal(adopted, handback); refusal != "" {
		return false, refusal, nil
	}
	live := catchUpState(adopted, entry, handback, marker, h.now())
	if err := h.CatchUp.ReserveLive(ctx, live, h.Capacity); err != nil {
		if capacity, taken := slotTakenMeanwhile(err); taken {
			return false, "", MergeQueueCatchUpWaiting{RunID: entry.RunID, TargetBranch: entry.TargetBranch, Why: slotWentElsewhere(capacity)}
		}
		return false, "", fmt.Errorf("put run %s back to bring its change up to date with %s: %w", entry.RunID, entry.TargetBranch, err)
	}
	return true, "", h.noteCatchUp(ctx, entry, notes)
}

// catchUpState is the run put back live at its developer attempt with the
// conflict recorded as its one reconciliation, ahead of any developer being
// invoked, so an interrupted attempt is still the one the run had. The move
// itself is the run store's (runstate.State.PutBackToCatchUpWithMergeQueue);
// this says what the conflict was and why.
func catchUpState(state runstate.State, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback, marker string, now time.Time) runstate.State {
	conflict := handback.Conflict
	paths := append([]string(nil), conflict.Paths...)
	omitted := 0
	if len(paths) > runstate.MaxConflictedPaths {
		omitted = len(paths) - runstate.MaxConflictedPaths
		paths = paths[:runstate.MaxConflictedPaths]
	}
	recorded := runstate.ReplayConflict{
		TargetBranch: entry.TargetBranch, TargetCommit: conflict.TargetBase, Paths: paths, Omitted: omitted,
		Detail: boundedTail(marker+"\n"+conflict.Detail, runstate.MaxConflictDetailBytes),
		Phase:  runstate.PhaseIntegrating, RecordedAt: now.UTC(),
	}
	reason := fmt.Sprintf("the merge queue could not merge entry %d onto %s at %s because %s moved under it (conflicting: %s), so the run was put back for its one attempt to bring the change up to date, checked and reviewed again, and admitted to the queue again; it is not a repair attempt and not a fresh run",
		entry.Order, entry.TargetBranch, shortCommit(conflict.TargetBase), entry.TargetBranch, nonEmpty(strings.Join(paths, ", "), "no paths named"))
	return state.PutBackToCatchUpWithMergeQueue(recorded, boundedReason(reason), now)
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
