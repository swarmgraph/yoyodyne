package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each cleanup is its own record, read back oldest first; the latest is the
// last one that started and the last finished one skips a stopped one after it.
func TestTrackerStoreCleanupsAreRecordedAndReadInOrder(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerStoreCleanupStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	if cleanups, err := store.Read(); err != nil || len(cleanups) != 0 {
		t.Fatalf("Read() of a store never cleaned up = %+v, %v, want nothing", cleanups, err)
	}
	if _, found := (TrackerStoreCleanups{}).LastFinished(); found {
		t.Fatal("LastFinished() of nothing found one")
	}
	first := TrackerStoreCleanup{StartedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), Took: 9 * time.Second, SizeBefore: 885 << 20, SizeAfter: 526 << 20}
	second := TrackerStoreCleanup{StartedAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), Took: 15 * time.Second, SizeBefore: 600 << 20, SizeAfter: 590 << 20, Stopped: true, Said: "stopped"}
	for _, cleanup := range []TrackerStoreCleanup{second, first} {
		if err := store.Record(cleanup); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	cleanups, err := store.Read()
	if err != nil || len(cleanups) != 2 {
		t.Fatalf("Read() = %+v, %v, want both", cleanups, err)
	}
	if latest, _ := cleanups.Latest(); !latest.Stopped || !latest.StartedAt.Equal(second.StartedAt) {
		t.Errorf("Latest() = %+v, want the stopped one that started last", latest)
	}
	finished, found := cleanups.LastFinished()
	if !found || !finished.StartedAt.Equal(first.StartedAt) || finished.SizeAfter != 526<<20 || finished.ProductID != "calc" {
		t.Errorf("LastFinished() = %+v, %t, want the first, finished one", finished, found)
	}
	if said := finished.Says(); !strings.Contains(said, "from 885 MB to 526 MB in 9.0 seconds") {
		t.Errorf("Says() = %q, want the sizes and how long in words", said)
	}
	if latest, _ := cleanups.Latest(); !strings.Contains(latest.Says(), "stopped before it finished") {
		t.Errorf("Says() = %q, want the stop said", latest.Says())
	}
	if err := store.Record(TrackerStoreCleanup{}); err == nil {
		t.Error("Record() of a cleanup with no start = nil, want it refused")
	}
}

// A record from another product is refused by name rather than measured from.
func TestATrackerStoreCleanupFromAnotherProductIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	other, err := NewTrackerStoreCleanupStore(root, "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Record(TrackerStoreCleanup{StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store, err := NewTrackerStoreCleanupStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(other.Path())
	if err != nil || len(entries) != 1 {
		t.Fatalf("records = %v, %v", entries, err)
	}
	if err := os.MkdirAll(store.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(other.Path(), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Path(), entries[0].Name()), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err == nil || !strings.Contains(err.Error(), `belongs to product "other"`) {
		t.Errorf("Read() error = %v, want the other product's record refused", err)
	}
}

func TestSizesAreSaidInWords(t *testing.T) {
	t.Parallel()
	for size, want := range map[int64]string{512: "512 bytes", 184 << 10: "184 KB", 2 << 20: "2.0 MB", 885 << 20: "885 MB"} {
		if said := SayBytes(size); said != want {
			t.Errorf("SayBytes(%d) = %q, want %q", size, said, want)
		}
	}
}
