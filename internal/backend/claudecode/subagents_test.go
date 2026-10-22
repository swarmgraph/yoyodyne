package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// runDeveloper makes one developer invocation through a fake runner and
// returns the command the CLI would have been started with.
func runDeveloper(t *testing.T, subagents *backendapi.SubagentRequest) execution.Command {
	t.Helper()
	runner := &fakeRunner{results: claudeCompletedTurn()}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(),
		Prompt: "do the work", Model: "opus", Subagents: subagents,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return runner.commands[0]
}

// allowedToolsOf is the rules an invocation passed after --allowedTools, which
// runs to the next option.
func allowedToolsOf(args []string) []string {
	index := slices.Index(args, "--allowedTools")
	if index < 0 {
		return nil
	}
	var rules []string
	for _, arg := range args[index+1:] {
		if strings.HasPrefix(arg, "--") {
			break
		}
		rules = append(rules, arg)
	}
	return rules
}

// With sub-agents off a developer is invoked exactly as before they existed:
// the same tools, the same settings, no agent definition, nothing forwarded,
// and none of the variables that confine sub-agents.
func TestADeveloperWithoutSubagentsIsInvokedAsBefore(t *testing.T) {
	t.Parallel()

	command := runDeveloper(t, nil)
	if got := allowedToolsOf(command.Args); !reflect.DeepEqual(got, developerTools) {
		t.Fatalf("allowed tools = %v, want the developer's own %v", got, developerTools)
	}
	for _, option := range []string{"--agents", "--forward-subagent-text"} {
		if slices.Contains(command.Args, option) {
			t.Fatalf("a developer without sub-agents was passed %s: %v", option, command.Args)
		}
	}
	hooks, _ := invocationSettings(t, command.Args)["hooks"].(map[string]any)
	if pre, _ := hooks["PreToolUse"].([]any); len(pre) != 1 {
		t.Fatalf("PreToolUse hooks = %v, want the attribution guard alone", pre)
	}
	for _, variable := range subagentEnvironment {
		if _, found := environmentValue(command.Env, variable[0]); found {
			t.Fatalf("a developer without sub-agents carries %s", variable[0])
		}
	}
}

