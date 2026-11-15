package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/tooluse"
)

const toolStreamInit = `{"type":"system","subtype":"init","session_id":"session-tools","model":"claude-opus-5"}`

const toolStreamResult = `{"type":"result","subtype":"success","session_id":"session-tools","is_error":false,"result":"done","total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1}}`

// runRoleStream runs one stream through the adapter as the role, so the tool
// policy on the result is the one the adapter launches that role under.
func runRoleStream(t *testing.T, role domain.AgentRole, lines ...string) backendapi.RunResult {
	t.Helper()
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: strings.Join(lines, "\n")}}},
		Clock:  fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             role,
		WorkingDirectory: t.TempDir(),
		Prompt:           "work",
		RedactValues:     []string{"secret-token-value"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result
}

// The recorded stream of run-f3755e3f carries one Bash call that ran, and a
// developer is launched with its tools: the call is read as executed under the
// developer's policy, with nothing the adapter could not see.
func TestRecordedDeveloperCallIsReadAsExecutedUnderTheDeveloperPolicy(t *testing.T) {
	t.Parallel()
	stream, err := os.ReadFile("testdata/run-f3755e3f-second-terminal.jsonl")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	result, _ := runStream(t, string(stream), "resolve the review findings")
	want := []backendapi.ToolCall{{ID: "toolu_01WnLksMJnqRtoaubVxLrsmJ", Tool: "Bash", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolExecuted}}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0] != want[0] {
		t.Fatalf("ToolCalls = %#v, want %#v", result.ToolCalls, want)
	}
	visibility := result.ToolVisibility
	if !visibility.Read || visibility.Policy != backendapi.ToolPolicyDeveloper || len(visibility.Gaps) != 0 || visibility.Subagents {
		// The recorded stream's every event is one the parser reads, so it names
		// nothing as unseen.
		t.Fatalf("ToolVisibility = %#v, want a complete reading under the developer's policy", visibility)
	}
}

func TestToolPolicyFollowsTheRole(t *testing.T) {
	t.Parallel()
	for role, want := range map[domain.AgentRole]backendapi.ToolPolicy{
		domain.RoleDeveloper:          backendapi.ToolPolicyDeveloper,
		domain.RoleReviewer:           backendapi.ToolPolicyInspection,
		domain.RoleProductManager:     backendapi.ToolPolicyNone,
		domain.RoleArchitect:          backendapi.ToolPolicyNone,
		domain.RoleDevelopmentManager: backendapi.ToolPolicyNone,
		domain.RoleProgramManager:     backendapi.ToolPolicyNone,
	} {
		result := runRoleStream(t, role, toolStreamInit, toolStreamResult)
		if result.ToolVisibility.Policy != want || !result.ToolVisibility.Read {
			t.Errorf("%s: ToolVisibility = %#v, want policy %q", role, result.ToolVisibility, want)
		}
		if len(result.ToolCalls) != 0 {
			t.Errorf("%s: a stream with no calls recorded %#v", role, result.ToolCalls)
		}
	}
}

