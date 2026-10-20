//go:build darwin || linux

package repowrite

// This file is the descriptor-level half of the package: what the
// repository-confined-writes design calls the primitive. Everything that
// changes a root goes through it, and it holds containment while the change is
// made rather than only before:
//
//   - every lookup starts from the root's open descriptor (lookupDirectory, one
//     per platform), so a path string never decides where a write lands;
//   - a held directory is looked up again from the root, and compared by device
//     and file number, immediately before and immediately after each change, and
//     a change the second check rejects is undone;
//   - no existing file is written in place except a lock, so a link somebody
//     adds from outside the root never carries a write out with it: new content
//     is a new file, published by a rename that refuses to replace, or swapped
//     with the old one by an exchange that can be swapped back;
//   - appends go in batches, each checked on both sides for the directory and
//     for a second link;
//   - a removal moves the name into a holding directory at the root first, and
//     removes it only if what arrived is what was checked.
//
// Where the design leaves something open (one append batch visible through a
// link that appeared while it was written) the comments say so at the place.

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	// maxLinksFollowed bounds the symbolic links one lookup follows.
	maxLinksFollowed = 40
	// appendBatchBytes is the most one append writes between its checks.
	appendBatchBytes = 64 << 10
	// removingDirectory is the holding directory at the root a removal moves a
	// name into before it removes it.
	removingDirectory = ".yoyodyne-removing"
	// removalAttempts bounds the retries of a removal whose holding directory
	// another removal took away between its creation and its use.
	removalAttempts = 64
)

func statID(st *unix.Stat_t) fileID { return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)} }

// sameID reports whether a FileInfo from the os package describes the file id.
func sameID(info fs.FileInfo, id fileID) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(st.Dev) == id.dev && uint64(st.Ino) == id.ino
}

// entryExists reports whether anything, a link included, stands at name in h.
func (h *heldDir) entryExists(name string) (bool, error) {
	_, err := lstatAt(h.fd, name)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, pathError("inspect", joinRel(h.rel, name), err)
	}
	return true, nil
}

func fstatFD(fd int) (unix.Stat_t, error) {
	var st unix.Stat_t
	for {
		err := unix.Fstat(fd, &st)
		if err != unix.EINTR {
			return st, err
		}
	}
}

func lstatAt(dirfd int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	for {
		err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if err != unix.EINTR {
			return st, err
		}
	}
}

func openat(dirfd int, name string, flags int, mode uint32) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, flags|unix.O_CLOEXEC, mode)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func dupFD(fd int) (int, error) {
	return unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
}

func readlinkat(dirfd int, name string) (string, error) {
	for size := 256; ; size *= 2 {
		buffer := make([]byte, size)
		n, err := unix.Readlinkat(dirfd, name, buffer)
		if err != nil {
			return "", err
		}
		if n < size {
			return string(buffer[:n]), nil
		}
	}
}

func isType(st *unix.Stat_t, kind uint32) bool { return uint32(st.Mode)&unix.S_IFMT == kind }

// anchor is a declared root, opened once and held for the whole of one
// operation. Every lookup starts from its descriptor.
type anchor struct {
	fd   int
	path string
	id   fileID
}

// openAnchor opens the directory a root was declared as. The path is the
// declaration: what it opens to now is the root this operation is held to,
// and a final component that is a symbolic link is refused rather than taken
// for one.
func openAnchor(path string) (*anchor, error) {
	if reason, detail := platformSupported(); reason != "" {
		return nil, &RefusalError{Root: path, Path: ".", Reason: reason, Detail: detail}
	}
	fd, err := openat(unix.AT_FDCWD, path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, pathError("open the root", path, err)
	}
	return anchorFromFD(fd, path)
}

func anchorFromFD(fd int, path string) (*anchor, error) {
	st, err := fstatFD(fd)
	if err != nil {
		unix.Close(fd)
		return nil, pathError("inspect the root", path, err)
	}
	if reason, detail := supportedFilesystem(fd); reason != "" {
		unix.Close(fd)
		return nil, &RefusalError{Root: path, Path: ".", Reason: reason, Detail: detail}
	}
	return &anchor{fd: fd, path: path, id: statID(&st)}, nil
}

