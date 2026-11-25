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

func sandboxCommand(t *testing.T, repository, worktree, scratch, session string, role domain.AgentRole) execution.Command {
	t.Helper()
	if scratch == "" && role == domain.RoleDeveloper {
		scratch = testScratch
	}
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"ok"}}`)}}}
	_, err := (Backend{StateRoot: testState, Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID: testRunID, Role: role, WorkingDirectory: worktree,
		RepositoryRoot: repository, ScratchDirectory: scratch, Prompt: "do the work", SessionID: session,
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
		state := t.TempDir()
		scratch, err := execution.PrepareScratchDirectory(state, testRunID)
		if err != nil {
			t.Fatal(err)
		}
		git, err := filepath.EvalSymlinks(filepath.Join(repository, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		cache := filepath.Join(git, "yoyodyne", "go-build")
		for _, session := range []string{"", "session-one"} {
			command := sandboxCommand(t, repository, worktree, scratch, session, domain.RoleDeveloper)
			got := profileFilesystem(t, command.Args)
			want := developerFilesystem(worktree, cache, scratch)
			want[servicesBeside(scratch)] = "deny"
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("developer file system = %q, want the services folder denied and only the worktree, the declared cache and scratch, and the temporary directories: %q", got, want)
			}
			// The scratch folder is the one place in the state directory the run
			// may write: nothing else there, nor the state directory itself, is a
			// writable root.
			resolvedState, _ := filepath.EvalSymlinks(state)
			for root, access := range got {
				if access != "write" || root == scratch {
					continue
				}
				if relative, err := filepath.Rel(resolvedState, root); err == nil && !strings.HasPrefix(relative, "..") {
					t.Fatalf("the developer may write %q in the state directory beside its own scratch %q", root, scratch)
				}
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
			command := sandboxCommand(t, repository, worktree, "", session, role)
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
			RunID: testRunID, Role: domain.RoleDeveloper, ScratchDirectory: testScratch, WorkingDirectory: worktree,
			RepositoryRoot: repository, Prompt: "do the work", SessionID: session,
		})
		if err == nil || !strings.Contains(err.Error(), "not inside the repository") || len(runner.commands) != 0 {
			t.Fatalf("Run() = %v, commands = %v, want a refusal before invocation", err, runner.commands)
		}
	}
}

// A scratch directory that is not this run's own folder in the state directory
// -- another run's, the state directory itself, the scratch folders above them,
// a link to one -- is refused before Codex starts, rather than granted.
func TestDeveloperSandboxRefusesAnyScratchButTheRunsOwnFolder(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	state := t.TempDir()
	if _, err := execution.PrepareScratchDirectory(state, testRunID); err != nil {
		t.Fatal(err)
	}
	other, err := execution.PrepareScratchDirectory(state, "run-another")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "scratch", testRunID)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	for name, scratch := range map[string]string{
		"another run's folder": other,
		"the state directory":  resolved,
		"the scratch folders":  filepath.Join(resolved, "scratch"),
		"a link to the state":  link,
	} {
		for _, session := range []string{"", "session-one"} {
			runner := &fakeRunner{}
			_, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
				RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
				RepositoryRoot: repository, ScratchDirectory: scratch, Prompt: "do the work", SessionID: session,
			})
			if err == nil || len(runner.commands) != 0 {
				t.Fatalf("%s: Run() = %v, commands = %v, want a refusal before invocation", name, err, runner.commands)
			}
		}
	}
}

// servicesBeside is the services folder of the state directory a run's scratch
// folder was cut in.
func servicesBeside(scratch string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(scratch)), "services")
}

// A developer is refused the services folder on every turn, under its own
// profile or held read-only by its role definition, and the network is off under
// both, which refuses connecting to a socket file in it.
func TestEveryCodexDeveloperIsRefusedTheServicesFolder(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	scratch, err := execution.PrepareScratchDirectory(t.TempDir(), testRunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, posture := range []backendapi.Posture{backendapi.PostureWorktreeWrite, backendapi.PostureReadOnly} {
		for _, session := range []string{"", "session-one"} {
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded,
				Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"ok"}}`)}}}
			request := backendapi.RunRequest{Model: "gpt-6.1-sol", RunID: testRunID, Role: domain.RoleDeveloper, Posture: posture,
				WorkingDirectory: worktree, RepositoryRoot: repository, ScratchDirectory: scratch, Prompt: "do the work", SessionID: session}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			args := runner.commands[0].Args
			if got := profileFilesystem(t, args)[servicesBeside(scratch)]; got != "deny" {
				t.Errorf("%s (session %q): access to the services folder = %q, want it denied", posture, session, got)
			}
			if off := "permissions." + profileNameArgument(t, args) + ".network.enabled=false"; !strings.Contains(strings.Join(args, " "), off) {
				t.Errorf("%s (session %q): the network is not off: %q", posture, session, args)
			}

			refused := &fakeRunner{}
			request.ScratchDirectory = ""
			if _, err := (Backend{Runner: refused}).Run(context.Background(), request); err == nil || len(refused.commands) != 0 {
				t.Errorf("%s (session %q): Run() with no scratch = %v, commands = %v, want a refusal", posture, session, err, refused.commands)
			}
		}
	}
	if info, err := os.Stat(servicesBeside(scratch)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("services folder = %v, %v; want it made for its owner only", info, err)
	}
}

