//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"errors"
	"fmt"
	"syscall"
)

// processIsRunning reports whether the process a durable stamp names is still
// there. It is how a holder somebody wrote down is checked without taking
// anything from it: a lease that is observed rather than acquired needs the
// stamp and the operating system to agree, and the operating system's answer is
// which processes exist.
//
// The null signal is delivered to nothing; the kernel does the existence check
// and reports it. A process this one may not signal exists all the same, which
// is the one case where a permission failure is an answer rather than a
// problem. A process that has exited and not yet been collected by its parent
// still answers the null signal, and is not running for any purpose a stamp is
// read for, so where the platform can say so it is reported gone.
func processIsRunning(pid int) (bool, error) {
	look, err := LookProcess(pid)
	if err != nil {
		return false, err
	}
	return look.Running(), nil
}

// LookProcess asks the operating system about one process id: whether a process
// has it, whether that process has exited without being collected, and when it
// started, where the platform says.
func LookProcess(pid int) (ProcessLook, error) {
	if pid <= 0 {
		return ProcessLook{}, fmt.Errorf("%d is not a process identifier", pid)
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
	case errors.Is(err, syscall.ESRCH):
		return ProcessLook{}, nil
	default:
		return ProcessLook{}, err
	}
	look := ProcessLook{Exists: true}
	// What else the platform says is a refinement of an answer already made: a
	// reading that fails leaves the process existing with nothing more known,
	// which is what every reading said before the refinement existed.
	if exited, started, known := describeProcess(pid); known {
		look.Exited, look.StartedAt = exited, started
	}
	return look, nil
}
