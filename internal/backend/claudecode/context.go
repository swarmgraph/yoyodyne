package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// What a Claude Code invocation is given beside its prompt.
//
// The CLI finds settings, memory, skills, plugins, connectors, and instruction
// files on its own, most of them in the account's home: the user settings file
// and the hooks, plugins, and permissions it names; the auto-memory index kept
// per project under the home; the skills and agents in the home's directories;
// the MCP servers in the home's own configuration; the connectors attached to
// the claude.ai account; and the home's CLAUDE.md and rules. A harness
// invocation made under the operator's login therefore carried his memory
// index, his skills, and his claude.ai connectors, one of which sends mail from
// his account (yoyodyne-ifd.435.22).
//
// What turns each one off was read from Claude Code 2.1.286 itself, its help and
// the settings handling in the shipped executable, with no provider call:
//
//   - `--setting-sources` names which settings files are read: user, project,
//     local. Settings passed with `--settings` and admin-managed policy are read
//     whatever it says. The user source also gates the home's CLAUDE.md and
//     rules, its skills and agents, plugin and skill sync from claude.ai, and the
//     plugins the user settings enable. A developer reads the project source
//     alone, which is its worktree's own checked-in settings and CLAUDE.md; every
//     other role reads none, so the repository it inspects cannot configure it.
//   - `--strict-mcp-config` reads MCP servers only from `--mcp-config`, which is
//     never passed, so no MCP server reaches a role from anywhere.
//   - `disableClaudeAiConnectors` in the settings passed, and
//     ENABLE_CLAUDEAI_MCP_SERVERS=false in the environment, each keep the
//     claude.ai account's connectors from being fetched or connected.
//   - `autoMemoryEnabled: false` in the settings passed, and
//     CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 in the environment, each keep the
//     auto-memory directory from being read or written.
//   - `--disable-slash-commands` disables every skill, wherever it was found,
//     the repository's own and the CLI's bundled ones among them, so a role has
//     the skills the project names and no others.
//   - The CLI reads CLAUDE.md from the working directory and every directory
//     above it as project instructions, so a developer would also read one left
//     in a directory above its worktree, the operator's home among them.
//     `claudeMdExcludes` in the settings passed names each of those so it is
//     skipped; the worktree's own are left.
//
// Every one of these goes on every invocation, initial and resumed: a resumed
// session reads its settings afresh, so isolation that only the first turn had
// would end on the second.

// Settings every invocation carries, whatever its role.
var contextSettings = map[string]any{
	"autoMemoryEnabled":         false,
	"disableClaudeAiConnectors": true,
}

// contextEnvironment is the environment every invocation carries, replacing
// whatever the harness's own environment says for each, since an operator's
// shell that forced memory or connectors on would otherwise carry through.
var contextEnvironment = [][2]string{
	{"CLAUDE_CODE_DISABLE_AUTO_MEMORY", "1"},
	{"ENABLE_CLAUDEAI_MCP_SERVERS", "false"},
}

// Which settings files an invocation reads: one that writes its worktree — a
// developer — that worktree's checked-in settings, and every other invocation
// none, including a developer whose role definition holds it read-only.
const (
	developerSettingSources = "project"
	readOnlySettingSources  = ""
)

// contextArgs are the flags every invocation carries, initial and resumed,
// beside the settings the caller passes with --settings. writesWorktree is
// whether the invocation is held to worktree-write access.
func contextArgs(writesWorktree bool) []string {
	sources := readOnlySettingSources
	if writesWorktree {
		sources = developerSettingSources
	}
	return []string{"--setting-sources", sources, "--strict-mcp-config", "--disable-slash-commands"}
}

