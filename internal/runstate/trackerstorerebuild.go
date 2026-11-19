package runstate

// Each decision the maintenance pass made about rebuilding the live tracker
// store from its export (internal/maintain): the rebuild on a copy that comes
// first, the rebuild of the live store that follows it where that passed, and
// the old store kept beside the records.
//
// A rebuild is recorded twice under one name: once when the old store's place
// is recorded and before it is moved there, which is the kept copy's place
// recorded before anything is replaced, and again, replacing that, once the
// rebuild has ended. A record that was never replaced is a rebuild whose end
// nobody saw. A rebuild that was due and did not go ahead — a process outside
// the harness having the store open, the store lock not had in time, the store
// changing while it was copied, or a directory it needed not made — is
// recorded too, with what stopped it, and the next pass tries again.
//
// The kept stores are in a directory of their own beside the records, under
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

// TrackerStoreRebuildSchemaVersion is 1 and has never changed.
const TrackerStoreRebuildSchemaVersion = 1

// rebuildNameLayout is how a rebuild's name, and its kept store's, begins: when
// it was made, so a kept store's age can be read off its name.
const rebuildNameLayout = "20060102T150405.000000000Z"

// TrackerStoreRebuild is one decision about rebuilding the live store.
type TrackerStoreRebuild struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Name is the record's own, and its kept store's where it kept one.
	Name string `json:"name"`
	// DecidedAt is when the rebuild was decided, and Reason why it was due.
	DecidedAt time.Time `json:"decided_at"`
	Reason    string    `json:"reason,omitempty"`
	// Rehearsal is the rebuild of a copy of the store, which comes first, and
	// Rebuild the rebuild of the live store that follows where it passed.
	Rehearsal *beads.StoreRebuild `json:"rehearsal,omitempty"`
	Rebuild   *beads.StoreRebuild `json:"rebuild,omitempty"`
	// TooLong is the rehearsal's time where it was longer than Bound, the
	// longest a rebuild may hold the store, so the live store was not rebuilt.
	TooLong time.Duration `json:"too_long,omitempty"`
	Bound   time.Duration `json:"bound,omitempty"`
	// Ended says the rebuild's end is recorded here; a record without it was
	// written once the old store's place was recorded, and before it was moved.
	Ended bool `json:"ended,omitempty"`
	// HeldBack says, where the rebuild was due and did not go ahead, what
	// stopped it, and HeldBackBy which kind of thing that was, which a warning
	// about several such passes in a row is not repeated for until it changes.
	HeldBack   string `json:"held_back,omitempty"`
	HeldBackBy string `json:"held_back_by,omitempty"`
	// Reported is the warning filed about a rebuild that was not made, or about
	// several passes in a row that did not go ahead, or empty where none was
	// filed.
	Reported string `json:"reported,omitempty"`
	// StillOver is the figures of a rebuilt store still past its limit, which its
	// warning carries and a later rebuild's are compared with so the warning is
	// not repeated until they change. StillOverReported is that warning, or the
	// earlier one that carried the same figures, empty where none was filed yet.
	StillOver         string `json:"still_over,omitempty"`
	StillOverReported string `json:"still_over_reported,omitempty"`
}

// Refused reports a decision not to rebuild the live store, because the
// rehearsal was too long, failed, or found a record different, or because the
// live rebuild itself did not pass. A rebuild held back is not refused: what
// stopped it is outside the rebuild, so the next pass tries again.
func (r TrackerStoreRebuild) Refused() bool {
	if r.HeldBack != "" {
		return false
	}
	if r.TooLong > 0 {
		return true
	}
	if r.Rehearsal != nil && !r.Rehearsal.Passed() {
		return true
	}
	return r.Rebuild != nil && !r.Rebuild.Swapped && r.Ended
}

// Replaced reports a live store that was rebuilt.
func (r TrackerStoreRebuild) Replaced() bool { return r.Rebuild != nil && r.Rebuild.Swapped }

// StillOverUnwarned reports a rebuild that finished with the store still past
// its limit and no warning filed.
func (r TrackerStoreRebuild) StillOverUnwarned() bool {
	return r.Replaced() && r.StillOver != "" && r.StillOverReported == ""
}

