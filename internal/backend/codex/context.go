package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// What a Codex invocation is given beside its prompt.
//
// The CLI finds skills, plugins, and instruction files on its own: skills in the
// provider home's skills directory, in ~/.agents/skills, in the repository's
// .agents/skills, and wherever the home's config.toml points; plugins from the
// home's configuration; and instruction files in the provider home's AGENTS.md
// or AGENTS.override.md as well as the repository's. A harness invocation made
// under the operator's login therefore carried his skills and instructions,
// which no role contract or persona named (yoyodyne-ifd.435.19).
//
// What turns each one off was read from codex-cli 0.160.0 itself, with no
// provider call: `codex debug prompt-input` renders the model-visible input, and
// a provider home holding a marked skill and a marked AGENTS.md showed both in
// it. `skills.include_instructions=false` removes the whole skill list, from
// every root; disabling `skill_search` and `skill_mcp_dependency_install` keeps
// the model from finding one another way; disabling `plugins`, `remote_plugin`,
// and `apps` keeps plugins out. Nothing turns off the provider home's own
// AGENTS.md — no configuration key or flag names it, and `--ignore-user-config`
// does not reach it — so where the home has one the invocation runs under a
// home the harness makes for it: every entry of the account's home linked in,
// the instruction files left out. Authentication, sessions, and configuration
// are the account's own through those links, so resume and the effort rule are
// unchanged; codex-cli 0.160.0 was seen to save auth.json through a link rather
// than replace it, so a login refreshed during a run lands in the account's home.
//
// The account's config.toml can carry instruction text too, and a developer's
// turn still reads that file. `developer_instructions` is overridden with an
// empty value on every turn, which the same rendering showed removes it.
// `model_instructions_file`, which replaces Codex's own base instructions, and
// the compaction prompt keys cannot be reset from the command line — an empty
// file path is refused — so where the account's file sets one at its top level,
// the home the harness makes carries a copy of config.toml without those lines.
//
// What the project names is read by the harness and added to the prompt; see
// internal/backend/namedcontext.go.

// contextArgs are the settings every invocation carries, initial and resumed,
// whatever its role. They go on `exec` ahead of `resume`, which is the level
// whose help lists them.
func contextArgs() []string {
	args := []string{"--config", "skills.include_instructions=false", "--config", `developer_instructions=""`}
	for _, feature := range []string{"skill_search", "skill_mcp_dependency_install", "plugins", "remote_plugin", "apps"} {
		args = append(args, "--disable", feature)
	}
	return args
}

// instructionFileNames are the instruction files Codex reads from a
// directory, the provider home among them, in the order it prefers them.
var instructionFileNames = []string{"AGENTS.override.md", "AGENTS.md"}

// providerHome is the home the CLI would read for this invocation: the account
// it names, the one the harness's environment names, or the CLI's own default.
func providerHome(configDir, workingDirectory string) (string, error) {
	home := strings.TrimSpace(configDir)
	if home == "" {
		home = os.Getenv(ProviderHomeVariable)
	}
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find the Codex provider home: %w", err)
		}
		return filepath.Join(user, ".codex"), nil
	}
	if !filepath.IsAbs(home) {
		home = filepath.Join(workingDirectory, home)
	}
	return home, nil
}

// configInstructionKeys are the top-level config.toml keys that put instruction
// text in front of the model and cannot be reset from the command line.
var configInstructionKeys = []string{"model_instructions_file", "experimental_compact_prompt_file", "compact_prompt"}

// withoutInstructionKeys is config.toml without the top-level lines that set an
// instruction key, a multi-line string value included, and whether any were
// there. Tables are left alone: a key under a table header is not the top-level
// setting, and a profile applies only to an invocation that names it.
func withoutInstructionKeys(content string) (string, bool) {
	var kept []string
	removed, skipping, closing, topLevel := false, false, "", true
	for _, line := range strings.SplitAfter(content, "\n") {
		if skipping {
			if strings.Contains(line, closing) {
				skipping = false
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			topLevel = false
		}
		if topLevel {
			if key, value, found := strings.Cut(trimmed, "="); found && slices.Contains(configInstructionKeys, strings.Trim(strings.TrimSpace(key), `"'`)) {
				removed = true
				value = strings.TrimSpace(value)
				for _, quote := range []string{`"""`, "'''"} {
					if strings.HasPrefix(value, quote) && !strings.Contains(value[len(quote):], quote) {
						skipping, closing = true, quote
					}
				}
				continue
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, ""), removed
}

// prepareProviderHome is the provider home an invocation runs under. Where the
// account's home holds no instruction file and its config.toml sets no
// instruction key it is that home and nothing is made; otherwise it is a
// temporary directory linking every other entry of the account's home, with a
// copy of config.toml that leaves the instruction keys out, which the caller
// removes when the invocation ends. Removing it removes the links and the copy
// and never what the links point at.
func prepareProviderHome(home string) (prepared string, made bool, err error) {
	personal := false
	for _, name := range instructionFileNames {
		if _, err := os.Lstat(filepath.Join(home, name)); err == nil {
			personal = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("inspect the Codex provider home: %w", err)
		}
	}
	config, filtered := "", false
	if content, err := os.ReadFile(filepath.Join(home, "config.toml")); err == nil {
		config, filtered = withoutInstructionKeys(string(content))
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("read the Codex provider home's configuration: %w", err)
	}
	if !personal && !filtered {
		return home, false, nil
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return "", false, fmt.Errorf("read the Codex provider home: %w", err)
	}
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", false, fmt.Errorf("resolve temporary directory: %w", err)
	}
	prepared, err = os.MkdirTemp(temporary, "yoyodyne-codex-home-")
	if err != nil {
		return "", false, fmt.Errorf("create the Codex provider home: %w", err)
	}
	root, err := repowrite.OpenPinnedRoot(prepared)
	if err != nil {
		_ = os.RemoveAll(prepared)
		return "", false, fmt.Errorf("open the Codex provider home: %w", err)
	}
	defer root.Close()
	for _, entry := range entries {
		if isPersonalInstructionFile(entry.Name()) {
			continue
		}
		if entry.Name() == "config.toml" && filtered {
			if err := root.WriteFile("config.toml", []byte(config), 0o600, true); err != nil {
				_ = os.RemoveAll(prepared)
				return "", false, fmt.Errorf("write the Codex provider home's configuration: %w", err)
			}
			continue
		}
		if err := root.Symlink(filepath.Join(home, entry.Name()), entry.Name()); err != nil {
			_ = os.RemoveAll(prepared)
			return "", false, fmt.Errorf("link %s into the Codex provider home: %w", entry.Name(), err)
		}
	}
	return prepared, true, nil
}

func isPersonalInstructionFile(name string) bool {
	for _, personal := range instructionFileNames {
		if name == personal {
			return true
		}
	}
	return false
}

// withProviderHome is environment with the provider home set to home, replacing
// any the environment already names.
func withProviderHome(environment []string, home string) []string {
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, ProviderHomeVariable+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, ProviderHomeVariable+"="+home)
}

// repositoryInstructions is the instruction file Codex reads from the directory
// it is launched in, which for a developer is the worktree's root. It is the
// repository's own file rather than anybody's personal one, so it stays; it is
// recorded so the run says it was there. A read-only role is launched from an
// empty directory and reads none.
func repositoryInstructions(directory string) []backend.LoadedItem {
	for _, name := range instructionFileNames {
		path := filepath.Join(directory, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
			return []backend.LoadedItem{{Name: name, Source: backend.LoadedFromRepository, Path: path}}
		}
	}
	return nil
}
