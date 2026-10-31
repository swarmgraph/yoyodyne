package orchestrator

// The fetch before each developer run
// (docs/designs/working-alongside-other-contributors.md, section 3). Other
// contributors merge onto the remote target while the factory works, so the
// local target is brought onto the remote's immediately before a run's worktree
// is cut from it, and the run records which target it started from and how it
// came by it. A fetch that fails never stops the run: it starts from the target
// the last fetch left, and its record says so and how old that target is.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TargetFetcher brings a target branch onto the remote's for a run about to be
// cut from it. It is satisfied by *gitworktree.Manager, and a manager that does
// not offer it starts every run from the local target as it stands.
type TargetFetcher interface {
	FetchTarget(ctx context.Context, targetBranch string, now time.Time) gitworktree.TargetFetch
	// Remote names the remote the target is fetched from, so a fetch that could
	// not be attempted still records which remote it was for.
	Remote() string
}

// startingTargetNoteTimeout bounds the note naming the starting target, which
// is written before the developer starts and must not hold it up.
const startingTargetNoteTimeout = time.Minute

// fetchTarget fetches this run's target under the target branch's promotion
// lease, because moving the local target is what a promotion does and the two
// must not read the branch and then both move it. The lease is the harness's
// own and is held only for the fetch; nothing an agent can reach takes it.
//
// It records what it found on the run and never fails: a lease it could not
// take is recorded as the fetch failing, and the run starts from the local
// target as it stands.
func (a *activeRun) fetchTarget(ctx context.Context, targetBranch string) {
	fetcher, ok := a.pipeline.Worktrees.(TargetFetcher)
	if !ok || targetBranch == "" {
		return
	}
	now := a.pipeline.clock().Now().UTC()
	var fetch gitworktree.TargetFetch
	lease, err := a.pipeline.Store.LeasePromotion(ctx, targetBranch)
	if err != nil {
		fetch = gitworktree.TargetFetch{
			TargetBranch: targetBranch,
			Remote:       fetcher.Remote(),
			Failure:      fmt.Sprintf("waiting for a turn to move %s: %v", targetBranch, err),
		}
	} else {
		fetch = fetcher.FetchTarget(ctx, targetBranch, now)
		// Releasing lets the next promotion in, and the operating system does it
		// anyway when the process exits, so a close that failed says nothing
		// about the branch.
		_ = lease.Release()
	}
	a.state.StartingTarget = &runstate.StartingTarget{
		TargetBranch:  fetch.TargetBranch,
		Commit:        fetch.Commit,
		Remote:        fetch.Remote,
		Fetched:       fetch.Fetched,
		RemoteCommit:  fetch.RemoteCommit,
		Advanced:      fetch.Advanced,
		Ahead:         fetch.Ahead,
		Held:          fetch.Held,
		Failure:       fetch.Failure,
		LastFetchedAt: fetch.LastFetchedAt,
		CommittedAt:   fetch.CommittedAt,
		RecordedAt:    now,
	}
}

// recordStartingTarget names the commit the run's worktree was actually cut
// from on the record, and on the item's notes, once the worktree exists. The
// worktree's base is the commit that counts: the target can move between the
// fetch and the cut, and the base is what the run starts from either way.
//
// The note is written once, only where there is a remote, and is not waited
// on: a tracker that does not answer costs the item the note, and the run's
// record still names the target.
func (a *activeRun) recordStartingTarget(ctx context.Context, worktree gitworktree.Worktree) {
	started := a.state.StartingTarget
	if started == nil {
		return
	}
	started.Commit = worktree.BaseCommit
	// A repository with no remote has nothing fetched to say, and its run's
	// notes already name the base commit it started from.
	if started.Remote == "" {
		return
	}
	noteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), startingTargetNoteTimeout)
	defer cancel()
	_, _ = a.pipeline.Tracker.RecordOutcome(noteCtx, a.state.WorkItemID, fmt.Sprintf("Run %s %s.", a.state.RunID, started.Describe()))
}
