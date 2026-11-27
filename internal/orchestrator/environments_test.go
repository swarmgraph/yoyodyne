package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// environmentChecksSeen is the pipeline's check runner with every request it was
// given kept. A request wrapped in the developer's restrictions is recorded
// with what the wrap made of a command, then run unwrapped, because a test
// process inside a sandbox cannot start another.
type environmentChecksSeen struct {
	runner   CheckRunner
	requests []checks.Request
	wrapped  []execution.Command
}

func (r *environmentChecksSeen) Run(ctx context.Context, request checks.Request, sink func(execution.Event) error) ([]checks.Result, uint64, error) {
	r.requests = append(r.requests, request)
	if request.Wrap != nil {
		wrapped, err := request.Wrap(execution.Command{Name: "/bin/sh", Args: []string{"-c", strings.Join(request.Commands, " ")}})
		if err != nil {
			return nil, request.LastSequence, err
		}
		r.wrapped = append(r.wrapped, wrapped)
		request.Wrap = nil
	}
	return r.runner.Run(ctx, request, sink)
}

// environmentItem is the fixture's item with an Environments: line.
func environmentItem(id, line string) *orchestratortest.Tracker {
	tracker := npmProjectItem(id)
	tracker.Item.Description = "Follow docs/design.md\n" + line + "\n"
	return tracker
}

// environmentPipeline is the npm fixture with environments declared and its
// check runner recorded.
func environmentPipeline(t *testing.T, tracker *orchestratortest.Tracker, provider *orchestratortest.Backend, environments []config.Environment) (Pipeline, *runstate.Store, *environmentChecksSeen) {
	t.Helper()
	repository := npmProjectRepository(t, nil)
	pipeline, store := npmProjectPipeline(t, repository, tracker, provider, "sh scripts/npm-ci.sh")
	pipeline.Config.Environments = environments
	recorded := &environmentChecksSeen{runner: pipeline.Checks}
	pipeline.Checks = recorded
	return pipeline, store, recorded
}

// With no environment usable, the run ends before any developer turn with no
// attempt spent, each environment's result is on the run apart from the
// others, and the item is held for the development manager naming each one,
// the criteria it blocks and what its check printed. For a machine
// environment the hold quotes how_to_install and says a person has to run it,
// and the harness never runs that text.
func TestARunWithNoNamedEnvironmentUsableEndsBeforeAnyDeveloperTurn(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "how-to-install-ran")
	tracker := environmentItem("environments-1", "Environments: android client (criteria 1, 2); web ui (criterion 3)")
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store, recorded := environmentPipeline(t, tracker, provider, []config.Environment{
		{Name: "android client", Where: config.EnvironmentInHarness, Check: "echo 'no android emulator found' >&2; exit 3", Test: "true"},
		{Name: "web ui", Machine: true, HowToInstall: "touch " + marker, Where: config.EnvironmentInHarness, Check: "echo 'chromium is not installed'; exit 1", Test: "true", TimeLimit: config.Duration(time.Minute)},
	})

	outcome, _ := pipeline.Run(context.Background(), tracker.Item.ID)
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("developer invocations = %d, want none", got)
	}
	if outcome.Status == runstate.StatusSucceeded || outcome.WorkItemClosed {
		t.Fatalf("Run() outcome = %#v, want the run stopped", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.StopClass != runstate.CauseEnvironmentsMissing.StopClass() || state.Environmental == nil || state.Environmental.Cause != runstate.CauseEnvironmentsMissing {
		t.Fatalf("stop class %q, environmental %#v; want environments-missing", state.StopClass, state.Environmental)
	}
	if state.RepairAttempts != 0 || state.ProviderSessionID != "" {
		t.Fatalf("repair attempts = %d, session %q; want no attempt recorded", state.RepairAttempts, state.ProviderSessionID)
	}
	if len(state.Environments) != 2 {
		t.Fatalf("environments = %#v, want one result for each", state.Environments)
	}
	android, web := state.Environments[0], state.Environments[1]
	if android.Name != "android client" || android.Result != runstate.EnvironmentFailed || android.ExitCode != 3 || android.Where != "harness" || !slices.Equal(android.Criteria, []int{1, 2}) || !strings.Contains(android.Output, "no android emulator found") {
		t.Fatalf("android result = %#v", android)
	}
	if web.Name != "web ui" || !web.Machine || !slices.Equal(web.Criteria, []int{3}) || !strings.Contains(web.Output, "chromium is not installed") {
		t.Fatalf("web result = %#v", web)
	}
	for _, want := range []string{"android client", "criteria 1, 2", "no android emulator found", "web ui", "criterion 3", "chromium is not installed", "touch " + marker, "A person has to run this on this machine", "no developer or repair attempt was spent"} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Errorf("blocker = %q, want it to say %q", tracker.BlockReason, want)
		}
	}
	if !tracker.Blocked {
		t.Fatal("the item was not held")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("how_to_install ran (marker %s: %v); the harness must never run it", marker, err)
	}
	for _, request := range recorded.requests {
		for _, command := range request.Commands {
			if strings.Contains(command, marker) {
				t.Fatalf("the check runner was given how_to_install as a command: %q", command)
			}
		}
	}
	if says := state.EnvironmentsMissingSays(); !strings.Contains(says, "android client") || !strings.Contains(says, "criteria 1, 2") {
		t.Fatalf("held says %q, want each missing environment and what it blocks", says)
	}
}

