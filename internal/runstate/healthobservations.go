package runstate

// Where a health observation is kept while it can still be followed.
//
// A health observation is rendered within a bound, and what the bound left out
// is read afterwards, part by part, through health.follow naming the
// observation. The part that asks may be a later turn in another process than
// the pass that took the observation, so the observation is kept here rather
// than in memory. It is kept only as long as it can be followed — fifteen
// minutes, after which a caller observes again — and a little longer, so a
// refusal can say the observation was too old rather than that it never
// existed. Nothing here is a history of anomalies: when a condition was first
// seen and how often it has recurred is read off the pass records
// (Sweep.Health), which is the design's rule.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

const healthObservationsDirectory = "health-observations"

// HealthObservationKept is how long a kept observation stays on disk. It is
// longer than the fifteen minutes an observation can be followed for, so a
// follow just past that limit is refused as too old rather than as unknown.
const HealthObservationKept = time.Hour

// HealthObservationSchemaVersion is 1 and has never changed.
const HealthObservationSchemaVersion = 1

// HealthObservationRecord is one kept observation. The observation itself is
// carried as the health package encoded it, so this store needs none of its
// types.
type HealthObservationRecord struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	ID            string           `json:"id"`
	ObservedAt    time.Time        `json:"observed_at"`
	Observation   json.RawMessage  `json:"observation"`
}

// ErrHealthObservationUnknown is a follow naming an observation this product
// does not keep.
var ErrHealthObservationUnknown = errors.New("no such health observation is kept")

// HealthObservationStore keeps observations under the product's state.
type HealthObservationStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

func NewHealthObservationStore(root string, productID domain.ProductID) (*HealthObservationStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the health observations' state root: %w", err)
	}
	return &HealthObservationStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

// ValidHealthObservationID is an identifier the store could have issued: a
// plain name, so a follow can never name a path.
func ValidHealthObservationID(id string) bool {
	return strings.HasPrefix(id, "health-") && len(id) <= 96 && !strings.ContainsAny(id, "/\\. \t\r\n")
}

func (s *HealthObservationStore) directory() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), healthObservationsDirectory)
}

// Save keeps one observation and removes those kept longer than
// HealthObservationKept.
func (s *HealthObservationStore) Save(id string, observedAt time.Time, observation []byte) error {
	if !ValidHealthObservationID(id) {
		return fmt.Errorf("health observation id %q is not one this store issues", id)
	}
	encoded, err := encodeRecord("health observation", HealthObservationRecord{
		SchemaVersion: HealthObservationSchemaVersion, ProductID: s.productID, ID: id, ObservedAt: observedAt.UTC(), Observation: observation,
	})
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStateBytes {
		return fmt.Errorf("encoded health observation is %d bytes, limit is %d", len(encoded), maxEncodedStateBytes)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the health observations' state root: %w", err)
	}
	defer root.Close()
	directory := s.directory()
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("create the health observations' directory: %w", err)
	}
	if err := root.WriteFile(filepath.Join(directory, id+".json"), encoded, 0o600, false); err != nil {
		return fmt.Errorf("keep health observation %s: %w", id, err)
	}
	s.prune(root, directory, observedAt)
	return root.Unchanged()
}

// prune removes what is past keeping. A file it cannot read or remove is left
// for the next save: keeping too much is a cost, and never wrong.
func (s *HealthObservationStore) prune(root *repowrite.PinnedRoot, directory string, now time.Time) {
	entries, err := root.ReadDirectory(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= HealthObservationKept {
			continue
		}
		_ = root.Remove(filepath.Join(directory, name))
	}
}

// Load reads one kept observation.
func (s *HealthObservationStore) Load(id string) (HealthObservationRecord, error) {
	if !ValidHealthObservationID(id) {
		return HealthObservationRecord{}, fmt.Errorf("%w: %q is not an observation identifier", ErrHealthObservationUnknown, id)
	}
	file, err := os.Open(filepath.Join(s.stateRoot, s.directory(), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return HealthObservationRecord{}, fmt.Errorf("%w: %s", ErrHealthObservationUnknown, id)
	}
	if err != nil {
		return HealthObservationRecord{}, fmt.Errorf("open health observation %s: %w", id, err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return HealthObservationRecord{}, fmt.Errorf("read health observation %s: %w", id, err)
	}
	var record HealthObservationRecord
	if _, err := decodeTolerating(encoded, &record); err != nil {
		return HealthObservationRecord{}, fmt.Errorf("decode health observation %s: %w", id, err)
	}
	if record.ProductID != s.productID || record.ID != id {
		return HealthObservationRecord{}, fmt.Errorf("%w: %s belongs to another product or names another observation", ErrHealthObservationUnknown, id)
	}
	return record, nil
}
