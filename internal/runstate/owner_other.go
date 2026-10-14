//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package runstate

import "io/fs"

// ownedByThisUser has no owner to compare on a platform without Unix file
// ownership, where the mode checks beside it are the whole of the answer.
func ownedByThisUser(fs.FileInfo) bool { return true }
