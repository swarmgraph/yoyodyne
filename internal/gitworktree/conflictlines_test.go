package gitworktree

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A conflict's lines are counted from the versions Git left, so text that looks
// like a marker is not mistaken for one, and a conflict with no lines to count
// is not known rather than nothing.
func TestAConflictsLinesAreCountedOrSaidNotToBeKnown(t *testing.T) {
	t.Parallel()

	const markerLike = "<<<<<<< not a marker\n"
	for _, test := range []struct {
		name         string
		base         string
		ours, theirs string
		lines        int
		unknown      string
	}{
		{name: "both changed one line", base: "keep\nold\n" + markerLike, ours: "keep\nours\n" + markerLike, theirs: "keep\none\ntwo\n" + markerLike, lines: 3},
		{name: "both added the file", ours: "ours\n", theirs: "theirs\n", lines: 2},
		{name: "one side deleted the file", base: "old\n", ours: "new\n", unknown: "kept by one side"},
		{name: "not text", base: "a\x00b\n", ours: "a\x00c\n", theirs: "a\x00d\n", unknown: "could not be counted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository, _ := queueRepository(t)
			write := func(content, message string) {
				if content == "" {
					runGit(t, repository, "rm", "-q", "--ignore-unmatch", "conflict.txt")
				} else {
					writeFile(t, repository, "conflict.txt", content)
					runGit(t, repository, "add", "conflict.txt")
				}
				runGit(t, repository, "commit", "--allow-empty", "-m", message)
			}
			write(test.base, "base")
			runGit(t, repository, "switch", "-c", "theirs")
			write(test.theirs, "theirs")
			head := gitLine(t, repository, "rev-parse", "HEAD")
			runGit(t, repository, "switch", "main")
			write(test.ours, "ours")
			manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
			_, err := manager.BuildQueueCandidate(context.Background(), QueueCandidateRequest{Entry: testQueueEntry, TargetBranch: "main", Heads: []string{head}})
			var conflict *QueueCandidateConflict
			if !errors.As(err, &conflict) {
				t.Fatalf("BuildQueueCandidate() error = %v, want a conflict", err)
			}
			got := conflict.Lines
			if test.unknown != "" {
				if got.Lines != nil || !strings.Contains(got.Unknown, test.unknown) {
					t.Fatalf("lines = %+v, want not known: %q", got, test.unknown)
				}
				return
			}
			if got.Lines == nil || *got.Lines != test.lines || got.Unknown != "" {
				t.Fatalf("lines = %v (%q), want %d", got.Lines, got.Unknown, test.lines)
			}
		})
	}
}
