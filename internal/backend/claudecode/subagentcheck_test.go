package claudecode

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// recordedSubagents is what Claude Code 2.1.286 answered the sub-agent check.
// See testdata/settings-check/README.md.
const recordedSubagents = "testdata/settings-check/claude-2.1.286-subagents.jsonl"

// subagentAnswers answers as 2.1.286 did for the launch of a run with
// sub-agents: the sub-agents it loaded, renamed for the run being checked; the
// merged settings as passed; and its hooks, with the recorded placeholder
// command replaced by the guard the run is launched with. change, where set,
// edits each answer before it is given.
func subagentAnswers(t *testing.T, change func(id string, response map[string]any)) func(string) string {
	t.Helper()
	recorded := readRecorded(t, recordedSubagents)
	name := subagentName(testRunID)
	return func(settings string) string {
		var lines []string
		for _, line := range strings.Split(strings.TrimSpace(recorded), "\n") {
			var envelope map[string]any
			if err := json.Unmarshal([]byte(line), &envelope); err != nil {
				t.Fatalf("recorded answer %q: %v", line, err)
			}
			outer := envelope["response"].(map[string]any)
			response := outer["response"].(map[string]any)
			id, _ := outer["request_id"].(string)
			switch id {
			case initializeQuestion:
				for _, agent := range response["agents"].([]any) {
					entry := agent.(map[string]any)
					if strings.HasPrefix(entry["name"].(string), "yoyodyne-developer-") {
						entry["name"] = name
					}
				}
			case settingsQuestion:
				var passed map[string]any
				if err := json.Unmarshal([]byte(settings), &passed); err != nil {
					t.Fatalf("settings %q: %v", settings, err)
				}
				response["effective"] = passed
			case hooksQuestion:
				for _, hook := range response["hooks"].([]any) {
					entry := hook.(map[string]any)
					if entry["matcher"] == "Agent|Task" {
						entry["commandText"] = subagentGuard(name)
					}
				}
			}
			if change != nil {
				change(id, response)
			}
			encoded, _ := json.Marshal(envelope)
			lines = append(lines, string(encoded))
		}
		return strings.Join(lines, "\n") + "\n"
	}
}

func checkSubagents(t *testing.T, cli *scriptedCLI) backendapi.LaunchSettingsCheck {
	t.Helper()
	check, err := (Backend{Runner: cli}).CheckSubagentLaunch(context.Background(), t.TempDir(), testRunID, backendapi.SubagentRequest{Model: "sonnet"})
	if err != nil {
		t.Fatalf("CheckSubagentLaunch() error = %v", err)
	}
	return check
}

// A CLI that loaded the run's own sub-agent on its model, rejected none of the
// settings, and will run the sub-agent guard confirms sub-agents; the check
// launched it exactly as the run is launched with sub-agents, and sent it
// questions and no prompt.
func TestACLIThatAppliesWhatSubagentsAreLaunchedWithConfirmsThem(t *testing.T) {
	t.Parallel()

	worktree := t.TempDir()
	cli := &scriptedCLI{version: "2.1.286 (Claude Code)", answers: subagentAnswers(t, nil)}
	request := backendapi.SubagentRequest{Model: "sonnet", Effort: "medium"}
	check, err := (Backend{Runner: cli}).CheckSubagentLaunch(context.Background(), worktree, testRunID, request)
	if err != nil || !check.InForce() {
		t.Fatalf("check = %#v, %v; want sub-agents confirmed on 2.1.286", check, err)
	}
	launched := cli.commands[len(cli.commands)-1]

	developer := &fakeRunner{results: claudeCompletedTurn()}
	if _, err := (Backend{Runner: developer, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree, Prompt: "do the work", Subagents: &request,
	}); err != nil {
		t.Fatal(err)
	}
	run := developer.commands[0]
	for _, option := range []string{"--settings", "--agents"} {
		checked, _ := optionValue(launched.Args, option)
		given, _ := optionValue(run.Args, option)
		if checked == "" || checked != given {
			t.Fatalf("checked %s %s, want the run's %s", option, checked, given)
		}
	}
	if !slices.Equal(allowedToolsOf(launched.Args), allowedToolsOf(run.Args)) {
		t.Fatalf("checked allowed tools %v, want the run's %v", allowedToolsOf(launched.Args), allowedToolsOf(run.Args))
	}
	for _, variable := range subagentEnvironment {
		if value, _ := environmentValue(launched.Env, variable[0]); value != variable[1] {
			t.Fatalf("check environment %s = %q, want the run's %q", variable[0], value, variable[1])
		}
	}
	for _, arg := range launched.Args {
		if strings.HasPrefix(arg, "-") && !slices.Contains(reliedFlags, arg) && !slices.Contains(subagentFlags, arg) {
			t.Fatalf("the check passes %s, which nothing confirms", arg)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(cli.stdin[0]), "\n") {
		var sent struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &sent); err != nil || sent.Type != "control_request" {
			t.Fatalf("sent %q, want only control questions and no prompt", line)
		}
	}
}

