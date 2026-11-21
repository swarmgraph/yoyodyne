package orchestrator

// A repair run that updates the pull request of an earlier run
// (docs/designs/integrating-to-a-local-branch.md, section 2, "Deciding a repair
// of an item whose run has ended").
//
// The run was started from the change the request's kept branch carries, applied
// onto the target branch as it stands now (runstate.Selection.Lift). It
// publishes by replacing that branch's head, not by cutting a branch and opening
// a request of its own, so the same request carries the repaired change and the
// reviewer judges the whole change against the target branch.
//
// The replacing push is a compare and swap against the head the harness last put
// on the branch. A branch holding anything else, such as a commit a person pushed
// there, is refused and left as it is: the run's change is kept in its own
// checkout and branch, and the development manager's list gets one entry naming
// the branch and the commit found there. That run stops in the ordinary way.

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ContinuationPublisher puts a run's change at the head of an existing remote
// branch and replaces that head again after a replay. It is satisfied by
// *gitworktree.Manager, and asked for only by a run whose selection names a
// pull request to continue, so a manager without it costs every other run
// nothing.
type ContinuationPublisher interface {
	PublishOnto(ctx context.Context, worktree gitworktree.Worktree, message, branch, previousCommit string) (gitworktree.Publication, error)
	RepublishBranchAs(ctx context.Context, worktree gitworktree.Worktree, branch, previousCommit string) (gitworktree.Publication, error)
}

var _ ContinuationPublisher = (*gitworktree.Manager)(nil)

// continuation is the pull request this run's change goes onto, or nil for a run
// that opens its own.
func (a *activeRun) continuation() *runstate.Continuation {
	if a.state.Selection == nil {
		return nil
	}
	return a.state.Selection.Continues
}

// continuedHead is the head the harness last put on the continued branch: the
// one this run pushed, where it has pushed, and the earlier run's otherwise.
func (a *activeRun) continuedHead(cont runstate.Continuation) string {
	if earlier := a.state.PullRequest; earlier != nil && earlier.Number == cont.Number && earlier.Branch == cont.Branch && earlier.HeadCommit != "" {
		return earlier.HeadCommit
	}
	return cont.HeadCommit
}

