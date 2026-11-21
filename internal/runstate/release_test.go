package runstate

import (
	"os"
	"testing"
	"time"
)

// The release is written by an operator's process and read by the one serving
// the wait, so it has to survive the gap between them exactly as the run state
// does — and it must be readable without holding the run's lease, because the
// process that holds it is the one asleep.
func TestReleaseSurvivesTheProcessThatRecordedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStoreAt(t, root)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, released, err := store.ReleasedWait(state.RunID); err != nil || released {
		t.Fatalf("ReleasedWait() = %t, %v, want no release recorded", released, err)
	}

	recorded := Release{
		SchemaVersion: ReleaseSchemaVersion,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		ReleasedAt:    time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC),
	}
	if err := store.RecordRelease(recorded); err != nil {
		t.Fatalf("RecordRelease() error = %v", err)
	}
	// A second release of the same run says the same thing, and an operator
	// cannot see whether the run re-parked between the two, so it is accepted.
	if err := store.RecordRelease(recorded); err != nil {
		t.Fatalf("second RecordRelease() error = %v", err)
	}

	// A separate store over the same root is what the waiting process is.
	loaded, released, err := newStoreAt(t, root).ReleasedWait(state.RunID)
	if err != nil || !released {
		t.Fatalf("ReleasedWait() = %t, %v, want the recorded release", released, err)
	}
	if loaded.WorkItemID != state.WorkItemID || !loaded.ReleasedAt.Equal(recorded.ReleasedAt) {
		t.Fatalf("ReleasedWait() = %#v, want the release as it was recorded", loaded)
	}

	if err := store.ClearRelease(state.RunID); err != nil {
		t.Fatalf("ClearRelease() error = %v", err)
	}
	if _, released, err := store.ReleasedWait(state.RunID); err != nil || released {
		t.Fatalf("ReleasedWait() after clearing = %t, %v, want the release consumed", released, err)
	}
	// Clearing what is already gone is what a run does after acting on a release
	// it consumed in an earlier process, so it is not an error.
	if err := store.ClearRelease(state.RunID); err != nil {
		t.Fatalf("second ClearRelease() error = %v", err)
	}
}

// A release lives beside the run state in the same directory, so discovering
// runs must not mistake one for a run. Getting this wrong would not fail the
// release — it would fail every command that lists what is in flight.
func TestReleaseIsNotDiscoveredAsARun(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.RecordRelease(Release{
		SchemaVersion: ReleaseSchemaVersion,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		ReleasedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordRelease() error = %v", err)
	}

	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 1 || incomplete[0].RunID != state.RunID {
		t.Fatalf("Incomplete() = %#v, want only the run itself", incomplete)
	}
	outstanding, err := store.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 1 {
		t.Fatalf("Outstanding() = %#v, want only the run itself", outstanding)
	}
}

// A release that cannot be read is reported rather than treated as an absence:
// an operator whose release was silently dropped is back where the verb exists
// to get them out of.
func TestUnreadableReleaseIsReportedRatherThanIgnored(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.releasePath(state.RunID)
	if err != nil {
		t.Fatalf("releasePath() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, released, err := store.ReleasedWait(state.RunID); err == nil || released {
		t.Fatalf("ReleasedWait() = %t, %v, want the unreadable record reported", released, err)
	}
}

// A release for another product's run is refused, for the same reason every
// other record here is: the state root is shared and the product scopes it.
func TestReleaseRefusesAnotherProductsRun(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.RecordRelease(Release{
		SchemaVersion: ReleaseSchemaVersion,
		ProductID:     "other-product",
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		ReleasedAt:    time.Now().UTC(),
	}); err == nil {
		t.Fatal("RecordRelease() accepted another product's release")
	}
}

func newStoreAt(t *testing.T, root string) *Store {
	t.Helper()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

// A run whose checkout was kept is found through the index without being owed
// anything, and a run that kept none is not listed.
func TestKeptCheckoutsAreListedWithoutBeingOutstanding(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if kept, err := store.KeptCheckouts(); err != nil || len(kept) != 0 {
		t.Fatalf("KeptCheckouts() = %v, %v, want none", kept, err)
	}
	state.Status = StatusSucceeded
	state.Phase = PhaseComplete
	completed := time.Now().UTC()
	state.CompletedAt = &completed
	state.KeptCheckout = "the checkout of run " + state.RunID + " was kept because it holds unsaved.txt"
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	kept, err := store.KeptCheckouts()
	if err != nil || len(kept) != 1 || kept[0].RunID != state.RunID {
		t.Fatalf("KeptCheckouts() = %v, %v, want the run", kept, err)
	}
	if outstanding, err := store.Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("Outstanding() = %v, %v, want none", outstanding, err)
	}
}
