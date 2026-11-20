package codex

import (
	"context"
	"errors"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// pricedEnding is a Codex turn that reported what it used and ended, followed
// by a line nobody can read. Codex reports usage rather than cost, and the
// usage is what the invocation is priced from.
var pricedEnding = lines(
	`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-6.1-sol"}}`,
	`{"id":"1","msg":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":7}}}}`,
	`{"id":"2","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	`{not json`,
)

// A line Codex writes after its terminal that will not decode is recorded as
// the plain output it is, and the terminal's usage is kept.
func TestAPricedEndingIsKeptWhenLaterOutputIsMalformed(t *testing.T) {
	t.Parallel()

	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: pricedEnding}}}, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.UsageReading == nil || result.SessionID != "session-1" || result.FinalText != "done" {
		t.Fatalf("result = %+v, want the terminal's usage, session and reply kept", result)
	}
}

// Where the harness cannot record an event after the terminal -- here the
// unreadable line, which Codex's adapter records as output -- the invocation
// fails over it, and the usage the terminal reported comes back beside the
// error rather than an empty result that would leave it priced as unknown.
func TestAPricedEndingIsKeptWhenAnEventAfterItCannotBeRecorded(t *testing.T) {
	t.Parallel()

	refused := errors.New("the event log refused the line")
	terminated := false
	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: pricedEnding}}}, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
		EventSink: func(event execution.Event) error {
			if terminated {
				return refused
			}
			terminated = event.Type == execution.EventRunCompleted
			return nil
		},
	})
	if !errors.Is(err, refused) {
		t.Fatalf("Run() error = %v, want the refused event reported", err)
	}
	if result.UsageReading == nil || result.SessionID != "session-1" || result.Backend != domain.BackendCodex {
		t.Fatalf("result = %+v, want the terminal's usage, session and backend kept beside the error", result)
	}
	if !result.IsError {
		t.Fatal("a result handed back beside an error must not read as a success")
	}
}
