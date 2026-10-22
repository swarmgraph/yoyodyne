package repowrite

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// PinnedRoot is a directory held open beneath the root it was opened under,
// rather than a pathname that can be replaced between a check and a write.
// Close releases it.
//
// Reads go through the held directory itself. Every change goes through the
// descriptor-level primitive, against the root the first PinnedRoot was opened
// at: a PinnedRoot from OpenDirectory is checked beneath that same root, not
// beneath itself, so a directory moved out of the root is refused rather than
// written into wherever it went. A directory that is no longer at the path it
// was opened by is refused the same way.
type PinnedRoot struct {
	root *os.Root
	// anchor is this PinnedRoot's own hold on the root it was opened under.
	anchor *anchor
	// rel is this directory's path relative to that root, and id is its
	// identity when it was opened.
	rel  string
	id   fileID
	path string
}

// OpenPinnedRoot opens a directory as the root its writes are confined to. No
// component of the path may be a symlink.
func OpenPinnedRoot(path string) (*PinnedRoot, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	base := filepath.VolumeName(absolute) + string(filepath.Separator)
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	walked := base
	for _, component := range strings.Split(strings.TrimPrefix(absolute, base), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		child, err := openReadDirectory(root, component)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = child
		walked = filepath.Join(walked, component)
	}
	a, err := openAnchor(walked)
	if err != nil {
		root.Close()
		return nil, describe(err, "open", walked, ".")
	}
	opened, err := root.Stat(".")
	if err != nil || !sameID(opened, a.id) {
		root.Close()
		a.close()
		return nil, fmt.Errorf("repository directory changed while being opened: %s", walked)
	}
	return &PinnedRoot{root: root, anchor: a, rel: ".", id: a.id, path: walked}, nil
}

// openReadDirectory opens a directory under an os.Root for reading, refusing a
// symlink at its final component.
func openReadDirectory(root *os.Root, relative string) (*os.Root, error) {
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repository directory must not be a symlink: %s", relative)
	}
	child, err := root.OpenRoot(relative)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		child.Close()
		return nil, fmt.Errorf("repository directory changed while being opened: %s", relative)
	}
	return child, nil
}

func (r *PinnedRoot) Close() error { return errors.Join(r.root.Close(), r.anchor.close()) }

// Path names the pinned directory for link text and diagnostics, never as a
// substitute for the handle when writing.
func (r *PinnedRoot) Path() string { return r.path }

// Unchanged reports whether the pinned directory is still the one its path
// names. A change refuses on its own when it is not, so this is for a caller
// that wants to say so before trying.
func (r *PinnedRoot) Unchanged() error {
	info, err := os.Lstat(r.path)
	if err != nil {
		return err
	}
	opened, err := r.root.Stat(".")
	if err != nil || !info.IsDir() || !os.SameFile(info, opened) {
		return fmt.Errorf("repository write root was replaced: %s", r.path)
	}
	return nil
}

// within is a path below this directory as a path below the root, refusing
// one that climbs out of this directory.
func (r *PinnedRoot) within(relative string) (string, error) {
	clean := path.Clean(filepath.ToSlash(relative))
	if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q is not inside %s", relative, r.path)
	}
	return joinRel(r.rel, clean), nil
}

// self holds this directory and confirms it is still the one opened, still
// at the path beneath the root it was opened by.
func (r *PinnedRoot) self() (*heldDir, error) {
	held, err := r.anchor.hold(r.rel)
	if err != nil {
		return nil, describe(movedFrom(r.rel, err), "write in", r.anchor.path, r.rel)
	}
	if held.id != r.id {
		held.close()
		return nil, describe(refusal(ReasonMoved, r.rel+" is now a different directory"), "write in", r.anchor.path, r.rel)
	}
	return held, nil
}

// hold holds a directory below this one, after confirming this one is still
// where it was opened.
func (r *PinnedRoot) hold(relative string) (*heldDir, error) {
	held, err := r.self()
	if err != nil {
		return nil, err
	}
	rel, err := r.within(relative)
	if err != nil {
		held.close()
		return nil, err
	}
	if rel == r.rel {
		return held, nil
	}
	held.close()
	return r.anchor.hold(rel)
}

