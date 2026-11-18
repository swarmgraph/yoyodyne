package gitworktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A run's change that reached the target as a different commit — a squash
// under another pull request — is found by its content, and the commit that
// brought it in names that pull request.
func TestChangeLandedFindsAChangeSquashedInThroughAnotherPullRequest(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-landed", BaseRef: "HEAD", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "the change\n")
	publication, err := manager.PublishBranch(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("PublishBranch() error = %v", err)
	}

	before, err := manager.ChangeLanded(context.Background(), "main", publication.Commit)
	if err != nil {
		t.Fatalf("ChangeLanded() before error = %v", err)
	}
	if before.Landed || before.Unread != "" || before.Commit != "" {
		t.Fatalf("before landing = %#v, want not landed", before)
	}

	// Somebody else's commit first, then the change squashed in by another
	// pull request, then another commit after it: the commit named is the one
	// in the middle.
	pushUnrelatedCommit(t, remote, "main")
	squash := squashInRemote(t, remote, "main", publication.Commit, "Same change, landed again (#1535)")
	addInRemote(t, remote, "main", "later.txt", "later work\n", "later work")

	after, err := manager.ChangeLanded(context.Background(), "main", publication.Commit)
	if err != nil {
		t.Fatalf("ChangeLanded() after error = %v", err)
	}
	if !after.Landed || after.Commit != squash || after.Number != 1535 || after.Unread != "" {
		t.Fatalf("after landing = %#v, want landed at %s through #1535", after, squash)
	}
}

// A head this repository does not have is not known, not "no": nothing may be
// closed on a guess about a change nobody can read.
func TestChangeLandedReportsAHeadItCannotReadAsUnread(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	landing, err := manager.ChangeLanded(context.Background(), "main", strings.Repeat("ab", 20))
	if err != nil {
		t.Fatalf("ChangeLanded() error = %v", err)
	}
	if landing.Landed || !strings.Contains(landing.Unread, "is not in this repository") {
		t.Fatalf("landing = %#v, want unread", landing)
	}
}

// The release of a replaced branch is a compare-and-swap on the commit its pull
// request carried, so a branch with anything more on it is left.
func TestReleaseReplacedBranchDeletesOnlyAtThePublishedCommit(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-release", BaseRef: "HEAD", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "the change\n")
	publication, err := manager.PublishBranch(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("PublishBranch() error = %v", err)
	}
	runGit(t, repository, "worktree", "remove", "--force", worktree.Path)
	if _, err := manager.ReleaseReplacedBranch(context.Background(), worktree.Branch, worktree.BaseCommit); err == nil || !strings.Contains(err.Error(), "want the integrated commit") {
		t.Fatalf("ReleaseReplacedBranch() at another commit error = %v", err)
	}
	released, err := manager.ReleaseReplacedBranch(context.Background(), worktree.Branch, publication.Commit)
	if err != nil || !released {
		t.Fatalf("ReleaseReplacedBranch() = %v, %v", released, err)
	}
	if _, err := attemptGit(repository, "show-ref", "--verify", "--quiet", "refs/heads/"+worktree.Branch); err == nil {
		t.Fatalf("branch %s is still there", worktree.Branch)
	}
}

func TestPullRequestNamedReadsTheForgesSubjects(t *testing.T) {
	t.Parallel()
	for subject, want := range map[string]int{
		"Merge pull request #2021 from swarmgraph/yoyodyne/x": 2021,
		"Bring in Chart.js (#1535)":                           1535,
		"yoyodyne: merge queue candidate for an item":         0,
		"Fix #12 in passing":                                  0,
	} {
		if got := PullRequestNamed(subject); got != want {
			t.Errorf("PullRequestNamed(%q) = %d, want %d", subject, got, want)
		}
	}
}

// squashInRemote lands the change at promoted on branch as one new commit with
// the forge's squash subject, which is not promoted and does not carry it.
func squashInRemote(t *testing.T, remote, branch, promoted, subject string) string {
	t.Helper()
	tip := gitLine(t, remote, "rev-parse", "refs/heads/"+branch)
	base := gitLine(t, remote, "merge-base", tip, promoted)
	merged := gitLine(t, remote, "merge-tree", "--write-tree", "--merge-base="+base, tip, promoted)
	commit := gitLine(t, remote, "-c", "user.name=Forge", "-c", "user.email=forge@example.invalid",
		"commit-tree", strings.Fields(merged)[0], "-p", tip, "-m", subject)
	runGit(t, remote, "update-ref", "refs/heads/"+branch, commit, tip)
	return commit
}

// addInRemote commits one file onto branch in the remote from a clone of its
// own, as another machine would.
func addInRemote(t *testing.T, remote, branch, file, content, subject string) string {
	t.Helper()
	root := t.TempDir()
	clone := filepath.Join(root, "another")
	runGit(t, root, "clone", "--branch", branch, remote, clone)
	runGit(t, clone, "config", "user.name", "Someone Else")
	runGit(t, clone, "config", "user.email", "someone@example.invalid")
	disableBackgroundMaintenance(t, clone)
	writeFile(t, clone, file, content)
	runGit(t, clone, "add", ".")
	runGit(t, clone, "commit", "-m", subject)
	runGit(t, clone, "push", "origin", "HEAD:refs/heads/"+branch)
	return gitLine(t, clone, "rev-parse", "HEAD")
}
