package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
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
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.99.0\n"}, nil
	}
	return v.inner.Run(ctx, command, observer)
}

type keptInvocations struct {
	sent []backend.SentInvocation
	fail error
}

func (k *keptInvocations) RecordInvocation(sent backend.SentInvocation) error {
	if k.fail != nil {
		return k.fail
	}
	k.sent = append(k.sent, sent)
	return nil
}

// What is recorded for a Codex invocation is the one message the provider was
// given, byte for byte, for each role and for a fresh and a resumed session.
func TestWhatIsRecordedForACodexInvocationIsWhatTheProviderWasGiven(t *testing.T) {
	t.Parallel()
	named := backend.NamedContext{Instructions: []backend.ContextFile{{Path: "docs/agent-notes.md"}}}
	for _, role := range []domain.AgentRole{domain.RoleReviewer, domain.RoleDeveloper} {
		for _, session := range []string{"", "session-1"} {
			repository, worktree := sandboxRepository(t, false)
			writeNamedFile(t, repository, "docs/agent-notes.md", "PROJECT_NOTES_BODY\n")
			kept := &keptInvocations{}
			fake := &fakeRunner{results: completedTurn()}
			runner := &versionAnswering{inner: fake}
			_, err := (Backend{Runner: runner, ConfigDir: personalHome(t), Context: named}).Run(context.Background(), backend.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: worktree, RepositoryRoot: repository,
				Prompt: "do the work\nwith a second line", SystemPrompt: "the role's contract", Model: "gpt-6-astra",
				SessionID: session, Recorder: kept,
			})
			if err != nil {
				t.Fatalf("%s: %v", role, err)
			}
			if len(kept.sent) != 1 || len(kept.sent[0].Messages) != 1 {
				t.Fatalf("%s: recorded %+v", role, kept.sent)
			}
			message := kept.sent[0].Messages[0]
			if message.Text() != fake.prompts[0] {
				t.Fatalf("%s: recorded %q, the provider was given %q", role, message.Text(), fake.prompts[0])
			}
			names := []string{}
			for _, part := range message.Parts {
				names = append(names, part.Name)
			}
			want := []string{"system instructions", "skills and instructions the project names", "first message"}
			if role == domain.RoleReviewer {
				want = append([]string{"read-only inspection note"}, want...)
			}
			if !slices.Equal(names, want) {
				t.Fatalf("%s: parts = %q, want %q", role, names, want)
			}
			if kept.sent[0].ProviderVersion != "codex-cli 0.99.0" || kept.sent[0].Resumed != session || !strings.Contains(message.Text(), "PROJECT_NOTES_BODY") {
				t.Fatalf("%s: settings recorded as %+v", role, kept.sent[0])
			}
		}
	}
}

// An invocation whose record cannot be written is not started.
func TestACodexInvocationThatCannotBeRecordedIsNotStarted(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, false)
	fake := &fakeRunner{results: completedTurn()}
	_, err := (Backend{Runner: &versionAnswering{inner: fake}, ConfigDir: personalHome(t)}).Run(context.Background(), backend.RunRequest{
		RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: worktree, RepositoryRoot: repository,
		Prompt: "do the work", Model: "gpt-6-astra", Recorder: &keptInvocations{fail: errors.New("the disk is full")},
	})
	if !backend.IsInvocationNotRecorded(err) || !strings.Contains(err.Error(), "the disk is full") {
		t.Fatalf("error = %v, want a refusal to start naming the failed write", err)
	}
	if len(fake.commands) != 0 {
		t.Fatalf("the provider was started anyway: %v", fake.commands)
	}
}

func writeNamedFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
