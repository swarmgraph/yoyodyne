package orchestrator

// Moving a queued change between the harness's queue and the forge's: the
// explicit transfer in docs/designs/integration-through-a-merge-queue.md,
// "Crash recovery and disabling". Nothing moves an entry between modes on its
// own — not the switch being turned off, not the forge answering differently
// later — so a transfer is something a person asks for (`yoyo queue transfer`).
//
// It is three steps, each recorded before the next, so a transfer interrupted
// anywhere is finished by asking again rather than repeated. First whatever
// the old mode was asked for is withdrawn, and the withdrawal has to be
// confirmed: a merge the forge may still land is never left armed beside a
// new entry for the same change, so a withdrawal the forge has not confirmed
// refuses the transfer and nothing moves. Then the entry is released for the
// move, naming the mode it moves to. Then the change is admitted again in that
// mode, in the place the released entry had, and the released entry keeps its
// generations, its landing record and its release as history. The new entry
// has no candidate: it is built, checked and reviewed from nothing.

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueTransferRefusal is a transfer that moved nothing, and why.
type MergeQueueTransferRefusal struct {
	EntryID string
	Reason  string
}

func (e MergeQueueTransferRefusal) Error() string {
	return fmt.Sprintf("merge queue entry %s was not moved: %s", e.EntryID, e.Reason)
}

// Transfer moves one entry's change to the given queue mode, and reports the
// entry that holds it there.
func (p MergeQueuePromoter) Transfer(ctx context.Context, key runstate.MergeQueueKey, entryID string, mode runstate.MergeQueueMode) (runstate.MergeQueueEntry, error) {
	if err := p.validate(); err != nil {
		return runstate.MergeQueueEntry{}, err
	}
	refuse := func(format string, args ...any) (runstate.MergeQueueEntry, error) {
		return runstate.MergeQueueEntry{}, MergeQueueTransferRefusal{EntryID: entryID, Reason: fmt.Sprintf(format, args...)}
	}
	if mode != runstate.MergeQueueHarness && mode != runstate.MergeQueueForge {
		return refuse("%q is neither the harness's queue nor the forge's", mode)
	}
	evidence, refusal, err := p.transferEvidence(ctx, key.TargetBranch, mode)
	if err != nil {
		return runstate.MergeQueueEntry{}, err
	}
	if refusal != "" {
		return refuse("%s", refusal)
	}
	worker, held, err := p.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return runstate.MergeQueueEntry{}, err
	}
	if !held {
		return runstate.MergeQueueEntry{}, ErrMergeQueueWorkerBusy
	}
	defer func() { _ = worker.Release() }()
	entry, landing, err := p.entry(key, entryID)
	if err != nil {
		return runstate.MergeQueueEntry{}, err
	}
	reason := fmt.Sprintf("it is moving to the %s queue by an explicit transfer", modeName(mode))
	switch handback := landing.Handback; {
	case landing.Completion != nil:
		return refuse("its change has already landed")
	case handback != nil && (handback.Continuation != runstate.MergeQueueReleased || handback.TransferTo != mode):
		return refuse("it already left the queue (%s), so there is nothing to move", handback.Continuation)
	case handback == nil && entry.Mode == mode:
		return refuse("it is already in the %s queue", modeName(mode))
	case handback == nil:
		q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
		if err := q.withdraw(ctx, reason); err != nil {
			return runstate.MergeQueueEntry{}, err
		}
		if !q.outcome.Withdrawn {
			// A merge whose withdrawal the forge has not confirmed may still land,
			// so the change is not admitted anywhere else while it stands.
			return refuse("what the %s queue was asked for is not confirmed withdrawn (%s)", modeName(entry.Mode),
				nonEmpty(q.outcome.Unresolved, nonEmpty(q.outcome.Refusal, nonEmpty(q.outcome.Waiting, "no answer was recorded"))))
		}
		if err := q.release("its queued merge was withdrawn: "+reason, mode); err != nil {
			return runstate.MergeQueueEntry{}, err
		}
	}
	moved, _, err := p.Queue.Transfer(ctx, key, entryID, mode, evidence, p.Pipeline.clock().Now())
	if err != nil {
		return runstate.MergeQueueEntry{}, err
	}
	p.recordReadmission(moved)
	return moved, nil
}

// transferEvidence is the mode evidence a transfer admits under, observed now,
// and a refusal where the mode cannot be used. The forge's queue is moved to
// only where it qualifies today, exactly as at admission; the harness's queue
// is moved to unless the target lands only through the forge's queue.
func (p MergeQueuePromoter) transferEvidence(ctx context.Context, branch string, mode runstate.MergeQueueMode) (runstate.MergeQueueModeEvidence, string, error) {
	now := p.Pipeline.clock().Now().UTC()
	observed := publish.QueueCapabilities{TargetBranch: branch, ObservedAt: now}
	if p.Forge != nil {
		var err error
		if observed, err = p.Forge.QueueCapabilities(ctx, branch); err != nil {
			return runstate.MergeQueueModeEvidence{}, "", fmt.Errorf("ask the forge what its merge queue for %s establishes: %w", branch, err)
		}
	}
	selection, err := queuemode.Select(branch, observed, p.Harness, now)
	if err != nil {
		return runstate.MergeQueueModeEvidence{}, "", err
	}
	switch {
	case selection.Hold != nil:
		return runstate.MergeQueueModeEvidence{}, "neither queue can land into " + branch + ": " + selection.Hold.Requirement, nil
	case mode == runstate.MergeQueueForge && selection.Mode != runstate.MergeQueueForge:
		return runstate.MergeQueueModeEvidence{}, "the forge's queue cannot be used: " + selection.Evidence.Explanation, nil
	}
	evidence := selection.Evidence
	if mode == runstate.MergeQueueHarness && selection.Mode == runstate.MergeQueueForge {
		evidence.Explanation = "Moved to the harness's queue by an explicit transfer, although the forge's queue for " + branch + " qualifies."
	}
	return evidence, "", nil
}

// recordReadmission names the entry that now holds a run's change on the
// run's own record, so a reader of the run finds where its change waits. The
// queue's record is the authority, so a run record that will not take it is
// not a failure of the transfer.
func (p MergeQueuePromoter) recordReadmission(entry runstate.MergeQueueEntry) {
	if p.Pipeline == nil || p.Pipeline.Store == nil {
		return
	}
	state, err := p.Pipeline.Store.Load(entry.RunID)
	if err != nil || state.MergeQueue == nil {
		return
	}
	admitted := entry.AdmissionRecord()
	state.MergeQueue = &admitted
	state.UpdatedAt = p.Pipeline.clock().Now()
	_ = p.Pipeline.Store.Save(state)
}

func modeName(mode runstate.MergeQueueMode) string {
	if mode == runstate.MergeQueueForge {
		return "forge's"
	}
	return "harness's"
}
