package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/launchd"
	"github.com/mason-bryant/yoyodyne/internal/maintain"
	"github.com/mason-bryant/yoyodyne/internal/maintenancejob"
)

// runYoyo runs one command the way the executable does, so what is recorded
// is what the command line records.
func runYoyo(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := RunContext(context.Background(), args, &stdout, &stderr, "test")
	return code, stdout.String(), stderr.String()
}

func listedInterventions(t *testing.T, configPath string) interventionOutput {
	t.Helper()
	code, stdout, stderr := runYoyo(t, "intervention", "list", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("yoyo intervention list exited %d: %s", code, stderr)
	}
	var listed interventionOutput
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("decode the listing: %v\n%s", err, stdout)
	}
	return listed
}

// A person at the command line takes two hand steps through the harness, and
// writes down a third they took outside it. Each is one event, and a later
// command — a process that shares nothing with the ones that wrote them but the
// state root — reads all three back, with the count beside them saying it is a
// floor.
func TestHandStepsAtTheCommandLineAreRecordedAndReadBack(t *testing.T) {
	// Not parallel: the state root and the process's own markers are set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(execution.StartedByVariable, "")
	t.Setenv(execution.LaunchdJobVariable, "")
	configPath := writeConfig(t, validConfig)

	if listed := listedInterventions(t, configPath); len(listed.Interventions) != 0 {
		t.Fatalf("a product nobody has touched lists %v", listed.Interventions)
	}

	directiveID := recordFromCommandLine(t, configPath, "--scope", "calc-1", "stop opening pull requests for documentation-only changes")
	if code, _, stderr := runYoyo(t, "triage", "override", "--clear", "--by", "Mason", "--reason", "the item is worth one more round", "--config", configPath, "calc-1"); code != 0 {
		t.Fatalf("yoyo triage override exited %d: %s", code, stderr)
	}
	code, stdout, stderr := runYoyo(t, "intervention", "record",
		"--kind", "restart", "--by", "Mason", "--subject", "the scheduler",
		"--at", "2026-10-05T09:30:00-07:00",
		"--config", configPath,
		"restarted the scheduler by hand after it stopped pulling work")
	if code != 0 {
		t.Fatalf("yoyo intervention record exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Mason restarted a part of the product by hand") {
		t.Errorf("recording said %q, want it to say what was recorded", stdout)
	}

	listed := listedInterventions(t, configPath)
	if len(listed.Interventions) != 3 {
		t.Fatalf("listed %d steps, want 3: %+v", len(listed.Interventions), listed.Interventions)
	}
	byKind := map[intervention.Kind]intervention.Event{}
	for _, event := range listed.Interventions {
		byKind[event.Kind] = event
	}
	recorded, ok := byKind[intervention.KindDirective]
	if !ok || recorded.Observed || recorded.Via != viaCommandLine || recorded.Subject != directiveID || !recorded.Names("calc-1", "") {
		t.Errorf("the directive was recorded as %+v, want a step carried out at the command line naming %s and calc-1", recorded, directiveID)
	}
	override, ok := byKind[intervention.KindOverride]
	if !ok || override.Observed || !override.Names("calc-1", "") {
		t.Errorf("the override was recorded as %+v, want a step carried out on calc-1", override)
	}
	restart, ok := byKind[intervention.KindRestart]
	if !ok || !restart.Observed || restart.By != "Mason" || restart.RecordedBy != "Mason" || restart.Via != "" {
		t.Errorf("the restart was recorded as %+v, want it observed, taken and recorded by Mason", restart)
	}
	if want := time.Date(2026, 10, 5, 16, 30, 0, 0, time.UTC); !restart.At.Equal(want) {
		t.Errorf("the restart was taken at %s, want %s", restart.At, want)
	}
	// It is the earliest step, so it is listed first.
	if listed.Interventions[0].Kind != intervention.KindRestart {
		t.Errorf("listed %s first, want the steps in the order they were taken", listed.Interventions[0].Kind)
	}
	if listed.Count == nil || !strings.Contains(listed.Count.Floor, "a floor") || listed.Count.Unattributed != 3 {
		t.Errorf("count = %+v, want three steps naming nothing merged, said to be a floor", listed.Count)
	}

	code, text, _ := runYoyo(t, "intervention", "list", "--config", configPath)
	if code != 0 || !strings.Contains(text, "no change has been merged yet") || !strings.Contains(text, "a floor") {
		t.Errorf("the listing said %q, want it to say nothing has merged and the count is a floor", text)
	}
}

// What the system does on its own is not the operator's hand: a verb an
// agent's process runs, and one the maintenance pass starts, record nothing,
// and an agent cannot write down a person's step.
func TestNothingIsRecordedForAnAgentOrTheHarnessItself(t *testing.T) {
	// Not parallel: the state root and the process's own markers are set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(execution.StartedByVariable, maintain.StartedByMaintenance)
	configPath := writeConfig(t, validConfig)

	recordFromCommandLine(t, configPath, "a directive the maintenance pass never records")

	t.Setenv(execution.StartedByVariable, "")
	t.Setenv(execution.AgentRoleVariable, string(domain.RoleDeveloper))
	recordFromCommandLine(t, configPath, "a directive an agent's process records")
	code, _, stderr := runYoyo(t, "intervention", "record", "--kind", "restart", "--by", "Mason", "--config", configPath, "restarted the scheduler")
	if code == 0 || !strings.Contains(stderr, "refused from a process the harness launched for the developer") {
		t.Errorf("an agent recorded a person's step: exit %d, %s", code, stderr)
	}

	t.Setenv(execution.AgentRoleVariable, "")
	if listed := listedInterventions(t, configPath); len(listed.Interventions) != 0 {
		t.Fatalf("listed %+v, want nothing recorded for the system's own work", listed.Interventions)
	}
}

// A `yoyo reconcile` or `yoyo run` a schedule starts is the system's own work
// whichever schedule it is: the supervisor's maintenance pass, which marks what
// it starts, or a launchd job of the product's own, which launchd marks with
// the job's label — the operator's retired maintenance job, which ran
// reconcile from a script, among them. The same two verbs typed at a terminal
// are the operator's hand steps, and a terminal's own launchd name does not
// make them anything else.
func TestAScheduledReconcileOrRunIsNotCountedAndOneTypedAtATerminalIs(t *testing.T) {
	// Not parallel: the state root and the process's own markers are set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(execution.StartedByVariable, "")
	t.Setenv(execution.LaunchdJobVariable, "")

	project := t.TempDir()
	git(t, project, "init", "-b", "main")
	git(t, project, "config", "user.name", "Yoyodyne Test")
	git(t, project, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, project, "first")
	directory := filepath.Join(project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(directory, config.FileName)
	if err := os.WriteFile(configPath, []byte(validConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reconcile := func(by string) {
		t.Helper()
		if code, _, stderr := runYoyo(t, "reconcile", "--config", configPath); code != 0 {
			t.Fatalf("yoyo reconcile %s exited %d: %s", by, code, stderr)
		}
	}
	// The item does not exist, so the run fails; the step is recorded before the
	// run starts, which is all this asks about it.
	run := func() { runYoyo(t, "run", "--config", configPath, "calc-1") }

	scheduled := []struct {
		name  string
		value string
		by    string
	}{
		{execution.StartedByVariable, maintain.StartedByMaintenance, "from the supervisor's maintenance pass"},
		{execution.LaunchdJobVariable, maintenancejob.Label, "from the operator's retired maintenance job"},
		{execution.LaunchdJobVariable, launchd.Label("yoyodyne"), "from beneath the product's launch agent"},
	}
	for _, schedule := range scheduled {
		t.Setenv(schedule.name, schedule.value)
		reconcile(schedule.by)
		run()
		t.Setenv(schedule.name, "")
	}
	if listed := listedInterventions(t, configPath); len(listed.Interventions) != 0 {
		t.Fatalf("listed %+v, want nothing recorded for a schedule's reconcile or run", listed.Interventions)
	}

	t.Setenv(execution.LaunchdJobVariable, "0")
	reconcile("typed at a terminal")
	run()
	listed := listedInterventions(t, configPath)
	kinds := map[intervention.Kind]intervention.Event{}
	for _, event := range listed.Interventions {
		kinds[event.Kind] = event
	}
	if len(listed.Interventions) != 2 {
		t.Fatalf("listed %+v, want the reconcile and the run typed at a terminal", listed.Interventions)
	}
	if settle, ok := kinds[intervention.KindSettle]; !ok || settle.Via != viaCommandLine {
		t.Errorf("the reconcile was recorded as %+v, want a settlement taken at the command line", settle)
	}
	if ran, ok := kinds[intervention.KindRun]; !ok || ran.Via != viaCommandLine || !ran.Names("calc-1", "") {
		t.Errorf("the run was recorded as %+v, want calc-1 run by name at the command line", ran)
	}
}

func TestRecordingAnOutsideStepAsksWhoTookIt(t *testing.T) {
	// Not parallel: the state root and the process's own markers are set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(execution.StartedByVariable, "")
	configPath := writeConfig(t, validConfig)

	code, _, stderr := runYoyo(t, "intervention", "record", "--kind", "reset", "--config", configPath, "reset main by hand")
	if code == 0 || !strings.Contains(stderr, "say who took the step") {
		t.Errorf("a step nobody took was recorded: exit %d, %s", code, stderr)
	}
	code, _, stderr = runYoyo(t, "intervention", "record", "--kind", "reset", "--by", "Mason", "--at", "yesterday", "--config", configPath, "reset main by hand")
	if code == 0 || !strings.Contains(stderr, "is not a time") {
		t.Errorf("a step at no time was recorded: exit %d, %s", code, stderr)
	}
	code, _, stderr = runYoyo(t, "intervention", "record", "--kind", "rebooted", "--by", "Mason", "--config", configPath, "rebooted")
	if code == 0 || !strings.Contains(stderr, "the kinds are") {
		t.Errorf("a step of no kind was recorded: exit %d, %s", code, stderr)
	}
}
