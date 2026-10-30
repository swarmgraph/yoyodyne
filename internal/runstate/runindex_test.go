package runstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// createRun records one run of an item at a status, and returns it.
func createRun(t *testing.T, store *Store, item string, status Status) State {
	t.Helper()
	state := testState(t, status)
	state.WorkItemID = item
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return state
}

func writeRecordBytes(t *testing.T, store *Store, runID string, encoded []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(store.Root(), runID+".json"), encoded, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// The routine listings are answered from the index, so a record the index says
// ended is never read to answer them: an old record spoiled after it ended
// costs no listing of what is going and no reservation anything.
func TestIncompleteAndReserveNeverReadAnEndedRecord(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ended := createRun(t, store, "yoyodyne-old", StatusSucceeded)
	going := createRun(t, store, "yoyodyne-going", StatusRunning)
	writeRecordBytes(t, store, ended.RunID, []byte("{"))

	active, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v, want the ended record never read", err)
	}
	if len(active) != 1 || active[0].RunID != going.RunID {
		t.Fatalf("Incomplete() = %+v, want only the run going", active)
	}
	outstanding, err := store.Outstanding()
	if err != nil || len(outstanding) != 1 {
		t.Fatalf("Outstanding() = %d run(s), %v", len(outstanding), err)
	}
	fresh := testState(t, StatusPending)
	fresh.WorkItemID = "yoyodyne-fresh"
	lease, err := store.Reserve(context.Background(), fresh, 4)
	if err != nil {
		t.Fatalf("Reserve() error = %v, want an unreadable ended record not to refuse it", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// Building the index reads every record once, and a record that will not read
// but still says it ended is reported and left out rather than refusing the
// index: the reservation goes ahead, and the record is named where it can be
// seen.
func TestBuildingTheIndexReportsAnUnreadableEndedRecordAndReservesPastIt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ended := createRun(t, store, "yoyodyne-old", StatusSucceeded)
	// A record a newer build wrote: valid JSON saying it ended, with a schema
	// version this build refuses.
	writeRecordBytes(t, store, ended.RunID, []byte(`{"schema_version": 9999, "run_id": "`+ended.RunID+`", "work_item_id": "yoyodyne-old", "status": "succeeded"}`))
	if err := os.Remove(store.runIndexPath()); err != nil {
		t.Fatalf("Remove(index) error = %v", err)
	}

	fresh := testState(t, StatusPending)
	fresh.WorkItemID = "yoyodyne-fresh"
	lease, err := store.Reserve(context.Background(), fresh, 4)
	if err != nil {
		t.Fatalf("Reserve() error = %v, want the unreadable ended record stepped over", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	unreadable, err := store.UnreadableRuns()
	if err != nil {
		t.Fatalf("UnreadableRuns() error = %v", err)
	}
	if len(unreadable) != 1 || unreadable[0].Record != ended.RunID {
		t.Fatalf("UnreadableRuns() = %+v, want the ended record named", unreadable)
	}
	// The record is kept as it is: nothing about it was removed.
	if _, err := os.Stat(filepath.Join(store.Root(), ended.RunID+".json")); err != nil {
		t.Fatalf("the unreadable record is gone: %v", err)
	}
}

// A record that will not read and does not say it ended may be a run still
// going, so a listing of what is going refuses rather than steps over it — a
// run counted as absent is one a second start could duplicate.
func TestAnUnreadableRecordThatMayBeGoingStillRefusesTheListing(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	going := createRun(t, store, "yoyodyne-going", StatusRunning)
	writeRecordBytes(t, store, going.RunID, []byte(`{"schema_version": 9999, "run_id": "`+going.RunID+`", "work_item_id": "yoyodyne-going", "status": "running"}`))
	if err := os.Remove(store.runIndexPath()); err != nil {
		t.Fatalf("Remove(index) error = %v", err)
	}
	if _, err := store.Incomplete(); err == nil || !strings.Contains(err.Error(), "discover incomplete runs") {
		t.Fatalf("Incomplete() error = %v, want the listing refused", err)
	}
	// The item's own runs are refused too, and another item's are not.
	if _, err := store.Runs("yoyodyne-going"); err == nil {
		t.Fatal("Runs() of the unreadable record's item error = nil")
	}
	if runs, err := store.Runs("yoyodyne-other"); err != nil || len(runs) != 0 {
		t.Fatalf("Runs() of another item = %d, %v", len(runs), err)
	}
}

// An index missing is rebuilt from the records, and a record written round the
// store — a file the index does not name — is read and added when a reader
// notices it.
func TestTheIndexIsRebuiltAndPicksUpRecordsItDoesNotName(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	first := createRun(t, store, "yoyodyne-first", StatusRunning)
	if err := os.Remove(store.runIndexPath()); err != nil {
		t.Fatalf("Remove(index) error = %v", err)
	}
	active, err := store.Incomplete()
	if err != nil || len(active) != 1 || active[0].RunID != first.RunID {
		t.Fatalf("Incomplete() after removing the index = %+v, %v", active, err)
	}

	other := testState(t, StatusRunning)
	other.WorkItemID = "yoyodyne-second"
	encoded, err := encodeRecord("run state", other)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	writeRecordBytes(t, store, other.RunID, encoded)
	active, err = store.Incomplete()
	if err != nil || len(active) != 2 {
		t.Fatalf("Incomplete() after a record written round the store = %d run(s), %v", len(active), err)
	}
	if runs, err := store.Runs("yoyodyne-second"); err != nil || len(runs) != 1 {
		t.Fatalf("Runs() = %d, %v", len(runs), err)
	}
}

// A write that makes a run in flight names it so in the index before the record
// is written, so a write that fails part way — or a process that dies in
// between — leaves the index saying too much rather than too little.
func TestTheIndexNamesARunInFlightBeforeItsRecordSaysSo(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ended := createRun(t, store, "yoyodyne-ended", StatusFailed)
	live := ended
	live.Status = StatusRunning
	live.CompletedAt = nil
	failed := errors.New("the disk went away")
	if err := store.writeIndexed(live, func() error { return failed }); !errors.Is(err, failed) {
		t.Fatalf("writeIndexed() error = %v", err)
	}
	index, ok, err := store.readRunIndex()
	if err != nil || !ok {
		t.Fatalf("readRunIndex() = %v, %v", ok, err)
	}
	if entry := index.Runs[ended.RunID]; !entry.InFlight {
		t.Fatalf("index entry = %+v, want the run named in flight", entry)
	}
	// The record itself still says it ended, and the listing reads the record.
	if active, err := store.Incomplete(); err != nil || len(active) != 0 {
		t.Fatalf("Incomplete() = %+v, %v", active, err)
	}
}

// One reading lists each kind of run once, however many of its sections ask:
// a run recorded after the reading made its listing is not in it.
func TestOneReadingListsTheRunsOnce(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	createRun(t, store, "yoyodyne-first", StatusRunning)
	reading := store.OneReading()
	if recorded, err := reading.Recorded(); err != nil || len(recorded) != 1 {
		t.Fatalf("Recorded() = %d, %v", len(recorded), err)
	}
	createRun(t, store, "yoyodyne-second", StatusRunning)
	if active, err := reading.Incomplete(); err != nil || len(active) != 1 {
		t.Fatalf("the reading's Incomplete() = %d, %v, want the listing it already made", len(active), err)
	}
	if active, err := store.Incomplete(); err != nil || len(active) != 2 {
		t.Fatalf("a fresh Incomplete() = %d, %v", len(active), err)
	}
}

// What a finished stream spent is read from its log once and kept: a later
// report gives the same figures without reading the log, and one whose window
// the stream's spend is wholly before reads nothing of it but how far back
// the evidence reaches.
func TestSpendKeepsWhatAFinishedRunSpent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, _, _ := streamStores(t, root)
	store := newStreamStore(t, root)
	day := time.Date(2026, 8, 3, 12, 0, 0, 0, time.Local)
	ended := testState(t, StatusSucceeded)
	ended.StartedAt = day
	if err := runs.Create(ended); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, ended.RunID, 1, execution.EventRunStarted, day, nil)
	appendStreamEvent(t, runs, ended.RunID, 2, execution.EventRunCompleted, day, invocationPayload(2.5, 1, 2, 3, 4))

	first, err := store.Spend(SpendQuery{Now: day})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(first.Rows) != 1 || first.Rows[0].CostUSD != 2.5 || first.Rows[0].Status != string(StatusSucceeded) {
		t.Fatalf("first report rows = %+v", first.Rows)
	}

	// Spoil the log without changing its length or its time, so only a report
	// that read it again could notice.
	logPath := filepath.Join(runs.Root(), ended.RunID+eventLogSuffix)
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if err := os.WriteFile(logPath, []byte(strings.Repeat("x", int(info.Size()))), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chtimes(logPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	again, err := store.Spend(SpendQuery{Now: day})
	if err != nil {
		t.Fatalf("Spend() again error = %v", err)
	}
	if len(again.Rows) != 1 || again.Rows[0].CostUSD != 2.5 || again.Rows[0].Calls != 1 {
		t.Fatalf("a report from what was kept = %+v, want the figures the log gave", again.Rows)
	}

	// A week later the run's spend is wholly before a one-day window, so its
	// own kept file is not opened; the report still says how far back the
	// evidence reaches.
	if err := os.Remove(store.keptCallsPath(StreamRun, ended.RunID)); err != nil {
		t.Fatalf("Remove(kept calls) error = %v", err)
	}
	later, err := store.Spend(SpendQuery{Days: 1, Now: day.AddDate(0, 0, 7), Since: day.AddDate(0, 0, 6)})
	if err != nil {
		t.Fatalf("Spend() a week later error = %v", err)
	}
	if len(later.Rows) != 0 || len(later.Rolling) != 0 || later.Reaches != LocalDay(day) {
		t.Fatalf("a window after the spend = rows %+v, rolling %+v, reaches %q", later.Rows, later.Rolling, later.Reaches)
	}
}

// A record held as unreadable is read again as soon as its file changes, so a
// record repaired by hand — or rewritten by a build that reads it — stops
// refusing work without anybody rebuilding the index.
func TestARepairedRecordStopsRefusingTheListingWithoutARebuild(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	going := createRun(t, store, "yoyodyne-going", StatusRunning)
	good, err := os.ReadFile(filepath.Join(store.Root(), going.RunID+".json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	writeRecordBytes(t, store, going.RunID, []byte("{"))
	if err := os.Remove(store.runIndexPath()); err != nil {
		t.Fatalf("Remove(index) error = %v", err)
	}
	if _, err := store.Incomplete(); err == nil {
		t.Fatal("Incomplete() over a spoiled record that may be going error = nil")
	}
	if _, err := store.Runs("yoyodyne-going"); err == nil {
		t.Fatal("Runs() of the spoiled record's item error = nil")
	}

	// Repaired by hand: the index is not touched.
	writeRecordBytes(t, store, going.RunID, good)
	active, err := store.Incomplete()
	if err != nil || len(active) != 1 || active[0].RunID != going.RunID {
		t.Fatalf("Incomplete() after the repair = %+v, %v, want the repaired run", active, err)
	}
	if runs, err := store.Runs("yoyodyne-going"); err != nil || len(runs) != 1 {
		t.Fatalf("Runs() after the repair = %d, %v", len(runs), err)
	}
	if unreadable, err := store.UnreadableRuns(); err != nil || len(unreadable) != 0 {
		t.Fatalf("UnreadableRuns() after the repair = %+v, %v", unreadable, err)
	}
}

// A record whose file could not be opened at all — not read and refused — is
// tried again on the next look even though nothing about the file's contents
// changed, because what stopped it can pass on its own.
func TestARecordThatCouldNotBeOpenedIsTriedAgain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	going := createRun(t, store, "yoyodyne-going", StatusRunning)
	path := filepath.Join(store.Root(), going.RunID+".json")
	if err := os.Remove(store.runIndexPath()); err != nil {
		t.Fatalf("Remove(index) error = %v", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this process reads a file with no permissions, so it cannot be made unreadable here")
	}
	if _, err := store.Incomplete(); err == nil {
		t.Fatal("Incomplete() over a record nobody can open error = nil")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	if active, err := store.Incomplete(); err != nil || len(active) != 1 {
		t.Fatalf("Incomplete() once the record opens again = %d, %v", len(active), err)
	}
}
