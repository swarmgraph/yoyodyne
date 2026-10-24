package claudecode

import (
	"context"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/usage"
)

func runUsageStream(t *testing.T, stream string) backendapi.RunResult {
	t.Helper()
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream + "\n"}}}
	result, _ := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "do the work",
		EventSink:        func(execution.Event) error { return nil },
	})
	return result
}

func TestATerminalHandsOverItsOwnUsageWithReasoningAbsent(t *testing.T) {
	t.Parallel()

	result := runUsageStream(t, `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done",`+
		`"total_cost_usd":12.5,"usage":{"input_tokens":114,"cache_read_input_tokens":7796697,"cache_creation_input_tokens":0,`+
		`"output_tokens":58231,"service_tier":"standard"},"terminal_reason":"end_turn"}`)
	reading := result.UsageReading
	if reading == nil {
		t.Fatal("a terminal that reported usage handed over no reading")
	}
	want := usage.Counters{
		FreshInput:  usage.Measured(114),
		CachedInput: usage.Measured(7796697),
		CacheWrite:  usage.Measured(0),
		Output:      usage.Measured(58231),
	}
	if reading.Counters != want || reading.Running || reading.Tier != "standard" || reading.Session != "session-1" {
		t.Fatalf("reading = %+v", reading)
	}
	if reading.Counters.Reasoning.Measured {
		t.Fatal("reasoning the provider never reported apart was recorded as measured")
	}
	if !result.CostReported || result.CostUSD != 12.5 {
		t.Fatalf("the provider's own cost was lost: %+v", result)
	}
}

func TestATerminalWithNoUsageHandsOverNoReading(t *testing.T) {
	t.Parallel()

	result := runUsageStream(t, `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":1,"terminal_reason":"end_turn"}`)
	if result.UsageReading != nil {
		t.Fatalf("reading = %+v", result.UsageReading)
	}
}
