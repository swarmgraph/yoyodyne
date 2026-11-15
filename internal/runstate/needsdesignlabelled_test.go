package runstate

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// An item is recorded as labelled since the first pull that saw it, keeps that
// time while it carries the label, and leaves the record when the label comes
// off, so labelling it again starts a new wait.
func TestTheNeedsDesignLabelledRecordKeepsTheFirstSightingAndDropsUnlabelledItems(t *testing.T) {
	t.Parallel()

	store, err := NewNeedsDesignLabelledStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewNeedsDesignLabelledStore() error = %v", err)
	}
	if _, found, err := store.Load(); err != nil || found {
		t.Fatalf("Load() before any pull = %v, %v, want nothing recorded", found, err)
	}
	ctx := context.Background()
	first := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	if _, err := store.Observe(ctx, []string{"item-1"}, first); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	record, err := store.Observe(ctx, []string{"item-1", "item-2"}, first.Add(time.Hour))
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if since, _ := record.Since("item-1"); !since.Equal(first) {
		t.Fatalf("item-1 since %v, want its first sighting %v", since, first)
	}
	if since, _ := record.Since("item-2"); !since.Equal(first.Add(time.Hour)) {
		t.Fatalf("item-2 since %v, want %v", since, first.Add(time.Hour))
	}
	if _, err := store.MarkReported(ctx, "item-1", first.Add(25*time.Hour)); err != nil {
		t.Fatalf("MarkReported() error = %v", err)
	}
	pruned, err := store.Observe(ctx, []string{"item-2"}, first.Add(26*time.Hour))
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if _, kept := pruned.Since("item-1"); kept {
		t.Fatalf("item-1 is still recorded after its label came off: %+v", pruned.Items)
	}
	again, err := store.Observe(ctx, []string{"item-1", "item-2"}, first.Add(27*time.Hour))
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if since, _ := again.Since("item-1"); !since.Equal(first.Add(27*time.Hour)) || again.Reported("item-1") {
		t.Fatalf("item-1 labelled again = %+v, want a new wait that has not been reported", again.Items["item-1"])
	}
	loaded, found, err := store.Load()
	if err != nil || !found || len(loaded.Items) != 2 {
		t.Fatalf("Load() = %+v, %v, %v, want both items read back", loaded, found, err)
	}
}

// A P0 wait is marked as reported once, and the mark is never moved.
func TestANeedsDesignWaitIsMarkedReportedOnce(t *testing.T) {
	t.Parallel()

	store, err := NewNeedsDesignLabelledStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewNeedsDesignLabelledStore() error = %v", err)
	}
	ctx := context.Background()
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	if _, err := store.Observe(ctx, []string{"item-1"}, at); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if _, err := store.MarkReported(ctx, "item-1", at.Add(25*time.Hour)); err != nil {
		t.Fatalf("MarkReported() error = %v", err)
	}
	record, err := store.MarkReported(ctx, "item-1", at.Add(50*time.Hour))
	if err != nil {
		t.Fatalf("MarkReported() error = %v", err)
	}
	if wait := record.Items["item-1"]; wait.ReportedAt == nil || !wait.ReportedAt.Equal(at.Add(25*time.Hour)) {
		t.Fatalf("reported at %v, want the first report's moment kept", wait.ReportedAt)
	}
	if _, err := store.MarkReported(ctx, "item-gone", at); err != nil {
		t.Fatalf("MarkReported() of an unrecorded item error = %v", err)
	}
	if loaded, _, _ := store.Load(); loaded.Reported("item-gone") {
		t.Fatal("an item never seen labelled was recorded as reported")
	}
}

// The surfaces read past a field a newer build added; the writer refuses it
// rather than writing the record back without it.
func TestTheNeedsDesignLabelledRecordIsReadPastANewerFieldAndNotRewrittenOverIt(t *testing.T) {
	t.Parallel()

	store, err := NewNeedsDesignLabelledStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewNeedsDesignLabelledStore() error = %v", err)
	}
	ctx := context.Background()
	if _, err := store.Observe(ctx, []string{"item-1"}, time.Now()); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	stored, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	newer := strings.Replace(string(stored), `"schema_version"`, `"newer_field": true, "schema_version"`, 1)
	if err := os.WriteFile(store.Path(), []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	if loaded, found, err := store.Load(); err != nil || !found || len(loaded.Items) != 1 {
		t.Fatalf("Load() = %+v, %v, %v, want the record read past the newer field", loaded, found, err)
	}
	if _, err := store.Observe(ctx, []string{"item-2"}, time.Now()); err == nil {
		t.Fatal("Observe() over a newer record succeeded, want it refused rather than rewritten without the field")
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "decode the needs-design labelled record") {
		t.Fatalf("Load() error = %v, want an unreadable record said as one", err)
	}
}
