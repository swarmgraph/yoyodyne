package testharness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// NewOutcomeTracker is the work item every run here is made for.
func NewOutcomeTracker() *orchestratortest.Tracker {
	return &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
}

// WriteFeature is the change a developer attempt makes, so a preserved worktree
// has something in it to be preserved.
func WriteFeature(request backend.RunRequest) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
}

// OnlyRecordedRun reads the history back the way the listing does and returns
// the single run it holds, so a test asserts over what an operator would be
// shown rather than over the state file behind it.
func OnlyRecordedRun(t *testing.T, store *runstate.Store) runstate.RunSummary {
	t.Helper()
	history, err := store.History(runstate.RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history = %#v, want the one run this test made", history.Runs)
	}
	return history.Runs[0]
}
