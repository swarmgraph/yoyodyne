package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// What a project names is put in front of a role by the harness rather than
// left for the provider's CLI to find: each skill's SKILL.md and each
// instruction file is read and added to what the role is sent, so what a role
// was given is what the configuration says, and the run records it. Every
// adapter reads them here, so a project's files reach a Claude Code role and a
// Codex role the same way. A relative path is read from the harness's own
// checkout and never from a worktree under review: a reviewer's standing
// instructions read from the candidate would be instructions the candidate's
// author wrote.

// ReadNamedContext reads what the project named for this role, and returns the text
// it adds to the prompt and what it loaded. A named file that cannot be read
// refuses the invocation: running without what the project said the role needs
// would be a different role, and nobody would be told.
func ReadNamedContext(named NamedContext, role domain.AgentRole, repository string) (string, []LoadedItem, []LoadedItem, error) {
	var sections []string
	var skills, instructions []LoadedItem
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
		skills = append(skills, LoadedItem{Name: name, Source: LoadedFromProjectConfiguration, Path: path})
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
		instructions = append(instructions, LoadedItem{Name: filepath.Base(path), Source: LoadedFromProjectConfiguration, Path: path})
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

// NamedRoot is where a relative named path is read from for this invocation:
// the harness's own checkout when the request names it, and for a developer, its
// worktree otherwise. A read-only role is never pointed at the directory it is
// inspecting, which for a reviewer is the candidate; with no checkout named, a
// relative path is refused for it instead.
func NamedRoot(request RunRequest) string {
	if strings.TrimSpace(request.RepositoryRoot) != "" {
		return request.RepositoryRoot
	}
	if PostureFor(request.Role) == PostureWorktreeWrite {
		return request.WorkingDirectory
	}
	return ""
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
	case repository == "":
		return "", fmt.Errorf("the project configuration names %q by a relative path, which a role that inspects a repository is never given from the repository it inspects; it is read from the harness's own checkout, which this invocation was not given", path)
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
