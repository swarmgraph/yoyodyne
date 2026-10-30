package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Another contributor's merge lands on the remote while the factory is idle.
// The next run is cut from the target the harness fetched just before it, so
// the merge is in its worktree, and its record and the item's notes name that
// target as fetched for the run.
func TestPipelineStartsARunFromTheTargetTheRemoteMovedTo(t *testing.T) {
	t.Parallel()

	repository, tracker, _, pipeline, store := automaticFixture(t)
	remote := addBareRemote(t, repository)
	before := gitLine(t, repository, "rev-parse", "refs/heads/main")
	theirs := commitOnRemote(t, remote, "theirs.txt", "merged by somebody else\n")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to succeed", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	started := state.StartingTarget
	if started == nil || !started.Fetched || started.Failure != "" || !started.Advanced {
		t.Fatalf("starting target = %#v, want main fetched and advanced for the run", started)
	}
	if started.Commit != theirs || started.RemoteCommit != theirs {
		t.Fatalf("starting target = %#v, want the run started from %s rather than %s", started, theirs, before)
	}
	if outcome.BaseCommit != theirs {
		t.Errorf("base commit = %q, want %q", outcome.BaseCommit, theirs)
	}
	if content, err := os.ReadFile(filepath.Join(repository, "theirs.txt")); err != nil || string(content) != "merged by somebody else\n" {
		t.Errorf("theirs.txt = %q, %v; want the remote's merge under the integrated change", content, err)
	}
	if !strings.Contains(tracker.Notes, "started from main at "+theirs+", fetched from origin for this run") {
		t.Errorf("notes = %q, want the starting target named", tracker.Notes)
	}
}

// A fetch that fails starts the run anyway, from the target the last fetch
// left, and the record and the notes say the fetch failed and how old the
// target was.
func TestPipelineStartsARunWhoseFetchFailedFromTheLastFetchedTarget(t *testing.T) {
	t.Parallel()

	repository, tracker, _, pipeline, store := automaticFixture(t)
	remote := addBareRemote(t, repository)
	theirs := commitOnRemote(t, remote, "theirs.txt", "merged by somebody else\n")
	fetchedAt := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	if fetch := pipeline.Worktrees.(TargetFetcher).FetchTarget(context.Background(), "main", fetchedAt); !fetch.Fetched || fetch.Commit != theirs {
		t.Fatalf("earlier fetch = %#v, want main fetched at %s", fetch, theirs)
	}
	commitOnRemote(t, remote, "later.txt", "never seen here\n")
	runPipelineGit(t, repository, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "unreachable.git"))

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to succeed whatever the network did", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	started := state.StartingTarget
	if started == nil || started.Fetched || started.Failure == "" {
		t.Fatalf("starting target = %#v, want the failed fetch recorded", started)
	}
	if started.Commit != theirs || outcome.BaseCommit != theirs {
		t.Fatalf("starting target = %#v, base %s; want the last fetched target %s", started, outcome.BaseCommit, theirs)
	}
	if !started.LastFetchedAt.Equal(fetchedAt) {
		t.Errorf("last fetched = %s, want %s", started.LastFetchedAt, fetchedAt)
	}
	if age, ok := started.Age(); !ok || age < 3*time.Hour || age > 4*time.Hour {
		t.Errorf("age = %s, %t; want about three hours", age, ok)
	}
	if !strings.Contains(tracker.Notes, "the target the last fetch left, because fetching it from origin failed") ||
		!strings.Contains(tracker.Notes, "it was last fetched 3 hours") {
		t.Errorf("notes = %q, want the failed fetch and the target's age named", tracker.Notes)
	}
}

// commitOnRemote puts a commit on the remote's main that this repository has
// never seen, which is another contributor's merge as it looks from here.
func commitOnRemote(t *testing.T, remote, relative, content string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "elsewhere")
	runPipelineGit(t, filepath.Dir(clone), "clone", "--quiet", remote, clone)
	runPipelineGit(t, clone, "config", "user.name", "Someone Else")
	runPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	disablePipelineMaintenance(t, clone)
	if err := os.WriteFile(filepath.Join(clone, relative), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, clone, "add", relative)
	runPipelineGit(t, clone, "commit", "--quiet", "-m", "work from elsewhere")
	runPipelineGit(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return gitLine(t, clone, "rev-parse", "HEAD")
}
