package cli

// The read model as a program manager reads it: what each pass opens with, and
// the one named query a reply may ask for in full.
//
// Both are read over the same stores `yoyo status` and the dashboard read, so
// what an instance is told and what the operator's surfaces show are one
// derivation rather than two. See readmodel/passopening.go.

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// passReadModel satisfies orchestrator.PassReadModel and chat.ReadModelQueries.
// The sources are opened afresh on each reading, as `yoyo status` opens them,
// so a pass is told the product as it stands when the pass is taken.
type passReadModel struct {
	configPath string
	stateRoot  string
	productID  domain.ProductID
}

func passReadModelFrom(parts components) passReadModel {
	return passReadModel{configPath: parts.configPath, stateRoot: parts.stateRoot, productID: parts.config.Product.ID}
}

// Opening is what a pass opens with.
func (r passReadModel) Opening(ctx context.Context) readmodel.PassOpening {
	return readmodel.ReadPassOpening(ctx, standingSources(r.configPath), throughputSources(r.stateRoot, r.productID))
}

// Query answers one named query in full.
func (r passReadModel) Query(ctx context.Context, name, agent string) (readmodel.QueryResult, error) {
	return readmodel.Query(ctx, standingSources(r.configPath), throughputSources(r.stateRoot, r.productID), name, agent)
}
