//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package execution

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// gateSupported reports that a launch can be held behind a gate here: the
// process leads a group of its own, and a held file lock is released only when
// the last process holding it exits.
const gateSupported = true

// configureProcessTree makes the command the leader of a new process group and
// replaces CommandContext's single-process cancellation with a group kill.
// Shell checks frequently spawn descendants that inherit stdout and stderr; if
// only the shell dies, those descendants can keep the pipes open indefinitely.
func configureProcessTree(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
}

// reapProcessTree kills whatever the command left running in its group, and is
// called once the command itself has been waited for — on every way out, not
// only the cancellation Cancel above covers. A command that backgrounds work
// with its output pointed away from these pipes and then exits reports success
// while that work keeps the machine's cores: nothing cancelled the context, so
// nothing killed the group, and the result says only that the command finished.
// That is how twenty-four spin loops from one run's load test outlived it by
// hours and starved the run working beside it.
//
// The pid is the group's, because Setpgid made this command the leader of it,
// and a group id stays reserved while any member of the group is alive — so in
// the moment between Wait and here it cannot have been handed to somebody
// else's group. Where the group is empty the kill fails with ESRCH, which is
// the ordinary case and says the command left nothing behind; any other failure
// is a signal this process was not permitted to send, which is nothing a caller
// can act on either. So a reap is attempted and never reported, and what bounds
// the case where it does not reach far enough — work that put itself in a
// session of its own — is the timeout that work carries, not this.
func reapProcessTree(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

// processGroupOf is the group a started command leads: Setpgid above made it
// the leader of a group numbered by its own process identifier.
func processGroupOf(command *exec.Cmd) int {
	return command.Process.Pid
}
