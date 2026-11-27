package orchestrator

// An approved change with no file differences from its base has nothing to
// put in the merge queue and nothing to promote. That is the ordinary result
// of an investigation that changed no files, a parent item whose pieces have
// all landed, and an item whose work was already on the target when it was
// admitted, and none of those is a failure: the reviewer approved the run's
// account of the work. So the run ends complete rather than failing on the
// commit it would have made (gitworktree.ErrNoChanges), and its item is
// settled exactly as it would be had a change landed:
//
//   - approved as the work, the item closes as finished;
//   - approved only as evidence, or claimed as evidence by the developer, the
//     item goes back open, parked or waiting on the work the landing named
//     (settleUndischarged), with nothing held for the development manager.
//
// Only an approval reaches here: promoteApproved is entered from an approving
// verdict, so a run that changed nothing and was not approved is handed back
// or stopped by its review as before.
//
// The branch and worktree are kept, as a run raised for design keeps them. The
// cleanup step removes only what it can prove reached the target, and a branch
// standing at its base proves nothing (CleanupIntegrated); the sweep that
// retires an unintegrated run's artifacts settles them afterwards.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// approvedUnchanged reports a promotion refused because the approved change
// has nothing above its base, which is the one refusal that ends the run
// complete instead of failed.
func approvedUnchanged(err error) bool {
	return errors.Is(err, gitworktree.ErrNoChanges)
}

// completeUnchanged ends a run whose approved change had no file differences
// from its base: the outcome is recorded on the item, the item is closed or
// put back as the landing and the approval say, and the run is made terminal
// as succeeded and complete.
func (a *activeRun) completeUnchanged(ctx context.Context) (Outcome, error) {
	p := a.pipeline
	a.state.ApprovedWithNoChanges = true
	a.outcome.ApprovedWithNoChanges = true
	a.leftLanding()
	discharges := a.state.Discharges()
	// Where the item stays open, where it goes is decided before the notes are
	// written, for the reason complete decides it first: the notes name the
	// disposition, and a tracker refusing the dependency moves it.
	var item beads.WorkItem
	decided := false
	if !discharges {
		if arranged, shown, err := arrangeUndischarged(ctx, p.Tracker, a.state); err == nil {
			a.applyUndischargedDisposition(arranged)
			item = shown
			decided = true
		}
	}
	if err := a.recordOutcomeOnce(ctx, func() string { return renderOutcomeNotes(a.outcome) }); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("record the outcome of an approved run with no file changes: %w", err)), runstate.StatusFailed)
	}
	if discharges {
		if _, err := recoveringTrackerWrite(ctx, a,
			itemLanded(p.Tracker, a.state.WorkItemID, func(item beads.WorkItem) bool { return item.Status == "closed" }),
			func(ctx context.Context) (beads.WorkItem, error) {
				return p.Tracker.Complete(ctx, a.state.WorkItemID, unchangedCompletionReason(a.outcome))
			}); err != nil {
			return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("close the work item an approved run with no file changes finished: %w", err)), runstate.StatusFailed)
		}
		a.outcome.WorkItemClosed = true
		p.closeDocketWithItem(a.state, "closed by run "+a.state.RunID+", approved with no file changes")
	} else {
		var settleErr error
		if !decided {
			settleErr = a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
				arranged, shown, err := arrangeUndischarged(ctx, p.Tracker, a.state)
				if err != nil {
					return err
				}
				a.applyUndischargedDisposition(arranged)
				item = shown
				return nil
			})
		}
		if settleErr == nil {
			settleErr = a.reopenUndischargedOnce(ctx, item)
		}
		if settleErr != nil {
			return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("reopen the work item an approved run with no file changes did not discharge: %w", settleErr)), runstate.StatusFailed)
		}
	}
	a.recordPrice()
	// The ending is a narrow update of the run as stored, so the run's status
	// moves only through the run store, carrying where the settlement left the
	// item and what the workflow observation says about ending here.
	divergenceBefore := a.state.WorkflowDivergence
	a.observe(ctx, deliveryComplete, "completed")
	divergence := ""
	if a.state.WorkflowDivergence != divergenceBefore {
		divergence = a.state.WorkflowDivergence
	}
	ended, err := p.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.UnchangedRunCompleted{
		LandingBlockedBy: a.state.LandingBlockedBy, LandingImpedimentProblem: a.state.LandingImpedimentProblem,
		Divergence: divergence, At: p.clock().Now(),
	})
	if err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("save the completed run approved with no file changes: %w", err)), runstate.StatusFailed)
	}
	a.state = ended
	a.outcome.Status = runstate.StatusSucceeded
	a.outcome.Phase = a.state.Phase
	return a.outcome, nil
}

// unchangedCompletionReason is the closure of an item whose approved run had
// no file changes. It names the base the reviewer judged, because that is the
// commit the work was found already done at.
func unchangedCompletionReason(outcome Outcome) string {
	return fmt.Sprintf("Reviewed and approved by Yoyodyne run %s with no file changes from its base %s, so nothing was put in the merge queue or integrated; closed as finished on the approval",
		outcome.RunID, shortCommit(outcome.BaseCommit))
}

// unchangedHeadline is the first line of the notes of a run approved with no
// file changes: what happened, and how its item was settled.
func unchangedHeadline(outcome Outcome) string {
	said := "Yoyodyne run passed checks and was approved by an independent reviewer, and changed no files from its base, so nothing was put in the merge queue or integrated"
	switch {
	case outcome.Landing == "" && outcome.LandingProblem == "" && outcome.ApprovalDischarges():
		return said + "; the item is closed as finished on the approval."
	case outcome.LandingDischarges() && !outcome.ApprovalDischarges():
		return said + "; the reviewer approved it as evidence rather than as the work this item asked for, so the item " + outcome.UndischargedDisposition() + "."
	case !outcome.LandingDischarges():
		return said + "; the developer did not claim it discharges this item, so the item " + outcome.UndischargedDisposition() + "."
	}
	return said + "; the item is closed as finished on the approval."
}

// unchangedUndischargedReason is what is written onto an item left open by a
// run approved with no file changes, in place of the reasons that say a change
// was integrated.
func unchangedUndischargedReason(state runstate.State) string {
	where := "stays open and parked"
	if impediment := state.LandingImpediment(); impediment != "" {
		where = "stays open, waiting on " + impediment
	}
	account := "The developer's account: " + state.LandingReason
	switch {
	case state.LandingProblem != "":
		account = "The developer claimed a landing outcome that could not be read: " + state.LandingProblem
	case state.LandingDischarges() && !state.ApprovalDischarges():
		account = "The reviewer approved it as evidence rather than as the work this item asked for: " + state.ReviewSummary
	}
	said := fmt.Sprintf("Yoyodyne run %s was approved with no file changes from its base, so nothing was put in the merge queue or integrated, and it did not discharge this item, which %s. %s",
		state.RunID, where, strings.TrimSpace(account))
	if problem := strings.TrimSpace(state.LandingImpedimentProblem); problem != "" {
		said += fmt.Sprintf(" It was parked rather than left waiting because %s.", problem)
	}
	return said
}
