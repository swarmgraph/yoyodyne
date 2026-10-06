package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// recordedHelp is Claude Code 2.1.286's own `claude --help`, which is where the
// flags an invocation carries to keep the account's configuration out were read
// from. Every one of them is checked against it, so a flag this adapter passes
// is one that CLI version names.
const recordedHelp = "testdata/cli-help/claude-2.1.286.txt"

// personalClaudeHome is a provider home set up the way the operator's was: user
// settings enabling a plugin and a hook, a memory index, a skill, an agent, a
// CLAUDE.md, and a claude.ai connector cached in the home's own configuration.
// Each carries a marker nothing an invocation is sent may contain.
func personalClaudeHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "claude-home")
	writeTestFiles(t, home, map[string]string{
		"settings.json":                         `{"enabledPlugins":{"personal@market":true},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"PERSONAL_HOOK_MARKER"}]}]}}`,
		"CLAUDE.md":                             "PERSONAL_CLAUDE_MD_MARKER\n",
		"skills/code-review/SKILL.md":           "---\nname: code-review\ndescription: PERSONAL_SKILL_MARKER\n---\n",
		"agents/helper.md":                      "PERSONAL_AGENT_MARKER\n",
		"projects/-worktree/memory/MEMORY.md":   "- PERSONAL_MEMORY_MARKER\n",
		".claude.json":                          `{"mcpServers":{"personal":{"command":"PERSONAL_MCP_MARKER"}},"claudeAiMcpEverConnected":["Gmail"]}`,
		"plugins/installed_plugins.json":        `{"plugins":{"personal@market":{}}}`,
		"plugins/personal/skills/x/SKILL.md":    "PERSONAL_PLUGIN_MARKER\n",
		"projects/-worktree/memory/feedback.md": "PERSONAL_MEMORY_MARKER\n",
	})
	return home
}

func writeTestFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for file, body := range files {
		path := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func claudeCompletedTurn() []execution.ProcessResult {
	return []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}`,
	}, "\n") + "\n"}}
}

// optionValue is the value an invocation gave option, and whether it gave one.
func optionValue(args []string, option string) (string, bool) {
	index := slices.Index(args, option)
	if index < 0 || index+1 >= len(args) {
		return "", false
	}
	return args[index+1], true
}

// invocationSettings decodes the settings an invocation was passed.
func invocationSettings(t *testing.T, args []string) map[string]any {
	t.Helper()
	encoded, found := optionValue(args, "--settings")
	if !found {
		t.Fatalf("no --settings: %v", args)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		t.Fatalf("settings %q: %v", encoded, err)
	}
	return settings
}

func environmentValue(environment []string, name string) (string, bool) {
	for _, entry := range environment {
		if value, found := strings.CutPrefix(entry, name+"="); found {
			return value, true
		}
	}
	return "", false
}

// Every role, on a fresh invocation and on a resumed one, reads no settings
// file from the account's home, no memory, no skill, no plugin, no MCP server,
// no claude.ai connector, and no instruction file other than its own
// repository's, and its run says so.
func TestNoPersonalSettingsMemorySkillsConnectorsOrInstructionFilesReachAnyRoleFreshOrResumed(t *testing.T) {
	t.Parallel()
	for _, role := range domain.Roles() {
		if !supportedRole(role) {
			continue
		}
		for _, session := range []string{"", "session-1"} {
			home := personalClaudeHome(t)
			worktree := filepath.Join(t.TempDir(), "worktree")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			runner := &fakeRunner{results: claudeCompletedTurn()}
			var started []string
			request := backendapi.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: worktree,
				Prompt: "do the work", SystemPrompt: "the role's contract", SessionID: session,
				EventSink: func(event execution.Event) error {
					if event.Type == execution.EventRunStarted {
						started = append(started, string(event.Payload))
					}
					return nil
				},
			}
			if role != domain.RoleDeveloper {
				request.AllowedTools = []string{}
			}
			result, err := (Backend{Runner: runner, Clock: fixedClock{}, ConfigDir: home}).Run(context.Background(), request)
			if err != nil {
				t.Fatalf("%s, session %q: %v", role, session, err)
			}
			command := runner.commands[0]
			args := command.Args

			// The settings files read: the developer its worktree's own, every
			// other role none. Never the user's.
			sources, found := optionValue(args, "--setting-sources")
			want := readOnlySettingSources
			if role == domain.RoleDeveloper {
				want = developerSettingSources
			}
			if !found || sources != want || strings.Contains(sources, "user") || strings.Contains(sources, "local") {
				t.Fatalf("%s, session %q: --setting-sources = %q (given %v), want %q", role, session, sources, found, want)
			}
			for _, flag := range []string{"--strict-mcp-config", "--disable-slash-commands"} {
				if !slices.Contains(args, flag) {
					t.Fatalf("%s, session %q: %s missing: %v", role, session, flag, args)
				}
			}
			// Nothing names an MCP server, a plugin, or another directory to read.
			for _, flag := range []string{"--mcp-config", "--plugin-dir", "--plugin-url", "--add-dir"} {
				if slices.Contains(args, flag) {
					t.Fatalf("%s, session %q: %s passed: %v", role, session, flag, args)
				}
			}
			if session != "" {
				if resumed, _ := optionValue(args, "--resume"); resumed != session {
					t.Fatalf("%s: not resumed: %v", role, args)
				}
			}
			settings := invocationSettings(t, args)
			if settings["autoMemoryEnabled"] != false || settings["disableClaudeAiConnectors"] != true {
				t.Fatalf("%s, session %q: settings = %v", role, session, settings)
			}
			for name, value := range map[string]string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1", "ENABLE_CLAUDEAI_MCP_SERVERS": "false"} {
				if got, _ := environmentValue(command.Env, name); got != value {
					t.Fatalf("%s, session %q: %s = %q, want %q", role, session, name, got, value)
				}
			}
			if role == domain.RoleDeveloper {
				// Its sandbox and guard are still what the developer runs under.
				if settings["sandbox"] == nil || settings["hooks"] == nil {
					t.Fatalf("developer settings lost the sandbox or guard: %v", settings)
				}
				excluded, _ := settings["claudeMdExcludes"].([]any)
				parent := filepath.ToSlash(filepath.Join(filepath.Dir(worktree), "CLAUDE.md"))
				if !slices.Contains(excluded, any(globLiteral(parent))) {
					t.Fatalf("the CLAUDE.md above the worktree is not excluded: %v", excluded)
				}
				for _, pattern := range excluded {
					if strings.HasPrefix(pattern.(string), globLiteral(filepath.ToSlash(worktree))+"/") {
						t.Fatalf("the worktree's own instruction file is excluded: %v", pattern)
					}
				}
			}
			sent := strings.Join(args, "\n") + runner.prompts[0]
			for _, marker := range []string{"PERSONAL_HOOK_MARKER", "PERSONAL_CLAUDE_MD_MARKER", "PERSONAL_SKILL_MARKER", "PERSONAL_AGENT_MARKER", "PERSONAL_MEMORY_MARKER", "PERSONAL_MCP_MARKER", "PERSONAL_PLUGIN_MARKER"} {
				if strings.Contains(sent, marker) {
					t.Fatalf("%s, session %q: the invocation carries %s", role, session, marker)
				}
			}
			const none = "settings sources: none; skills: none; plugins: none; connectors: none; instruction files: none"
			if !result.Loaded.Reported || result.Loaded.String() != none {
				t.Fatalf("%s, session %q: loaded = %q", role, session, result.Loaded.String())
			}
			if len(started) != 1 || !strings.Contains(started[0], `"loaded":"`+none+`"`) {
				t.Fatalf("%s, session %q: run.started = %v", role, session, started)
			}
		}
	}
}

// An operator's own shell that forces memory or connectors on does not carry
// through to an invocation.
func TestTheHarnessEnvironmentCannotTurnMemoryOrConnectorsBackOn(t *testing.T) {
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "0")
	t.Setenv("ENABLE_CLAUDEAI_MCP_SERVERS", "true")
	runner := &fakeRunner{results: claudeCompletedTurn()}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(), Prompt: "do the work",
	}); err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, entry := range runner.commands[0].Env {
		if strings.HasPrefix(entry, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=") || strings.HasPrefix(entry, "ENABLE_CLAUDEAI_MCP_SERVERS=") {
			values = append(values, entry)
		}
	}
	slices.Sort(values)
	if want := []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "ENABLE_CLAUDEAI_MCP_SERVERS=false"}; !slices.Equal(values, want) {
		t.Fatalf("environment = %v, want %v", values, want)
	}
}

// Every flag the adapter passes to keep the account's configuration out is one
// the recorded CLI help names.
func TestEveryContextFlagIsOneTheRecordedCLINames(t *testing.T) {
	t.Parallel()
	help, err := os.ReadFile(recordedHelp)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		for _, arg := range append(contextArgs(role), "--settings", "--safe-mode") {
			if strings.HasPrefix(arg, "--") && !strings.Contains(string(help), "  "+arg+" ") {
				t.Fatalf("%s is not in %s", arg, recordedHelp)
			}
		}
	}
}

// A project names a skill and an instruction file in its configuration, and the
// role it names them for is given both, in its standing instructions, and its
// run says so; a role they are not named for is not given the skill.
func TestTheSkillsAndInstructionFilesTheProjectNamesAreGivenToAClaudeCodeRole(t *testing.T) {
	t.Parallel()
	named := backendapi.NamedContext{
		Skills:       []backendapi.ContextFile{{Path: ".yoyodyne/skills/review", Roles: []domain.AgentRole{domain.RoleReviewer}}},
		Instructions: []backendapi.ContextFile{{Path: "docs/agent-notes.md"}},
	}
	for _, role := range []domain.AgentRole{domain.RoleReviewer, domain.RoleDeveloper} {
		for _, session := range []string{"", "session-1"} {
			repository := t.TempDir()
			writeTestFiles(t, repository, map[string]string{
				".yoyodyne/skills/review/SKILL.md": "---\nname: careful-review\ndescription: review carefully\n---\nPROJECT_SKILL_BODY\n",
				"docs/agent-notes.md":              "PROJECT_NOTES_BODY\n",
			})
			worktree := t.TempDir()
			request := backendapi.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: worktree, RepositoryRoot: repository,
				Prompt: "do the work", SystemPrompt: "the role's contract", SessionID: session,
			}
			if role != domain.RoleDeveloper {
				request.AllowedTools = []string{}
			}
			runner := &fakeRunner{results: claudeCompletedTurn()}
			result, err := (Backend{Runner: runner, Clock: fixedClock{}, ConfigDir: personalClaudeHome(t), Context: named}).Run(context.Background(), request)
			if err != nil {
				t.Fatalf("%s: %v", role, err)
			}
			system, _ := optionValue(runner.commands[0].Args, "--append-system-prompt")
			if !strings.HasPrefix(system, "the role's contract") || !strings.Contains(system, "PROJECT_NOTES_BODY") {
				t.Fatalf("%s: the named instruction file does not follow the contract:\n%s", role, system)
			}
			if strings.Contains(runner.prompts[0], "PROJECT_NOTES_BODY") {
				t.Fatalf("%s: the named context was sent as the task", role)
			}
			notes := backendapi.LoadedItem{Name: "agent-notes.md", Source: backendapi.LoadedFromProjectConfiguration, Path: filepath.Join(repository, "docs", "agent-notes.md")}
			if !slices.Equal(result.Loaded.Instructions, []backendapi.LoadedItem{notes}) {
				t.Fatalf("%s: instructions = %+v", role, result.Loaded.Instructions)
			}
			if role == domain.RoleReviewer {
				skill := backendapi.LoadedItem{Name: "careful-review", Source: backendapi.LoadedFromProjectConfiguration, Path: filepath.Join(repository, ".yoyodyne", "skills", "review", "SKILL.md")}
				if !strings.Contains(system, "PROJECT_SKILL_BODY") || !slices.Equal(result.Loaded.Skills, []backendapi.LoadedItem{skill}) {
					t.Fatalf("the reviewer was not given the skill named for it: %+v", result.Loaded.Skills)
				}
				want := "settings sources: none; skills: careful-review (project configuration, " + skill.Path + "); plugins: none; connectors: none; instruction files: agent-notes.md (project configuration, " + notes.Path + ")"
				if result.Loaded.String() != want {
					t.Fatalf("summary = %q, want %q", result.Loaded.String(), want)
				}
			} else if strings.Contains(system, "PROJECT_SKILL_BODY") || len(result.Loaded.Skills) != 0 {
				t.Fatal("the developer was given a skill named only for the reviewer")
			}
		}
	}
}

// A named file that cannot be read refuses the invocation before anything is
// sent, rather than running the role without it.
func TestANamedFileThatCannotBeReadRefusesTheClaudeCodeInvocation(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: claudeCompletedTurn()}
	_, err := (Backend{Runner: runner, Clock: fixedClock{}, Context: backendapi.NamedContext{Instructions: []backendapi.ContextFile{{Path: "/nowhere/notes.md"}}}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(), Prompt: "do the work",
	})
	if err == nil || !strings.Contains(err.Error(), "/nowhere/notes.md") || len(runner.commands) != 0 {
		t.Fatalf("err = %v, commands = %d", err, len(runner.commands))
	}
}

// What a developer reads from its own worktree is the repository's, and the run
// names each: the checked-in settings, the plugins they enable, and the
// instruction files at the worktree's root.
func TestADeveloperRunNamesWhatItReadsFromItsOwnRepository(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	writeTestFiles(t, worktree, map[string]string{
		"CLAUDE.md":             "the repository's instructions\n",
		".claude/settings.json": `{"enabledPlugins":{"team-tool@market":true,"off@market":false}}`,
	})
	runner := &fakeRunner{results: claudeCompletedTurn()}
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree, Prompt: "do the work",
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(worktree, ".claude", "settings.json")
	want := "settings sources: .claude/settings.json (repository, " + settings + "); skills: none; plugins: team-tool@market (repository, " + settings + "); connectors: none; instruction files: CLAUDE.md (repository, " + filepath.Join(worktree, "CLAUDE.md") + ")"
	if result.Loaded.String() != want {
		t.Fatalf("loaded = %q, want %q", result.Loaded.String(), want)
	}
}

// A path is excluded as itself: characters a glob gives a meaning to are
// escaped, so a directory named with one excludes that directory and no other.
func TestAnExcludedInstructionFileIsMatchedLiterally(t *testing.T) {
	t.Parallel()
	if got := globLiteral("/Users/a (b)/[x]*/CLAUDE.md"); got != `/Users/a \(b\)/\[x\]\*/CLAUDE.md` {
		t.Fatalf("globLiteral = %q", got)
	}
	patterns := instructionFilesAbove("/one/two/worktree")
	for _, want := range []string{"/one/two/CLAUDE.md", "/one/two/CLAUDE.local.md", "/one/two/.claude/CLAUDE.md", "/one/two/.claude/rules/**", "/one/CLAUDE.md", "/CLAUDE.md"} {
		if !slices.Contains(patterns, want) {
			t.Fatalf("patterns = %v, want %s", patterns, want)
		}
	}
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "/one/two/worktree/") {
			t.Fatalf("the worktree's own file is excluded: %s", pattern)
		}
	}
}
