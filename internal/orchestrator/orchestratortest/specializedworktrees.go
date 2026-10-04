package orchestratortest

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// RemoteTarget is the pre-merge check on the remote target, which a re-arm
// makes exactly as the original merge did.
type RemoteTarget struct {
	Failure  error
	Verified []gitworktree.Integration
}

func (s *RemoteTarget) VerifyRemoteTarget(_ context.Context, integration gitworktree.Integration) error {
	s.Verified = append(s.Verified, integration)
	return s.Failure
}

// Ownership stands in for the two questions a re-entry asks of the preserved
// worktree: whether it is as the harness left it, and whether the change is
// still in it. What each was asked about, and what each says.
type Ownership struct {
	Err   error
	Asked []gitworktree.Worktree
	// changed is what the preserved worktree holds. A nil value is the ordinary
	// case — the change is still there — so a test about anything else is the only
	// one that has to say so; an empty non-nil slice is the worktree a handback
	// must refuse. readErr is what stopped the reading where nothing could be read.
	Changed []string
	ReadErr error
	Read    []gitworktree.Worktree
}

func (f *Ownership) VerifyOwnedHead(_ context.Context, worktree gitworktree.Worktree) error {
	f.Asked = append(f.Asked, worktree)
	return f.Err
}

func (f *Ownership) ChangedPaths(_ context.Context, worktree gitworktree.Worktree) ([]string, error) {
	f.Read = append(f.Read, worktree)
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	if f.Changed == nil {
		return []string{"internal/orchestrator/repaircontinue.go"}, nil
	}
	return f.Changed, nil
}

// ResumeOwnership stands in for the three questions a resumption asks of the
// repository: whether the primary checkout is one a promotion can be made from,
// whether the preserved worktree is as the harness left it, and whether the
// change is still in it.
type ResumeOwnership struct {
	Ownership
	ReadyErr error
	Asked    int
	// restored is each retired worktree this was asked to put back, and
	// restoreErr what stopped it where nothing could be.
	Restored   []gitworktree.Worktree
	RestoreErr error
	// held and divergenceErr are what the target branch's catch-up would still
	// hold on, and accessErr what the remotes still say of the credential.
	Held          string
	DivergenceErr error
	AccessErr     error
	TargetAsked   int
	AccessAsked   int
}

func (f *ResumeOwnership) ValidateReady(context.Context) error {
	f.Asked++
	return f.ReadyErr
}

func (f *ResumeOwnership) TargetDivergence(_ context.Context, targetBranch string) (gitworktree.Catchup, error) {
	f.TargetAsked++
	return gitworktree.Catchup{TargetBranch: targetBranch, Held: f.Held}, f.DivergenceErr
}

func (f *ResumeOwnership) VerifyRemoteAccess(context.Context, string) error {
	f.AccessAsked++
	return f.AccessErr
}

func (f *ResumeOwnership) RestoreWorktree(_ context.Context, worktree gitworktree.Worktree) (gitworktree.Worktree, error) {
	f.Restored = append(f.Restored, worktree)
	if f.RestoreErr != nil {
		return gitworktree.Worktree{}, f.RestoreErr
	}
	return worktree, nil
}

type RecoveryCheckout struct {
	*Ownership
	Present       bool
	Branch        bool
	Restores      int
	RestoreErr    error
	SurviveErr    error
	Dirty         bool
	BeforeRestore func()
}

func (w *RecoveryCheckout) Inspect(_ context.Context, tree gitworktree.Worktree) (gitworktree.Inspection, error) {
	return gitworktree.Inspection{Registered: w.Present, Branch: tree.Branch, Dirty: w.Dirty}, nil
}

func (w *RecoveryCheckout) Survives(context.Context, gitworktree.Worktree) (gitworktree.Survival, error) {
	return gitworktree.Survival{WorktreePresent: w.Present, BranchExists: w.Branch}, w.SurviveErr
}

func (w *RecoveryCheckout) RestoreWorktree(_ context.Context, tree gitworktree.Worktree) (gitworktree.Worktree, error) {
	w.Restores++
	if w.BeforeRestore != nil {
		w.BeforeRestore()
	}
	if w.RestoreErr != nil {
		return gitworktree.Worktree{}, w.RestoreErr
	}
	w.Present = true
	return tree, nil
}

// Survival is a repository that answers for the one stopped run: whether its
// branch and its checkout are there.
type Survival struct{ Survival gitworktree.Survival }

func (l *Survival) Survives(context.Context, gitworktree.Worktree) (gitworktree.Survival, error) {
	return l.Survival, nil
}
