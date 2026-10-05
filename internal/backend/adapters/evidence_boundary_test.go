package adapters

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

type boundaryRunner struct {
	command execution.Command
	during  func(execution.Command)
}

func (r *boundaryRunner) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.command = command
	if r.during != nil {
		r.during(command)
	}
	for _, line := range []string{`{"type":"thread.started","thread_id":"review-session"}`, `{"type":"item.completed","item":{"type":"agent_message","text":"Review complete."}}`, `{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`} {
		observer(execution.Output{Stream: execution.StreamStdout, Text: line})
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

// A declaration can select an executable and dialect, but cannot widen the
// compiled adapter's read-only launch policy, including on resumed turns.
func TestDeclaredCodexProviderKeepsReadOnlyPolicyThroughFactory(t *testing.T) {
	t.Parallel()
	const provider domain.Backend = "declared-codex"
	registry, err := backend.NewRegistry(map[domain.Backend]backend.ProviderPlugin{provider: {Adapter: domain.BackendCodex, Binary: "codex-proxy", Roles: []domain.AgentRole{domain.RoleReviewer}, Postures: []backend.Posture{backend.PostureReadOnly}, Dialect: backend.DialectSpec{Rules: []backend.DialectRule{{Answer: backend.AnswerRetrying, Type: "retry"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := registry.Lookup(provider)
	if !ok || !descriptor.SupportsRole(domain.RoleReviewer) || !descriptor.SupportsPosture(backend.PostureReadOnly) {
		t.Fatalf("descriptor = %#v, found=%t", descriptor, ok)
	}
	for _, test := range []struct{ session, effort string }{
		{"", ""},
		{"previous-session", ""},
		{"", "high"},
		{"previous-session", "high"},
	} {
		t.Run("session="+test.session+", effort="+test.effort, func(t *testing.T) {
			repo := t.TempDir()
			canonical, err := filepath.EvalSymlinks(repo)
			if err != nil {
				t.Fatal(err)
			}
			runner := &boundaryRunner{during: func(command execution.Command) {
				if command.Dir == canonical {
					t.Fatal("provider launches in the inspected repository")
				}
				entries, err := os.ReadDir(command.Dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("launch directory is not empty: %v %v", entries, err)
				}
			}}
			adapter, built := For(descriptor, provider, runner, t.TempDir())
			if !built || adapter == nil {
				t.Fatal("adapter not built")
			}
			result, err := adapter.Run(context.Background(), backend.RunRequest{RunID: "run-boundary", Role: domain.RoleReviewer, Model: "gpt-6.1-sol", Effort: test.effort, WorkingDirectory: repo, Prompt: "Review the repository.", SessionID: test.session})
			if err != nil {
				t.Fatal(err)
			}
			command := runner.command
			if command.Name != "codex-proxy" {
				t.Fatalf("binary=%q", command.Name)
			}
			args := strings.Join(command.Args, "\n")
			for _, want := range []string{"--sandbox\nread-only", "--ignore-user-config", "--ignore-rules", "--strict-config", "approval_policy=\"never\"", "web_search=\"disabled\"", "--cd\n" + command.Dir, "--disable\nplugins", "--disable\nhooks", "--disable\ncomputer_use"} {
				if !strings.Contains(args, want) {
					t.Errorf("launch lacks %q: %v", want, command.Args)
				}
			}
			wantOverrides := 0
			wantDescription := "not reported, from the Codex configuration"
			if test.effort != "" {
				wantOverrides = 1
				wantDescription = test.effort + ", from the agent"
				override := "--config\nmodel_reasoning_effort=\"" + test.effort + "\""
				if !strings.Contains(args, override) {
					t.Fatalf("configured effort lost: %v", command.Args)
				}
				if test.session != "" && strings.Index(args, override) > strings.Index(args, "resume\n") {
					t.Fatalf("effort override must precede resume: %v", command.Args)
				}
			}
			if count := strings.Count(args, "model_reasoning_effort="); count != wantOverrides {
				t.Fatalf("effort override occurs %d times, want %d: %v", count, wantOverrides, command.Args)
			}
			if result.EffortDescription != wantDescription {
				t.Fatalf("effort description = %q, want %q", result.EffortDescription, wantDescription)
			}
			if test.session != "" && !strings.Contains(args, "resume\n"+test.session) {
				t.Fatalf("native resume lost: %v", command.Args)
			}
			prompt, readErr := io.ReadAll(command.Stdin)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(prompt), canonical) {
				t.Fatalf("prompt does not identify inspected repository: %q", string(prompt))
			}
			if _, err := os.Stat(command.Dir); !os.IsNotExist(err) {
				t.Fatalf("launch directory retained: %v", err)
			}
		})
	}
}
