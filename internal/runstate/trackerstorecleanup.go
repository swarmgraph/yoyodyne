package runstate

// Each cleanup of the tracker's store, as the maintenance pass ran it.
//
// The pass cleans up the store when it has grown a bound since the last
// cleanup left it, or a bound of time has passed since then
// (internal/maintain), so it needs the last cleanup to outlive the supervisor
// that ran it; and `yoyo status` says when the last one ran, the store's size
// either side, and how long it took. Each cleanup that started is one file of
// its own, created once and never rewritten, so nothing here needs a lock and
// two records can never be one write that lost the other. A cleanup that never
// started — not due, or another process using the store — is said on the pass
// that skipped it and is not recorded here.

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
)

// TrackerStoreCleanupSchemaVersion is 1 and has never changed.
const TrackerStoreCleanupSchemaVersion = 1

// maxTrackerStoreCleanupTextBytes bounds what a record keeps of what a stopped
// cleanup said. The whole of it is in the supervisor's log.
const maxTrackerStoreCleanupTextBytes = 1 << 10

// TrackerStoreCleanup is one cleanup of the tracker's store that started.
type TrackerStoreCleanup struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Store is the database directory cleaned up.
	Store string `json:"store"`
	// StartedAt is when the cleanup started, and Took how long it ran.
	StartedAt time.Time     `json:"started_at"`
	Took      time.Duration `json:"took"`
	// SizeBefore and SizeAfter are the store's size in bytes either side of it.
	SizeBefore int64 `json:"size_before"`
	SizeAfter  int64 `json:"size_after"`
	// Stopped is a cleanup stopped when its hold on the store ran out, before it
	// finished, and Said what its stopped call said.
	Stopped bool   `json:"stopped,omitempty"`
	Said    string `json:"said,omitempty"`
	// Reason is why it was due, in words.
	Reason string `json:"reason,omitempty"`
}

// Says is the cleanup in a sentence: when it ran, the store's size either side,
// and how long it took, and that it was stopped where it was.
func (c TrackerStoreCleanup) Says() string {
	said := fmt.Sprintf("at %s, from %s to %s in %s",
		c.StartedAt.Local().Format("2006-01-02 15:04 MST"), SayBytes(c.SizeBefore), SayBytes(c.SizeAfter), sayTook(c.Took))
	if c.Stopped {
		said += ", stopped before it finished when its hold on the store ran out"
	}
	return said
}

// SayBytes is a size as a person reads one, said as the tracker client says
// the store's size (beads.SayBytes).
func SayBytes(size int64) string { return beads.SayBytes(size) }

// sayTook is a duration to the tenth of a second under a minute, and to the
// second above.
func sayTook(took time.Duration) string {
	if took < time.Minute {
		return fmt.Sprintf("%.1f seconds", took.Seconds())
	}
	return took.Round(time.Second).String()
}

// TrackerStoreCleanups is every recorded cleanup, oldest first.
type TrackerStoreCleanups []TrackerStoreCleanup

// Latest is the most recent cleanup, whether or not it finished.
func (c TrackerStoreCleanups) Latest() (TrackerStoreCleanup, bool) {
	if len(c) == 0 {
		return TrackerStoreCleanup{}, false
	}
	return c[len(c)-1], true
}

// LastFinished is the most recent cleanup that finished, which is what the
// pass's bounds are measured from: a stopped one left the store part cleaned at
// most, so the time since it does not say the store is clean.
func (c TrackerStoreCleanups) LastFinished() (TrackerStoreCleanup, bool) {
	for index := len(c) - 1; index >= 0; index-- {
		if !c[index].Stopped {
			return c[index], true
		}
	}
	return TrackerStoreCleanup{}, false
}

// TrackerStoreCleanupStore is where the product's cleanups are recorded, a
// directory of one file each under the product.
type TrackerStoreCleanupStore struct {
	root, anchor string
	productID    domain.ProductID
}

func NewTrackerStoreCleanupStore(root string, productID domain.ProductID) (*TrackerStoreCleanupStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &TrackerStoreCleanupStore{
		root:   filepath.Join(home.ProductDirectory(root, string(productID)), "tracker-store-cleanups"),
		anchor: anchor, productID: productID,
	}, nil
}

// Path names the directory, so a failure can say where it is.
func (s *TrackerStoreCleanupStore) Path() string { return s.root }

// Record writes one cleanup that started.
func (s *TrackerStoreCleanupStore) Record(cleanup TrackerStoreCleanup) error {
	if cleanup.StartedAt.IsZero() {
		return errors.New("a tracker store cleanup's start time is required")
	}
	cleanup.SchemaVersion, cleanup.ProductID = TrackerStoreCleanupSchemaVersion, s.productID
	cleanup.StartedAt = cleanup.StartedAt.UTC()
	cleanup.Said = oneline.Fold(cleanup.Said, maxTrackerStoreCleanupTextBytes)
	cleanup.Reason = oneline.Fold(cleanup.Reason, maxTrackerStoreCleanupTextBytes)
	encoded, err := json.Marshal(cleanup)
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	name := cleanup.StartedAt.Format("20060102T150405.000000000") + "-" + rand.Text() + ".json"
	return root.CreateFile(name, append(encoded, '\n'), 0o600)
}

// Read returns every recorded cleanup, oldest first. No directory is a product
// whose store the harness has never cleaned up. A field this build does not
// know is stepped over, because what reads this is the pass deciding whether a
// cleanup is due and the status saying when one ran, and a newer build's
// record must blank neither; a record that cannot be read at all is refused by
// name, because a bound measured past it would be measured from the wrong
// cleanup.
func (s *TrackerStoreCleanupStore) Read() (TrackerStoreCleanups, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the tracker store cleanups in %s: %w", s.root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	cleanups := make(TrackerStoreCleanups, 0, len(names))
	for _, name := range names {
		encoded, err := readCleanupFile(filepath.Join(s.root, name))
		if err != nil {
			return nil, fmt.Errorf("read the tracker store cleanup %s: %w", name, err)
		}
		var cleanup TrackerStoreCleanup
		if _, err := decodeTolerating(encoded, &cleanup); err != nil {
			return nil, fmt.Errorf("decode the tracker store cleanup %s: %w", name, err)
		}
		if cleanup.SchemaVersion != TrackerStoreCleanupSchemaVersion {
			return nil, fmt.Errorf("the tracker store cleanup %s has schema version %d, which is not supported", name, cleanup.SchemaVersion)
		}
		if cleanup.ProductID != s.productID {
			return nil, fmt.Errorf("the tracker store cleanup %s belongs to product %q, not %q", name, cleanup.ProductID, s.productID)
		}
		cleanups = append(cleanups, cleanup)
	}
	return cleanups, nil
}

func readCleanupFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
}
