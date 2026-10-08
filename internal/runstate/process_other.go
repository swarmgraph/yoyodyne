//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package runstate

import "errors"

// processIsRunning has nothing to check on a platform that takes no lease in the
// first place: nothing here can hold a conversation, so a stamp saying somebody
// does is a question this build cannot answer rather than one it should guess
// at.
func processIsRunning(int) (bool, error) {
	return false, errors.New("observing a lease holder is unsupported on this platform")
}

// LookProcess is the same question, unanswerable here for the same reason.
func LookProcess(int) (ProcessLook, error) {
	return ProcessLook{}, errors.New("observing a process is unsupported on this platform")
}
