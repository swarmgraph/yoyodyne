package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// KeptCheckoutError is the refusal to remove a finished run's checkout because
// it holds a file that exists nowhere else. It is a type so a caller can tell a
// checkout that has to stay, which no later attempt will change, from a removal
// that failed and is worth trying again.
type KeptCheckoutError struct {
	Path string
	// Files are the repository-relative paths that are not the harness's own
	// leftovers, in the order Git reported them.
	Files []string
}

func (e *KeptCheckoutError) Error() string {
	return fmt.Sprintf("refusing to remove a dirty worktree: %s holds %s", e.Path, strings.Join(e.Files, ", "))
}

// dirtyPath is one line of Git's status for a checkout.
type dirtyPath struct {
	index    byte
	worktree byte
	path     string
}

// removable reports whether losing this change loses nothing that is not
// already recorded. The caller has proven the checkout is at the integrated
// commit, so a tracked file that is merely missing from disk is still in that
// commit, which is what an interrupted removal leaves behind. A current export
// is the other case: the harness wrote that copy itself from the primary
// checkout and the branch carries the original.
func (p dirtyPath) removable(exports map[string]bool) bool {
	if exports[p.path] && p.index != 'U' && p.worktree != 'U' {
		return true
	}
	return p.index == ' ' && p.worktree == 'D'
}

// dirtyPaths lists every uncommitted path, untracked files included.
func (m *Manager) dirtyPaths(ctx context.Context, path string) ([]dirtyPath, error) {
	environment := append(execution.GitEnvironment(nil), "GIT_OPTIONAL_LOCKS=0")
	result, err := m.runWithEnvironment(ctx, environment, "-C", path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("inspect Git status failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var paths []dirtyPath
	fields := strings.Split(result.Stdout, "\x00")
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 4 {
			continue
		}
		entry := dirtyPath{index: field[0], worktree: field[1], path: field[3:]}
		// A rename or copy is followed by the path it came from.
		if entry.index == 'R' || entry.index == 'C' || entry.worktree == 'R' || entry.worktree == 'C' {
			i++
		}
		paths = append(paths, entry)
	}
	return paths, nil
}

// leftoversOnly says whether a checkout's uncommitted paths are all ones the
// harness may discard, and otherwise returns the error naming the files that
// make it stay. Nothing is deleted here.
func (m *Manager) leftoversOnly(ctx context.Context, path string) (bool, error) {
	paths, err := m.dirtyPaths(ctx, path)
	if err != nil {
		return false, err
	}
	exports := make(map[string]bool, len(m.currentExports))
	for _, export := range m.currentExports {
		exports[export] = true
	}
	var kept []string
	for _, entry := range paths {
		if !entry.removable(exports) {
			kept = append(kept, entry.path)
		}
	}
	if len(kept) > 0 {
		return false, &KeptCheckoutError{Path: path, Files: kept}
	}
	return len(paths) > 0, nil
}

// IsKeptCheckout reports whether err is a checkout kept for its files.
func IsKeptCheckout(err error) (*KeptCheckoutError, bool) {
	var kept *KeptCheckoutError
	if errors.As(err, &kept) {
		return kept, true
	}
	return nil, false
}
