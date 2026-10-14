package cli

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An instance with no triggers, as the technical-health program manager is
// configured: it passes only when asked.
const onRequestProgramManager = `  technical-health-pm:
    role: program-manager
    backend: claude-code
    model: opus
    lane: technical-health
`

// The operator asks a program manager instance for a pass by command: the
// request is recorded as his, with its focus, and nothing is run by the command
// itself; a second request while the first is open joins it.
func TestTheOperatorAsksForAPassByCommand(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, twoArchitectsConfig+onRequestProgramManager)

	stdout, stderr, code := runCLI(t, "agent", "pass", "technical-health-pm", "--focus", "test quality", "--config", configPath)
	if code != 0 {
		t.Fatalf("agent pass code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"Asked technical-health-pm for a pass", `the area "test quality"`, "Nothing has run yet", "next pull"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to say %q", stdout, want)
		}
	}

	root, err := resolvedTestStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	requests, err := runstate.NewPassRequestStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	open, found, err := requests.Open("technical-health-pm")
	if err != nil || !found {
		t.Fatalf("Open() = %v, %v, want the request recorded", found, err)
	}
	if open.AskedBy != runstate.PassAskerOperator || len(open.Focus.Areas) != 1 || open.Focus.Areas[0] != "test quality" {
		t.Fatalf("request = %+v, want the operator's request naming its area", open)
	}

	stdout, stderr, code = runCLI(t, "agent", "pass", "technical-health-pm", "--config", configPath)
	if code != 0 {
		t.Fatalf("second agent pass code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "already open ("+open.ID+")") {
		t.Errorf("stdout = %q, want the second request said to have joined the open one", stdout)
	}
	listed, err := requests.List("technical-health-pm")
	if err != nil || len(listed) != 2 || listed[1].JoinedTo != open.ID {
		t.Fatalf("List() = %+v, %v, want the second request joined to the first", listed, err)
	}
}

// Only a program manager instance takes a pass on request, a request names one
// area at most, and the instance's listing says it passes only when asked.
func TestAgentPassRefusesAnotherRoleAndTwoAreas(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, twoArchitectsConfig+onRequestProgramManager)

	if _, stderr, code := runCLI(t, "agent", "pass", "developer", "--config", configPath); code == 0 || !strings.Contains(stderr, "only a program manager instance takes a pass on request") {
		t.Errorf("agent pass developer code = %d, stderr = %q, want it refused", code, stderr)
	}
	if _, stderr, code := runCLI(t, "agent", "pass", "technical-health-pm", "--focus", "test quality", "--focus", "dead code", "--config", configPath); code != 2 || !strings.Contains(stderr, "names two areas") {
		t.Errorf("two areas code = %d, stderr = %q, want them refused", code, stderr)
	}

	stdout, stderr, code := runCLI(t, "agent", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("agent list code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "passes over its lane only when asked") {
		t.Errorf("agent list = %q, want the instance said to pass only when asked", stdout)
	}
}
