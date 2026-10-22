package runstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// confinedStateRoot resolves the existing prefix once. Missing directories are
// later created below that anchor through the shared, pinned writer.
func confinedStateRoot(root string) (stateRoot, anchor string, err error) {
	if !filepath.IsAbs(root) {
		return "", "", errors.New("state root must be an absolute path")
	}
	root = filepath.Clean(root)
	ancestor := root
	for {
		_, err := os.Lstat(ancestor)
		if !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return "", "", err
			}
			break
		}
		ancestor = filepath.Dir(ancestor)
	}
	anchor, err = filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(ancestor, root)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(anchor, relative), anchor, nil
}

func pinStateRoot(stateRoot, anchor string) (*repowrite.PinnedRoot, error) {
	root, err := repowrite.OpenPinnedRoot(anchor)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(anchor, stateRoot)
	if err != nil {
		root.Close()
		return nil, err
	}
	if relative != "." {
		for _, component := range strings.Split(relative, string(filepath.Separator)) {
			if err := root.MakeDirectory(component, 0o700); err != nil {
				root.Close()
				return nil, err
			}
			child, err := root.OpenDirectory(component)
			root.Close()
			if err != nil {
				return nil, err
			}
			root = child
		}
	}
	return root, nil
}

// stateWriter is the state root declared as the root a store's writes are
// confined to. A state root that is not there yet is created first, beneath
// the existing ancestor it was resolved from, through the same primitive.
func stateWriter(stateRoot, anchor string) (repowrite.Root, error) {
	if _, err := os.Lstat(stateRoot); errors.Is(err, os.ErrNotExist) {
		base, err := repowrite.NewRoot(anchor)
		if err != nil {
			return repowrite.Root{}, err
		}
		relative, err := filepath.Rel(anchor, stateRoot)
		if err != nil {
			return repowrite.Root{}, err
		}
		if _, err := base.MakeDirectory(filepath.ToSlash(relative), 0o700); err != nil {
			return repowrite.Root{}, err
		}
	}
	return repowrite.NewRoot(stateRoot)
}

// stateRoot is a state root as a store was given it, and what every write a
// store makes beneath it is confined to: the root resolved, and the nearest
// ancestor of it that existed, which a root not there yet is created beneath.
type stateRoot struct {
	given, confined, anchor string
}

func newStateRoot(root string) (stateRoot, error) {
	confined, anchor, err := confinedStateRoot(root)
	if err != nil {
		return stateRoot{}, fmt.Errorf("resolve the state root: %w", err)
	}
	return stateRoot{given: filepath.Clean(root), confined: confined, anchor: anchor}, nil
}

// writer is the state root as the root a write is confined to, and path, which
// a store built from the state root it was given, as a path relative to it.
func (r stateRoot) writer(path string) (repowrite.Root, string, error) {
	relative, err := filepath.Rel(r.given, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return repowrite.Root{}, "", fmt.Errorf("%s is not inside the state root %s", path, r.given)
	}
	root, err := stateWriter(r.confined, r.anchor)
	if err != nil {
		return repowrite.Root{}, "", fmt.Errorf("open the state root: %w", err)
	}
	return root, filepath.ToSlash(relative), nil
}

// openLock opens the lock file at path, creating it and the directories above
// it: created once and never replaced, and refused as a link or with a second
// link.
func (r stateRoot) openLock(path string) (*os.File, error) {
	writer, relative, err := r.writer(path)
	if err != nil {
		return nil, err
	}
	return writer.OpenLock(relative, 0o600, 0o700)
}

// appendTo appends content to the log at path in checked batches.
func (r stateRoot) appendTo(path string, content []byte) error {
	writer, relative, err := r.writer(path)
	if err != nil {
		return err
	}
	return writer.Append(relative, content, 0o600, 0o700)
}

// replace publishes content at path in place of whatever was there.
func (r stateRoot) replace(path string, content []byte) error {
	writer, relative, err := r.writer(path)
	if err != nil {
		return err
	}
	return writer.ReplaceFile(relative, content, 0o600, 0o700)
}

// create publishes content at path only if nothing is there; an existing name
// is an error that is fs.ErrExist.
func (r stateRoot) create(path string, content []byte) error {
	writer, relative, err := r.writer(path)
	if err != nil {
		return err
	}
	return writer.CreateFileMode(relative, content, 0o600, 0o700)
}

// remove takes the file at path away; one that is not there is already gone.
func (r stateRoot) remove(path string) error {
	writer, relative, err := r.writer(path)
	if err != nil {
		return err
	}
	_, err = writer.RemoveFile(relative)
	return err
}