// With one environment usable and another not, the run goes on, and the one
// that was not is recorded as still to be checked with its criteria, on the
// run and in the item's notes.
func TestARunWithSomeNamedEnvironmentsUsableGoesOnAndRecordsTheRest(t *testing.T) {
	t.Parallel()

	tracker := environmentItem("environments-2", "Environments: android client (criteria 1, 2); ios client (criterion 3)")
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "farewell.ts"), []byte(nonGoSource("  ")), 0o600)
	}, approveVerdict)
	pipeline, store, _ := environmentPipeline(t, tracker, provider, []config.Environment{
		{Name: "android client", Where: config.EnvironmentInHarness, Check: "echo 'emulator booted'", Test: "true"},
		{Name: "ios client", Where: config.EnvironmentInHarness, Check: "echo 'no simulator runtime'; exit 1", Test: "true"},
	})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got == 0 {
		t.Fatal("the developer was never invoked, want the run to go on")
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Environments) != 2 || !state.Environments[0].Passed() || state.Environments[1].Passed() {
		t.Fatalf("environments = %#v, want android passed and ios not", state.Environments)
	}
	pending := state.EnvironmentsStillToCheck()
	if len(pending) != 1 || pending[0].Name != "ios client" || !slices.Equal(pending[0].Criteria, []int{3}) {
		t.Fatalf("still to check = %#v, want ios client for criterion 3", pending)
	}
	if says := state.EnvironmentsStillToCheckSays(); !strings.Contains(says, `"ios client"`) || !strings.Contains(says, "criterion 3") || strings.Contains(says, "android") {
		t.Fatalf("still to check says %q", says)
	}
	if !strings.Contains(tracker.Notes, "ios client") || !strings.Contains(tracker.Notes, "still to be checked") || !strings.Contains(tracker.Notes, "no simulator runtime") {
		t.Fatalf("notes = %q, want the environment still to be checked named", tracker.Notes)
	}
}

// A developer environment's check runs under the developer session's own
// restrictions: no network but localhost, no socket file, writes only where a
// developer may write, and never what an install step is given.
func TestADeveloperEnvironmentsCheckRunsUnderTheDevelopersRestrictions(t *testing.T) {
	t.Parallel()

	tracker := environmentItem("environments-3", "Environments: web ui (criterion 1)")
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "farewell.ts"), []byte(nonGoSource("  ")), 0o600)
	}, approveVerdict)
	pipeline, store, recorded := environmentPipeline(t, tracker, provider, []config.Environment{
		{Name: "web ui", Check: "echo ready", Test: "true", TimeLimit: config.Duration(2 * time.Minute)},
	})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var environmentRequest *checks.Request
	for index := range recorded.requests {
		if slices.Equal(recorded.requests[index].Commands, []string{"echo ready"}) {
			environmentRequest = &recorded.requests[index]
		}
	}
	if environmentRequest == nil {
		t.Fatalf("no check request ran the environment's check; requests = %#v", recorded.requests)
	}
	if environmentRequest.Timeout != 2*time.Minute || !environmentRequest.Unbounded {
		t.Fatalf("timeout %s unbounded %v, want the environment's own time limit", environmentRequest.Timeout, environmentRequest.Unbounded)
	}
	for _, variable := range environmentRequest.Env {
		if strings.HasPrefix(variable, checks.InstallCacheVariable+"=") {
			t.Fatalf("the developer check was given the install step's cache: %q", variable)
		}
	}
	if len(recorded.wrapped) != 1 {
		t.Fatalf("wrapped commands = %#v, want the environment's check wrapped once", recorded.wrapped)
	}
	wrapped := recorded.wrapped[0]
	if wrapped.Name != "/usr/bin/sandbox-exec" || len(wrapped.Args) < 2 || !strings.Contains(wrapped.Args[1], "(deny network*)") || !strings.Contains(wrapped.Args[1], "(deny file-write*)") {
		t.Fatalf("wrapped = %#v, want the developer's sandbox profile", wrapped)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Environments) != 1 || !state.Environments[0].Passed() || state.Environments[0].Where != "developer" {
		t.Fatalf("environments = %#v", state.Environments)
	}
}

