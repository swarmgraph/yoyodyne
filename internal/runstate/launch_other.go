//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package runstate

// observeExecution establishes nothing on a platform with no file locks to
// hold a tree by and no process groups to look for it in: every execution is
// one recovery waits on, with the reason recorded, rather than one it guesses
// has stopped.
func (s *Store) observeExecution(string, ExecutionIdentity) (executionState, string, func(bool)) {
	return executionUnknown, "this platform cannot establish whether a provider process has stopped", func(bool) {}
}
