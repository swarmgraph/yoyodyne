package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
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
// What the project names is put in front of the role by the harness rather than
// left for the CLI to find: each skill's SKILL.md and each instruction file is
// read and added to the prompt, so what a role was given is what the
// configuration says, and the run records it.

// contextArgs are the settings every invocation carries, initial and resumed,
// whatever its role. They go on `exec` ahead of `resume`, which is the level
// whose help lists them.
func contextArgs() []string {
	args := []string{"--config", "skills.include_instructions=false"}
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

// prepareProviderHome is the provider home an invocation runs under. Where the
// account's home holds no instruction file it is that home and nothing is made;
// otherwise it is a temporary directory linking every other entry of the
// account's home, which the caller removes when the invocation ends. Removing it
// removes the links and never what they point at.
func prepareProviderHome(home string) (prepared string, made bool, err error) {
	personal := false
	for _, name := range instructionFileNames {
		if _, err := os.Lstat(filepath.Join(home, name)); err == nil {
			personal = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("inspect the Codex provider home: %w", err)
		}
	}
	if !personal {
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

// namedContext reads what the project named for this role, and returns the text
// it adds to the prompt and what it loaded. A named file that cannot be read
// refuses the invocation: running without what the project said the role needs
// would be a different role, and nobody would be told.
func namedContext(named backend.NamedContext, role domain.AgentRole, repository string) (string, []backend.LoadedItem, []backend.LoadedItem, error) {
	var sections []string
	var skills, instructions []backend.LoadedItem
	for _, file := range named.Skills {
		if !file.AppliesTo(role) {
			continue
		}
		path, err := namedPath(file.Path, repository)
		if err != nil {
			return "", nil, nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			path = filepath.Join(path, "SKILL.md")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", nil, nil, fmt.Errorf("read the skill %q the project configuration names: %w", file.Path, err)
		}
		name := skillName(string(content), path)
		skills = append(skills, backend.LoadedItem{Name: name, Source: backend.LoadedFromProjectConfiguration, Path: path})
		sections = append(sections, fmt.Sprintf("## Skill %s (%s; its other files are in %s)\n\n%s", name, path, filepath.Dir(path), strings.TrimSpace(string(content))))
	}
	for _, file := range named.Instructions {
		if !file.AppliesTo(role) {
			continue
		}
		path, err := namedPath(file.Path, repository)
		if err != nil {
			return "", nil, nil, err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", nil, nil, fmt.Errorf("read the instruction file %q the project configuration names: %w", file.Path, err)
		}
		instructions = append(instructions, backend.LoadedItem{Name: filepath.Base(path), Source: backend.LoadedFromProjectConfiguration, Path: path})
		sections = append(sections, fmt.Sprintf("## Instructions from %s\n\n%s", path, strings.TrimSpace(string(content))))
	}
	if len(sections) == 0 {
		return "", nil, nil, nil
	}
	text := "# Skills and instructions this project's configuration names\n\n" +
		"Follow a skill when the task matches what it describes.\n\n" +
		strings.Join(sections, "\n\n")
	return text, skills, instructions, nil
}

// namedPath is where a named file is: an absolute path as written, "~/" under
// the home directory of whoever runs the harness, and anything else in the
// repository the invocation works in.
func namedPath(path, repository string) (string, error) {
	path = strings.TrimSpace(path)
	switch {
	case path == "":
		return "", errors.New("the project configuration names a skill or instruction file with no path")
	case path == "~" || strings.HasPrefix(path, "~/"):
		user, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", path, err)
		}
		return filepath.Join(user, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/")), nil
	case filepath.IsAbs(path):
		return filepath.Clean(path), nil
	default:
		return filepath.Join(repository, filepath.FromSlash(path)), nil
	}
}

// skillName is the name a SKILL.md gives itself in its front matter, and the
// name of the directory holding it where it gives none.
func skillName(content, path string) string {
	if rest, found := strings.CutPrefix(content, "---"); found {
		if front, _, closed := strings.Cut(rest, "\n---"); closed {
			for _, line := range strings.Split(front, "\n") {
				if value, ok := strings.CutPrefix(strings.TrimSpace(line), "name:"); ok {
					if name := strings.Trim(strings.TrimSpace(value), `"'`); name != "" {
						return name
					}
				}
			}
		}
	}
	return filepath.Base(filepath.Dir(path))
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