// A resumed or continued run is a new process, so it runs the checks again
// before its next developer turn, and an environment that now passes stops
// being recorded as still to be checked. A repair round in the same process
// does not run them again.
func TestAResumedRunChecksItsEnvironmentsAgainAndForgetsOneThatNowPasses(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	id, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: id, ProductID: "yoyodyne", RepositoryID: "yoyodyne",
		WorkItemID: "environments-4", Backend: "claude-code", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping,
		StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(directory, "simulator-ready")
	pipeline := Pipeline{Store: store, Checks: checks.Runner{Process: execution.OSProcessRunner{}}}
	pipeline.Config.Environments = []config.Environment{
		{Name: "android client", Where: config.EnvironmentInHarness, Check: "true", Test: "true"},
		{Name: "ios client", Where: config.EnvironmentInHarness, Check: "test -f " + ready, Test: "true"},
	}
	tracker := environmentItem("environments-4", "Environments: android client (criterion 1); ios client (criterion 2)")
	pipeline.Tracker = tracker
	newProcess := func() *activeRun {
		state, err := store.Load(id)
		if err != nil {
			t.Fatal(err)
		}
		return &activeRun{pipeline: pipeline, state: state, item: tracker.Item, claimed: true, worktree: gitworktree.Worktree{Path: directory}}
	}

	first := newProcess()
	if err := first.checkEnvironmentsBeforeTurn(context.Background()); err != nil {
		t.Fatalf("first check error = %v", err)
	}
	if pending := first.state.EnvironmentsStillToCheck(); len(pending) != 1 || pending[0].Name != "ios client" {
		t.Fatalf("still to check after the first round = %#v", pending)
	}
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A repair round in the same process does not check again.
	if err := first.checkEnvironmentsBeforeTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending := first.state.EnvironmentsStillToCheck(); len(pending) != 1 {
		t.Fatalf("still to check after a second turn in the same process = %#v, want it unchanged", pending)
	}

	resumed := newProcess()
	if err := resumed.checkEnvironmentsBeforeTurn(context.Background()); err != nil {
		t.Fatalf("resumed check error = %v", err)
	}
	stored, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Environments) != 2 || len(stored.EnvironmentsStillToCheck()) != 0 || stored.EnvironmentsStillToCheckSays() != "" {
		t.Fatalf("environments after the resumed round = %#v, want both passed and none still to check", stored.Environments)
	}
}

// With no environments declared, or no Environments: line on the item, no
// check runs and nothing is recorded.
func TestAnItemNamingNoEnvironmentRunsNoEnvironmentCheck(t *testing.T) {
	t.Parallel()

	run := &activeRun{item: npmProjectItem("environments-5").Item}
	run.pipeline.Config.Environments = []config.Environment{{Name: "web ui", Check: "exit 1", Test: "true"}}
	if err := run.checkEnvironmentsBeforeTurn(context.Background()); err != nil || run.environmentsChecked || len(run.state.Environments) != 0 {
		t.Fatalf("error %v, checked %v, recorded %#v; want nothing run for an item with no line", err, run.environmentsChecked, run.state.Environments)
	}
	run = &activeRun{item: environmentItem("environments-6", "Environments: web ui (criterion 1)").Item}
	if err := run.checkEnvironmentsBeforeTurn(context.Background()); err != nil || run.environmentsChecked {
		t.Fatalf("error %v, checked %v; want nothing run for a project declaring none", err, run.environmentsChecked)
	}
}
