package pipelinetests

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// unreadableReply is a first reply nothing can read as a verdict, which is what
// sends a review to its second asking.
const unreadableReply = "Sure! Here is my review."

// scriptedCLI is a provider CLI that answers each launch with what the test
// scripted for it, so a real adapter reads the CLI's refusal exactly as it would
// read the CLI.
type scriptedCLI struct {
	results  []execution.ProcessResult
	launches int
}

func (s *scriptedCLI) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	index := s.launches
	s.launches++
	if command.Stdin != nil {
		_, _ = io.ReadAll(command.Stdin)
	}
	if index >= len(s.results) {
		return execution.ProcessResult{}, errors.New("unexpected process call")
	}
	result := s.results[index]
	for _, stream := range []struct {
		name execution.Stream
		text string
	}{{execution.StreamStdout, result.Stdout}, {execution.StreamStderr, result.Stderr}} {
		for _, line := range strings.Split(strings.TrimSuffix(stream.text, "\n"), "\n") {
			if line != "" && observer != nil {
				observer(execution.Output{Stream: stream.name, Text: line, Timestamp: time.Now()})
			}
		}
	}
	return result, nil
}

// resumeRefusingProvider serves the developer and every fresh review from the
// fake provider, and hands a review that asks to resume a session to a real
// provider adapter whose CLI refuses it.
type resumeRefusingProvider struct {
	*orchestratortest.Backend
	adapter backend.Backend
	resumes []backend.RunRequest
}

func (p *resumeRefusingProvider) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	if request.Role == domain.RoleReviewer && request.SessionID != "" {
		p.resumes = append(p.resumes, request)
		return p.adapter.Run(ctx, request)
	}
	return p.Backend.Run(ctx, request)
}

func writeFeature(request backend.RunRequest) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
}

