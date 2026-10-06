package codex

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// personalHome is a provider home set up the way the operator's was: a personal
// skill, personal instruction files, and the account's own authentication and
// configuration beside them. Each personal file carries a marker no prompt may
// contain.
func personalHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "codex-home")
	for file, body := range map[string]string{
		"skills/code-review/SKILL.md":       "---\nname: code-review\ndescription: PERSONAL_SKILL_MARKER\n---\nreview it my way\n",
		"AGENTS.md":                         "PERSONAL_AGENTS_MARKER\n",
		"AGENTS.override.md":                "PERSONAL_OVERRIDE_MARKER\n",
		"auth.json":                         `{"auth_mode":"chatgpt"}`,
		"config.toml":                       "model_reasoning_effort = \"high\"\n[[skills.config]]\npath = \"/elsewhere/SKILL.md\"\n",
		"sessions/2026/10/05/rollout.jsonl": "{}\n",
	} {
		path := filepath.Join(home, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// homeRunner records, while the invocation is running, what the provider home
// it was handed holds: the harness removes a home it made once the invocation
// ends, so this is the only moment it can be looked at.
type homeRunner struct {
	fakeRunner
	homes        []string
	instructions [][]string
	auth         []string
}

func (r *homeRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	home := ""
	for _, entry := range command.Env {
		if value, found := strings.CutPrefix(entry, ProviderHomeVariable+"="); found {
			home = value
		}
	}
	r.homes = append(r.homes, home)
	var present []string
	for _, name := range instructionFileNames {
		if _, err := os.Lstat(filepath.Join(home, name)); err == nil {
			present = append(present, name)
		}
	}
	r.instructions = append(r.instructions, present)
	auth, _ := os.ReadFile(filepath.Join(home, "auth.json"))
	r.auth = append(r.auth, string(auth))
	return r.fakeRunner.Run(ctx, command, observer)
}

func completedTurn() []execution.ProcessResult {
	return []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: lines(
		`{"type":"thread.started","thread_id":"session-1"}`,
		`{"type":"turn.completed"}`,
	)}}
}

func TestNoPersonalSkillPluginOrInstructionFileReachesAnyRoleFreshOrResumed(t *testing.T) {
	t.Parallel()
	for _, role := range domain.Roles() {
		for _, session := range []string{"", "session-1"} {
			home := personalHome(t)
			repository, worktree := sandboxRepository(t, true)
			runner := &homeRunner{fakeRunner: fakeRunner{results: completedTurn()}}
			result, err := (Backend{Runner: runner, ConfigDir: home}).Run(context.Background(), backend.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: worktree, RepositoryRoot: repository,
				Prompt: "do the work", SystemPrompt: "the role's contract", Model: "gpt-6-astra", SessionID: session,
			})
			if err != nil {
				t.Fatalf("%s, session %q: %v", role, session, err)
			}
			args := runner.commands[0].Args
			if err := recordedContract(t).misplacedOption(args); err != nil {
				t.Fatalf("%s, session %q: %v", role, session, err)
			}
			// Every skill list and every plugin is switched off, on the level
			// ahead of resume, whatever the role.
			resume := slices.Index(args, "resume")
			if resume < 0 {
				resume = len(args)
			}
			for _, want := range [][2]string{
				{"--config", "skills.include_instructions=false"},
				{"--disable", "skill_search"},
				{"--disable", "skill_mcp_dependency_install"},
				{"--disable", "plugins"},
				{"--disable", "remote_plugin"},
				{"--disable", "apps"},
			} {
				found := false
				for index := 0; index+1 < resume; index++ {
					if args[index] == want[0] && args[index+1] == want[1] {
						found = true
					}
				}
				if !found {
					t.Fatalf("%s, session %q: %s %s missing before resume: %v", role, session, want[0], want[1], args)
				}
			}
			// The provider home's own instruction files are not in the home the
			// invocation ran under, and the account's authentication is.
			if runner.homes[0] == "" || runner.homes[0] == home {
				t.Fatalf("%s, session %q: ran under the account's own home %q, which holds its instruction files", role, session, runner.homes[0])
			}
			if len(runner.instructions[0]) != 0 {
				t.Fatalf("%s, session %q: the provider home held %v", role, session, runner.instructions[0])
			}
			if runner.auth[0] != `{"auth_mode":"chatgpt"}` {
				t.Fatalf("%s, session %q: the account's authentication was not reachable: %q", role, session, runner.auth[0])
			}
			if _, err := os.Stat(runner.homes[0]); !os.IsNotExist(err) {
				t.Fatalf("%s, session %q: the home made for the invocation outlived it: %v", role, session, err)
			}
			for _, marker := range []string{"PERSONAL_SKILL_MARKER", "PERSONAL_AGENTS_MARKER", "PERSONAL_OVERRIDE_MARKER"} {
				if strings.Contains(runner.prompts[0], marker) {
					t.Fatalf("%s, session %q: the prompt carries %s", role, session, marker)
				}
			}
			if !result.Loaded.Reported || result.Loaded.String() != "skills: none; plugins: none; instruction files: none" {
				t.Fatalf("%s, session %q: loaded = %+v", role, session, result.Loaded)
			}
			// The account's own files are untouched.
			if _, err := os.Stat(filepath.Join(home, "AGENTS.md")); err != nil {
				t.Fatalf("the account's instruction file was removed: %v", err)
			}
		}
	}
}

