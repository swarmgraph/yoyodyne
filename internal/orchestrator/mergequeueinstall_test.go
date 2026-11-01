package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// installCount is how many times the fixture's install step has run, read from
// what it writes into the project's package cache folder.
func installCount(t *testing.T, stateRoot, product string) int {
	t.Helper()
	root, err := filepath.EvalSymlinks(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, filepath.FromSlash(home.InstallCacheDirectoryWithin(root, product)))
	content, err := os.ReadFile(filepath.Join(cache, "runs"))
	if err != nil {
		return 0
	}
	return strings.Count(string(content), "\n")
}

// The merge queue installs the project's dependencies in a candidate's
// checkout before checking it, keeps that successful run on the generation
// without its output, and runs the step there again only where an output has
// gone missing or the inputs changed.
func TestTheMergeQueueInstallsInTheCandidatesCheckoutBeforeCheckingIt(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	writeQueueFile(t, f.repository, ".gitignore", "installed/\n")
	writeQueueFile(t, f.repository, "deps.lock", "greeting 1.0.0\n")
	runPipelineGit(t, f.repository, "add", ".gitignore", "deps.lock")
	runPipelineGit(t, f.repository, "commit", "-m", "the project's dependencies")
	pipeline := f.worker.Pipeline
	pipeline.StateRoot = t.TempDir()
	pipeline.Config.Checks = append(append([]string(nil), queueChecks...), "cmp -s deps.lock installed/lock")
	pipeline.Config.Install = []config.InstallStep{{
		Name:    "dependencies",
		Run:     `mkdir -p installed && cp deps.lock installed/lock && echo installed >> "$YOYODYNE_INSTALL_CACHE/runs" && echo 'installed 1 package'`,
		Inputs:  []string{"deps.lock"},
		Outputs: []string{"installed"},
	}}
	product := string(pipeline.Config.Product.ID)

	verification, err := f.work()
	if err != nil {
		t.Fatalf("Work() error = %v", err)
	}
	if !verification.Verified {
		t.Fatalf("Work() = %#v, want the candidate verified with its dependencies installed", verification)
	}
	generation := verification.Generation
	if len(generation.Install) != 1 {
		t.Fatalf("install records = %#v, want the one step", generation.Install)
	}
	record := generation.Install[0]
	if record.Name != "dependencies" || record.Directory != generation.Checkout || record.Result != runstate.InstallPassed || record.Output != "" {
		t.Fatalf("install record = %#v, want a pass in the candidate's checkout %s kept without its output", record, generation.Checkout)
	}
	if got := installCount(t, pipeline.StateRoot, product); got != 1 {
		t.Fatalf("the step ran %d times, want once", got)
	}

	// Asked again in the same checkout with the inputs unchanged and the output
	// there, nothing runs.
	ctx := context.Background()
	lease, leased, err := f.queue.LeaseWorker(ctx, queueKey)
	if err != nil || !leased {
		t.Fatalf("LeaseWorker() = %v, %v", leased, err)
	}
	defer lease.Release()
	if problem, err := f.worker.installWhereDue(ctx, lease, queueKey, &generation, generation.Checkout, "before the test", nil); err != nil || problem != "" {
		t.Fatalf("installWhereDue() = %q, %v", problem, err)
	}
	if got := installCount(t, pipeline.StateRoot, product); got != 1 {
		t.Fatalf("the step ran %d times with nothing changed, want still once", got)
	}

	// With the output gone it runs again, and the record of it replaces the
	// earlier one rather than growing beside it.
	if err := os.RemoveAll(filepath.Join(generation.Checkout, "installed")); err != nil {
		t.Fatal(err)
	}
	if problem, err := f.worker.installWhereDue(ctx, lease, queueKey, &generation, generation.Checkout, "before the test", nil); err != nil || problem != "" {
		t.Fatalf("installWhereDue() = %q, %v", problem, err)
	}
	if got := installCount(t, pipeline.StateRoot, product); got != 2 {
		t.Fatalf("the step ran %d times after its output went missing, want twice", got)
	}
	stored := f.generations()
	if len(stored[len(stored)-1].Install) != 1 {
		t.Fatalf("stored install records = %#v, want the one step's last run", stored[len(stored)-1].Install)
	}
}

// A step that does not pass in the candidate's checkout is the checks' problem:
// they never ran, earned nothing, and the candidate is not verified.
func TestAFailedInstallInTheMergeQueueEarnsTheChecksNothing(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	writeQueueFile(t, f.repository, ".gitignore", "installed/\n")
	writeQueueFile(t, f.repository, "deps.lock", "greeting 1.0.0\n")
	runPipelineGit(t, f.repository, "add", ".gitignore", "deps.lock")
	runPipelineGit(t, f.repository, "commit", "-m", "the project's dependencies")
	pipeline := f.worker.Pipeline
	pipeline.StateRoot = t.TempDir()
	pipeline.Config.Install = []config.InstallStep{{
		Name:    "dependencies",
		Run:     "echo 'the registry could not be reached' >&2; exit 1",
		Inputs:  []string{"deps.lock"},
		Outputs: []string{"installed"},
	}}

	verification, err := f.work()
	if err != nil {
		t.Fatalf("Work() error = %v", err)
	}
	if verification.Verified {
		t.Fatalf("Work() = %#v, want the candidate unverified", verification)
	}
	run := verification.Generation.CheckRun
	if run == nil || run.FinishedAt == nil || len(run.Results) != 0 || !strings.Contains(run.Problem, "dependencies") || !strings.Contains(run.Problem, "registry could not be reached") {
		t.Fatalf("check run = %#v, want no results and the failed step as its problem", run)
	}
	if len(verification.Generation.Install) != 0 {
		t.Fatalf("install records = %#v, want no failure kept", verification.Generation.Install)
	}
}
