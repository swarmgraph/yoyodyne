package orchestrator

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
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
