package gitworktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run starts from the target the remote has just now, not the one the local
// branch last saw: another contributor's merge on the remote is in the base the
// next worktree is cut from.
func TestFetchTargetStartsFromTheTargetTheRemoteMovedTo(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	before := gitLine(t, repository, "rev-parse", "refs/heads/main")
	elsewhere := commitElsewhere(t, remote, map[string]string{"theirs.txt": "merged by somebody else\n"})
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

	fetch := manager.FetchTarget(context.Background(), "main", now)
	if !fetch.Fetched || fetch.Failure != "" || fetch.Held != "" || !fetch.Advanced {
		t.Fatalf("fetch = %#v, want main fetched and advanced", fetch)
	}
	if fetch.Commit != elsewhere || fetch.RemoteCommit != elsewhere || fetch.Remote != "origin" {
		t.Fatalf("fetch = %#v, want the run to start from %s, not %s", fetch, elsewhere, before)
	}
	if !fetch.LastFetchedAt.Equal(now) {
		t.Errorf("last fetched = %s, want %s", fetch.LastFetchedAt, now)
	}
	if local := gitLine(t, repository, "rev-parse", "refs/heads/main"); local != elsewhere {
		t.Errorf("local main = %q, want %q", local, elsewhere)
	}

	// The worktree the run is cut from carries the other contributor's work.
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-fetched", BaseRef: "main", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.BaseCommit != elsewhere {
		t.Errorf("worktree base = %q, want %q", worktree.BaseCommit, elsewhere)
	}
	if content := readFile(t, worktree.Path, "theirs.txt"); content != "merged by somebody else\n" {
		t.Errorf("theirs.txt = %q, want the remote's merge in the worktree", content)
	}
}

// A fetch that fails starts the run from the target the last fetch left, and
// says when that was, so the record can say how old the target is. A second
// successful fetch that finds the same commit still moves the moment on.
func TestFetchTargetThatFailsStartsFromTheLastFetchedTargetAndSaysHowOld(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	elsewhere := commitElsewhere(t, remote, map[string]string{"theirs.txt": "merged by somebody else\n"})
	first := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	if fetch := manager.FetchTarget(context.Background(), "main", first); !fetch.Fetched {
		t.Fatalf("first fetch = %#v, want it fetched", fetch)
	}
	again := first.Add(30 * time.Minute)
	if fetch := manager.FetchTarget(context.Background(), "main", again); !fetch.Fetched || fetch.Advanced || !fetch.LastFetchedAt.Equal(again) {
		t.Fatalf("repeated fetch = %#v, want it fetched at %s with nothing to move", fetch, again)
	}
	if recorded := gitLine(t, repository, "rev-parse", FetchedTargetRef("main")+"^"); recorded != elsewhere {
		t.Errorf("recorded fetch = %q, want %q", recorded, elsewhere)
	}

	// The remote moves again, and then cannot be reached.
	commitElsewhere(t, remote, map[string]string{"later.txt": "never seen here\n"})
	runGit(t, repository, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	fetch := manager.FetchTarget(context.Background(), "main", again.Add(2*time.Hour))
	if fetch.Fetched || fetch.Failure == "" {
		t.Fatalf("fetch = %#v, want the failed fetch reported", fetch)
	}
	if fetch.Commit != elsewhere {
		t.Errorf("fetch commit = %q, want the last fetched target %q", fetch.Commit, elsewhere)
	}
	if !fetch.LastFetchedAt.Equal(again) {
		t.Errorf("last fetched = %s, want %s", fetch.LastFetchedAt, again)
	}
	if fetch.CommittedAt.IsZero() {
		t.Errorf("committed at is zero, want the starting commit's time")
	}
	if local := gitLine(t, repository, "rev-parse", "refs/heads/main"); local != elsewhere {
		t.Errorf("local main = %q, want it left at %q", local, elsewhere)
	}
}

// A repository that has no remote has nothing to fetch, and that is not a
// failure: its local branch is the latest target there is.
func TestFetchTargetWithNoRemoteStartsFromTheLocalBranch(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newConvergeManager(t, repository)
	local := gitLine(t, repository, "rev-parse", "refs/heads/main")

	fetch := manager.FetchTarget(context.Background(), "main", time.Now())
	if fetch.Fetched || fetch.Failure != "" || fetch.Remote != "" || fetch.Commit != local {
		t.Fatalf("fetch = %#v, want the local branch at %s with nothing fetched or failed", fetch, local)
	}
}

// A remote that holds history the local branch does not lie under is read, and
// the run starts from the local branch, with the reason named.
func TestFetchTargetHeldByADivergedRemoteStartsFromTheLocalBranch(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	local := gitLine(t, repository, "rev-parse", "refs/heads/main")
	rewritten := rewriteRemoteBranch(t, remote, "main")

	fetch := manager.FetchTarget(context.Background(), "main", time.Now())
	if !fetch.Fetched || fetch.Advanced || fetch.Commit != local || !strings.Contains(fetch.Held, rewritten) {
		t.Fatalf("fetch = %#v, want the divergence held and the run started from %s", fetch, local)
	}
}

// A project that integrates locally moves its target ahead of a remote it does
// not push to. That is not a split history, so nothing is held and nothing is
// said to need a person: the run starts from the local branch.
func TestFetchTargetWithTheLocalBranchAheadOfTheRemoteHoldsNothing(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	published := gitLine(t, remote, "rev-parse", "refs/heads/main")
	writeFile(t, repository, "promoted.txt", "promoted here, never pushed\n")
	runGit(t, repository, "add", "promoted.txt")
	runGit(t, repository, "commit", "-m", "a local promotion")
	local := gitLine(t, repository, "rev-parse", "refs/heads/main")

	fetch := manager.FetchTarget(context.Background(), "main", time.Now())
	if !fetch.Fetched || fetch.Held != "" || fetch.Failure != "" || fetch.Advanced || !fetch.Ahead {
		t.Fatalf("fetch = %#v, want main fetched, ahead of the remote, and held on nothing", fetch)
	}
	if fetch.Commit != local || fetch.RemoteCommit != published {
		t.Fatalf("fetch = %#v, want the run started from the local %s with the remote at %s", fetch, local, published)
	}
	if moved := gitLine(t, repository, "rev-parse", "refs/heads/main"); moved != local {
		t.Errorf("local main = %q, want it left at %q", moved, local)
	}
}
