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
