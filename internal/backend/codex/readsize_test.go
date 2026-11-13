package codex

import (
	"context"
	"encoding/json"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// completedPayload is what a command.completed says about how its call ended.
type completedPayload struct {
	ToolUseID    string `json:"tool_use_id"`
	IsError      *bool  `json:"is_error"`
	ContentBytes *int   `json:"content_bytes"`
}

// completedCommands runs a stream through the adapter as a reviewer and
// returns what each command.completed it emitted says, by call id.
func completedCommands(t *testing.T, stream string) map[string]completedPayload {
	t.Helper()
	completed := map[string]completedPayload{}
	_, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}},
		Clock:  fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleReviewer,
		WorkingDirectory: t.TempDir(),
		Prompt:           "review the change",
		EventSink: func(event execution.Event) error {
			if event.Type != execution.EventCommandCompleted {
				return nil
			}
			var payload completedPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("command.completed payload %s: %v", event.Payload, err)
			}
			completed[payload.ToolUseID] = payload
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return completed
}

// A finished command reports the size of its output wherever the stream
// carried the output, empty output included, and reports no size where the
// stream carried none, so a reader can tell an unmeasured read from an empty
// one. Each says whether the call was refused or failed, in both vocabularies.
func TestCodexCommandCompletionsReportTheSizeTheStreamCarries(t *testing.T) {
	t.Parallel()

	items := completedCommands(t, withItems(t,
		`{"type":"item.started","item":{"id":"item_3","type":"command_execution","command":"cat README.md","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_3","type":"command_execution","command":"cat README.md","aggregated_output":"# title\n","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"item_4","type":"command_execution","command":"git status --short","aggregated_output":"","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"item_5","type":"command_execution","command":"git log -1","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"item_6","type":"command_execution","command":"touch probe","aggregated_output":"touch: probe: Operation not permitted","exit_code":1,"status":"failed"}}`,
		`{"type":"item.completed","item":{"id":"item_7","type":"mcp_tool_call","server":"anchor","tool":"listTasks","status":"completed"}}`,
	))
	older := completedCommands(t, lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-6.1-sol"}}`,
		`{"id":"1","msg":{"type":"exec_command_end","call_id":"call-1","exit_code":0,"stdout":"abc","stderr":"de"}}`,
		`{"id":"2","msg":{"type":"exec_command_end","call_id":"call-2","exit_code":0,"stdout":"abc","stderr":"de","aggregated_output":"abcde"}}`,
		`{"id":"3","msg":{"type":"exec_command_end","call_id":"call-3","exit_code":0}}`,
		`{"id":"4","msg":{"type":"mcp_tool_call_end","call_id":"call-4","success":true}}`,
		`{"id":"5","msg":{"type":"exec_command_end","call_id":"call-5","exit_code":2,"stdout":"","stderr":"no such file"}}`,
		`{"id":"6","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))

	size := func(n int) *int { return &n }
	for _, test := range []struct {
		name    string
		got     map[string]completedPayload
		id      string
		isError bool
		bytes   *int
	}{
		{"an item with its output", items, "item_3", false, size(len("# title\n"))},
		{"an item whose output was empty", items, "item_4", false, size(0)},
		{"an item that left its output out", items, "item_5", false, nil},
		{"an item the sandbox refused", items, "item_6", true, size(len("touch: probe: Operation not permitted"))},
		{"an MCP item, which carries no output field", items, "item_7", false, nil},
		{"an end event with both streams", older, "call-1", false, size(5)},
		{"an end event with its aggregated output, counted once", older, "call-2", false, size(5)},
		{"an end event that left its output out", older, "call-3", false, nil},
		{"an MCP end event", older, "call-4", false, nil},
		{"an end event that failed", older, "call-5", true, size(len("no such file"))},
	} {
		got, ok := test.got[test.id]
		if !ok {
			t.Errorf("%s: no command.completed for %s", test.name, test.id)
			continue
		}
		if got.IsError == nil || *got.IsError != test.isError {
			t.Errorf("%s: is_error = %v, want %v", test.name, got.IsError, test.isError)
		}
		switch {
		case test.bytes == nil && got.ContentBytes != nil:
			t.Errorf("%s: content_bytes = %d, want none, because the stream carried no output", test.name, *got.ContentBytes)
		case test.bytes != nil && (got.ContentBytes == nil || *got.ContentBytes != *test.bytes):
			t.Errorf("%s: content_bytes = %v, want %d", test.name, got.ContentBytes, *test.bytes)
		}
	}
}
