package runstate

// Each measurement of the tracker's store, made on a copy of it by the
// maintenance pass (internal/beads, storemeasure.go).
//
// A measurement is the figures somebody deciding how to keep the store small
// reads: what its size is made of, how long the reads that have been timing
// out take as it is, after a cleanup, and after squashes of its history, and
// whether a full export was the same after each. So each one that was made is
// kept, one file of its own, created once and never rewritten, as the cleanups
// are. A measurement that never started — another process using the store, or
// the store changing while it was copied — is said on the pass that skipped it
// and is not recorded. The pass reads the record back to tell when the next
// measurement is due.
//
// The copy itself is made in a directory of its own beside the records, under
// the product's state rather than the repository, and is removed when the
// measurement ends.

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// TrackerStoreMeasurementSchemaVersion is 1 and has never changed.
const TrackerStoreMeasurementSchemaVersion = 1

// TrackerStoreMeasurement is one measurement of the tracker's store that
// started.
type TrackerStoreMeasurement struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Measurement is what the tracker client measured.
	Measurement beads.StoreMeasurement `json:"measurement"`
	// Failed is why the measurement did not finish: the copy not being made, or
	// the supervisor stopping. A stage whose command failed is recorded on the
	// stage rather than here.
	Failed string `json:"failed,omitempty"`
}

// StartedAt is when the measurement started.
func (m TrackerStoreMeasurement) StartedAt() time.Time { return m.Measurement.StartedAt }

// Breakdown is the store's size divided as the measurement's stages divide it,
// in bytes. Each part is known only where the stage that reveals it was
// measured, and Known says which were.
type Breakdown struct {
	Total int64
	// Leftover is what the cleanup took back: data no history reaches.
	Leftover int64
	// OldHistory is what the squash of history older than thirty days took back,
	// and RecentHistory what squashing the rest took back.
	OldHistory    int64
	RecentHistory int64
	// Current is what is left once all history is squashed: the current data.
	Current int64
	// Known is how many of the parts above, after Total, were measured, in the
	// order they are listed.
	Known int
}

// Breakdown is the store's size divided by what each stage took back.
func (m TrackerStoreMeasurement) Breakdown() Breakdown {
	var breakdown Breakdown
	sizes := []int64{}
	for _, name := range []string{beads.StageAsItIs, beads.StageCleanedUp, beads.StageOldSquashed, beads.StageAllSquashed} {
		stage, found := m.Measurement.Stage(name)
		if !found || stage.Failed != "" || stage.Size == 0 {
			break
		}
		sizes = append(sizes, stage.Size)
	}
	if len(sizes) == 0 {
		return breakdown
	}
	breakdown.Total = sizes[0]
	parts := []*int64{&breakdown.Leftover, &breakdown.OldHistory, &breakdown.RecentHistory}
	for index := 1; index < len(sizes); index++ {
		*parts[index-1] = sizes[index-1] - sizes[index]
		breakdown.Known = index
	}
	if len(sizes) == 4 {
		breakdown.Current = sizes[3]
		breakdown.Known = 4
	}
	return breakdown
}

// Says is the measurement in a sentence: the breakdown, how long each read
// took at each stage, and whether each export was unchanged.
func (m TrackerStoreMeasurement) Says() string {
	measured := m.Measurement
	said := fmt.Sprintf("at %s", measured.StartedAt.Local().Format("2006-01-02 15:04 MST"))
	if !measured.Measured() {
		if m.Failed != "" {
			said += ", it failed before the copy was measured: " + m.Failed
		}
		return said
	}
	said += fmt.Sprintf(", copied in %s and measured in %s", sayTook(measured.CopyTook), sayTook(measured.Took))
	if parts := m.Breakdown().says(); parts != "" {
		said += "; " + parts
	}
	for _, stage := range measured.Stages {
		said += "; " + sayStage(stage)
	}
	if m.Failed != "" {
		said += "; it did not finish: " + m.Failed
	}
	return said
}

func (b Breakdown) says() string {
	if b.Total == 0 {
		return ""
	}
	said := []string{fmt.Sprintf("the store is %s", SayBytes(b.Total))}
	named := []struct {
		size int64
		what string
	}{
		{b.Leftover, "leftover data a cleanup takes back"},
		{b.OldHistory, "history older than 30 days"},
		{b.RecentHistory, "history of the last 30 days"},
		{b.Current, "current data"},
	}
	for index := 0; index < b.Known && index < len(named); index++ {
		if size := named[index].size; size < 0 {
			said = append(said, fmt.Sprintf("%s (the stage left the store %s larger)", named[index].what, SayBytes(-size)))
		} else {
			said = append(said, fmt.Sprintf("%s %s", SayBytes(size), named[index].what))
		}
	}
	return strings.Join(said, ", ")
}

