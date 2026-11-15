package runstate

import (
	"errors"
	"testing"
	"time"
)

// The clean-up's record is written once, read back whole, and a second record
// is refused, which is what keeps the scheduler from running it again.
func TestAssigneeCleanupIsRecordedOnce(t *testing.T) {
	t.Parallel()

	store, err := NewAssigneeCleanupStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if record, err := store.Load(); err != nil || record != nil {
		t.Fatalf("Load() before any record = %+v, %v; want nothing", record, err)
	}
	record := AssigneeCleanupRecord{
		SchemaVersion: AssigneeCleanupSchemaVersion,
		ProductID:     "yoyodyne",
		RanAt:         time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC),
		Cleared:       []LeftoverAssignee{{WorkItemID: "yoyodyne-1", Title: "t", Status: "open", Assignee: "Mason Bryant", Because: "a run's claim set it"}},
		ByPerson:      []LeftoverAssignee{{WorkItemID: "yoyodyne-2", Assignee: "Ada", Because: "no run has ever claimed it"}},
	}
	if err := store.Record(record); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	loaded, err := store.Load()
	if err != nil || loaded == nil {
		t.Fatalf("Load() = %+v, %v", loaded, err)
	}
	if len(loaded.Cleared) != 1 || loaded.Cleared[0].WorkItemID != "yoyodyne-1" || len(loaded.ByPerson) != 1 || !loaded.RanAt.Equal(record.RanAt) {
		t.Fatalf("Load() = %+v, want the record written", loaded)
	}
	if err := store.Record(record); !errors.Is(err, ErrAssigneeCleanupRecorded) {
		t.Fatalf("a second Record() error = %v, want it refused as already recorded", err)
	}
	other := record
	other.ProductID = "another"
	if err := store.Record(other); err == nil {
		t.Fatal("Record() for another product error = nil")
	}
}
