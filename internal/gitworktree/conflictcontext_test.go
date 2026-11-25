package gitworktree

import (
	"context"
	"strings"
	"testing"
)

func TestARangeIsDescribedWithoutAWorktreeAndOnlyItsCommitsToThePathsAreListed(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, t.TempDir())
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")
	writeFile(t, repository, "README.txt", "an earlier answer\n")
	runGit(t, repository, "add", "README.txt")
	runGit(t, repository, "commit", "-m", "conflicting target change")
	writeFile(t, repository, "unrelated.txt", "elsewhere\n")
	runGit(t, repository, "add", "unrelated.txt")
	runGit(t, repository, "commit", "-m", "an unrelated change")
	writeFile(t, repository, "README.txt", "a later answer\n")
	runGit(t, repository, "add", "README.txt")
	runGit(t, repository, "commit", "-m", "a later change to it")
	tip := gitLine(t, repository, "rev-parse", "refs/heads/main")
	changes, err := manager.RangeChanges(context.Background(), base, tip, DiffLimits{})
	if err != nil || !strings.Contains(changes.Patch, "+a later answer") || !strings.Contains(changes.Patch, "unrelated.txt") {
		t.Fatalf("RangeChanges() = %#v, %v; want the whole range", changes, err)
	}
	commits, omitted, err := manager.CommitsTouching(context.Background(), base, tip, []string{"README.txt"}, 10)
	if err != nil || omitted != 0 || len(commits) != 2 || commits[0].Subject != "conflicting target change" || commits[1].Subject != "a later change to it" {
		t.Fatalf("CommitsTouching() = %v, %d, %v; want the two commits to the file, oldest first", commits, omitted, err)
	}
	newest, omitted, err := manager.CommitsTouching(context.Background(), base, tip, []string{"README.txt"}, 1)
	if err != nil || omitted != 1 || len(newest) != 1 || newest[0].Commit != tip {
		t.Fatalf("CommitsTouching() bounded = %v, %d, %v; want the newest and one left out", newest, omitted, err)
	}
	if _, _, err := manager.CommitsTouching(context.Background(), base, tip, nil, 1); err == nil {
		t.Fatal("CommitsTouching() listed commits for no paths")
	}
	if _, err := manager.RangeChanges(context.Background(), "main", tip, DiffLimits{}); err == nil {
		t.Fatal("RangeChanges() accepted a name in place of a commit")
	}
}
