package gitworktree

// Starting the merge a run settles when bringing the target branch up to date
// with the team's branch conflicts (docs/designs/integrating-to-a-local-branch.md,
// section 3). The harness begins the merge in the run's worktree and leaves it
// open, with Git's own conflict markers in the files; the developer resolves
// them, and the harness's ordinary commit of what the developer left then
// makes the merge commit, with the target the run started from as its first
// parent and the team's commit as its second.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ErrMergeNotStarted is a merge Git did not begin, which is not a conflict:
// the commit is already in the branch, or Git refused for another reason.
var ErrMergeNotStarted = errors.New("the merge could not be started in the worktree")

// StartMerge begins merging commit into the worktree's branch without
// committing, and leaves it open. It answers the paths the merge conflicts in,
// empty where it applied cleanly. The worktree must stand at its recorded base
// with nothing made yet.
func (m *Manager) StartMerge(ctx context.Context, worktree Worktree, commit, message string) ([]string, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("%q is not a full commit id", commit)
	}
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return nil, err
	}
	if head != worktree.BaseCommit {
		return nil, fmt.Errorf("%w: the worktree is at %s, not the base %s the merge starts from", ErrMergeNotStarted, head, worktree.BaseCommit)
	}
	merged, err := m.runWithEnvironment(ctx, harnessCommitEnvironment(), "-C", path,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"merge", "--no-commit", "--no-ff", "--no-verify", "--no-gpg-sign", "--message", strings.TrimSpace(message), commit)
	if err != nil {
		return nil, err
	}
	switch {
	case merged.Status == execution.ProcessSucceeded:
		return nil, nil
	case merged.ExitCode != 1:
		return nil, fmt.Errorf("%w: git merge exited %d: %s", ErrMergeNotStarted, merged.ExitCode, strings.TrimSpace(merged.Stderr))
	}
	unmerged, err := m.run(ctx, "-C", path, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	if unmerged.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("%w: list the conflicting paths failed with exit code %d: %s", ErrMergeNotStarted, unmerged.ExitCode, strings.TrimSpace(unmerged.Stderr))
	}
	var paths []string
	for _, p := range strings.Split(unmerged.Stdout, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: git merge exited 1 and left no conflicting path: %s", ErrMergeNotStarted, strings.TrimSpace(merged.Stdout+merged.Stderr))
	}
	return paths, nil
}
