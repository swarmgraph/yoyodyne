package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The answers Claude Code 2.1.286 gave to the check's three questions, once for
// the developer's settings as passed and once for the same settings with one key
// of the wrong type. See testdata/settings-check/README.md.
const (
	recordedAccepted = "testdata/settings-check/claude-2.1.286-accepted.jsonl"
	recordedDropped  = "testdata/settings-check/claude-2.1.286-dropped.jsonl"
)

// scriptedCLI answers the check the way an installed CLI would: its version, its
// help, and its answers to the settings questions, which a test supplies as a
// function of the settings it was launched with.
type scriptedCLI struct {
	version  string
	help     string
	answers  func(settings string) string
	commands []execution.Command
	stdin    []string
}

func (s *scriptedCLI) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	s.commands = append(s.commands, command)
	if command.Stdin != nil {
		data, err := io.ReadAll(command.Stdin)
		if err != nil {
			return execution.ProcessResult{}, err
		}
		s.stdin = append(s.stdin, string(data))
	}
	switch {
	case slices.Equal(command.Args, []string{"--version"}):
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: s.version + "\n"}, nil
	case slices.Equal(command.Args, []string{"--help"}):
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: s.help}, nil
	case slices.Contains(command.Args, "--input-format"):
		settings, _ := optionValue(command.Args, "--settings")
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: s.answers(settings)}, nil
	}
	return execution.ProcessResult{}, errors.New("unexpected process call")
}