func sayStage(stage beads.StoreStage) string {
	said := stage.Name
	if len(stage.Command) > 0 {
		said += fmt.Sprintf(" (bd %s, %s)", strings.Join(stage.Command, " "), sayTook(stage.Took))
	}
	if stage.Failed != "" {
		return said + ": failed: " + stage.Failed
	}
	said += fmt.Sprintf(": %s", SayBytes(stage.Size))
	for _, read := range stage.Reads {
		if read.Failed != "" {
			said += fmt.Sprintf(", %s failed after %s: %s", read.Name, sayTook(read.Took), read.Failed)
			continue
		}
		said += fmt.Sprintf(", %s %s", read.Name, sayTook(read.Took))
	}
	export := stage.Export
	switch {
	case export.Failed != "":
		said += ", the export failed: " + export.Failed
	case export.Unchanged():
		said += fmt.Sprintf(", every one of the export's %d records unchanged", export.Records)
	case export.Compared:
		said += fmt.Sprintf(", the export's records CHANGED: %d differ (%s)", export.Differing, sayDiffering(export))
	default:
		said += fmt.Sprintf(", an export of %d records", export.Records)
	}
	return said
}

func sayDiffering(export beads.StoreExport) string {
	var said []string
	if len(export.Missing) > 0 {
		said = append(said, "missing "+sayIdentifiers(export.Missing))
	}
	if len(export.Added) > 0 {
		said = append(said, "added "+sayIdentifiers(export.Added))
	}
	if len(export.Changed) > 0 {
		said = append(said, "changed "+sayIdentifiers(export.Changed))
	}
	return strings.Join(said, "; ")
}

// ExportsUnchanged reports a measurement in which every export after the first
// was taken and held exactly what the one before it held.
func (m TrackerStoreMeasurement) ExportsUnchanged() bool {
	for index, stage := range m.Measurement.Stages {
		if index == 0 || stage.Failed != "" {
			continue
		}
		if !stage.Export.Unchanged() {
			return false
		}
	}
	return true
}

// StageFailed reports a measurement one of whose stages' commands failed, so
// that the stages after it were not measured.
func (m TrackerStoreMeasurement) StageFailed() bool {
	for _, stage := range m.Measurement.Stages {
		if stage.Failed != "" {
			return true
		}
	}
	return false
}

// TrackerStoreMeasurements is every recorded measurement, oldest first.
type TrackerStoreMeasurements []TrackerStoreMeasurement

// Latest is the most recent measurement.
func (m TrackerStoreMeasurements) Latest() (TrackerStoreMeasurement, bool) {
	if len(m) == 0 {
		return TrackerStoreMeasurement{}, false
	}
	return m[len(m)-1], true
}

// TrackerStoreMeasurementStore is where the product's measurements are
// recorded, a directory of one file each under the product, and where the
// copy each is made on goes.
type TrackerStoreMeasurementStore struct {
	root, work, anchor string
	productID          domain.ProductID
}

func NewTrackerStoreMeasurementStore(root string, productID domain.ProductID) (*TrackerStoreMeasurementStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	product := home.ProductDirectory(root, string(productID))
	return &TrackerStoreMeasurementStore{
		root:   filepath.Join(product, "tracker-store-measurements"),
		work:   filepath.Join(product, "tracker-store-measurement-copy"),
		anchor: anchor, productID: productID,
	}, nil
}

// Path names the directory of records, so a failure can say where it is.
func (s *TrackerStoreMeasurementStore) Path() string { return s.root }

// WorkDirectory is the directory a measurement copies the store into, made
// where it does not exist and pinned so the copy cannot be steered outside it.
func (s *TrackerStoreMeasurementStore) WorkDirectory() (*repowrite.PinnedRoot, error) {
	return pinStateRoot(s.work, s.anchor)
}

// Record writes one measurement that started.
func (s *TrackerStoreMeasurementStore) Record(measurement TrackerStoreMeasurement) error {
	if measurement.Measurement.StartedAt.IsZero() {
		return errors.New("a tracker store measurement's start time is required")
	}
	measurement.SchemaVersion, measurement.ProductID = TrackerStoreMeasurementSchemaVersion, s.productID
	measurement.Measurement.StartedAt = measurement.Measurement.StartedAt.UTC()
	measurement.Failed = oneline.Fold(measurement.Failed, maxTrackerStoreCleanupTextBytes)
	encoded, err := json.Marshal(measurement)
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	name := measurement.Measurement.StartedAt.Format("20060102T150405.000000000") + "-" + rand.Text() + ".json"
	return root.CreateFile(name, append(encoded, '\n'), 0o600)
}

// Read returns every recorded measurement, oldest first. No directory is a
// product whose store has never been measured. A field this build does not
// know is stepped over, as a cleanup's is.
func (s *TrackerStoreMeasurementStore) Read() (TrackerStoreMeasurements, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the tracker store measurements in %s: %w", s.root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	measurements := make(TrackerStoreMeasurements, 0, len(names))
	for _, name := range names {
		encoded, err := readCleanupFile(filepath.Join(s.root, name))
		if err != nil {
			return nil, fmt.Errorf("read the tracker store measurement %s: %w", name, err)
		}
		var measurement TrackerStoreMeasurement
		if _, err := decodeTolerating(encoded, &measurement); err != nil {
			return nil, fmt.Errorf("decode the tracker store measurement %s: %w", name, err)
		}
		if measurement.SchemaVersion != TrackerStoreMeasurementSchemaVersion {
			return nil, fmt.Errorf("the tracker store measurement %s has schema version %d, which is not supported", name, measurement.SchemaVersion)
		}
		if measurement.ProductID != s.productID {
			return nil, fmt.Errorf("the tracker store measurement %s belongs to product %q, not %q", name, measurement.ProductID, s.productID)
		}
		measurements = append(measurements, measurement)
	}
	return measurements, nil
}
