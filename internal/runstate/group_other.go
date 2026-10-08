//go:build !linux

package runstate

// groupHasOnlyExited answers false where this build cannot list a group's
// members, so a group that answers a signal is never read as stopped; see
// group_linux.go for the gap it closes there.
func groupHasOnlyExited(int) (bool, error) { return false, nil }
