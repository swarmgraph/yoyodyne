package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The answers Claude Code 2.1.286 gave to the check, and its help, as the
// adapter's own tests hold them. See
// internal/backend/claudecode/testdata/settings-check/README.md.
var recordedClaudeCode = filepath.Join("..", "backend", "claudecode", "testdata")

func recordedClaudeCodeFile(name string) string {
	data, err := os.ReadFile(filepath.Join(recordedClaudeCode, name))
	if err != nil {
		panic(err)
	}
	return string(data)
}

// answerLaunchSettingsCheck answers the check made before a developer is
// started, as a CLI that applied the developer's settings or as one that
// dropped them, and reports whether command was part of the check at all.
func answerLaunchSettingsCheck(command execution.Command, applied bool) (execution.ProcessResult, bool) {
	switch {
	case slices.Equal(command.Args, []string{"--help"}):
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: recordedClaudeCodeFile("cli-help/claude-2.1.286.txt")}, true
	case !slices.Contains(command.Args, "--input-format"):
		return execution.ProcessResult{}, false
	case !applied:
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: recordedClaudeCodeFile("settings-check/claude-2.1.286-dropped.jsonl")}, true
	}
	settings := ""
	if index := slices.Index(command.Args, "--settings"); index >= 0 && index+1 < len(command.Args) {
		settings = command.Args[index+1]
	}
	var passed map[string]any
	if err := json.Unmarshal([]byte(settings), &passed); err != nil {
		panic(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(recordedClaudeCodeFile("settings-check/claude-2.1.286-accepted.jsonl")), "\n") {
		var envelope map[string]any
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			panic(err)
		}
		response := envelope["response"].(map[string]any)
		if response["request_id"] == "settings" {
			response["response"].(map[string]any)["effective"] = passed
			encoded, _ := json.Marshal(envelope)
			line = string(encoded)
		}
		lines = append(lines, line)
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: strings.Join(lines, "\n") + "\n"}, true
}

// droppingCLI is an installed Claude Code that is logged in and drops the
// developer's settings payload. A developer's turn is never expected of it.
type droppingCLI struct {
	t        *testing.T
	mu       sync.Mutex
	checks   int
	launched int
}

