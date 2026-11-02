package orchestrator

// Bringing a pull request a person is to merge up to date.
//
// The forge check puts a run whose waiting request the target moved past back
// at its promotion (Reconciler.updatePersonMerge, in personmerge.go), with the
// request's merge authority already withdrawn. This is the run's half: the
// change is replayed onto the new target, the configured checks and the
// independent reviewer run on the replayed head through the ordinary gate, and
// the head is pushed to the same request, which then waits for the person
// again (activeRun.complete).
//
// A replay that conflicts is handed to the developer that wrote the change,
// with the conflicting paths named, and counted against
// runstate.MaxUpToDateConflicts rather than the repair attempts, because
// another contributor's work caused it. Once those are spent the run stops with
// its change kept, and the item goes to the development manager.
//
// The push replaces a head a person may be merging, so the request's state is
// read immediately before it. A request the forge reports merged, closed, or
// being merged is not pushed to: the run waits again with the replayed change
// kept in its worktree, and the next check settles whatever the person did.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// bringPersonMergeUpToDate replays the change of a run the forge check put back
// at its promotion onto the target that moved past its pull request, and puts
// the replayed change through the whole gate again. A conflict is handed to the
// developer (handUpToDateConflict) before the gate.
func (a *activeRun) bringPersonMergeUpToDate(ctx context.Context) (Outcome, error) {
	target := a.worktree.TargetBranch
	number := a.state.PullRequest.Number
	a.landingAt(fmt.Sprintf("replaying the change onto %s to bring pull request #%d up to date", target, number), landingAsksNothing)
	// The approval described the change on its old base, and every way out of
	// the replay changes what it described, so the replayed change earns its own
	// verdict, as a replayed promotion does. The replay's record carries this.
	a.clearReviewEvidence()
	rebase, err := a.replayOntoTarget(ctx)
	if recordErr := a.recordRebase(rebase); recordErr != nil {
		if err == nil {
			err = recordErr
		} else {
			err = withFailedRecord(err, recordErr)
		}
		return a.fail(stoppedBy(runstate.StopIntegration, err), failureStatus(ctx, err))
	}
	switch {
	case errors.Is(err, gitworktree.ErrRebaseConflict):
		a.leftLanding()
		if err := a.handUpToDateConflict(ctx, err); err != nil {
			return a.stop(ctx, err)
		}
	case err != nil:
		err = fmt.Errorf("replay the change onto %s to bring pull request #%d up to date: %w", target, number, err)
		return a.fail(stoppedBy(runstate.StopIntegration, err), failureStatus(ctx, err))
	default:
		a.leftLanding()
	}
	return a.verifyReviewAndFinish(ctx)
}

// handUpToDateConflict hands a replay that conflicted to the developer that
// wrote the change, moved onto the target with the disagreement left in the
// worktree, as continueOnRebaseConflict does at a landing. The round is counted
// before the developer is invoked, so an interrupted round is still one the run
// had, and the count is the run's own rather than the repair attempts. Past
// runstate.MaxUpToDateConflicts the run stops instead.
func (a *activeRun) handUpToDateConflict(ctx context.Context, cause error) error {
	a.recordReplayConflict(recordedReplayConflict(a.worktree, cause, a.state.Phase, a.pipeline.clock().Now().UTC()))
	if a.state.UpToDateConflicts >= runstate.MaxUpToDateConflicts {
		return a.blockOnUpToDateConflicts(cause)
	}
	// The move saves the record, so the round is counted with it.
	a.state.StartUpToDateConflictRound(a.pipeline.clock().Now())
	if err := a.moveOntoTargetForRepair(ctx); err != nil {
		return errors.Join(cause, err)
	}
	conflict := *a.state.ReplayConflict
	return a.develop(ctx, upToDateConflictPrompt(a.deliveredInvariants().Text(), a.scratch, a.pipeline.Config.Checks, conflict, a.state.UpToDateConflicts), a.state.ProviderSessionID)
}