// With sub-agents on, the only sub-agent the session may start is the one
// the harness defines for the run: its tools are the run's own without the
// sub-agent tool, it runs on the configured model and effort and never in the
// background, the allow rule names it alone, and the session's sandbox and
// write rules are exactly what they are without it.
func TestADeveloperWithSubagentsMayStartOnlyTheRunsOwnSubagent(t *testing.T) {
	t.Parallel()

	without := runDeveloper(t, nil)
	command := runDeveloper(t, &backendapi.SubagentRequest{Model: "sonnet", Effort: "medium"})
	name := subagentName(testRunID)

	encoded, found := optionValue(command.Args, "--agents")
	if !found {
		t.Fatalf("no --agents: %v", command.Args)
	}
	var definitions map[string]struct {
		Tools           []string `json:"tools"`
		DisallowedTools []string `json:"disallowedTools"`
		Model           string   `json:"model"`
		Effort          string   `json:"effort"`
		Background      *bool    `json:"background"`
		Prompt          string   `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(encoded), &definitions); err != nil {
		t.Fatalf("--agents %q: %v", encoded, err)
	}
	if len(definitions) != 1 {
		t.Fatalf("--agents defines %d sub-agents, want the run's one: %s", len(definitions), encoded)
	}
	definition, found := definitions[name]
	if !found {
		t.Fatalf("--agents does not define %q: %s", name, encoded)
	}
	for _, tool := range definition.Tools {
		if isSubagentTool(tool) {
			t.Fatalf("the sub-agent may start a sub-agent: tools %v", definition.Tools)
		}
		if !slices.ContainsFunc(developerTools, func(rule string) bool { return rule == tool || strings.HasPrefix(rule, tool+"(") }) {
			t.Fatalf("the sub-agent holds %q, which the run does not: tools %v", tool, definition.Tools)
		}
	}
	for _, tool := range subagentToolNames {
		if !slices.Contains(definition.DisallowedTools, tool) {
			t.Fatalf("the sub-agent is not refused %s: %v", tool, definition.DisallowedTools)
		}
	}
	if definition.Model != "sonnet" || definition.Effort != "medium" {
		t.Fatalf("sub-agent model %q effort %q, want sonnet and medium", definition.Model, definition.Effort)
	}
	if definition.Background == nil || *definition.Background {
		t.Fatalf("the sub-agent is not held out of the background: %s", encoded)
	}

	wantRules := append(append([]string(nil), developerTools...), "Agent("+name+")", "Task("+name+")")
	if got := allowedToolsOf(command.Args); !reflect.DeepEqual(got, wantRules) {
		t.Fatalf("allowed tools = %v, want %v", got, wantRules)
	}
	if !slices.Contains(command.Args, "--forward-subagent-text") {
		t.Fatalf("a sub-agent's text is not on the stream, so a working sub-agent could read as silence: %v", command.Args)
	}
	for _, option := range []string{"--add-dir", "--dangerously-skip-permissions"} {
		if slices.Contains(command.Args, option) {
			t.Fatalf("a run with sub-agents was passed %s: %v", option, command.Args)
		}
	}
	if command.Dir == "" {
		t.Fatalf("the session runs in no worktree: %q", command.Dir)
	}

	settings := invocationSettings(t, command.Args)
	before := invocationSettings(t, without.Args)
	for _, key := range []string{"sandbox", "permissions"} {
		if !reflect.DeepEqual(settings[key], before[key]) {
			t.Fatalf("settings %q = %v with sub-agents and %v without; the sub-agent must run under the session's own", key, settings[key], before[key])
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	var guarded bool
	for _, entry := range pre {
		hook, _ := entry.(map[string]any)
		if hook["matcher"] == "Agent|Task" {
			guarded = true
		}
	}
	if len(pre) != 2 || !guarded {
		t.Fatalf("PreToolUse hooks = %v, want the attribution guard and the sub-agent guard", pre)
	}

	for _, variable := range subagentEnvironment {
		if value, _ := environmentValue(command.Env, variable[0]); value != variable[1] {
			t.Fatalf("%s = %q, want %q", variable[0], value, variable[1])
		}
	}
	if command.IdleTimeout != without.IdleTimeout || command.Timeout != without.Timeout {
		t.Fatalf("limits with sub-agents idle %s total %s, without idle %s total %s; neither is lengthened for them",
			command.IdleTimeout, command.Timeout, without.IdleTimeout, without.Timeout)
	}
}

// An operator's own sub-agent model variables do not reach a run: the model a
// run's sub-agents use is the configuration's.
func TestAnOperatorsSubagentModelDoesNotReachARun(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", "haiku")
	t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL_FORCE", "haiku")

	command := runDeveloper(t, &backendapi.SubagentRequest{Model: "sonnet"})
	for _, name := range subagentModelOverrides {
		if value, found := environmentValue(command.Env, name); found {
			t.Fatalf("%s=%s carried into a run with sub-agents", name, value)
		}
	}
}

// The guard is executed here, as Claude Code executes it, against the tool
// calls it has to decide: the run's own sub-agent passes, and every other
// type, a missing type, a background call, and a call that only mentions the
// run's sub-agent in its text are refused.
func TestTheSubagentGuardRefusesEverySubagentButTheRunsOwn(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell to run the guard with")
	}
	name := subagentName(testRunID)
	call := func(input map[string]any) string {
		encoded, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Agent", "tool_input": input})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	for _, test := range []struct {
		name    string
		input   string
		allowed bool
	}{
		{"the run's own", call(map[string]any{"subagent_type": name, "description": "write the parser", "prompt": "do it"}), true},
		{"a built-in", call(map[string]any{"subagent_type": "general-purpose", "description": "x", "prompt": "do it"}), false},
		{"the repository's", call(map[string]any{"subagent_type": "repo-agent", "description": "x", "prompt": "do it"}), false},
		{"no type", call(map[string]any{"description": "x", "prompt": "do it"}), false},
		{"in the background", call(map[string]any{"subagent_type": name, "description": "x", "prompt": "do it", "run_in_background": true}), false},
		{"named only in its text", call(map[string]any{"subagent_type": "general-purpose", "description": `"subagent_type":"` + name + `"`, "prompt": "do it"}), false},
		{"the run's own, written with spaces", `{"tool_name": "Agent", "tool_input": {"subagent_type": "` + name + `", "prompt": "do it"}}`, true},
		{"in the background, written with spaces", `{"tool_name": "Agent", "tool_input": {"subagent_type": "` + name + `", "run_in_background" : true}}`, false},
		{"background named only in its text", call(map[string]any{"subagent_type": name, "description": `"run_in_background":true`, "prompt": "do it"}), true},
	} {
		command := exec.Command("sh", "-c", subagentGuard(name))
		command.Stdin = strings.NewReader(test.input)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		err := command.Run()
		var exit *exec.ExitError
		switch {
		case test.allowed && err != nil:
			t.Fatalf("%s: refused: %v %s", test.name, err, stderr.String())
		case !test.allowed && (!errors.As(err, &exit) || exit.ExitCode() != 2):
			t.Fatalf("%s: not refused (error %v); input %s", test.name, err, test.input)
		case !test.allowed && !strings.Contains(stderr.String(), "Yoyodyne:"):
			t.Fatalf("%s: refused without saying why: %q", test.name, stderr.String())
		}
	}
}

// Sub-agents are refused where they cannot be contained or would be given
// tools nobody chose, rather than started anyway or dropped silently.
func TestRunRefusesSubagentsItCannotGive(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		request backendapi.RunRequest
	}{
		{"a reviewer", backendapi.RunRequest{Role: domain.RoleReviewer, Subagents: &backendapi.SubagentRequest{Model: "sonnet"}}},
		{"a developer held read-only", backendapi.RunRequest{Role: domain.RoleDeveloper, Posture: backendapi.PostureReadOnly, Subagents: &backendapi.SubagentRequest{Model: "sonnet"}}},
		{"no model", backendapi.RunRequest{Role: domain.RoleDeveloper, Subagents: &backendapi.SubagentRequest{}}},
		{"a caller's own rule", backendapi.RunRequest{Role: domain.RoleDeveloper, AllowedTools: []string{"Read", "Agent"}}},
	} {
		request := test.request
		request.RunID, request.WorkingDirectory, request.Prompt = testRunID, t.TempDir(), "do the work"
		runner := &fakeRunner{results: claudeCompletedTurn()}
		if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), request); err == nil {
			t.Fatalf("%s: sub-agents were given", test.name)
		}
		if len(runner.commands) != 0 {
			t.Fatalf("%s: the provider was started", test.name)
		}
	}
}

// Every call to a sub-agent is recorded with its model, its task, how
// many tools it used and its usage; one the invocation ended before it
// finished is recorded as interrupted; what the sub-agent says is not the
// developer's reply; and the terminal's usage is kept for each model.
func TestRunRecordsEachSubagentCallAndTheUsageOfEachModel(t *testing.T) {
	t.Parallel()

	name := subagentName(testRunID)
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-opus-5-5"}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Agent","input":{"subagent_type":"` + name + `","description":"write the parser","prompt":"write it"}}]}}`,
		`{"type":"assistant","session_id":"session-1","parent_tool_use_id":"toolu_1","message":{"content":[{"type":"text","text":"I will edit parser.go"},{"type":"tool_use","id":"toolu_1a","name":"Edit","input":{}}]}}`,
		`{"type":"user","session_id":"session-1","parent_tool_use_id":"toolu_1","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1a","content":"ok"}]}}`,
		`{"type":"assistant","session_id":"session-1","parent_tool_use_id":"toolu_1","message":{"content":[{"type":"tool_use","id":"toolu_1b","name":"Bash","input":{}}]}}`,
		`{"type":"user","session_id":"session-1","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"changed parser.go"}]},"tool_use_result":{"status":"completed","totalTokens":1234,"totalToolUseCount":2}}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"Agent","input":{"subagent_type":"` + name + `","description":"write the tests","prompt":"write them"}}]}}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"text","text":"The parser is done."}]}}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"The parser is done.","total_cost_usd":1.5,` +
			`"modelUsage":{"claude-opus-5-5":{"inputTokens":10,"outputTokens":20,"cacheReadInputTokens":30,"cacheCreationInputTokens":40,"costUSD":1.25},` +
			`"claude-sonnet-5-5":{"inputTokens":1,"outputTokens":2,"costUSD":0.25}}}`,
	}, "\n") + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}
	var events []execution.Event
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(), Prompt: "do the work",
		Subagents: &backendapi.SubagentRequest{Model: "sonnet"},
		EventSink: func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	at := fixedClock{}.Now()
	want := []backendapi.SubagentCall{
		{ID: "toolu_1", StartedAt: at, EndedAt: at, Agent: name, Model: "sonnet", Description: "write the parser", ToolUses: 2, TotalTokens: 1234, UsageReported: true},
		{ID: "toolu_2", StartedAt: at, Interrupted: true, Agent: name, Model: "sonnet", Description: "write the tests"},
	}
	if !reflect.DeepEqual(result.SubagentCalls, want) {
		t.Fatalf("sub-agent calls = %+v\nwant %+v", result.SubagentCalls, want)
	}
	if result.FinalText != "The parser is done." {
		t.Fatalf("final text = %q; a sub-agent's prose was taken as the developer's", result.FinalText)
	}
	if !result.ModelUsageReported || !reflect.DeepEqual(result.ModelUsage, []backendapi.ModelUsage{
		{Model: "claude-opus-5-5", CostUSD: 1.25, InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 30, CacheCreationInputTokens: 40},
		{Model: "claude-sonnet-5-5", CostUSD: 0.25, InputTokens: 1, OutputTokens: 2},
	}) {
		t.Fatalf("model usage = %+v (reported %v)", result.ModelUsage, result.ModelUsageReported)
	}
	if !result.CostReported || result.CostUSD != 1.5 {
		t.Fatalf("cost = %v (reported %v); the sub-agent's spend is the invocation's", result.CostUSD, result.CostReported)
	}
	// Every line the sub-agent produced is an event of the invocation, which is
	// what the runner's limit on silence counts.
	var nested int
	for _, event := range events {
		if strings.Contains(string(event.Payload), `"parent_tool_use_id":"toolu_1"`) {
			nested++
		}
	}
	if nested != 4 {
		t.Fatalf("%d events are the sub-agent's, want its text, two tool calls and a result", nested)
	}
}