func (a *anchor) duplicate() (*anchor, error) {
	fd, err := dupFD(a.fd)
	if err != nil {
		return nil, err
	}
	return &anchor{fd: fd, path: a.path, id: a.id}, nil
}

func (a *anchor) close() error { return unix.Close(a.fd) }

// walkDirectory looks a directory up beneath the anchor one part at a time,
// each part opened relative to the directory before it and never followed as a
// link by the kernel. A link is read and its target walked the same way from
// the directories already held: a relative target that stays beneath the root
// is followed, an absolute one is refused, a `..` that would climb above the
// root is refused, and so is a part on another device than the root.
//
// It is the lookup on macOS, and on Linux it names which way a lookup the
// kernel refused left the root.
func walkDirectory(a *anchor, rel string) (int, error) {
	first, err := dupFD(a.fd)
	if err != nil {
		return -1, err
	}
	stack := []int{first}
	defer func() {
		for _, fd := range stack {
			unix.Close(fd)
		}
	}()
	pending := strings.Split(rel, "/")
	followed := 0
	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if len(stack) == 1 {
				return -1, refusal(ReasonLinkLeavesRoot, "a .. in "+rel+" climbs above the root")
			}
			unix.Close(stack[len(stack)-1])
			stack = stack[:len(stack)-1]
			continue
		}
		top := stack[len(stack)-1]
		fd, err := openat(top, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err == nil {
			st, err := fstatFD(fd)
			if err != nil {
				unix.Close(fd)
				return -1, pathError("inspect", rel, err)
			}
			if uint64(st.Dev) != a.id.dev {
				unix.Close(fd)
				return -1, refusal(ReasonOtherFilesystem, component+" in "+rel)
			}
			stack = append(stack, fd)
			continue
		}
		if errors.Is(err, unix.ENOENT) {
			return -1, pathError("open", rel, unix.ENOENT)
		}
		st, statErr := lstatAt(top, component)
		if statErr != nil {
			return -1, pathError("open", rel, statErr)
		}
		if !isType(&st, unix.S_IFLNK) {
			return -1, pathError("open", rel, unix.ENOTDIR)
		}
		followed++
		if followed > maxLinksFollowed {
			return -1, pathError("open", rel, unix.ELOOP)
		}
		target, err := readlinkat(top, component)
		if err != nil {
			return -1, pathError("read the link", rel, err)
		}
		if strings.HasPrefix(target, "/") {
			return -1, refusal(ReasonAbsoluteLink, component+" in "+rel+" points at "+target)
		}
		pending = append(strings.Split(target, "/"), pending...)
	}
	result := stack[len(stack)-1]
	stack = stack[:len(stack)-1]
	return result, nil
}

// heldDir is a directory held open beneath an anchor, with the path relative
// to the root it was looked up by and its identity when it was.
type heldDir struct {
	anchor *anchor
	fd     int
	rel    string
	id     fileID
}

func (h *heldDir) close() error { return unix.Close(h.fd) }

// absolute is where the held directory was when it was looked up, for
// messages and for a test hook to find it by.
func (h *heldDir) absolute() string {
	return filepath.Join(h.anchor.path, filepath.FromSlash(h.rel))
}

// hold looks a directory up beneath the anchor and holds it.
func (a *anchor) hold(rel string) (*heldDir, error) {
	if rel == "" {
		rel = "."
	}
	fd, err := lookupDirectory(a, rel)
	if err != nil {
		return nil, err
	}
	st, err := fstatFD(fd)
	if err != nil {
		unix.Close(fd)
		return nil, pathError("inspect", rel, err)
	}
	return &heldDir{anchor: a, fd: fd, rel: rel, id: statID(&st)}, nil
}

// beneath is the check the design makes of a held directory on both sides of
// every change: looked up again from the root by the path it was held by, it
// is still the same directory.
func (h *heldDir) beneath() error {
	fd, err := lookupDirectory(h.anchor, h.rel)
	if err != nil {
		return movedFrom(h.rel, err)
	}
	st, err := fstatFD(fd)
	unix.Close(fd)
	if err != nil {
		return pathError("inspect", h.rel, err)
	}
	if statID(&st) != h.id {
		return refusal(ReasonMoved, h.rel+" is now a different directory")
	}
	return nil
}