// Each thing the check confirms, missing, is named in plain words: the run's
// sub-agent not loaded, loaded on another model, the guard not among the
// hooks, a settings file rejected, and on an upgraded CLI a flag its help no
// longer names.
func TestACLIThatDoesNotApplyWhatSubagentsNeedSaysWhat(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		cli    *scriptedCLI
		wanted string
	}{
		{"no sub-agent", &scriptedCLI{version: "2.1.286 (Claude Code)", answers: subagentAnswers(t, func(id string, response map[string]any) {
			if id == initializeQuestion {
				response["agents"] = []any{map[string]any{"name": "general-purpose"}}
			}
		})}, "the run's own sub-agent " + subagentName(testRunID) + " is not among the sub-agents it loaded"},
		{"another model", &scriptedCLI{version: "2.1.286 (Claude Code)", answers: subagentAnswers(t, func(id string, response map[string]any) {
			if id == initializeQuestion {
				response["agents"] = []any{map[string]any{"name": subagentName(testRunID), "model": "opus"}}
			}
		})}, `is loaded on model "opus", not the "sonnet" it was defined with`},
		{"no guard", &scriptedCLI{version: "2.1.286 (Claude Code)", answers: subagentAnswers(t, func(id string, response map[string]any) {
			if id == hooksQuestion {
				response["hooks"] = []any{}
			}
		})}, "the hook that refuses every other sub-agent and any in the background is not among the hooks it will run"},
		{"rejected settings", &scriptedCLI{version: "2.1.286 (Claude Code)", answers: subagentAnswers(t, func(id string, response map[string]any) {
			if id == settingsQuestion {
				response["errors"] = []any{map[string]any{"path": "hooks", "message": "Invalid hook"}}
			}
		})}, "it rejected the setting hooks (Invalid hook)"},
		{"an upgraded CLI without the flag", &scriptedCLI{
			version: "2.2.0 (Claude Code)",
			help:    strings.ReplaceAll(readRecorded(t, recordedHelp), "--forward-subagent-text", "--forward-agent-text"),
			answers: subagentAnswers(t, nil),
		}, "the flag --forward-subagent-text is not in its help (it was read from 2.1.286)"},
	} {
		check := checkSubagents(t, test.cli)
		if check.InForce() || !strings.Contains(check.Says(), test.wanted) {
			t.Fatalf("%s: check says %q, want it to name %q", test.name, check.Says(), test.wanted)
		}
	}

	upgraded := checkSubagents(t, &scriptedCLI{version: "2.2.0 (Claude Code)", help: readRecorded(t, recordedHelp), answers: subagentAnswers(t, nil)})
	if !upgraded.InForce() {
		t.Fatalf("an upgraded CLI that names both flags and applies everything: %q", upgraded.Says())
	}
}
