package beads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// realStore is a tracker made by the installed bd holding two items, one with a
// label, a dependency and a comment, and the client that reads it.
func realStore(t *testing.T) (Client, string) {
	t.Helper()
	project := newTracker(t)
	var ids []string
	for _, title := range []string{"first", "second"} {
		created, err := (Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}).Create(context.Background(), NewWorkItem{Title: title, Description: "d", Type: "task"})
		if err != nil {
			t.Fatalf("Create(%s) error = %v", title, err)
		}
		ids = append(ids, created.ID)
	}
	runCommand(t, project, "bd", "dep", "add", ids[0], ids[1])
	runCommand(t, project, "bd", "label", "add", ids[0], "reliability")
	runCommand(t, project, "bd", "comments", "add", ids[0], "a comment")
	runCommand(t, project, "bd", "update", ids[1], "--append-notes", "a note")
	return Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout, Holders: fixedHolders{}}, project
}

func exportOf(t *testing.T, client Client) map[string]string {
	t.Helper()
	records, export := client.measuredExport(context.Background())
	if export.Failed != "" {
		t.Fatalf("export failed: %s", export.Failed)
	}
	return records
}

func pinnedDirectory(t *testing.T) *repowrite.PinnedRoot {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := repowrite.OpenPinnedRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

// A rehearsal builds a new store from a copy and finds its export equal, and
// leaves the live store and the directory it worked in as they were.
func TestARehearsalBuildsAStoreFromACopyAndLeavesTheLiveStoreAlone(t *testing.T) {
	t.Parallel()
	client, project := realStore(t)
	before := exportOf(t, client)
	work := pinnedDirectory(t)

	rebuild, err := client.RehearseRebuild(context.Background(), work)
	if err != nil {
		t.Fatalf("RehearseRebuild() error = %v", err)
	}
	if !rebuild.Passed() || rebuild.Records != len(before) || rebuild.Took <= 0 || rebuild.SizeAfter <= 0 || rebuild.Swapped {
		t.Fatalf("rebuild = %+v, want one that passed over %d records and swapped nothing", rebuild, len(before))
	}
	if entries, _ := os.ReadDir(work.Path()); len(entries) != 0 {
		t.Errorf("the work directory still holds %d entries", len(entries))
	}
	if after := exportOf(t, client); len(after) != len(before) {
		t.Errorf("the live store holds %d records, want %d", len(after), len(before))
	}
	_ = project
}

// A rebuild of the live store moves the old database aside, records where
// first, puts the new one in its place with every record equal, and the store
// keeps working for later writes.
func TestARebuildReplacesTheLiveDatabaseAndKeepsTheOldOne(t *testing.T) {
	t.Parallel()
	client, project := realStore(t)
	before := exportOf(t, client)
	work, kept := pinnedDirectory(t), pinnedDirectory(t)
	recordedBeforeMove := false
	live := filepath.Join(project, ".beads", "embeddeddolt")

	rebuild, err := client.RebuildStore(context.Background(), work, kept, "20261118T000000.000000000Z-A", func(r StoreRebuild) error {
		_, statErr := os.Stat(live)
		recordedBeforeMove = statErr == nil && r.Kept == filepath.Join(kept.Path(), "20261118T000000.000000000Z-A")
		return nil
	})
	if err != nil {
		t.Fatalf("RebuildStore() error = %v", err)
	}
	if !rebuild.Swapped || !rebuild.Passed() || rebuild.HeldFor <= 0 || !recordedBeforeMove {
		t.Fatalf("rebuild = %+v, recorded before the move = %v, want a swap recorded first", rebuild, recordedBeforeMove)
	}
	if _, err := os.Stat(filepath.Join(rebuild.Kept, "embeddeddolt")); err == nil {
		t.Errorf("the kept copy is nested inside itself")
	}
	if _, err := os.Stat(rebuild.Kept); err != nil {
		t.Errorf("the kept copy is not at %s: %v", rebuild.Kept, err)
	}
	after := exportOf(t, client)
	if len(after) != len(before) {
		t.Fatalf("the rebuilt store holds %d records, want %d", len(after), len(before))
	}
	for id, record := range before {
		if after[id] != record {
			t.Errorf("record %s changed:\n%s\nwant\n%s", id, after[id], record)
		}
	}
	items, err := client.ListStatuses(context.Background(), []string{"open"})
	if err != nil || len(items) != 2 {
		t.Fatalf("listing the rebuilt store = %v, %v, want 2 items", items, err)
	}
	if _, err := client.Create(context.Background(), NewWorkItem{Title: "third", Description: "d", Type: "task"}); err != nil {
		t.Errorf("a write after the rebuild failed: %v", err)
	}
}

// Another process having the store open stops a rebuild before anything is
// built or moved.
func TestARebuildWaitsForAStoreSomeoneHasOpen(t *testing.T) {
	t.Parallel()
	client, project := realStore(t)
	client.Holders = fixedHolders{holders: []StoreHolder{{PID: 42, Command: "bd"}}}
	work, kept := pinnedDirectory(t), pinnedDirectory(t)

	rebuild, err := client.RebuildStore(context.Background(), work, kept, "x-A", func(StoreRebuild) error { t.Error("recorded"); return nil })
	if err != nil || len(rebuild.Busy) != 1 || rebuild.Swapped {
		t.Fatalf("rebuild = %+v, %v, want one stopped by its holder", rebuild, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".beads", "embeddeddolt")); err != nil {
		t.Errorf("the live database is gone: %v", err)
	}
}

// A call made while the store is being rebuilt waits for it and is not lost: the
// store lock is exclusive for the rebuild and shared for every other call.
func TestACallWaitsBehindTheExclusiveStoreLock(t *testing.T) {
	t.Parallel()
	_, project := realStore(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: 300 * 1000000}
	unlock, err := client.lockStore(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.run(context.Background(), "list", "--json")
	unlock()
	if err == nil || !strings.Contains(err.Error(), "rebuilt") {
		t.Fatalf("a call during the rebuild error = %v, want one that says the store was being rebuilt", err)
	}
	client.Timeout = conformanceTimeout
	if _, err := client.run(context.Background(), "list", "--json"); err != nil {
		t.Errorf("a call after the rebuild error = %v", err)
	}
}

// A new database that cannot be moved into place leaves the old one where it
// was: the store is never left without a database.
func TestAFailedMoveIntoPlacePutsTheOldDatabaseBack(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	database := filepath.Join(store, "embeddeddolt")
	if err := os.MkdirAll(database, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(database, "data"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	work, kept := pinnedDirectory(t), pinnedDirectory(t)
	rebuild, err := swapDatabases(store, database, work, filepath.Join(work.Path(), "fresh", ".beads", "embeddeddolt"), kept, "x-A", StoreRebuild{Records: 1}, func(StoreRebuild) error { return nil })
	if err == nil || rebuild.Swapped || !rebuild.Restored || rebuild.Step != RebuildMove {
		t.Fatalf("swap = %+v, %v, want a failed move that restored the old database", rebuild, err)
	}
	if data, err := os.ReadFile(filepath.Join(database, "data")); err != nil || string(data) != "old" {
		t.Errorf("the old database = %q, %v, want it back in place", data, err)
	}
}
