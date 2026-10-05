package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// lingeringRunner plays a session that writes its final reply and then stays
// alive on work it backgrounded, the way the process runner reports one: every
// line reaches the observer, Replied is asked after each, the waiting notice is
// given once the reply is in, and the process is ended at its bound as a turn
// that succeeded rather than a stall.
type lingeringRunner struct {
	stdout  []string
	command execution.Command
	// repliedAfter is what Replied answered after each line, in order.
	repliedAfter []bool
}

func (r *lingeringRunner) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.command = command
	repliedAt := time.Date(2026, 9, 28, 18, 58, 33, 0, time.UTC)
	replied := false
	for _, line := range r.stdout {
		observer(execution.Output{Stream: execution.StreamStdout, Text: line, Timestamp: repliedAt})
		answer := command.Replied != nil && command.Replied()
		r.repliedAfter = append(r.repliedAfter, answer)
		replied = replied || answer
	}
	result := execution.ProcessResult{Status: execution.ProcessSucceeded, ExitCode: -1}
	if replied {
		account := execution.AfterReply{RepliedAt: repliedAt, BoundSeconds: int64(command.AfterReplyTimeout / time.Second)}
		command.AfterReplyWaiting(account)
		account.Outcome = execution.AfterReplyEnded
		account.WaitedSeconds = account.BoundSeconds
		result.AfterReply = &account
	}
	return result, nil
}

// A session that writes its final reply and is kept alive by background work
// is read as a turn that ended: the adapter tells the runner the terminal is the
// reply, gives the wait a bound, passes the waiting notice to the caller, and
// records which way the wait ended — and the result is the reply, never a stop
// named as a stall.
func TestRunReadsTheFinalReplyAsTheEndOfTheTurn(t *testing.T) {
	t.Parallel()

	runner := &lingeringRunner{stdout: []string{
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-6.1-sol"}}`,
		`{"id":"7","msg":{"type":"task_complete","last_agent_message":"done; make test and make race are running in the background"}}`,
	}}
	var waiting []execution.AfterReply
	var events []execution.Event
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
		AfterReplyWaiting: func(account execution.AfterReply) { waiting = append(waiting, account) },
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := runner.repliedAfter; len(got) != 2 || got[0] || !got[1] {
		t.Fatalf("Replied after each line = %v, want false for the session line and true once the terminal is in", got)
	}
	if runner.command.AfterReplyTimeout != defaultAfterReplyTimeout {
		t.Fatalf("after-reply bound = %s, want %s", runner.command.AfterReplyTimeout, defaultAfterReplyTimeout)
	}
	if len(waiting) != 1 || !waiting[0].Waiting() {
		t.Fatalf("waiting notices passed to the caller = %+v, want one with no outcome yet", waiting)
	}
	if result.IsError || result.StopReason == string(execution.ProcessStalled) {
		t.Fatalf("Run() result = IsError %v, StopReason %q: a finished turn was read as a stall", result.IsError, result.StopReason)
	}
	if result.Process.Status == execution.ProcessStalled || result.Process.AfterReply == nil || result.Process.AfterReply.Outcome != execution.AfterReplyEnded {
		t.Fatalf("Run() process = %+v, want a turn that succeeded with its background work recorded as ended", result.Process)
	}
	if !strings.HasPrefix(result.FinalText, "done") {
		t.Fatalf("Run() final text = %q, want the reply", result.FinalText)
	}
	var said []string
	for _, event := range events {
		if event.Type != execution.EventProcessOutput {
			continue
		}
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(event.Payload, &payload)
		text := payload.Text
		if strings.Contains(text, "reply written") {
			said = append(said, text)
		}
	}
	if len(said) != 2 || !strings.Contains(said[0], "waiting for background processes") || !strings.Contains(said[1], "were ended") {
		t.Fatalf("the invocation's log says %q, want the wait and then how it ended", said)
	}
}