// Every other role's session -- a review, a management conversation, a helper
// answering for a role -- has no run folder, and is refused the services folder
// of the adapter's state directory, fresh and resumed, with the rest of the
// machine still readable. One whose folder cannot be found is not started.
func TestEveryCodexSessionOfAnyOtherRoleIsRefusedTheServicesFolder(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	services := filepath.Join(state, "services")
	for _, role := range domain.Roles() {
		if role == domain.RoleDeveloper {
			continue
		}
		for name, request := range map[string]backendapi.RunRequest{
			"a fresh session":   {WorkingDirectory: worktree, RepositoryRoot: repository},
			"a resumed session": {WorkingDirectory: worktree, RepositoryRoot: repository, SessionID: "session-one"},
			// As repositoryask launches the reading helper: under the asking
			// role, in the checkout, with no tools asked for.
			"a helper": {WorkingDirectory: repository, RepositoryRoot: repository, AllowedTools: []string{}},
		} {
			request.Model, request.RunID, request.Role, request.Prompt = "gpt-6.1-sol", testRunID, role, "answer"
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded,
				Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"ok"}}`)}}}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}, StateRoot: state}).Run(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			args := runner.commands[0].Args
			if got, want := profileFilesystem(t, args), map[string]string{":root": "read", services: "deny"}; !reflect.DeepEqual(got, want) {
				t.Errorf("%s, %s: file system = %q, want %q", role, name, got, want)
			}
			if off := "permissions." + profileNameArgument(t, args) + ".network.enabled=false"; !strings.Contains(strings.Join(args, " "), off) {
				t.Errorf("%s, %s: the network is not off: %q", role, name, args)
			}
			for _, missing := range []string{"", filepath.Join(state, "state*"), filepath.Join(state, "absent")} {
				refused := &fakeRunner{}
				if _, err := (Backend{Runner: refused, StateRoot: missing}).Run(context.Background(), request); err == nil || len(refused.commands) != 0 {
					t.Errorf("%s, %s: Run() over state directory %q = %v, commands = %v, want a refusal", role, name, missing, err, refused.commands)
				}
			}
		}
	}
}

// developerFilesystemArgument is the whole file system argument of a developer
// given these writable folders and the test scratch's services folder denied.
func developerFilesystemArgument(writable ...string) string {
	table := `{":root"="read",` + tomlString(servicesBeside(testScratch)) + `="deny",":slash_tmp"="write",":tmpdir"="write"`
	for _, root := range writable {
		table += "," + tomlString(root) + `="write"`
		for _, name := range []string{".git", ".agents", ".codex", ".aws"} {
			table += "," + tomlString(root+"/"+name) + `="read"`
		}
	}
	return "permissions.yoyodyne-developer.filesystem=" + table + "}"
}
