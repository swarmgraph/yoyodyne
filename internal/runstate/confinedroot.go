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
	return openStateRoot(stateRoot, anchor, true)
}

func openStateRoot(stateRoot, anchor string, create bool) (*repowrite.PinnedRoot, error) {
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
			if create {
				if err := root.MakeDirectory(component, 0o700); err != nil {
					root.Close()
					return nil, err
				}
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
