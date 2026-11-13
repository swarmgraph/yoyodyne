package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// codexStream is a Codex CLI that writes one stream of events and exits, so a
// review runs through the real Codex adapter and its stream reader.
type codexStream struct{ lines []string }

func (c codexStream) Run(_ context.Context, _ execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	for _, line := range c.lines {
		if observer != nil {
			observer(execution.Output{Stream: execution.StreamStdout, Text: line, Timestamp: time.Now()})
		}
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

// codexReviewStream is a Codex review turn that runs the given command items
// and then gives an approving verdict.
func codexReviewStream(items ...string) codexStream {
	verdict, _ := json.Marshal(map[string]any{"id": "item_9", "type": "agent_message", "text": `{"decision":"approve","approves":"implementation","summary":"read the callers"}`})
	lines := []string{`{"type":"thread.started","thread_id":"codex-review-session"}`, `{"type":"turn.started"}`}
	lines = append(lines, items...)
	return codexStream{lines: append(lines,
		`{"type":"item.completed","item":`+string(verdict)+`}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}`,
	)}
}

// A review served by Codex whose reads ran is judged whether or not the stream
// carried each command's output: a read that reports no size is unmeasured,
// not empty, and the record says how many reads were unmeasured. A Codex review
// whose every read failed is still refused, as one on Claude Code is.
func TestACodexReviewIsNotRefusedForAReadWithNoSize(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		stream     codexStream
		unmeasured int
	}{
		{
			name: "a command completion without its output",
			stream: codexReviewStream(
				`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"git show HEAD","status":"in_progress"}}`,
				`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"git show HEAD","exit_code":0,"status":"completed"}}`,
			),
			unmeasured: 1,
		},
		{
			name: "a command completion with its output",
			stream: codexReviewStream(
				`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"git show HEAD","status":"in_progress"}}`,
				`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"git show HEAD","aggregated_output":"commit abc\n","exit_code":0,"status":"completed"}}`,
			),
		},
		{
			name: "a command completion whose output was empty",
			stream: codexReviewStream(
				`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"git status --short","status":"in_progress"}}`,
				`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"git status --short","aggregated_output":"","exit_code":0,"status":"completed"}}`,
			),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, events, err := reviewWith(t, codex.Backend{Runner: test.stream, Clock: reviewClock{}, ConfigDir: t.TempDir()}, t.TempDir())
			if err != nil || result.Decision != DecisionApprove {
				t.Fatalf("Review() = %#v, %v, want a judged approval", result, err)
			}
			last := events[len(events)-1]
			var recorded struct {
				Attempted  *int `json:"attempted_reads"`
				Answered   *int `json:"answered_reads"`
				Unmeasured *int `json:"unmeasured_reads"`
			}
			if err := json.Unmarshal(last.Payload, &recorded); err != nil {
				t.Fatal(err)
			}
			if last.Type != execution.EventReviewCompleted || recorded.Attempted == nil || *recorded.Attempted != 1 ||
				recorded.Answered == nil || *recorded.Answered != 1 || recorded.Unmeasured == nil || *recorded.Unmeasured != test.unmeasured {
				t.Fatalf("last event = %s %s, want review.completed with 1 read answered and %d unmeasured", last.Type, last.Payload, test.unmeasured)
			}
		})
	}

	failed := codexReviewStream(
		`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"cat missing.go","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"cat missing.go","exit_code":1,"status":"failed"}}`,
	)
	_, events, err := reviewWith(t, codex.Backend{Runner: failed, Clock: reviewClock{}, ConfigDir: t.TempDir()}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "1 read(s) that were refused or failed") {
		t.Fatalf("Review() error = %v, want a Codex review whose only read failed refused", err)
	}
	if last := events[len(events)-1]; last.Type != execution.EventReviewUnread || !strings.Contains(string(last.Payload), `"unmeasured_reads":0`) {
		t.Fatalf("last event = %s %s, want review.unread recording no unmeasured reads", last.Type, last.Payload)
	}
}
