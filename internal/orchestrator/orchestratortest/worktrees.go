package orchestratortest

import (
	"context"
	"errors"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// PartialWorktreeManager is a worktree manager whose worktree was never finished:
// Create answers with Worktree and Err as given, and nothing after it can be
// done to what it returned.
type PartialWorktreeManager struct {
	Worktree gitworktree.Worktree
	Err      error
}

func (PartialWorktreeManager) ValidateReady(context.Context) error { return nil }

func (PartialWorktreeManager) CurrentBranch(context.Context) (string, error) { return "main", nil }

func (m PartialWorktreeManager) Create(context.Context, gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	return m.Worktree, m.Err
}

// Observe refuses, as the real manager does for a worktree it never finished
// creating: the recorded path is not one it owns, so there is nothing it can say
// about what is there. A preservation check that gets this must claim nothing.
func (PartialWorktreeManager) Observe(context.Context, gitworktree.Worktree) (gitworktree.Observation, error) {
	return gitworktree.Observation{}, errors.New("partial worktree cannot be observed")
}

func (PartialWorktreeManager) SummarizeChanges(context.Context, gitworktree.Worktree) (gitworktree.ChangeSummary, error) {
	return gitworktree.ChangeSummary{}, nil
}

func (PartialWorktreeManager) UnifiedChanges(context.Context, gitworktree.Worktree, gitworktree.DiffLimits) (gitworktree.ChangeDiff, error) {
	return gitworktree.ChangeDiff{}, nil
}

func (PartialWorktreeManager) FileAtCommit(context.Context, string, string, int64) (gitworktree.FileAt, error) {
	return gitworktree.FileAt{}, gitworktree.ErrNotAtCommit
}

func (PartialWorktreeManager) FilesAtCommit(context.Context, string, int, int) (gitworktree.CommitListing, error) {
	return gitworktree.CommitListing{}, errors.New("partial worktree has no committed listing")
}

func (PartialWorktreeManager) ChangedPaths(context.Context, gitworktree.Worktree) ([]string, error) {
	return nil, nil
}

// PartialContentIdentity is the one content a partial worktree ever holds, so a
// test binding evidence to it can say exactly what the promotion will read.
const PartialContentIdentity = gitworktree.ContentIdentityPrefix + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func (PartialWorktreeManager) ContentIdentity(context.Context, gitworktree.Worktree) (string, error) {
	return PartialContentIdentity, nil
}

func (PartialWorktreeManager) CurrentExports() []string { return nil }

func (PartialWorktreeManager) CommitAttempt(context.Context, gitworktree.Worktree, string) (string, error) {
	return "", errors.New("partial worktree cannot be committed")
}

func (PartialWorktreeManager) Integrate(context.Context, gitworktree.Worktree, string) (gitworktree.Integration, error) {
	return gitworktree.Integration{}, errors.New("partial worktree cannot be integrated")
}

func (PartialWorktreeManager) PrepareLanding(context.Context, gitworktree.Worktree, string) (gitworktree.Integration, error) {
	return gitworktree.Integration{}, errors.New("partial worktree cannot be landed")
}

func (PartialWorktreeManager) RebaseOntoTarget(context.Context, gitworktree.Worktree, string) (gitworktree.Rebase, error) {
	return gitworktree.Rebase{}, errors.New("partial worktree cannot be replayed")
}

func (PartialWorktreeManager) ReplayForRepair(context.Context, gitworktree.Worktree, string) (gitworktree.Rebase, error) {
	return gitworktree.Rebase{}, errors.New("partial worktree cannot be replayed")
}

func (PartialWorktreeManager) CleanupIntegrated(context.Context, gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
	return gitworktree.Cleanup{}, errors.New("partial worktree cannot be cleaned up")
}

func (PartialWorktreeManager) RemoteConfigured(context.Context) (bool, error) { return false, nil }

func (PartialWorktreeManager) PushRemoteConfigured(context.Context) (bool, error) {
	return false, nil
}

func (PartialWorktreeManager) PublishBranch(context.Context, gitworktree.Worktree, string) (gitworktree.Publication, error) {
	return gitworktree.Publication{}, errors.New("partial worktree cannot be published")
}

func (PartialWorktreeManager) RepublishBranch(context.Context, gitworktree.Worktree, string) (gitworktree.Publication, error) {
	return gitworktree.Publication{}, errors.New("partial worktree cannot be republished")
}

func (PartialWorktreeManager) VerifyRemoteTarget(context.Context, gitworktree.Integration) error {
	return errors.New("partial worktree has no remote target")
}

func (PartialWorktreeManager) ConfirmRemoteTarget(context.Context, gitworktree.Integration, string) (string, error) {
	return "", errors.New("partial worktree has no remote")
}

func (PartialWorktreeManager) DeleteRemoteBranch(context.Context, gitworktree.Worktree, string) error {
	return errors.New("partial worktree has no remote branch")
}

func (PartialWorktreeManager) CatchUpTarget(context.Context, string) (gitworktree.Catchup, error) {
	return gitworktree.Catchup{}, errors.New("partial worktree has no remote to catch up to")
}

func (PartialWorktreeManager) CommitsChanging(context.Context, string, string, int) ([]string, error) {
	return nil, errors.New("partial worktree has no history")
}
