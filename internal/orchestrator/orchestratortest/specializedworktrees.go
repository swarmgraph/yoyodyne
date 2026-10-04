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

// Survival is a repository that answers for the one stopped run: whether its
// branch and its checkout are there.
type Survival struct{ Survival gitworktree.Survival }

func (l *Survival) Survives(context.Context, gitworktree.Worktree) (gitworktree.Survival, error) {
	return l.Survival, nil
}
