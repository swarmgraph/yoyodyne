package runstate

// One reading of the tracker's two whole-set listings, shared by every status
// reading the product makes, whichever process makes it.
//
// A status reading asks the tracker for every item and for what is ready
// (internal/readmodel, sharedlisting.go). `yoyo status`, the dashboard, Slack,
// the health observation handed to every scheduled pass, and the recurring
// passes each make one, from processes of their own, and every bd process opens
// the whole store under one exclusive lock. So each reading held the store for
// two full listings, the readings did not know about each other, and a write
// arriving behind a few of them waited out its own limit.
//
// So the reading is kept here, and a status reading asks for it rather than for
// the tracker: one begun recently enough is handed over as it is, and only where
// none was does the caller make one, under a lock every caller on the machine
// queues on, so that the callers queued behind it are handed the one it made.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// TrackerReadingSchemaVersion is 1 and has never changed.
const TrackerReadingSchemaVersion = 1

// maxTrackerReadingBytes bounds the document: the two listings it carries are
// each bounded by what the tracker client keeps of one bd answer.
const maxTrackerReadingBytes = 512 << 20

// TrackerReading is one reading of every item the tracker holds and of what it
// reports ready, and when it began. A listing that failed carries what it said
// in place of its items, so the callers queued behind a reading the tracker did
// not answer are told so rather than each asking the tracker again.
type TrackerReading struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// StartedAt is when the reading began, before either listing was asked for,
	// so every write the tracker finished before it is in what it carries.
	StartedAt    time.Time        `json:"started_at"`
	Every        []beads.WorkItem `json:"every,omitempty"`
	EveryFailure string           `json:"every_failure,omitempty"`
	Ready        []beads.WorkItem `json:"ready,omitempty"`
	ReadyFailure string           `json:"ready_failure,omitempty"`
}

// TrackerReadingStore is where the product's shared reading is kept, one file
// under the product beside the other records the status reads.
type TrackerReadingStore struct {
	root      string
	productID domain.ProductID
	now       func() time.Time
}

func NewTrackerReadingStore(root string, productID domain.ProductID) (*TrackerReadingStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &TrackerReadingStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
		now:       time.Now,
	}, nil
}

// Path names the document, so a failure can say where it is.
func (s *TrackerReadingStore) Path() string { return filepath.Join(s.root, "tracker-reading.json") }

// Share hands over a reading that began no more than fresh before it was asked
// for, making one with read where none did. Callers queue on one lock across
// processes, so a caller that arrives while a reading is being made waits for
// it and is handed it, rather than asking the tracker behind it.
//
// ctx bounds only the wait for the lock; read is called with none of it, and is
// what bounds its own listings. read says whether what it read may be kept for
// others: a reading its own caller abandoned may not. Share fails only where it
// has no reading to hand over: one it made and could not keep is handed over
// all the same, and the next caller makes its own.
func (s *TrackerReadingStore) Share(ctx context.Context, fresh time.Duration, read func() (TrackerReading, bool)) (TrackerReading, error) {
	asked := s.now()
	release, err := s.lock(ctx)
	if err != nil {
		return TrackerReading{}, err
	}
	defer release()
	if kept, err := s.read(); err == nil && !kept.StartedAt.Before(asked.Add(-fresh)) {
		return kept, nil
	}
	started := s.now()
	reading, keep := read()
	reading.SchemaVersion, reading.ProductID, reading.StartedAt = TrackerReadingSchemaVersion, s.productID, started.UTC()
	if keep {
		_ = s.write(reading)
	}
	return reading, nil
}

// read is the kept reading. No document, or one this build cannot read, is
// answered as an error, which Share reads as no reading kept.
func (s *TrackerReadingStore) read() (TrackerReading, error) {
	file, err := os.Open(s.Path())
	if err != nil {
		return TrackerReading{}, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxTrackerReadingBytes+1))
	if err != nil {
		return TrackerReading{}, fmt.Errorf("read the shared tracker reading: %w", err)
	}
	if len(encoded) > maxTrackerReadingBytes {
		return TrackerReading{}, fmt.Errorf("the shared tracker reading is over %d bytes", maxTrackerReadingBytes)
	}
	var reading TrackerReading
	if err := json.Unmarshal(encoded, &reading); err != nil {
		return TrackerReading{}, fmt.Errorf("decode the shared tracker reading: %w", err)
	}
	if reading.SchemaVersion != TrackerReadingSchemaVersion {
		return TrackerReading{}, fmt.Errorf("shared tracker reading schema version %d is not supported", reading.SchemaVersion)
	}
	if reading.ProductID != s.productID {
		return TrackerReading{}, fmt.Errorf("shared tracker reading belongs to product %q, not %q", reading.ProductID, s.productID)
	}
	return reading, nil
}

func (s *TrackerReadingStore) write(reading TrackerReading) error {
	encoded, err := json.Marshal(reading)
	if err != nil {
		return fmt.Errorf("encode the shared tracker reading: %w", err)
	}
	if len(encoded) > maxTrackerReadingBytes {
		return fmt.Errorf("the shared tracker reading is %d bytes, limit is %d", len(encoded), maxTrackerReadingBytes)
	}
	temporary, err := os.CreateTemp(s.root, ".tracker-reading-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary shared tracker reading: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary shared tracker reading: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary shared tracker reading: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary shared tracker reading: %w", err)
	}
	if err := os.Rename(temporaryPath, s.Path()); err != nil {
		return fmt.Errorf("replace shared tracker reading: %w", err)
	}
	return nil
}

func (s *TrackerReadingStore) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create product state directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open shared tracker reading lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("wait for the shared tracker reading: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
