package gitworktree

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// LandingPaths lists the repository-relative paths a landing commit changed on
// its target branch: the difference from its first parent, which is the target
// as it stood before the change landed whether the landing was a merge commit
// or a commit replayed onto the target. It reads and moves nothing.
func (m *Manager) LandingPaths(ctx context.Context, commit string) ([]string, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("landing commit %q is not a full commit hash", commit)
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot, "diff", "--name-only", "--no-renames", "--no-ext-diff", "-z", commit+"^1", commit, "--")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list the paths landing commit %s changed failed with exit code %d: %s",
			commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var paths []string
	// The runner may hand back the output with a line break of its own around
	// it; a path is never only line breaks, so those pieces are dropped.
	for _, path := range strings.Split(result.Stdout, "\x00") {
		if path = strings.Trim(path, "\r\n"); path != "" {
			paths = append(paths, filepath.ToSlash(path))
		}
	}
	return paths, nil
}
