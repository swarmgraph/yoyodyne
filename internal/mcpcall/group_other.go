//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package mcpcall

import "os/exec"

// groupSupported says a command server cannot be given a process group of its
// own here, so none is started: a server whose descendants could outlive the
// batch is not one the client runs.
const groupSupported = false

func configureGroup(*exec.Cmd) {}

func killGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
