package beads

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestTrackerWritesDisableAutomaticExportEvenWhenInherited(t *testing.T) {
	t.Parallel()
	environment := withoutAutoExport([]string{"BD_EXPORT_AUTO=true", "PATH=/tools", "BD_EXPORT_AUTO=1"})
	if !slices.Equal(environment, []string{"PATH=/tools", "BD_EXPORT_AUTO=false"}) {
		t.Fatalf("environment = %v", environment)
	}
	runner := &fakeRunner{responses: []string{"updated"}}
	if _, err := (Client{Runner: runner}).run(context.Background(), "update", "item"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runner.commands[0].Env, "BD_EXPORT_AUTO=false") {
		t.Fatal("the bd invocation did not disable auto-export")
	}
}

func TestTrackerExportPublishesOnlyACompleteSnapshotAndSharesRecentCopies(t *testing.T) {
	t.Parallel()
	repository := exportRepository(t)
	runner := &fakeRunner{responses: []string{"{\"id\":\"new-item\",\"notes\":\"exact bytes\"}\n", "not JSON\n"}}
	client := Client{Dir: repository, Runner: runner}
	if err := client.RefreshExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repository, ExportPath)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshExportIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatal("a recent export was rewritten for another reader")
	}
	if err := client.RefreshExport(context.Background()); err == nil {
		t.Fatal("an invalid export was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed export changed the snapshot: %q, %v", after, err)
	}
	if runner.commands[0].RawStdout == nil || !slices.Equal(runner.commands[0].Args, []string{"export"}) {
		t.Fatal("the export was not read as exact stdout bytes")
	}
	// A killed export also leaves the previous file intact.
	runner.results = []execution.ProcessResult{{Status: execution.ProcessTimedOut, Stderr: "killed before export finished"}}
	runner.commands, runner.args = nil, nil
	if err := client.RefreshExport(context.Background()); err == nil {
		t.Fatal("a timed-out export was accepted")
	}
	after, err = os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("timed-out export changed the snapshot: %q, %v", after, err)
	}
}

func TestTrackerExportRefusesAnEscapingDirectory(t *testing.T) {
	t.Parallel()
	repository, outside := t.TempDir(), exportRepository(t)
	if err := os.Symlink(filepath.Join(outside, ".beads"), filepath.Join(repository, ".beads")); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{responses: []string{"{\"id\":\"item\"}\n"}}
	if err := (Client{Dir: repository, Runner: runner}).RefreshExport(context.Background()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("escaping export error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("bd was asked before the write destination was confined")
	}
}

func exportRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".beads", "metadata.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repository
}

// An export that queued behind one which began after it was asked for is handed
// that one's snapshot rather than asking bd again, and only while the snapshot
// published is the one the stamp describes: a snapshot something else replaced
// since, or an export that began before this one was asked for, is exported
// again.
func TestAnExportBegunAfterTheAskIsSharedWhileItsSnapshotStands(t *testing.T) {
	t.Parallel()
	repository := exportRepository(t)
	runner := &fakeRunner{responses: []string{"{\"id\":\"first\"}\n", "{\"id\":\"second\"}\n", "{\"id\":\"third\"}\n"}}
	client := Client{Dir: repository, Runner: runner}
	if err := client.RefreshExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repository, ExportPath)
	stampPath := filepath.Join(repository, ".beads", exportStampName)
	stamp := func(started time.Time) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(exportStamp{StartedAt: started, ModifiedAt: info.ModTime(), Size: info.Size()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stampPath, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if kept, err := os.ReadFile(stampPath); err != nil || !strings.Contains(string(kept), "started_at") {
		t.Fatalf("stamp after an export = %q, %v; want when it began", kept, err)
	}

	// Stamped as begun after this call asks: what a caller queued behind a
	// later export finds once it holds the lock.
	stamp(time.Now().Add(time.Hour))
	if err := client.RefreshExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("bd was asked %d times; want the export begun after the ask shared", len(runner.commands))
	}

	// The same stamp over a snapshot something else has since replaced.
	if err := os.WriteFile(path, []byte("{\"id\":\"checked out\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("bd was asked %d times; want a replaced snapshot exported again", len(runner.commands))
	}

	// An export begun before this call asked holds nothing written since.
	stamp(time.Now().Add(-time.Second))
	if err := client.RefreshExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("bd was asked %d times; want an export begun before the ask taken again", len(runner.commands))
	}
	if published, err := os.ReadFile(path); err != nil || string(published) != "{\"id\":\"third\"}\n" {
		t.Fatalf("published snapshot = %q, %v", published, err)
	}
}