// publishContinuation is publishAttempt for a run that continues a request.
func (a *activeRun) publishContinuation(ctx context.Context, cont runstate.Continuation) error {
	publisher, ok := a.pipeline.Worktrees.(ContinuationPublisher)
	if !ok {
		return fmt.Errorf("this run repairs pull request #%d on %s, and the worktree manager it was given cannot put a change on an existing branch", cont.Number, cont.Branch)
	}
	previous := a.continuedHead(cont)
	publication, err := recoveringValue(ctx, a, runstate.RetryPublishBranch, func(ctx context.Context) (gitworktree.Publication, error) {
		published, publishErr := publisher.PublishOnto(ctx, a.worktree, attemptMessage(a.item, a.outcome), cont.Branch, previous)
		if published.Commit != "" {
			a.recordHarnessCommit(published.Commit)
		}
		return published, publishErr
	})
	if errors.Is(err, gitworktree.ErrNoChanges) {
		return nil
	}
	if err != nil {
		var moved *gitworktree.RemoteBranchMoved
		if errors.As(err, &moved) {
			a.listMovedBranch(cont, moved)
			return fmt.Errorf("the change was not published, because %s on pull request #%d holds a commit the harness did not write (%s), and it is not overwritten; the repaired change is kept in this run's checkout and branch, and the development manager has been told: %w", cont.Branch, cont.Number, commitOrNothing(moved.Found), err)
		}
		return fmt.Errorf("publish the repaired change onto %s: %w", cont.Branch, err)
	}
	pullRequest, err := recoveringValue(ctx, a, runstate.RetryOpenPullRequest, func(ctx context.Context) (publish.PullRequest, error) {
		return a.pipeline.Publisher.Ensure(ctx, publish.Request{
			Head:  publication.Branch,
			Base:  a.worktree.TargetBranch,
			Title: pullRequestTitle(a.item, a.outcome),
			Body:  pullRequestBody(a.item, a.outcome, a.worktree.TargetBranch, a.pipeline.automatic(), a.pipeline.factoryLine(ctx)),
			Mark:  forgeMark(ctx, a.pipeline.Factory, a.pipeline.Config.Product.ID),
		})
	})
	if err != nil {
		return fmt.Errorf("update the pull request for %s: %w", cont.Branch, err)
	}
	if pullRequest.Number != cont.Number {
		return fmt.Errorf("asked to update pull request #%d, the forge answered with #%d on %s; nothing was recorded as published", cont.Number, pullRequest.Number, cont.Branch)
	}
	pushedAt := a.pipeline.clock().Now()
	published := &runstate.PullRequest{
		Remote: publication.Remote, Branch: publication.Branch, Number: pullRequest.Number, URL: pullRequest.URL,
		HeadCommit: publication.Commit, State: pullRequest.State, Merged: pullRequest.Merged, PushedAt: &pushedAt,
	}
	if earlier := a.state.PullRequest; earlier != nil && earlier.Number == published.Number && earlier.Feedback != nil {
		published.Feedback = earlier.Feedback
	}
	a.state.PullRequest = published
	a.outcome.PullRequest = published
	a.state.UpdatedAt = pushedAt
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the published pull request: %w", err)
	}
	// The request is this run's wait from here, so the earlier run's is over:
	// two runs never wait on one request.
	if _, err := a.pipeline.Store.Update(ctx, cont.RunID, runstate.PersonMergeContinued{PullRequest: cont.Number, RunID: a.state.RunID, At: pushedAt}); err != nil {
		return fmt.Errorf("hand pull request #%d on from run %s: %w", cont.Number, cont.RunID, err)
	}
	return nil
}

// listMovedBranch puts the refusal on the development manager's list, once for
// the commit found. A run that cannot file it says so in its report problem
// rather than failing a second time.
func (a *activeRun) listMovedBranch(cont runstate.Continuation, moved *gitworktree.RemoteBranchMoved) {
	if a.pipeline.Reports == nil {
		a.noteReportProblem(domain.RoleDevelopmentManager, errors.New("nothing collects reports for this run, so the refused push to a kept branch was not put on the development manager's list"))
		return
	}
	found := "the branch is gone from the remote"
	if moved.Found != "" {
		found = "the remote holds commit " + moved.Found
	}
	message := fmt.Sprintf("The repair of %s from feedback on pull request #%d was not published: its branch %s no longer holds commit %s, which the harness last put there, and %s, so the push was refused and nothing on the branch was overwritten. The repaired change is kept in run %s's checkout and branch. Whether to take the commit found there in, or to replace it, is yours to decide.",
		a.state.WorkItemID, cont.Number, cont.Branch, shortCommit(moved.Expected), found, a.state.RunID)
	entry := report.Entry{Severity: report.SeverityWarning, Message: oneline.Fold(message, report.MaxMessageBytes), For: domain.RoleDevelopmentManager}
	collected, err := report.Collect([]report.Entry{entry}, report.Attribution{
		Role: report.HarnessReporter, RunID: a.state.RunID, WorkItemID: a.state.WorkItemID, Build: a.state.Build,
		ProductID: a.pipeline.Config.Product.ID, RepositoryID: string(a.pipeline.Config.Product.RepositoryID),
	}, a.pipeline.clock().Now())
	if err == nil {
		for _, reported := range collected {
			if err = a.pipeline.Reports.Append(reported); err != nil {
				break
			}
		}
	}
	if err != nil {
		a.noteReportProblem(domain.RoleDevelopmentManager, err)
	}
}

// commitOrNothing names a commit for a sentence, or says there is none.
func commitOrNothing(commit string) string {
	if commit == "" {
		return "nothing"
	}
	return shortCommit(commit)
}
