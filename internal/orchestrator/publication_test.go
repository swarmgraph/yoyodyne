package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// publicationFixture is a repository, a worktree root and a run state store a
// second process can also see, which is what a reconciling sweep is.
type publicationFixture struct {
	repository   string
	worktreeRoot string
	store        *runstate.Store
	tracker      *orchestratortest.Tracker
}

// newPublicationFixture drives a whole publishing run that ends blocked with
// nothing integrated, and hands back the record it left. That is the run this
// sweep exists for: its pull request is recorded exactly as it stood when the
// run ended, and no settle path is ever going to ask about it again.
func newPublicationFixture(t *testing.T) (publicationFixture, runstate.State) {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	remote := addBareRemote(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider), &orchestratortest.Forge{Remote: remote})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() error = nil, want the run to end on its spent repair budget")
	}
	state := loadRun(t, store, pipelineRunID)
	if state.Integration != nil || state.PullRequest == nil {
		t.Fatalf("run = %#v, want a failed run that published and integrated nothing", state)
	}
	if state.PullRequest.Merged || state.PullRequest.State != "OPEN" || state.Outstanding() {
		t.Fatalf("publication = %#v, want it recorded open and unmerged with nothing owed", state.PullRequest)
	}
	return publicationFixture{repository: repository, worktreeRoot: worktreeRoot, store: store, tracker: tracker}, state
}

// refresh is the later sweep that re-asks the forge about what the run left
// recorded.
func (f publicationFixture) refresh(t *testing.T, forge ReconcilePullRequests) []PublicationRefresh {
	t.Helper()
	refreshed, err := Reconciler{
		Tracker:   f.tracker,
		Worktrees: newObserver(t, f.repository, f.worktreeRoot),
		Store:     f.store,
		Publisher: forge,
	}.RefreshPublications(context.Background())
	if err != nil {
		t.Fatalf("RefreshPublications() error = %v", err)
	}
	return refreshed
}

func loadRun(t *testing.T, store *runstate.Store, runID string) runstate.State {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}
