package runstate

// Each decision the maintenance pass made about squashing the live tracker
// store's history, on one measurement (internal/maintain): the squash it ran,
// with the copy of the store made before it, or the squash it refused because
// the measurement showed an item or a note changed.
//
// A squash is recorded twice under one name: once when its copy is made and
// before bd runs, which is the copy's place recorded before the squash the
// design asks for, and again, replacing that, once bd has ended. A record that
// was never replaced is a squash whose end nobody saw. A squash that never
// started — another process using the store, or the store changing while it
// was copied — is said on the pass and not recorded, and the next pass tries
// again.
//
// The copies are kept in a directory of their own beside the records, under
// the product's state, each named by when it was made, and removed once they
// are older than the pass keeps them.

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

// TrackerStoreSquashSchemaVersion is 1 and has never changed.
const TrackerStoreSquashSchemaVersion = 1

// squashNameLayout is how a squash's name, and its copy's, begins: when it was
// made, so a copy's age can be read off its name.
const squashNameLayout = "20060102T150405.000000000Z"

// ChangedRecords is what one stage of a measurement found changed in the
// export: the records missing, added, and changed, each by id.
type ChangedRecords struct {
	Stage   string   `json:"stage"`
	Command []string `json:"command,omitempty"`
	Missing []string `json:"missing,omitempty"`
	Added   []string `json:"added,omitempty"`
	Changed []string `json:"changed,omitempty"`
	// Differing is how many differed in all, however many are named.
	Differing int `json:"differing"`
}

// Says is the stage and what it changed, in a sentence.
func (c ChangedRecords) Says() string {
	said := c.Stage
	if len(c.Command) > 0 {
		said += " (bd " + strings.Join(c.Command, " ") + ")"
	}
	var parts []string
	if len(c.Missing) > 0 {
		parts = append(parts, "missing "+sayIdentifiers(c.Missing))
	}
	if len(c.Added) > 0 {
		parts = append(parts, "added "+sayIdentifiers(c.Added))
	}
	if len(c.Changed) > 0 {
		parts = append(parts, "changed "+sayIdentifiers(c.Changed))
	}
	return fmt.Sprintf("%s: %d record(s) differ (%s)", said, c.Differing, strings.Join(parts, "; "))
}

// ChangedRecords is every stage of the measurement whose export was compared
// with the one before it and differed.
func (m TrackerStoreMeasurement) ChangedRecords() []ChangedRecords {
	var changed []ChangedRecords
	for _, stage := range m.Measurement.Stages {
		export := stage.Export
		if !export.Compared || export.Failed != "" || export.Differing == 0 {
			continue
		}
		changed = append(changed, ChangedRecords{
			Stage: stage.Name, Command: stage.Command,
			Missing: export.Missing, Added: export.Added, Changed: export.Changed,
			Differing: export.Differing,
		})
	}
	return changed
}

// TrackerStoreSquash is one decision about squashing the live store.
type TrackerStoreSquash struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Name is the record's own, and its copy's where it made one.
	Name string `json:"name"`
	// MeasuredAt is when the measurement it was decided on began, and Reason
	// why that measurement chose this squash.
	MeasuredAt time.Time `json:"measured_at"`
	Reason     string    `json:"reason,omitempty"`
	// Changed is what the measurement found changed, on a squash refused for
	// it, and Reported the warning filed about it, empty where none could be.
	Changed  []ChangedRecords `json:"changed,omitempty"`
	Reported string           `json:"reported,omitempty"`
	// Squash is the squash run, with the copy made before it.
	Squash beads.StoreSquash `json:"squash"`
	// Ended says the squash's end is recorded here; a record without it was
	// written once the copy was made and before bd ran.
	Ended bool `json:"ended,omitempty"`
	// Failed is why the squash did not finish.
	Failed string `json:"failed,omitempty"`
}

// Refused reports a decision not to squash because the measurement showed an
// item or a note changed.
func (s TrackerStoreSquash) Refused() bool { return len(s.Changed) > 0 }

// Says is the decision in a sentence.
func (s TrackerStoreSquash) Says() string {
	measured := s.MeasuredAt.Local().Format("2006-01-02 15:04 MST")
	if s.Refused() {
		said := fmt.Sprintf("the store was not squashed on the measurement at %s, because it showed records changed: ", measured)
		var stages []string
		for _, changed := range s.Changed {
			stages = append(stages, changed.Says())
		}
		said += strings.Join(stages, "; ")
		if s.Reported != "" {
			said += "; the development manager was warned in " + s.Reported
		}
		return said
	}
	squash := s.Squash
	said := fmt.Sprintf("%s, decided on the measurement at %s", SquashedHistory(squash.Stage), measured)
	if s.Reason != "" {
		said += ", because " + s.Reason
	}
	said += fmt.Sprintf("; the store was copied first to %s in %s", squash.Copy, sayTook(squash.CopyTook))
	switch {
	case !s.Ended:
		said += fmt.Sprintf("; bd %s was started at %s and its end was not recorded", strings.Join(squash.Command, " "), squash.StartedAt.Local().Format("2006-01-02 15:04 MST"))
	case s.Failed != "":
		said += fmt.Sprintf("; bd %s held the store %s and failed: %s", strings.Join(squash.Command, " "), sayTook(squash.Took), s.Failed)
	default:
		said += fmt.Sprintf("; bd %s held the store %s and took it from %s to %s", strings.Join(squash.Command, " "), sayTook(squash.Took), SayBytes(squash.SizeBefore), SayBytes(squash.SizeAfter))
	}
	return said
}

