package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinkedRevisionsSinceNamesOnlyLinkedDocumentsRevisedAfterConfirmation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	confirmed := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	file := func(id string, at time.Time) {
		content := fmt.Sprintf("---\nid: %s\nkind: design\ntitle: %s\nsupports:\n    - brief\nstatus: active\nrevisions:\n    - action: created\n      by: architect\n      at: %s\n      reason: made\n---\n# %s\n", id, id, at.Format(time.RFC3339), id)
		if err := os.MkdirAll(filepath.Join(root, "docs", "designs"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "docs", "designs", id+".md"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file("supported", confirmed.AddDate(0, 0, 3))
	file("linked", confirmed.AddDate(0, 0, 3))
	file("older", confirmed.AddDate(0, 0, -3))
	file("unrelated", confirmed.AddDate(0, 0, 3))
	candidate := ConfirmedDocument{
		Artifact: Artifact{ID: "gtx", Path: "docs/designs/gtx.md", Supports: []string{"supported"},
			Revisions: []Revision{{At: confirmed.Add(-time.Hour)}}, Approvals: []Approval{{At: confirmed}}},
		Content: "---\nid: gtx\n---\nSee [linked](linked.md#part), [older](./older.md) and [site](https://example.com/unrelated.md).\n",
	}
	store := Store{RepositoryRoot: root, Homes: []string{"docs/designs"}}
	got, err := store.LinkedRevisionsSince(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "linked" || got[1].ID != "supported" || !got[0].RevisedAt.Equal(confirmed.AddDate(0, 0, 3)) {
		t.Fatalf("revised = %+v, want linked and supported only", got)
	}
}