// openChild holds an existing directory entry of h, refusing a link there.
func (h *heldDir) openChild(name string) (*heldDir, error) {
	fd, err := openat(h.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		if st, statErr := lstatAt(h.fd, name); statErr == nil && isType(&st, unix.S_IFLNK) {
			return nil, refusal(ReasonFinalLink, joinRel(h.rel, name))
		}
		return nil, pathError("open", joinRel(h.rel, name), err)
	}
	st, err := fstatFD(fd)
	if err != nil {
		unix.Close(fd)
		return nil, pathError("inspect", joinRel(h.rel, name), err)
	}
	if uint64(st.Dev) != h.anchor.id.dev {
		unix.Close(fd)
		return nil, refusal(ReasonOtherFilesystem, joinRel(h.rel, name))
	}
	return &heldDir{anchor: h.anchor, fd: fd, rel: joinRel(h.rel, name), id: statID(&st)}, nil
}

// makeDirectories holds the directory rel names beneath the anchor, creating
// whatever of it is missing. Creation proceeds from the nearest existing
// ancestor, one directory at a time, each made relative to the descriptor of
// the one above it.
func (a *anchor) makeDirectories(rel string, mode fs.FileMode) (*heldDir, error) {
	var components []string
	if rel != "." && rel != "" {
		components = strings.Split(rel, "/")
	}
	existing := len(components)
	var held *heldDir
	for ; existing >= 0; existing-- {
		prefix := "."
		if existing > 0 {
			prefix = strings.Join(components[:existing], "/")
		}
		h, err := a.hold(prefix)
		if err == nil {
			held = h
			break
		}
		if !errors.Is(err, fs.ErrNotExist) || existing == 0 {
			return nil, err
		}
	}
	for _, component := range components[existing:] {
		child, err := held.makeDirectory(component, mode, false)
		held.close()
		if err != nil {
			return nil, err
		}
		held = child
	}
	return held, nil
}

// makeDirectory creates one directory in h and holds it. An existing one is
// taken as it is unless exclusive asks for a new one.
func (h *heldDir) makeDirectory(name string, mode fs.FileMode, exclusive bool) (*heldDir, error) {
	if err := h.beneath(); err != nil {
		return nil, err
	}
	fire(PointBeforeChange, h.absolute(), name)
	created := true
	if err := unix.Mkdirat(h.fd, name, uint32(mode.Perm())); err != nil {
		if !errors.Is(err, unix.EEXIST) || exclusive {
			return nil, pathError("mkdir", joinRel(h.rel, name), err)
		}
		created = false
	}
	child, err := h.openChild(name)
	if err != nil {
		return nil, err
	}
	fire(PointAfterChange, h.absolute(), name)
	if err := h.beneath(); err != nil {
		child.close()
		if created {
			err = errors.Join(err, h.removeOwn(name, child.id))
		}
		return nil, err
	}
	return child, nil
}

// singleLink confirms an open file is a regular file with exactly one link, on
// the root's device.
func (h *heldDir) singleLink(file *os.File, rel string) (fileID, error) {
	st, err := fstatFD(int(file.Fd()))
	if err != nil {
		return fileID{}, pathError("inspect", rel, err)
	}
	if !isType(&st, unix.S_IFREG) {
		return fileID{}, pathError("open", rel, errors.New("it is not a regular file"))
	}
	if uint64(st.Dev) != h.anchor.id.dev {
		return fileID{}, refusal(ReasonOtherFilesystem, rel)
	}
	if st.Nlink != 1 {
		return fileID{}, refusal(ReasonLinked, fmt.Sprintf("%s has %d links", rel, st.Nlink))
	}
	return statID(&st), nil
}

// temporary is a new file in a held directory, written under a reserved name
// and not yet published under the one it is for.
type temporary struct {
	dir  *heldDir
	file *os.File
	name string
	id   fileID
}

