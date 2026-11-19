package beads

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// storeLockName is the lock every bd call takes shared and the rebuild of the
// store (storerebuild.go) takes exclusive, beside the locks each item's writes
// and the listings queue on. It is inside the store's directory and not its
// database, so it survives the database being replaced.
const storeLockName = "store.lock"

// storeBusy is a call that never got the store lock within its bound, because
// the store was being rebuilt. Like a listing that waited too long, it is asked
// again after the listing waits, since the rebuild will have finished by then.
type storeBusy struct{ error }

func (b storeBusy) Unwrap() error        { return b.error }
func (b storeBusy) Is(target error) bool { return target == ErrUnanswered }

// lockStore takes the store lock, shared or exclusive, waiting for as long as
// one bd call may run, and returns what releases it. Where no store is found
// there is nothing to lock and bd is left to say so itself.
func (c Client) lockStore(ctx context.Context, shared bool) (func(), error) {
	if c.Dir == "" || c.storeLocked {
		return func() {}, nil
	}
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return func() {}, nil
	}
	root, err := repowrite.NewRoot(store)
	if err != nil {
		return nil, fmt.Errorf("lock the tracker's store: %w", err)
	}
	file, err := root.OpenAppend(writeLockDirectory+"/"+storeLockName, 0o600, 0o700)
	if err != nil {
		return nil, fmt.Errorf("lock the tracker's store: %w", err)
	}
	waiting, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	if err := lockStoreFile(waiting, file, shared); err != nil {
		_ = file.Close()
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			held := c.whoHeld("the store lock", filepath.Join(store, writeLockDirectory, storeLockName))
			return nil, storeBusy{fmt.Errorf("the store was being rebuilt for longer than %s, so this call was not made (%s): %w", c.timeout(), held, err)}
		}
		return nil, fmt.Errorf("lock the tracker's store: %w", err)
	}
	return func() {
		_ = unlockWriteFile(file)
		_ = file.Close()
	}, nil
}
