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
