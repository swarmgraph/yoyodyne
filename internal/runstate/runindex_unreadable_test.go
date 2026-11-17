package runstate

import (
	"os"
	"strings"
	"testing"
)

// One record that will not read among records that do: the listing a surface
// shows returns every readable run in flight and names the unreadable one by
// its run, its file, and what refused it, while the listing that acts on runs
// still refuses, because the unreadable record may be a run still going.
func TestAListingWithOneUnreadableRecordReturnsTheRestAndNamesIt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	first := createRun(t, store, "yoyodyne-first", StatusRunning)
	bad := createRun(t, store, "yoyodyne-bad", StatusRunning)
	second := createRun(t, store, "yoyodyne-second", StatusRunning)
	// A record a newer build wrote, with a value this build refuses, changed in
	// place: the index still names it as in flight.
	writeRecordBytes(t, store, bad.RunID, []byte(`{"schema_version": 9999, "run_id": "`+bad.RunID+`", "work_item_id": "yoyodyne-bad", "status": "running"}`))

	states, unreadable, err := store.IncompleteReadable()
	if err != nil {
		t.Fatalf("IncompleteReadable() error = %v, want the readable runs returned", err)
	}
	if len(states) != 2 || !hasRun(states, first.RunID) || !hasRun(states, second.RunID) {
		t.Fatalf("IncompleteReadable() = %+v, want the two readable runs", states)
	}
	if len(unreadable) != 1 || unreadable[0].Record != bad.RunID {
		t.Fatalf("unreadable = %+v, want the bad record named", unreadable)
	}
	path, _ := store.statePath(bad.RunID)
	if unreadable[0].Path != path || unreadable[0].Err == nil {
		t.Fatalf("unreadable = %+v, want its file %s and what refused it", unreadable[0], path)
	}
	if says := unreadable[0].Says(); !strings.Contains(says, bad.RunID) || !strings.Contains(says, path) || !strings.Contains(says, "could not be read") {
		t.Fatalf("Says() = %q, want the run, its file, and that it could not be read", says)
	}

	// The names are the index's own list: the index was rebuilt when the record
	// it chose would not read, so the record is now among its unreadable ones.
	listed, err := store.UnreadableRuns()
	if err != nil || len(listed) != 1 || listed[0].Record != bad.RunID || listed[0].Path != path {
		t.Fatalf("UnreadableRuns() = %+v, %v, want the index's list to name the same record", listed, err)
	}

	// The listing callers act on never treats the unreadable run as finished.
	if _, err := store.Incomplete(); err == nil || !strings.Contains(err.Error(), bad.RunID) {
		t.Fatalf("Incomplete() error = %v, want it refused naming %s", err, bad.RunID)
	}
}

// A record the index named as in flight, changed in place to one that will not
// read but says it ended, is a bad index entry: the listing rebuilds the index
// on finding it, the entry is cleared, and the listing answers with the rest —
// with nobody removing the index by hand.
func TestARebuildClearsAnIndexEntryWhoseRecordWillNotRead(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	going := createRun(t, store, "yoyodyne-going", StatusRunning)
	spoiled := createRun(t, store, "yoyodyne-spoiled", StatusRunning)
	writeRecordBytes(t, store, spoiled.RunID, []byte(`{"schema_version": 9999, "run_id": "`+spoiled.RunID+`", "work_item_id": "yoyodyne-spoiled", "status": "succeeded"}`))

	active, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v, want the bad entry cleared by a rebuild", err)
	}
	if len(active) != 1 || active[0].RunID != going.RunID {
		t.Fatalf("Incomplete() = %+v, want only the run going", active)
	}
	index, ok, err := store.readRunIndex()
	if err != nil || !ok {
		t.Fatalf("readRunIndex() = %v, %v", ok, err)
	}
	if _, named := index.Runs[spoiled.RunID]; named {
		t.Fatalf("the index still names %s as a readable run: %+v", spoiled.RunID, index.Runs[spoiled.RunID])
	}
	if _, unreadable := index.Unreadable[spoiled.RunID]; !unreadable {
		t.Fatalf("the index does not hold %s as unreadable: %+v", spoiled.RunID, index.Unreadable)
	}
	// The record itself is kept as it is.
	if _, err := os.Stat(index.pathOf(store, spoiled.RunID)); err != nil {
		t.Fatalf("the spoiled record is gone: %v", err)
	}
}

// A record changed in place by something that went round the store — an older
// build still running through a redeploy — leaves the index saying what the
// record used to: a run made live again is missing from the runs in flight
// until the index is rebuilt, and the rebuild picks it up.
func TestARebuildPicksUpARecordChangedInPlaceOutsideTheStore(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ended := createRun(t, store, "yoyodyne-revived", StatusSucceeded)
	revived := testState(t, StatusRunning)
	revived.RunID, revived.WorkItemID = ended.RunID, ended.WorkItemID
	live, err := encodeRecord("run state", revived)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	if active, err := store.Incomplete(); err != nil || len(active) != 0 {
		t.Fatalf("Incomplete() after the run ended = %+v, %v", active, err)
	}
	// Written back in place as live, round the store.
	writeRecordBytes(t, store, revived.RunID, live)
	if active, err := store.Incomplete(); err != nil || len(active) != 0 {
		t.Fatalf("Incomplete() before a rebuild = %+v, %v; the index was expected not to know yet", active, err)
	}

	if err := store.RebuildRunIndex(); err != nil {
		t.Fatalf("RebuildRunIndex() error = %v", err)
	}
	active, err := store.Incomplete()
	if err != nil || len(active) != 1 || active[0].RunID != revived.RunID {
		t.Fatalf("Incomplete() after the rebuild = %+v, %v, want the revived run", active, err)
	}
}

func hasRun(states []State, runID string) bool {
	for _, state := range states {
		if state.RunID == runID {
			return true
		}
	}
	return false
}

func (x runIndex) pathOf(store *Store, runID string) string {
	path, _ := store.statePath(runID)
	return path
}
