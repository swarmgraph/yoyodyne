package repowrite

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PinnedRoot holds the directory itself, rather than a pathname that can be
// replaced between validation and a write. Close releases its directory handle.
// Unlike Resolve, none of its writes hands an absolute path to another writer.
type PinnedRoot struct {
	root *os.Root
	path string
}

func OpenPinnedRoot(path string) (*PinnedRoot, error) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" || runtime.GOOS == "wasip1" {
		return nil, errors.New("this platform cannot pin a repository directory across replacement")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	base := filepath.VolumeName(absolute) + string(filepath.Separator)
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	pinned := &PinnedRoot{root: root, path: base}
	for _, component := range strings.Split(strings.TrimPrefix(absolute, base), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		child, err := pinned.OpenDirectory(component)
		pinned.Close()
		if err != nil {
			return nil, err
		}
		pinned = child
	}
	return pinned, nil
}

func (r *PinnedRoot) Close() error { return r.root.Close() }

// Path names the pinned directory for link text and diagnostics, never as a
// substitute for the handle when writing.
func (r *PinnedRoot) Path() string { return r.path }

// Unchanged is a refusal after a replacement, not the confinement mechanism:
// the handle keeps writes confined even while this answer becomes false.
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

func (r *PinnedRoot) OpenDirectory(relative string) (*PinnedRoot, error) {
	info, err := r.root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repository directory must not be a symlink: %s", relative)
	}
	root, err := r.root.OpenRoot(relative)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fmt.Errorf("repository directory changed while being opened: %s", relative)
	}
	return &PinnedRoot{root: root, path: filepath.Join(r.path, relative)}, nil
}

func (r *PinnedRoot) MakeDirectory(relative string, mode fs.FileMode) error {
	clean, err := Relative(relative)
	if err != nil {
		return err
	}
	for prefix := ""; ; {
		component, remainder, more := strings.Cut(clean, "/")
		prefix = filepath.Join(prefix, component)
		if err := r.root.Mkdir(prefix, mode); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := r.root.Stat(prefix)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("repository path is not a directory: %s", prefix)
		}
		if !more {
			return nil
		}
		clean = remainder
	}
}

// CreateDirectory reserves a new name; an existing directory is a conflict.
func (r *PinnedRoot) CreateDirectory(relative string) (*PinnedRoot, error) {
	if _, err := Relative(relative); err != nil {
		return nil, err
	}
	if err := r.root.Mkdir(relative, 0o700); err != nil {
		return nil, err
	}
	return r.OpenDirectory(relative)
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
// one. The temporary file and the final link stay in the held directory.
func (r *PinnedRoot) CreateFile(relative string, content []byte, mode fs.FileMode) error {
	clean, err := Relative(relative)
	if err != nil {
		return err
	}
	parent, err := r.OpenDirectory(filepath.Dir(clean))
	if err != nil {
		return err
	}
	defer parent.Close()
	temporary := strings.Replace(temporaryPattern, "*", rand.Text(), 1)
	file, err := parent.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer parent.root.Remove(temporary)
	err = file.Chmod(mode)
	if err == nil {
		_, err = file.Write(content)
	}
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := parent.root.Link(temporary, filepath.Base(clean)); err != nil {
		return err
	}
	directory, err := parent.root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// FileWriter always streams into a newly created inode. Exclusive writes
// reserve the requested name; replacements reserve a temporary name in a
// pinned parent directory and publish it only on a successful Close. Opening
// an existing inode for truncation would also change any hard links outside
// the root, even though the open itself was confined.
func (r *PinnedRoot) FileWriter(relative string, mode fs.FileMode, exclusive bool) (io.WriteCloser, error) {
	clean, err := Relative(relative)
	if err != nil {
		return nil, err
	}
	if parent := filepath.Dir(clean); parent != "." {
		if err := r.MakeDirectory(parent, 0o755); err != nil {
			return nil, err
		}
	}
	if exclusive {
		file, err := r.root.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return nil, err
		}
		return &pinnedFileWriter{file}, nil
	}
	parent, err := r.OpenDirectory(filepath.Dir(clean))
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 10; attempt++ {
		temporary := strings.Replace(temporaryPattern, "*", rand.Text(), 1)
		file, err := parent.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			parent.Close()
			return nil, err
		}
		if err := file.Chmod(mode); err != nil {
			return nil, errors.Join(err, file.Close(), parent.root.Remove(temporary), parent.Close())
		}
		return &pinnedReplacementWriter{file: file, parent: parent, temporary: temporary, target: filepath.Base(clean)}, nil
	}
	parent.Close()
	return nil, errors.New("could not reserve a new file for confined replacement")
}

type pinnedFileWriter struct{ file *os.File }

func (w *pinnedFileWriter) Write(data []byte) (int, error) { return w.file.Write(data) }
func (w *pinnedFileWriter) Close() error                   { return w.file.Close() }

type pinnedReplacementWriter struct {
	file               *os.File
	parent             *PinnedRoot
	temporary, target  string
	writeErr, closeErr error
	closed             bool
}

func (w *pinnedReplacementWriter) Write(data []byte) (int, error) {
	if w.closed {
		return 0, os.ErrClosed
	}
	n, err := w.file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.writeErr = errors.Join(w.writeErr, err)
	}
	return n, err
}

func (w *pinnedReplacementWriter) Close() error {
	if w.closed {
		return w.closeErr
	}
	w.closed = true
	w.closeErr = w.writeErr
	if w.closeErr == nil {
		w.closeErr = w.file.Sync()
	}
	w.closeErr = errors.Join(w.closeErr, w.file.Close())
	if w.closeErr == nil {
		// Rename changes the directory entry, never the previous inode's bytes.
		// Both names are relative to the same held parent directory throughout.
		w.closeErr = w.parent.root.Rename(w.temporary, w.target)
		if w.closeErr == nil {
			w.closeErr = w.parent.Sync()
		}
	}
	if w.closeErr != nil {
		if err := w.parent.root.Remove(w.temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
			w.closeErr = errors.Join(w.closeErr, err)
		}
	}
	w.closeErr = errors.Join(w.closeErr, w.parent.Close())
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

// OpenLock creates or opens an advisory lock in the held directory, refusing a
// final symlink. The descriptor is read-only: flock needs no content write, and
// an existing inode's bytes must stay untouched even if it has other hard links.
func (r *PinnedRoot) OpenLock(relative string, mode fs.FileMode) (*os.File, error) {
	if _, err := Relative(relative); err != nil {
		return nil, err
	}
	return r.root.OpenFile(relative, appendFlags & ^(os.O_WRONLY|os.O_APPEND), mode)
}

func (r *PinnedRoot) Remove(relative string) error { return r.root.Remove(relative) }

// Sync makes changes to the held directory durable without reopening its path,
// which may have been replaced since the directory was pinned.
func (r *PinnedRoot) Sync() error {
	directory, err := r.root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func (r *PinnedRoot) Exists(relative string) (bool, error) {
	_, err := r.root.Lstat(relative)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
