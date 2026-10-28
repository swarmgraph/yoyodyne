package beads

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// fakeTracker is bd as a measurement meets it: it answers listings, reads, and
// exports from the items it holds, and runs the cleanup and the squashes by
// shrinking the database file in whichever store BEADS_DIR names, which is the
// store the real bd would change. Every command it is given is kept.
type fakeTracker struct {
	mu       sync.Mutex
	commands []execution.Command
	// shrinkTo is the database file's size after each command that changes the
	// store, by verb.
	shrinkTo map[string]int
	// changeNotes is a verb after which the export carries different notes on
	// calc-2, and dropItem one after which it no longer carries calc-3.
	changeNotes string
	dropItem    string
	// fail is a verb that fails.
	fail string
	// changed is every verb run so far that changes the store.
	changed []string
}

func (f *fakeTracker) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	verb := command.Args[0]
	if verb == f.fail {
		return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "bd " + verb + ": storage backend does not support it"}, nil
	}
	switch verb {
	case "gc", "compact", "flatten":
		database := filepath.Join(environmentValue(command.Env, "BEADS_DIR"), "embeddeddolt", "calc", "data")
		if err := os.WriteFile(database, []byte(strings.Repeat("d", f.shrinkTo[verb])), 0o600); err != nil {
			return execution.ProcessResult{}, err
		}
		f.changed = append(f.changed, verb)
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"success":true}`}, nil
	case "list", "ready":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `[{"id":"calc-1","title":"a","status":"open","priority":2,"issue_type":"task"},{"id":"calc-3","title":"c","status":"open","priority":2,"issue_type":"task"}]`}, nil
	case "show":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: fmt.Sprintf(`[{"id":%q,"title":"a","status":"open","priority":2,"issue_type":"task"}]`, command.Args[1])}, nil
	case "export":
		_, err := io.WriteString(command.RawStdout, f.export())
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, err
	}
	return execution.ProcessResult{}, fmt.Errorf("unexpected command %v", command.Args)
}

func (f *fakeTracker) export() string {
	notes := "first note"
	lines := []string{`{"id":"calc-1","title":"a","status":"open","notes":"","priority":2}`}
	for _, verb := range f.changed {
		if verb == f.changeNotes {
			notes = "first note rewritten"
		}
	}
	lines = append(lines, fmt.Sprintf(`{"priority":2,"id":"calc-2","title":"b","status":"closed","notes":%q}`, notes))
	dropped := false
	for _, verb := range f.changed {
		dropped = dropped || verb == f.dropItem
	}
	if !dropped {
		lines = append(lines, `{"id":"calc-3","title":"c","status":"open","notes":"","priority":2}`)
	}
	return strings.Join(lines, "\n") + "\n"
}

func (f *fakeTracker) ran() []execution.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execution.Command(nil), f.commands...)
}

func environmentValue(environment []string, name string) string {
	value := ""
	for _, entry := range environment {
		if found, ok := strings.CutPrefix(entry, name+"="); ok {
			value = found
		}
	}
	return value
}

// liveStore is a project with a store of its own: metadata, a 4096-byte
// database file, and a lock file. It returns the project and its store.
func liveStore(t *testing.T) (project, store string) {
	t.Helper()
	project = t.TempDir()
	store = filepath.Join(project, ".beads")
	storeAt(t, store, "metadata.json", "embeddeddolt/", "embeddeddolt/calc/")
	writeFile(t, filepath.Join(store, "embeddeddolt", "calc", "data"), strings.Repeat("x", 4096))
	writeFile(t, filepath.Join(store, "embeddeddolt", "calc", "LOCK"), "")
	// Old enough that a write in the same second would show in the time too.
	old := time.Now().Add(-time.Hour)
	if err := filepath.WalkDir(store, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}
	return project, store
}

// snapshot is every file under dir with its contents and modification time.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(dir, path)
		content := ""
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content = string(data)
		}
		files[relative] = fmt.Sprintf("%s %s %d", info.Mode(), info.ModTime().Format(time.RFC3339Nano), len(content)) + content
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func workDirectory(t *testing.T) (*repowrite.PinnedRoot, string) {
	t.Helper()
	// Resolved, since the temporary directory may be reached through a link and
	// a pinned directory is opened only through real directories.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work, err := repowrite.OpenPinnedRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { work.Close() })
	return work, dir
}

func defaultShrinks() map[string]int {
	return map[string]int{"gc": 3072, "compact": 2048, "flatten": 512}
}

// A measurement copies the store and, on the copy only, measures its size,
// times the three reads, and takes an export as it is, after the cleanup, after
// squashing old history, and after squashing all of it, comparing each export
// with the one before. The live store is left exactly as it was, even with
// BEADS_DIR in the environment naming it, and the copy is removed afterwards.
//
// Not parallel: it sets BEADS_DIR for the whole process, to the live store, so
// that a bd call the measurement failed to point at the copy would land there.
func TestAMeasurementMeasuresEveryStageOnACopyAndLeavesTheLiveStoreAlone(t *testing.T) {
	project, store := liveStore(t)
	t.Setenv("BEADS_DIR", store)
	before := snapshot(t, store)
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	work, workDir := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil {
		t.Fatalf("MeasureStore() error = %v", err)
	}

	if after := snapshot(t, store); !reflect.DeepEqual(after, before) {
		t.Errorf("the live store changed:\nbefore %v\nafter  %v", before, after)
	}
	copyDir := filepath.Join(workDir, measuredCopyName)
	for _, command := range tracker.ran() {
		if command.Dir != copyDir {
			t.Errorf("bd %v ran in %s, want the copy's directory %s", command.Args, command.Dir, copyDir)
		}
		if got := environmentValue(command.Env, "BEADS_DIR"); got != filepath.Join(copyDir, ".beads") {
			t.Errorf("bd %v ran with BEADS_DIR=%s, want the copy's store", command.Args, got)
		}
	}
	if _, err := os.Stat(copyDir); !os.IsNotExist(err) {
		t.Errorf("the copy is still there after the measurement: %v", err)
	}

	if !measured.Measured() || measured.Store != canonicalPath(store) || measured.StartedAt.IsZero() || measured.Took <= 0 {
		t.Fatalf("MeasureStore() = %+v, want a measurement of %s", measured, store)
	}
	wantStages := []string{StageAsItIs, StageCleanedUp, StageOldSquashed, StageAllSquashed}
	wantSizes := []int64{4096, 3072, 2048, 512}
	if len(measured.Stages) != len(wantStages) {
		t.Fatalf("stages = %+v, want %v", measured.Stages, wantStages)
	}
	wantCommands := [][]string{nil, {"gc", "--skip-decay", "--force"}, {"compact", "--days", "30", "--force", "--json"}, {"flatten", "--force", "--json"}}
	for index, stage := range measured.Stages {
		if stage.Name != wantStages[index] || stage.Size != wantSizes[index] || stage.Failed != "" || !reflect.DeepEqual(stage.Command, wantCommands[index]) {
			t.Errorf("stage %d = %+v, want %q at %d bytes made by %v", index, stage, wantStages[index], wantSizes[index], wantCommands[index])
		}
		for _, name := range []string{ReadOpenListing, ReadReadyListing, ReadOneItem} {
			read, found := stage.Read(name)
			if !found || read.Failed != "" || read.Items == 0 {
				t.Errorf("stage %q read %q = %+v, %t, want it timed and answered", stage.Name, name, read, found)
			}
		}
		if open, _ := stage.Read(ReadOpenListing); !reflect.DeepEqual(open.Command, []string{"list", "--json", "--limit=0", "--status=open"}) {
			t.Errorf("open listing = %v, want the open items listed whole", open.Command)
		}
		if one, _ := stage.Read(ReadOneItem); !reflect.DeepEqual(one.Command, []string{"show", "calc-1", "--json"}) {
			t.Errorf("single read = %v, want the first open item read at every stage", one.Command)
		}
		if stage.Export.Records != 3 || stage.Export.Failed != "" {
			t.Errorf("stage %q export = %+v, want three records", stage.Name, stage.Export)
		}
		if index > 0 && !stage.Export.Unchanged() {
			t.Errorf("stage %q export = %+v, want it compared and unchanged", stage.Name, stage.Export)
		}
	}
	if measured.Stages[0].Export.Compared {
		t.Errorf("the first export = %+v, want nothing to compare it with", measured.Stages[0].Export)
	}
	exports := 0
	for _, command := range tracker.ran() {
		if command.Args[0] == "export" {
			exports++
			if !reflect.DeepEqual(command.Args, []string{"export", "--all"}) {
				t.Errorf("export = %v, want every record exported", command.Args)
			}
		}
	}
	if exports != 4 {
		t.Errorf("exports = %d, want one at each of the four stages", exports)
	}
}

// An export that differs from the one before is named for what differs: an
// item whose notes changed, and an item no longer there, each by its id.
func TestAMeasurementNamesWhatAnExportChanged(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks(), changeNotes: "compact", dropItem: "flatten"}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	work, _ := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil {
		t.Fatalf("MeasureStore() error = %v", err)
	}
	cleaned, _ := measured.Stage(StageCleanedUp)
	squashed, _ := measured.Stage(StageOldSquashed)
	flattened, _ := measured.Stage(StageAllSquashed)
	if !cleaned.Export.Unchanged() {
		t.Errorf("export after the cleanup = %+v, want it unchanged", cleaned.Export)
	}
	if squashed.Export.Unchanged() || !reflect.DeepEqual(squashed.Export.Changed, []string{"calc-2"}) || squashed.Export.Differing != 1 {
		t.Errorf("export after squashing old history = %+v, want calc-2 named as changed", squashed.Export)
	}
	if flattened.Export.Unchanged() || !reflect.DeepEqual(flattened.Export.Missing, []string{"calc-3"}) || flattened.Export.Records != 2 {
		t.Errorf("export after squashing all history = %+v, want calc-3 named as missing", flattened.Export)
	}
}

// A store another process has open is not copied: nothing is run, and the
// measurement names who had it open.
func TestAStoreSomebodyHasOpenIsNotCopied(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	holder := StoreHolder{PID: 4242, Command: "bd list --json"}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{holders: []StoreHolder{holder}}}
	work, workDir := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil || measured.Measured() || !reflect.DeepEqual(measured.Busy, []StoreHolder{holder}) || !measured.StartedAt.IsZero() {
		t.Fatalf("MeasureStore() = %+v, %v, want nothing measured and the holder named", measured, err)
	}
	if ran := tracker.ran(); len(ran) != 0 {
		t.Errorf("bd ran %d commands, want none", len(ran))
	}
	if entries, _ := os.ReadDir(workDir); len(entries) != 0 {
		t.Errorf("work directory holds %v, want nothing copied", entries)
	}
}

// A store written to while it was copied leaves a copy that is no one state of
// it, so the copy is discarded and nothing is measured.
func TestACopyMadeWhileTheStoreChangedIsDiscarded(t *testing.T) {
	t.Parallel()
	project, store := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	client.storeCopied = func() {
		writeFile(t, filepath.Join(store, "embeddeddolt", "calc", "data"), strings.Repeat("y", 5000))
	}
	work, workDir := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil || measured.Measured() || !strings.Contains(measured.Moved, "embeddeddolt/calc/data was written to") {
		t.Fatalf("MeasureStore() = %+v, %v, want the copy discarded naming the file written to", measured, err)
	}
	if ran := tracker.ran(); len(ran) != 0 {
		t.Errorf("bd ran %d commands, want none on a discarded copy", len(ran))
	}
	if _, err := os.Stat(filepath.Join(workDir, measuredCopyName)); !os.IsNotExist(err) {
		t.Errorf("the discarded copy is still there: %v", err)
	}
}

// A store holding a symbolic link is not copied, since bd run on the copy
// could write through the link into the live store.
func TestAStoreHoldingASymbolicLinkIsNotCopied(t *testing.T) {
	t.Parallel()
	project, store := liveStore(t)
	if err := os.Symlink(filepath.Join(store, "embeddeddolt", "calc", "data"), filepath.Join(store, "embeddeddolt", "calc", "linked")); err != nil {
		t.Fatal(err)
	}
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	work, workDir := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err == nil || !strings.Contains(err.Error(), "holds a symbolic link") || measured.Measured() {
		t.Fatalf("MeasureStore() = %+v, %v, want it refused for the link", measured, err)
	}
	if ran := tracker.ran(); len(ran) != 0 {
		t.Errorf("bd ran %d commands, want none", len(ran))
	}
	if entries, _ := os.ReadDir(workDir); len(entries) != 0 {
		t.Errorf("work directory holds %v, want nothing copied", entries)
	}
}

// A stage whose command fails ends the measurement there, recorded as failed
// with what bd said, and nothing after it is run.
func TestAFailedStageEndsTheMeasurement(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks(), fail: "compact"}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	work, _ := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil {
		t.Fatalf("MeasureStore() error = %v", err)
	}
	if len(measured.Stages) != 3 {
		t.Fatalf("stages = %+v, want as it is, the cleanup, and the failed squash", measured.Stages)
	}
	failed := measured.Stages[2]
	if failed.Name != StageOldSquashed || !strings.Contains(failed.Failed, "storage backend does not support it") || len(failed.Reads) != 0 {
		t.Errorf("failed stage = %+v, want the squash failed with bd's words and nothing read after it", failed)
	}
	for _, command := range tracker.ran() {
		if command.Args[0] == "flatten" {
			t.Errorf("bd flatten ran after the squash before it failed")
		}
	}
}

// A copy an earlier measurement left behind, when its process was killed, is
// removed before the next copy is made.
func TestACopyLeftBehindIsRemovedFirst(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	work, workDir := workDirectory(t)
	if err := os.MkdirAll(filepath.Join(workDir, measuredCopyName, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workDir, measuredCopyName, ".beads", "leftover"), strings.Repeat("z", 100000))

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil || !measured.Measured() {
		t.Fatalf("MeasureStore() = %+v, %v, want a measurement", measured, err)
	}
	if first := measured.Stages[0]; first.Size != 4096 {
		t.Errorf("first stage size = %d, want the store's own size with nothing left over from before", first.Size)
	}
	if _, err := os.Stat(filepath.Join(workDir, measuredCopyName)); !os.IsNotExist(err) {
		t.Errorf("the copy is still there: %v", err)
	}
}