// settingsFor is the settings an invocation is passed with --settings: base,
// which for a developer carries its sandbox and guard, with the context
// settings added, and for an invocation that writes its worktree the
// instruction files above that worktree excluded.
func settingsFor(base string, writesWorktree bool, directory string) (string, error) {
	settings := map[string]any{}
	if base != "" {
		if err := json.Unmarshal([]byte(base), &settings); err != nil {
			return "", fmt.Errorf("decode the settings an invocation is given: %w", err)
		}
	}
	for key, value := range contextSettings {
		settings[key] = value
	}
	if writesWorktree {
		settings["claudeMdExcludes"] = instructionFilesAbove(directory)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("encode the settings an invocation is given: %w", err)
	}
	return string(encoded), nil
}

// instructionFilesAbove is every instruction file the CLI would read from a
// directory above the working directory, as patterns for claudeMdExcludes. It
// names the directory as given and as its links resolve, because the CLI may
// walk either.
func instructionFilesAbove(directory string) []string {
	var patterns []string
	seen := map[string]bool{}
	starts := []string{filepath.Clean(directory)}
	if resolved, err := filepath.EvalSymlinks(directory); err == nil && resolved != starts[0] {
		starts = append(starts, resolved)
	}
	for _, start := range starts {
		for parent := filepath.Dir(start); ; parent = filepath.Dir(parent) {
			if !seen[parent] {
				seen[parent] = true
				for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md", filepath.Join(".claude", "CLAUDE.md")} {
					patterns = append(patterns, globLiteral(filepath.ToSlash(filepath.Join(parent, name))))
				}
				patterns = append(patterns, globLiteral(filepath.ToSlash(filepath.Join(parent, ".claude", "rules")))+"/**")
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
	}
	return patterns
}

// globLiteral is path as a glob pattern matching only itself: every character
// a glob gives a meaning to is escaped.
func globLiteral(path string) string {
	var escaped strings.Builder
	for _, character := range path {
		if strings.ContainsRune(`\*?[]{}()!+@`, character) {
			escaped.WriteRune('\\')
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

// withEnvironmentValue is environment with name set to value, replacing any
// value the environment already carries for it.
func withEnvironmentValue(environment []string, name, value string) []string {
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, name+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, name+"="+value)
}

// withContextEnvironment is environment with every context variable set.
func withContextEnvironment(environment []string) []string {
	for _, variable := range contextEnvironment {
		environment = withEnvironmentValue(environment, variable[0], variable[1])
	}
	return environment
}

// repositoryContext is what a developer reads from its own worktree under the
// project settings source: the checked-in settings file, the plugins it
// enables, and the instruction files at the worktree's root. They are the
// repository's rather than anybody's personal ones, so they stay; they are
// recorded so the run says they were there. A role reading no settings source
// reads none of them. Instruction files the CLI picks up later, from a
// subdirectory the agent reads in, are the repository's too and are not listed.
func repositoryContext(directory string) (settings, plugins, instructions []backend.LoadedItem) {
	settingsPath := filepath.Join(directory, ".claude", "settings.json")
	if content, err := os.ReadFile(settingsPath); err == nil {
		settings = append(settings, backend.LoadedItem{Name: ".claude/settings.json", Source: backend.LoadedFromRepository, Path: settingsPath})
		var declared struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(content, &declared) == nil {
			var names []string
			for name, enabled := range declared.EnabledPlugins {
				if enabled {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				plugins = append(plugins, backend.LoadedItem{Name: name, Source: backend.LoadedFromRepository, Path: settingsPath})
			}
		}
	}
	for _, name := range []string{"CLAUDE.md", filepath.Join(".claude", "CLAUDE.md")} {
		path := filepath.Join(directory, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
			instructions = append(instructions, backend.LoadedItem{Name: filepath.ToSlash(name), Source: backend.LoadedFromRepository, Path: path})
		}
	}
	return settings, plugins, instructions
}

// appendNamed is the system prompt with what the project named added after it,
// so it reads as part of the role's standing instructions rather than the task.
func appendNamed(systemPrompt, named string) string {
	switch {
	case strings.TrimSpace(named) == "":
		return systemPrompt
	case strings.TrimSpace(systemPrompt) == "":
		return named
	default:
		return systemPrompt + "\n\n" + named
	}
}
