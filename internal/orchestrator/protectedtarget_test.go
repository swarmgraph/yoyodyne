package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive a run end to end against a scratch remote that refuses direct
// pushes to main, which is what a repository requiring a pull request is. The
// rule they hold is yoyodyne-ifd.429.5's: on a protected target a run never
// moves the primary checkout's main ahead of the forge. The change lands through
// its pull request, and main moves only by a fast-forward onto what the remote
// has — so no ending of the run can leave local main ahead of origin, which is
// what stranded commits there on 2026-09-20 and 2026-09-24.

// protectedRun is one publishing run against a protected main, with the local
// main read at the moment the forge is asked to merge.
type protectedRun struct {
	repository string
	remote     string
	tracker    *orchestratortest.Tracker
	forge      *orchestratortest.Forge
	pipeline   Pipeline
	store      *runstate.Store
	// atMerge is local main when the forge was asked to merge, or "" if it never
	// was.
	atMerge string
}

func newProtectedRun(t *testing.T, forge *orchestratortest.Forge) *protectedRun {
	t.Helper()
	repository, remote := publishedRepository(t)
	protectBranch(t, remote, "main")
	forge.Remote = remote
	run := &protectedRun{
		repository: repository,
		remote:     remote,
		tracker:    &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}},
		forge:      forge,
	}
	forge.OnMerge = func() { run.atMerge = publishedCommit(t, repository, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	run.pipeline, run.store = newPublishingPipeline(t, repository, run.tracker, provider, forge, []string{"exit 0"})
	return run
}

// assertMainNotAhead holds the invariant itself: whatever the run did, local
// main is at or behind what the remote carries.
func (r *protectedRun) assertMainNotAhead(t *testing.T) {
	t.Helper()
	local := publishedCommit(t, r.repository, "main")
	remote := publishedCommit(t, r.remote, "main")
	if local == remote {
		return
	}
	// The remote may hold commits this checkout has not fetched, so the question
	// is asked of the remote, which has both.
	if err := runGitQuiet(r.remote, "merge-base", "--is-ancestor", local, remote); err != nil {
		t.Errorf("local main %s is not contained in the remote's main %s: the run left local main ahead of origin", local, remote)
	}
}

func runGitQuiet(directory string, arguments ...string) error {
	_, err := (&orchestratortest.Forge{Remote: directory}).Git(arguments...)
	return err
}
