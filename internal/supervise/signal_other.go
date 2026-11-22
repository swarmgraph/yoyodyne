//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package supervise

import "errors"

// Stopping a process by signal is implemented for Yoyodyne's supported Unix
// hosts, as detaching one is.
func terminate(int) error {
	return errors.New("stopping a process by signal is not supported on this platform")
}

// killTree is not supported here either, for the same reason.
func killTree(int) error {
	return errors.New("forcing a process to stop is not supported on this platform")
}
