package codex

import (
	"context"
	"os"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// runRoleStream runs one stream through the adapter as the role, in a working
// directory that exists, which a read-only launch resolves.
func runRoleStream(t *testing.T, role domain.AgentRole, stream string) backendapi.RunResult {
	t.Helper()
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}},
		Clock:  fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             role,
		WorkingDirectory: t.TempDir(),
		Prompt:           "do the work",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result
}

// Shell, MCP, and patch calls in the vocabulary the parser reads are each one
// call, whichever of start and end arrives and however often.
func TestCodexToolCallsAreNormalized(t *testing.T) {
	t.Parallel()
	result := runRoleStream(t, domain.RoleReviewer, lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-6.1-sol"}}`,
		`{"id":"1","msg":{"type":"exec_command_begin","call_id":"call-1","command":["bash","-lc","cat secrets.txt"],"cwd":"/worktree"}}`,
		`{"id":"2","msg":{"type":"exec_command_begin","call_id":"call-1","command":["bash","-lc","cat secrets.txt"],"cwd":"/worktree"}}`,
		`{"id":"3","msg":{"type":"exec_command_end","call_id":"call-1","exit_code":0,"stdout":"x","stderr":""}}`,
		`{"id":"4","msg":{"type":"exec_command_end","call_id":"call-1","exit_code":0,"stdout":"x","stderr":""}}`,
		`{"id":"5","msg":{"type":"mcp_tool_call_begin","call_id":"call-2","server":"anchor","tool":"listTasks"}}`,
		`{"id":"6","msg":{"type":"mcp_tool_call_end","call_id":"call-2","success":true}}`,
		`{"id":"7","msg":{"type":"patch_apply_begin","call_id":"call-3","changes":{"a.go":{}}}}`,
		`{"id":"8","msg":{"type":"patch_apply_end","call_id":"call-3","success":false}}`,
		`{"id":"10","msg":{"type":"exec_command_begin","call_id":"call-4","command":["bash","-lc","touch x"],"cwd":"/worktree"}}`,
		`{"id":"11","msg":{"type":"exec_command_end","call_id":"call-4","exit_code":1,"stdout":"","stderr":"touch: x: Read-only file system"}}`,
		`{"id":"9","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))
	want := []backendapi.ToolCall{
		{ID: "call-1", Tool: "shell", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolExecuted},
		{ID: "call-2", Tool: "anchor.listTasks", Kind: backendapi.ToolKindExternal, Outcome: backendapi.ToolExecuted},
		{ID: "call-3", Tool: "apply_patch", Kind: backendapi.ToolKindWrite, Outcome: backendapi.ToolFailed},
		{ID: "call-4", Tool: "shell", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolDenied},
	}
	if len(result.ToolCalls) != len(want) {
		t.Fatalf("ToolCalls = %#v, want %#v", result.ToolCalls, want)
	}
	for index := range want {
		if result.ToolCalls[index] != want[index] {
			t.Errorf("ToolCalls[%d] = %#v, want %#v", index, result.ToolCalls[index], want[index])
		}
	}
	visibility := result.ToolVisibility
	if !visibility.Read || visibility.Policy != backendapi.ToolPolicyInspection || len(visibility.Gaps) != 0 {
		t.Fatalf("ToolVisibility = %#v, want a complete reading under read-only inspection", visibility)
	}
}

func TestCodexToolPolicyFollowsTheProfile(t *testing.T) {
	t.Parallel()
	stream := lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-6.1-sol"}}`,
		`{"id":"1","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	)
	for role, want := range map[domain.AgentRole]backendapi.ToolPolicy{
		domain.RoleDeveloper:      backendapi.ToolPolicyDeveloper,
		domain.RoleReviewer:       backendapi.ToolPolicyInspection,
		domain.RoleProductManager: backendapi.ToolPolicyInspection,
	} {
		result := runRoleStream(t, role, stream)
		if result.ToolVisibility.Policy != want || !result.ToolVisibility.Read || len(result.ToolCalls) != 0 {
			t.Errorf("%s: ToolVisibility = %#v, calls %#v, want policy %q and no calls", role, result.ToolVisibility, result.ToolCalls, want)
		}
	}
}

// withItems is the recorded codex-cli 0.159.2 reply stream with the given
// events spliced in ahead of its terminal, so the items are read inside the
// stream the CLI actually writes.
func withItems(t *testing.T, items ...string) string {
	t.Helper()
	recorded, err := os.ReadFile("testdata/streams/codex-cli-0.159.2/reply-and-turn-completed.jsonl")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	spliced := append(append([]string(nil), lines[:len(lines)-1]...), items...)
	return strings.Join(append(spliced, lines[len(lines)-1]), "\n")
}

// The recorded reply stream uses no tool and hides nothing, so it is a
// complete reading with no calls.
func TestTheRecordedCodexReplyIsACompleteReading(t *testing.T) {
	t.Parallel()
	result, _ := runStream(t, domain.RoleDeveloper, withItems(t))
	if len(result.ToolCalls) != 0 || len(result.ToolVisibility.Gaps) != 0 || !result.ToolVisibility.Read {
		t.Fatalf("calls %#v, visibility %#v, want a complete reading with no calls", result.ToolCalls, result.ToolVisibility)
	}
}

// The newer vocabulary's tool items, by the names and fields codex-cli
// 0.164.0 declares for them: each item is one call however often it is
// started, updated, and completed; a declined item and a command the sandbox
// stopped are refusals; a failed one is a failure. The Code Mode probe's two
// nested calls are here as the stream carries them -- a shell read that ran
// and a write the read-only sandbox refused -- each an item of its own.
func TestCodexToolItemsAreNormalized(t *testing.T) {
	t.Parallel()
	result := runRoleStream(t, domain.RoleReviewer, withItems(t,
		`{"type":"item.started","item":{"id":"item_3","type":"command_execution","command":"cat README.md","aggregated_output":"","status":"in_progress"}}`,
		`{"type":"item.updated","item":{"id":"item_3","type":"command_execution","command":"cat README.md","aggregated_output":"# t","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_3","type":"command_execution","command":"cat README.md","aggregated_output":"# t","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"item_3","type":"command_execution","command":"cat README.md","aggregated_output":"# t","exit_code":0,"status":"completed"}}`,
		`{"type":"item.started","item":{"id":"item_4","type":"command_execution","command":"touch probe","aggregated_output":"","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_4","type":"command_execution","command":"touch probe","aggregated_output":"touch: probe: Operation not permitted","exit_code":1,"status":"failed"}}`,
		`{"type":"item.completed","item":{"id":"item_5","type":"command_execution","command":"false","aggregated_output":"","exit_code":1,"status":"failed"}}`,
		`{"type":"item.completed","item":{"id":"item_6","type":"file_change","changes":[{"path":"a.go","kind":"update"}],"status":"declined"}}`,
		`{"type":"item.started","item":{"id":"item_7","type":"mcp_tool_call","server":"anchor","tool":"listTasks","arguments":{},"status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_7","type":"mcp_tool_call","server":"anchor","tool":"listTasks","arguments":{},"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"item_8","type":"web_search","query":"q"}}`,
		`{"type":"item.started","item":{"id":"item_9","type":"todo_list","items":[]}}`,
		`{"type":"item.started","item":{"id":"item_10","type":"command_execution","command":"sleep 99","status":"in_progress"}}`,
	))
	want := []backendapi.ToolCall{
		{ID: "item_3", Tool: "shell", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolExecuted, InvocationID: true},
		{ID: "item_4", Tool: "shell", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolDenied, InvocationID: true},
		{ID: "item_5", Tool: "shell", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolFailed, InvocationID: true},
		{ID: "item_6", Tool: "apply_patch", Kind: backendapi.ToolKindWrite, Outcome: backendapi.ToolDenied, InvocationID: true},
		{ID: "item_7", Tool: "anchor.listTasks", Kind: backendapi.ToolKindExternal, Outcome: backendapi.ToolExecuted, InvocationID: true},
		{ID: "item_8", Tool: "web_search", Kind: backendapi.ToolKindWeb, Outcome: backendapi.ToolExecuted, InvocationID: true},
		{ID: "item_9", Tool: "update_plan", Kind: backendapi.ToolKindSession, Outcome: backendapi.ToolAttempted, InvocationID: true},
		{ID: "item_10", Tool: "shell", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolAttempted, InvocationID: true},
	}
	if len(result.ToolCalls) != len(want) {
		t.Fatalf("ToolCalls = %#v, want %#v", result.ToolCalls, want)
	}
	for index := range want {
		if result.ToolCalls[index] != want[index] {
			t.Errorf("ToolCalls[%d] = %#v, want %#v", index, result.ToolCalls[index], want[index])
		}
	}
	if gaps := result.ToolVisibility.Gaps; len(gaps) != 0 {
		t.Fatalf("Gaps = %q, want every item read", gaps)
	}
}

// An item type the executable does not declare as a tool call is said as tool
// use that could not be seen, whichever event carries it, rather than read as
// no tool use.
func TestCodexItemsItCannotReadAreLimits(t *testing.T) {
	t.Parallel()
	result, _ := runStream(t, domain.RoleDeveloper, withItems(t,
		`{"type":"item.started","item":{"id":"item_7","type":"code_cell","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_7","type":"code_cell","status":"completed"}}`,
	))
	gaps := strings.Join(result.ToolVisibility.Gaps, "\n")
	if len(result.ToolCalls) != 0 || !strings.Contains(gaps, "item.started (code_cell item)") || !strings.Contains(gaps, "item.completed (code_cell item)") {
		t.Fatalf("calls %#v, gaps %q, want each unread item named as a limit", result.ToolCalls, gaps)
	}
}
