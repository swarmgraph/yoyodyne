//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByThisUser reports whether a file belongs to the user this process runs
// as. A file whose owner cannot be read is not counted as this user's.
func ownedByThisUser(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
