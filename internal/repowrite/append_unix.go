//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package repowrite

import (
	"fmt"
	"os"
	"syscall"
)

// appendFlags open a file for appending and refuse a symlink standing where it
// goes. Directory traversal is confined by os.Root; O_NOFOLLOW additionally
// refuses a link planted at the resolved final component before the open.
const appendFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND | syscall.O_NOFOLLOW

// truncateFlags open an existing file to cut it, refusing a link at the final
// component for the same reason.
const truncateFlags = os.O_WRONLY | syscall.O_NOFOLLOW

const inPlaceWritesSupported = true

// singleLinkFile inspects the opened inode, never its replaceable pathname.
// Any additional hard link might be outside the declared root, so even links
// that happen to be inside it are refused before changing the shared bytes.
func singleLinkFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing in-place write to %s: it is not a regular file", file.Name())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect the hard link count of %s before writing", file.Name())
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("refusing in-place write to %s: it has %d hard links, want exactly one", file.Name(), stat.Nlink)
	}
	return nil
}
