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
		`{"id":"9","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))
	want := []backendapi.ToolCall{
		{ID: "call-1", Tool: "shell", Kind: backendapi.ToolKindShell, Outcome: backendapi.ToolExecuted},
		{ID: "call-2", Tool: "anchor.listTasks", Kind: backendapi.ToolKindExternal, Outcome: backendapi.ToolExecuted},
		{ID: "call-3", Tool: "apply_patch", Kind: backendapi.ToolKindWrite, Outcome: backendapi.ToolFailed},
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

// The newer vocabulary's command, patch, and nested tool items have not been
// recorded, so the parser does not read them, and a stream carrying one says
// the call could not be seen rather than reading as a session that used none.
// The recorded reply stream carries no such item, so it is a complete reading.
func TestCodexItemsItCannotReadAreLimits(t *testing.T) {
	t.Parallel()
	recorded, err := os.ReadFile("testdata/streams/codex-cli-0.159.2/reply-and-turn-completed.jsonl")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	result, _ := runStream(t, domain.RoleDeveloper, string(recorded))
	if len(result.ToolCalls) != 0 || len(result.ToolVisibility.Gaps) != 0 || !result.ToolVisibility.Read {
		t.Fatalf("recorded reply: calls %#v, visibility %#v, want a complete reading with no calls", result.ToolCalls, result.ToolVisibility)
	}

	lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	last := lines[len(lines)-1]
	withItems := append(append([]string(nil), lines[:len(lines)-1]...),
		`{"type":"item.started","item":{"id":"item_7","type":"command_execution","command":"ls","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_7","type":"command_execution","command":"ls","exit_code":0,"status":"completed"}}`,
		last,
	)
	result, _ = runStream(t, domain.RoleDeveloper, strings.Join(withItems, "\n"))
	gaps := strings.Join(result.ToolVisibility.Gaps, "\n")
	if len(result.ToolCalls) != 0 || !strings.Contains(gaps, `"item.started"`) || !strings.Contains(gaps, "command_execution") {
		t.Fatalf("calls %#v, gaps %q, want each unread item named as a limit", result.ToolCalls, gaps)
	}
}