func (h *heldDir) createTemporary(mode fs.FileMode) (*temporary, error) {
	if err := h.beneath(); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 10; attempt++ {
		name := temporaryPrefix + rand.Text() + temporarySuffix
		fd, err := openat(h.fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW, uint32(mode.Perm()))
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, pathError("create a temporary file in", h.rel, err)
		}
		file := os.NewFile(uintptr(fd), filepath.Join(h.absolute(), name))
		if err := file.Chmod(mode.Perm()); err != nil {
			file.Close()
			unix.Unlinkat(h.fd, name, 0)
			return nil, pathError("set the permissions of a temporary file in", h.rel, err)
		}
		st, err := fstatFD(fd)
		if err != nil {
			file.Close()
			unix.Unlinkat(h.fd, name, 0)
			return nil, pathError("inspect a temporary file in", h.rel, err)
		}
		return &temporary{dir: h, file: file, name: name, id: statID(&st)}, nil
	}
	return nil, errors.New("could not reserve a temporary name in " + h.rel)
}

// discard takes an unpublished temporary file back out.
func (t *temporary) discard() error {
	closeErr := t.file.Close()
	if errors.Is(closeErr, os.ErrClosed) {
		closeErr = nil
	}
	return errors.Join(closeErr, t.dir.removeOwn(t.name, t.id))
}

// publish puts a written temporary file under name in its directory.
//
// Without replace it is the design's creation: a rename that refuses to
// replace, so an existing name is left alone and reported as fs.ErrExist. With
// replace, an existing regular file at name is swapped out by an exchange and
// then removed, so its content is never changed and a link to it from outside
// keeps exactly what it had. expected, when given, is the file the caller
// read and means to replace; anything else at name refuses.
//
// Either way, the new file is synced and confirmed to have one link before it
// is published, the directory is checked on both sides of the rename, and a
// failed second check undoes the rename: a created name is removed, a swap is
// swapped back.
func (t *temporary) publish(name string, replace bool, expected *fileID) error {
	h := t.dir
	rel := joinRel(h.rel, name)
	if err := t.file.Sync(); err != nil {
		return errors.Join(pathError("sync", rel, err), t.discard())
	}
	fire(PointBeforePublish, h.absolute(), name)
	if _, err := h.singleLink(t.file, rel); err != nil {
		return errors.Join(err, t.discard())
	}
	if err := h.beneath(); err != nil {
		return errors.Join(err, t.discard())
	}
	fire(PointBeforeChange, h.absolute(), name)
	var previous *fileID
	var err error
	if replace {
		previous, err = h.swapIn(t.name, name, expected)
	} else {
		err = renameNoReplace(h.fd, t.name, h.fd, name)
		if errors.Is(err, unix.EEXIST) {
			// A link standing at the name is refused rather than reported as a
			// record already there, which a caller would then read through it.
			if st, statErr := lstatAt(h.fd, name); statErr == nil && isType(&st, unix.S_IFLNK) {
				err = refusal(ReasonFinalLink, rel)
			}
		}
		if err != nil && !isRefusal(err) {
			err = pathError("create", rel, err)
		}
	}
	if err != nil {
		return errors.Join(err, t.discard())
	}
	fire(PointAfterChange, h.absolute(), name)
	if err := h.beneath(); err != nil {
		if previous != nil {
			// Swapped back, the old file is under its name again and the new
			// one is under the temporary name, to be discarded with it.
			if undo := exchange(h.fd, t.name, h.fd, name); undo != nil {
				return errors.Join(err, pathError("swap back", rel, undo))
			}
			return errors.Join(err, t.discard())
		}
		closeErr := t.file.Close()
		return errors.Join(err, closeErr, h.removeOwn(name, t.id))
	}
	closeErr := t.file.Close()
	var removeErr error
	if previous != nil {
		removeErr = h.removeOwn(t.name, *previous)
	}
	return errors.Join(closeErr, removeErr, syncDirectory(h))
}