func readRecorded(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// acceptingCLI answers as 2.1.286 did for settings it accepted: the hooks and
// sandbox answers it gave, and a settings answer whose merged settings are
// whatever it was launched with, since the patterns excluded above the
// directory differ from one test directory to the next.
func acceptingCLI(t *testing.T) func(string) string {
	t.Helper()
	recorded := readRecorded(t, recordedAccepted)
	return func(settings string) string {
		var lines []string
		for _, line := range strings.Split(strings.TrimSpace(recorded), "\n") {
			var envelope map[string]any
			if err := json.Unmarshal([]byte(line), &envelope); err != nil {
				t.Fatalf("recorded answer %q: %v", line, err)
			}
			response := envelope["response"].(map[string]any)
			if response["request_id"] == settingsQuestion {
				var passed map[string]any
				if err := json.Unmarshal([]byte(settings), &passed); err != nil {
					t.Fatalf("settings %q: %v", settings, err)
				}
				answer := response["response"].(map[string]any)
				answer["effective"] = passed
				answer["sources"] = []any{map[string]any{"source": "flagSettings", "settings": passed}}
				encoded, _ := json.Marshal(envelope)
				line = string(encoded)
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n") + "\n"
	}
}

func droppingCLI(t *testing.T) func(string) string {
	t.Helper()
	recorded := readRecorded(t, recordedDropped)
	return func(string) string { return recorded }
}

// A CLI that applied the developer's settings is in force: the version it gave
// is the one the flags were read from, so its help is not asked for, and the
// check launched it exactly as a developer is launched in that directory and
// sent it questions and no prompt.
func TestACLIThatAppliedTheDevelopersSettingsIsInForce(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	cli := &scriptedCLI{version: "2.1.286 (Claude Code)", answers: acceptingCLI(t)}
	check, err := (Backend{Runner: cli}).CheckLaunchSettings(context.Background(), worktree)
	if err != nil {
		t.Fatalf("CheckLaunchSettings() error = %v", err)
	}
	if !check.InForce() || check.Version != "2.1.286 (Claude Code)" {
		t.Fatalf("check = %#v, want everything in force on 2.1.286", check)
	}
	for _, command := range cli.commands {
		if slices.Equal(command.Args, []string{"--help"}) {
			t.Fatal("asked the recorded version for its help")
		}
	}

	launched := cli.commands[len(cli.commands)-1]
	if launched.Dir != worktree {
		t.Fatalf("checked in %q, want the directory a developer would start in", launched.Dir)
	}
	developer := &fakeRunner{results: claudeCompletedTurn()}
	if _, err := (Backend{Runner: developer, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree, Prompt: "do the work",
	}); err != nil {
		t.Fatal(err)
	}
	checked, _ := optionValue(launched.Args, "--settings")
	given, _ := optionValue(developer.commands[0].Args, "--settings")
	if checked != given {
		t.Fatalf("checked settings %s, want the developer's %s", checked, given)
	}
	for _, flag := range append(contextArgs(domain.RoleDeveloper), "--permission-mode", worktreeWriteSessionMode, "--allowedTools", "--no-session-persistence") {
		if !slices.Contains(launched.Args, flag) {
			t.Fatalf("check launched with %v, want %s as the developer has it", launched.Args, flag)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(cli.stdin[0]), "\n") {
		var sent struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &sent); err != nil || sent.Type != "control_request" {
			t.Fatalf("sent %q, want only control questions and no prompt", line)
		}
	}
	if value, _ := environmentValue(launched.Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY"); value != "1" {
		t.Fatalf("check environment %v, want the developer's", launched.Env)
	}
}

// A CLI that dropped the settings payload over one key it rejected is not in
// force, and what did not take is named: the rejected key in the CLI's own
// words, the sandbox not applied and not running, and the guard missing.
func TestACLIThatDroppedTheSettingsPayloadIsNotInForce(t *testing.T) {
	t.Parallel()
	cli := &scriptedCLI{version: "2.1.286 (Claude Code)", answers: droppingCLI(t)}
	check, err := (Backend{Runner: cli}).CheckLaunchSettings(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("CheckLaunchSettings() error = %v", err)
	}
	if check.InForce() {
		t.Fatal("a dropped payload was found in force")
	}
	said := check.Says()
	for _, want := range []string{
		"it rejected the setting autoMemoryEnabled (Expected boolean",
		"the setting sandbox is not applied as passed",
		"the sandbox that confines a developer's shell to its worktree is not running: it is not enabled, would run a command unconfined",
		"the notes guard (`yoyo goals guard` before every Bash command) is not among the hooks it will run",
		"the setting disableClaudeAiConnectors is not applied as passed",
	} {
		if !strings.Contains(said, want) {
			t.Fatalf("check says %q, want it to name %q", said, want)
		}
	}
}

// On a version other than the recorded one the installed CLI's own help is
// asked for, and a flag the adapter relies on that it no longer names is not in
// force even though every setting took.
func TestAnUpgradedCLIIsHeldToItsOwnHelp(t *testing.T) {
	t.Parallel()
	help := readRecorded(t, recordedHelp)
	cli := &scriptedCLI{version: "2.2.0 (Claude Code)", help: help, answers: acceptingCLI(t)}
	check, err := (Backend{Runner: cli}).CheckLaunchSettings(context.Background(), t.TempDir())
	if err != nil || !check.InForce() {
		t.Fatalf("check = %#v, %v; want an upgraded CLI that names every flag in force", check, err)
	}

	var renamed []string
	for _, line := range strings.Split(help, "\n") {
		renamed = append(renamed, strings.Replace(line, "--setting-sources", "--settings-sources", 1))
	}
	cli = &scriptedCLI{version: "2.2.0 (Claude Code)", help: strings.Join(renamed, "\n"), answers: acceptingCLI(t)}
	check, err = (Backend{Runner: cli}).CheckLaunchSettings(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("CheckLaunchSettings() error = %v", err)
	}
	if want := "the flag --setting-sources is not in its help (it was read from 2.1.286)"; check.Says() != want {
		t.Fatalf("check says %q, want %q", check.Says(), want)
	}
}

// A CLI that answered none of the questions is not in force, and is said with
// its exit and what it printed; one that never answered in time is a check that
// could not be made.
func TestACLIThatDoesNotAnswerIsNotInForceAndOneThatHangsIsNoCheck(t *testing.T) {
	t.Parallel()
	silent := &scriptedCLI{version: "2.1.286 (Claude Code)", answers: func(string) string { return "" }}
	failing := failingSettingsRunner{scriptedCLI: silent, result: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "error: unknown option '--input-format'"}}
	check, err := (Backend{Runner: failing}).CheckLaunchSettings(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("CheckLaunchSettings() error = %v", err)
	}
	if !strings.Contains(check.Says(), "exit 1: error: unknown option '--input-format'") {
		t.Fatalf("check says %q, want the CLI's own refusal", check.Says())
	}

	hung := failingSettingsRunner{scriptedCLI: silent, result: execution.ProcessResult{Status: execution.ProcessTimedOut}}
	if _, err := (Backend{Runner: hung}).CheckLaunchSettings(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a CLI that never answered was judged rather than reported as a check that could not be made")
	}
}

type failingSettingsRunner struct {
	*scriptedCLI
	result execution.ProcessResult
}

func (f failingSettingsRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if slices.Contains(command.Args, "--input-format") {
		return f.result, nil
	}
	return f.scriptedCLI.Run(ctx, command, observer)
}

// Every flag any invocation of this adapter passes is one the check confirms
// on an upgraded CLI, and every one of those is in the recorded help.
func TestEveryFlagTheAdapterPassesIsOneTheCheckConfirms(t *testing.T) {
	t.Parallel()
	help := readRecorded(t, recordedHelp)
	for _, flag := range reliedFlags {
		if !helpNames(help, flag) {
			t.Fatalf("%s is not in %s", flag, recordedHelp)
		}
	}
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, role := range domain.Roles() {
		if !supportedRole(role) {
			continue
		}
		for _, session := range []string{"", "session-1"} {
			runner := &fakeRunner{results: claudeCompletedTurn()}
			request := backendapi.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: worktree, Prompt: "do the work",
				SystemPrompt: "the contract", SessionID: session, Model: "opus", Effort: "high",
			}
			if role != domain.RoleDeveloper {
				request.AllowedTools = []string{}
			}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), request); err != nil {
				t.Fatalf("%s: %v", role, err)
			}
			for _, arg := range runner.commands[0].Args {
				if strings.HasPrefix(arg, "-") && !slices.Contains(reliedFlags, arg) {
					t.Fatalf("%s passes %s, which the check does not confirm", role, arg)
				}
			}
		}
	}
	for _, arg := range settingsCheckArgs("{}") {
		if strings.HasPrefix(arg, "-") && !slices.Contains(reliedFlags, arg) {
			t.Fatalf("the check passes %s, which it does not confirm", arg)
		}
	}
}
