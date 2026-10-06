package runstate

// Whether the tracker is answering listings, and since when it has not been.
//
// A `bd list` the harness gives up on is said where it failed — a problem on a
// pass, a line on a docket, a refusal in a conversation — and nowhere that
// outlives the reader of that one place. On 2026-09-29 the development
// manager reported listings timing out, and the only way to learn since when
// was to read every place a listing had failed and put the times in order. So
// every listing the harness's tracker client makes writes its outcome here, and
// `yoyo status` reads this one document to say the tracker is not answering
// listings, since when, and what the last one said.
//
// It is one small document rewritten in place rather than a log. What is
// needed is whether listings are failing now and when that began; a listing
// that answers ends it. And it is written only when something changes: a
// listing that answers while nothing is failing reads the document and writes
// nothing, so the dozens of listings a busy hour makes cost a read each rather
// than a write each.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// TrackerListingsSchemaVersion is 1 and has never changed.
const TrackerListingsSchemaVersion = 1

// maxTrackerListingFailureBytes bounds the failure the record keeps. It is what
// the last listing said, read beside the moment it began; the whole of it is
// wherever that listing failed.
const maxTrackerListingFailureBytes = 1 << 10

// TrackerListings is how the tracker's listings stand: answering, or failing
// since a moment, with how many have failed since then and what the latest
// said.
type TrackerListings struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// FailingSince is when the first listing failed after the last one that
	// answered. It is zero while listings are answering.
	FailingSince time.Time `json:"failing_since,omitzero"`
	// Failures is how many listings have failed since FailingSince, each after
	// the retries its own bound allows.
	Failures int `json:"failures,omitempty"`
	// LatestAt is when the latest failure was recorded, and Latest what it said.
	LatestAt time.Time `json:"latest_at,omitzero"`
	Latest   string    `json:"latest,omitempty"`
	// AnsweredAt is when a listing last answered after listings had been
	// failing, and zero where none has had to. It is kept so a reader can say
	// the tracker came back and when, rather than only that nothing is failing.
	AnsweredAt time.Time `json:"answered_at,omitzero"`
}

// Failing reports listings that are failing now.
func (t TrackerListings) Failing() bool {
	return !t.FailingSince.IsZero()
}

// Says is the failure as a sentence: since when, how many, and what the latest
// listing said. It is empty while listings are answering.
func (t TrackerListings) Says() string {
	if !t.Failing() {
		return ""
	}
	return fmt.Sprintf("the tracker has not answered a listing since %s: %d listing(s) failed after their retries, the latest at %s: %s",
		t.FailingSince.UTC().Format(time.RFC3339), t.Failures, t.LatestAt.UTC().Format(time.RFC3339), t.Latest)
}

// TrackerListingStore is where the product's listing record is kept, one file
// under the product beside the other records the status reads.
type TrackerListingStore struct {
	root      string
	productID domain.ProductID
}

func NewTrackerListingStore(root string, productID domain.ProductID) (*TrackerListingStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &TrackerListingStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

// Path names the document, so a failure can say where it is.
func (s *TrackerListingStore) Path() string { return filepath.Join(s.root, "tracker-listings.json") }

// Failed records a listing that failed after its retries. The first failure
// after an answer sets when the failing began; every later one counts and
// replaces what the latest said.
func (s *TrackerListingStore) Failed(at time.Time, failure string) error {
	failure = oneline.Fold(failure, maxTrackerListingFailureBytes)
	release, err := s.lock()
	if err != nil {
		return err
	}
	defer release()
	record, err := s.read(true)
	if err != nil {
		return err
	}
	at = at.UTC()
	if !record.Failing() {
		record.FailingSince, record.Failures = at, 0
	}
	record.Failures++
	record.LatestAt, record.Latest = at, failure
	return s.write(record)
}

// Answered records a listing that answered. It writes only where listings had
// been failing, which is the one case in which an answer is news; otherwise it
// is a read.
func (s *TrackerListingStore) Answered(at time.Time) error {
	current, err := s.read(false)
	if err != nil {
		return err
	}
	if !current.Failing() {
		return nil
	}
	release, err := s.lock()
	if err != nil {
		return err
	}
	defer release()
	record, err := s.read(true)
	if err != nil {
		return err
	}
	if !record.Failing() {
		return nil
	}
	record.FailingSince, record.Failures, record.LatestAt, record.Latest = time.Time{}, 0, time.Time{}, ""
	record.AnsweredAt = at.UTC()
	return s.write(record)
}

// Read returns how listings stand. No document is the ordinary answer on a
// product whose listings have never failed, and reads as answering. A field
// this build does not know is stepped over, because what reads this is the
// read model and a newer build's record must not blank its reading.
func (s *TrackerListingStore) Read() (TrackerListings, error) {
	return s.read(false)
}

func (s *TrackerListingStore) read(strict bool) (TrackerListings, error) {
	empty := TrackerListings{SchemaVersion: TrackerListingsSchemaVersion, ProductID: s.productID}
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("open tracker listing record: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return empty, fmt.Errorf("read tracker listing record: %w", err)
	}
	var record TrackerListings
	if strict {
		err = decodeStrictly(encoded, &record)
	} else {
		_, err = decodeTolerating(encoded, &record)
	}
	if err != nil {
		return empty, fmt.Errorf("decode tracker listing record: %w", err)
	}
	if record.SchemaVersion != TrackerListingsSchemaVersion {
		return empty, fmt.Errorf("tracker listing schema version %d is not supported", record.SchemaVersion)
	}
	if record.ProductID != s.productID {
		return empty, fmt.Errorf("tracker listing record belongs to product %q, not %q", record.ProductID, s.productID)
	}
	return record, nil
}

func (s *TrackerListingStore) write(record TrackerListings) error {
	record.SchemaVersion, record.ProductID = TrackerListingsSchemaVersion, s.productID
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".tracker-listings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary tracker listing record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary tracker listing record: %w", err)
	}
	if err := writeJSONFile(temporary, "tracker listing record", record); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary tracker listing record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.Path()); err != nil {
		return fmt.Errorf("replace tracker listing record: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the writers: every process that lists the tracker writes
// here, and two failures counted over one read would lose one of them.
func (s *TrackerListingStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create product state directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open tracker listing lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), intakeLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the tracker listing record: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
