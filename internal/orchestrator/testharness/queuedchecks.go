package testharness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// sweep is the reconciler the reconcile verb builds: the forge's checks read,
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
