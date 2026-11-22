//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervise

import (
	"errors"
	"fmt"
	"syscall"
)

// terminate sends the stop signal every harness process answers: the one that
// cancels its work and, past its grace, ends it. A process that is already
// gone is reported so rather than signalled, because a stop of what has
// already stopped is not a failure.
func terminate(pid int) error {
	err := syscall.Kill(pid, syscall.SIGTERM)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.ESRCH):
		return fmt.Errorf("pid %d is not running", pid)
	default:
		return err
	}
}

// killTree ends a process that did not stop when asked, and everything it
// started. It is frozen first, so it starts nothing more while its children are
// found; each child that leads a process group of its own — every command the
// harness runs leads one (execution.configureProcessTree), the AI sessions a
// scheduler hosts among them — is killed with its whole group, and the process
// last. The children have to be found before the process dies, because a
// child outlives its parent and is then nobody's to find: a developer session
// left running after its scheduler was killed would go on writing in a
// worktree the next scheduler continues the run in.
func killTree(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	own := syscall.Getpgrp()
	children, listErr := childrenOf(pid)
	for _, child := range children {
		target := child.pid
		if child.group == child.pid && child.group != own {
			target = -child.group
		}
		_ = syscall.Kill(target, syscall.SIGKILL)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if listErr != nil {
		// The process is killed either way; the children that could not be
		// found are said, because one may still be running.
		return fmt.Errorf("pid %d was killed, and the processes it had started could not be listed to stop them: %w", pid, listErr)
	}
	return nil
}
