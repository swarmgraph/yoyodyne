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

// A change that conflicts with where the target now stands is still lifted: the
// merge is left with its conflict markers for the developer, the files are named
// on the lift, HEAD stays where the harness cut it, and once the files are
// resolved the harness's own commit carries the resolution.
func TestALiftThatConflictsLeavesTheConflictsForTheDeveloper(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	commit := raisedBranch(t, repository, "yoyodyne/raised/bbbb", "README.txt", "the raising run's README\n")
	writeFile(t, repository, "README.txt", "main moved on\n")
	runGit(t, repository, "commit", "--quiet", "-am", "main moved on")
	worktree := preservedWorktree(t, manager, "yoyodyne-raised")

	lifted, err := manager.LiftChange(context.Background(), worktree, "yoyodyne/raised/bbbb")
	if err != nil {
		t.Fatalf("LiftChange() error = %v, want the conflicting change lifted", err)
	}
	if lifted.Commit != commit || len(lifted.Conflicts) != 1 || lifted.Conflicts[0] != "README.txt" {
		t.Fatalf("lifted = %#v, want %s with README.txt in conflict", lifted, commit)
	}
	got := readFile(t, worktree.Path, "README.txt")
	if !strings.Contains(got, "<<<<<<<") || !strings.Contains(got, "main moved on") || !strings.Contains(got, "the raising run's README") {
		t.Fatalf("README.txt = %q, want both sides between conflict markers", got)
	}
	if head := strings.TrimSpace(gitOutput(t, worktree.Path, "rev-parse", "HEAD")); head != worktree.BaseCommit {
		t.Fatalf("HEAD = %s, want the base %s the harness cut", head, worktree.BaseCommit)
	}

	writeFile(t, worktree.Path, "README.txt", "resolved\n")
	committed, err := manager.CommitAttempt(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("CommitAttempt() after resolving the lift error = %v", err)
	}
	if got := gitOutput(t, worktree.Path, "show", committed+":README.txt"); got != "resolved\n" {
		t.Fatalf("committed README.txt = %q, want the resolution", got)
	}
}
