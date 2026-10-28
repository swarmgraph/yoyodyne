package runstate

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// An item is recorded as woken for once, keeping the moment it was first woken
// for, and leaves the record once it no longer carries the label.
func TestTheNeedsDesignWakeRecordKeepsTheFirstWakeAndDropsUnlabelledItems(t *testing.T) {
	t.Parallel()

	store, err := NewNeedsDesignWakeStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewNeedsDesignWakeStore() error = %v", err)
	}
	if _, found, err := store.Load(); err != nil || found {
		t.Fatalf("Load() before any wake = %v, %v, want nothing recorded", found, err)
	}
	first := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	if _, err := store.Record(context.Background(), []string{"item-1"}, []string{"item-1"}, first); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	record, err := store.Record(context.Background(), []string{"item-1", "item-2"}, []string{"item-1", "item-2"}, first.Add(time.Hour))
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !record.Woken["item-1"].Equal(first) || !record.Woken["item-2"].Equal(first.Add(time.Hour)) {
		t.Fatalf("woken = %v, want item-1 kept at its first wake and item-2 added", record.Woken)
	}
	if stale := record.Stale([]string{"item-2"}); len(stale) != 1 || stale[0] != "item-1" {
		t.Fatalf("Stale() = %v, want item-1", stale)
	}
	pruned, err := store.Record(context.Background(), nil, []string{"item-2"}, first.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if pruned.Has("item-1") || !pruned.Has("item-2") {
		t.Fatalf("woken = %v, want only item-2 left", pruned.Woken)
	}
	loaded, found, err := store.Load()
	if err != nil || !found || loaded.Has("item-1") || !loaded.Has("item-2") {
		t.Fatalf("Load() = %+v, %v, %v, want the pruned record read back", loaded, found, err)
	}
}

// A record that cannot be read is a failure rather than an empty record, which
// would wake the architect again for everything.
func TestAnUnreadableNeedsDesignWakeRecordIsAFailure(t *testing.T) {
	t.Parallel()

	store, err := NewNeedsDesignWakeStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewNeedsDesignWakeStore() error = %v", err)
	}
	if _, err := store.Record(context.Background(), []string{"item-1"}, []string{"item-1"}, time.Now()); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "decode the needs-design wake record") {
		t.Fatalf("Load() error = %v, want the record refused", err)
	}
	if _, err := store.Record(context.Background(), []string{"item-2"}, []string{"item-2"}, time.Now()); err == nil {
		t.Fatal("Record() over an unreadable record succeeded, want it refused rather than overwritten")
	}
}
