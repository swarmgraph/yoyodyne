//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package runstate

import "github.com/mason-bryant/yoyodyne/internal/repowrite"

// observe establishes nothing on a platform with no file locks or process
// groups to read a check's processes by, so no started check's place is ever
// released on a guess. Asking for a place fails on such a platform anyway,
// at the record's lock.
func (s *HeavyCheckStore) observe(*repowrite.PinnedRoot, HeavyCheckPermit) (executionState, string) {
	return executionUnknown, "this platform cannot establish whether a check's processes have stopped"
}