// A terminal that reports no usage per model leaves the split unknown rather
// than attributing the whole to the model the run asked for.
func TestATerminalWithoutModelUsageLeavesTheSplitUnknown(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-opus-5-5"}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":1.5}`,
	}, "\n") + "\n"}}}
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(), Prompt: "do the work",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ModelUsageReported || result.ModelUsage != nil {
		t.Fatalf("model usage = %+v reported %v, want unknown", result.ModelUsage, result.ModelUsageReported)
	}
}

// A usage limit the provider reports on the sub-agents' model alone — the
// weekly Sonnet limit on a developer running on Opus — stops the sub-agents and
// not the invocation: it is recorded apart, and the invocation carries no
// limit, so the run neither waits nor moves to another endpoint. The same
// report on a run without sub-agents, a limit on the whole account, and a
// limit on the developer's own family are the invocation's, as they always
// were.
func TestALimitOnTheSubagentsModelAloneStopsThemAndNotTheRun(t *testing.T) {
	t.Parallel()

	stream := func(kind string) string {
		return strings.Join([]string{
			`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-opus-5-5"}`,
			`{"type":"rate_limit_event","session_id":"session-1","rate_limit_info":{"status":"rejected","rateLimitType":"` + kind + `","resetsAt":1792800000}}`,
			`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done without the sub-agent","terminal_reason":"completed","total_cost_usd":1}`,
		}, "\n") + "\n"
	}
	run := func(kind string, subagents *backendapi.SubagentRequest) backendapi.RunResult {
		runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream(kind)}}}
		result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
			RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(), Prompt: "do the work",
			Model: "opus", Subagents: subagents,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		return result
	}
	sonnet := &backendapi.SubagentRequest{Model: "sonnet"}

	result := run("seven_day_sonnet", sonnet)
	if result.UsageLimit != nil {
		t.Fatalf("the invocation carries the sub-agents' limit as its own: %+v", result.UsageLimit)
	}
	if result.SubagentLimit == nil || result.SubagentLimit.Kind != "seven_day_sonnet" || result.SubagentLimit.ResetsAt.Unix() != 1792800000 {
		t.Fatalf("sub-agent limit = %+v", result.SubagentLimit)
	}
	for _, test := range []struct {
		name      string
		kind      string
		subagents *backendapi.SubagentRequest
	}{
		{"no sub-agents", "seven_day_sonnet", nil},
		{"the whole account", "five_hour", sonnet},
		{"the developer's own family", "seven_day_opus", sonnet},
	} {
		result := run(test.kind, test.subagents)
		if result.UsageLimit == nil || result.UsageLimit.Kind != test.kind || result.SubagentLimit != nil {
			t.Fatalf("%s: usage limit %+v, sub-agent limit %+v; want the invocation's own", test.name, result.UsageLimit, result.SubagentLimit)
		}
	}
}
