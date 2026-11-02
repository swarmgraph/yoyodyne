package runstate

// The hold the maintenance pass places on new runs while a squash of the live
// tracker store is due (internal/maintain, trackersquash.go).
//
// A squash holds the store's exclusive lock for its whole length, so a run in
// the middle of its work when it starts can have its tracker reads time out
// over nothing it did. So once a squash is due, maintenance places this hold,
// waits for the runs already going to finish, and squashes only once none is
// in flight; a watching session reads the hold at every pull and starts
// nothing while it stands. Maintenance lifts it however the squash step ends.
//
// It carries its own expiry, Until, which is the redeploy drain bound and the
// squash's measured time on the copy added to when it was placed. A pass that
// dies with the hold placed leaves a record nothing lifts, and the expiry is
// what keeps that record from holding the line for ever: a hold past its
// Until is read as no hold at all.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// TrackerSquashHoldSchemaVersion is 1 and has never changed.
const TrackerSquashHoldSchemaVersion = 1

// trackerSquashHoldFile is the hold's one fixed name under the product.
const trackerSquashHoldFile = "tracker-squash-hold.json"

// TrackerSquashHold is the hold standing on new runs while a squash of the
// tracker store is due or running.
type TrackerSquashHold struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Since is when maintenance placed it, and Until when it ends on its own
	// if nothing lifts it first.
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	// Reason is why the squash is due, in maintenance's words.
	Reason string `json:"reason"`
}

// Standing reports a hold that has not yet reached its expiry at now.
func (h TrackerSquashHold) Standing(now time.Time) bool { return now.Before(h.Until) }

// Says is the hold in a sentence, for the line a watching session writes while
// it starts nothing.
func (h TrackerSquashHold) Says() string {
	return fmt.Sprintf("maintenance is about to squash the tracker store's history, which holds the store so other tracker reads wait behind it, so no new run is started until it has finished or been skipped; held since %s, and the hold ends on its own at %s if maintenance has not lifted it by then",
		h.Since.Local().Format("2006-01-02 15:04 MST"), h.Until.Local().Format("2006-01-02 15:04 MST"))
}

// TrackerSquashHoldStore is where the hold is kept: one file under the
// product, written by maintenance alone and read by every watching session.
type TrackerSquashHoldStore struct {
	root, anchor string
	productID    domain.ProductID
}

func NewTrackerSquashHoldStore(root string, productID domain.ProductID) (*TrackerSquashHoldStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &TrackerSquashHoldStore{root: home.ProductDirectory(root, string(productID)), anchor: anchor, productID: productID}, nil
}

// Path names the hold's file, so a failure can say where it is.
func (s *TrackerSquashHoldStore) Path() string { return filepath.Join(s.root, trackerSquashHoldFile) }

// Place writes the hold, replacing any earlier one.
func (s *TrackerSquashHoldStore) Place(hold TrackerSquashHold) error {
	if hold.Since.IsZero() || !hold.Until.After(hold.Since) {
		return errors.New("a hold on new runs for a tracker squash needs a start and an end after it")
	}
	hold.SchemaVersion, hold.ProductID = TrackerSquashHoldSchemaVersion, s.productID
	hold.Since, hold.Until = hold.Since.UTC(), hold.Until.UTC()
	hold.Reason = oneline.Fold(strings.TrimSpace(hold.Reason), maxTrackerStoreCleanupTextBytes)
	encoded, err := json.Marshal(hold)
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.WriteFile(trackerSquashHoldFile, append(encoded, '\n'), 0o600, false)
}

// Lift removes the hold. Lifting none is not an error.
func (s *TrackerSquashHoldStore) Lift() error {
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(trackerSquashHoldFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("lift the hold on new runs for a tracker squash: %w", err)
	}
	return nil
}

// Standing reports the hold where one is placed and has not reached its end at
// now. No file is the ordinary answer. A record that cannot be read is an
// error, which a pull reports and pulls through, because a hold maintenance
// cannot be shown to have placed must not stop the line.
func (s *TrackerSquashHoldStore) Standing(now time.Time) (TrackerSquashHold, bool, error) {
	if s == nil {
		return TrackerSquashHold{}, false, nil
	}
	encoded, err := readCleanupFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return TrackerSquashHold{}, false, nil
	}
	if err != nil {
		return TrackerSquashHold{}, false, fmt.Errorf("read the hold on new runs for a tracker squash: %w", err)
	}
	var hold TrackerSquashHold
	if _, err := decodeTolerating(encoded, &hold); err != nil {
		return TrackerSquashHold{}, false, fmt.Errorf("decode the hold on new runs for a tracker squash %s: %w", s.Path(), err)
	}
	if hold.SchemaVersion != TrackerSquashHoldSchemaVersion {
		return TrackerSquashHold{}, false, fmt.Errorf("the hold on new runs for a tracker squash %s has schema version %d, which is not supported", s.Path(), hold.SchemaVersion)
	}
	if hold.ProductID != s.productID {
		return TrackerSquashHold{}, false, fmt.Errorf("the hold on new runs for a tracker squash %s belongs to product %q, not %q", s.Path(), hold.ProductID, s.productID)
	}
	return hold, hold.Standing(now), nil
}
