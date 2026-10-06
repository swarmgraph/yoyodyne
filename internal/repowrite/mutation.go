package repowrite

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var errInPlaceWritesUnsupported = errors.New("this platform cannot inspect file hard links before in-place writing")

// resolve follows contained links using the held root. Absolute links into the
// root are translated to relative names for os.Root, which rejects absolute
// links itself. The returned name is used only with directory handles: topology
// changes after this walk cannot turn a later mutation into a pathname write.
func (r *PinnedRoot) resolve(relative string) (string, error) {
	clean, err := Relative(relative)
	if err != nil {
		return "", err
	}
	return r.resolveLinks(clean, clean, 0, false)
}

func (r *PinnedRoot) resolveLinks(relative, requested string, depth int, mustExist bool) (string, error) {
	if depth > 40 {
		return "", fmt.Errorf("too many symlinks resolving %s", requested)
	}
	elements := strings.Split(filepath.ToSlash(relative), "/")
	current := "."
	for index, element := range elements {
		candidate := filepath.Join(current, element)
		if candidate == ".." || strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
			return "", &EscapeError{Path: requested, Component: candidate, Resolved: filepath.Join(r.path, candidate), Root: r.path}
		}
		info, err := r.root.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) && !mustExist {
			return filepath.Join(append([]string{candidate}, elements[index+1:]...)...), nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect %s inside the repository: %w", candidate, err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			current = candidate
			continue
		}
		linked, err := r.root.Readlink(candidate)
		if err != nil {
			return "", fmt.Errorf("read symlink %s: %w", candidate, err)
		}
		if filepath.IsAbs(linked) {
			if linked == r.path {
				linked = "."
			} else if strings.HasPrefix(linked, r.path+string(filepath.Separator)) {
				linked = strings.TrimPrefix(linked, r.path+string(filepath.Separator))
			} else {
				// The link may use an alias of the declared root (for example
				// /var on macOS). This read-only resolution preserves those
				// layouts; mutation still uses the held root, never this path.
				resolved, err := filepath.EvalSymlinks(linked)
				if err != nil {
					return "", err
				}
				linked, err = filepath.Rel(r.path, resolved)
				if err != nil {
					return "", err
				}
			}
		} else {
			// Preserve .. until the components before it have been followed.
			// Cleaning a link such as a/link/../file first changes its meaning.
			linked = current + string(filepath.Separator) + linked
		}
		if linked == ".." || strings.HasPrefix(linked, ".."+string(filepath.Separator)) {
			return "", &EscapeError{Path: requested, Component: candidate, Resolved: filepath.Join(r.path, linked), Root: r.path}
		}
		// A link's target must exist even when the suffix being created does not.
		// Otherwise a dangling link would be mistaken for a missing descendant.
		current, err = r.resolveLinks(linked, requested, depth+1, true)
		if err != nil {
			return "", err
		}
	}
	return current, nil
}

// ReplaceFile publishes a complete, synced file through a held parent directory.
func (r *PinnedRoot) ReplaceFile(relative string, content []byte, mode, directory fs.FileMode) error {
	target, err := r.resolve(relative)
	if err != nil {
		return err
	}
	if err := r.root.MkdirAll(filepath.Dir(target), directory); err != nil {
		return err
	}
	return r.WriteFile(target, content, mode, false)
}

// OpenAppend hands back a descriptor confined at open time; replacing any path
// component afterwards cannot redirect the descriptor's subsequent writes.
// Shared inodes are refused because another hard link may name an external file.
func (r *PinnedRoot) OpenAppend(relative string, file, directory fs.FileMode) (*os.File, error) {
	return r.openFile(relative, appendFlags, file, directory)
}

// OpenReadWrite opens the stable inode shared by advisory lock holders. It never
// truncates or replaces that inode, so queued claimants lock the same file.
// The inode must have only one hard link, just as for an append descriptor.
func (r *PinnedRoot) OpenReadWrite(relative string) (*os.File, error) {
	return r.openFile(relative, os.O_RDWR|os.O_CREATE, 0o600, 0o700)
}

func (r *PinnedRoot) openFile(relative string, flags int, file, directory fs.FileMode) (*os.File, error) {
	if !inPlaceWritesSupported {
		return nil, errInPlaceWritesUnsupported
	}
	target, err := r.resolve(relative)
	if err != nil {
		return nil, err
	}
	if err := r.root.MkdirAll(filepath.Dir(target), directory); err != nil {
		return nil, err
	}
	parent, err := r.OpenDirectory(filepath.Dir(target))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	// Concurrent creators can receive ENOENT on macOS even with O_CREATE.
	// Retry only that creation race, through the same held parent; never
	// replace the file whose inode advisory lock holders must keep sharing.
	for attempt := 0; ; attempt++ {
		opened, err := parent.root.OpenFile(filepath.Base(target), flags, file)
		if err == nil {
			if err := singleLinkFile(opened); err != nil {
				return nil, errors.Join(err, opened.Close())
			}
			return opened, nil
		}
		if flags&os.O_CREATE == 0 || !errors.Is(err, fs.ErrNotExist) || attempt == 9 {
			return nil, err
		}
	}
}

func (r *PinnedRoot) Truncate(relative string, size int64) error {
	if !inPlaceWritesSupported {
		return errInPlaceWritesUnsupported
	}
	target, err := r.resolve(relative)
	if err != nil {
		return err
	}
	file, err := r.root.OpenFile(target, truncateFlags, 0)
	if err != nil {
		return err
	}
	err = singleLinkFile(file)
	if err == nil {
		err = file.Truncate(size)
	}
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

func (r *PinnedRoot) remove(relative string, directory bool) error {
	target, err := r.resolve(relative)
	if err != nil {
		return err
	}
	info, err := r.root.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || info.IsDir() != directory {
		return fmt.Errorf("refusing to remove %s: it is not the requested file or directory", relative)
	}
	if directory {
		err = r.root.RemoveAll(target)
	} else {
		err = r.root.Remove(target)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
