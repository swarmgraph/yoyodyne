package orchestrator

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// IntentWorktrees is the worktree manager for a project's companion intent
// repository: everything a run needs of the repository it works in, and the
// commit a branch there holds, which is what companion intent is read at.
// *gitworktree.Manager satisfies it.
type IntentWorktrees interface {
	WorktreeManager
	TargetCommit(ctx context.Context, branch string) (string, error)
}

// targetCommits is the part of a worktree manager that names the commit a
// branch holds. The project's own manager has it where it is a
// *gitworktree.Manager.
type targetCommits interface {
	TargetCommit(ctx context.Context, branch string) (string, error)
}

// intentRoute is this pipeline set to land a document in the project's
// companion intent repository. Every step that reads or changes the run's
// worktree, branch or target then acts on that repository, because Worktrees is
// its manager and the promotion lease is taken on its target branch. The
// project's own manager is kept as projectWorktrees, which is where the
// reviewer's view of the project's code comes from.
func (p Pipeline) intentRoute() Pipeline {
	p.projectWorktrees = p.Worktrees
	p.Worktrees = p.IntentWorktrees
	p.intentLanding = true
	return p
}

// landsInIntentRepository reports whether this run is a landing in the companion
// intent repository. The run's own record is read as well as the pipeline, so a
// run taken up again behaves as it did when it started.
func (a *activeRun) landsInIntentRepository() bool {
	return a.pipeline.intentLanding || a.state.Repository == runstate.RepositoryIntent
}

// worktreesFor is the worktree manager that holds a recorded run's worktree and
// branch: the companion intent repository's for a run that landed there, and the
// project's otherwise. A pipeline that has no manager for the repository a run
// is in answers nil, and a caller that gets nil must refuse to act rather than
// act on the project's repository.
func (p Pipeline) worktreesFor(state runstate.State) WorktreeManager {
	if state.Repository != runstate.RepositoryIntent {
		if p.intentLanding {
			return p.projectWorktrees
		}
		return p.Worktrees
	}
	if p.intentLanding {
		return p.Worktrees
	}
	if p.IntentWorktrees == nil {
		return nil
	}
	return p.IntentWorktrees
}

// intentChecks is the list of checks a landing in the companion intent
// repository runs, which are fit for a repository of documents.
func (p Pipeline) intentChecks() []string { return p.Config.IntentChecks() }

// intentCheckEnv is the environment the intent checks run with: the project's
// configuration is named for them, because their working directory is a
// repository that holds none.
func (p Pipeline) intentCheckEnv() []string {
	if p.ConfigPath == "" {
		return nil
	}
	return []string{config.EnvironmentVariable + "=" + p.ConfigPath}
}

// projectCommit is the commit of the project's own repository a document run's
// reviewer is shown the code at: the commit its target branch holds. It is
// evidence rather than a reason to stop, so a commit that cannot be read is left
// empty.
func (p Pipeline) projectCommit(ctx context.Context) string {
	if p.projectWorktrees == nil {
		return ""
	}
	commits, ok := p.projectWorktrees.(targetCommits)
	if !ok {
		return ""
	}
	branch, err := p.projectWorktrees.CurrentBranch(ctx)
	if err != nil {
		return ""
	}
	commit, err := commits.TargetCommit(ctx, branch)
	if err != nil {
		return ""
	}
	return commit
}

// intentRevision is how a run reads the companion intent repository: at the
// recorded commit, or where none is recorded yet at the commit the repository's
// target branch holds now, which the caller records so that every later reader
// of the same run reads the same commit. It answers nil and no commit where the
// project keeps its intent with its code, or where this pipeline was built
// without access to the companion repository. An error means the commit could not
// be resolved; the caller stops rather than reading the checkout.
func (p Pipeline) intentRevision(ctx context.Context, recorded string) (*contextbundle.Revision, string, error) {
	return intentRevisionOf(ctx, p.Config.Product, p.IntentWorktrees, recorded)
}

// intentRevisionOf is intentRevision for a caller that is not a run: a branch
// review and the merge queue's review read the companion intent at the commit its
// target branch holds now. Where worktrees is nil the answer is nil, so a caller
// built without access to the repository reads what it always read; the caller
// that holds a non-nil value in an interface must not pass a typed nil.
func intentRevisionOf(ctx context.Context, product config.Product, worktrees IntentWorktrees, recorded string) (*contextbundle.Revision, string, error) {
	if !product.HasIntentRepository() || worktrees == nil {
		return nil, "", nil
	}
	commit, branch := recorded, ""
	if commit == "" {
		var err error
		if branch, err = worktrees.CurrentBranch(ctx); err != nil {
			return nil, "", fmt.Errorf("find the companion intent repository's target branch: %w", err)
		}
		if commit, err = worktrees.TargetCommit(ctx, branch); err != nil {
			return nil, "", fmt.Errorf("read the commit the companion intent repository's %s branch holds: %w", branch, err)
		}
	}
	revision := reviewedRevision(ctx, worktrees, commit)
	revision.Name = "commit " + commit + " of the companion intent repository"
	if branch != "" {
		revision.Name += " (" + branch + ")"
	}
	return revision, commit, nil
}

// projectCodeFor is what the reviewer of a change landing in the companion
// intent repository is shown of the project's code: the repository at the commit
// recorded for the run, with the files the document names read whole. Where
// there is no such commit or no way to read it, the evidence says so.
func (a *activeRun) projectCodeFor(ctx context.Context) (review.RepositoryEvidence, string) {
	p := a.pipeline
	commit := a.state.ProjectCommit
	switch {
	case commit == "":
		return review.RepositoryEvidence{Unavailable: "the project's code could not be read: no commit of the project's repository was recorded for this run"}, ""
	case p.projectWorktrees == nil:
		return review.RepositoryEvidence{Unavailable: "the project's code could not be read: this run has no access to the project's repository"}, commit
	}
	item := a.item
	if a.state.Document != nil {
		item.Description = a.state.Document.Candidate.Content
	}
	return reviewedRepository(ctx, p.projectWorktrees, commit, item, gitworktree.ChangeDiff{}), commit
}

// checkEnvironment is what the run's checks are given beyond the process's own
// environment: for a landing in the companion intent repository, the
// configuration the intent checks read, and nothing otherwise.
func (a *activeRun) checkEnvironment() []string {
	if !a.landsInIntentRepository() {
		return nil
	}
	return a.pipeline.intentCheckEnv()
}

// forRepository is this sweep set to act on the repository a recorded run is
// in. A run in the project's own repository is acted on as before. A run in the
// companion intent repository is acted on through IntentWorktrees, and where
// the sweep has none the answer is an error saying so, because the run's branch
// and checkout are not in the project's repository and a sweep that looked there
// would remove or report on the wrong thing.
func (r Reconciler) forRepository(state runstate.State) (Reconciler, error) {
	if state.Repository != runstate.RepositoryIntent {
		return r, nil
	}
	if r.IntentWorktrees == nil {
		return r, fmt.Errorf("run %s is in the companion intent repository, and this sweep has no access to that repository, so its branch and checkout are left as they are", state.RunID)
	}
	r.Worktrees = r.IntentWorktrees
	return r, nil
}