// swapIn exchanges the temporary file with the regular file at name, or
// renames it there when nothing is. It returns the identity of the file it
// swapped out, now under the temporary name, or nil when there was none.
func (h *heldDir) swapIn(temporaryName, name string, expected *fileID) (*fileID, error) {
	rel := joinRel(h.rel, name)
	for attempt := 0; attempt < 8; attempt++ {
		st, err := lstatAt(h.fd, name)
		if errors.Is(err, unix.ENOENT) {
			if expected != nil {
				return nil, refusal(ReasonChanged, rel+" is no longer there")
			}
			err := renameNoReplace(h.fd, temporaryName, h.fd, name)
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			if err != nil {
				return nil, pathError("create", rel, err)
			}
			return nil, nil
		}
		if err != nil {
			return nil, pathError("inspect", rel, err)
		}
		if isType(&st, unix.S_IFLNK) {
			return nil, refusal(ReasonFinalLink, rel)
		}
		if !isType(&st, unix.S_IFREG) {
			return nil, pathError("replace", rel, errors.New("it is not a regular file"))
		}
		old := statID(&st)
		if expected != nil && old != *expected {
			return nil, refusal(ReasonChanged, rel+" is a different file from the one read")
		}
		if err := exchange(h.fd, temporaryName, h.fd, name); err != nil {
			return nil, pathError("replace", rel, err)
		}
		after, err := lstatAt(h.fd, temporaryName)
		if err == nil && statID(&after) == old {
			return &old, nil
		}
		// Something else stood at the name by the moment of the exchange. It is
		// put back where it was rather than removed, and the exchange is tried
		// again against whatever is there now.
		if undo := exchange(h.fd, temporaryName, h.fd, name); undo != nil {
			return nil, pathError("swap back", rel, undo)
		}
		if expected != nil {
			return nil, refusal(ReasonChanged, rel+" changed while it was replaced")
		}
	}
	return nil, refusal(ReasonChanged, rel+" kept changing while it was replaced")
}

// holding is the root's holding directory for removals, held. Every removal
// in the root shares it: each creates it when it needs it and takes it away
// again when it is empty, so one removal can find it gone between creating it
// and opening it, and makes it again.
func (a *anchor) holding() (*heldDir, error) {
	root, err := a.hold(".")
	if err != nil {
		return nil, err
	}
	defer root.close()
	for attempt := 1; ; attempt++ {
		if err := unix.Mkdirat(a.fd, removingDirectory, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, pathError("create the holding directory", removingDirectory, err)
		}
		holding, err := root.openChild(removingDirectory)
		if err == nil || !errors.Is(err, fs.ErrNotExist) || attempt == removalAttempts {
			return holding, err
		}
	}
}

// gone reports whether the root's holding directory is no longer the one held,
// which it is not once another removal has taken it away.
func (a *anchor) gone(holding *heldDir) bool {
	st, err := lstatAt(a.fd, removingDirectory)
	return err != nil || statID(&st) != holding.id
}

func (a *anchor) releaseHolding(holding *heldDir) {
	holding.close()
	// Another removal may be using it, in which case it is not empty and stays.
	unix.Unlinkat(a.fd, removingDirectory, unix.AT_REMOVEDIR)
}

// removeOwn removes name from h only while it is still the file id. The name
// is moved into the root's holding directory first, and what arrived there is
// removed only if it is the file that was checked; anything else is moved
// back and refused. It makes no check of h itself, because it is also how a
// change the second check rejected is undone in a directory that has moved.
func (h *heldDir) removeOwn(name string, id fileID) error {
	return h.removeChecked(name, id, nil)
}

// removeChecked is removeOwn with confirm run once the name has been moved
// aside and before anything is deleted: a removal whose confirm fails is
// moved back, so it is undone as completely as any other change.
func (h *heldDir) removeChecked(name string, id fileID, confirm func() error) error {
	rel := joinRel(h.rel, name)
	for attempt := 0; ; attempt++ {
		holding, err := h.anchor.holding()
		if err != nil {
			return err
		}
		aside := "removing-" + rand.Text()
		err = renameNoReplace(h.fd, name, holding.fd, aside)
		if err != nil && attempt < removalAttempts && h.anchor.gone(holding) {
			// Another removal took the holding directory away between this one
			// opening it and moving the name into it. Nothing moved, so the
			// removal starts again with the holding directory made afresh.
			h.anchor.releaseHolding(holding)
			continue
		}
		if err != nil {
			h.anchor.releaseHolding(holding)
			return pathError("remove", rel, err)
		}
		st, err := lstatAt(holding.fd, aside)
		if err != nil || statID(&st) != id {
			back := renameNoReplace(holding.fd, aside, h.fd, name)
			h.anchor.releaseHolding(holding)
			if back != nil {
				back = pathError("put back", rel, back)
			}
			return errors.Join(refusal(ReasonChanged, rel+" is not the file that was checked"), back)
		}
		if confirm != nil {
			if err := confirm(); err != nil {
				back := renameNoReplace(holding.fd, aside, h.fd, name)
				h.anchor.releaseHolding(holding)
				if back != nil {
					back = pathError("put back", rel, back)
				}
				return errors.Join(err, back)
			}
		}
		if isType(&st, unix.S_IFDIR) {
			err = holding.removeTree(aside)
		} else {
			err = unix.Unlinkat(holding.fd, aside, 0)
		}
		if err != nil {
			// What could not be removed goes back where it was rather than
			// staying in the holding directory, where nobody looks for it.
			back := renameNoReplace(holding.fd, aside, h.fd, name)
			h.anchor.releaseHolding(holding)
			if back != nil {
				back = pathError("put back", rel, back)
			}
			return errors.Join(pathError("remove", rel, err), back)
		}
		h.anchor.releaseHolding(holding)
		return nil
	}
}

