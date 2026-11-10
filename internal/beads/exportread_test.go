package beads

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadExportReadsTheStatusesAskedForAndPassesOverOtherRecords(t *testing.T) {
	repository := t.TempDir()
	writeExportForRead(t, repository,
		`{"_type":"issue","id":"item-1","title":"Open","status":"open","priority":1,"notes":"Goal served: g","metadata":{"other":"x"}}`,
		`{"_type":"memory","id":"not-an-item","status":"open"}`,
		`{"_type":"issue","id":"item-2","title":"Closed","status":"closed","priority":2}`,
		``,
		`{"id":"item-3","title":"Blocked","status":"blocked","priority":0,"labels":["reliability"," "]}`,
	)
	items, err := ReadExport(repository, []string{"open", "blocked"})
	if err != nil {
		t.Fatalf("ReadExport() error = %v", err)
	}
	if len(items) != 2 || items[0].ID != "item-1" || items[1].ID != "item-3" {
		t.Fatalf("ReadExport() = %#v, want item-1 and item-3 in the export's order", items)
	}
	if items[0].Notes != "Goal served: g" || items[0].Priority != 1 || items[0].Title != "Open" {
		t.Fatalf("item-1 decoded as %#v", items[0])
	}
	if len(items[1].Labels) != 1 || items[1].Labels[0] != "reliability" {
		t.Fatalf("item-3 labels decoded as %#v", items[1].Labels)
	}

	every, err := ReadExport(repository, nil)
	if err != nil || len(every) != 3 {
		t.Fatalf("ReadExport(no statuses) = %d item(s), %v; want every work item", len(every), err)
	}
}

func TestReadExportRefusesAnExportItCannotRead(t *testing.T) {
	repository := t.TempDir()
	if _, err := ReadExport(repository, nil); err == nil {
		t.Fatal("a missing export read as an empty backlog")
	}
	writeExportForRead(t, repository, `{"_type":"issue","id":"item-1","status":"open"}`, `{"_type":"issue",`)
	if _, err := ReadExport(repository, nil); err == nil || !strings.Contains(err.Error(), "record 2") {
		t.Fatalf("a malformed export: error = %v, want it to name the record", err)
	}
	writeExportForRead(t, repository, `{"_type":"issue","id":"not a valid id!","status":"open"}`)
	if _, err := ReadExport(repository, nil); err == nil {
		t.Fatal("an export naming an invalid item id was read")
	}
}

func writeExportForRead(t *testing.T, repository string, lines ...string) {
	t.Helper()
	path := filepath.Join(repository, ExportPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
