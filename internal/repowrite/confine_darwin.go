package repowrite

import (
	"golang.org/x/sys/unix"
)

// On macOS a lookup is the walk itself: each part opened with O_NOFOLLOW
// relative to the directory before it, and every link resolved by the walk
// rather than by the kernel.
func lookupDirectory(a *anchor, rel string) (int, error) {
	return walkDirectory(a, rel)
}

func renameNoReplace(fromFD int, from string, toFD int, to string) error {
	return unix.RenameatxNp(fromFD, from, toFD, to, unix.RENAME_EXCL)
}

func exchange(fromFD int, from string, toFD int, to string) error {
	return unix.RenameatxNp(fromFD, from, toFD, to, unix.RENAME_SWAP)
}

// platformSupported is always true here: Go itself needs a macOS newer than
// the 11 the design names.
func platformSupported() (Reason, string) { return "", "" }

// supportedFilesystem accepts APFS, the filesystem with both the exchange and
// the rename that refuses to replace.
func supportedFilesystem(fd int) (Reason, string) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return ReasonFilesystem, "its filesystem could not be identified: " + err.Error()
	}
	name := unix.ByteSliceToString(st.Fstypename[:])
	if name != "apfs" {
		return ReasonFilesystem, name
	}
	return "", ""
}
