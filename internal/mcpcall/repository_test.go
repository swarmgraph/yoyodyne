//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcpcall

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// repositoryPath is a call path whose one server is declared by the command
// it is given, held by a conversation agent, a developer agent and a
// recurring task, with the main checkout as the path's repository.
func repositoryPath(t *testing.T, command []string, tools map[string]config.MCPToolClass, main string) (*Path, config.Config) {
	t.Helper()
	stores := testStores(t)
	var names []string
	for tool := range tools {
		names = append(names, "repo."+tool)
	}
	cfg := config.Config{
		MCP: config.MCP{Servers: map[string]config.MCPServer{"repo": {Command: command, Tools: tools}}},
		Agents: map[string]config.AgentConfig{
			"lead": {Role: domain.RoleProductManager, MCPTools: names},
			"dev":  {Role: domain.RoleDeveloper, MCPTools: names},
		},
	}
	hold, err := runstate.NewOperatorHoldStore(stores.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	records, err := NewRecords(stores.StateRoot, testProduct)
	if err != nil {
		t.Fatal(err)
	}
	return &Path{Config: cfg, Stores: stores, Hold: hold, Records: records, Repository: main}, cfg
}

func doOne(t *testing.T, path *Path, caller Caller, tool string, arguments any) Answer {
	t.Helper()
	encoded, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	answers, err := path.Do(context.Background(), caller, []Request{{Tool: tool, Arguments: encoded}})
	if err != nil {
		t.Fatalf("the record failed: %v", err)
	}
	if len(answers) != 1 {
		t.Fatalf("%d answers for one request", len(answers))
	}
	return answers[0]
}

// handedRepository is the value of RepositoryVariable the stand-in server was
// started with, from its environment tool's answer; empty where it had none.
func handedRepository(t *testing.T, answer Answer) string {
	t.Helper()
	if answer.Outcome != OutcomePerformed {
		t.Fatalf("the environment call = %+v, want performed", answer)
	}
	var seen struct {
		Env []string `json:"env"`
	}
	if err := json.Unmarshal([]byte(answer.Result), &seen); err != nil {
		t.Fatalf("the environment answer %q: %v", answer.Result, err)
	}
	found := ""
	for _, entry := range seen.Env {
		if value, ok := strings.CutPrefix(entry, RepositoryVariable+"="); ok {
			found = value
		}
	}
	return found
}

// A developer run's server is handed the run's own worktree, and a
// conversation's and a recurring task's are handed the main checkout. Each
// call starts a server of its own, so two runs asking one after the other are
// each handed their own worktree and neither sees the other's. A developer
// caller with no worktree of its own is refused, rather than given the main
// checkout, and nothing the caller asks for can change the path.
func TestEachCallerIsHandedItsOwnRepository(t *testing.T) {
	main := filepath.Join(t.TempDir(), "main")
	path, cfg := repositoryPath(t, fakeCommand(), map[string]config.MCPToolClass{"environment": config.MCPRead}, main)

	first := filepath.Join(t.TempDir(), "worktree-one")
	second := filepath.Join(t.TempDir(), "worktree-two")
	one, err := DeveloperCaller(cfg, "dev", "run-1", 0, first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := DeveloperCaller(cfg, "dev", "run-2", 0, second)
	if err != nil {
		t.Fatal(err)
	}
	lead, err := ConversationCaller(cfg, "lead", "chat-1", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	task, err := ScheduledCaller(cfg, "intake", []string{"repo.environment"})
	if err != nil {
		t.Fatal(err)
	}

	// The arguments name another checkout, which changes nothing about what
	// the server is handed: the repository comes from the harness alone.
	asked := map[string]string{"repo_path": main}
	for _, check := range []struct {
		name   string
		caller Caller
		want   string
	}{
		{"the first run", one, first},
		{"the second run", two, second},
		{"the first run again", one, first},
		{"a conversation", lead, main},
		{"a recurring task", task, main},
	} {
		if got := handedRepository(t, doOne(t, path, check.caller, "repo.environment", asked)); got != check.want {
			t.Errorf("%s's server was handed %s=%q, want %q", check.name, RepositoryVariable, got, check.want)
		}
	}

	for _, worktree := range []string{"", "relative/worktree"} {
		if _, err := DeveloperCaller(cfg, "dev", "run-3", 0, worktree); err == nil || !strings.Contains(err.Error(), "no worktree of its own") {
			t.Errorf("a developer caller with worktree %q = %v, want refused", worktree, err)
		}
	}
}

// A server declared without RepositoryVariable is handed nothing it did not
// ask for when the path names no repository: the variable is set only where
// the harness has a repository to name.
func TestNoRepositoryIsHandedWhereThePathHasNone(t *testing.T) {
	path, cfg := repositoryPath(t, fakeCommand(), map[string]config.MCPToolClass{"environment": config.MCPRead}, "")
	lead, err := ConversationCaller(cfg, "lead", "chat-1", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := handedRepository(t, doOne(t, path, lead, "repo.environment", map[string]any{})); got != "" {
		t.Errorf("the server was handed %s=%q, want nothing", RepositoryVariable, got)
	}
}

// The real git server, declared as the project declares it with the
// repository named from RepositoryVariable, answers a developer run's git_log
// on its own worktree with that branch's history, and refuses the main
// checkout and another run's worktree. A linked worktree's git directory is
// under the main checkout's .git, outside the worktree, and the server accepts
// it as the worktree's own. It runs where mcp-server-git is installed, as it
// is on a machine that declares it, and skips elsewhere;
// TestEachCallerIsHandedItsOwnRepository holds what the harness hands the
// server everywhere.
func TestTheGitServerReadsADeveloperRunsOwnWorktree(t *testing.T) {
	binary, err := exec.LookPath("mcp-server-git")
	if err != nil {
		t.Skipf("the git server is not installed here: %v", err)
	}
	base := t.TempDir()
	main := filepath.Join(base, "main")
	mine := filepath.Join(base, "worktrees", "mine")
	theirs := filepath.Join(base, "worktrees", "theirs")
	if err := os.MkdirAll(main, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@invalid"}, args...)...)
		command.Dir = dir
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	git(main, "init", "-q", "-b", "main")
	git(main, "commit", "-q", "--allow-empty", "-m", "the base on main")
	git(main, "worktree", "add", "-q", "-b", "run/mine", mine)
	git(main, "worktree", "add", "-q", "-b", "run/theirs", theirs)
	git(mine, "commit", "-q", "--allow-empty", "-m", "only on my run's branch")
	git(theirs, "commit", "-q", "--allow-empty", "-m", "only on their run's branch")

	command := []string{"sh", "-c", `exec "$0" --repository "$` + RepositoryVariable + `"`, binary}
	path, cfg := repositoryPath(t, command, map[string]config.MCPToolClass{"git_log": config.MCPRead}, main)
	developer, err := DeveloperCaller(cfg, "dev", "run-mine", 0, mine)
	if err != nil {
		t.Fatal(err)
	}

	own := doOne(t, path, developer, "repo.git_log", map[string]any{"repo_path": mine, "max_count": 5})
	if own.Outcome != OutcomePerformed || own.ToolError {
		t.Fatalf("git_log on the run's own worktree = %+v, want its branch's history", own)
	}
	if !strings.Contains(own.Result, "only on my run's branch") || !strings.Contains(own.Result, "the base on main") {
		t.Errorf("git_log on the run's own worktree did not return its branch's commits: %s", own.Result)
	}
	if strings.Contains(own.Result, "their run's branch") {
		t.Errorf("git_log on the run's own worktree returned another run's commit: %s", own.Result)
	}

	for name, other := range map[string]string{"the main checkout": main, "another run's worktree": theirs} {
		answer := doOne(t, path, developer, "repo.git_log", map[string]any{"repo_path": other, "max_count": 5})
		if answer.Outcome == OutcomePerformed && !answer.ToolError {
			t.Errorf("git_log on %s from a developer run = %+v, want refused by the server", name, answer)
		}
		if !strings.Contains(answer.Result, "outside the allowed repository") {
			t.Errorf("git_log on %s was not refused as outside the run's worktree: %+v", name, answer)
		}
	}

	lead, err := ConversationCaller(cfg, "lead", "chat-1", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := doOne(t, path, lead, "repo.git_log", map[string]any{"repo_path": main, "max_count": 5})
	if conversation.Outcome != OutcomePerformed || conversation.ToolError || !strings.Contains(conversation.Result, "the base on main") {
		t.Errorf("git_log on the main checkout from a conversation = %+v, want main's history", conversation)
	}
	if strings.Contains(conversation.Result, "only on my run's branch") {
		t.Errorf("a conversation's git_log on the main checkout returned a run's commit: %s", conversation.Result)
	}
}