// Says is the decision in a sentence.
func (r TrackerStoreRebuild) Says() string {
	said := "the store was due a rebuild from its export"
	if r.Reason != "" {
		said += " because " + r.Reason
	}
	if rehearsal := r.Rehearsal; rehearsal != nil {
		said += fmt.Sprintf("; on a copy the rebuild took %s and made a store of %s from %s", sayTook(rehearsal.Took), SayBytes(rehearsal.SizeAfter), SayBytes(rehearsal.SizeBefore))
		switch {
		case r.TooLong > 0:
			said += fmt.Sprintf(", longer than the %s a rebuild may take, so the live store was not rebuilt", sayTook(r.Bound))
		case rehearsal.Failed != "":
			said += fmt.Sprintf(", but the step %q failed (%s), so the live store was not rebuilt", rehearsal.Step, rehearsal.Failed)
		case rehearsal.Differing > 0:
			said += fmt.Sprintf(", but %d record(s) differed, the first %s, so the live store was not rebuilt", rehearsal.Differing, rehearsal.FirstDifference())
		}
	}
	if live := r.Rebuild; live != nil {
		switch {
		case live.Swapped:
			said += fmt.Sprintf("; the live store was rebuilt, every other tracker call waited behind it for %s, and it went from %s to %s; the old store is kept at %s", sayTook(live.HeldFor), SayBytes(live.SizeBefore), SayBytes(live.SizeAfter), live.Kept)
		case live.Failed != "":
			said += fmt.Sprintf("; the live rebuild failed at the step %q (%s)", live.Step, live.Failed)
			switch {
			case live.Restored:
				said += ", and the old store was put back"
			case live.Kept != "":
				said += fmt.Sprintf(", and the old store is at %s and the live store may be missing", live.Kept)
			}
		case live.Differing > 0:
			said += fmt.Sprintf("; on the live store %d record(s) differed, the first %s, so the old store was left in place", live.Differing, live.FirstDifference())
		case !r.Ended:
			said += "; the old store's place was recorded and its end was not"
		}
	}
	if r.HeldBack != "" {
		said += "; it did not go ahead, because " + r.HeldBack
	}
	if r.StillOver != "" {
		said += "; the store is STILL PAST its limit: " + r.StillOver
	}
	if r.Reported != "" {
		said += "; the development manager was warned in " + r.Reported
	}
	return said
}

// TrackerStoreRebuilds is every recorded decision, oldest first.
type TrackerStoreRebuilds []TrackerStoreRebuild

// Latest is the latest decision, where there is one.
func (r TrackerStoreRebuilds) Latest() (TrackerStoreRebuild, bool) {
	if len(r) == 0 {
		return TrackerStoreRebuild{}, false
	}
	return r[len(r)-1], true
}

// HeldBackInARow is the decisions at the end of the record that were held
// back, oldest first: the passes in a row since a rebuild last went ahead or
// was refused.
func (r TrackerStoreRebuilds) HeldBackInARow() TrackerStoreRebuilds {
	start := len(r)
	for start > 0 && r[start-1].HeldBack != "" {
		start--
	}
	return r[start:]
}

// LastReplaced is the latest rebuild that replaced the live store, where one
// did.
func (r TrackerStoreRebuilds) LastReplaced() (TrackerStoreRebuild, bool) {
	for index := len(r) - 1; index >= 0; index-- {
		if r[index].Replaced() {
			return r[index], true
		}
	}
	return TrackerStoreRebuild{}, false
}

// TrackerStoreRebuildStore is where the product's rebuild decisions are
// recorded, a directory of one file each under the product, and where the
// stores they replaced are kept.
type TrackerStoreRebuildStore struct {
	root, kept, anchor string
	productID          domain.ProductID
}

func NewTrackerStoreRebuildStore(root string, productID domain.ProductID) (*TrackerStoreRebuildStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	product := home.ProductDirectory(root, string(productID))
	return &TrackerStoreRebuildStore{
		root:   filepath.Join(product, "tracker-store-rebuilds"),
		kept:   filepath.Join(product, "tracker-store-rebuild-kept"),
		anchor: anchor, productID: productID,
	}, nil
}

// Path names the directory of records, so a failure can say where it is.
func (s *TrackerStoreRebuildStore) Path() string { return s.root }

// NewName is a name for a rebuild decided at now, and for its kept store.
func (s *TrackerStoreRebuildStore) NewName(now time.Time) string {
	return now.UTC().Format(rebuildNameLayout) + "-" + rand.Text()
}

