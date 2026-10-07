package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/maintain"
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