// SquashedHistory is the history a squash of stage takes away, as a sentence
// names it.
func SquashedHistory(stage string) string {
	switch stage {
	case beads.StageOldSquashed:
		return "history older than 30 days"
	case beads.StageAllSquashed:
		return "all history"
	}
	return stage
}

// TrackerStoreSquashes is every recorded decision, oldest first.
type TrackerStoreSquashes []TrackerStoreSquash

// OnMeasurement is the decision made on the measurement that began at, where
// one was.
func (s TrackerStoreSquashes) OnMeasurement(at time.Time) (TrackerStoreSquash, bool) {
	for index := len(s) - 1; index >= 0; index-- {
		if s[index].MeasuredAt.Equal(at) {
			return s[index], true
		}
	}
	return TrackerStoreSquash{}, false
}

// TrackerStoreSquashStore is where the product's squash decisions are
// recorded, a directory of one file each under the product, and where the
// copies made before each squash are kept.
type TrackerStoreSquashStore struct {
	root, copies, anchor string
	productID            domain.ProductID
}

func NewTrackerStoreSquashStore(root string, productID domain.ProductID) (*TrackerStoreSquashStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	product := home.ProductDirectory(root, string(productID))
	return &TrackerStoreSquashStore{
		root:   filepath.Join(product, "tracker-store-squashes"),
		copies: filepath.Join(product, "tracker-store-squash-copies"),
		anchor: anchor, productID: productID,
	}, nil
}

// Path names the directory of records, so a failure can say where it is.
func (s *TrackerStoreSquashStore) Path() string { return s.root }

// NewName is a name for a squash decided at now, and for its copy.
func (s *TrackerStoreSquashStore) NewName(now time.Time) string {
	return now.UTC().Format(squashNameLayout) + "-" + rand.Text()
}

// CopyDirectory is the directory the copies are made in, made where it does
// not exist and pinned so a copy cannot be steered outside it.
func (s *TrackerStoreSquashStore) CopyDirectory() (*repowrite.PinnedRoot, error) {
	return pinStateRoot(s.copies, s.anchor)
}

// Record writes one decision, replacing an earlier record under its name.
func (s *TrackerStoreSquashStore) Record(squash TrackerStoreSquash) error {
	if squash.Name == "" || strings.ContainsAny(squash.Name, `/\`) || strings.HasPrefix(squash.Name, ".") {
		return fmt.Errorf("a tracker store squash's name %q is not a file name", squash.Name)
	}
	if squash.MeasuredAt.IsZero() {
		return errors.New("a tracker store squash's measurement time is required")
	}
	squash.SchemaVersion, squash.ProductID = TrackerStoreSquashSchemaVersion, s.productID
	squash.MeasuredAt = squash.MeasuredAt.UTC()
	squash.Reason = oneline.Fold(squash.Reason, maxTrackerStoreCleanupTextBytes)
	squash.Failed = oneline.Fold(squash.Failed, maxTrackerStoreCleanupTextBytes)
	encoded, err := json.Marshal(squash)
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.WriteFile(squash.Name+".json", append(encoded, '\n'), 0o600, false)
}

// Read returns every recorded decision, oldest first. No directory is a
// product whose store has never been squashed.
func (s *TrackerStoreSquashStore) Read() (TrackerStoreSquashes, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the tracker store squashes in %s: %w", s.root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	squashes := make(TrackerStoreSquashes, 0, len(names))
	for _, name := range names {
		encoded, err := readCleanupFile(filepath.Join(s.root, name))
		if err != nil {
			return nil, fmt.Errorf("read the tracker store squash %s: %w", name, err)
		}
		var squash TrackerStoreSquash
		if _, err := decodeTolerating(encoded, &squash); err != nil {
			return nil, fmt.Errorf("decode the tracker store squash %s: %w", name, err)
		}
		if squash.SchemaVersion != TrackerStoreSquashSchemaVersion {
			return nil, fmt.Errorf("the tracker store squash %s has schema version %d, which is not supported", name, squash.SchemaVersion)
		}
		if squash.ProductID != s.productID {
			return nil, fmt.Errorf("the tracker store squash %s belongs to product %q, not %q", name, squash.ProductID, s.productID)
		}
		squashes = append(squashes, squash)
	}
	return squashes, nil
}

// RemoveCopiesMadeBefore removes every copy made before cutoff, as its name
// says, and returns the paths it removed. An entry whose name does not begin
// with when it was made is not one of these copies and is left alone.
func (s *TrackerStoreSquashStore) RemoveCopiesMadeBefore(cutoff time.Time) ([]string, error) {
	entries, err := os.ReadDir(s.copies)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the copies of the tracker store in %s: %w", s.copies, err)
	}
	var stale []string
	for _, entry := range entries {
		made, _, found := strings.Cut(entry.Name(), "-")
		if !found {
			continue
		}
		at, err := time.Parse(squashNameLayout, made)
		if err != nil || !at.Before(cutoff) {
			continue
		}
		stale = append(stale, entry.Name())
	}
	if len(stale) == 0 {
		return nil, nil
	}
	root, err := pinStateRoot(s.copies, s.anchor)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var removed []string
	var problems []error
	for _, name := range stale {
		if err := root.RemoveAll(name); err != nil {
			problems = append(problems, fmt.Errorf("remove %s: %w", filepath.Join(s.copies, name), err))
			continue
		}
		removed = append(removed, filepath.Join(s.copies, name))
	}
	return removed, errors.Join(problems...)
}