// A call streamed again, a nested sub-agent call, a refusal said in the result,
// a refusal listed on the terminal, a provider-side tool, and a content block
// nobody describes: each is one call, in the shape the harness compares, and
// the block is a limit rather than nothing.
func TestClaudeToolCallsAreNormalized(t *testing.T) {
	t.Parallel()
	result := runRoleStream(t, domain.RoleProductManager,
		toolStreamInit,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call-read","name":"Read","input":{"file_path":"/etc/secret-token-value"}}]}}`,
		// The same call reported a second time, as a replay would.
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call-read","name":"Read","input":{"file_path":"/etc/secret-token-value"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-read","is_error":false,"content":"contents"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call-agent","name":"Task","input":{"description":"look"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"call-agent","message":{"content":[{"type":"tool_use","id":"call-nested","name":"Write","input":{"file_path":"x"}}]}}`,
		`{"type":"user","parent_tool_use_id":"call-agent","message":{"content":[{"type":"tool_result","tool_use_id":"call-nested","is_error":true,"content":[{"type":"text","text":"Claude requested permissions to use Write, but you haven't granted it yet."}]}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call-bash","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-bash","is_error":true,"content":"exit 2"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"server_tool_use","id":"call-web","name":"web_search","input":{"query":"q"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call-fetch","name":"WebFetch","input":{"url":"u"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"code_execution_v9","id":"x"}]}}`,
		`{"type":"result","subtype":"success","session_id":"session-tools","is_error":false,"result":"done","total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1},"permission_denials":[{"tool_name":"WebFetch","tool_use_id":"call-fetch","tool_input":{"url":"u"}}]}`,
	)
	want := []backendapi.ToolCall{
		{ID: "call-read", Tool: "Read", Kind: backendapi.ToolKindRead, Outcome: backendapi.ToolExecuted},
		{ID: "call-agent", Tool: "Task", Kind: backendapi.ToolKindAgent, Outcome: backendapi.ToolAttempted},
		{ID: "call-nested", Tool: "Write", Kind: backendapi.ToolKindWrite, Parent: "call-agent", Outcome: backendapi.ToolDenied},
		{ID: "call-bash", Tool: "Bash", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolFailed},
		{ID: "call-web", Tool: "web_search", Kind: backendapi.ToolKindWeb, Outcome: backendapi.ToolExecuted},
		{ID: "call-fetch", Tool: "WebFetch", Kind: backendapi.ToolKindWeb, Outcome: backendapi.ToolDenied},
	}
	if len(result.ToolCalls) != len(want) {
		t.Fatalf("ToolCalls = %#v, want %#v", result.ToolCalls, want)
	}
	for index := range want {
		if result.ToolCalls[index] != want[index] {
			t.Errorf("ToolCalls[%d] = %#v, want %#v", index, result.ToolCalls[index], want[index])
		}
	}
	gaps := result.ToolVisibility.Gaps
	if len(gaps) != 1 || !strings.Contains(gaps[0], `"code_execution_v9"`) {
		t.Fatalf("Gaps = %q, want the unread content block named", gaps)
	}
	if result.ToolVisibility.Policy != backendapi.ToolPolicyNone {
		t.Fatalf("Policy = %q, want a product manager held to no tools", result.ToolVisibility.Policy)
	}
}

// A result for a call whose start never arrived is a limit: nothing says which
// tool ran.
func TestAnOrphanToolResultIsALimit(t *testing.T) {
	t.Parallel()
	result := runRoleStream(t, domain.RoleArchitect,
		toolStreamInit,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-unseen","is_error":false,"content":"x"}]}}`,
		toolStreamResult,
	)
	if len(result.ToolCalls) != 0 || len(result.ToolVisibility.Gaps) != 1 {
		t.Fatalf("result = %#v / %#v, want no call and one limit", result.ToolCalls, result.ToolVisibility)
	}
}

// A top-level event type the parser has no reading of is said once as tool use
// that could not be seen, however often it arrives, while the types Claude
// Code sends that never carry a tool call add nothing.
func TestAnUnknownTopLevelEventIsALimit(t *testing.T) {
	t.Parallel()
	result := runRoleStream(t, domain.RoleDeveloper,
		toolStreamInit,
		`{"type":"stream_event","event":{"type":"content_block_delta"}}`,
		`{"type":"keep_alive"}`,
		`{"type":"control_request","request_id":"r1","request":{"subtype":"interrupt"}}`,
		`{"type":"tool_invocation_v2","name":"Bash"}`,
		`{"type":"tool_invocation_v2","name":"Bash"}`,
		toolStreamResult,
	)
	gaps := result.ToolVisibility.Gaps
	if len(gaps) != 1 || !strings.Contains(gaps[0], `"tool_invocation_v2"`) {
		t.Fatalf("Gaps = %q, want the one unread event type named once", gaps)
	}
}

// The tool_progress samples below are sanitized copies of the events Claude
// Code 2.1.321 writes, in the fields its own output schema declares for the
// event: a long command's elapsed time, a heartbeat, a sub-agent's retry, and a
// call a script tool made from inside its run.

