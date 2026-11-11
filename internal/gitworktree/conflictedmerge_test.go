package gitworktree

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// conflictingHead is a branch off the commit before main's last, which adds one
// file main also added, differently, and one main does not have.
func conflictingHead(t *testing.T, repository string) string {
	t.Helper()
	runGit(t, repository, "switch", "-c", "conflicting", "main~1")
	writeFile(t, repository, "other.txt", "a different other\n")
	writeFile(t, repository, "clean.txt", "a clean addition\n")
	runGit(t, repository, "add", "other.txt", "clean.txt")
	runGit(t, repository, "commit", "-m", "disagrees with main on other.txt")
	head := gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "switch", "main")
	return head
}

func TestAConflictedMergeIsTheTargetWithTheMergeLaidOverItAsGitLeftIt(t *testing.T) {
	t.Parallel()

	repository, _ := queueRepository(t)
	head := conflictingHead(t, repository)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	ctx := context.Background()
	target := gitLine(t, repository, "rev-parse", "main")

	built, err := manager.BuildConflictedMerge(ctx, ConflictedMergeRequest{Entry: testQueueEntry, TargetBranch: "main", Head: head})
	if err != nil {
		t.Fatalf("BuildConflictedMerge() error = %v", err)
	}
	if len(built.Paths) != 1 || built.Paths[0] != "other.txt" || built.BaseCommit != target || built.Head != head {
		t.Fatalf("BuildConflictedMerge() = %#v, want other.txt conflicting on %s", built, target)
	}
	// Its only parent is the target, so a change made on it is the resolution.
	if parents := gitLine(t, repository, "rev-list", "--parents", "-n", "1", built.Commit); parents != built.Commit+" "+target {
		t.Fatalf("conflicted merge parents = %q, want the target alone", parents)
	}
	if author := gitLine(t, repository, "log", "-1", "--format=%an", built.Commit); author != harnessCommitAuthorName {
		t.Fatalf("author = %q, want the harness", author)
	}
	marked := gitLine(t, repository, "show", built.Commit+":other.txt")
	if !strings.Contains(marked, "<<<<<<<") || !strings.Contains(marked, "=======") || !strings.Contains(marked, ">>>>>>>") {
		t.Fatalf("other.txt in the conflicted merge = %q, want Git's conflict markers", marked)
	}
	if clean := gitLine(t, repository, "show", built.Commit+":clean.txt"); clean != "a clean addition" {
		t.Fatalf("clean.txt = %q, want the clean part of the merge applied", clean)
	}
	// The checkout it was built in is gone, and nothing moved.
	if _, err := manager.RestoreQueueCandidate(ctx, testQueueEntry, built.Commit); err != nil {
		t.Fatalf("the checkout name was not free afterwards: %v", err)
	}
	if now := gitLine(t, repository, "rev-parse", "main"); now != target {
		t.Fatalf("main moved to %s", now)
	}
}

func TestAMergeThatDoesNotConflictIsNoConflictedMerge(t *testing.T) {
	t.Parallel()

	repository, head := queueRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	if _, err := manager.BuildConflictedMerge(context.Background(), ConflictedMergeRequest{Entry: testQueueEntry, TargetBranch: "main", Head: head}); !errors.Is(err, ErrMergeDoesNotConflict) {
		t.Fatalf("BuildConflictedMerge() error = %v, want ErrMergeDoesNotConflict", err)
	}
}

func TestCommitMergeOfTakesTheSourcesContentAndTheTwoParentsInOrder(t *testing.T) {
	t.Parallel()

	repository, head := queueRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	ctx := context.Background()
	base := gitLine(t, repository, "rev-parse", "main")
	// The source is unrelated to either parent; only its content counts.
	runGit(t, repository, "switch", "-c", "resolved", "main")
	writeFile(t, repository, "resolved.txt", "resolved\n")
	runGit(t, repository, "add", "resolved.txt")
	runGit(t, repository, "commit", "-m", "the resolution")
	source := gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "switch", "main")
	refs := gitLine(t, repository, "for-each-ref", "--format=%(refname) %(objectname)")

	merge, err := manager.CommitMergeOf(ctx, source, base, head, "yoyodyne: the merge")
	if err != nil {
		t.Fatalf("CommitMergeOf() error = %v", err)
	}
	if parents := gitLine(t, repository, "rev-list", "--parents", "-n", "1", merge); parents != merge+" "+base+" "+head {
		t.Fatalf("parents = %q, want the base then the head", parents)
	}
	if got, want := gitLine(t, repository, "rev-parse", merge+"^{tree}"), gitLine(t, repository, "rev-parse", source+"^{tree}"); got != want {
		t.Fatalf("tree = %s, want the source's %s", got, want)
	}
	if author := gitLine(t, repository, "log", "-1", "--format=%an", merge); author != harnessCommitAuthorName {
		t.Fatalf("author = %q, want the harness", author)
	}
	if after := gitLine(t, repository, "for-each-ref", "--format=%(refname) %(objectname)"); after != refs {
		t.Fatalf("refs changed from\n%s\nto\n%s", refs, after)
	}
	if _, err := manager.CommitMergeOf(ctx, "nope", base, head, ""); err == nil {
		t.Fatal("CommitMergeOf() accepted a source that is not a commit id")
	}
}