func TestAHomeWithNoInstructionFileIsUsedAsItIs(t *testing.T) {
	t.Parallel()
	home := personalHome(t)
	for _, name := range instructionFileNames {
		if err := os.Remove(filepath.Join(home, name)); err != nil {
			t.Fatal(err)
		}
	}
	repository, worktree := sandboxRepository(t, true)
	runner := &homeRunner{fakeRunner: fakeRunner{results: completedTurn()}}
	if _, err := (Backend{Runner: runner, ConfigDir: home}).Run(context.Background(), backend.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree, RepositoryRoot: repository,
		Prompt: "do the work", Model: "gpt-6-astra",
	}); err != nil {
		t.Fatal(err)
	}
	if runner.homes[0] != home {
		t.Fatalf("home = %q, want the account's own %q", runner.homes[0], home)
	}
	if strings.Contains(runner.prompts[0], "PERSONAL_SKILL_MARKER") {
		t.Fatal("the prompt carries the personal skill")
	}
}

func TestTheSkillsAndInstructionFilesTheProjectNamesAreLoaded(t *testing.T) {
	t.Parallel()
	named := backend.NamedContext{
		Skills: []backend.ContextFile{
			{Path: ".yoyodyne/skills/review", Roles: []domain.AgentRole{domain.RoleReviewer}},
		},
		Instructions: []backend.ContextFile{{Path: "docs/agent-notes.md"}},
	}
	for _, role := range []domain.AgentRole{domain.RoleReviewer, domain.RoleDeveloper} {
		for _, session := range []string{"", "session-1"} {
			home := personalHome(t)
			repository, worktree := sandboxRepository(t, false)
			for file, body := range map[string]string{
				".yoyodyne/skills/review/SKILL.md": "---\nname: careful-review\ndescription: review carefully\n---\nPROJECT_SKILL_BODY\n",
				"docs/agent-notes.md":              "PROJECT_NOTES_BODY\n",
			} {
				path := filepath.Join(worktree, filepath.FromSlash(file))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			runner := &homeRunner{fakeRunner: fakeRunner{results: completedTurn()}}
			result, err := (Backend{Runner: runner, ConfigDir: home, Context: named}).Run(context.Background(), backend.RunRequest{
				RunID: testRunID, Role: role, WorkingDirectory: worktree, RepositoryRoot: repository,
				Prompt: "do the work", SystemPrompt: "the role's contract", Model: "gpt-6-astra", SessionID: session,
			})
			if err != nil {
				t.Fatalf("%s: %v", role, err)
			}
			prompt := runner.prompts[0]
			if !strings.Contains(prompt, "PROJECT_NOTES_BODY") {
				t.Fatalf("%s: the named instruction file is not in the prompt:\n%s", role, prompt)
			}
			if strings.Index(prompt, "the role's contract") > strings.Index(prompt, "PROJECT_NOTES_BODY") ||
				strings.Index(prompt, "PROJECT_NOTES_BODY") > strings.Index(prompt, "do the work") {
				t.Fatalf("%s: the named context is not between the contract and the prompt:\n%s", role, prompt)
			}
			if strings.Contains(prompt, "PERSONAL_SKILL_MARKER") || strings.Contains(prompt, "PERSONAL_AGENTS_MARKER") {
				t.Fatalf("%s: a personal file reached the prompt", role)
			}
			// A read-only role reads the repository by its resolved path.
			directory := worktree
			if role == domain.RoleReviewer {
				directory = resolved(t, worktree)
			}
			notes := backend.LoadedItem{Name: "agent-notes.md", Source: backend.LoadedFromProjectConfiguration, Path: filepath.Join(directory, "docs", "agent-notes.md")}
			if !slices.Equal(result.Loaded.Instructions, []backend.LoadedItem{notes}) {
				t.Fatalf("%s: instructions = %+v", role, result.Loaded.Instructions)
			}
			if result.Loaded.Plugins != nil {
				t.Fatalf("%s: plugins = %+v", role, result.Loaded.Plugins)
			}
			if role == domain.RoleReviewer {
				if !strings.Contains(prompt, "PROJECT_SKILL_BODY") {
					t.Fatalf("the reviewer was not given the skill named for it:\n%s", prompt)
				}
				skill := backend.LoadedItem{Name: "careful-review", Source: backend.LoadedFromProjectConfiguration, Path: filepath.Join(directory, ".yoyodyne", "skills", "review", "SKILL.md")}
				if !slices.Equal(result.Loaded.Skills, []backend.LoadedItem{skill}) {
					t.Fatalf("skills = %+v", result.Loaded.Skills)
				}
				if want := "skills: careful-review (project configuration, " + skill.Path + "); plugins: none; instruction files: agent-notes.md (project configuration, " + notes.Path + ")"; result.Loaded.String() != want {
					t.Fatalf("summary = %q, want %q", result.Loaded.String(), want)
				}
			} else {
				if strings.Contains(prompt, "PROJECT_SKILL_BODY") || len(result.Loaded.Skills) != 0 {
					t.Fatalf("the developer was given a skill named only for the reviewer")
				}
			}
		}
	}
}

// resolved is a test directory as the read-only launch sees it, which is with
// the temporary directory's own links resolved.
func resolved(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestANamedFileThatCannotBeReadRefusesTheInvocation(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	runner := &fakeRunner{results: completedTurn()}
	_, err := (Backend{Runner: runner, ConfigDir: personalHome(t), Context: backend.NamedContext{
		Skills: []backend.ContextFile{{Path: ".yoyodyne/skills/missing"}},
	}}).Run(context.Background(), backend.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree, RepositoryRoot: repository,
		Prompt: "do the work", Model: "gpt-6-astra",
	})
	if err == nil || !strings.Contains(err.Error(), ".yoyodyne/skills/missing") {
		t.Fatalf("err = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("the provider was invoked without the skill the project named")
	}
}

func TestTheRepositorysOwnInstructionFileIsRecordedForADeveloper(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	if err := os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("the project's own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &homeRunner{fakeRunner: fakeRunner{results: completedTurn()}}
	result, err := (Backend{Runner: runner, ConfigDir: personalHome(t)}).Run(context.Background(), backend.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree, RepositoryRoot: repository,
		Prompt: "do the work", Model: "gpt-6-astra",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []backend.LoadedItem{{Name: "AGENTS.md", Source: backend.LoadedFromRepository, Path: filepath.Join(worktree, "AGENTS.md")}}
	if !slices.Equal(result.Loaded.Instructions, want) {
		t.Fatalf("instructions = %+v, want %+v", result.Loaded.Instructions, want)
	}
}

func TestTheStartingEventSaysWhatWasLoaded(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	var events []execution.Event
	runner := &fakeRunner{results: completedTurn()}
	if _, err := (Backend{Runner: runner, ConfigDir: personalHome(t)}).Run(context.Background(), backend.RunRequest{
		RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: worktree, RepositoryRoot: repository,
		Prompt: "review", Model: "gpt-6-astra",
		EventSink: func(event execution.Event) error { events = append(events, event); return nil },
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == execution.EventRunStarted {
			if !strings.Contains(string(event.Payload), `"loaded":"skills: none; plugins: none; instruction files: none"`) {
				t.Fatalf("run.started payload = %s", event.Payload)
			}
			return
		}
	}
	t.Fatalf("no run.started event in %+v", events)
}
