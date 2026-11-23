package claudecode

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// versionAnswering answers the provider's version question itself and passes
// every other command on, so a test counting the invocations a fake runner was
// given still counts only invocations.
type versionAnswering struct {
	inner   execution.ProcessRunner
	version int
}

func (v *versionAnswering) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if slices.Equal(command.Args, []string{"--version"}) {
		v.version++
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "2.1.286 (Claude Code)\n"}, nil
	}
	return v.inner.Run(ctx, command, observer)
}

type keptInvocations struct {
	sent []backendapi.SentInvocation
	fail error
}

func (k *keptInvocations) RecordInvocation(sent backendapi.SentInvocation) error {
	if k.fail != nil {
		return k.fail
	}
	k.sent = append(k.sent, sent)
	return nil
}

// What is recorded for an invocation is what the provider was given: the system
// prompt argument and the standard input, byte for byte, for a fresh and a
// resumed session and for each role, with what the project named in its own part.
func TestWhatIsRecordedForAClaudeCodeInvocationIsWhatTheProviderWasGiven(t *testing.T) {
	t.Parallel()
	named := backendapi.NamedContext{Instructions: []backendapi.ContextFile{{Path: "docs/agent-notes.md"}}}
	for _, role := range []domain.AgentRole{domain.RoleReviewer, domain.RoleDeveloper} {
		for _, session := range []string{"", "session-1"} {
			repository := t.TempDir()
			writeTestFiles(t, repository, map[string]string{"docs/agent-notes.md": "PROJECT_NOTES_BODY\n"})
			kept := &keptInvocations{}
			request := backendapi.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: t.TempDir(), RepositoryRoot: repository,
				Prompt: "do the work\nwith a second line", SystemPrompt: "the role's contract", SessionID: session,
				Model: "claude-test", Recorder: kept,
			}
			if role != domain.RoleDeveloper {
				request.AllowedTools = []string{}
			}
			fake := &fakeRunner{results: claudeCompletedTurn()}
			runner := &versionAnswering{inner: fake}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}, ConfigDir: personalClaudeHome(t), Context: named}).Run(context.Background(), request); err != nil {
				t.Fatalf("%s: %v", role, err)
			}
			if len(kept.sent) != 1 || runner.version != 1 {
				t.Fatalf("%s: %d invocation(s) recorded, %d version question(s) asked, want one of each", role, len(kept.sent), runner.version)
			}
			sent := kept.sent[0]
			var system, user *backendapi.SentMessage
			for index := range sent.Messages {
				switch sent.Messages[index].Role {
				case backendapi.SentSystem:
					system = &sent.Messages[index]
				case backendapi.SentUser:
					user = &sent.Messages[index]
				}
			}
			argument, _ := optionValue(fake.commands[0].Args, "--append-system-prompt")
			if system == nil || system.Text() != argument {
				t.Fatalf("%s: recorded system text %q, the provider was given %q", role, system, argument)
			}
			if user == nil || user.Text() != fake.prompts[0] {
				t.Fatalf("%s: recorded message %q, the provider was given %q", role, user, fake.prompts[0])
			}
			if !strings.Contains(system.Text(), "PROJECT_NOTES_BODY") || !strings.HasPrefix(system.Text(), "the role's contract") {
				t.Fatalf("%s: the recorded system text does not carry the contract and the named file:\n%s", role, system.Text())
			}
			names := []string{}
			for _, part := range system.Parts {
				names = append(names, part.Name)
			}
			want := []string{"system instructions", "skills and instructions the project names"}
			if role == domain.RoleReviewer {
				want = []string{"system instructions", "reviewer tools note", "skills and instructions the project names"}
			}
			if !slices.Equal(names, want) {
				t.Fatalf("%s: system parts = %q, want %q", role, names, want)
			}
			if sent.ProviderVersion != "2.1.286 (Claude Code)" || sent.Model != "claude-test" || sent.Resumed != session {
				t.Fatalf("%s: settings recorded as %+v", role, sent)
			}
			if !strings.HasPrefix(sent.WorkingDirectory, filepath.Dir(request.WorkingDirectory)) {
				t.Fatalf("%s: working directory recorded as %q", role, sent.WorkingDirectory)
			}
		}
	}
}

// An invocation whose record cannot be written is not started.
func TestAClaudeCodeInvocationThatCannotBeRecordedIsNotStarted(t *testing.T) {
	t.Parallel()
	kept := &keptInvocations{fail: errors.New("the disk is full")}
	fake := &fakeRunner{results: claudeCompletedTurn()}
	_, err := (Backend{Runner: &versionAnswering{inner: fake}, Clock: fixedClock{}, ConfigDir: personalClaudeHome(t)}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(), Prompt: "do the work", Recorder: kept,
	})
	if !backendapi.IsInvocationNotRecorded(err) || !strings.Contains(err.Error(), "the disk is full") {
		t.Fatalf("error = %v, want a refusal to start naming the failed write", err)
	}
	if len(fake.commands) != 0 {
		t.Fatalf("the provider was started anyway: %v", fake.commands)
	}
}

// A request that names no recorder is sent exactly as it always was, with no
// extra process started to ask for a version.
func TestARequestWithNoRecorderStartsNothingExtra(t *testing.T) {
	t.Parallel()
	fake := &fakeRunner{results: claudeCompletedTurn()}
	runner := &versionAnswering{inner: fake}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}, ConfigDir: personalClaudeHome(t)}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(), Prompt: "do the work",
	}); err != nil {
		t.Fatal(err)
	}
	if runner.version != 0 {
		t.Fatalf("%d version question(s) asked for an invocation nothing records", runner.version)
	}
}
