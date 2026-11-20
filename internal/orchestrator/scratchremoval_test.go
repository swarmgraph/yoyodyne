package orchestrator

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run that did not land is cleaned up by the sweep that retires its
// worktree, and its scratch folder goes with that worktree, whatever the run
// ended as: a link inside is removed as a link and what it points at stays.
func TestRetiringAStoppedRunsWorktreeRemovesItsScratch(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	run := pendingRun(0)
	run.Status, run.StartedAt, run.UpdatedAt, run.CompletedAt = runstate.StatusFailed, at, at, &at
	if err := store.Create(run); err != nil {
		t.Fatal(err)
	}
	scratch, err := execution.PrepareScratchDirectory(state, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(target, []byte("a program\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(scratch, "apply_patch")); err != nil {
		t.Fatal(err)
	}

	r := Reconciler{Store: store, StateRoot: state}
	if problem := r.recordSweptWorktree(&run, ""); problem != "" {
		t.Fatalf("recordSweptWorktree() = %q", problem)
	}
	if _, err := os.Lstat(scratch); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the stopped run's scratch folder is still there (err = %v)", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("removing the scratch folder removed what its link pointed at: %v", err)
	}
	if !run.WorktreeRemoved {
		t.Fatal("the run's record does not say its worktree was removed")
	}
}

// A stopped run that a fresh run replaces has its worktree retired by the
// re-run, and its scratch folder goes with it: once the worktree is recorded
// removed no sweep visits the run again, so this is the only chance. A link
// inside is removed as a link, and what it points at stays.
func TestRetiringAReplacedRunsWorktreeRemovesItsScratch(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	stopped := pendingRun(0)
	stopped.Status, stopped.StartedAt, stopped.UpdatedAt, stopped.CompletedAt = runstate.StatusFailed, at, at, &at
	if err := store.Create(stopped); err != nil {
		t.Fatal(err)
	}
	scratch, err := execution.PrepareScratchDirectory(state, stopped.RunID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := execution.PrepareScratchDirectory(state, runIDFor(1))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(target, []byte("a program\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(scratch, "apply_patch")); err != nil {
		t.Fatal(err)
	}

	r := Rerunner{Runs: store, StateRoot: state}
	retirement := gitworktree.Retirement{Worktree: gitworktree.WorktreeRemoval{Removed: true}}
	if problem := r.recordRemoval(stopped, runIDFor(1), retirement, false); problem != "" {
		t.Fatalf("recordRemoval() = %q", problem)
	}
	if _, err := os.Lstat(scratch); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the replaced run's scratch folder is still there (err = %v)", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("removing the scratch folder removed what its link pointed at: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("the fresh run's scratch folder went with the replaced run's: %v", err)
	}
	recorded, err := store.Load(stopped.RunID)
	if err != nil || !recorded.WorktreeRemoved {
		t.Fatalf("the replaced run's record = %v, %v; want its worktree recorded removed", recorded.WorktreeRemoved, err)
	}

	// A retirement that kept the worktree keeps the scratch folder too: the run
	// can still be continued in it.
	kept, err := execution.PrepareScratchDirectory(state, runIDFor(2))
	if err != nil {
		t.Fatal(err)
	}
	unremoved := pendingRun(2)
	unremoved.Status, unremoved.StartedAt, unremoved.UpdatedAt, unremoved.CompletedAt = runstate.StatusFailed, at, at, &at
	if err := store.Create(unremoved); err != nil {
		t.Fatal(err)
	}
	_ = r.recordRemoval(unremoved, runIDFor(1), gitworktree.Retirement{}, false)
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("a retirement that kept the worktree removed its scratch: %v", err)
	}
}