// Progress on a call the stream already showed starting carries no call of its
// own: the call stays one call with the outcome its result gave, and nothing is
// said as unseen.
func TestToolProgressOnAKnownCallAddsNothing(t *testing.T) {
	t.Parallel()
	result := runRoleStream(t, domain.RoleDeveloper,
		toolStreamInit,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"make test"}}]}}`,
		`{"type":"tool_progress","tool_use_id":"toolu_bash","tool_name":"Bash","parent_tool_use_id":null,"elapsed_time_seconds":30,"task_id":"task-1","uuid":"00000000-0000-4000-8000-000000000001","session_id":"session-tools"}`,
		`{"type":"tool_progress","tool_use_id":"toolu_bash","tool_name":"Bash","parent_tool_use_id":null,"elapsed_time_seconds":60,"heartbeat":true,"uuid":"00000000-0000-4000-8000-000000000002","session_id":"session-tools"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_bash","is_error":false,"content":"ok"}]}}`,
		toolStreamResult,
	)
	want := backendapi.ToolCall{ID: "toolu_bash", Tool: "Bash", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolExecuted}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0] != want {
		t.Fatalf("ToolCalls = %#v, want only %#v", result.ToolCalls, want)
	}
	if gaps := result.ToolVisibility.Gaps; len(gaps) != 0 {
		t.Fatalf("Gaps = %q, want progress on a known call read as carrying none", gaps)
	}
}

// A call the stream shows only through tool_progress is recorded once however
// often its progress arrives, and is compared with the session's tool access
// like a call shown as a tool_use block: a reviewer's write is unexpected. A
// call a script tool made from inside its run is recorded inside the script's
// call.
func TestToolProgressNamingAnUnseenCallRecordsItOnce(t *testing.T) {
	t.Parallel()
	request := backendapi.RunRequest{Role: domain.RoleReviewer}
	result := runRoleStream(t, domain.RoleReviewer,
		toolStreamInit,
		`{"type":"tool_progress","tool_use_id":"toolu_write","tool_name":"Write","parent_tool_use_id":"toolu_agent","elapsed_time_seconds":5,"uuid":"00000000-0000-4000-8000-000000000003","session_id":"session-tools"}`,
		`{"type":"tool_progress","tool_use_id":"toolu_write","tool_name":"Write","parent_tool_use_id":"toolu_agent","elapsed_time_seconds":10,"heartbeat":true,"uuid":"00000000-0000-4000-8000-000000000004","session_id":"session-tools"}`,
		`{"type":"tool_progress","tool_use_id":"toolu_script","tool_name":"Script","parent_tool_use_id":null,"elapsed_time_seconds":0,"inner_call":{"tool_name":"Read","tool_input":{"file_path":"/etc/secret-token-value"},"tool_use_id":"toolu_inner","phase":"start"},"uuid":"00000000-0000-4000-8000-000000000005","session_id":"session-tools"}`,
		`{"type":"tool_progress","tool_use_id":"toolu_script","tool_name":"Script","parent_tool_use_id":null,"elapsed_time_seconds":0,"inner_call":{"tool_name":"Read","tool_input":{"file_path":"/etc/secret-token-value"},"tool_use_id":"toolu_inner","phase":"end"},"uuid":"00000000-0000-4000-8000-000000000006","session_id":"session-tools"}`,
		toolStreamResult,
	)
	want := []backendapi.ToolCall{
		{ID: "toolu_write", Tool: "Write", Kind: backendapi.ToolKindWrite, Parent: "toolu_agent", Outcome: backendapi.ToolAttempted},
		{ID: "toolu_script", Tool: "Script", Kind: backendapi.ToolKindUnknown, Outcome: backendapi.ToolAttempted},
		{ID: "toolu_inner", Tool: "Read", Kind: backendapi.ToolKindRead, Parent: "toolu_script", Outcome: backendapi.ToolAttempted},
	}
	if len(result.ToolCalls) != len(want) {
		t.Fatalf("ToolCalls = %#v, want %#v", result.ToolCalls, want)
	}
	for index := range want {
		if result.ToolCalls[index] != want[index] {
			t.Errorf("ToolCalls[%d] = %#v, want %#v", index, result.ToolCalls[index], want[index])
		}
	}
	unexpected, gaps := tooluse.Check(request, result)
	if len(unexpected) != 2 || unexpected[0].ID != "toolu_write" || unexpected[1].ID != "toolu_script" {
		t.Fatalf("unexpected = %#v, want the write and the unknown script tool, each once", unexpected)
	}
	if len(gaps) != 0 {
		t.Fatalf("gaps = %q, want readable progress events said as nothing unseen", gaps)
	}
}