// A second asking whose resume of the reviewer's session each provider adapter
// refuses is made again in a fresh reviewer session, with the same change and
// the same instructions, rather than ending the run, and the verdict it gives is
// acted on exactly as the resumed one's would have been. It costs no repair
// attempt and is the run's one second asking.
func TestPipelineAsksTheSecondTimeInAFreshSessionWhenTheResumeIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		adapter func(*scriptedCLI) backend.Backend
		cli     []execution.ProcessResult
	}{
		{
			name:    "Claude Code",
			adapter: func(cli *scriptedCLI) backend.Backend { return claudecode.Backend{Runner: cli} },
			cli: []execution.ProcessResult{{
				Status: execution.ProcessFailed, ExitCode: 1,
				Stderr: "No conversation found with session ID: reviewer-session\n",
			}},
		},
		{
			name:    "Codex that cannot find the session",
			adapter: func(cli *scriptedCLI) backend.Backend { return codex.Backend{Runner: cli} },
			cli: []execution.ProcessResult{
				{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Error: no rollout found for thread id reviewer-session\n"},
				{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.160.0\n"},
			},
		},
		{
			name:    "Codex that starts a new session in its place",
			adapter: func(cli *scriptedCLI) backend.Backend { return codex.Backend{Runner: cli} },
			cli: []execution.ProcessResult{{
				Status: execution.ProcessSucceeded,
				Stdout: `{"id":"0","msg":{"type":"session_configured","session_id":"a-new-session"}}` + "\n" +
					`{"id":"1","msg":{"type":"task_complete","last_agent_message":"{\"decision\":\"approve\",\"approves\":\"implementation\",\"summary\":\"unseen\"}"}}` + "\n",
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			cli := &scriptedCLI{results: test.cli}
			provider := &resumeRefusingProvider{
				Backend: orchestratortest.RoleBackend(writeFeature, unreadableReply, testharness.ApproveVerdict),
				adapter: test.adapter(cli),
			}
			pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v, want the refused resume answered by a fresh second asking", err)
			}
			if outcome.Integration == nil || !tracker.Closed || outcome.ReviewDecision != review.DecisionApprove {
				t.Fatalf("the fresh second asking did not integrate: %#v, closed = %t", outcome, tracker.Closed)
			}
			if len(provider.resumes) != 1 || provider.resumes[0].SessionID != "reviewer-session" || cli.launches == 0 {
				t.Fatalf("resumes = %d (launches %d), want exactly one resume of the reviewer's session through the adapter", len(provider.resumes), cli.launches)
			}
			fresh := provider.RequestsForRole(domain.RoleReviewer)
			if len(fresh) != 2 || fresh[1].SessionID != "" {
				t.Fatalf("fresh reviews = %d, want the first asking and one fresh second asking", len(fresh))
			}
			// The fresh asking is the same review: the same change and the same
			// instructions, with what was wrong with the first reply said after them.
			if fresh[1].SystemPrompt != fresh[0].SystemPrompt || !strings.HasPrefix(fresh[1].Prompt, fresh[0].Prompt) ||
				!strings.Contains(fresh[1].Prompt, "Write it bare") {
				t.Fatalf("the fresh second asking is not the first asking's evidence with the problem after it:\n%s", fresh[1].Prompt)
			}
			if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 || outcome.RepairAttempts != 0 {
				t.Fatalf("developer invocations = %d, repair attempts = %d; the second asking costs neither", runs, outcome.RepairAttempts)
			}
			events, err := store.LoadEvents(outcome.RunID)
			if err != nil {
				t.Fatalf("LoadEvents() error = %v", err)
			}
			said := false
			for _, event := range events {
				if event.Type == execution.EventReviewStarted && strings.Contains(string(event.Payload), `"fresh_session":"the provider did not resume the reviewer's session reviewer-session`) {
					said = true
				}
			}
			if !said {
				t.Fatal("the run's record does not say why the second asking was made in a fresh session")
			}
		})
	}
}

// A second asking that would be made on another provider than the first reply
// cannot resume the first reply's session, so it is made in a fresh one from
// the start rather than asked of a provider that does not hold it.
func TestPipelineAsksTheSecondTimeInAFreshSessionWhenTheProviderChanged(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(writeFeature, unreadableReply, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	// The reviewer's provider changes once the first reply is in, which is the
	// configuration a second asking reads when it is made.
	agents := pipeline.Config.Agents
	respond := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := respond(request)
		if request.Role == domain.RoleReviewer {
			reviewer := agents["reviewer"]
			reviewer.Backend = domain.BackendCodex
			agents["reviewer"] = config.AgentConfig(reviewer)
		}
		return result, err
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a fresh second asking", err)
	}
	if outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the fresh second asking did not integrate: %#v, closed = %t", outcome, tracker.Closed)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 || reviews[1].SessionID != "" || !strings.HasPrefix(reviews[1].Prompt, reviews[0].Prompt) {
		t.Fatalf("reviews = %d, want one fresh second asking carrying the first asking's evidence", len(reviews))
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	said := false
	for _, event := range events {
		if event.Type == execution.EventReviewStarted && strings.Contains(string(event.Payload), "made on provider claude-code and this asking is made on codex") {
			said = true
		}
	}
	if !said {
		t.Fatal("the run's record does not say the provider changed between the two asks")
	}
}

// The fresh second asking is still the one second asking: a reply it cannot
// read either, or a failure of the fresh asking itself, ends the run, saying in
// plain words on the run and on the item what happened.
func TestPipelineEndsWhenTheFreshSecondAskingFailsToo(t *testing.T) {
	t.Parallel()

	refusal := []execution.ProcessResult{{
		Status: execution.ProcessFailed, ExitCode: 1,
		Stderr: "No conversation found with session ID: reviewer-session\n",
	}}
	for _, test := range []struct {
		name  string
		fresh func(*orchestratortest.Backend)
		class runstate.StopClass
		plain []string
	}{
		{
			name:  "a second unreadable reply",
			fresh: func(*orchestratortest.Backend) {},
			class: runstate.StopReviewAccount,
			plain: []string{"the reviewer was asked twice and neither reply could be accepted as a verdict",
				"asked in a fresh reviewer session because the provider did not resume the reviewer's session", "decode review verdict"},
		},
		{
			name: "a fresh asking the provider fails",
			fresh: func(provider *orchestratortest.Backend) {
				respond := provider.Respond
				provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
					if request.Role == domain.RoleReviewer && len(provider.RequestsForRole(domain.RoleReviewer)) > 1 {
						return backend.RunResult{Backend: domain.BackendClaudeCode, IsError: true, StopReason: "invalid_request", FinalText: "the request was refused",
							Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}, LastEvent: request.LastSequence}, nil
					}
					return respond(request)
				}
			},
			class: runstate.StopProvider,
			plain: []string{"the reviewer's first reply could not be accepted as a verdict, and the second ask",
				"asked in a fresh reviewer session because the provider did not resume the reviewer's session", "failed too", "the request was refused"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			fake := orchestratortest.RoleBackend(writeFeature, unreadableReply)
			test.fresh(fake)
			provider := &resumeRefusingProvider{Backend: fake, adapter: claudecode.Backend{Runner: &scriptedCLI{results: refusal}}}
			pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			testharness.AssertSavedStopClass(t, store, outcome.RunID, test.class)
			if err == nil || outcome.Integration != nil || tracker.Closed {
				t.Fatalf("Run() error = %v, integration %#v, closed %t; want the run ended unintegrated", err, outcome.Integration, tracker.Closed)
			}
			if reviews := len(fake.RequestsForRole(domain.RoleReviewer)); reviews != 2 || len(provider.resumes) != 1 {
				t.Fatalf("fresh reviews = %d, resumes = %d; want one resume and one fresh second asking, and nothing more", reviews, len(provider.resumes))
			}
			state, loadErr := store.Load(outcome.RunID)
			if loadErr != nil {
				t.Fatalf("Load() error = %v", loadErr)
			}
			for _, want := range test.plain {
				if !strings.Contains(err.Error(), want) || !strings.Contains(state.Failure, want) || !strings.Contains(tracker.Notes, want) {
					t.Fatalf("want %q on the error, the run, and the item:\nerror: %v\nrun: %s\nitem: %s", want, err, state.Failure, tracker.Notes)
				}
			}
		})
	}
}
