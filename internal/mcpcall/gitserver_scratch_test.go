//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcpcall

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The real git server the project declares, started the way the harness
// starts a server, still answers on a repository after a developer run left a
// link leading outside it in its scratch folder, because that folder is in the
// harness's state directory and not in the repository's `.git`
// (docs/decisions/developer-run-scratch-outside-git.md). The same link where
// scratch used to be cut, under `.git`, makes it refuse, which is what makes
// the answer mean something. It runs where mcp-server-git is installed, as it
// is on a machine that declares it, and skips elsewhere;
// internal/orchestrator/pipelinetests holds the same assertion against a
// stand-in for the server's check, which runs everywhere.
func TestTheGitServerAnswersWithALinkLeftInAStateDirectoryScratch(t *testing.T) {
	binary, err := exec.LookPath("mcp-server-git")
	if err != nil {
		t.Skipf("the git server is not installed here: %v", err)
	}
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=test", "-c", "user.email=test@invalid", "commit", "-q", "--allow-empty", "-m", "base"},
	} {
		command := exec.Command("git", args...)
		command.Dir = repository
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	program := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(program, []byte("a program outside the repository\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	scratch, err := execution.PrepareScratchDirectory(t.TempDir(), "run-one")
	if err != nil {
		t.Fatal(err)
	}
	arg0 := filepath.Join(scratch, "home", "tmp", "arg0")
	if err := os.MkdirAll(arg0, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(program, filepath.Join(arg0, "apply_patch")); err != nil {
		t.Fatal(err)
	}

	server := Server{Name: "git", Declared: config.MCPServer{Command: []string{binary, "--repository", repository}}}
	options := Options{Stores: testStores(t)}
	answer := func() Result {
		var result Result
		arguments, _ := json.Marshal(map[string]any{"repo_path": repository, "max_count": 1})
		if err := batch(context.Background(), server, options, func(session *Session) error {
			result = call(t, session, "git_log", string(arguments))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := answer(); result.IsError {
		t.Fatalf("the git server refused with a link in a scratch folder in the state directory: %s", result.Raw)
	}

	old := filepath.Join(repository, ".git", "yoyodyne", "scratch", "run-one")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(program, filepath.Join(old, "apply_patch")); err != nil {
		t.Fatal(err)
	}
	if result := answer(); !result.IsError {
		t.Fatalf("the git server answered with a link leading out of .git, so this test cannot tell where scratch is: %s", result.Raw)
	}
}
