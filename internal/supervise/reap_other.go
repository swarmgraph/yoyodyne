//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package supervise

// reap has nothing to collect where the supervisor signals nothing.
func reap(int) (bool, error) { return false, nil }
