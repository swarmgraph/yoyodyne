package invariant

import "github.com/mason-bryant/yoyodyne/internal/config"

// StoreFor is the invariants store a product configuration describes: its
// invariants directory, in the repository the product's intent is kept in — the
// companion intent repository where the configuration names one, and
// repositoryRoot otherwise (config.Product.IntentRoot). Every reader and writer
// of the invariants assembles its store here, so none of them can read the
// project's repository when the constraints are kept somewhere else.
func StoreFor(repositoryRoot string, product config.Product) Store {
	return Store{RepositoryRoot: product.IntentRoot(repositoryRoot), Directory: product.Invariants}
}
