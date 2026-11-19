package runstate

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedRecordsStateRoundTrips(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewSharedRecordsStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSharedRecordsStore() error = %v", err)
	}
	if _, found, err := store.Load(); err != nil || found {
		t.Fatalf("Load() = found %v, error %v, want nothing recorded", found, err)
	}
	sharedAt := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	want := SharedRecordsState{
		CheckedAt: sharedAt.Add(time.Minute),
		Fetched:   "0123456789abcdef0123456789abcdef01234567",
		SharedAt:  &sharedAt,
		Waiting:   2,
		Problem:   "could not reach the remote",
		Copied:    3,
		// Save sets these two; whatever the caller put there is replaced.
		SchemaVersion: 99,
		ProductID:     "someone-else",
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, found, err := store.Load()
	if err != nil || !found {
		t.Fatalf("Load() = found %v, error %v, want the saved state", found, err)
	}
	if got.SchemaVersion != SharedRecordsSchemaVersion || got.ProductID != "yoyodyne" {
		t.Fatalf("Load() = %#v, want the schema version and product Save sets", got)
	}
	if !got.CheckedAt.Equal(want.CheckedAt) || got.Fetched != want.Fetched || !got.SharedAt.Equal(sharedAt) ||
		got.Waiting != 2 || got.Problem != want.Problem || got.Copied != 3 {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}

	// Another store on the same root reads what this one wrote.
	other, err := NewSharedRecordsStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := other.Load(); err != nil || !found {
		t.Fatalf("Load() through a second store = found %v, error %v", found, err)
	}
}

func TestSharedRecordsRepositoryIsAbsoluteAndInTheProductDirectory(t *testing.T) {
	t.Parallel()

	store, err := NewSharedRecordsStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	directory := store.RepositoryDirectory()
	if !filepath.IsAbs(directory) || filepath.Base(directory) != "records.git" {
		t.Fatalf("RepositoryDirectory() = %q, want an absolute path ending in records.git", directory)
	}
}

func TestSharedRecordsStoreRefusesAnInvalidProduct(t *testing.T) {
	t.Parallel()

	if _, err := NewSharedRecordsStore(t.TempDir(), "../elsewhere"); err == nil {
		t.Fatal("NewSharedRecordsStore() accepted a product that is not an identifier")
	}
}

// One process holds the sharing at a time: a second waits for the first to
// release, and gives up when its context ends.
func TestSharedRecordsLockExcludesAnotherHolderUntilReleased(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := NewSharedRecordsStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSharedRecordsStore() error = %v", err)
	}
	second, err := NewSharedRecordsStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSharedRecordsStore() error = %v", err)
	}
	release, err := first.Lock(context.Background())
	if err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	waiting, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if unlock, err := second.Lock(waiting); err == nil {
		unlock()
		t.Fatal("a second Lock() succeeded while the first was held")
	}
	release()
	unlock, err := second.Lock(context.Background())
	if err != nil {
		t.Fatalf("Lock() after release error = %v", err)
	}
	unlock()
}
