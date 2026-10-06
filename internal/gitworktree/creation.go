package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// resumeCreation cannot adopt changes: the branch is still at its base, the
// target already contains that base, and the checkout must be unchanged. No
// developer or document writer has been allowed to work before this checkpoint.
func (m *Manager) resumeCreation(ctx context.Context, request CreateRequest) (Worktree, bool, error) {
	branch := branchName(request.WorkItemID, request.RunID)
	commit, exists, err := m.optionalBranchCommit(ctx, branch)
	if err != nil || !exists {
		return Worktree{}, false, err
	}
	if request.TargetBranch == "" {
		return Worktree{}, false, errors.New("recovering a creation requires its recorded target")
	}
	contained, err := m.contains(ctx, commit, request.TargetBranch)
	if err != nil {
		return Worktree{}, false, fmt.Errorf("read the interrupted creation's target: %w", err)
	}
	if !contained {
		return Worktree{}, false, errors.New("the interrupted creation's base is not contained in its target")
	}
	tree := Worktree{RunID: request.RunID, WorkItemID: request.WorkItemID, Path: filepath.Join(m.worktreeRoot, worktreeDirectoryName(request.WorkItemID, request.RunID)), Branch: branch, BaseRef: request.BaseRef, BaseCommit: commit, TargetBranch: request.TargetBranch}
	if _, err := m.ownedPath(tree); err != nil {
		return Worktree{}, false, err
	}
	if _, err := os.Lstat(tree.Path); errors.Is(err, os.ErrNotExist) {
		// Reuse the confined checkout restorer; the initial base itself is the
		// only commit this unfinished creation can be restored at.
		restore := tree
		restore.HarnessCommit = commit
		if _, err := m.RestoreWorktree(ctx, restore); err != nil {
			return Worktree{}, false, err
		}
	} else if err != nil {
		return Worktree{}, false, err
	}
	path, _, err := m.verifyOwnedHead(ctx, tree)
	if err != nil {
		return Worktree{}, false, err
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return Worktree{}, false, err
	}
	if dirty {
		return Worktree{}, false, errors.New("an interrupted creation has changes and cannot be adopted as an empty checkout")
	}
	if err := m.refreshExports(ctx, path); err != nil {
		return Worktree{}, false, err
	}
	return tree, true, nil
}
