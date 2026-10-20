package runstate

// The account rotation, held together across the two steps it is made of.
//
// The pool's cursor is the run records: a start is served by the first active
// alias after the one the last run recorded, and the record that moves the
// cursor is the one that start reserves. Those are two steps, and with nothing
// holding them together two starts in the same moment read the same cursor and
// are both served by the same account — the pool double-serving under exactly
// the concurrency it exists for. The lease below spans the read and the write,
// so a start that has chosen has already moved the cursor the next one reads.
//
// It is a sibling of the promotion lease and works the same way: an advisory
// file lock the operating system drops when its holder exits, so a start whose
// process died leaves no stale lock for anybody to clear. Like a promotion and
// unlike a run, it is waited out rather than refused — two starts choosing
// accounts are not a duplicate of one another, they are two runs entitled to
// begin one after the other.
//
// It is per product, because the records the cursor is read from are.

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

const (
	// rotationQueueWait bounds how long a start waits its turn to choose. The
	// section it queues behind is short — a scan of the run records, the week of
	// spend behind them where the operator budgeted an account, and one file
	// write — so the bound only has to cover a queue of starts rather than a run.
	// What it is really for is the holder that never finishes, which must not hold
	// every other start on this product until its process dies.
	rotationQueueWait = 2 * time.Minute
	// rotationLockName is the file the rotation lease is taken on. It is a single
	// file per product because the rotation is one: there is one cursor and one
	// order to advance it in, and two starts choosing at once is precisely what
	// must not happen.
	rotationLockName = ".rotation.lock"
)

// LeaseRotation admits this process to choose the account its run is served by
// and to record the run that will spend it, waiting its turn behind whichever
// start is choosing now. The wait is bounded: a caller that never reaches the
// front is told so rather than held forever, and a cancelled start stops waiting
// immediately.
func (s *Store) LeaseRotation(ctx context.Context) (*Lease, error) {
	// The lease file outlives every start, for the reason a run's does: removing
	// it while another process holds it would let a third take a lock on a file
	// nobody else can see.
	file, err := s.state.openLock(filepath.Join(s.root, rotationLockName))
	if err != nil {
		return nil, fmt.Errorf("open the account rotation lease: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, rotationQueueWait)
	defer cancel()
	if err := lockStateFile(waitCtx, file); err != nil {
		file.Close()
		// A caller whose own context is still live waited out the bound rather than
		// being cancelled, and the two must not read alike: one is a rotation
		// nothing is draining, the other is the start being stopped.
		if ctx.Err() == nil {
			return nil, fmt.Errorf("wait to choose a provider account: another start held the rotation lease for the whole %s wait", rotationQueueWait)
		}
		return nil, fmt.Errorf("wait to choose a provider account: %w", err)
	}
	return &Lease{label: "account rotation", file: file}, nil
}
