package runstate

// What this machine knows about sharing its records with the other factories of
// a team, in team mode (docs/designs/working-alongside-other-contributors.md,
// section 7).
//
// The records themselves (reports, report handlings, directives) travel through
// a Git reference on the shared remote, and each check copies what other
// factories wrote into this machine's own stores. What is kept here is only the
// state of that sharing: when it was last tried, what the remote held, how many
// records are still waiting to reach it, and why the last try failed, so a
// surface can say so rather than let a machine that cannot reach the remote look
// like one with nothing to share.
//
// One file per product, replaced whole.

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

// SharedRecordsSchemaVersion is 1 and has never changed.
const SharedRecordsSchemaVersion = 1

const (
	sharedRecordsFile       = "shared-records.json"
	sharedRecordsRepository = "records.git"
	sharedRecordsLockFile   = ".shared-records.lock"
)

// SharedRecordsState is the state of one product's record sharing.
type SharedRecordsState struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// CheckedAt is when this machine last tried to share and read the records.
	CheckedAt time.Time `json:"checked_at"`
	// Fetched is the records commit the last fetch read, and empty where the
	// remote held none or it could not be read.
	Fetched string `json:"fetched,omitempty"`
	// SharedAt is when this machine's records last all reached the remote.
	SharedAt *time.Time `json:"shared_at,omitempty"`
	// Waiting is how many records filed on this machine are not yet on the shared
	// remote.
	Waiting int `json:"waiting"`
	// Problem is why the last try could not reach the remote, in the failure's own
	// words; it is empty when the try did.
	Problem string `json:"problem,omitempty"`
	// Copied is how many records the last check copied in from other factories.
	Copied int `json:"copied,omitempty"`
}

// SharedRecordsStore keeps the state of one product's record sharing, in the
// product's directory under the operating-system state root.
type SharedRecordsStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

func NewSharedRecordsStore(root string, productID domain.ProductID) (*SharedRecordsStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the shared records' state root: %w", err)
	}
	return &SharedRecordsStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

// productDirectory is the product's directory relative to the state root.
func (s *SharedRecordsStore) productDirectory() string {
	return filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID)))
}

// RepositoryDirectory is where the bare Git repository holding the records
// commits is kept: records.git in the product's directory, as an absolute path.
func (s *SharedRecordsStore) RepositoryDirectory() string {
	return filepath.Join(s.stateRoot, s.productDirectory(), sharedRecordsRepository)
}

// Load reads the state of the product's record sharing, and false where nothing
// was ever recorded.
func (s *SharedRecordsStore) Load() (SharedRecordsState, bool, error) {
	encoded, err := os.ReadFile(filepath.Join(s.stateRoot, s.productDirectory(), sharedRecordsFile))
	if errors.Is(err, os.ErrNotExist) {
		return SharedRecordsState{}, false, nil
	}
	if err != nil {
		return SharedRecordsState{}, false, err
	}
	var state SharedRecordsState
	if err := decodeStrictly(encoded, &state); err != nil {
		return SharedRecordsState{}, false, fmt.Errorf("decode the shared records' state: %w", err)
	}
	return state, true, nil
}

// Save replaces the state of the product's record sharing.
func (s *SharedRecordsStore) Save(state SharedRecordsState) error {
	state.SchemaVersion = SharedRecordsSchemaVersion
	state.ProductID = s.productID
	encoded, err := encodeRecord("shared records state", state)
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the shared records' state root: %w", err)
	}
	defer root.Close()
	if err := root.Unchanged(); err != nil {
		return err
	}
	if err := root.WriteFile(filepath.Join(s.productDirectory(), sharedRecordsFile), encoded, 0o600, false); err != nil {
		return fmt.Errorf("record the state of the shared records: %w", err)
	}
	return nil
}

// Lock holds the product's record sharing against every other process on this
// machine that shares records for it, until the returned function is called.
func (s *SharedRecordsStore) Lock(ctx context.Context) (func(), error) {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the shared records' state root: %w", err)
	}
	defer root.Close()
	if err := root.MakeDirectory(s.productDirectory(), 0o700); err != nil {
		return nil, err
	}
	lock, err := root.OpenLock(filepath.Join(s.productDirectory(), sharedRecordsLockFile), 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockStateFile(ctx, lock); err != nil {
		closeStateFile(lock)
		return nil, fmt.Errorf("wait for the shared records: %w", err)
	}
	return func() { unlockStateFile(lock); closeStateFile(lock) }, nil
}