// blockOnUpToDateConflicts ends a run whose change conflicted again once its
// rounds for bringing its pull request up to date were spent. Nothing is forced
// or resolved: the branch, the worktree and the pull request are kept as they
// are, and the item is blocked so the stoppage reaches the development
// manager's list with the conflict beside it.
func (a *activeRun) blockOnUpToDateConflicts(cause error) error {
	a.outcome.ReplayConflict = a.state.ReplayConflict
	blocked := fmt.Errorf("the change conflicted with %s while pull request #%d was being brought up to date, after the %d conflict rounds a run is given for that were spent: %w",
		a.worktree.TargetBranch, a.state.PullRequest.Number, runstate.MaxUpToDateConflicts, cause)
	if err := a.block(renderUpToDateConflictNotes(a.outcome, a.state, blocked.Error())); err != nil {
		return stoppedBy(runstate.StopIntegrationBudget, withFailedRecord(blocked, fmt.Errorf("record the conflict as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopIntegrationBudget, blocked)
}

// publishUpdated pushes the head a run brought up to date to the pull request a
// person is to merge, in place of the head the request carried, and reports
// whether the push was withheld. The request's state is read immediately before
// the push, and a request the forge reports merged, closed, or being merged is
// not pushed to: the item is told why, and the run waits for the person again.
func (a *activeRun) publishUpdated(ctx context.Context) (bool, error) {
	if !a.publishing || a.outcome.PullRequest == nil {
		return false, a.publishAttempt(ctx)
	}
	published := *a.outcome.PullRequest
	a.landingAt(fmt.Sprintf("reading pull request #%d before pushing the head brought up to date", published.Number), landingAsksForge)
	current, err := recoveringValue(ctx, a, runstate.RetryRepublishBranch, func(ctx context.Context) (publish.PullRequest, error) {
		return a.pipeline.Publisher.State(ctx, published.Branch)
	})
	if err != nil {
		return false, fmt.Errorf("read pull request #%d before pushing the head brought up to date: %w", published.Number, err)
	}
	if merging := personMergeInProgress(current, published.Number); merging != "" {
		a.leftLanding()
		note := renderWithheldUpdateNotes(a.state, merging)
		if err := a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
			_, err := a.pipeline.Tracker.RecordOutcome(ctx, a.state.WorkItemID, note)
			return err
		}); err != nil {
			return false, fmt.Errorf("record on %s that the update of pull request #%d was not pushed: %w", a.state.WorkItemID, published.Number, err)
		}
		return true, nil
	}
	if err := a.pushUpdatedHead(ctx); err != nil {
		return false, err
	}
	a.leftLanding()
	return false, nil
}

// pushUpdatedHead replaces the published head with the one the run
// now holds, from exactly the commit the harness published there, whether the
// replay applied cleanly or the developer settled a conflict on top of the
// target.
func (a *activeRun) pushUpdatedHead(ctx context.Context) error {
	head := a.state.HarnessCommit
	if head == "" {
		return fmt.Errorf("the change brought up to date has no commit of its own above %s, so there is nothing to push to pull request #%d", a.worktree.TargetBranch, a.outcome.PullRequest.Number)
	}
	return a.republishRebase(ctx, gitworktree.Rebase{HeadCommit: head})
}

// personMergeInProgress says why a request a run is about to push an updated
// head to must be left alone, and nothing for one that may be pushed to.
func personMergeInProgress(current publish.PullRequest, number int) string {
	switch {
	case current.Number != number:
		return fmt.Sprintf("the forge names pull request #%d for the branch instead of #%d", current.Number, number)
	case current.Merged:
		return "a person merged it while it was being brought up to date"
	case !strings.EqualFold(strings.TrimSpace(current.State), "open"):
		return "it was closed while it was being brought up to date"
	case current.AutoMerge:
		return "a person started a merge of it while it was being brought up to date"
	}
	return ""
}

// upToDateConflictPrompt is the conflict handed to the developer while its
// waiting pull request is brought up to date.
func upToDateConflictPrompt(invariants, scratchDirectory string, checks []string, conflict runstate.ReplayConflict, round int) string {
	return replayConflictPrompt(invariants, scratchDirectory, checks, conflict, "# Integration conflict: bring your waiting pull request up to date\n\n",
		fmt.Sprintf("Your pull request was waiting for a person to merge it when other contributors' work landed on the target. This is conflict round %d of %d for bringing it up to date; these rounds are not counted against your repair attempts.",
			round, runstate.MaxUpToDateConflicts))
}

// renderUpToDateConflictNotes is the blocker a run leaves when its conflict
// rounds for bringing its pull request up to date are spent.
func renderUpToDateConflictNotes(outcome Outcome, state runstate.State, failure string) string {
	lines := []string{
		fmt.Sprintf("Yoyodyne stopped this item: the target moved past pull request #%d while it waited for a person to merge it, and the change conflicts with what the target now holds.", state.PullRequest.Number),
		fmt.Sprintf("Nothing was force-merged, reset, or settled automatically. The developer was given %d conflict rounds for bringing the pull request up to date, which are not counted against the repair attempts, and they are spent, so the development manager decides what happens next.", runstate.MaxUpToDateConflicts),
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
	}
	if conflict := state.ReplayConflict; conflict != nil {
		if conflict.TargetCommit != "" {
			lines = append(lines, "Target branch is at: "+conflict.TargetCommit)
		}
		if len(conflict.Paths) > 0 {
			listed := "The replay stopped on: " + strings.Join(conflict.Paths, ", ")
			if conflict.Omitted > 0 {
				listed = fmt.Sprintf("%s, and %d more", listed, conflict.Omitted)
			}
			lines = append(lines, listed)
		}
	}
	lines = append(lines, "The branch, the worktree and the pull request are kept as they were.")
	return strings.Join(lines, "\n")
}

// renderWithheldUpdateNotes tells the item that the head brought up to date
// was not pushed, because a person was already merging the request.
func renderWithheldUpdateNotes(state runstate.State, why string) string {
	return strings.Join([]string{
		fmt.Sprintf("Yoyodyne did not push the update of pull request #%d: %s.", state.PullRequest.Number, why),
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"The pull request keeps the head it had, the change brought up to date stays in the run's worktree, and the next check of the forge settles what the person did.",
	}, "\n")
}
