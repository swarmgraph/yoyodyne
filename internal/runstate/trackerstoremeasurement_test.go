package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

const measuredMegabyte = int64(1) << 20

func measuredStage(name string, size int64, took time.Duration, export beads.StoreExport) beads.StoreStage {
	return beads.StoreStage{
		Name: name,
		Size: size,
		Reads: []beads.StoreRead{
			{Name: beads.ReadOpenListing, Took: took, Items: 40},
			{Name: beads.ReadReadyListing, Took: took, Items: 12},
			{Name: beads.ReadOneItem, Took: took / 4, Items: 1},
		},
		Export: export,
	}
}

func fullMeasurement() TrackerStoreMeasurement {
	unchanged := beads.StoreExport{Records: 900, Compared: true}
	measurement := TrackerStoreMeasurement{Measurement: beads.StoreMeasurement{
		Store:     "/opt/calc/.beads",
		StartedAt: time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC),
		CopyTook:  4 * time.Second,
		Took:      9 * time.Minute,
		Stages: []beads.StoreStage{
			measuredStage(beads.StageAsItIs, 930*measuredMegabyte, 33*time.Second, beads.StoreExport{Records: 900}),
			measuredStage(beads.StageCleanedUp, 900*measuredMegabyte, 31*time.Second, unchanged),
			measuredStage(beads.StageOldSquashed, 200*measuredMegabyte, 6*time.Second, unchanged),
			measuredStage(beads.StageAllSquashed, 40*measuredMegabyte, 2*time.Second, unchanged),
		},
	}}
	measurement.Measurement.Stages[1].Command = []string{"gc", "--skip-decay", "--force"}
	return measurement
}

// The breakdown is what each stage took back: leftover data, old history,
// recent history, and the current data that is left.
func TestAMeasurementsBreakdownIsWhatEachStageTookBack(t *testing.T) {
	t.Parallel()
	breakdown := fullMeasurement().Breakdown()
	want := Breakdown{Total: 930 * measuredMegabyte, Leftover: 30 * measuredMegabyte, OldHistory: 700 * measuredMegabyte, RecentHistory: 160 * measuredMegabyte, Current: 40 * measuredMegabyte, Known: 4}
	if breakdown != want {
		t.Errorf("Breakdown() = %+v, want %+v", breakdown, want)
	}

	partial := fullMeasurement()
	partial.Measurement.Stages[2].Failed = "bd compact failed"
	if got := partial.Breakdown(); got.Known != 1 || got.Leftover != 30*measuredMegabyte || got.OldHistory != 0 || got.Current != 0 {
		t.Errorf("Breakdown() of a measurement whose squash failed = %+v, want only the leftover known", got)
	}
}

// What a measurement says carries the breakdown, every read's time at every
// stage, and whether each export was unchanged, saying a changed one plainly.
func TestAMeasurementSaysItsFigures(t *testing.T) {
	t.Parallel()
	said := fullMeasurement().Says()
	for _, want := range []string{
		"the store is 930 MB, 30 MB leftover data a cleanup takes back, 700 MB history older than 30 days, 160 MB history of the last 30 days, 40 MB current data",
		"as it is: 930 MB, listing open items 33.0 seconds, listing ready items 33.0 seconds, reading one item 8.2 seconds, an export of 900 records",
		"after the cleanup (bd gc --skip-decay --force, 0.0 seconds): 900 MB, listing open items 31.0 seconds",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("Says() = %q, want it to say %q", said, want)
		}
	}
	if !strings.Contains(said, "after squashing all history: 40 MB, listing open items 2.0 seconds") || !strings.Contains(said, "every one of the export's 900 records unchanged") {
		t.Errorf("Says() = %q, want the last stage's figures and the exports unchanged", said)
	}

	changed := fullMeasurement()
	changed.Measurement.Stages[2].Export = beads.StoreExport{Records: 899, Compared: true, Missing: []string{"calc-7"}, Changed: []string{"calc-2"}, Differing: 2}
	if said := changed.Says(); !strings.Contains(said, "the export's records CHANGED: 2 differ (missing calc-7; changed calc-2)") {
		t.Errorf("Says() = %q, want the changed export said plainly", said)
	}
	if changed.ExportsUnchanged() || !fullMeasurement().ExportsUnchanged() {
		t.Errorf("ExportsUnchanged() did not tell a changed export from unchanged ones")
	}
}

// Each measurement is kept in a file of its own and read back oldest first,
// with every figure it was recorded with.
func TestMeasurementsAreRecordedAndReadBack(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerStoreMeasurementStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	if read, err := store.Read(); err != nil || len(read) != 0 {
		t.Fatalf("Read() of no measurements = %v, %v, want none", read, err)
	}
	older := fullMeasurement()
	older.Measurement.StartedAt = older.Measurement.StartedAt.Add(-7 * 24 * time.Hour)
	older.Failed = "context deadline exceeded"
	for _, measurement := range []TrackerStoreMeasurement{fullMeasurement(), older} {
		if err := store.Record(measurement); err != nil {
			t.Fatal(err)
		}
	}
	read, err := store.Read()
	if err != nil || len(read) != 2 {
		t.Fatalf("Read() = %v, %v, want two measurements", read, err)
	}
	if read[0].Failed == "" || read[1].Failed != "" {
		t.Errorf("Read() = %+v, want the older measurement first", read)
	}
	latest, _ := read.Latest()
	if latest.Breakdown() != fullMeasurement().Breakdown() || latest.ProductID != "calc" || latest.Measurement.Stages[0].Reads[0].Took != 33*time.Second {
		t.Errorf("Latest() = %+v, want every figure read back", latest)
	}
	work, err := store.WorkDirectory()
	if err != nil {
		t.Fatalf("WorkDirectory() error = %v", err)
	}
	defer work.Close()
	if !strings.HasSuffix(work.Path(), "tracker-store-measurement-copy") {
		t.Errorf("WorkDirectory() = %s, want the copy's own directory under the product", work.Path())
	}
}

// A measurement with no start time is not one that started, and is refused.
func TestAMeasurementWithNoStartIsRefused(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerStoreMeasurementStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(TrackerStoreMeasurement{}); err == nil {
		t.Error("Record() of a measurement with no start = nil, want it refused")
	}
}
