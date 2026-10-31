package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Install steps: the commands a project declares to put its dependencies in a
// checkout before anything runs the project's code there
// (docs/designs/project-dependencies-in-runs.md).
//
// The harness knows nothing about any package manager. It runs the command,
// reads the files the step names as deciding what it installs, and looks for
// the folders it names as what it produces. The rules a step is held to when
// the configuration is read are the ones that keep installed packages out of
// every change: an input is tracked by Git, so its content is something a
// change can carry and a digest of it means something; an output is ignored by
// Git and inside the repository, so nothing the step writes can reach a commit
// or a diff, or land anywhere but the checkout it was run in; and no two steps
// produce the same folder, so neither can undo the other.
//
// A step is the operator's to write and never a role's (RoleMayApply), because
// a command that fetches from the network and runs what it fetched decides what
// reaches this machine.

// DefaultInstallTimeLimit is how long one attempt at a step may run where the
// step names no time_limit.
const DefaultInstallTimeLimit = 10 * time.Minute

// InstallStep is one declared step.
type InstallStep struct {
	// Name is what the step is called wherever it is shown, such as "npm
	// packages".
	Name string `yaml:"name" json:"name"`
	// Run is the command, run through "/bin/sh -c" in the root of the checkout
	// like every entry in Checks.
	Run string `yaml:"run" json:"run"`
	// Inputs are the files, relative to the repository root, whose content
	// decides what the step installs. Each is tracked by Git.
	Inputs []string `yaml:"inputs" json:"inputs"`
	// Outputs are the folders, relative to the repository root, the step
	// produces. Each is ignored by Git.
	Outputs []string `yaml:"outputs" json:"outputs"`
	// TimeLimit bounds one attempt. Zero is DefaultInstallTimeLimit.
	TimeLimit Duration `yaml:"time_limit,omitempty" json:"time_limit,omitempty"`
}

// Limit is how long one attempt at the step may run.
func (s InstallStep) Limit() time.Duration {
	if s.TimeLimit <= 0 {
		return DefaultInstallTimeLimit
	}
	return s.TimeLimit.Duration()
}

// installProblems holds every step to the rules that need no repository: each
// has a name, a command, at least one input and one output, every path stays
// inside the repository, and no two steps share a name or an output.
func installProblems(steps []InstallStep) []string {
	var problems []string
	names := map[string]int{}
	outputs := map[string]int{}
	for index, step := range steps {
		label := fmt.Sprintf("install step %d", index)
		name := strings.TrimSpace(step.Name)
		if name == "" {
			problems = append(problems, label+": name cannot be empty")
		} else {
			label = fmt.Sprintf("install step %q", name)
			if earlier, seen := names[name]; seen {
				problems = append(problems, fmt.Sprintf("%s: install step %d has the same name", label, earlier))
			} else {
				names[name] = index
			}
		}
		if strings.TrimSpace(step.Run) == "" {
			problems = append(problems, label+": run cannot be empty")
		}
		if step.TimeLimit < 0 {
			problems = append(problems, fmt.Sprintf("%s: time_limit cannot be negative; leave it out for %s", label, DefaultInstallTimeLimit))
		}
		if len(step.Inputs) == 0 {
			problems = append(problems, label+": inputs must name the tracked files whose content decides what the step installs")
		}
		if len(step.Outputs) == 0 {
			problems = append(problems, label+": outputs must name the ignored folders the step produces")
		}
		for _, input := range step.Inputs {
			if _, problem := installPath(input); problem != "" {
				problems = append(problems, fmt.Sprintf("%s: input %q %s", label, input, problem))
			}
		}
		for _, output := range step.Outputs {
			cleaned, problem := installPath(output)
			if problem != "" {
				problems = append(problems, fmt.Sprintf("%s: output %q %s", label, output, problem))
				continue
			}
			if earlier, seen := outputs[cleaned]; seen {
				problems = append(problems, fmt.Sprintf("%s: output %q is also an output of install step %d; two steps cannot produce the same folder", label, output, earlier))
				continue
			}
			outputs[cleaned] = index
		}
	}
	return problems
}

// installPath is a step's path cleaned to repository-relative slash form, or
// what is wrong with it.
func installPath(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", "cannot be empty"
	}
	if filepath.IsAbs(trimmed) || path.IsAbs(filepath.ToSlash(trimmed)) {
		return "", "must be relative to the repository root"
	}
	cleaned := path.Clean(filepath.ToSlash(trimmed))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", "lies outside the repository root"
	}
	return cleaned, ""
}

