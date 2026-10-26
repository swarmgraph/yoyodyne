package runstate

// Each tracker call the harness made that ran until a limit stopped it.
//
// A call stopped at its limit is said wherever it failed — a pass's problem, a
// clean-up's error, a refusal in a conversation — and nowhere that collects
// them. Whether the tracker is slow because calls queue behind one another or
// because its store has grown is answered by many of them read together: how
// long each ran, how big the store was, and whether anything else had it
// open. So the tracker client hands each one here (beads.SlowCall), as one
// file of its own, created once and never rewritten, so nothing here needs a
// lock and two calls stopped at once are never one write that lost the other.
// The newest maxTrackerSlowCalls are kept, which is weeks of a tracker timing
// out several times an hour, and the oldest are removed past that.

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

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// TrackerSlowCallSchemaVersion is 1 and has never changed.
const TrackerSlowCallSchemaVersion = 1

// maxTrackerSlowCalls is how many slow calls are kept.
const maxTrackerSlowCalls = 1000

// maxTrackerSlowCallTextBytes bounds each piece of text a record keeps.
const maxTrackerSlowCallTextBytes = 1 << 10

// TrackerSlowCall is one recorded slow call.
type TrackerSlowCall struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	beads.SlowCall
}

// TrackerSlowCallStore is where the product's slow tracker calls are recorded,
// a directory of one file each under the product.
type TrackerSlowCallStore struct {
	root, anchor string
	productID    domain.ProductID
	// keep is how many records are kept, and zero for maxTrackerSlowCalls; it
	// is set only by this package's tests.
	keep int
}

func NewTrackerSlowCallStore(root string, productID domain.ProductID) (*TrackerSlowCallStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &TrackerSlowCallStore{
		root:   filepath.Join(home.ProductDirectory(root, string(productID)), "tracker-slow-calls"),
		anchor: anchor, productID: productID,
	}, nil
}

// Path names the directory, so a failure can say where it is.
func (s *TrackerSlowCallStore) Path() string { return s.root }

// RecordSlowCall writes one slow call, and removes the oldest past the number
// kept. A removal that fails leaves one more record than the bound and is not
// an error: the call is recorded.
func (s *TrackerSlowCallStore) RecordSlowCall(call beads.SlowCall) error {
	if call.StartedAt.IsZero() {
		return errors.New("a slow tracker call's start time is required")
	}
	call.StartedAt = call.StartedAt.UTC()
	call.For = oneline.Fold(call.For, maxTrackerSlowCallTextBytes)
	call.StoreUnmeasured = oneline.Fold(call.StoreUnmeasured, maxTrackerSlowCallTextBytes)
	call.HoldersUnread = oneline.Fold(call.HoldersUnread, maxTrackerSlowCallTextBytes)
	encoded, err := json.Marshal(TrackerSlowCall{SchemaVersion: TrackerSlowCallSchemaVersion, ProductID: s.productID, SlowCall: call})
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	name := call.StartedAt.Format("20060102T150405.000000000") + "-" + rand.Text() + ".json"
	if err := root.CreateFile(name, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	entries, err := root.ReadDirectory(".")
	if err != nil {
		return nil
	}
	keep := s.keep
	if keep == 0 {
		keep = maxTrackerSlowCalls
	}
	names := recordNames(entries)
	for _, old := range names[:max(0, len(names)-keep)] {
		_ = root.Remove(old)
	}
	return nil
}

// Read returns every recorded slow call, oldest first. No directory is a
// product whose tracker calls have never run out of time. A field this build
// does not know is stepped over, so a newer build's record does not blank an
// older build's reading.
func (s *TrackerSlowCallStore) Read() ([]TrackerSlowCall, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the slow tracker calls in %s: %w", s.root, err)
	}
	names := recordNames(entries)
	calls := make([]TrackerSlowCall, 0, len(names))
	for _, name := range names {
		encoded, err := readCleanupFile(filepath.Join(s.root, name))
		if err != nil {
			return nil, fmt.Errorf("read the slow tracker call %s: %w", name, err)
		}
		var call TrackerSlowCall
		if _, err := decodeTolerating(encoded, &call); err != nil {
			return nil, fmt.Errorf("decode the slow tracker call %s: %w", name, err)
		}
		if call.SchemaVersion != TrackerSlowCallSchemaVersion {
			return nil, fmt.Errorf("the slow tracker call %s has schema version %d, which is not supported", name, call.SchemaVersion)
		}
		if call.ProductID != s.productID {
			return nil, fmt.Errorf("the slow tracker call %s belongs to product %q, not %q", name, call.ProductID, s.productID)
		}
		calls = append(calls, call)
	}
	return calls, nil
}

// recordNames is the JSON records among entries, oldest first, which is the
// order their names sort in.
func recordNames(entries []fs.DirEntry) []string {
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}
