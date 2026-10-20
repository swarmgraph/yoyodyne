package runstate

// The operator's release of a run's usage-limit wait.
//
// A run that paused on an exhausted usage limit sleeps against a deadline the
// provider named, and the wait rule deliberately honors that deadline rather
// than re-asking: it is what keeps a crash from retrying straight back into a
// window that is still closed. What that rule cannot express is the ground truth
// changing mid-wait. The deadline is a claim about the provider, and the
// operator can change what it is a claim about — by raising the account's
// capacity, for instance — so a run waiting out a limit its owner has already
// lifted is autonomy working against them.
//
// This record is how the operator says so. It is written beside the run rather
// than into it because the waiting process holds the run's lease and is the only
// thing entitled to write that state; the operator states the fact in its own
// file, and the run reads it while it waits. It carries no instruction beyond
// "stop waiting and ask again now", so the worst a wrong one can cost is a
// single refused request and a fresh pause on whatever the provider then
// reports.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ReleaseSchemaVersion is 1 and has never changed.
const ReleaseSchemaVersion = 1

// Release is one operator statement that a run's recorded usage-limit deadline
// no longer describes the provider, so the run should ask again now. It names
// the work item as well as the run because that is what the operator typed, and
// a record nobody can trace back to the item it was about is a record nobody can
// read afterwards.
type Release struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	RunID         string           `json:"run_id"`
	WorkItemID    string           `json:"work_item_id"`
	ReleasedAt    time.Time        `json:"released_at"`
}

func (r Release) Validate() error {
	var problems []error
	if r.SchemaVersion != ReleaseSchemaVersion {
		problems = append(problems, fmt.Errorf("release schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !runIDPattern.MatchString(r.RunID) {
		problems = append(problems, errors.New("run id is invalid"))
	}
	if strings.TrimSpace(r.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	if r.ReleasedAt.IsZero() {
		problems = append(problems, errors.New("released at is required"))
	}
	return errors.Join(problems...)
}

// RecordRelease makes one release durable. Recording twice is deliberately not
// an error: an operator who releases a run that has already re-parked on a fresh
// report means the same thing the second time, and refusing them would make the
// verb depend on timing they cannot see.
func (s *Store) RecordRelease(released Release) error {
	if released.ProductID != s.productID {
		return fmt.Errorf("release product %q does not match store product %q", released.ProductID, s.productID)
	}
	if err := released.Validate(); err != nil {
		return err
	}
	path, err := s.releasePath(released.RunID)
	if err != nil {
		return err
	}
	return replaceJSONFile(s.state, path, "release", released)
}

// ReleasedWait reports whether the operator has released one run's usage-limit
// wait. No record is the ordinary answer and means the recorded deadline still
// stands, which is why it is reported as an absence rather than as a failure to
// look.
func (s *Store) ReleasedWait(runID string) (Release, bool, error) {
	path, err := s.releasePath(runID)
	if err != nil {
		return Release{}, false, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Release{}, false, nil
	}
	if err != nil {
		return Release{}, false, fmt.Errorf("open release: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var released Release
	if err := decoder.Decode(&released); err != nil {
		return Release{}, false, fmt.Errorf("decode release %s: %w", runID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Release{}, false, fmt.Errorf("decode release %s: %w", runID, err)
	}
	if released.RunID != runID {
		return Release{}, false, fmt.Errorf("release file %s belongs to run %s", runID, released.RunID)
	}
	if err := released.Validate(); err != nil {
		return Release{}, false, err
	}
	return released, true, nil
}

// ClearRelease consumes one release. The run does this as it reissues the
// attempt the release freed, because a record left behind would release the
// *next* pause too — and the next pause is one the provider has just reported,
// which nobody has said anything about yet.
func (s *Store) ClearRelease(runID string) error {
	path, err := s.releasePath(runID)
	if err != nil {
		return err
	}
	if err := s.state.remove(path); err != nil {
		return fmt.Errorf("clear release: %w", err)
	}
	return nil
}
