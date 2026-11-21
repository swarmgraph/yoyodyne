package orchestrator

// A run that updates the pull request an earlier run of its item opened.
//
// The development manager's repair, from feedback, of an item whose change waits
// on a pull request the harness opened for a person to merge starts a new run of
// the item (teamfeedbackrepair.go). That run is cut from the target branch and
// starts from the change the earlier run kept on its branch (runstate.Lift), so
// the reviewer judges the whole change against the target as it judges any run's.
// When its change is approved it is not published as a request of its own: it
// replaces the head of the earlier run's branch, from exactly the head the
// harness put there, and the request that branch carries is the request that
// carries it now (gitworktree.Manager.ReplaceBranch). The earlier run's wait on
// the request is then over (runstate.PersonMergeSuperseded), and this run waits
// on it the way any run does that left its approved change for a person to merge.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// BranchReplacer is the worktree manager's way of publishing onto a branch an
// earlier run published. It is satisfied by *gitworktree.Manager, and asked for
// only by a run whose selection names a request to update.
type BranchReplacer interface {
	ReplaceBranch(ctx context.Context, worktree gitworktree.Worktree, branch, previousCommit, message string) (gitworktree.Publication, error)
}

// pullRequestUpdate is the request this run was selected to update, or nil.
func (a *activeRun) pullRequestUpdate() *runstate.PullRequestUpdate {
	if a.state.Selection == nil {
		return nil
	}
	return a.state.Selection.UpdatePullRequest
}

// publishOntoPullRequest puts the run's approved change on the pull request it
// was selected to update. The request is read first and left alone when it has
// merged, closed or has a merge started on it, and the push replaces the branch
// only from the head the harness last put there.
func (a *activeRun) publishOntoPullRequest(ctx context.Context, update runstate.PullRequestUpdate) error {
	replacer, ok := a.pipeline.Worktrees.(BranchReplacer)
	if !ok {
		return fmt.Errorf("this run was selected to update pull request #%d, and the worktree manager it was given cannot publish onto an existing branch", update.Number)
	}
	previous := update.HeadCommit
	if earlier := a.state.PullRequest; earlier != nil && earlier.Number == update.Number && earlier.HeadCommit != "" {
		previous = earlier.HeadCommit
	}
	current, err := recoveringValue(ctx, a, runstate.RetryRepublishBranch, func(ctx context.Context) (publish.PullRequest, error) {
		return a.pipeline.Publisher.State(ctx, update.Branch)
	})
	if err != nil {
		return fmt.Errorf("read pull request #%d before updating it: %w", update.Number, err)
	}
	if why := personMergeInProgress(current, update.Number); why != "" {
		return fmt.Errorf("pull request #%d was not updated, because %s; the repaired change is kept in this run's worktree", update.Number, strings.Replace(why, " while it was being brought up to date", " while this repair was running", 1))
	}
	publication, err := recoveringValue(ctx, a, runstate.RetryRepublishBranch, func(ctx context.Context) (gitworktree.Publication, error) {
		published, publishErr := replacer.ReplaceBranch(ctx, a.worktree, update.Branch, previous, attemptMessage(a.item, a.outcome))
		if published.Commit != "" {
			a.recordHarnessCommit(published.Commit)
		}
		return published, publishErr
	})
	if errors.Is(err, gitworktree.ErrNoChanges) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("update pull request #%d with the repaired change: %w", update.Number, err)
	}
	pushedAt := a.pipeline.clock().Now()
	published := &runstate.PullRequest{
		Remote:     publication.Remote,
		Branch:     publication.Branch,
		Number:     current.Number,
		URL:        current.URL,
		HeadCommit: publication.Commit,
		State:      current.State,
		Merged:     current.Merged,
		PushedAt:   &pushedAt,
	}
	// What the checks of the forge have read of the request, and what the harness
	// wrote on it, belong to the request rather than to the run that opened it.
	if earlier, err := a.pipeline.Store.Load(update.RunID); err == nil && earlier.PullRequest != nil && earlier.PullRequest.Number == published.Number {
		published.Feedback = earlier.PullRequest.Feedback
		published.Wrote = earlier.PullRequest.Wrote
	}
	a.state.PullRequest = published
	a.outcome.PullRequest = published
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the updated pull request: %w", err)
	}
	// The earlier run stops waiting on a request this run now carries. Recording
	// it again is a no-op, so a run that finds the request already at its head
	// settles the earlier run on the way past.
	if _, err := a.pipeline.Store.Update(ctx, update.RunID, runstate.PersonMergeSuperseded{Number: update.Number, RunID: a.state.RunID, At: a.pipeline.clock().Now()}); err != nil {
		return fmt.Errorf("pull request #%d now carries this run's change, and run %s could not be recorded as no longer waiting on it: %w", update.Number, update.RunID, err)
	}
	return nil
}
