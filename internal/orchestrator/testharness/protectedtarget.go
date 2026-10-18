package testharness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ProtectedRun is one publishing run against a protected main, with the local
// main read at the moment the forge is asked to merge.
type ProtectedRun struct {
	Repository string
	Remote     string
	Tracker    *orchestratortest.Tracker
	Forge      *orchestratortest.Forge
	Pipeline   orchestrator.Pipeline
	Store      *runstate.Store
	// AtMerge is local main when the forge was asked to merge, or "" if it never
	// was.
	AtMerge string
}

func NewProtectedRun(t *testing.T, forge *orchestratortest.Forge) *ProtectedRun {
	t.Helper()
	repository, remote := PublishedRepository(t)
	ProtectBranch(t, remote, "main")
	forge.Remote = remote
	run := &ProtectedRun{
		Repository: repository,
		Remote:     remote,
		Tracker:    &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}},
		Forge:      forge,
	}
	forge.OnMerge = func() { run.AtMerge = PublishedCommit(t, repository, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, ApproveVerdict)
	run.Pipeline, run.Store = NewPublishingPipeline(t, repository, run.Tracker, provider, forge, []string{"exit 0"})
	return run
}

// AssertMainNotAhead holds the invariant itself: whatever the run did, local
// main is at or behind what the remote carries.
func (r *ProtectedRun) AssertMainNotAhead(t *testing.T) {
	t.Helper()
	local := PublishedCommit(t, r.Repository, "main")
	remote := PublishedCommit(t, r.Remote, "main")
	if local == remote {
		return
	}
	// The remote may hold commits this checkout has not fetched, so the question
	// is asked of the remote, which has both.
	if err := RunGitQuiet(r.Remote, "merge-base", "--is-ancestor", local, remote); err != nil {
		t.Errorf("local main %s is not contained in the remote's main %s: the run left local main ahead of origin", local, remote)
	}
}

func RunGitQuiet(directory string, arguments ...string) error {
	_, err := (&orchestratortest.Forge{Remote: directory}).Git(arguments...)
	return err
}
