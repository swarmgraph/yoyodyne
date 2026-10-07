package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func writeCLI(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), mode); err != nil {
		t.Fatal(err)
	}
}

const codexCLI = `case "$1" in
  --version) printf 'codex-cli fixture\n';;
  login) printf 'Logged in using ChatGPT\n';;
  exec)
    while IFS= read -r line; do :; done
    printf '%s\n' '{"type":"thread.started","thread_id":"fixture-session"}' '{"type":"item.completed","item":{"type":"agent_message","text":"Fixture reply."}}' '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}';;
  *) exit 2;;
esac
`

type recordingRunner struct {
	commands []execution.Command
}

func (r *recordingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, command)
	return (execution.OSProcessRunner{}).Run(ctx, command, observer)
}

// These are controlled CLI processes, not live provider or account checks.
func TestAvailabilityAndLaunchUseTheSameExecutable(t *testing.T) {
	for _, setup := range []string{"desktop bundle override", "PATH installation"} {
		t.Run(setup, func(t *testing.T) {
			root := t.TempDir()
			bundle := filepath.Join(root, "Desktop App.app", "Contents", "Resources", "codex-cli", "bin", "codex")
			pathCLI := filepath.Join(root, "terminal bin", "codex")
			writeCLI(t, bundle, codexCLI, 0o755)
			writeCLI(t, pathCLI, strings.ReplaceAll(codexCLI, "Fixture reply.", "Wrong installation."), 0o755)
			t.Setenv("PATH", filepath.Dir(pathCLI))
			descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
			want := bundle
			if setup == "desktop bundle override" {
				descriptor.Binary = bundle
			} else {
				want = pathCLI
			}
			runner := &recordingRunner{}
			provider, _ := For(descriptor, descriptor.ID, runner, "")
			availability, err := provider.CheckAvailability(context.Background())
			if err != nil || !availability.Installed || !availability.Authenticated {
				t.Fatalf("availability = %+v, %v", availability, err)
			}
			// A different PATH between checking and launching must not select a
			// different installation. Read-only launch also changes directory.
			t.Setenv("PATH", filepath.Join(root, "empty"))
			for _, session := range []string{"", "fixture-session"} {
				result, err := provider.Run(context.Background(), backend.RunRequest{RunID: "run-fixture", Role: domain.RoleArchitect, Model: "gpt-6-astra", WorkingDirectory: t.TempDir(), Prompt: "ping", SessionID: session})
				if err != nil || result.IsError || result.FinalText == "" {
					t.Fatalf("launch = %+v, %v", result, err)
				}
			}
			for _, command := range runner.commands {
				if command.Name != want {
					t.Fatalf("used %q, want checked executable %q", command.Name, want)
				}
			}
			if len(runner.commands) != 4 {
				t.Fatalf("commands = %d, want version, login, start and resume", len(runner.commands))
			}
		})
	}
}

func TestExecutableDiscoveryFailuresDoNotBecomeAuthenticationFailures(t *testing.T) {
	for _, setup := range []string{"missing PATH", "missing override", "non-executable override", "non-executable PATH", "directory override"} {
		t.Run(setup, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PATH", root)
			descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
			want := "not found"
			switch setup {
			case "missing override":
				descriptor.Binary = filepath.Join(root, "missing codex")
				writeCLI(t, filepath.Join(root, "codex"), codexCLI, 0o755)
			case "non-executable override", "non-executable PATH":
				path := filepath.Join(root, "codex")
				writeCLI(t, path, codexCLI, 0o644)
				if setup == "non-executable override" {
					descriptor.Binary = path
				}
				want = "not executable"
			case "directory override":
				descriptor.Binary = root
				want = "not executable"
			}
			runner := &recordingRunner{}
			provider, _ := For(descriptor, descriptor.ID, runner, "")
			availability, err := provider.CheckAvailability(context.Background())
			if err != nil || availability.Installed || !strings.Contains(availability.Missing, want) || !strings.Contains(availability.Missing, "providers.codex.binary") {
				t.Fatalf("availability = %+v, %v", availability, err)
			}
			_, err = provider.Run(context.Background(), backend.RunRequest{RunID: "run-fixture", Role: domain.RoleDeveloper, Model: "gpt-6-astra", WorkingDirectory: root, Prompt: "ping"})
			if !errors.Is(err, execution.ErrProcessNotStarted) || !strings.Contains(err.Error(), want) || len(runner.commands) != 0 {
				t.Fatalf("launch refusal = %v, commands = %v", err, runner.commands)
			}
		})
	}
}

func TestConfiguredExecutableAuthenticationRemainsASeparateCheck(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, adapter := range []domain.Backend{domain.BackendCodex, domain.BackendClaudeCode} {
		t.Run(string(adapter), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CLI with spaces")
			writeCLI(t, path, "case \"$1\" in --version) printf 'fixture version\\n';; *) printf '%s\\n' '{\"loggedIn\":false,\"authMethod\":\"none\"}'; exit 1;; esac\n", 0o755)
			descriptor, _ := backend.BuiltInDescriptor(adapter)
			descriptor.Binary = path
			runner := &recordingRunner{}
			provider, _ := For(descriptor, adapter, runner, "")
			availability, err := provider.CheckAvailability(context.Background())
			if err != nil || !availability.Installed || availability.Authenticated || availability.Missing != "" || len(runner.commands) != 2 {
				t.Fatalf("authentication = %+v, %v, commands=%v", availability, err, runner.commands)
			}
		})
	}
}

func TestFailedDiscoveryCanBeRepairedWithoutSelectingAnotherBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installed later")
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	descriptor.Binary = path
	provider, _ := For(descriptor, descriptor.ID, execution.OSProcessRunner{}, "")
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil || availability.Installed {
		t.Fatalf("before installation = %+v, %v", availability, err)
	}
	writeCLI(t, path, codexCLI, 0o755)
	availability, err = provider.CheckAvailability(context.Background())
	if err != nil || !availability.Authenticated {
		t.Fatalf("after installation = %+v, %v", availability, err)
	}
}
