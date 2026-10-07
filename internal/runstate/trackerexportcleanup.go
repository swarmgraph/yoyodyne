package runstate

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// TrackerExportCleanupStore keeps each maintenance removal in its own durable
// record, so concurrent passes never overwrite one another's account.
type TrackerExportCleanupStore struct {
	root, anchor string
	productID    domain.ProductID
}

func NewTrackerExportCleanupStore(root string, productID domain.ProductID) (*TrackerExportCleanupStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &TrackerExportCleanupStore{
		root:   filepath.Join(home.ProductDirectory(root, string(productID)), "tracker-export-cleanups"),
		anchor: anchor, productID: productID,
	}, nil
}

func (s *TrackerExportCleanupStore) Record(cleaned beads.ExportCleanup) error {
	if len(cleaned.Removed) == 0 {
		return nil
	}
	if cleaned.At.IsZero() {
		return fmt.Errorf("tracker export cleanup time is required")
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion int                 `json:"schema_version"`
		ProductID     domain.ProductID    `json:"product_id"`
		Cleanup       beads.ExportCleanup `json:"cleanup"`
	}{1, s.productID, cleaned})
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	name := cleaned.At.UTC().Format("20060102T150405.000000000") + "-" + rand.Text() + ".json"
	return root.CreateFile(name, append(encoded, '\n'), 0o600)
}
