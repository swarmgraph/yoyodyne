//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervise

import (
	"errors"
	"syscall"
)

// reap collects pid if it is an exited child of this process, and reports
// whether it did. A child still running is left running, and a process that is
// not this one's child is nothing this process can collect — its own parent
// does — which is no failure.
func reap(pid int) (bool, error) {
	var status syscall.WaitStatus
	got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	switch {
	case errors.Is(err, syscall.ECHILD):
		return false, nil
	case err != nil:
		return false, err
	}
	return got == pid, nil
}

// collectExited collects pid, which has been seen to have exited, and reports
// whether nothing of it is left for this process to collect: it was collected
// now, or it is not this process's child and its own parent collects it. It
// reports false for a child the kernel will not hand over yet, which on Linux
// is a multi-threaded program whose main thread has exited while its other
// threads are still ending; asked again a moment later, it is collected. Only
// an interrupted wait is asked again too; any other failure leaves nothing this
// process could do, so it reports true.
func collectExited(pid int) bool {
	var status syscall.WaitStatus
	got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	switch {
	case errors.Is(err, syscall.EINTR):
		return false
	case err != nil:
		return true
	}
	return got == pid
}
