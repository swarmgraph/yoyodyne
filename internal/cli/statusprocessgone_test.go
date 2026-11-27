package cli

import (
	"encoding/json"
	"os/exec"
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
