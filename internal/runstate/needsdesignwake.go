package runstate

// Which items carrying the needs-design label the architect has already been
// woken for.
//
// Adding the label wakes her conversation with the item named
// (docs/designs/needs-design-gate.md, "Waking the architect"). The wake is read
// from the label as the tracker holds it rather than from whichever path added
// it, so a label set by a conversation's action, by an item created carrying it,
// or by a developer's raise wakes her alike. What turns that reading into a wake
// rather than one every pull is this record: an item is woken for once, after a
// pass that named it completed, and an item that loses the label is dropped
// from it, so labelling it again later wakes her again.
//
// It lives under the state root beside the other passes' bookkeeping, and never
// in the repository: it is the harness's record about one conversation, not
// anything a person edits.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// NeedsDesignWakesSchemaVersion is the shape of the stored record.
const NeedsDesignWakesSchemaVersion = 1

const (
	needsDesignWakesDirectory = "architect"
	needsDesignWakesFile      = "needs-design-wakes.json"
	needsDesignWakesLockFile  = ".needs-design-wakes.lock"
)

// NeedsDesignWakes is the items the architect has been woken for.
type NeedsDesignWakes struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Woken is each item a completed pass named while it carried the label, with
	// when that pass was taken.
	Woken     map[string]time.Time `json:"woken,omitempty"`
	UpdatedAt time.Time            `json:"updated_at"`
}

// Has reports an item the architect has been woken for.
func (w NeedsDesignWakes) Has(item string) bool {
	_, woken := w.Woken[item]
	return woken
}

// Stale is the items recorded as woken for that no longer carry the label, in
// a stable order.
func (w NeedsDesignWakes) Stale(labelled []string) []string {
	current := make(map[string]bool, len(labelled))
	for _, item := range labelled {
		current[item] = true
	}
	var stale []string
	for item := range w.Woken {
		if !current[item] {
			stale = append(stale, item)
		}
	}
	sort.Strings(stale)
	return stale
}

// Validate reports every contract violation in the record at once.
func (w NeedsDesignWakes) Validate() error {
	var problems []error
	if w.SchemaVersion != NeedsDesignWakesSchemaVersion {
		problems = append(problems, fmt.Errorf("needs-design wake record schema version %d is not supported", w.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(w.ProductID)); err != nil {
		problems = append(problems, err)
	}
	for item, at := range w.Woken {
		if strings.TrimSpace(item) == "" {
			problems = append(problems, errors.New("an item is recorded as woken for with no id"))
		}
		if at.IsZero() {
			problems = append(problems, fmt.Errorf("item %q is recorded as woken for at no time", item))
		}
	}
	if w.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid needs-design wake record: %w", err)
	}
	return nil
}

// NeedsDesignWakeStore keeps the record for one product.
type NeedsDesignWakeStore struct {
	root      string
	productID domain.ProductID
}

// NewNeedsDesignWakeStore opens the record kept under a state root.
func NewNeedsDesignWakeStore(root string, productID domain.ProductID) (*NeedsDesignWakeStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &NeedsDesignWakeStore{root: home.ProductDirectory(root, string(productID)), productID: productID}, nil
}

// NeedsDesignWakes is the record for this run store's product, reached from
// here for the reason the sweeps are.
func (s *Store) NeedsDesignWakes() *NeedsDesignWakeStore {
	return &NeedsDesignWakeStore{root: filepath.Dir(s.root), productID: s.productID}
}

// Path names the record, so a failure can say where it is.
func (s *NeedsDesignWakeStore) Path() string {
	return filepath.Join(s.root, needsDesignWakesDirectory, needsDesignWakesFile)
}

// Load reads the record. No record is the ordinary answer before the first
// wake; one that cannot be read is a failure, because reading past it as empty
// would wake the architect again for every item she was already woken for.
func (s *NeedsDesignWakeStore) Load() (NeedsDesignWakes, bool, error) {
	stored, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return NeedsDesignWakes{}, false, nil
	}
	if err != nil {
		return NeedsDesignWakes{}, false, fmt.Errorf("read the needs-design wake record: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(stored)))
	decoder.DisallowUnknownFields()
	var record NeedsDesignWakes
	if err := decoder.Decode(&record); err != nil {
		return NeedsDesignWakes{}, false, fmt.Errorf("decode the needs-design wake record at %s: %w", s.Path(), err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return NeedsDesignWakes{}, false, fmt.Errorf("decode the needs-design wake record at %s: %w", s.Path(), err)
	}
	if err := record.Validate(); err != nil {
		return NeedsDesignWakes{}, false, err
	}
	if record.ProductID != s.productID {
		return NeedsDesignWakes{}, false, fmt.Errorf("the needs-design wake record at %s belongs to product %q, not %q", s.Path(), record.ProductID, s.productID)
	}
	return record, true, nil
}

// Record adds the items a completed pass named, as woken for at the moment
// given, and drops every item no longer in labelled, the items carrying the
// label as the tracker last read. An item already recorded keeps its first
// moment.
func (s *NeedsDesignWakeStore) Record(ctx context.Context, woken, labelled []string, at time.Time) (NeedsDesignWakes, error) {
	release, err := s.lock(ctx)
	if err != nil {
		return NeedsDesignWakes{}, err
	}
	defer release()

	record, _, err := s.Load()
	if err != nil {
		return NeedsDesignWakes{}, err
	}
	if record.Woken == nil {
		record.Woken = map[string]time.Time{}
	}
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	for _, item := range woken {
		item = strings.TrimSpace(item)
		if item == "" {
			return NeedsDesignWakes{}, errors.New("an item woken for has no id")
		}
		if _, kept := record.Woken[item]; !kept {
			record.Woken[item] = at
		}
	}
	for _, item := range record.Stale(labelled) {
		delete(record.Woken, item)
	}
	if len(record.Woken) == 0 {
		record.Woken = nil
	}
	record.SchemaVersion = NeedsDesignWakesSchemaVersion
	record.ProductID = s.productID
	record.UpdatedAt = at
	if err := record.Validate(); err != nil {
		return NeedsDesignWakes{}, err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return NeedsDesignWakes{}, fmt.Errorf("encode the needs-design wake record: %w", err)
	}
	confined, err := repowrite.NewRoot(s.root)
	if err != nil {
		return NeedsDesignWakes{}, fmt.Errorf("resolve the product's state directory: %w", err)
	}
	if _, err := confined.WriteFile(filepath.Join(needsDesignWakesDirectory, needsDesignWakesFile), append(encoded, '\n')); err != nil {
		return NeedsDesignWakes{}, fmt.Errorf("record the needs-design wakes: %w", err)
	}
	return record, nil
}

// lock serializes the read-modify-write of the record across every Yoyodyne
// process.
func (s *NeedsDesignWakeStore) lock(ctx context.Context) (func(), error) {
	directory := filepath.Join(s.root, needsDesignWakesDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the needs-design wake record's directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, needsDesignWakesLockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the needs-design wake record's lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the needs-design wake record: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
