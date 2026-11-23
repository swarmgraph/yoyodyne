//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervise

import (
	"errors"
	"syscall"
	"time"
)

// reapWait bounds how long reap goes on asking for a child that has not yet
// become collectable. On Linux a process with several threads shows as exited
// once its first thread has, but its parent can collect it only after the
// rest have finished, a moment later; asking once in that moment leaves it in
// the process table with nothing left to collect it.
const reapWait = 2 * time.Second

// reap collects pid if it is an exited child of this process, and reports
// whether it did. It is asked only of a process seen to have exited, so a
// child not yet collectable is asked again until reapWait runs out. A child
// still running past that is left running, and a process that is not this
// one's child is nothing this process can collect — its own parent does —
// which is no failure.
func reap(pid int) (bool, error) {
	end := time.Now().Add(reapWait)
	for {
		var status syscall.WaitStatus
		got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		switch {
		case errors.Is(err, syscall.ECHILD):
			return false, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case err != nil:
			return false, err
		case got == pid:
			return true, nil
		}
		if time.Now().After(end) {
			return false, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
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