// removeTree removes a directory entry of h and everything in it, depth first,
// through open descriptors, checking each directory before and after its
// contents are removed.
func (h *heldDir) removeTree(name string) error {
	child, err := h.openChild(name)
	if err != nil {
		return err
	}
	defer child.close()
	if err := child.beneath(); err != nil {
		return err
	}
	names, err := child.names()
	if err != nil {
		return err
	}
	for _, entry := range names {
		st, err := lstatAt(child.fd, entry)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return pathError("inspect", joinRel(child.rel, entry), err)
		}
		if isType(&st, unix.S_IFDIR) {
			err = child.removeTree(entry)
		} else {
			err = unix.Unlinkat(child.fd, entry, 0)
		}
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return pathError("remove", joinRel(child.rel, entry), err)
		}
	}
	if err := child.beneath(); err != nil {
		return err
	}
	return unix.Unlinkat(h.fd, name, unix.AT_REMOVEDIR)
}

func (h *heldDir) names() ([]string, error) {
	fd, err := dupFD(h.fd)
	if err != nil {
		return nil, err
	}
	directory := os.NewFile(uintptr(fd), h.absolute())
	defer directory.Close()
	if _, err := directory.Seek(0, io.SeekStart); err != nil {
		return nil, pathError("read", h.rel, err)
	}
	names, err := directory.Readdirnames(-1)
	if err != nil {
		return nil, pathError("read", h.rel, err)
	}
	return names, nil
}

// removeEntry is a removal a caller asked for: the directory is checked on
// both sides, and the entry removed is the one inspected at the start. kind
// is what the caller named: a file, a directory and everything in it, any
// entry with an empty directory as os.Remove means it, or any entry with a
// directory's contents as os.RemoveAll means it.
func (h *heldDir) removeEntry(name string, kind entryKind) error {
	rel := joinRel(h.rel, name)
	if err := h.beneath(); err != nil {
		return err
	}
	st, err := lstatAt(h.fd, name)
	if err != nil {
		return pathError("remove", rel, err)
	}
	switch {
	case isType(&st, unix.S_IFLNK) && (kind == fileEntry || kind == directoryEntry):
		return refusal(ReasonFinalLink, rel)
	case isType(&st, unix.S_IFDIR) && kind == fileEntry:
		return pathError("remove", rel, errors.New("it is a directory"))
	case !isType(&st, unix.S_IFDIR) && kind == directoryEntry:
		return pathError("remove", rel, errors.New("it is not a directory"))
	case isType(&st, unix.S_IFDIR) && kind == anyEntry:
		// os.Remove's meaning: a directory goes only when it is empty.
		child, err := h.openChild(name)
		if err != nil {
			return err
		}
		names, err := child.names()
		child.close()
		if err != nil {
			return err
		}
		if len(names) != 0 {
			return pathError("remove", rel, unix.ENOTEMPTY)
		}
	}
	fire(PointBeforeChange, h.absolute(), name)
	fire(PointBeforeRemove, h.absolute(), name)
	// The change is the move aside; the directory is checked after it, and a
	// failed check moves the name back before anything is deleted.
	if err := h.removeChecked(name, statID(&st), func() error {
		fire(PointAfterChange, h.absolute(), name)
		return h.beneath()
	}); err != nil {
		return err
	}
	// The name is gone from this directory once it was moved aside, and that
	// is made durable here as any other change to the directory is.
	return syncDirectory(h)
}

