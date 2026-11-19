package beads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// storeLockName is the lock every bd call takes shared and the rebuild of the
// store (storerebuild.go) takes exclusive, beside the locks each item's writes
// and the listings queue on. It is inside the store's directory and not its
// database, so it survives the database being replaced.
//
// storeEntryName is the lock in front of it. An ordinary call passes through it
// shared on its way to the store lock and lets go as soon as it has the store
// lock; the rebuild takes it exclusive and keeps it. That is what lets the
// rebuild get the store lock on a busy factory: without it, the operating
// system hands a shared lock to each new call while older ones still hold
// theirs, so calls that overlap one another keep the store lock shared without
// a gap, and a rebuild waiting for it exclusive waits for ever. With it, once
// the rebuild is waiting, a new call waits behind it, the calls already in
// flight finish, and the rebuild goes next.
const (
	storeLockName  = "store.lock"
	storeEntryName = "store-entry.lock"
)

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
	entry, err := root.OpenAppend(writeLockDirectory+"/"+storeEntryName, 0o600, 0o700)
	if err != nil {
		return nil, fmt.Errorf("lock the tracker's store: %w", err)
	}
	file, err := root.OpenAppend(writeLockDirectory+"/"+storeLockName, 0o600, 0o700)
	if err != nil {
		_ = entry.Close()
		return nil, fmt.Errorf("lock the tracker's store: %w", err)
	}
	release := func(files ...*os.File) {
		for _, each := range files {
			_ = unlockWriteFile(each)
			_ = each.Close()
		}
	}
	waiting, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	stopped := func(err error, path string) error {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			held := c.whoHeld("the store lock", filepath.Join(store, writeLockDirectory, path))
			if !shared {
				return storeBusy{fmt.Errorf("the tracker calls already running did not finish within %s, so the store lock could not be taken (%s): %w", c.timeout(), held, err)}
			}
			return storeBusy{fmt.Errorf("the store was being rebuilt for longer than %s, so this call was not made (%s): %w", c.timeout(), held, err)}
		}
		return fmt.Errorf("lock the tracker's store: %w", err)
	}
	if err := lockStoreFile(waiting, entry, shared); err != nil {
		_ = entry.Close()
		_ = file.Close()
		return nil, stopped(err, storeEntryName)
	}
	if err := lockStoreFile(waiting, file, shared); err != nil {
		release(entry)
		_ = file.Close()
		return nil, stopped(err, storeLockName)
	}
	if shared {
		release(entry)
		return func() { release(file) }, nil
	}
	return func() { release(file, entry) }, nil
}
