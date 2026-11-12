package gitworktree

import (
	"context"
	"testing"
)

func TestSharedFileReadsWhatTheRemoteBranchHoldsAndMovesNothingLocal(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	ctx := context.Background()

	if _, found, err := manager.SharedFile(ctx, "main", ".beads/issues.jsonl"); err != nil || found {
		t.Fatalf("a branch without the file: found %v, error %v", found, err)
	}
	if _, found, err := manager.SharedFile(ctx, "no-such-branch", ".beads/issues.jsonl"); err != nil || found {
		t.Fatalf("a branch the remote lacks: found %v, error %v", found, err)
	}

	before := gitLine(t, repository, "rev-parse", "main")
	want := "{\"id\":\"t-a\",\"title\":\"exact bytes\\tkept\"}\n"
	commitElsewhere(t, remote, map[string]string{".beads/issues.jsonl": want})
	content, found, err := manager.SharedFile(ctx, "main", ".beads/issues.jsonl")
	if err != nil || !found {
		t.Fatalf("found %v, error %v", found, err)
	}
	if string(content) != want {
		t.Errorf("content = %q, want %q", content, want)
	}
	if after := gitLine(t, repository, "rev-parse", "main"); after != before {
		t.Errorf("the local branch moved from %s to %s", before, after)
	}
}
