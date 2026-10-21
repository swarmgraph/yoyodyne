package gitworktree

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A revert git made names the commit it undid, and that is what is read back:
// an ordinary commit, and one whose message mentions reverting in passing, are
// not reverts.
func TestRevertsOnReadsTheCommitsGitSaysWereReverted(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, "a.txt", "one\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "the first change")
	first := gitLine(t, repository, "rev-parse", "HEAD")
	writeFile(t, repository, "b.txt", "two\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "the second change", "-m", "It says This reverts commit nothing, mid-line.")
	runGit(t, repository, "revert", "--no-edit", first)
	revert := gitLine(t, repository, "rev-parse", "HEAD")
	branch := gitLine(t, repository, "rev-parse", "--abbrev-ref", "HEAD")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	reverts, err := manager.RevertsOn(context.Background(), branch, time.Time{})
	if err != nil {
		t.Fatalf("RevertsOn() error = %v", err)
	}
	if len(reverts) != 1 || reverts[0].Commit != revert || len(reverts[0].Reverts) != 1 || reverts[0].Reverts[0] != first || reverts[0].At.IsZero() {
		t.Fatalf("RevertsOn() = %+v, want one revert %s of %s", reverts, revert, first)
	}
	// Nothing committed after a moment is nothing reverted since it.
	later, err := manager.RevertsOn(context.Background(), branch, time.Now().Add(time.Hour))
	if err != nil || len(later) != 0 {
		t.Fatalf("RevertsOn(an hour from now) = %+v, %v; want none", later, err)
	}
}

func TestParseRevertsReadsOldestFirstAndRefusesWhatIsNotACommit(t *testing.T) {
	t.Parallel()
	newer := "b" + repeat("0", 39)
	older := "a" + repeat("0", 39)
	log := newer + "\x1f2026-10-02T10:00:00-07:00\x1fRevert \"y\"\n\nThis reverts commit " + repeat("2", 40) + ", reversing\nchanges made to " + repeat("3", 40) + ".\n\x1e\n" +
		older + "\x1f2026-10-01T10:00:00-07:00\x1fRevert \"x\"\n\nThis reverts commit " + repeat("1", 40) + ".\n\x1e\n"
	reverts, err := parseReverts(log)
	if err != nil || len(reverts) != 2 || reverts[0].Commit != older || reverts[1].Reverts[0] != repeat("2", 40) {
		t.Fatalf("parseReverts() = %+v, %v", reverts, err)
	}
	if _, err := parseReverts("not a commit\x1e"); err == nil {
		t.Fatal("parseReverts() read an entry with no fields")
	}
}

func repeat(text string, count int) string {
	result := ""
	for range count {
		result += text
	}
	return result
}