// installRepositoryProblems holds every step to the rules that need the
// repository: each input is tracked by Git, each output is ignored by Git with
// nothing tracked inside it, and no output that already exists resolves,
// through a link, outside the repository. A repository Git cannot read refuses
// every step, rather than passing steps nothing could check.
func installRepositoryProblems(ctx context.Context, steps []InstallStep, repository string) []string {
	if len(steps) == 0 {
		return nil
	}
	root, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return []string{fmt.Sprintf("install steps are checked against the repository %s, which could not be read: %v", repository, err)}
	}
	if _, err := installGit(ctx, root, "rev-parse", "--git-dir"); err != nil {
		return []string{fmt.Sprintf("install steps are checked against the repository %s, which Git cannot read: %v", repository, err)}
	}
	var problems []string
	for index, step := range steps {
		label := fmt.Sprintf("install step %d", index)
		if name := strings.TrimSpace(step.Name); name != "" {
			label = fmt.Sprintf("install step %q", name)
		}
		for _, input := range step.Inputs {
			cleaned, problem := installPath(input)
			if problem != "" {
				continue
			}
			if _, err := installGit(ctx, root, "ls-files", "--error-unmatch", "--", cleaned); err != nil {
				problems = append(problems, fmt.Sprintf("%s: input %s is not tracked by Git in %s; an input is a file whose content a change carries, so add it with git add or name one that is", label, cleaned, root))
			}
		}
		for _, output := range step.Outputs {
			cleaned, problem := installPath(output)
			if problem != "" {
				continue
			}
			if outside := installOutputOutsideRoot(root, cleaned); outside != "" {
				problems = append(problems, fmt.Sprintf("%s: output %s resolves to %s, outside the repository root %s", label, cleaned, outside, root))
				continue
			}
			tracked, err := installGit(ctx, root, "ls-files", "--", cleaned)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: whether Git tracks anything in output %s could not be read: %v", label, cleaned, err))
				continue
			}
			if first, _, _ := strings.Cut(strings.TrimSpace(tracked), "\n"); first != "" {
				problems = append(problems, fmt.Sprintf("%s: output %s holds %s, which Git tracks; an output holds nothing a change carries, so remove it with git rm --cached", label, cleaned, first))
				continue
			}
			// The trailing slash asks about the folder, so a pattern written
			// either as "node_modules" or "node_modules/" answers for a folder
			// the step has not made yet.
			if _, err := installGit(ctx, root, "check-ignore", "--quiet", "--", cleaned+"/"); err != nil {
				problems = append(problems, fmt.Sprintf("%s: output %s is not ignored by Git in %s; add it to .gitignore so nothing the step installs can reach a commit", label, cleaned, root))
				continue
			}
		}
	}
	return problems
}

// installOutputOutsideRoot is where an existing output, or the nearest folder
// above it that exists, resolves to when that is outside the root, and empty
// otherwise. A folder that is a link elsewhere would have the step write
// outside the checkout it was run in.
func installOutputOutsideRoot(root, cleaned string) string {
	candidate := filepath.Join(root, filepath.FromSlash(cleaned))
	for candidate != root {
		if _, err := os.Lstat(candidate); err == nil {
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return ""
			}
			relative, err := filepath.Rel(root, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return resolved
			}
			return ""
		}
		candidate = filepath.Dir(candidate)
	}
	return ""
}

// installGitTimeout bounds each Git question asked of the repository while the
// configuration is read.
const installGitTimeout = 30 * time.Second

func installGit(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, installGitTimeout)
	defer cancel()
	// A path is a path rather than a pattern, so ls-files is told so;
	// check-ignore reads paths already and refuses the option.
	global := []string{"-C", root}
	if len(args) > 0 && args[0] == "ls-files" {
		global = append(global, "--literal-pathspecs")
	}
	command := exec.CommandContext(ctx, "git", append(global, args...)...)
	command.Env = execution.GitEnvironment(nil)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return "", errors.New(said)
		}
		return "", err
	}
	return stdout.String(), nil
}

// installRepository is the repository a configuration's install steps are
// checked against: product.repository, read from the directory the
// configuration is kept in.
func installRepository(configPath string, product Product) string {
	repository := filepath.FromSlash(strings.TrimSpace(product.Repository))
	if filepath.IsAbs(repository) {
		return filepath.Clean(repository)
	}
	return filepath.Join(ProjectDirectory(configPath), repository)
}
