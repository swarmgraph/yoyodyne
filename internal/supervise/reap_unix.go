//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervise

import (
	"errors"
	"syscall"
)

// reap collects pid if it is an exited child of this process, and reports
// whether it did. A process that is not this one's child is nothing this
// process can collect — its own parent does — which is no failure. A child the
// system does not yet offer to be collected, as one still finishing its exit,
// is reported with errNotCollectable, so the caller looks again rather than
// leaving it in the process table.
func reap(pid int) (bool, error) {
	var status syscall.WaitStatus
	got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	switch {
	case errors.Is(err, syscall.ECHILD):
		return false, nil
	case err != nil:
		return false, err
	case got == 0:
		return false, errNotCollectable
	}
	return got == pid, nil
}