// A due export is taken at most once every five minutes, and then only where
// the store's database has changed since the published snapshot's export began.
func TestADueExportWaitsFiveMinutesAndRunsOnlyWhenTheStoreChanged(t *testing.T) {
	t.Parallel()
	repository := exportRepository(t)
	database := filepath.Join(repository, ".beads", "embeddeddolt", "yoyodyne", ".dolt", "noms")
	if err := os.MkdirAll(database, 0o700); err != nil {
		t.Fatal(err)
	}
	chunks := filepath.Join(database, "journal.idx")
	if err := os.WriteFile(chunks, []byte("written\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{responses: []string{"{\"id\":\"first\"}\n", "{\"id\":\"second\"}\n"}}
	client := Client{Dir: repository, Runner: runner}
	if err := client.RefreshExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repository, ExportPath)
	stampPath := filepath.Join(repository, ".beads", exportStampName)
	// age makes the published snapshot, and the export it came from, as old as
	// ago, and the database files as old as written.
	age := func(ago, written time.Duration) {
		t.Helper()
		published, began, changed := time.Now().Add(-ago), time.Now().Add(-ago-time.Second), time.Now().Add(-written)
		if err := os.Chtimes(path, published, published); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(chunks, changed, changed); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(exportStamp{StartedAt: began, ModifiedAt: info.ModTime(), Size: info.Size()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stampPath, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Changed since, but the last export is under five minutes old.
	age(4*time.Minute, time.Minute)
	if err := client.RefreshExportIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("bd was asked %d times; want no export within five minutes of the last", len(runner.commands))
	}

	// Due, but nothing in the store changed since that export began. A lock
	// taken since is not a change.
	age(6*time.Minute, time.Hour)
	if err := os.WriteFile(filepath.Join(database, "LOCK"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshExportIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("bd was asked %d times; want no export of a store nobody wrote to", len(runner.commands))
	}

	// Due, and the store was written after that export began.
	age(6*time.Minute, time.Minute)
	if err := client.RefreshExportIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("bd was asked %d times; want a changed store exported once due", len(runner.commands))
	}
	if published, err := os.ReadFile(path); err != nil || string(published) != "{\"id\":\"second\"}\n" {
		t.Fatalf("published snapshot = %q, %v", published, err)
	}
}

// Where nothing says what export the published snapshot came from, or no
// database can be found to compare against, a due export is taken: one export
// too many costs a read, and one too few publishes a snapshot older than the
// store.
func TestADueExportIsTakenWhereNothingSaysTheStoreIsUnchanged(t *testing.T) {
	t.Parallel()
	repository := exportRepository(t)
	runner := &fakeRunner{responses: []string{"{\"id\":\"first\"}\n", "{\"id\":\"second\"}\n", "{\"id\":\"third\"}\n"}}
	client := Client{Dir: repository, Runner: runner}
	if err := client.RefreshExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repository, ExportPath)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	// The stamp describes a snapshot of another time, so it says nothing.
	if err := client.RefreshExportIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("bd was asked %d times; want a snapshot no stamp describes exported once due", len(runner.commands))
	}
	// Stamped, but there is no database here to compare against.
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(exportStamp{StartedAt: old.Add(-time.Second), ModifiedAt: info.ModTime(), Size: info.Size()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".beads", exportStampName), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshExportIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("bd was asked %d times; want a store with no database to compare exported once due", len(runner.commands))
	}
}

// The export before a commit is always taken fresh, and refused where there is
// no store to take it from, rather than passed over the way a due export is.
func TestTheExportBeforeACommitIsTakenFreshOrRefused(t *testing.T) {
	t.Parallel()
	repository := exportRepository(t)
	runner := &fakeRunner{responses: []string{"{\"id\":\"first\"}\n", "{\"id\":\"second\"}\n"}}
	client := Client{Dir: repository, Runner: runner}
	for range 2 {
		if err := client.ExportBeforeCommit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.commands) != 2 {
		t.Fatalf("bd was asked %d times; want an export for each commit, however recent the last", len(runner.commands))
	}
	if published, err := os.ReadFile(filepath.Join(repository, ExportPath)); err != nil || string(published) != "{\"id\":\"second\"}\n" {
		t.Fatalf("published snapshot = %q, %v", published, err)
	}
	empty := Client{Dir: t.TempDir(), Runner: runner}
	if err := empty.ExportBeforeCommit(context.Background()); err == nil || !strings.Contains(err.Error(), "no tracker store") {
		t.Fatalf("export with no store = %v; want it refused", err)
	}
}