func (r *PinnedRoot) makeDirectories(relative string, mode fs.FileMode) (*heldDir, error) {
	held, err := r.self()
	if err != nil {
		return nil, err
	}
	held.close()
	rel, err := r.within(relative)
	if err != nil {
		return nil, err
	}
	return r.anchor.makeDirectories(rel, mode)
}

func (r *PinnedRoot) OpenDirectory(relative string) (*PinnedRoot, error) {
	rel, err := r.within(relative)
	if err != nil {
		return nil, err
	}
	child, err := openReadDirectory(r.root, relative)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil {
		child.Close()
		return nil, err
	}
	a, err := r.anchor.duplicate()
	if err != nil {
		child.Close()
		return nil, err
	}
	held, err := a.hold(rel)
	if err != nil {
		child.Close()
		a.close()
		return nil, describe(err, "open", a.path, rel)
	}
	id := held.id
	held.close()
	if !sameID(opened, id) {
		child.Close()
		a.close()
		return nil, fmt.Errorf("repository directory changed while being opened: %s", relative)
	}
	return &PinnedRoot{root: child, anchor: a, rel: rel, id: id, path: filepath.Join(r.path, relative)}, nil
}

func (r *PinnedRoot) MakeDirectory(relative string, mode fs.FileMode) error {
	clean, err := Relative(relative)
	if err != nil {
		return err
	}
	held, err := r.makeDirectories(clean, mode)
	if err != nil {
		return describe(err, "create", r.anchor.path, joinRel(r.rel, clean))
	}
	return held.close()
}

// CreateDirectory reserves a new name; an existing directory is a conflict.
func (r *PinnedRoot) CreateDirectory(relative string) (*PinnedRoot, error) {
	clean, err := Relative(relative)
	if err != nil {
		return nil, err
	}
	dir, name := splitTarget(clean)
	parent, err := r.hold(dir)
	if err != nil {
		return nil, describe(err, "create", r.anchor.path, joinRel(r.rel, clean))
	}
	defer parent.close()
	created, err := parent.makeDirectory(name, 0o700, true)
	if err != nil {
		return nil, describe(err, "create", r.anchor.path, joinRel(r.rel, clean))
	}
	created.close()
	return r.OpenDirectory(clean)
}

func (r *PinnedRoot) WriteFile(relative string, content []byte, mode fs.FileMode, exclusive bool) error {
	file, err := r.FileWriter(relative, mode, exclusive)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

// CreateFile publishes a complete, synced record only if its name is unused.
// A reader never sees a partial record, and a competing writer cannot replace
// one: an existing name is reported as fs.ErrExist.
func (r *PinnedRoot) CreateFile(relative string, content []byte, mode fs.FileMode) error {
	clean, err := Relative(relative)
	if err != nil {
		return err
	}
	dir, name := splitTarget(clean)
	parent, err := r.hold(dir)
	if err != nil {
		return describe(err, "create", r.anchor.path, joinRel(r.rel, clean))
	}
	defer parent.close()
	temporary, err := parent.createTemporary(mode)
	if err != nil {
		return describe(err, "create", r.anchor.path, joinRel(r.rel, clean))
	}
	if _, err := temporary.file.Write(content); err != nil {
		return errors.Join(err, temporary.discard())
	}
	return describe(temporary.publish(name, false, nil), "create", r.anchor.path, joinRel(r.rel, clean))
}

// FileWriter always streams into a newly created file, published only on a
// successful Close. An exclusive write reserves the requested name: one that
// exists is refused when the writer is opened, and one that appears before
// Close is not replaced. A replacement is swapped with the existing file, so
// any link to that file outside the root keeps what it had.
func (r *PinnedRoot) FileWriter(relative string, mode fs.FileMode, exclusive bool) (io.WriteCloser, error) {
	clean, err := Relative(relative)
	if err != nil {
		return nil, err
	}
	rel := joinRel(r.rel, clean)
	dir, name := splitTarget(clean)
	parent, err := r.makeDirectories(dir, 0o755)
	if err != nil {
		return nil, describe(err, "write", r.anchor.path, rel)
	}
	if exclusive {
		if exists, err := parent.entryExists(name); err != nil || exists {
			parent.close()
			if err == nil {
				err = pathError("create", rel, fs.ErrExist)
			}
			return nil, err
		}
	}
	temporary, err := parent.createTemporary(mode)
	if err != nil {
		parent.close()
		return nil, describe(err, "write", r.anchor.path, rel)
	}
	return &publishingWriter{temporary: temporary, dir: parent, name: name, replace: !exclusive, root: r.anchor.path, rel: rel}, nil
}

type publishingWriter struct {
	temporary          *temporary
	dir                *heldDir
	name               string
	replace            bool
	root, rel          string
	writeErr, closeErr error
	closed             bool
}

func (w *publishingWriter) Write(data []byte) (int, error) {
	if w.closed {
		return 0, os.ErrClosed
	}
	n, err := w.temporary.file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.writeErr = errors.Join(w.writeErr, err)
	}
	return n, err
}

