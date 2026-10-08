package config

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// PathCheck is one check the per-run gate runs only for a change that touches
// what it vouches for: a walkthrough of the documented install, say, which a
// change to the documentation or to the program it documents can break and a
// change to anything else cannot.
// GateCheckCommands is every command a run's check stage can run: the
// configured checks and the path checks, in that order. It is what a reading
// of past check stages is held to, so a check changed or taken out of the
// configuration stops being reported about.
func (c Config) GateCheckCommands() []string {
	commands := append([]string{}, c.Checks...)
	for _, check := range c.PathChecks {
		commands = append(commands, check.Command)
	}
	return commands
}

type PathCheck struct {
	// Command is the check, run through "/bin/sh -c" in the run's worktree like
	// every entry in Checks.
	Command string `yaml:"command" json:"command"`
	// Paths is the repository-relative file listing the paths the check vouches
	// for, one pattern a line. It is read from the worktree of the change under
	// test, and only decides for a change that leaves it untouched: a change that
	// edits the list runs the check whatever the list now says, so no change can
	// narrow the check that holds it.
	Paths string `yaml:"paths" json:"paths"`
	// NeedsProviderCLIs runs this check with the provider CLIs left on its
	// search path, where every other check has them hidden: it is for a check
	// whose whole point is how the harness drives a real one, such as the Codex
	// native-resume test. It reaches no agent: a check is the project's own
	// command, run by the harness, and the hiding it lifts is about where a
	// test can pass, not about what anything may do.
	NeedsProviderCLIs bool `yaml:"needs_provider_clis,omitempty" json:"needs_provider_clis,omitempty"`
}

func (c PathCheck) problems(index int) []string {
	var problems []string
	if strings.TrimSpace(c.Command) == "" {
		problems = append(problems, fmt.Sprintf("path check %d: command cannot be empty", index))
	}
	paths := strings.TrimSpace(c.Paths)
	switch {
	case paths == "":
		problems = append(problems, fmt.Sprintf("path check %d: paths must name the file listing what the check vouches for", index))
	case filepath.IsAbs(paths) || path.IsAbs(filepath.ToSlash(paths)):
		problems = append(problems, fmt.Sprintf("path check %d: paths %q must be relative to the repository", index, c.Paths))
	default:
		cleaned := path.Clean(filepath.ToSlash(paths))
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			problems = append(problems, fmt.Sprintf("path check %d: paths %q must name a file inside the repository", index, c.Paths))
		}
	}
	return problems
}
