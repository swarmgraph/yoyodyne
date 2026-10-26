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

// A cleanup's work items are kept either side and read back, and a count that
// changed is said with what went missing; a failure is said with it.
func TestATrackerStoreCleanupKeepsItsItemCounts(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerStoreCleanupStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	changed := TrackerStoreCleanup{StartedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), SizeBefore: 885 << 20, SizeAfter: 526 << 20,
		ItemsBefore: 812, ItemsAfter: 805, ItemsCounted: true, Missing: []string{"calc-1", "calc-2", "calc-3", "calc-4", "calc-5", "calc-6", "calc-7"}}
	if err := store.Record(changed); err != nil {
		t.Fatal(err)
	}
	cleanups, err := store.Read()
	if err != nil || len(cleanups) != 1 {
		t.Fatalf("Read() = %+v, %v", cleanups, err)
	}
	read := cleanups[0]
	if !read.ItemsChanged() || !read.Lost() || read.ItemsBefore != 812 || read.ItemsAfter != 805 || len(read.Missing) != 7 {
		t.Errorf("Read() = %+v, want the counts and the missing items", read)
	}
	if said := read.Says(); !strings.Contains(said, "812 work items before and 805 after, calc-1, calc-2, calc-3, calc-4, calc-5 and 2 more missing") {
		t.Errorf("Says() = %q, want the changed count said with what went missing", said)
	}
	unchanged := TrackerStoreCleanup{ItemsBefore: 812, ItemsAfter: 812, ItemsCounted: true}
	if unchanged.ItemsChanged() || unchanged.SaysItems() != "812 work items before and after" {
		t.Errorf("SaysItems() = %q, changed %t, want the count unchanged", unchanged.SaysItems(), unchanged.ItemsChanged())
	}
	added := TrackerStoreCleanup{ItemsBefore: 812, ItemsAfter: 813, ItemsCounted: true, Added: []string{"calc-813"}}
	if !added.ItemsChanged() || added.Lost() || added.SaysItems() != "812 work items before and 813 after, calc-813 added meanwhile" {
		t.Errorf("SaysItems() = %q, changed %t, lost %t, want a count that rose said as changed and not lost", added.SaysItems(), added.ItemsChanged(), added.Lost())
	}
	// A record written before items were counted says nothing of them.
	if old := (TrackerStoreCleanup{SizeBefore: 1}); old.ItemsChanged() || old.SaysItems() != "" {
		t.Errorf("an uncounted cleanup = %q, changed %t, want nothing said", old.SaysItems(), old.ItemsChanged())
	}
	if said := (TrackerStoreCleanup{Failed: "disk full"}).Says(); !strings.HasSuffix(said, ", and it failed: disk full") {
		t.Errorf("Says() = %q, want the failure said", said)
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