// KeptDirectory is the directory the replaced stores are kept in, made where it
// does not exist and pinned so a move cannot be steered outside it.
func (s *TrackerStoreRebuildStore) KeptDirectory() (*repowrite.PinnedRoot, error) {
	return pinStateRoot(s.kept, s.anchor)
}

// WorkDirectory is where a new store is built, beside the kept ones so a move
// from it to the live store is a rename on the same volume.
func (s *TrackerStoreRebuildStore) WorkDirectory() (*repowrite.PinnedRoot, error) {
	return pinStateRoot(filepath.Join(filepath.Dir(s.kept), "tracker-store-rebuild-work"), s.anchor)
}

// Record writes one decision, replacing an earlier record under its name.
func (s *TrackerStoreRebuildStore) Record(rebuild TrackerStoreRebuild) error {
	if rebuild.Name == "" || strings.ContainsAny(rebuild.Name, `/\`) || strings.HasPrefix(rebuild.Name, ".") {
		return fmt.Errorf("a tracker store rebuild's name %q is not a file name", rebuild.Name)
	}
	if rebuild.DecidedAt.IsZero() {
		return errors.New("a tracker store rebuild's decision time is required")
	}
	rebuild.SchemaVersion, rebuild.ProductID = TrackerStoreRebuildSchemaVersion, s.productID
	rebuild.DecidedAt = rebuild.DecidedAt.UTC()
	rebuild.Reason = oneline.Fold(rebuild.Reason, maxTrackerStoreCleanupTextBytes)
	rebuild.HeldBack = oneline.Fold(rebuild.HeldBack, maxTrackerStoreCleanupTextBytes)
	encoded, err := json.Marshal(rebuild)
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.WriteFile(rebuild.Name+".json", append(encoded, '\n'), 0o600, false)
}

// Read returns every recorded decision, oldest first. No directory is a
// product whose store has never been rebuilt.
func (s *TrackerStoreRebuildStore) Read() (TrackerStoreRebuilds, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the tracker store rebuilds in %s: %w", s.root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	rebuilds := make(TrackerStoreRebuilds, 0, len(names))
	for _, name := range names {
		encoded, err := readCleanupFile(filepath.Join(s.root, name))
		if err != nil {
			return nil, fmt.Errorf("read the tracker store rebuild %s: %w", name, err)
		}
		var rebuild TrackerStoreRebuild
		if _, err := decodeTolerating(encoded, &rebuild); err != nil {
			return nil, fmt.Errorf("decode the tracker store rebuild %s: %w", name, err)
		}
		if rebuild.SchemaVersion != TrackerStoreRebuildSchemaVersion {
			return nil, fmt.Errorf("the tracker store rebuild %s has schema version %d, which is not supported", name, rebuild.SchemaVersion)
		}
		if rebuild.ProductID != s.productID {
			return nil, fmt.Errorf("the tracker store rebuild %s belongs to product %q, not %q", name, rebuild.ProductID, s.productID)
		}
		rebuilds = append(rebuilds, rebuild)
	}
	return rebuilds, nil
}

// RemoveKeptMadeBefore removes every kept store made before cutoff, as its name
// says, and returns the paths it removed. An entry whose name does not begin
// with when it was made is not one of these and is left alone.
func (s *TrackerStoreRebuildStore) RemoveKeptMadeBefore(cutoff time.Time) ([]string, error) {
	entries, err := os.ReadDir(s.kept)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the kept tracker stores in %s: %w", s.kept, err)
	}
	var stale []string
	for _, entry := range entries {
		made, _, found := strings.Cut(entry.Name(), "-")
		if !found {
			continue
		}
		at, err := time.Parse(rebuildNameLayout, made)
		if err != nil || !at.Before(cutoff) {
			continue
		}
		stale = append(stale, entry.Name())
	}
	if len(stale) == 0 {
		return nil, nil
	}
	root, err := pinStateRoot(s.kept, s.anchor)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var removed []string
	var problems []error
	for _, name := range stale {
		if err := root.RemoveAll(name); err != nil {
			problems = append(problems, fmt.Errorf("remove %s: %w", filepath.Join(s.kept, name), err))
			continue
		}
		removed = append(removed, filepath.Join(s.kept, name))
	}
	return removed, errors.Join(problems...)
}
