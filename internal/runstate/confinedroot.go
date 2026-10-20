package runstate

import (
	"errors"
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
