package runstate

// The record that the harness has cleared the assignees runs' claims left on
// unfinished items, kept so it is done once.
//
// Claims made before they recorded the assignee they set left this machine's
// Git name on every item they touched (docs/diagnoses/yoyodyne-ifd-433-35-assignees-left-by-claims.md).
// The scheduler clears those names by itself on its first pass after the
// change that records them, and this file is what says it has: what it
// cleared, what it left because a person set the name, and what it left
// because a run holds the item. It is written once and never rewritten, so a
// pass that finds it does nothing, and a reader can always see what was done.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// AssigneeCleanupSchemaVersion is 1 and has never changed.
const AssigneeCleanupSchemaVersion = 1

const (
	assigneeCleanupFile = "assignee-cleanup.json"
	assigneeCleanupLock = ".assignee-cleanup.lock"
	// maxAssigneeCleanupTextBytes bounds each title, name, and sentence kept,
	// so a record of every unfinished item stays well inside the state bound.
	maxAssigneeCleanupTextBytes = 512
)

// ErrAssigneeCleanupRecorded is a second record of the clean-up, which is
// refused: the first one is the account of what was done.
var ErrAssigneeCleanupRecorded = errors.New("the clean-up of assignees runs left is already recorded")

// LeftoverAssignee is one unfinished item that carried an assignee, and what
// the clean-up made of it.
type LeftoverAssignee struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Assignee   string `json:"assignee"`
	// Because is why the clean-up cleared it or left it, in a reader's words.
	Because string `json:"because"`
}

// AssigneeCleanupRecord is what one clean-up did.
type AssigneeCleanupRecord struct {
	SchemaVersion int                `json:"schema_version"`
	ProductID     domain.ProductID   `json:"product_id"`
	RanAt         time.Time          `json:"ran_at"`
	Cleared       []LeftoverAssignee `json:"cleared"`
	ByPerson      []LeftoverAssignee `json:"left_set_by_a_person"`
	Held          []LeftoverAssignee `json:"left_held_by_a_run"`
	// Problems are the clears and marks the tracker refused. The scheduler
	// writes no record over a sweep that has any, so it is empty on every
	// record the scheduler keeps.
	Problems []string `json:"problems,omitempty"`
}

func (r AssigneeCleanupRecord) Validate() error {
	var problems []error
	if r.SchemaVersion != AssigneeCleanupSchemaVersion {
		problems = append(problems, fmt.Errorf("assignee clean-up schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if r.RanAt.IsZero() {
		problems = append(problems, errors.New("ran_at is required"))
	}
	return errors.Join(problems...)
}

// bounded folds every kept string to one bounded line.
func (r AssigneeCleanupRecord) bounded() AssigneeCleanupRecord {
	fold := func(entries []LeftoverAssignee) []LeftoverAssignee {
		folded := make([]LeftoverAssignee, 0, len(entries))
		for _, entry := range entries {
			entry.Title = oneline.Fold(entry.Title, maxAssigneeCleanupTextBytes)
			entry.Assignee = oneline.Fold(entry.Assignee, maxAssigneeCleanupTextBytes)
			entry.Because = oneline.Fold(entry.Because, maxAssigneeCleanupTextBytes)
			folded = append(folded, entry)
		}
		return folded
	}
	r.Cleared, r.ByPerson, r.Held = fold(r.Cleared), fold(r.ByPerson), fold(r.Held)
	problems := make([]string, 0, len(r.Problems))
	for _, problem := range r.Problems {
		problems = append(problems, oneline.Fold(problem, maxAssigneeCleanupTextBytes))
	}
	r.Problems = problems
	r.RanAt = r.RanAt.UTC()
	return r
}

// AssigneeCleanupStore keeps the one record, under the product's state.
type AssigneeCleanupStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

func NewAssigneeCleanupStore(root string, productID domain.ProductID) (*AssigneeCleanupStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the assignee clean-up's state root: %w", err)
	}
	return &AssigneeCleanupStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

func (s *AssigneeCleanupStore) directory() string {
	return filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID)))
}

// Path names the record, so whatever reports the clean-up can say where the
// whole list is.
func (s *AssigneeCleanupStore) Path() string {
	return filepath.Join(s.stateRoot, s.directory(), assigneeCleanupFile)
}

// Load reads the record, and answers nil where the clean-up has not run.
func (s *AssigneeCleanupStore) Load() (*AssigneeCleanupRecord, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the assignee clean-up record: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the assignee clean-up record: %w", err)
	}
	if len(encoded) > maxEncodedStateBytes {
		return nil, fmt.Errorf("the assignee clean-up record exceeds %d bytes", maxEncodedStateBytes)
	}
	var record AssigneeCleanupRecord
	unknown, err := decodeTolerating(encoded, &record)
	if err != nil {
		return nil, fmt.Errorf("decode the assignee clean-up record: %w", err)
	}
	noteUnknownFields("assignee clean-up record", unknown)
	if err := record.Validate(); err != nil {
		return nil, fmt.Errorf("the assignee clean-up record: %w", err)
	}
	return &record, nil
}

// Record writes the record once, as a new file read back before it is
// reported written. A record already there is refused with
// ErrAssigneeCleanupRecorded.
func (s *AssigneeCleanupStore) Record(record AssigneeCleanupRecord) error {
	record = record.bounded()
	if record.ProductID != s.productID {
		return fmt.Errorf("assignee clean-up record names product %q, this store keeps %q", record.ProductID, s.productID)
	}
	if err := record.Validate(); err != nil {
		return err
	}
	encoded, err := encodeRecord("assignee clean-up record", record)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStateBytes {
		return fmt.Errorf("encoded assignee clean-up record is %d bytes, limit is %d", len(encoded), maxEncodedStateBytes)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the assignee clean-up's state root: %w", err)
	}
	defer root.Close()
	directory := s.directory()
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("create the product's state directory: %w", err)
	}
	return s.locked(root, directory, func() error {
		path := filepath.Join(directory, assigneeCleanupFile)
		if _, err := root.ReadFile(path); err == nil {
			return ErrAssigneeCleanupRecorded
		}
		if err := root.CreateFile(path, encoded, 0o600); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return ErrAssigneeCleanupRecorded
			}
			return fmt.Errorf("record the assignee clean-up: %w", err)
		}
		if err := root.Unchanged(); err != nil {
			return err
		}
		readBack, err := root.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read back the assignee clean-up record: %w", err)
		}
		if !bytes.Equal(readBack, encoded) {
			return errors.New("the assignee clean-up record read back differently from what was written")
		}
		return nil
	})
}

func (s *AssigneeCleanupStore) locked(root *repowrite.PinnedRoot, directory string, change func() error) error {
	lock, err := root.OpenLock(filepath.Join(directory, assigneeCleanupLock), 0o600)
	if err != nil {
		return fmt.Errorf("open the assignee clean-up's lock: %w", err)
	}
	defer lock.Close()
	if err := lockStateFile(context.Background(), lock); err != nil {
		return fmt.Errorf("lock the assignee clean-up: %w", err)
	}
	defer unlockStateFile(lock)
	return change()
}
