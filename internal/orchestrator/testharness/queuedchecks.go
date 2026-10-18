package testharness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// Sweep is the reconciler the reconcile verb builds: the forge's checks read,
// a free slot, and — where hosts is set — the run it makes
// live continued through the same pipeline a run is.
func (f QueuedFixture) Sweep(t *testing.T, forge *orchestratortest.CheckedForge, hosts bool) orchestrator.Reconciler {
	t.Helper()
	reconciler := f.Reconciler(t)
	reconciler.Publisher = forge
	reconciler.Checks = forge
	reconciler.Capacity = 1
	reconciler.HostsRuns = hosts
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, ApproveVerdict)
	pipeline := Publishing(Automatic(NewSharedPipeline(t, f.Repository, f.WorktreeRoot, f.Store, f.Tracker, provider, []string{"exit 0"}), provider), f.Forge)
	pipeline.Publisher = forge
	reconciler.Continue = pipeline.Continue
	return reconciler
}

// JobFailure is a head level with main whose adoption job the forge ended
// itself — here cancelled — with its only annotation the forge's own on
// .github. The shape is that of pull request 863's reading at 04:15Z on
// 2026-09-28, whose record kept no conclusion; the conclusion here is the
// fixture's, not that run's. checkRun is the check run the forge reports, and
// a re-run is given a new one.
func JobFailure(checkRun int64) publish.CheckReading {
	return publish.CheckReading{
		Files:   []string{"feature.txt"},
		Failing: []publish.FailedCheck{{Name: "adoption", Paths: []string{".github"}, ID: checkRun, Conclusion: "cancelled"}},
		Passing: 1,
	}
}
