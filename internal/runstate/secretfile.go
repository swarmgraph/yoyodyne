package runstate

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// CreateProductSecret creates a file named name in the product's own directory
// under the state root, holding content, readable and writable by the user the
// harness runs as alone. A file already there under that name is left exactly
// as it is, whatever it holds, and reported as not created: this makes a secret
// that is missing and never replaces or rotates one somebody is using. It
// returns where the file is either way.
func CreateProductSecret(stateRoot string, productID domain.ProductID, name string, content []byte) (string, bool, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return "", false, err
	}
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return "", false, errors.New("a product secret is named by one file name, with no directory in it")
	}
	// The path said is the one under the state root as the caller named it,
	// which is the one every other message about the file names.
	path := filepath.Join(home.ProductDirectory(filepath.Clean(stateRoot), string(productID)), name)
	root, anchor, err := confinedStateRoot(stateRoot)
	if err != nil {
		return path, false, err
	}
	pinned, err := pinStateRoot(home.ProductDirectory(root, string(productID)), anchor)
	if err != nil {
		return path, false, err
	}
	defer pinned.Close()
	// CreateFile publishes by a hard link, which refuses a name already taken,
	// so a file that appeared after any check made here is still never replaced.
	if err := pinned.CreateFile(name, content, 0o600); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return path, false, nil
		}
		return path, false, err
	}
	return path, true, nil
}
