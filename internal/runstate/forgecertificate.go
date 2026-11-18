package runstate

// Whether the supervisor's forge checks are failing on the system certificate
// store, and since when.
//
// A supervisor started from a shell that cannot read the system certificate
// store (an AI session's sandbox on macOS is one) cannot verify GitHub's
// certificate, so every forge check it runs fails the same way. Each check says
// so in the supervisor's log and nowhere that `yoyo status` or the dashboard
// reads. This one small document records that every check since a moment has
// failed on the store, so they can say it once rather than once per check.
//
// The supervisor's forge check is the only writer, and it runs one check at a
// time, so no lock is taken. The document is written only when something
// changes: a clean check while nothing is failing reads and writes nothing.

import (
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

// ForgeCertificateSchemaVersion is 1 and has never changed.
const ForgeCertificateSchemaVersion = 1

// maxForgeCertificateLatestBytes bounds the line the record keeps of the latest
// check; the whole of it is in the supervisor's log.
const maxForgeCertificateLatestBytes = 1 << 10

// ForgeCertificateRemedy is what a person does about it. It is said wherever
// the failure is, so it is written once.
const ForgeCertificateRemedy = "start the supervisor again outside that shell: from an ordinary terminal run `yoyo stop` and then `yoyo start`, or run `yoyo setup --launch-agent` there so the launch agent starts it outside any shell from then on; the first forge check that finishes cleanly clears this"

// ForgeCertificateFailure is that every forge check one supervisor has run since
// Since failed on the system certificate store.
type ForgeCertificateFailure struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// SupervisorPID is the supervisor whose checks failed; a different one
	// starts the count again.
	SupervisorPID int `json:"supervisor_pid,omitempty"`
	// Since is when the first of those checks ended. It is zero while nothing is
	// failing.
	Since time.Time `json:"since,omitzero"`
	// Checks is how many checks have failed since Since.
	Checks int `json:"checks,omitempty"`
	// LatestAt is when the latest check ended, and Latest the line it said.
	LatestAt time.Time `json:"latest_at,omitzero"`
	Latest   string    `json:"latest,omitempty"`
}

// Failing reports checks that are failing on the certificate store now.
func (f ForgeCertificateFailure) Failing() bool { return !f.Since.IsZero() }

// Says is the failure as one sentence, empty while nothing is failing.
func (f ForgeCertificateFailure) Says() string {
	if !f.Failing() {
		return ""
	}
	const layout = "2006-01-02 15:04 MST"
	return fmt.Sprintf("every forge check the supervisor (pid %d) has run since %s has failed on the system certificate store, %d in all, the latest at %s: %s; the supervisor was started from a shell that cannot read the certificate store, such as an AI session's sandbox, so it cannot reach GitHub, and pull requests merged there are not noticed or closed out",
		f.SupervisorPID, f.Since.Local().Format(layout), f.Checks, f.LatestAt.Local().Format(layout), f.Latest)
}

// ForgeCertificateStore is where the product's record is kept, one file in the
// product's state directory.
type ForgeCertificateStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

func NewForgeCertificateStore(root string, productID domain.ProductID) (*ForgeCertificateStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the forge certificate record's state root: %w", err)
	}
	return &ForgeCertificateStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

func (s *ForgeCertificateStore) directory() string {
	return filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID)))
}

// Path names the document, so a failure can say where it is.
func (s *ForgeCertificateStore) Path() string {
	return filepath.Join(s.stateRoot, s.directory(), "forge-certificate.json")
}

// Failed records a forge check that failed on the certificate store. It starts a
// new failure when none is standing or the supervisor is not the one recorded;
// either way it then counts this check and keeps what it said.
func (s *ForgeCertificateStore) Failed(pid int, at time.Time, detail string) error {
	record, err := s.Read()
	if err != nil {
		// A record this build cannot read is replaced rather than left to hide
		// the failure.
		record = s.empty()
	}
	at = at.UTC()
	if !record.Failing() || record.SupervisorPID != pid {
		record.Since, record.Checks = at, 0
	}
	record.SupervisorPID = pid
	record.Checks++
	record.LatestAt, record.Latest = at, oneline.Fold(detail, maxForgeCertificateLatestBytes)
	return s.write(record)
}

// Cleared records that a check finished cleanly. It writes only where a failure
// is standing; otherwise it is a read.
func (s *ForgeCertificateStore) Cleared() error {
	record, err := s.Read()
	if err == nil && !record.Failing() {
		return nil
	}
	return s.write(s.empty())
}

// Read returns how the checks stand. No document is the ordinary answer and
// reads as nothing failing. A field this build does not know is stepped over.
func (s *ForgeCertificateStore) Read() (ForgeCertificateFailure, error) {
	empty := s.empty()
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("open forge certificate record: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return empty, fmt.Errorf("read forge certificate record: %w", err)
	}
	var record ForgeCertificateFailure
	if _, err := decodeTolerating(encoded, &record); err != nil {
		return empty, fmt.Errorf("decode forge certificate record: %w", err)
	}
	if record.SchemaVersion != ForgeCertificateSchemaVersion {
		return empty, fmt.Errorf("forge certificate schema version %d is not supported", record.SchemaVersion)
	}
	if record.ProductID != s.productID {
		return empty, fmt.Errorf("forge certificate record belongs to product %q, not %q", record.ProductID, s.productID)
	}
	return record, nil
}

func (s *ForgeCertificateStore) empty() ForgeCertificateFailure {
	return ForgeCertificateFailure{SchemaVersion: ForgeCertificateSchemaVersion, ProductID: s.productID}
}

func (s *ForgeCertificateStore) write(record ForgeCertificateFailure) error {
	record.SchemaVersion, record.ProductID = ForgeCertificateSchemaVersion, s.productID
	encoded, err := encodeRecord("forge certificate record", record)
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the forge certificate record's state root: %w", err)
	}
	defer root.Close()
	directory := s.directory()
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("create the product state directory: %w", err)
	}
	if err := root.WriteFile(filepath.Join(directory, "forge-certificate.json"), encoded, 0o600, false); err != nil {
		return fmt.Errorf("write forge certificate record: %w", err)
	}
	return root.Unchanged()
}
