//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package repowrite

import "os"

// os.Root confines traversal on supported platforms. In-place writes also need
// a descriptor-based hard link count; without it these flags are never used.
const appendFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND

const truncateFlags = os.O_WRONLY

// Platforms without descriptor-based link counts refuse in-place writes before
// opening or creating their target. Replacement writers still use new inodes.
const inPlaceWritesSupported = false

func singleLinkFile(*os.File) error { return errInPlaceWritesUnsupported }
