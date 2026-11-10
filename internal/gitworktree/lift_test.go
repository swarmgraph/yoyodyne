package gitworktree

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// raisedBranch is a branch an earlier run left carrying one change past main,
// which is what a raise preserves.
func raisedBranch(t *testing.T, repository, branch, file, content string) string {
	t.Helper()
	runGit(t, repository, "branch", branch, "main")
	scratch := filepath.Join(t.TempDir(), "raised")
	runGit(t, repository, "worktree", "add", "--quiet", scratch, branch)
	writeFile(t, scratch, file, content)
	runGit(t, scratch, "add", file)
	runGit(t, scratch, "commit", "--quiet", "-m", "the raising run's change")
	commit := strings.TrimSpace(gitOutput(t, scratch, "rev-parse", "HEAD"))
	runGit(t, repository, "worktree", "remove", "--force", scratch)
	return commit
}

// A lift puts what the preserved branch carries past the target into a fresh
// worktree, uncommitted, and leaves HEAD where the harness cut it: the change is
// the fresh run's to carry forward, and the harness still owns every commit.
func TestALiftAppliesThePreservedChangeUncommitted(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	commit := raisedBranch(t, repository, "yoyodyne/raised/aaaa", "lifted.txt", "the raising run's change\n")
	worktree := preservedWorktree(t, manager, "yoyodyne-raised")

	lifted, err := manager.LiftChange(context.Background(), worktree, "yoyodyne/raised/aaaa")
	if err != nil {
		t.Fatalf("LiftChange() error = %v", err)
	}
	if lifted.Commit != commit || lifted.Branch != "yoyodyne/raised/aaaa" {
		t.Fatalf("lifted = %#v, want the branch at %s", lifted, commit)
	}
	if got := readFile(t, worktree.Path, "lifted.txt"); got != "the raising run's change\n" {
		t.Fatalf("lifted.txt = %q, want the raising run's change", got)
	}
	if head := strings.TrimSpace(gitOutput(t, worktree.Path, "rev-parse", "HEAD")); head != worktree.BaseCommit {
		t.Fatalf("HEAD = %s, want the base %s the harness cut", head, worktree.BaseCommit)
	}
	// The harness's own commit afterwards is the whole change, base-relative.
	if _, err := manager.CommitAttempt(context.Background(), worktree, ""); err != nil {
		t.Fatalf("CommitAttempt() after a lift error = %v", err)
	}
}

// A branch carrying nothing past the target has nothing to lift, and says so
// rather than being reported as a lift.
func TestALiftOfABranchCarryingNothingSaysSo(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	runGit(t, repository, "branch", "yoyodyne/raised/empty", "main")
	worktree := preservedWorktree(t, manager, "yoyodyne-raised")

	if _, err := manager.LiftChange(context.Background(), worktree, "yoyodyne/raised/empty"); !errors.Is(err, ErrNothingToLift) {
		t.Fatalf("LiftChange() error = %v, want ErrNothingToLift", err)
	}
	if _, err := manager.LiftChange(context.Background(), worktree, "yoyodyne/raised/gone"); !errors.Is(err, ErrLiftBranchGone) || !strings.Contains(err.Error(), "yoyodyne/raised/gone") {
		t.Fatalf("LiftChange() of a missing branch error = %v, want it refused by name", err)
	}
}

// A change that no longer merges with where the target now stands is not
// half-applied: the worktree goes back to exactly what was cut, and the conflict
// is the error.
func TestALiftThatConflictsLeavesTheWorktreeAsItWasCut(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	raisedBranch(t, repository, "yoyodyne/raised/bbbb", "README.txt", "the raising run's README\n")
	writeFile(t, repository, "README.txt", "main moved on\n")
	runGit(t, repository, "commit", "--quiet", "-am", "main moved on")
	worktree := preservedWorktree(t, manager, "yoyodyne-raised")

	_, err := manager.LiftChange(context.Background(), worktree, "yoyodyne/raised/bbbb")
	if !errors.Is(err, ErrLiftDoesNotApply) || !strings.Contains(err.Error(), "does not apply cleanly") {
		t.Fatalf("LiftChange() error = %v, want the conflict named as ErrLiftDoesNotApply", err)
	}
	if status := strings.TrimSpace(gitOutput(t, worktree.Path, "status", "--porcelain")); status != "" {
		t.Fatalf("status = %q, want the worktree back as it was cut", status)
	}
	if got := readFile(t, worktree.Path, "README.txt"); got != "main moved on\n" {
		t.Fatalf("README.txt = %q, want the target's", got)
	}
}
