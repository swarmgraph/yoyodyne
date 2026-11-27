package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A Git hook is a program the repository supplies and the harness executes, so
// what a harness-run Git command is launched with is what that program is
// launched with. Reading the environment out of a hook is the only way to see
// that from the outside, which is why this test writes one rather than
// inspecting the Command the manager composed.
//
// post-checkout is the hook `git worktree add` runs, and the add is deliberately
// one of the Git commands the harness does not disable hooks for: a worktree
// whose creation skipped the repository's own checkout hooks is not the worktree
// the project's tooling expects.
func TestAGitHookTheHarnessRunsSeesNoneOfTheHarnessesCredentials(t *testing.T) {
	repository := newRepository(t)
	dump := filepath.Join(t.TempDir(), "post-checkout.env")
	writeEnvironmentDumpingHook(t, repository, "post-checkout", dump)

	// Exported in the shell the harness was started from, which is exactly how
	// the Slack tokens reached an agent's process tree before yoyodyne-ifd.408
	// and how they reached a hook until this.
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-not-for-a-hook")
	t.Setenv("SLACK_APP_TOKEN", "xapp-not-for-a-hook")
	t.Setenv("ANTHROPIC_API_KEY", "sk-not-for-a-hook")
	t.Setenv("GH_TOKEN", "ghp-only-for-the-forge")

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	if _, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-hook-environment",
		BaseRef:    "main",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	hookEnvironment := readEnvironmentDump(t, dump)
	for _, name := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "ANTHROPIC_API_KEY", "GH_TOKEN"} {
		if value, carried := hookEnvironment[name]; carried {
			t.Errorf("the post-checkout hook was given %s=%q; no credential reaches a Git command", name, value)
		}
	}
	// The allowlist is what the hook got rather than nothing at all: a hook with
	// no PATH is a hook that cannot run the tools it was written to run, which
	// would make this a different defect rather than a fix.
	if hookEnvironment["PATH"] == "" {
		t.Error("the post-checkout hook was given no PATH")
	}
	// And the maintenance fence still reaches it, which is the guarantee
	// yoyodyne-ifd.334 made about every process the harness starts.
	if hookEnvironment["GIT_CONFIG_COUNT"] == "" {
		t.Error("the post-checkout hook was given no Git maintenance fence")
	}
}

// A command that talks to the remote is the one family of Git commands that
// needs a forge credential, and it is given one by name rather than by
// inheriting everything the harness holds.
func TestAGitCommandTalkingToTheRemoteCarriesTheForgeCredentialAndNoOther(t *testing.T) {
	repository, _ := newPublishedRepository(t)

	t.Setenv("SLACK_BOT_TOKEN", "xoxb-not-for-git")
	t.Setenv("GH_TOKEN", "ghp-only-for-the-forge")
	// A project whose remote is SSH authenticates the push with a key the agent
	// at this socket holds, and the socket is what points Git at it. It is on
	// the standing allowlist rather than on the forge's own list, which is easy
	// to read as an omission: a remote-reaching command that lost it would fail
	// to authenticate on every installation with an SSH remote, and every run
	// would stop at integration. The test remote here is a local path, so
	// nothing would notice the loss on its own — this is what notices it.
	t.Setenv("SSH_AUTH_SOCK", "/private/tmp/ssh-agent-for-this-test.sock")

	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Remote:         "origin",
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-remote-environment",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "published work\n")
	if _, err := manager.PublishBranch(context.Background(), worktree, ""); err != nil {
		t.Fatalf("PublishBranch() error = %v", err)
	}

	local, reachingTheRemote := 0, 0
	for _, command := range runner.bounds {
		environment := environmentValues(command.Env)
		if _, credentialed := environment["GH_TOKEN"]; credentialed {
			reachingTheRemote++
			if environment["SSH_AUTH_SOCK"] != "/private/tmp/ssh-agent-for-this-test.sock" {
				t.Errorf("git %v reaches the remote and was given SSH_AUTH_SOCK=%q, so an SSH remote would not authenticate",
					command.Args, environment["SSH_AUTH_SOCK"])
			}
		} else {
			local++
		}
		if _, leaked := environment["SLACK_BOT_TOKEN"]; leaked {
			t.Fatalf("git %v was given SLACK_BOT_TOKEN", command.Args)
		}
	}
	if reachingTheRemote == 0 {
		t.Error("no Git command talking to the remote was given the forge credential")
	}
	if local == 0 {
		t.Error("every Git command was given the forge credential; only the ones reaching the remote need it")
	}
}

// writeEnvironmentDumpingHook installs a repository hook that writes its own
// environment out and nothing else. The destination is written into the script
// rather than passed in a variable, so the test does not depend on the very
// allowlist it is checking to find its own output.
func writeEnvironmentDumpingHook(t *testing.T, repository, name, dump string) {
	t.Helper()
	script := "#!/bin/sh\nenv > '" + dump + "'\nexit 0\n"
	path := filepath.Join(repository, ".git", "hooks", name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}

func readEnvironmentDump(t *testing.T, dump string) map[string]string {
	t.Helper()
	content, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the hook wrote no environment: %v", err)
	}
	return environmentValues(strings.Split(strings.TrimRight(string(content), "\n"), "\n"))
}

func environmentValues(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		if name, value, named := strings.Cut(entry, "="); named {
			values[name] = value
		}
	}
	return values
}

// The harness's own commit runs none of the repository's commit hooks, so the
// tracker export the repository's pre-commit hook takes never runs inside a
// commit the harness makes, and no lock the harness holds while it commits can
// be one that commit's hook waits on. The hook here refuses every commit and
// says it was run, so a commit that ran it fails or leaves the mark.
func TestTheHarnessesCommitRunsNoCommitHook(t *testing.T) {
	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-commit-hooks",
		BaseRef:    "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	hooks := t.TempDir()
	mark := filepath.Join(t.TempDir(), "hook-ran")
	for _, hook := range []string{"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit"} {
		script := "#!/bin/sh\nprintf '%s\\n' " + hook + " >> '" + mark + "'\nexit 1\n"
		if err := os.WriteFile(filepath.Join(hooks, hook), []byte(script), 0o700); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	runGit(t, repository, "config", "core.hooksPath", hooks)
	writeFile(t, worktree.Path, "feature.txt", "work\n")

	if _, err := manager.commitWorktree(context.Background(), worktree.Path, "the harness's commit"); err != nil {
		t.Fatalf("commitWorktree() error = %v", err)
	}
	if ran, err := os.ReadFile(mark); err == nil {
		t.Fatalf("the harness's commit ran the repository's hooks: %s", strings.TrimSpace(string(ran)))
	}
}
