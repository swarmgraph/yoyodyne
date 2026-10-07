package orchestrator

import (
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

// stateRootOf is the state root a run store for the product yoyodyne was
// opened on. The store is <product directory>/runs, and the product directory
// is wherever the home's layout puts it, so the root is found by asking.
func stateRootOf(runsRoot string) string {
	product := filepath.Dir(runsRoot)
	root := product
	for root != filepath.Dir(root) && home.ProductDirectory(root, "yoyodyne") != product {
		root = filepath.Dir(root)
	}
	return root
}
