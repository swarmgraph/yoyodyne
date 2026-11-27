package gitworktree

import (
	"context"
	"strings"
	"testing"
)

// A commit fast-forwarded onto the target by hand is in its history, and one on
// a branch the target never took is not.
func TestContainsCommitReadsTheLocalTargetBranch(t *testing.T) {
	t.Parallel()
	repository := newRepository(t)
	manager := newManager(t, repository, t.TempDir())
	runGit(t, repository, "checkout", "-b", "work")
	writeFile(t, repository, "work.txt", "work\n")
	runGit(t, repository, "add", "work.txt")
	runGit(t, repository, "commit", "-m", "work")
	head := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))
	runGit(t, repository, "checkout", "main")

	contained, err := manager.ContainsCommit(context.Background(), head, "main")
	if err != nil || contained {
		t.Fatalf("before the fast-forward ContainsCommit() = %v, %v, want false", contained, err)
	}
	runGit(t, repository, "merge", "--ff-only", "work")
	if contained, err = manager.ContainsCommit(context.Background(), head, "main"); err != nil || !contained {
		t.Fatalf("after the fast-forward ContainsCommit() = %v, %v, want true", contained, err)
	}
	if _, err := manager.ContainsCommit(context.Background(), "not-a-commit", "main"); err == nil {
		t.Fatal("ContainsCommit() accepted a commit that is not a full commit id")
	}
	if contained, err := manager.ContainsCommit(context.Background(), strings.Repeat("a", 40), "main"); err != nil || contained {
		t.Fatalf("for a commit the repository lacks ContainsCommit() = %v, %v, want false", contained, err)
	}
}
