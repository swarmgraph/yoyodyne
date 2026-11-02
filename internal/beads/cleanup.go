package beads

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// Cleaning up the tracker's store.
//
// bd keeps every change in its Dolt store and never reclaims what later
// changes made unreachable on its own, so a store nobody cleans up grows
// without bound: the primary checkout's had every change since August in one
// 885 MB file, and every bd process opens the whole store, so every call paid
// for it and every wait behind bd's lock was longer
// (docs/diagnoses/yoyodyne-6u7-tracker-timeouts.md). `bd gc --skip-decay` is
// the cleanup: it deletes no item (the decay phase it skips is what would), it
// squashes no commit (its compaction phase only counts them, and `bd flatten`
// is the verb that squashes), and it runs Dolt's garbage collection, which
// rewrites the store keeping every chunk the history still reaches. So a
// listing, an item's text, and an item's history read the same after it as
// before (TestStoreCleanupConformance).
//
// The cleanup takes bd's exclusive lock on the store like every other call,
// and every call that arrives meanwhile waits behind it. Two things keep that
// wait inside the limit those calls are given. It does not start while any
// other process has the store open, so it never begins in the middle of
// somebody else's call; and it is given a hold far shorter than a call's limit
// and stopped when the hold runs out, so a call that arrives the moment it
// starts still has most of its limit left once it is stopped. Being stopped
// leaves the store whole: Dolt's collection writes what it keeps to new files
// and swaps them in by rewriting one manifest, so a collection stopped before
// the swap leaves the old store as it was, and one stopped after has finished.
//
// Nothing before the cleanup, or while it holds the store, lists or counts
// work items. A listing of every item is what an oversized store cannot answer
// within its limit, and an oversized store is exactly the one that needs
// cleaning up, so a cleanup that waited on one would never run. What it reads
// before it starts is the store's size on disk, which asks bd nothing. Once
// bd's cleanup has ended, the store's work items are counted, every status
// included, so the record says what the store held after it; a count that
// cannot be taken is said as unknown, with why, and the cleanup still stands.
// That bd's cleanup deletes no item rests on what it is (above) and on
// TestStoreCleanupConformance, not on a count either side.

// StoreCleanup is one cleanup of the tracker's store: what it found and what it
// came to.
type StoreCleanup struct {
	// Store is the database directory cleaned up.
	Store string
	// Busy is the processes that had the store open, where they stopped the
	// cleanup from starting. Nothing ran, and the sizes are the one reading.
	Busy []StoreHolder
	// StartedAt is when bd was started, and Took how long it ran.
	StartedAt time.Time
	Took      time.Duration
	// SizeBefore and SizeAfter are the database directory's size in bytes either
	// side of the cleanup.
	SizeBefore int64
	SizeAfter  int64
	// Stopped is a cleanup stopped at its hold, and Said what bd's stopped call
	// said about it, including what held the store at that moment.
	Stopped bool
	Said    string
	// ItemsAfter is how many work items the store held once bd's cleanup had
	// ended, every status counted, and Counted says it was taken. NotCounted is
	// why it was not, where bd ended and the count was tried.
	ItemsAfter int
	Counted    bool
	NotCounted string
}

// Ran reports a cleanup that started, whether it finished or was stopped.
func (s StoreCleanup) Ran() bool { return len(s.Busy) == 0 && !s.StartedAt.IsZero() }

// StoreSize is the tracker's store as this client's bd finds it, and its
// database directory's size in bytes.
func (c Client) StoreSize() (string, int64, error) {
	store := c.storeDatabase()
	if store == "" {
		return "", 0, fmt.Errorf("no tracker store was found from %s", describeDir(c.Dir))
	}
	size, err := directorySize(store)
	if err != nil {
		return store, 0, fmt.Errorf("measure the tracker's store %s: %w", store, err)
	}
	return store, size, nil
}

// CleanUpStore runs bd's cleanup over the store for at most hold, unless
// another process has the store open, in which case it starts nothing and says
// which. A cleanup stopped at its hold is not an error: it is reported as
// stopped, with the sizes either side and the items counted after it, and is
// the next cleanup's to finish. A finder that cannot tell who has the store
// open does not hold the cleanup back, since the hold alone keeps the wait it
// causes inside every other call's limit.
//
// Nothing is listed or counted before bd's cleanup starts: see the comment at
// the top of this file. The items are counted once it has ended, and a count
// that fails is said in NotCounted rather than returned as an error. A cleanup
// that fails is returned with what was measured of it and the error; its
// StartedAt says it ran.
func (c Client) CleanUpStore(ctx context.Context, hold time.Duration) (StoreCleanup, error) {
	if hold <= 0 {
		return StoreCleanup{}, errors.New("a cleanup of the tracker's store needs a hold longer than zero")
	}
	store, before, err := c.StoreSize()
	cleanup := StoreCleanup{Store: store, SizeBefore: before}
	if err != nil {
		return cleanup, err
	}
	lookup, cancel := context.WithTimeout(ctx, holderLookupTimeout)
	holders, holdersErr := c.holdersFinder().Holders(lookup, store)
	cancel()
	if holdersErr == nil && len(holders) > 0 {
		cleanup.Busy, cleanup.SizeAfter = holders, before
		return cleanup, nil
	}
	cleanup.StartedAt = time.Now()
	_, runErr := c.runWithin(ctx, hold, "gc", "--skip-decay", "--force")
	cleanup.Took = time.Since(cleanup.StartedAt)
	failed := false
	switch {
	case runErr == nil:
	case timedOut(runErr) && ctx.Err() == nil:
		cleanup.Stopped, cleanup.Said = true, runErr.Error()
	default:
		failed = true
	}
	after, err := directorySize(store)
	if err != nil {
		cleanup.SizeAfter = before
		if failed {
			return cleanup, runErr
		}
		return cleanup, fmt.Errorf("measure the tracker's store %s after its cleanup: %w", store, err)
	}
	cleanup.SizeAfter = after
	if failed {
		return cleanup, runErr
	}
	items, err := c.storeItems(ctx)
	if err != nil {
		cleanup.NotCounted = oneline.Fold(err.Error(), maxMeasureSaid)
		return cleanup, nil
	}
	cleanup.ItemsAfter, cleanup.Counted = items, true
	return cleanup, nil
}

// MissingItems is which of ids the store does not hold. Each is read on its
// own, once and with no retry, rather than in a listing of every item, which
// an oversized store cannot answer in time; it stops at the first id found
// missing, since one missing is answer enough for the caller. It is how the
// maintenance pass looks again for the items an earlier cleanup, which
// compared listings either side of it, found missing.
func (c Client) MissingItems(ctx context.Context, ids []string) ([]string, error) {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for _, id := range sorted {
		if err := validateIssueID(id); err != nil {
			return nil, err
		}
		_, err := c.showOnce(ctx, id)
		switch {
		case errors.Is(err, ErrNoSuchWorkItem):
			return []string{id}, nil
		case err != nil:
			return nil, err
		}
	}
	return nil, nil
}

// storeItems is how many work items the store holds, closed work included,
// read in one listing and not asked again where it does not answer: a count
// taken after a cleanup is a record, not something to keep the store busy
// for.
func (c Client) storeItems(ctx context.Context) (int, error) {
	items, err := c.workListing(ctx, "list", []string{everyStatus})
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

// directorySize is the bytes held by the regular files under dir.
func directorySize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A file the cleanup removed between the listing and the look is a
			// file that no longer takes any room.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
