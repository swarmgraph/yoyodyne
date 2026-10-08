package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testQueueEntry = "mqe-0123456789abcdef0123456789abcdef"

// queueRepository is a repository whose main branch moved on after a change
// was branched from it: the shape every merge queue candidate is built from.
func queueRepository(t *testing.T) (repository, head string) {
	t.Helper()
	repository = newRepository(t)
	runGit(t, repository, "switch", "-c", "change")
	writeFile(t, repository, "change.txt", "the change\n")
	runGit(t, repository, "add", "change.txt")
	runGit(t, repository, "commit", "-m", "the change")
	head = gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "switch", "main")
	writeFile(t, repository, "other.txt", "other work\n")
	runGit(t, repository, "add", "other.txt")
	runGit(t, repository, "commit", "-m", "other work landed")
	return repository, head
}

func TestAQueueCandidateIsTheTargetWithTheApprovedHeadMergedOntoIt(t *testing.T) {
	t.Parallel()

	repository, head := queueRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	ctx := context.Background()
	target := gitLine(t, repository, "rev-parse", "main")

	candidate, err := manager.BuildQueueCandidate(ctx, QueueCandidateRequest{Entry: testQueueEntry, TargetBranch: "main", Heads: []string{head}})
	if err != nil {
		t.Fatalf("BuildQueueCandidate() error = %v", err)
	}
	if candidate.BaseCommit != target || candidate.Commit == target || candidate.Commit == head {
		t.Fatalf("candidate = %#v, want a new commit on base %s", candidate, target)
	}
	if parents := gitLine(t, repository, "rev-list", "--parents", "-n", "1", candidate.Commit); parents != candidate.Commit+" "+target+" "+head {
		t.Fatalf("candidate parents = %q, want the target then the approved head", parents)
	}
	if tree := gitLine(t, repository, "rev-parse", candidate.Commit+"^{tree}"); tree != candidate.Tree {
		t.Fatalf("candidate tree = %s, reported %s", tree, candidate.Tree)
	}
	if readFile(t, candidate.Path, "change.txt") != "the change\n" || readFile(t, candidate.Path, "other.txt") != "other work\n" {
		t.Fatal("the candidate checkout does not hold both the target's work and the change")
	}
	if author := gitLine(t, repository, "log", "-1", "--format=%an", candidate.Commit); author != harnessCommitAuthorName {
		t.Fatalf("candidate author = %q, want the harness", author)
	}
	// Nothing the candidate was built from moved.
	if now := gitLine(t, repository, "rev-parse", "main"); now != target {
		t.Fatalf("main moved to %s", now)
	}
	if now := gitLine(t, repository, "rev-parse", "change"); now != head {
		t.Fatalf("the source branch moved to %s", now)
	}
	if got, err := manager.TargetCommit(ctx, "main"); err != nil || got != target {
		t.Fatalf("TargetCommit() = %s, %v", got, err)
	}
	if held, err := manager.HoldsCommit(ctx, candidate.Commit); err != nil || !held {
		t.Fatalf("HoldsCommit(candidate) = %t, %v", held, err)
	}

	change, err := manager.CandidateChanges(ctx, candidate.BaseCommit, candidate.Commit, DiffLimits{})
	if err != nil {
		t.Fatalf("CandidateChanges() error = %v", err)
	}
	if !strings.Contains(change.Changes.Patch, "+the change") || strings.Contains(change.Changes.Patch, "other work") {
		t.Fatalf("candidate patch = %q, want the change over the target and nothing of the target itself", change.Changes.Patch)
	}

	if err := manager.RemoveQueueCandidate(ctx, candidate.Path); err != nil {
		t.Fatalf("RemoveQueueCandidate() error = %v", err)
	}
	if _, err := os.Lstat(candidate.Path); !os.IsNotExist(err) {
		t.Fatalf("Lstat() after removal = %v, want the checkout gone", err)
	}
	if err := manager.RemoveQueueCandidate(ctx, repository); err == nil {
		t.Fatal("RemoveQueueCandidate() removed a path that is not a candidate checkout")
	}
}

func TestARestoredCandidateIsTheSameCommitNotARebuild(t *testing.T) {
	t.Parallel()

	repository, head := queueRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	ctx := context.Background()
	candidate, err := manager.BuildQueueCandidate(ctx, QueueCandidateRequest{Entry: testQueueEntry, TargetBranch: "main", Heads: []string{head}})
	if err != nil {
		t.Fatalf("BuildQueueCandidate() error = %v", err)
	}

	// A clean checkout at the candidate is kept as it stands.
	path, err := manager.RestoreQueueCandidate(ctx, testQueueEntry, candidate.Commit)
	if err != nil || path != candidate.Path {
		t.Fatalf("RestoreQueueCandidate() = %s, %v; want the standing checkout", path, err)
	}
	// One a dead worker left dirty, or one that is gone, is cut again from the
	// recorded commit.
	writeFile(t, candidate.Path, "change.txt", "half-written\n")
	if path, err = manager.RestoreQueueCandidate(ctx, testQueueEntry, candidate.Commit); err != nil {
		t.Fatalf("RestoreQueueCandidate(dirty) error = %v", err)
	}
	if readFile(t, path, "change.txt") != "the change\n" || gitLine(t, path, "rev-parse", "HEAD") != candidate.Commit {
		t.Fatal("the restored checkout is not the recorded candidate")
	}
	if err := manager.RemoveQueueCandidate(ctx, path); err != nil {
		t.Fatal(err)
	}
	if path, err = manager.RestoreQueueCandidate(ctx, testQueueEntry, candidate.Commit); err != nil || gitLine(t, path, "rev-parse", "HEAD") != candidate.Commit {
		t.Fatalf("RestoreQueueCandidate(gone) = %s, %v; want the recorded candidate cut again", path, err)
	}
}

func TestACandidateThatWillNotMergeLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	repository, _ := queueRepository(t)
	runGit(t, repository, "switch", "-c", "conflicting", "main~1")
	writeFile(t, repository, "other.txt", "a different other\n")
	runGit(t, repository, "add", "other.txt")
	runGit(t, repository, "commit", "-m", "disagrees with main")
	head := gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "switch", "main")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	_, err := manager.BuildQueueCandidate(context.Background(), QueueCandidateRequest{Entry: testQueueEntry, TargetBranch: "main", Heads: []string{head}})
	var conflict *QueueCandidateConflict
	if !errors.As(err, &conflict) || len(conflict.Paths) != 1 || conflict.Paths[0] != "other.txt" {
		t.Fatalf("BuildQueueCandidate() error = %v, want a conflict on other.txt", err)
	}
	if _, err := os.Lstat(filepath.Join(manager.worktreeRoot, queueCandidateDirectoryName(testQueueEntry))); !os.IsNotExist(err) {
		t.Fatalf("Lstat() = %v, want no checkout left behind", err)
	}
}

func TestAHeadTheTargetAlreadyHoldsIsNoCandidate(t *testing.T) {
	t.Parallel()

	repository, _ := queueRepository(t)
	held := gitLine(t, repository, "rev-parse", "main~1")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	if _, err := manager.BuildQueueCandidate(context.Background(), QueueCandidateRequest{Entry: testQueueEntry, TargetBranch: "main", Heads: []string{held}}); !errors.Is(err, ErrQueueCandidateEmpty) {
		t.Fatalf("BuildQueueCandidate() error = %v, want ErrQueueCandidateEmpty", err)
	}
}
