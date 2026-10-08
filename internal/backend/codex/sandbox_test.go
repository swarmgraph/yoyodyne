package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// These are Git's files, without a subprocess or a second real worktree. Spaces
// and quotes exercise the TOML value the CLI actually receives.
func sandboxRepository(t *testing.T, linked bool) (repository, worktree string) {
	t.Helper()
	return sandboxRepositoryNamed(t, linked, `repository with "quotes"`)
}

func sandboxRepositoryNamed(t *testing.T, linked bool, name string) (repository, worktree string) {
	t.Helper()
	repository = filepath.Join(t.TempDir(), name)
	git := filepath.Join(repository, ".git")
	if err := os.MkdirAll(git, 0o755); err != nil {
		t.Fatal(err)
	}
	if !linked {
		return repository, repository
	}
	administrative := filepath.Join(git, "worktrees", "one")
	worktree = filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(administrative, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		filepath.Join(worktree, ".git"):            "gitdir: " + administrative + "\n",
		filepath.Join(administrative, "commondir"): "../..\n",
	} {
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repository, worktree
}

func sandboxCommand(t *testing.T, repository, worktree, session string, role domain.AgentRole) execution.Command {
	t.Helper()
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"ok"}}`)}}}
	_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID: testRunID, Role: role, WorkingDirectory: worktree,
		RepositoryRoot: repository, Prompt: "do the work", SessionID: session,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner.commands[0]
}

func TestDeveloperSandboxGrantsTheDeclaredCacheAndScratchOnEveryTurn(t *testing.T) {
	t.Parallel()
	for _, linked := range []bool{false, true} {
		repository, worktree := sandboxRepository(t, linked)
		scratch, err := execution.PrepareScratchDirectory(repository, worktree, testRunID)
		if err != nil {
			t.Fatal(err)
		}
		git, err := filepath.EvalSymlinks(filepath.Join(repository, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		cache := filepath.Join(git, "yoyodyne", "go-build")
		for _, session := range []string{"", "session-one"} {
			command := sandboxCommand(t, repository, worktree, session, domain.RoleDeveloper)
			if got, want := profileFilesystem(t, command.Args), developerFilesystem(worktree, cache, scratch); !reflect.DeepEqual(got, want) {
				t.Fatalf("developer file system = %q, want only the worktree, the declared cache and scratch, and the temporary directories: %q", got, want)
			}
			if !hasEnvironment(command.Env, "GOCACHE="+cache) {
				t.Fatalf("the sandbox grants %q but GOCACHE names another path", cache)
			}
			if err := recordedContract(t).misplacedOption(command.Args); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(command.Args, " ")
			if session != "" && strings.Index(joined, ".filesystem=") > strings.Index(joined, " resume ") {
				t.Fatal("writable directory policy appears after resume")
			}
			for _, forbidden := range []string{"danger-full-access", "--dangerously-bypass", "--approve-for-me", "--add-dir", "--sandbox"} {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("unconfined or unsupported option %q in %q", forbidden, command.Args)
				}
			}
		}
	}
}

func TestReadOnlyRolesNeverReceiveDeveloperDirectoryGrants(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	for _, role := range domain.Roles() {
		if backendapi.PostureFor(role) != backendapi.PostureReadOnly {
			continue
		}
		for _, session := range []string{"", "session-one"} {
			command := sandboxCommand(t, repository, worktree, session, role)
			if profileArgument(t, command.Args) != profileReadOnly || strings.Contains(strings.Join(command.Args, " "), `="write"`) {
				t.Fatalf("read-only role %s received developer access: %q", role, command.Args)
			}
		}
	}
}

func TestDeveloperSandboxRefusesAnEscapingWorktreeBeforeLaunchOrResume(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+t.TempDir()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"", "session-one"} {
		runner := &fakeRunner{}
		_, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
			RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
			RepositoryRoot: repository, Prompt: "do the work", SessionID: session,
		})
		if err == nil || !strings.Contains(err.Error(), "not inside the repository") || len(runner.commands) != 0 {
			t.Fatalf("Run() = %v, commands = %v, want a refusal before invocation", err, runner.commands)
		}
	}
}
