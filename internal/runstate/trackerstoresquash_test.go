package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// A squash is recorded once its copy is made and again once bd has ended,
// under one name, so the record read back is the one that ended; it is found
// by the measurement it was decided on.
func TestASquashRecordedTwiceIsReadAsTheOneThatEnded(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerStoreSquashStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	measuredAt := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	record := TrackerStoreSquash{Name: store.NewName(measuredAt), MeasuredAt: measuredAt, Reason: "the listings came within the limit only after it",
		Squash: beads.StoreSquash{Stage: beads.StageOldSquashed, Copy: "/state/copy", CopyTook: 3 * time.Second}}
	if err := store.Record(record); err != nil {
		t.Fatal(err)
	}
	if read, err := store.Read(); err != nil || len(read) != 1 || read[0].Ended || !strings.Contains(read[0].Says(), "its end was not recorded") {
		t.Fatalf("Read() = %+v, %v, want the squash recorded as started", read, err)
	}
	record.Ended = true
	record.Squash.Command, record.Squash.Took = []string{"compact", "--days", "30", "--force", "--json"}, 42*time.Second
	record.Squash.SizeBefore, record.Squash.SizeAfter = 930<<20, 200<<20
	if err := store.Record(record); err != nil {
		t.Fatal(err)
	}
	read, err := store.Read()
	if err != nil || len(read) != 1 || !read[0].Ended {
		t.Fatalf("Read() = %+v, %v, want the one record, ended", read, err)
	}
	if found, ok := read.OnMeasurement(measuredAt); !ok || found.Name != record.Name {
		t.Errorf("OnMeasurement() = %+v, %t, want the squash decided on it", found, ok)
	}
	if _, ok := read.OnMeasurement(measuredAt.Add(time.Hour)); ok {
		t.Errorf("OnMeasurement() found a squash on a measurement nothing was decided on")
	}
	for _, want := range []string{"history older than 30 days, decided on the measurement at", "copied first to /state/copy", "held the store 42.0 seconds and took it from 930 MB to 200 MB"} {
		if !strings.Contains(read[0].Says(), want) {
			t.Errorf("Says() = %q, want %q", read[0].Says(), want)
		}
	}
}

// A name that is not a file name is refused.
func TestASquashNamedOutsideItsDirectoryIsRefused(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerStoreSquashStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "../escape", ".hidden"} {
		if err := store.Record(TrackerStoreSquash{Name: name, MeasuredAt: time.Now()}); err == nil {
			t.Errorf("Record(%q) succeeded, want it refused", name)
		}
	}
}