func (w *publishingWriter) Close() error {
	if w.closed {
		return w.closeErr
	}
	w.closed = true
	if w.writeErr != nil {
		w.closeErr = errors.Join(w.writeErr, w.temporary.discard())
	} else {
		w.closeErr = describe(w.temporary.publish(w.name, w.replace, nil), "write", w.root, w.rel)
	}
	w.closeErr = errors.Join(w.closeErr, w.dir.close())
	return w.closeErr
}

func (r *PinnedRoot) ReadFile(relative string) ([]byte, error) {
	return fs.ReadFile(r.root.FS(), relative)
}

func (r *PinnedRoot) ReadDirectory(relative string) ([]fs.DirEntry, error) {
	return fs.ReadDir(r.root.FS(), relative)
}

func (r *PinnedRoot) Lstat(relative string) (fs.FileInfo, error) {
	return r.root.Lstat(relative)
}

// OpenLock creates or opens an advisory lock in the held directory. A lock is
// identified by its file, so it is created once and never replaced, and one
// found as a link or with a second link is refused rather than opened. The
// descriptor is read-only: flock needs no content write.
//
// Two openers creating the same lock at once both get it: the one that loses
// the exclusive create opens the file the other made, and one that finds it
// removed in between tries the create again, a bounded number of times.
func (r *PinnedRoot) OpenLock(relative string, mode fs.FileMode) (*os.File, error) {
	clean, err := Relative(relative)
	if err != nil {
		return nil, err
	}
	dir, name := splitTarget(clean)
	parent, err := r.hold(dir)
	if err != nil {
		return nil, describe(err, "lock", r.anchor.path, joinRel(r.rel, clean))
	}
	defer parent.close()
	file, err := parent.openExisting(name, os.O_RDONLY, mode, true)
	if err != nil {
		return nil, describe(err, "lock", r.anchor.path, joinRel(r.rel, clean))
	}
	return file, nil
}

// openLockAttempts bounds OpenLock's retries of a create that lost a race.
const openLockAttempts = 50

// Remove removes one file, link, or empty directory, as os.Remove does. What
// is removed is the entry inspected, moved aside into the root's holding
// directory first; a link is removed and what it names is not touched.
func (r *PinnedRoot) Remove(relative string) error {
	return r.remove(relative, anyEntry)
}

// RemoveAll removes a directory and everything in it without leaving the root;
// a symlink inside is removed rather than followed. Removing what is not there
// is a removal already made.
func (r *PinnedRoot) RemoveAll(relative string) error {
	err := r.remove(relative, treeEntry)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (r *PinnedRoot) remove(relative string, kind entryKind) error {
	clean, err := Relative(relative)
	if err != nil {
		return err
	}
	dir, name := splitTarget(clean)
	parent, err := r.hold(dir)
	if err != nil {
		return describe(err, "remove", r.anchor.path, joinRel(r.rel, clean))
	}
	defer parent.close()
	return describe(parent.removeEntry(name, kind), "remove", r.anchor.path, joinRel(r.rel, clean))
}

// Sync makes changes to the held directory durable without reopening its path.
func (r *PinnedRoot) Sync() error {
	held, err := r.self()
	if err != nil {
		return err
	}
	defer held.close()
	return syncDirectory(held)
}

func (r *PinnedRoot) Exists(relative string) (bool, error) {
	_, err := r.root.Lstat(relative)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
