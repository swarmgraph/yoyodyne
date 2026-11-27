package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// exitedPID is the number of a process that has run and exited, which is what a
// run's recorded worker is once its process died.
func exitedPID(t *testing.T) int {
	t.Helper()
	command := exec.Command("true")
	if err := command.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	return command.Process.Pid
}

// A developer run whose process died, in a project with no watch running, read
// as running until somebody typed `yoyo reconcile`. `yoyo status` now finds it,
// settles it exactly as the sweep would — the run ended with its process
// recorded as gone, which charges no repair attempt, and its item told — says so
// with when it was found, and lists the run as what it now is.
func TestStatusSettlesARunWhoseProcessIsGoneWithNoWatch(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// settlement's tracker is swapped in for this test.
	configPath, store, state, tracker := processGoneFixture(t)
	// A run with no worker identity cannot be shown to have lost its process,
	// so it is left for the sweep.
	uncertain := recordedRun(t, store, runstate.StatusRunning, "yoyodyne-uncertain", state.StartedAt)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"Run " + state.RunID + " for yoyodyne-gone had no process behind it: attempt 1's worker, pid",
		"has exited. This was found at ",
		"its work item now waits on the development manager",
		"no repair attempt was charged",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q; stderr = %q", stdout, want, stderr)
		}
	}
	if strings.Contains(stdout, uncertain.RunID+" for yoyodyne-uncertain had no process") {
		t.Fatalf("a run whose worker cannot be shown gone was settled:\n%s", stdout)
	}

	settled, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Status.InFlight() {
		t.Fatalf("status = %s, want the run settled", settled.Status)
	}
	if settled.StopClass != runstate.CauseProcessVanished.StopClass() {
		t.Fatalf("stop class = %q, want the process-gone stop that charges no repair attempt", settled.StopClass)
	}
	if settled.RepairAttempts != state.RepairAttempts {
		t.Fatalf("repair attempts = %d, want %d: nothing is charged for a process that vanished", settled.RepairAttempts, state.RepairAttempts)
	}
	if !tracker.Blocked {
		t.Fatalf("the item was not told: calls %v", tracker.Calls)
	}
	left, err := store.Load(uncertain.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !left.Status.InFlight() {
		t.Fatalf("the uncertain run was settled: %s", left.Status)
	}

	// A second status finds nothing left to settle, and the JSON answer carries
	// the field only when something was found.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var output struct {
		ProcessGone []processGoneSettlement `json:"process_gone"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(output.ProcessGone) != 0 {
		t.Fatalf("process_gone = %#v, want nothing left to settle", output.ProcessGone)
	}
}

// processGoneFixture is a project with no watch running whose one run is a
// developer run whose process has exited, and the tracker its settlement is
// given. It sets the state root, so a test using it is not parallel.
func processGoneFixture(t *testing.T) (string, *runstate.Store, runstate.State, *orchestratortest.Tracker) {
	t.Helper()
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Now().Add(-20 * time.Minute).UTC()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	// The worker is recorded as the run is created, as the harness records it.
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     domain.ProductID("yoyodyne"),
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-gone",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     started,
		Worker:        &runstate.WorkerIdentity{PID: exitedPID(t), StartedAt: started, ProgramStartedAt: started, RunID: runID, Attempt: 1},
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-gone", Title: "Gone", Status: "in_progress"}}
	previous := statusReconciler
	statusReconciler = func(configPath string) (orchestrator.Reconciler, error) {
		reconciler, err := buildReconciler(configPath)
		reconciler.Tracker = tracker
		reconciler.Children = nil
		reconciler.Filer = nil
		return reconciler, err
	}
	t.Cleanup(func() { statusReconciler = previous })

	return configPath, store, state, tracker
}

// The commands besides `yoyo status` that read runs have the dead run settled
// before they read it, and say so on standard error so their own answer is
// unchanged. A command that acts on a run itself, such as `yoyo run`, is left to
// do what it was asked.
func TestACommandThatReadsRunsSettlesARunWhoseProcessIsGone(t *testing.T) {
	configPath, store, state, tracker := processGoneFixture(t)

	// `yoyo run --help` acts on no run and reads none; it settles nothing.
	_, stderr, _ := runCLI(t, "run", "--help", "--config", configPath)
	if strings.Contains(stderr, "had no process behind it") {
		t.Fatalf("yoyo run settled a run before acting on one: %q", stderr)
	}
	if left, err := store.Load(state.RunID); err != nil || !left.Status.InFlight() {
		t.Fatalf("Load() = %s, %v; want the run untouched by a command that settles nothing", left.Status, err)
	}

	_, stderr, _ = runCLI(t, "invocations", "--config", configPath)
	for _, want := range []string{
		"Run " + state.RunID + " for yoyodyne-gone had no process behind it",
		"This was found at ",
		"no repair attempt was charged",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
	settled, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Status.InFlight() || settled.StopClass != runstate.CauseProcessVanished.StopClass() {
		t.Fatalf("status = %s, stop class = %q; want the run settled with its process recorded as gone", settled.Status, settled.StopClass)
	}
	if !tracker.Blocked {
		t.Fatalf("the item was not told: calls %v", tracker.Calls)
	}
}

// Every command named as settling first is one the dispatcher carries out, so a
// name that drifted from its command is caught rather than silently settling
// nothing.
func TestEveryCommandSettlingFirstIsACommand(t *testing.T) {
	t.Parallel()
	for name := range commandsSettlingFirst {
		_, stderr, _ := runCLI(t, name, "--help")
		if strings.Contains(stderr, "unknown command") {
			t.Errorf("%q settles first but is not a command: %q", name, stderr)
		}
	}
}

func TestConfigFlagOfFindsTheConfigurationWhereverItIsGiven(t *testing.T) {
	t.Parallel()
	for args, want := range map[string]string{
		"list --config a.yaml":  "a.yaml",
		"--config=b.yaml":       "b.yaml",
		"-config c.yaml --json": "c.yaml",
		"decide run-1 --json":   "",
	} {
		if got := configFlagOf(strings.Fields(args)); got != want {
			t.Errorf("configFlagOf(%q) = %q, want %q", args, got, want)
		}
	}
}

// committerHoldsTheLock is the settlement's tracker while the process making a
// commit holds the tracker's write lock: every write waits on that lock, which
// is not let go until the commit, and so the hook inside it, is over. It
// waits until the write's own bound runs out, as a write behind a held lock
// does, and records that it was asked.
type committerHoldsTheLock struct {
	*orchestratortest.Tracker
	asked chan string
	wait  time.Duration
}

func (t committerHoldsTheLock) waitOnTheLock(ctx context.Context, write string) error {
	t.asked <- write
	select {
	case <-ctx.Done():
	case <-time.After(t.wait):
	}
	return errors.New("another write held the lock for " + t.wait.String())
}

func (t committerHoldsTheLock) Block(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	return beads.WorkItem{}, t.waitOnTheLock(ctx, "block "+id)
}

func (t committerHoldsTheLock) RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error) {
	return beads.WorkItem{}, t.waitOnTheLock(ctx, "record an outcome on "+id)
}

func (t committerHoldsTheLock) Release(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	return beads.WorkItem{}, t.waitOnTheLock(ctx, "release "+id)
}

// checkoutWithAFakeTracker is a checkout holding a tracker store, with a bd on
// PATH that answers every call with one work item and writes each call it was
// asked into the file returned, so a test can read what bd was asked.
func checkoutWithAFakeTracker(t *testing.T) (string, string) {
	t.Helper()
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".beads", "metadata.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\nprintf '{\"id\":\"fresh\"}\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return checkout, calls
}

// The export the repository's pre-commit hook runs is inside a commit, so it
// settles nothing and writes nothing to the tracker, even with a run whose
// process is gone waiting to be settled and the committer holding the
// tracker's write lock: the export is taken at once and the commit goes ahead.
// The next command that reads runs outside a commit settles the run instead.
func TestTheTrackerExportACommitRunsWritesNothingAndIsNotHeldByTheCommittersLock(t *testing.T) {
	// Not parallel: the fixture sets the state root and PATH.
	configPath, store, state, fake := processGoneFixture(t)
	checkout, calls := checkoutWithAFakeTracker(t)
	held := committerHoldsTheLock{Tracker: fake, asked: make(chan string, 16), wait: 5 * time.Second}
	statusReconciler = func(configPath string) (orchestrator.Reconciler, error) {
		reconciler, err := buildReconciler(configPath)
		reconciler.Tracker = held
		reconciler.Children = nil
		reconciler.Filer = nil
		return reconciler, err
	}

	// The hook runs the export from the checkout being committed, with no
	// --config, so the nearest project configuration is the one it would find.
	t.Chdir(filepath.Dir(configPath))
	began := time.Now()
	stdout, stderr, code := runCLI(t, "tracker", "export", "--dir", checkout)
	took := time.Since(began)
	if code != 0 {
		t.Fatalf("tracker export = %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if took >= held.wait {
		t.Fatalf("tracker export took %s: it waited on the lock the committer holds", took)
	}
	select {
	case write := <-held.asked:
		t.Fatalf("the export run inside a commit asked the tracker to %s", write)
	default:
	}
	if len(fake.Calls) != 0 || fake.Blocked {
		t.Fatalf("the export run inside a commit wrote the tracker: %v", fake.Calls)
	}
	if strings.Contains(stderr, "had no process behind it") {
		t.Fatalf("the export run inside a commit settled a run: %q", stderr)
	}
	exported, err := os.ReadFile(filepath.Join(checkout, beads.ExportPath))
	if err != nil || string(exported) != "{\"id\":\"fresh\"}\n" {
		t.Fatalf("export = %q, %v; want the one bd answered", exported, err)
	}
	asked, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range strings.Split(strings.TrimSpace(string(asked)), "\n") {
		if !strings.HasPrefix(call, "export") {
			t.Fatalf("bd was asked %q during the export; want only the export, which reads", call)
		}
	}
	if left, err := store.Load(state.RunID); err != nil || !left.Status.InFlight() {
		t.Fatalf("Load() = %s, %v; want the run left for a command outside a commit", left.Status, err)
	}

	// Another tracker subcommand that reads runs still settles first.
	statusReconciler = func(configPath string) (orchestrator.Reconciler, error) {
		reconciler, err := buildReconciler(configPath)
		reconciler.Tracker = fake
		reconciler.Children = nil
		reconciler.Filer = nil
		return reconciler, err
	}
	_, stderr, _ = runCLI(t, "tracker", "slow-calls", "--config", configPath)
	if !strings.Contains(stderr, "Run "+state.RunID+" for yoyodyne-gone had no process behind it") {
		t.Fatalf("stderr = %q, want the run settled by a command outside a commit", stderr)
	}
	if !fake.Blocked {
		t.Fatalf("the item was not told: calls %v", fake.Calls)
	}
}
