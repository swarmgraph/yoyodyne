package review

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// scriptedProcess is a provider CLI that answers each launch with what the test
// scripted for it, line by line on the stream it names, so a real adapter reads
// a refused resume exactly as it would read the CLI's.
type scriptedProcess struct {
	results  []execution.ProcessResult
	commands []execution.Command
}

func (s *scriptedProcess) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	index := len(s.commands)
	s.commands = append(s.commands, command)
	if command.Stdin != nil {
		_, _ = io.ReadAll(command.Stdin)
	}
	if index >= len(s.results) {
		return execution.ProcessResult{}, errors.New("unexpected process call")
	}
	result := s.results[index]
	if observer != nil {
		for _, stream := range []struct {
			name execution.Stream
			text string
		}{{execution.StreamStdout, result.Stdout}, {execution.StreamStderr, result.Stderr}} {
			for _, line := range strings.Split(strings.TrimSuffix(stream.text, "\n"), "\n") {
				if line != "" {
					observer(execution.Output{Stream: stream.name, Text: line, Timestamp: time.Now()})
				}
			}
		}
	}
	return result, nil
}

// A second asking whose resume each provider adapter refuses is reported as a
// refused resume, with nothing on it the caller would read as a reason to wait
// or to relaunch, so the caller makes the second asking in a fresh session
// rather than ending the run. The refusals are the shapes each CLI gives for a
// session it does not hold: Claude Code exits naming the session it could not
// find; Codex either exits the same way or starts a new session in place of the
// one asked for.
func TestARefusedResumeOnEachProviderAdapterIsReportedAsOne(t *testing.T) {
	t.Parallel()

	const session = "review-session-gone"
	for _, test := range []struct {
		name    string
		backend func(*scriptedProcess) Backend
		process []execution.ProcessResult
	}{
		{
			name:    "Claude Code that cannot find the session",
			backend: func(runner *scriptedProcess) Backend { return claudecode.Backend{Runner: runner} },
			process: []execution.ProcessResult{{
				Status: execution.ProcessFailed, ExitCode: 1,
				Stderr: "No conversation found with session ID: " + session + "\n",
			}},
		},
		{
			name:    "Codex that cannot find the session",
			backend: func(runner *scriptedProcess) Backend { return codex.Backend{Runner: runner} },
			process: []execution.ProcessResult{
				{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Error: no rollout found for thread id " + session + "\n"},
				{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.160.0\n"},
			},
		},
		{
			name:    "Codex that starts a new session in its place",
			backend: func(runner *scriptedProcess) Backend { return codex.Backend{Runner: runner} },
			process: []execution.ProcessResult{{
				Status: execution.ProcessSucceeded,
				Stdout: `{"id":"0","msg":{"type":"session_configured","session_id":"a-new-session"}}` + "\n" +
					`{"id":"1","msg":{"type":"task_complete","last_agent_message":"{\"decision\":\"approve\",\"approves\":\"implementation\",\"summary\":\"fine\"}"}}` + "\n",
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &scriptedProcess{results: test.process}
			request := newRequest(nil)
			request.WorktreePath = t.TempDir()
			request.Changes = gitworktree.ChangeDiff{Patch: "diff --git a/runner.go b/runner.go\n+added\n"}
			request.Reask = &Reask{SessionID: session, Problem: "it is not one JSON object in the verdict's schema"}

			result, err := (Reviewer{Backend: test.backend(runner), Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request)
			var refused ResumeRefusedError
			if !errors.As(err, &refused) || refused.SessionID != session {
				t.Fatalf("Review() error = %v, want the resume of %s reported as refused", err, session)
			}
			if result.Decision.Valid() {
				t.Fatalf("a verdict from a session that never saw the evidence was read: %q", result.Decision)
			}
			if result.UsageLimit != nil || result.ServerOverload != nil || result.TransientFailure != nil || result.ProviderOutage != nil {
				t.Fatalf("a refused resume was read as a reason to wait or relaunch: %#v", result)
			}
			if len(runner.commands) == 0 || !strings.Contains(strings.Join(runner.commands[0].Args, " "), session) {
				t.Fatalf("the adapter was not asked to resume %s: %v", session, runner.commands)
			}
		})
	}
}

// A second asking that has been told its session cannot be resumed, or whose
// first reply was made on another provider or account, is made in a fresh
// session with the change given whole and the problem said after it, and the
// run's record says why.
func TestASecondAskingThatCannotResumeIsMadeFreshWithTheEvidence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		reask Reask
		want  string
	}{
		{name: "a refused resume", reask: Reask{FreshBecause: "the provider did not resume it"}, want: "the provider did not resume it"},
		{name: "another provider", reask: Reask{Backend: "codex"}, want: "made on provider codex"},
		{name: "another account", reask: Reask{AccountAlias: "first"}, want: `under account \"first\"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var events []execution.Event
			provider := &fakeBackend{finalText: `{"decision":"approve","approves":"implementation","summary":"fine"}`}
			request := newRequest(func(event execution.Event) error {
				events = append(events, event)
				return nil
			})
			request.Changes = gitworktree.ChangeDiff{Patch: "diff --git a/runner.go b/runner.go\n+added\n"}
			request.Spend.Backend = "claude-code"
			request.AccountAlias = "second"
			reask := test.reask
			reask.SessionID, reask.Problem = "review-session", "the reply was unreadable"
			if reask.Backend == "" {
				reask.Backend = "claude-code"
			}
			if reask.AccountAlias == "" {
				reask.AccountAlias = "second"
			}
			request.Reask = &reask

			result, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request)
			if err != nil || result.Decision != DecisionApprove {
				t.Fatalf("Review() = %#v, %v", result, err)
			}
			if provider.request.SessionID != "" {
				t.Fatalf("second asking resumed session %q, want a fresh one", provider.request.SessionID)
			}
			evidence := strings.Index(provider.request.Prompt, "diff --git a/runner.go")
			said := strings.Index(provider.request.Prompt, "the reply was unreadable")
			if evidence < 0 || said < evidence {
				t.Fatalf("a fresh second asking should give the evidence and then the problem:\n%s", provider.request.Prompt)
			}
			if len(events) == 0 || !strings.Contains(string(events[0].Payload), `"fresh_session":`) ||
				!strings.Contains(string(events[0].Payload), test.want) {
				t.Fatalf("review.started does not say why the session was fresh: %v", events)
			}
		})
	}
}
