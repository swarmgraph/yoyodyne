package runstate

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The first load that carries an instance records when it was seen, and no later
// load moves it: a second observation at a later moment, an instance dropped
// from the configuration, and one put back all leave the first moment standing.
func TestAnInstanceIsFirstSeenOnceAndNeverMoved(t *testing.T) {
	t.Parallel()

	store, err := NewFirstSeenStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewFirstSeenStore() error = %v", err)
	}
	if seen, err := store.FirstSeen(); err != nil || len(seen) != 0 {
		t.Fatalf("FirstSeen() before any load = %v, %v; want none and no failure", seen, err)
	}
	first := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	if _, err := store.Observe([]string{"factory-pgm"}, first); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	later := first.Add(3 * time.Hour)
	if _, err := store.Observe([]string{"writing-pgm"}, later); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	seen, err := store.Observe([]string{"factory-pgm", "writing-pgm"}, later.Add(time.Hour))
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if !seen["factory-pgm"].Equal(first) || !seen["writing-pgm"].Equal(later) {
		t.Fatalf("first seen = %v; want factory-pgm at %s and writing-pgm at %s", seen, first, later)
	}
	if !strings.HasSuffix(store.Path(), "/projects/yoyodyne/state/program-managers/first-seen.json") {
		t.Errorf("Path() = %q, want it beside the instances' other records", store.Path())
	}
}

// A record that cannot be decoded is a failure to read, never an empty record
// that would let the next load re-seed every instance at a later moment.
func TestAnUnreadableFirstSeenRecordIsAFailureAndIsNotOverwritten(t *testing.T) {
	t.Parallel()

	store, err := NewFirstSeenStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe([]string{"factory-pgm"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FirstSeen(); err == nil {
		t.Fatal("FirstSeen() read a torn record as none")
	}
	if _, err := store.Observe([]string{"writing-pgm"}, time.Now()); err == nil {
		t.Fatal("Observe() rewrote a record it could not read")
	}
}

func TestTheFirstSeenStoreRefusesARelativeStateRoot(t *testing.T) {
	t.Parallel()

	if _, err := NewFirstSeenStore("relative", "yoyodyne"); err == nil {
		t.Fatal("NewFirstSeenStore() accepted a relative state root")
	}
}
