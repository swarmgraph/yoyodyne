package claudecode

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A provider that has already written its priced terminal and then writes a
// line nobody can read has still charged for the invocation. The adapter fails
// the invocation over the unreadable line, as it always did, and hands back
// the terminal's cost beside the error rather than an empty result, which is
// what used to record the money as unknown.
func TestAPricedEndingIsKeptWhenLaterOutputIsMalformed(t *testing.T) {
	t.Parallel()

	stream := `{"type":"system","subtype":"init","session_id":"session-1","model":"claude-opus"}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":3.25,"usage":{"input_tokens":10,"output_tokens":20},"terminal_reason":"completed"}` + "\n" +
		"{not json\n"
	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
	})
	if err == nil || !strings.Contains(err.Error(), "decode stream event") {
		t.Fatalf("Run() error = %v, want the unreadable line reported", err)
	}
	if !result.CostReported || result.CostUSD != 3.25 {
		t.Fatalf("result cost = %v (reported %t), want the terminal's 3.25 kept beside the error", result.CostUSD, result.CostReported)
	}
	if result.SessionID != "session-1" || result.Backend == "" || result.UsageReading == nil {
		t.Fatalf("result = %+v, want the session, backend and usage the stream reported", result)
	}
	if !result.IsError {
		t.Fatal("a result handed back beside an error must not read as a success")
	}
	if result.Process.Stdout != "" {
		t.Fatal("the raw stream must not be returned as a second copy of the events")
	}
}

// The same holds for an event the harness could not record after the
// terminal: the recording failed, the spending did not.
func TestAPricedEndingIsKeptWhenAnEventAfterItCannotBeRecorded(t *testing.T) {
	t.Parallel()

	stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":1.5,"terminal_reason":"completed"}` + "\n" +
		`{"type":"system","subtype":"status","session_id":"session-1"}` + "\n"
	terminated := false
	result, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
		EventSink: func(event execution.Event) error {
			if terminated {
				return errSinkFull
			}
			terminated = event.Type == execution.EventRunCompleted
			return nil
		},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want the event that could not be recorded reported")
	}
	if !result.CostReported || result.CostUSD != 1.5 {
		t.Fatalf("result cost = %v (reported %t), want the terminal's 1.5 kept beside the error", result.CostUSD, result.CostReported)
	}
}

type sinkError string

func (e sinkError) Error() string { return string(e) }

const errSinkFull = sinkError("the event log refused the line")