func (d *droppingCLI) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case slices.Equal(command.Args, []string{"--version"}):
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "2.1.286 (Claude Code)\n"}, nil
	case len(command.Args) > 0 && command.Args[0] == "auth":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"loggedIn":true,"authMethod":"claude.ai"}` + "\n"}, nil
	}
	if answered, ok := answerLaunchSettingsCheck(command, false); ok {
		d.checks++
		return answered, nil
	}
	d.launched++
	d.t.Errorf("a developer was launched with %v on a CLI that dropped its settings", command.Args)
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}, nil
}

func newLaunchSettingsStore(t *testing.T) *runstate.LaunchSettingsHoldStore {
	t.Helper()
	store, err := runstate.NewLaunchSettingsHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewLaunchSettingsHoldStore() error = %v", err)
	}
	return store
}

// Two items are ready and the installed CLI drops the developer's settings
// payload. The first dispatch is refused before anything is claimed, naming the
// CLI's version and what did not take; one report is filed; and the provider is
// held, so the second item is never dispatched into the same refusal — by this
// pull or the next. Nothing is docketed or counted toward the brake.
func TestADroppedSettingsPayloadRefusesOneDispatchReportsOnceAndHoldsTheProvider(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	pipeline, store := newPipeline(t, repository, tracker, refusingBackend(0, nil, approveVerdict), []string{"exit 0"})
	cli := &droppingCLI{t: t}
	pipeline.Backend = claudecode.Backend{Runner: cli}
	holds := newLaunchSettingsStore(t)
	pipeline.LaunchSettings = holds
	reports := &fakeReports{}
	pipeline.Reports = reports
	pipeline.Build = "4d7e805"

	harness := newScheduleHarness(readyItems("yoyodyne-task", "yoyodyne-next")...)
	harness.launchSettings = holds
	harness.build = "4d7e805"
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if id != "yoyodyne-task" {
			h.retire(id)
			return h.complete(id), nil
		}
		return pipeline.Run(context.Background(), id)
	}
	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}

	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 1 || order[0] != "yoyodyne-task" {
		t.Fatalf("pulled %v, want only the first item dispatched", order)
	}
	if schedule.Stopped != ScheduleLaunchSettingsHeld {
		t.Fatalf("stopped = %q, want the drain stopped on the held provider: %s", schedule.Stopped, schedule.Render())
	}
	if cli.checks != 1 || cli.launched != 0 {
		t.Fatalf("checks = %d, developers launched = %d; want one refusal and no developer", cli.checks, cli.launched)
	}
	if len(schedule.Started) != 1 || !strings.Contains(schedule.Started[0].Failure, "2.1.286 (Claude Code)") ||
		!strings.Contains(schedule.Started[0].Failure, "notes guard") {
		t.Fatalf("started = %#v, want the refusal naming the CLI version and what did not take", schedule.Started)
	}

	hold, standing, err := holds.Standing()
	if err != nil || !standing {
		t.Fatalf("Standing() = %t, %v; want the provider held", standing, err)
	}
	if hold.Provider != domain.BackendClaudeCode || hold.Version != "2.1.286 (Claude Code)" || hold.Build != "4d7e805" || hold.Refusals != 1 {
		t.Fatalf("hold = %#v, want claude-code 2.1.286 held by this build after one refusal", hold)
	}
	said := strings.Join(hold.NotInForce, "; ")
	for _, want := range []string{"autoMemoryEnabled", "sandbox", "notes guard"} {
		if !strings.Contains(said, want) {
			t.Fatalf("hold names %q, want %s among what did not take", said, want)
		}
	}

	if len(reports.appended) != 1 {
		t.Fatalf("reports = %#v, want exactly one", reports.appended)
	}
	filed := reports.appended[0]
	if filed.Severity != report.SeverityCritical || filed.Role != report.HarnessReporter ||
		!strings.Contains(filed.Message, "2.1.286 (Claude Code)") || !strings.Contains(filed.Message, "notes guard") {
		t.Fatalf("report = %#v, want the harness's critical report naming the version and the guard", filed)
	}

	// Nothing was claimed, reserved, docketed, braked, or held for a person.
	if tracker.Claimed {
		t.Fatal("the item was claimed by a dispatch refused before any developer started")
	}
	if runs, err := store.Recorded(); err != nil || len(runs) != 0 {
		t.Fatalf("recorded runs = %#v, %v; want none", runs, err)
	}
	if schedule.Braked != nil {
		t.Fatalf("braked on %#v, want the refusal counted toward nothing", schedule.Braked)
	}
	if _, held, _ := harness.Held(); held {
		t.Fatal("intake is held, want nothing a person would have to release")
	}

	// The next pull starts nothing and files nothing more.
	again, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if again.Stopped != ScheduleLaunchSettingsHeld || len(again.Started) != 0 || len(harness.pullOrder()) != 1 {
		t.Fatalf("second pass stopped %q with %d started; want the provider still held", again.Stopped, len(again.Started))
	}
	if cli.checks != 1 || len(reports.appended) != 1 {
		t.Fatalf("checks = %d, reports = %d after the second pass, want one of each", cli.checks, len(reports.appended))
	}
	if !strings.Contains(again.Render(), "No developer is started on claude-code") {
		t.Fatalf("schedule does not say the hold:\n%s", again.Render())
	}
}

// acceptingCLI is an installed Claude Code that applies the developer's
// settings and then serves the developer's turn, recording the order it was
// asked in.
type acceptingCLI struct {
	stderrRefusingRunner
	order []string
}

func (a *acceptingCLI) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if slices.Contains(command.Args, "--input-format") {
		a.order = append(a.order, "check")
	} else if slices.Contains(command.Args, "--name") {
		a.order = append(a.order, "developer")
	}
	return a.stderrRefusingRunner.Run(ctx, command, observer)
}

// A run whose settings took effect starts as it did before the check: the
// check is made, before the developer is launched, and finds the settings in
// force; it files nothing; and it lifts a hold an earlier version left.
func TestARunWhoseSettingsTookEffectStartsAsBefore(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	reviewer := refusingBackend(0, nil, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, reviewer, []string{"exit 0"})
	pipeline = automatic(pipeline, reviewer)
	cli := &acceptingCLI{}
	pipeline.Backend = claudecode.Backend{Runner: cli}
	holds := newLaunchSettingsStore(t)
	if _, _, err := holds.Notice(runstate.LaunchSettingsObservation{
		Provider: domain.BackendClaudeCode, Version: "2.1.280 (Claude Code)", NotInForce: []string{"the sandbox is not running"}, Waiting: "the dispatch of yoyodyne-earlier",
	}); err != nil {
		t.Fatal(err)
	}
	pipeline.LaunchSettings = holds
	reports := &fakeReports{}
	pipeline.Reports = reports

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("outcome = %#v, want the run integrated and the item closed as before", outcome)
	}
	if len(cli.order) < 2 || cli.order[0] != "check" || cli.order[1] != "developer" {
		t.Fatalf("asked %v, want the check made before the developer was launched", cli.order)
	}
	if _, standing, err := holds.Standing(); err != nil || standing {
		t.Fatalf("Standing() = %t, %v; want the earlier hold lifted by a check that passed", standing, err)
	}
	if len(reports.appended) != 0 {
		t.Fatalf("reports = %#v, want none", reports.appended)
	}
}

// A provider whose adapter has nothing to establish is not asked.
func TestAProviderWithNothingToEstablishIsNotAsked(t *testing.T) {
	t.Parallel()
	var provider backend.Backend = orchestratortest.RoleBackend(nil, approveVerdict)
	if err := (Pipeline{}).requireLaunchSettings(context.Background(), "the dispatch of yoyodyne-task", provider, domain.BackendCodex); err != nil {
		t.Fatalf("requireLaunchSettings() = %v, want nothing to establish", err)
	}
}

// A hold lifts on a pull from another harness build, and lets one pull through
// to check again once the probe interval has passed since it was last
// confirmed. Until then the pull starts nothing.
func TestALaunchSettingsHoldLiftsOnANewBuildAndAfterTheProbeInterval(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		build     string
		confirmed time.Duration
		pulls     bool
		cleared   bool
	}{
		{name: "the same build inside the interval", build: "aaa1111", confirmed: time.Minute},
		{name: "another build", build: "bbb2222", confirmed: time.Minute, pulls: true, cleared: true},
		{name: "the same build after the interval", build: "aaa1111", confirmed: 31 * time.Minute, pulls: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			holds := newLaunchSettingsStore(t)
			harness := newScheduleHarness(readyItems("yoyodyne-two")...)
			if _, _, err := holds.Notice(runstate.LaunchSettingsObservation{
				Provider: domain.BackendClaudeCode, Version: "2.2.0 (Claude Code)", Build: "aaa1111",
				NotInForce: []string{"the flag --setting-sources is not in its help"}, At: harness.now.Add(-tc.confirmed),
			}); err != nil {
				t.Fatal(err)
			}
			harness.launchSettings = holds
			harness.build = tc.build
			harness.outageProbe = 30 * time.Minute

			schedule, err := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if pulled := len(harness.pullOrder()) == 1; pulled != tc.pulls {
				t.Fatalf("pulled %v, want pulled = %t: %s", harness.pullOrder(), tc.pulls, schedule.Render())
			}
			if !tc.pulls && schedule.Stopped != ScheduleLaunchSettingsHeld {
				t.Fatalf("stopped = %q, want the held provider", schedule.Stopped)
			}
			if _, standing, err := holds.Standing(); err != nil || standing == tc.cleared {
				t.Fatalf("Standing() = %t, %v; want cleared = %t", standing, err, tc.cleared)
			}
		})
	}
}

// A hold is on one provider. A developer slot that dispatches onto another
// provider is still filled while it stands; the slot on the held provider is
// passed over; and only when every slot is on the held provider does the pull
// stop whole.
func TestALaunchSettingsHoldPassesOverOnlyTheSlotsOnItsProvider(t *testing.T) {
	t.Parallel()

	codexSlot := domain.DeveloperSlot{Routing: &domain.EndpointPair{
		Enabled:   true,
		Primary:   &domain.EndpointSpec{Provider: domain.BackendCodex, Model: "gpt-6-astra"},
		Alternate: &domain.EndpointSpec{Provider: domain.BackendCodex, Model: "gpt-6.1-sol"},
	}}
	for _, tc := range []struct {
		name    string
		slots   []domain.DeveloperSlot
		started int
		stopped bool
	}{
		{name: "a second slot on another provider", slots: []domain.DeveloperSlot{{}, codexSlot}, started: 1},
		{name: "every slot on the held provider", slots: []domain.DeveloperSlot{{}, {}}, stopped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			holds := newLaunchSettingsStore(t)
			harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
			if _, _, err := holds.Notice(runstate.LaunchSettingsObservation{
				Provider: domain.BackendClaudeCode, Version: "2.2.0 (Claude Code)", Build: "aaa1111",
				NotInForce: []string{"the sandbox is not running"}, At: harness.now,
			}); err != nil {
				t.Fatal(err)
			}
			harness.launchSettings = holds
			harness.build = "aaa1111"
			harness.outageProbe = 30 * time.Minute
			harness.capacity = 2
			harness.slots = tc.slots
			harness.developerProvider = domain.BackendClaudeCode

			schedule, err := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if tc.stopped != (schedule.Stopped == ScheduleLaunchSettingsHeld) {
				t.Fatalf("stopped = %q, want stopped on the hold = %t: %s", schedule.Stopped, tc.stopped, schedule.Render())
			}
			if order := harness.pullOrder(); len(order) < tc.started || (tc.stopped && len(order) != 0) {
				t.Fatalf("pulled %v, want %d started", order, tc.started)
			}
			for _, started := range schedule.Started {
				if started.Slot == 1 {
					t.Fatalf("started %#v in the slot on the held provider", started)
				}
			}
			if _, standing, err := holds.Standing(); err != nil || !standing {
				t.Fatalf("Standing() = %t, %v; want the hold still standing", standing, err)
			}
		})
	}
}