// openExisting opens an existing file in h for appending or locking, or
// creates it when create is set, and confirms it is a regular file with one
// link on the root's device. The directory is checked before the open and
// after it, and a file this created is removed again when the second check
// fails.
func (h *heldDir) openExisting(name string, flags int, mode fs.FileMode, create bool) (*os.File, error) {
	rel := joinRel(h.rel, name)
	if err := h.beneath(); err != nil {
		return nil, err
	}
	fire(PointBeforeChange, h.absolute(), name)
	for attempt := 1; ; attempt++ {
		created := false
		fd := -1
		var err error
		if create {
			fd, err = openat(h.fd, name, flags|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW, uint32(mode.Perm()))
			created = err == nil
		}
		if !create || errors.Is(err, unix.EEXIST) {
			fd, err = openat(h.fd, name, flags|unix.O_NOFOLLOW, 0)
			// Removed between the exclusive create finding it and this open: a
			// lock is only ever opened to be shared, so the create is tried again.
			if create && errors.Is(err, unix.ENOENT) && attempt < openLockAttempts {
				continue
			}
		}
		if err != nil {
			if st, statErr := lstatAt(h.fd, name); statErr == nil && isType(&st, unix.S_IFLNK) {
				return nil, refusal(ReasonFinalLink, rel)
			}
			return nil, pathError("open", rel, err)
		}
		file := os.NewFile(uintptr(fd), filepath.Join(h.absolute(), name))
		if created {
			if err := file.Chmod(mode.Perm()); err != nil {
				file.Close()
				return nil, errors.Join(pathError("set the permissions of", rel, err), h.removeOwnByName(name))
			}
		}
		id, err := h.singleLink(file, rel)
		if err != nil {
			file.Close()
			if created {
				err = errors.Join(err, h.removeOwnByName(name))
			}
			return nil, err
		}
		fire(PointAfterChange, h.absolute(), name)
		if err := h.beneath(); err != nil {
			file.Close()
			if created {
				err = errors.Join(err, h.removeOwn(name, id))
			}
			return nil, err
		}
		// A file this created is durable in its directory before anything is
		// written to it, so a caller that later removes the only other copy of
		// what it appends here does not depend on luck.
		if created {
			if err := syncDirectory(h); err != nil {
				file.Close()
				return nil, err
			}
		}
		return file, nil
	}
}

func (h *heldDir) removeOwnByName(name string) error {
	st, err := lstatAt(h.fd, name)
	if err != nil {
		return nil
	}
	return h.removeOwn(name, statID(&st))
}

// appendWriter appends to a file in batches, each checked on both sides: the
// directory is still beneath the root, and the file still has one link.
//
// A link added outside the root while a batch is being written sees that one
// batch; the check after it stops the writer and says so, and every later
// write is refused. The batch is not undone, because undoing it would be
// another write through the same link.
type appendWriter struct {
	dir    *heldDir
	file   *os.File
	name   string
	failed error
	// anchor is the writer's own hold on the root, released with it.
	anchor *anchor
}

func (w *appendWriter) check() error {
	if err := w.dir.beneath(); err != nil {
		return err
	}
	_, err := w.dir.singleLink(w.file, joinRel(w.dir.rel, w.name))
	return err
}

func (w *appendWriter) Write(data []byte) (int, error) {
	if w.failed != nil {
		return 0, w.failed
	}
	written := 0
	for written < len(data) {
		end := min(written+appendBatchBytes, len(data))
		if err := w.check(); err != nil {
			w.failed = err
			return written, err
		}
		n, err := w.file.Write(data[written:end])
		written += n
		if err != nil {
			w.failed = err
			return written, err
		}
		if err := w.check(); err != nil {
			var refused *RefusalError
			if errors.As(err, &refused) {
				refused.Detail = strings.TrimSpace(refused.Detail + "; the batch just written may be visible outside the root")
			}
			w.failed = err
			return written, err
		}
		fire(PointBetweenBatches, w.dir.absolute(), w.name)
	}
	return written, nil
}

