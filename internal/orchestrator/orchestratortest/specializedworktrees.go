package orchestratortest

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// RemoteTarget scripts and records pre-merge target verification.
type RemoteTarget struct {
	Failure  error
	Verified []gitworktree.Integration
}

func (s *RemoteTarget) VerifyRemoteTarget(_ context.Context, integration gitworktree.Integration) error {
	s.Verified = append(s.Verified, integration)
	return s.Failure
}

// Ownership scripts and records ownership and content readings of a worktree.
type Ownership struct {
	Err   error
	Asked []gitworktree.Worktree
	// Nil Changed means the ordinary change; an empty slice means no change.
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

// Survival scripts the presence of a stopped run's branch and checkout.
type Survival struct{ Survival gitworktree.Survival }

func (l *Survival) Survives(context.Context, gitworktree.Worktree) (gitworktree.Survival, error) {
	return l.Survival, nil
}

// RecoveryCheckout scripts a stopped run's checkout as a repair finds it:
// whether it is registered, whether its branch survives, whether it is dirty,
// and how restoring it answers. A restoration that succeeds makes the checkout
// present, and BeforeRestore, where set, runs before each one is answered.
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

// ResumeOwnership is Ownership with the questions a resumed integration asks
// besides: whether the primary checkout is ready, whether the target branch
// would still hold, whether the remotes take the credential, and restoring a
// retired worktree.
type ResumeOwnership struct {
	Ownership
	ReadyErr   error
	ReadyAsked int
	// Restored is each retired worktree this was asked to put back, and
	// RestoreErr what stopped it where nothing could be.
	Restored   []gitworktree.Worktree
	RestoreErr error
	// Held and DivergenceErr are what the target branch's catch-up would still
	// hold on, and AccessErr what the remotes still say of the credential.
	Held          string
	DivergenceErr error
	AccessErr     error
	TargetAsked   int
	AccessAsked   int
}

func (f *ResumeOwnership) ValidateReady(context.Context) error {
	f.ReadyAsked++
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
