package orchestrator

// What the merge queue tidies on the forge once it has landed a change. The
// run that made the change published it on a pull request of its own before
// it was admitted, and the queue landed it through a candidate branch of the
// queue's own; neither is how the change reached the target any more, so both
// would sit open as debris for the development manager's forge check to
// report. The run's request is closed with a comment naming what landed it,
// as a superseded publication is (supersession.go), and both branches are
// deleted on the commit the harness last put there, so a branch somebody else
// moved is left for a person.
//
// None of it is the landing: the change is on the target whether or not any of
// this is done, so a failure here is reported beside the landing and never
// undoes or holds it. It is asked before the landing's completion is written
// whole, so a process that stops in between asks again, and each step is a
// thing already done when it is asked twice.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueRetirement tidies the forge after a landing. It is satisfied by
// MergeQueuePublicationRetirer.
type MergeQueueRetirement interface {
	Retire(ctx context.Context, entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion, attempt runstate.MergeQueuePromotionAttempt) error
}

// MergeQueuePublicationRetirer closes the landed run's own pull request and
// deletes its branch and the queue's candidate branch from the remote.
type MergeQueuePublicationRetirer struct {
	Store    StateStore
	Forge    SupersededPublications
	Branches SupersededBranches
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

var _ MergeQueueRetirement = MergeQueuePublicationRetirer{}

func (r MergeQueuePublicationRetirer) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Retire tidies what the landing left: the candidate branch first, which is
// the queue's own and nobody else's, then the run's request and its branch.
func (r MergeQueuePublicationRetirer) Retire(ctx context.Context, entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion, attempt runstate.MergeQueuePromotionAttempt) error {
	var problems []error
	if branch := strings.TrimSpace(attempt.CandidateBranch); branch != "" && attempt.Path.ThroughForge() && attempt.Path != runstate.MergeQueueLandThroughForgeQueue && r.Branches != nil {
		candidate := gitworktree.Worktree{RunID: entry.RunID, WorkItemID: entry.WorkItemID, Branch: branch, TargetBranch: entry.TargetBranch}
		if err := r.Branches.DeleteRemoteBranch(ctx, candidate, attempt.Candidate); err != nil {
			problems = append(problems, fmt.Errorf("delete the candidate branch %s: %w", branch, err))
		}
	}
	if err := r.retireRunRequest(ctx, entry, completion); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

func (r MergeQueuePublicationRetirer) retireRunRequest(ctx context.Context, entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion) error {
	if r.Store == nil || r.Forge == nil {
		return nil
	}
	run, err := r.Store.Load(entry.RunID)
	if err != nil {
		return fmt.Errorf("read run %s, whose pull request the landing leaves open: %w", entry.RunID, err)
	}
	published := run.PullRequest
	if published == nil || published.Merged || strings.TrimSpace(published.Superseded) != "" || published.Number == 0 {
		return nil
	}
	vehicle := fmt.Sprintf("merge queue entry %d (%s), which landed it on %s as %s", entry.Order, entry.EntryID, entry.TargetBranch, completion.Landed)
	if completion.PullRequest != "" {
		vehicle += " through " + completion.PullRequest
	}
	closure, err := r.Forge.Close(ctx, publish.CloseRequest{
		Head: published.Branch, Number: published.Number,
		Comment: strings.Join([]string{
			fmt.Sprintf("Yoyodyne closed this pull request: the change it carries was approved, admitted to the merge queue, and landed by %s.", vehicle),
			"",
			"- Work item: `" + entry.WorkItemID + "`",
			"- Run: `" + entry.RunID + "`, approved head `" + entry.ApprovedHead + "`",
			"",
			"The queue built a candidate of the target with this change merged onto it, ran the project's checks on that candidate, and had an independent reviewer approve it; nothing here is pending review or waiting to merge, and the branch this request carries is being deleted with it.",
		}, "\n"),
	})
	if err != nil {
		return fmt.Errorf("close pull request %d of run %s: %w", published.Number, entry.RunID, err)
	}
	if !closure.Merged {
		worktree := worktreeOf(run)
		worktree.Branch = published.Branch
		if err := r.Branches.DeleteRemoteBranch(ctx, worktree, published.HeadCommit); err != nil {
			return fmt.Errorf("delete the branch pull request %d published: %w", published.Number, err)
		}
	}
	// What retired it is written on the run, so the convergence sweep does not
	// ask the forge about a request already dealt with. A request the forge
	// reads as merged — its head reached the target inside the candidate — is
	// left as the forge says.
	if closure.Merged {
		return nil
	}
	published.Superseded = vehicle
	published.State = nonEmpty(closure.State, "CLOSED")
	run.UpdatedAt = r.now()
	if err := r.Store.Save(run); err != nil {
		return fmt.Errorf("record on run %s that its pull request was closed: %w", entry.RunID, err)
	}
	return nil
}
