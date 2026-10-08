package gitworktree

// A promotion into the primary checkout is `git merge --ff-only` run there, and
// Git writes the incoming files into the working tree before it records them in
// the index and moves the branch. A `git status` read inside that window names
// those files as uncommitted, so a run asking whether the checkout is ready while
// another run promotes beside it is refused over a change nobody made. Several
// runs at once is the configured case, so this is the scheduler refusing its own
// work rather than an accident of an unusual setup.
//
// The lease below queues the two. Moving the primary checkout's working tree
// takes it exclusively, and reading the checkout's status takes it shared, so a
// read waits for a promotion in flight and readers never wait for each other. It
// is built exactly as the worktree registry lease is (see registry.go): an
// advisory file lock in the common Git directory, which the operating system
// drops when its holder exits, so a killed promotion leaves nothing to clear.
// It is a lease of its own rather than that one because the two protect
// different things, and a promotion holding the registry lease would stall every
// rebase and branch deletion in every worktree for the length of a merge.
//
// A holder reads under its own lease rather than queueing behind itself: the
// catch-up reads the status of the checkout it is about to move, so the context
// a lease returns is marked, and a read under that mark takes nothing.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// primaryLockName is the file the primary checkout lease is taken on, beside the
// registry lease's file in the common Git directory. Like that one it is never
// removed, for the same reason.
const primaryLockName = "yoyodyne-primary-checkout.lock"

// primaryHold marks a context as running under a primary checkout lease this
// process holds.
type primaryHold struct{}

func primaryHeld(ctx context.Context) bool {
	held, _ := ctx.Value(primaryHold{}).(bool)
	return held
}

// leasePrimary admits this process to move the primary checkout's working tree,
// waiting for every read and move in flight. The returned context is the one to
// carry through the move, and release is safe to defer unconditionally.
//
// A platform with no advisory lock moves the checkout unqueued, as it did before
// this lease existed: refusing every promotion there would be a far larger
// refusal than the read it races.
func (m *Manager) leasePrimary(ctx context.Context) (context.Context, func(), error) {
	if primaryHeld(ctx) || !registryLockSupported {
		return ctx, func() {}, nil
	}
	release, err := m.takePrimaryLease(ctx, lockRegistryFile, "move")
	if err != nil {
		return nil, nil, err
	}
	return context.WithValue(ctx, primaryHold{}, true), release, nil
}

// leasePrimaryShared admits this process to read the primary checkout's status,
// waiting for a move in flight. Under a lease this process already holds it
// takes nothing.
func (m *Manager) leasePrimaryShared(ctx context.Context) (func(), error) {
	if primaryHeld(ctx) || !registryLockSupported {
		return func() {}, nil
	}
	return m.takePrimaryLease(ctx, lockRegistryFileShared, "read")
}

func (m *Manager) takePrimaryLease(ctx context.Context, lock func(context.Context, *os.File) error, purpose string) (func(), error) {
	directory, err := m.commonGitDirectory(holdingRegistry(ctx))
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, primaryLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the primary checkout lease: %w", err)
	}
	// Bounded as the registry lease is: a move is one Git command under the
	// manager's own timeout, so the bound is only ever met by a holder that has
	// wedged.
	waitCtx, cancel := context.WithTimeout(ctx, registryQueueWait)
	defer cancel()
	if err := lock(waitCtx, file); err != nil {
		file.Close()
		if ctx.Err() == nil {
			return nil, fmt.Errorf("wait to %s the primary checkout: another harness held it for the whole %s wait", purpose, registryQueueWait)
		}
		return nil, fmt.Errorf("wait to %s the primary checkout: %w", purpose, err)
	}
	return func() { file.Close() }, nil
}
