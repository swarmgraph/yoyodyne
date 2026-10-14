//go:build !(darwin || linux)

package gitworktree

// freeMemory is unread on this platform.
func freeMemory() (uint64, bool) {
	return 0, false
}
