package checks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Running one command under exactly the restrictions a developer session's own
// commands run under, for an environment declared with where: developer
// (docs/designs/checking-a-run-environment.md, section 3).
//
// Runner.Run puts no sandbox on a declared check. It runs "/bin/sh -c" in the
// checkout as the harness's own user, with the environment built from the
// allowlist every run's process is built from, the provider CLIs hidden from
// its search path (searchpath.go), its own time limit and the stage's, and the
// machine's limit on heavy checks (permits.go). It may reach any host the
// machine can, write anywhere the harness's user can, and connect to socket
// files. Nothing about how it runs reproduces the developer session, so a
// check that passes there says nothing about whether the developer can run it.
//
// DeveloperRestrictions is that reproduction. A Claude Code developer's Bash
// runs under the CLI's macOS sandbox with the settings internal/backend/claudecode
// gives a developer (developerSettings): no network but binding, accepting and
// connecting on localhost, no socket file connected to or created, writes only
// to the worktree, the session's temporary directory, the repository's Git
// directory and the run's scratch folder, and the harness's services folder
// unreadable. The profile below states those rules for sandbox-exec, the same
// way internal/beads reproduces the network rules to measure the tracker's
// server (developerNetworkProfile there). It does not reproduce the CLI's
// filtering proxy, which a developer run is given no domain through, so a
// command here reaches no host but localhost, and neither does a developer's.

// sandboxExec is macOS's program for running a command under a sandbox
// profile.
const sandboxExec = "/usr/bin/sandbox-exec"

// deviceWrites are the device files every command writes to whatever its
// sandbox, which Claude Code's sandbox allows too.
var deviceWrites = []string{"/dev/null", "/dev/zero", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/dtracehelper"}

// DeveloperRestrictions are the folders a developer session's restrictions
// are stated in, for one run.
type DeveloperRestrictions struct {
	// Worktree is the run's worktree, which the command may write.
	Worktree string
	// Temporary is the session's temporary directory, GitDirectory the
	// repository's Git directory, and Scratch the run's scratch folder; the
	// command may write each that is set.
	Temporary    string
	GitDirectory string
	Scratch      string
	// Unreadable are folders the command may not read, such as the harness's
	// services folder.
	Unreadable []string
}

// Profile is the restrictions as a sandbox-exec profile. Every folder must
// exist, because a sandbox rule names a folder by where it really is.
func (r DeveloperRestrictions) Profile() (string, error) {
	if strings.TrimSpace(r.Worktree) == "" {
		return "", errors.New("the developer's restrictions are stated for a worktree, and none was given")
	}
	var writable []string
	for _, folder := range []string{r.Worktree, r.Temporary, r.GitDirectory, r.Scratch} {
		if strings.TrimSpace(folder) == "" {
			continue
		}
		resolved, err := sandboxPath(folder)
		if err != nil {
			return "", err
		}
		writable = append(writable, resolved)
	}
	var unreadable []string
	for _, folder := range r.Unreadable {
		resolved, err := sandboxPath(folder)
		if err != nil {
			return "", err
		}
		unreadable = append(unreadable, resolved)
	}
	var profile strings.Builder
	profile.WriteString("(version 1)\n(allow default)\n")
	// The network: nothing but localhost, which is what allowLocalBinding
	// permits. A socket file is a network-outbound or network-bind of its own,
	// so denying network* refuses connecting to one and creating one alike.
	profile.WriteString("(deny network*)\n")
	profile.WriteString("(allow network-bind (local ip \"localhost:*\"))\n")
	profile.WriteString("(allow network-inbound (local ip \"localhost:*\"))\n")
	profile.WriteString("(allow network-outbound (remote ip \"localhost:*\"))\n")
	profile.WriteString("(deny file-write*)\n(allow file-write*")
	for _, device := range deviceWrites {
		fmt.Fprintf(&profile, " (literal %s)", sandboxString(device))
	}
	profile.WriteString(" (regex #\"^/dev/fd/\")")
	for _, folder := range writable {
		fmt.Fprintf(&profile, " (subpath %s)", sandboxString(folder))
	}
	profile.WriteString(")\n")
	for _, folder := range unreadable {
		fmt.Fprintf(&profile, "(deny file-read* (subpath %s))\n", sandboxString(folder))
	}
	return profile.String(), nil
}

// Wrap is command run under the restrictions: sandbox-exec with the profile,
// then the command as it was. It is refused off macOS, where the developer
// session's sandbox is not reproduced. A process that is itself inside a
// sandbox, such as a developer's own Bash, cannot start one: sandbox-exec says
// "sandbox_apply: Operation not permitted" and exits 71 (DeveloperSandboxRefused).
func (r DeveloperRestrictions) Wrap(command execution.Command) (execution.Command, error) {
	if runtime.GOOS != "darwin" {
		return execution.Command{}, fmt.Errorf("the developer session's restrictions are reproduced only on macOS, and this is %s", runtime.GOOS)
	}
	if strings.TrimSpace(command.Name) == "" {
		return execution.Command{}, errors.New("a command to run under the developer's restrictions names no program")
	}
	profile, err := r.Profile()
	if err != nil {
		return execution.Command{}, err
	}
	wrapped := command
	wrapped.Name = sandboxExec
	wrapped.Args = append([]string{"-p", profile, command.Name}, command.Args...)
	if wrapped.Dir == "" {
		wrapped.Dir = r.Worktree
	}
	return wrapped, nil
}

// DeveloperSandboxRefused reports whether what sandbox-exec wrote says it
// could not apply its profile at all, which is what it says when the process
// starting it is already inside a sandbox. The command was never run.
func DeveloperSandboxRefused(stderr string) bool {
	return strings.Contains(stderr, "sandbox_apply: Operation not permitted")
}

// sandboxPath is a folder as a sandbox rule names it: absolute, existing, and
// with every link resolved, since /tmp and /var are links on macOS.
func sandboxPath(folder string) (string, error) {
	absolute, err := filepath.Abs(folder)
	if err != nil {
		return "", fmt.Errorf("folder %s for the developer's restrictions: %w", folder, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("folder %s for the developer's restrictions could not be resolved: %w", folder, err)
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return "", fmt.Errorf("folder %s for the developer's restrictions is not a folder", folder)
	}
	return resolved, nil
}

// sandboxString is value as a quoted string in a sandbox profile.
func sandboxString(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
