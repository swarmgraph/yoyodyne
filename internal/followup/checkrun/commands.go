package checkrun

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// DeclaredCommand is one command check a project declares by name: the command
// and its time limit. A follow-up names the check, never the command.
type DeclaredCommand struct {
	Command string
	Timeout time.Duration
}

// Commands runs the command checks a project declares, in its repository, as
// a configured check is run: through the shell, with nothing the project's
// own checks are not given.
type Commands struct {
	Declared map[string]DeclaredCommand
	// Directory is where each command runs: the repository.
	Directory string
	// Run runs one command line and reports its combined output; it defaults to
	// `sh -c` in Directory.
	Run func(ctx context.Context, directory, command string) (string, error)
}

// Evaluate runs one declared command check.
func (c Commands) Evaluate(ctx context.Context, check followup.Check) Finding {
	name := check.Arguments[followup.ArgName]
	declared, ok := c.Declared[name]
	if !ok || strings.TrimSpace(declared.Command) == "" {
		return Finding{CouldNotRun: fmt.Sprintf("the project declares no command check named %q", name)}
	}
	timeout := declared.Timeout
	if timeout <= 0 {
		timeout = DefaultEnvironmentTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run := c.Run
	if run == nil {
		run = runShell
	}
	output, err := run(ctx, c.Directory, declared.Command)
	said := oneline.Fold(strings.TrimSpace(output), 300)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return Finding{Passed: true, Detail: "the declared check " + name + " passed"}
	case ctx.Err() != nil:
		return Finding{CouldNotRun: fmt.Sprintf("the declared check %s did not finish within %s", name, timeout)}
	case !errors.As(err, &exit):
		return Finding{CouldNotRun: "the declared check " + name + " could not be started: " + oneline.Fold(err.Error(), 300)}
	case said == "":
		return Finding{Detail: "the declared check " + name + " failed: " + err.Error()}
	}
	return Finding{Detail: "the declared check " + name + " failed: " + said}
}

func runShell(ctx context.Context, directory, command string) (string, error) {
	shell := exec.CommandContext(ctx, "sh", "-c", command)
	shell.Dir = directory
	output, err := shell.CombinedOutput()
	return string(output), err
}
