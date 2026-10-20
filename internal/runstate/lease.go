package runstate

// The lease, taken on something that is not a run.
//
// Runs, promotions, and the account rotation each take their lease through the
// store that owns them, because what may hold one is decided there. Other
// harness state has the same need and no such store: the Slack sink owns one
// thread map per product, and
// two sinks against it would open two threads per work item and post everything
// twice. That is the same problem an unheld run is, so it is solved the same
// way rather than with a second mechanism — an advisory file lock the operating
// system drops when its holder exits, so a killed process leaves nothing for
// anybody to clear.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// TryLeasePath takes the exclusive lease on one path without waiting, reporting
// whether it got it. A lease that is already held belongs to a live process, so
// the caller is told so rather than queued behind it: what these leases guard is
// ownership of something singular, and waiting for it would mean two owners
// taking turns rather than one owner.
//
// The label names what is owned, so a failure to take or release the lease says
// which. The file outlives the lease for the reason a run's does: removing it
// while another process holds it would let a third take a lock on a file nobody
// else can see.
//
// The lease file is opened through the confined writer as a lock file. A
// caller with only a path declares the lease's own directory as the root it is
// confined to; a store in this package declares the state root, through
// tryLeaseIn.
func TryLeasePath(path, label string) (*Lease, bool, error) {
	if !filepath.IsAbs(path) {
		return nil, false, errors.New("a lease path must be absolute")
	}
	state, err := newStateRoot(filepath.Dir(path))
	if err != nil {
		return nil, false, fmt.Errorf("open the %s lease: %w", label, err)
	}
	return tryLeaseIn(state, filepath.Join(state.given, filepath.Base(path)), label)
}

// tryLeaseIn is TryLeasePath confined to a state root a store declares.
func tryLeaseIn(state stateRoot, path, label string) (*Lease, bool, error) {
	if !filepath.IsAbs(path) {
		return nil, false, errors.New("a lease path must be absolute")
	}
	if strings.TrimSpace(label) == "" {
		return nil, false, errors.New("a lease label is required")
	}
	file, err := state.openLock(path)
	if err != nil {
		return nil, false, fmt.Errorf("open the %s lease: %w", label, err)
	}
	held, err := tryLockStateFile(file)
	if err != nil {
		file.Close()
		return nil, false, fmt.Errorf("lock the %s lease: %w", label, err)
	}
	if !held {
		file.Close()
		return nil, false, nil
	}
	return &Lease{label: label, file: file}, true, nil
}
