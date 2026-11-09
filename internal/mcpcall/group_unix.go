//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcpcall

import (
	"os"
	"os/exec"
	"syscall"
)

// groupSupported says a command server can be given a process group of its own.
const groupSupported = true

// configureGroup makes a command server the leader of a process group of its
// own, and has the batch's context end the whole group rather than the one
// process, so anything the server started goes with it.
func configureGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		killGroup(command)
		return nil
	}
}

// killGroup kills whatever is left of the server's group. The group's number
// is the server's own, and stays reserved while any member of it is alive, so
// it cannot name somebody else's group; where the group is already empty the
// kill fails with nothing to report.
func killGroup(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