func (w *appendWriter) Sync() error { return w.file.Sync() }

func (w *appendWriter) Close() error {
	err := errors.Join(w.file.Close(), w.dir.close())
	if w.anchor != nil {
		err = errors.Join(err, w.anchor.close())
	}
	return err
}

// shorten replaces the regular file at name with one holding its first size
// bytes, with the same permissions. It is a replacement like any other: the
// old file is swapped out whole and removed, never cut in place.
func (h *heldDir) shorten(name string, size int64) error {
	rel := joinRel(h.rel, name)
	fd, err := openat(h.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		if st, statErr := lstatAt(h.fd, name); statErr == nil && isType(&st, unix.S_IFLNK) {
			return refusal(ReasonFinalLink, rel)
		}
		return pathError("open", rel, err)
	}
	current := os.NewFile(uintptr(fd), filepath.Join(h.absolute(), name))
	defer current.Close()
	st, err := fstatFD(fd)
	if err != nil {
		return pathError("inspect", rel, err)
	}
	if !isType(&st, unix.S_IFREG) {
		return pathError("cut", rel, errors.New("it is not a regular file"))
	}
	id := statID(&st)
	temporary, err := h.createTemporary(fs.FileMode(st.Mode) & fs.ModePerm)
	if err != nil {
		return err
	}
	if err := copyPrefix(temporary.file, current, size); err != nil {
		return errors.Join(pathError("copy what is kept of", rel, err), temporary.discard())
	}
	return temporary.publish(name, true, &id)
}

// checkMovable refuses to move a link, which would move and leave what it
// names behind, and, when a directory was named, anything that is not one.
func (h *heldDir) checkMovable(name string, directoryOnly bool) error {
	rel := joinRel(h.rel, name)
	st, err := lstatAt(h.fd, name)
	if err != nil {
		return pathError("inspect", rel, err)
	}
	if isType(&st, unix.S_IFLNK) {
		return refusal(ReasonFinalLink, rel)
	}
	if directoryOnly && !isType(&st, unix.S_IFDIR) {
		return pathError("move", rel, errors.New("it is not a directory"))
	}
	return nil
}

// symlink creates a link in h, checked on both sides, and removed again when
// the second check fails. Its target is never traversed by anything here.
func (h *heldDir) symlink(target, name string) error {
	rel := joinRel(h.rel, name)
	if err := h.beneath(); err != nil {
		return err
	}
	fire(PointBeforeChange, h.absolute(), name)
	if err := unix.Symlinkat(target, h.fd, name); err != nil {
		return pathError("symlink", rel, err)
	}
	fire(PointAfterChange, h.absolute(), name)
	if err := h.beneath(); err != nil {
		return errors.Join(err, h.removeOwnByName(name))
	}
	return nil
}

// moveEntry renames an entry of one held directory into another without
// replacing anything, both checked on both sides, and moved back when the
// second check fails.
func moveEntry(from *heldDir, fromName string, to *heldDir, toName string) error {
	fromRel, toRel := joinRel(from.rel, fromName), joinRel(to.rel, toName)
	if err := from.beneath(); err != nil {
		return err
	}
	if err := to.beneath(); err != nil {
		return err
	}
	fire(PointBeforeChange, from.absolute(), fromName)
	if err := renameNoReplace(from.fd, fromName, to.fd, toName); err != nil {
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			return pathError("move", fromRel, fmt.Errorf("%s already exists", toRel))
		}
		return pathError("move "+fromRel+" to", toRel, err)
	}
	fire(PointAfterChange, to.absolute(), toName)
	err := errors.Join(from.beneath(), to.beneath())
	if err != nil {
		if undo := renameNoReplace(to.fd, toName, from.fd, fromName); undo != nil {
			return errors.Join(err, pathError("move back", fromRel, undo))
		}
		return err
	}
	return errors.Join(syncDirectory(from), syncDirectory(to))
}

func syncDirectory(h *heldDir) error {
	if err := unix.Fsync(h.fd); err != nil {
		return pathError("sync", h.rel, err)
	}
	return nil
}
