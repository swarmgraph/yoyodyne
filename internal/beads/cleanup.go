package beads

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"time"
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
// The cleanup also counts the work items the store holds either side of it,
// every status included, so each cleanup shows on its own record that it lost
// none. The count before is taken once nobody else has the store open, just
// before bd's cleanup starts, and the count after once it has ended, so another
// process creating an item between the two is the one way the counts differ
// without the cleanup having lost anything; the maintenance pass treats any
// difference as a loss all the same and stops cleaning up until somebody has
// looked (internal/maintain).

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
	// ItemsBefore and ItemsAfter are how many work items the store held either
	// side of the cleanup, every status counted, and Counted says both were
	// taken. Missing is the items it held before and not after.
	ItemsBefore int
	ItemsAfter  int
	Counted     bool
	Missing     []string
}

// Ran reports a cleanup that started, whether it finished or was stopped.
func (s StoreCleanup) Ran() bool { return len(s.Busy) == 0 && !s.StartedAt.IsZero() }

// ItemsChanged reports a cleanup counted either side whose store does not hold
// the work items after it that it held before.
func (s StoreCleanup) ItemsChanged() bool {
	return s.Counted && (s.ItemsBefore != s.ItemsAfter || len(s.Missing) > 0)
}

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
// stopped, with the sizes and item counts either side, and is the next
// cleanup's to finish. A finder that cannot tell who has the store open does
// not hold the cleanup back, since the hold alone keeps the wait it causes
// inside every other call's limit.
//
// The items are counted before bd's cleanup starts, and one that cannot be
// counted is not started, because nothing could then show it lost no item. A
// cleanup that fails, or whose items cannot be counted after it, is returned
// with what was measured of it and the error; its StartedAt says it ran.
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
	itemsBefore, err := c.storeItems(ctx)
	if err != nil {
		return cleanup, fmt.Errorf("the tracker's work items could not be counted before its store was cleaned up, so the cleanup was not started: %w", err)
	}
	cleanup.ItemsBefore = len(itemsBefore)
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
	itemsAfter, err := c.storeItems(ctx)
	if err != nil {
		return cleanup, fmt.Errorf("the tracker's work items could not be counted after its store was cleaned up, so whether the cleanup lost any cannot be told: %w", err)
	}
	cleanup.ItemsAfter, cleanup.Counted = len(itemsAfter), true
	for id := range itemsBefore {
		if _, kept := itemsAfter[id]; !kept {
			cleanup.Missing = append(cleanup.Missing, id)
		}
	}
	sort.Strings(cleanup.Missing)
	return cleanup, nil
}

// storeItems is the identifier of every work item the store holds, closed work
// included, read in one listing and not asked again where it does not answer:
// a count that fails is the cleanup failing, said as that, rather than a wait
// that keeps the store busy longer.
func (c Client) storeItems(ctx context.Context) (map[string]struct{}, error) {
	items, err := c.workListing(ctx, "list", []string{everyStatus})
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(items))
	for _, item := range items {
		ids[item.ID] = struct{}{}
	}
	return ids, nil
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
