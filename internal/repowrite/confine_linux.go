package repowrite

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// On Linux a lookup is one openat2 from the root's descriptor. The kernel
// refuses a lookup that would leave the root, by a link or a `..`, or cross
// onto another mounted filesystem, and refuses a magic link outright; a
// relative link that stays beneath the root is followed.
func lookupDirectory(a *anchor, rel string) (int, error) {
	how := unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	}
	for attempt := 0; ; attempt++ {
		fd, err := unix.Openat2(a.fd, rel, &how)
		switch {
		case err == nil:
			return fd, nil
		case (errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR)) && attempt < 16:
			// The kernel asks for a retry when a rename raced the lookup.
			continue
		case errors.Is(err, unix.ENOSYS):
			return -1, refusal(ReasonPlatform, "the kernel has no openat2, which needs Linux 5.6 or later")
		case errors.Is(err, unix.EXDEV):
			// The kernel says only that the lookup left; the walk names how.
			fd, walkErr := walkDirectory(a, rel)
			if walkErr != nil {
				return -1, walkErr
			}
			unix.Close(fd)
			return -1, refusal(ReasonLinkLeavesRoot, rel)
		default:
			return -1, pathError("open", rel, err)
		}
	}
}

func renameNoReplace(fromFD int, from string, toFD int, to string) error {
	return unix.Renameat2(fromFD, from, toFD, to, unix.RENAME_NOREPLACE)
}

func exchange(fromFD int, from string, toFD int, to string) error {
	return unix.Renameat2(fromFD, from, toFD, to, unix.RENAME_EXCHANGE)
}

// platformSupported refuses a kernel older than 5.6, which has no openat2.
func platformSupported() (Reason, string) {
	var name unix.Utsname
	if err := unix.Uname(&name); err != nil {
		return ReasonPlatform, "the kernel version could not be read: " + err.Error()
	}
	var major, minor int
	if _, err := fmt.Sscanf(unix.ByteSliceToString(name.Release[:]), "%d.%d", &major, &minor); err != nil {
		return ReasonPlatform, "the kernel version " + unix.ByteSliceToString(name.Release[:]) + " could not be read"
	}
	if major < 5 || major == 5 && minor < 6 {
		return ReasonPlatform, fmt.Sprintf("Linux %d.%d is older than 5.6", major, minor)
	}
	return "", ""
}

// supportedFilesystem accepts the filesystems the design names as having both
// the exchange and the rename that refuses to replace.
func supportedFilesystem(fd int) (Reason, string) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return ReasonFilesystem, "its filesystem could not be identified: " + err.Error()
	}
	switch uint64(st.Type) {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.TMPFS_MAGIC:
		return "", ""
	}
	return ReasonFilesystem, fmt.Sprintf("filesystem type %#x", uint64(st.Type))
}
