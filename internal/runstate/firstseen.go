package runstate

// When each program manager instance was first seen in the loaded
// configuration.
//
// An instance's stale reading is measured from its last completed pass, and an
// instance that has never completed one has no pass to measure from. The
// configuration keeps no record of when an instance was added to it, so without
// this the only moment such an instance had was its first conversation — and an
// instance the scheduler has never woken has none, which made the dead
// scheduler, the one case the stale reading exists to catch, the one case it
// could not see for a new instance. This record is that moment: the harness
// writes it at the first load of the configuration that carries the instance,
// and never moves it afterwards.
//
// It is one small record per product, beside the instances' other records under
// `program-managers/`, rewritten whole under a lock across processes so that two
// loads at one moment cannot each find an instance unrecorded and each give it
// a different first moment.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// FirstSeenSchemaVersion is 1 and has never changed.
const FirstSeenSchemaVersion = 1

// firstSeenLockWait bounds the wait for the record's lock, for the reason the
// restart request log's is bounded: what it waits on is one small rewrite.
const firstSeenLockWait = 5 * time.Second

// FirstSeenRecord is the whole of the record: each instance by its agent name,
// and when a load of the configuration first carried it.
type FirstSeenRecord struct {
	SchemaVersion int                  `json:"schema_version"`
	ProductID     domain.ProductID     `json:"product_id"`
	Instances     map[string]time.Time `json:"instances"`
}

func (r FirstSeenRecord) Validate() error {
	var problems []error
	if r.SchemaVersion != FirstSeenSchemaVersion {
		problems = append(problems, fmt.Errorf("first-seen schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	for agent, at := range r.Instances {
		if err := domain.ValidateIdentifier("agent", agent); err != nil {
			problems = append(problems, err)
		}
		if at.IsZero() {
			problems = append(problems, fmt.Errorf("%s has no moment it was first seen", agent))
		}
	}
	return errors.Join(problems...)
}

// FirstSeenStore is the record of when each program manager instance was first
// seen, under the product's state beside the instances' other records.
type FirstSeenStore struct {
	root      string
	productID domain.ProductID
}

func NewFirstSeenStore(root string, productID domain.ProductID) (*FirstSeenStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &FirstSeenStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "program-managers"),
		productID: productID,
	}, nil
}

// Path names the record itself, so a failure can say where it is.
func (s *FirstSeenStore) Path() string { return filepath.Join(s.root, "first-seen.json") }

// Observe records now as the moment each of the agents was first seen, for
// every one the record does not already hold, and returns the whole record. An
// agent already recorded keeps the moment it has: an instance taken out of the
// configuration and put back is the same instance, seen first when it was.
func (s *FirstSeenStore) Observe(agents []string, now time.Time) (map[string]time.Time, error) {
	for _, agent := range agents {
		if err := domain.ValidateIdentifier("agent", agent); err != nil {
			return nil, err
		}
	}
	current, err := s.FirstSeen()
	if err != nil {
		return nil, err
	}
	if !missingAny(current, agents) {
		return current, nil
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Read again under the lock: another load may have recorded the same
	// instance between the first read and taking it. This read precedes the
	// write, so it is the strict door.
	current, err = s.read(true)
	if err != nil {
		return nil, err
	}
	if !missingAny(current, agents) {
		return current, nil
	}
	for _, agent := range agents {
		if _, seen := current[agent]; !seen {
			current[agent] = now.UTC()
		}
	}
	record := FirstSeenRecord{SchemaVersion: FirstSeenSchemaVersion, ProductID: s.productID, Instances: current}
	if err := record.Validate(); err != nil {
		return nil, err
	}
	if err := replaceJSONFile(s.root, s.Path(), "program manager first-seen record", record); err != nil {
		return nil, err
	}
	return current, nil
}

// FirstSeen is every instance the record holds and when it was first seen. A
// record that does not exist yet is a product whose configuration has carried
// no program manager since this was recorded, which is not a failure to read.
// It is the read-only door the read model reads through, so a field a newer
// build added is stepped over rather than refused.
func (s *FirstSeenStore) FirstSeen() (map[string]time.Time, error) {
	return s.read(false)
}

// read decodes the record through one of its two doors: the strict one before a
// write, where a field stepped over on the way in is lost on the way out, and
// the tolerant one everywhere else.
func (s *FirstSeenStore) read(strict bool) (map[string]time.Time, error) {
	encoded, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]time.Time{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read program manager first-seen record: %w", err)
	}
	var record FirstSeenRecord
	if strict {
		err = decodeStrictly(encoded, &record)
	} else {
		_, err = decodeTolerating(encoded, &record)
	}
	if err != nil {
		return nil, fmt.Errorf("decode program manager first-seen record: %w", err)
	}
	if err := record.Validate(); err != nil {
		return nil, fmt.Errorf("decode program manager first-seen record: %w", err)
	}
	if record.ProductID != s.productID {
		return nil, fmt.Errorf("decode program manager first-seen record: record product %q does not match store product %q", record.ProductID, s.productID)
	}
	if record.Instances == nil {
		record.Instances = map[string]time.Time{}
	}
	return record.Instances, nil
}

func missingAny(recorded map[string]time.Time, agents []string) bool {
	for _, agent := range agents {
		if _, seen := recorded[agent]; !seen {
			return true
		}
	}
	return false
}

func (s *FirstSeenStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create program manager directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open program manager first-seen lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), firstSeenLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the program manager first-seen record: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
