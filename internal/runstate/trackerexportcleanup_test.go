package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

func TestTrackerExportCleanupRecordsEveryRemovalWithoutOverwriting(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerExportCleanupStore(t.TempDir(), "product")
	if err != nil {
		t.Fatal(err)
	}
	cleaned := beads.ExportCleanup{At: time.Now().UTC(), Removed: []beads.ExportTemporary{{
		Path: "/checkout/.beads/.~issues.jsonl.1", Bytes: 12, ModifiedAt: time.Now().Add(-48 * time.Hour).UTC(),
	}}}
	for range 2 {
		if err := store.Record(cleaned); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Record(beads.ExportCleanup{At: cleaned.At}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("records = %v, %v", entries, err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(store.root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			ProductID string              `json:"product_id"`
			Cleanup   beads.ExportCleanup `json:"cleanup"`
		}
		if err := json.Unmarshal(content, &record); err != nil {
			t.Fatal(err)
		}
		if record.ProductID != "product" || len(record.Cleanup.Removed) != 1 || record.Cleanup.Removed[0] != cleaned.Removed[0] {
			t.Fatalf("record = %+v", record)
		}
	}
}

func TestTrackerExportCleanupRecordRefusesAReplacedStateDirectory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	store, err := NewTrackerExportCleanupStore(directory, "product")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(directory, "projects")); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(beads.ExportCleanup{At: time.Now(), Removed: []beads.ExportTemporary{{Path: "removed"}}}); err == nil {
		t.Fatal("the removal record escaped through a replaced state directory")
	}
}
