package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The landing checks run over the commit a run integrated, after the run's own
// worktree is gone, and they need a checkout of exactly that commit to run in.
// The primary checkout is not it: it belongs to whoever is developing there,
// and a suite run in it would read whatever they have uncommitted rather than
// what landed. So a landing is given a checkout of its own, detached at the
// integrated commit, under the worktree root beside the run worktrees, and it
// is removed once the checks have ended.
//
// It is detached rather than on a branch because it is not a change and nothing
// will ever be promoted from it: a branch would be one more ref the sweeps have
// to account for, and a detached HEAD is what a listing already reads as a
// checkout that is nobody's change.

// landingDirectoryName is where a run's landing checkout goes under the
// worktree root. It carries the run rather than the commit because two runs
// never land one commit, and a reader of the root can tell whose it was.
func landingDirectoryName(runID string) string {
	return "landing-" + strings.TrimPrefix(runID, "run-")[:8]
}

// CheckoutCommit cuts a detached checkout of one commit for a run's landing
// checks and reports where it is. A checkout left at the same path by a process
// that died mid-landing is removed first, because the path is the run's own and
// nothing else is ever put there.
func (m *Manager) CheckoutCommit(ctx context.Context, runID, commit string) (string, error) {
	if strings.TrimSpace(runID) == "" || len(strings.TrimPrefix(runID, "run-")) < 8 {
		return "", fmt.Errorf("run id %q is not one a landing checkout can be named for", runID)
	}
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("landing commit %q is not a commit hash", commit)
	}
	if err := os.MkdirAll(m.worktreeRoot, 0o700); err != nil {
		return "", fmt.Errorf("create worktree root: %w", err)
	}
	path := filepath.Join(m.worktreeRoot, landingDirectoryName(runID))
	// Creation writes the same bookkeeping every run worktree's does, so it queues
	// on the same lease for the same reason: a creation that reads another's
	// half-written registration exits rather than creating anything.
	ctx, lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = lease.release() }()
	if _, err := os.Lstat(path); err == nil {
		if err := m.removeCheckout(ctx, path); err != nil {
			return "", fmt.Errorf("remove the landing checkout a previous process left at %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect landing checkout path: %w", err)
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "add", "--detach", path, commit)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("create landing checkout failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	// The checkout is cut from a commit, so the exports a store outside Git is
	// authoritative for are as current as that commit made them. They are
	// refreshed for the reason a run worktree's are — a check that reads one
	// reads the current export — and held out of a change nothing will make.
	// A checkout whose exports could not be refreshed is removed here rather
	// than handed back with the error: the caller records the failure and reads
	// no path from it, and a landing checkout nothing removes is one the sweep
	// never finds — it settles landings the record says are still running, and
	// this one is recorded as ended.
	if err := m.refreshExports(ctx, path); err != nil {
		refresh := fmt.Errorf("refresh the current exports in the landing checkout: %w", err)
		if removeErr := m.removeCheckout(ctx, path); removeErr != nil {
			return "", errors.Join(refresh, fmt.Errorf("remove the landing checkout at %s afterwards: %w", path, removeErr))
		}
		return "", refresh
	}
	return path, nil
}

// RemoveCheckout removes a landing checkout once its checks have ended. It
// refuses a path outside the worktree root or one that is not a landing
// checkout, because it is handed a path a record carried and a record can be
// wrong.
func (m *Manager) RemoveCheckout(ctx context.Context, path string) error {
	if filepath.Dir(path) != m.worktreeRoot || !strings.HasPrefix(filepath.Base(path), "landing-") {
		return fmt.Errorf("%s is not a landing checkout under %s", path, m.worktreeRoot)
	}
	ctx, lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = lease.release() }()
	return m.removeCheckout(ctx, path)
}

// RemoveLandingCheckout removes whatever landing checkout a run left, and
// nothing where there is none. It is what the sweep calls for a run whose
// process died with its landing checks running: the record names no path for
// the checkout, so it is found by the name the run's landing would have used.
func (m *Manager) RemoveLandingCheckout(ctx context.Context, runID string) error {
	if strings.TrimSpace(runID) == "" || len(strings.TrimPrefix(runID, "run-")) < 8 {
		return fmt.Errorf("run id %q is not one a landing checkout can be named for", runID)
	}
	path := filepath.Join(m.worktreeRoot, landingDirectoryName(runID))
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect landing checkout path: %w", err)
	}
	return m.RemoveCheckout(ctx, path)
}

// removeCheckout removes a checkout whatever it holds: a landing checkout is
// nobody's change, so a check that left build products in it is not work to
// preserve. The caller holds the registry lease.
//
// The force is given twice because the one lock such a checkout can carry is
// the "initializing" lock `git worktree add` takes while it writes the tree,
// and an add ended part-way leaves it: Git refuses to remove a locked checkout
// on one force, and refuses to add one at the same path while the locked
// registration stands, even with its directory gone. The harness never locks a
// checkout of its own, so the lock is never somebody's to keep.
func (m *Manager) removeCheckout(ctx context.Context, path string) error {
	removed, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "remove", "--force", "--force", path)
	if err != nil {
		return err
	}
	if removed.Status != execution.ProcessSucceeded {
		// A directory Git no longer knows about is removed as a directory, so a
		// registration a prune already dropped does not leave the path standing.
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove landing checkout failed with exit code %d: %s; and removing the directory failed: %w", removed.ExitCode, strings.TrimSpace(removed.Stderr), err)
		}
	}
	return nil
}