// A tool_progress event whose fields cannot be read, or that names no call,
// is still said as tool use that could not be seen, never dropped.
func TestUnreadableToolProgressIsALimit(t *testing.T) {
	t.Parallel()
	for name, line := range map[string]string{
		"an identifier that is not text": `{"type":"tool_progress","tool_use_id":42,"tool_name":"Bash","parent_tool_use_id":null,"elapsed_time_seconds":1,"uuid":"u","session_id":"session-tools"}`,
		"no tool named":                  `{"type":"tool_progress","tool_use_id":"toolu_x","parent_tool_use_id":null,"elapsed_time_seconds":1,"uuid":"u","session_id":"session-tools"}`,
		"no call named":                  `{"type":"tool_progress","elapsed_time_seconds":1,"uuid":"u","session_id":"session-tools"}`,
		"an inner call with no id":       `{"type":"tool_progress","tool_use_id":"toolu_script","tool_name":"Script","parent_tool_use_id":null,"elapsed_time_seconds":0,"inner_call":{"tool_name":"Write"},"uuid":"u","session_id":"session-tools"}`,
	} {
		result := runRoleStream(t, domain.RoleDeveloper, toolStreamInit, line, line, toolStreamResult)
		gaps := result.ToolVisibility.Gaps
		if len(gaps) != 1 || gaps[0] != `Claude Code wrote a "tool_progress" event the harness does not read, so a tool call it may carry was not observed` {
			t.Errorf("%s: Gaps = %q, want the unread event said once", name, gaps)
		}
		if len(result.ToolCalls) != 0 {
			t.Errorf("%s: ToolCalls = %#v, want nothing guessed from an event that could not be read", name, result.ToolCalls)
		}
	}
}

// Every tool the recorded developer session of run-841f5ee1 was offered has a
// deliberate kind, and a developer may use exactly the ones its work needs.
// Claude Code offers a developer more than its allowed-tools list, and the
// ones that ask no permission run, so a tool with no kind here would file a
// false ERROR on an ordinary run; a CLI that adds or renames one fails this
// test instead. Tools served by an MCP server are outside on purpose.
func TestEveryToolTheRecordedDeveloperSessionOffersHasADeliberateKind(t *testing.T) {
	t.Parallel()
	file, err := os.Open("testdata/run-841f5ee1-nested-agent-result.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	offered := map[string]bool{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		var event struct {
			Type    string   `json:"type"`
			Subtype string   `json:"subtype"`
			Tools   []string `json:"tools"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Type == "system" && event.Subtype == "init" {
			for _, tool := range event.Tools {
				offered[tool] = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(offered) == 0 {
		t.Fatal("the recorded session offered no tools, so this test is reading the wrong file")
	}
	forbidden := map[string]bool{
		// The network, other agents without sub-agents, and anything that
		// reaches outside the run are not a developer's.
		"WebFetch": true, "WebSearch": true, "Task": true, "SendMessage": true, "ListAgents": true,
		"CronCreate": true, "CronDelete": true, "CronList": true, "ScheduleWakeup": true, "RemoteTrigger": true,
		"PushNotification": true, "DesignSync": true, "EnterWorktree": true, "ExitWorktree": true,
	}
	var unknown, wrong []string
	for tool := range offered {
		kind := backendapi.ClassifyTool(tool)
		if strings.HasPrefix(tool, "mcp__") {
			if kind != backendapi.ToolKindExternal {
				wrong = append(wrong, tool)
			}
			continue
		}
		if kind == backendapi.ToolKindUnknown {
			unknown = append(unknown, tool)
			continue
		}
		if backendapi.ToolPolicyDeveloper.Permits(kind, false) == forbidden[tool] {
			wrong = append(wrong, tool)
		}
	}
	sort.Strings(unknown)
	sort.Strings(wrong)
	if len(unknown) > 0 || len(wrong) > 0 {
		t.Fatalf("tools with no kind: %v; tools a developer is wrongly allowed or refused: %v", unknown, wrong)
	}
}
