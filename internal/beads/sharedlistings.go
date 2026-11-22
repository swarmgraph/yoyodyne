package beads

import (
	"context"
	"slices"
	"sync"
	"time"
)

// The open and ready listings are shared, and any write clears them.
//
// Every bd call opens the whole store, and while the store carries a journal
// that costs from under a second to several seconds a call, so a listing that
// several callers each ask for in the same half minute is several of those
// costs for one answer (docs/designs/tracker-store-size.md, build piece 10). So
// this process keeps one listing of the unfinished items and one of what is
// ready, for each store, and every caller of either is answered from it while it
// is younger than sharedListingFresh and nothing this process wrote since has
// cleared it: the scheduler's pull, reconcile, status, the dashboard and the
// recurring passes all ask through here.
//
// The listing of open work is bd's listing of every unfinished item (`bd list`
// with no status, ListUnfinished), and a listing of one unfinished status, or of
// several, is read out of it item for item: it is the same verb writing the
// same fields, and bd lists every status but closed in it
// (TestUnfinishedAndChangedClosedListingConformance). A listing that names
// closed work, or every status, is not shared and asks bd itself.
//
// Any bd command this client runs that is not a read clears both listings for
// its store, before it runs and again once it has ended, so a listing begun
// while a write was under way is handed to the callers waiting for it and never
// kept. A write made by another process does not reach this one's listings,
// which is why neither is kept past sharedListingFresh. Nothing is decided from
// a shared listing on the way to a write: a claim reads its item afresh first
// (claimWouldMark), the claim past a stale blocked status lists what is
// unfinished afresh (live), and the callers that give a claim back or clear an
// assignee read the item they are about to change first.
//
// What is kept is the bytes bd wrote, so each caller decodes its own copy and
// no caller can change another's items. A listing that fails is not kept: its
// failure is handed to the callers that waited for it, and the next caller asks
// again.

// sharedListingFresh is how old a shared listing may be and still be handed
// to a caller.
const sharedListingFresh = 30 * time.Second

// The two listings that are shared.
const (
	sharedOpenListing  = "shared:open"
	sharedReadyListing = "shared:ready"
)

// sharedListing is one kept listing and the read of it under way, if any.
type sharedListing struct {
	kept       bool
	generation uint64
	takenAt    time.Time
	data       []byte
	reading    *sharedListingRead
}

// sharedListingRead is a read of a shared listing under way, and what the
// callers waiting for it are answered with.
type sharedListingRead struct {
	done       chan struct{}
	generation uint64
	data       []byte
	err        error
	// asked is the context the read runs under. A read that ended because its
	// caller gave up says nothing about the tracker, so a waiter that has not
	// given up reads again rather than inheriting it.
	asked context.Context
}

var (
	sharedListingsMu sync.Mutex
	// sharedListingGenerations counts, for each store, the writes that have
	// cleared its listings. A listing kept under an older count is cleared.
	sharedListingGenerations = map[string]uint64{}
	sharedListings           = map[listingKey]*sharedListing{}
)

// sharedListingKeyFor is the key of the shared listing named, and false for a
// client that does not share: one that cannot name its store, one reading a
// measurement's copy of the store, whose listings are what it measures, the
// rebuild's, which reads the store it is rebuilding, and one made to read
// afresh (live).
func (c Client) sharedListingKeyFor(name string) (listingKey, bool) {
	if c.unshared || c.beadsDir != "" || c.storeLocked {
		return listingKey{}, false
	}
	return c.sharedListingKey([]string{name})
}

// sharedListingData answers the listing named from the shared one where it is
// kept and fresh, waits for the read under way where there is one, and
// otherwise reads it with read and keeps what it read. shared is false for a
// client that does not share, which then makes the listing itself.
func (c Client) sharedListingData(ctx context.Context, name string, read func(Client, context.Context) ([]byte, error)) (data []byte, shared bool, err error) {
	key, shares := c.sharedListingKeyFor(name)
	if !shares {
		return nil, false, nil
	}
	for {
		sharedListingsMu.Lock()
		generation := sharedListingGenerations[key.store]
		entry := sharedListings[key]
		if entry == nil {
			entry = &sharedListing{}
			sharedListings[key] = entry
		}
		now := c.listingNow()
		if entry.kept && entry.generation == generation && !now.Before(entry.takenAt) && now.Sub(entry.takenAt) < sharedListingFresh {
			data := entry.data
			sharedListingsMu.Unlock()
			return data, true, nil
		}
		if reading := entry.reading; reading != nil && reading.generation == generation {
			sharedListingsMu.Unlock()
			select {
			case <-reading.done:
			case <-ctx.Done():
				return nil, true, ctx.Err()
			}
			if reading.err != nil && reading.asked.Err() != nil && ctx.Err() == nil {
				continue
			}
			return reading.data, true, reading.err
		}
		reading := &sharedListingRead{done: make(chan struct{}), generation: generation, asked: ctx}
		entry.reading = reading
		sharedListingsMu.Unlock()

		// The read is keyed below this by its generation as well, so a read
		// begun after a write never joins one begun before it (runListing).
		reader := c
		reader.listingGeneration = generation + 1
		takenAt := c.listingNow()
		reading.data, reading.err = read(reader, ctx)

		sharedListingsMu.Lock()
		if entry.reading == reading {
			entry.reading = nil
		}
		if reading.err == nil && sharedListingGenerations[key.store] == generation {
			entry.kept, entry.generation, entry.takenAt, entry.data = true, generation, takenAt, reading.data
		}
		sharedListingsMu.Unlock()
		close(reading.done)
		return reading.data, true, reading.err
	}
}

// clearSharedListings clears the shared listings of this client's store, which
// every bd command that is not a read does before it runs and once it has
// ended. A client that cannot name its store shares nothing, so has nothing to
// clear.
func (c Client) clearSharedListings() {
	if c.Dir == "" {
		return
	}
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return
	}
	sharedListingsMu.Lock()
	sharedListingGenerations[store]++
	sharedListingsMu.Unlock()
}

func (c Client) listingNow() time.Time {
	if c.listingClock != nil {
		return c.listingClock()
	}
	return time.Now()
}

// sharedOpen is every unfinished item, from the shared listing of open work,
// and shared is false for a client that does not share. The caller that makes
// the listing records how it ended (recordListing); one answered from it
// records nothing, because it learned nothing new about the tracker.
func (c Client) sharedOpen(ctx context.Context) (items []WorkItem, shared bool, err error) {
	data, shared, err := c.sharedListingData(ctx, sharedOpenListing, func(reader Client, ctx context.Context) ([]byte, error) {
		data, err := reader.listDataPatiently(ctx, nil)
		if err == nil {
			_, err = decodeWorkListing(data, "list", reader.writerZone())
		}
		reader.recordListing(ctx, err)
		return data, err
	})
	if !shared || err != nil {
		return nil, shared, err
	}
	items, err = decodeWorkListing(data, "list", c.writerZone())
	return items, true, err
}

// sharedReady is the tracker's account of what is ready, from the shared
// listing of it, recorded as sharedOpen is.
func (c Client) sharedReady(ctx context.Context) (items []WorkItem, shared bool, err error) {
	data, shared, err := c.sharedListingData(ctx, sharedReadyListing, func(reader Client, ctx context.Context) ([]byte, error) {
		data, err := reader.workListingData(ctx, "ready", nil)
		if err == nil {
			_, err = decodeWorkListing(data, "ready", reader.writerZone())
		}
		reader.recordListing(ctx, err)
		return data, err
	})
	if !shared || err != nil {
		return nil, shared, err
	}
	items, err = decodeWorkListing(data, "ready", c.writerZone())
	return items, true, err
}

// sharedOpenHolding is the unfinished items holding any of statuses, read out
// of the shared listing of open work, and shared is false where the client
// does not share or a status named is closed, which that listing does not hold.
func (c Client) sharedOpenHolding(ctx context.Context, statuses []string) (items []WorkItem, shared bool, err error) {
	if slices.Contains(statuses, statusClosed) {
		return nil, false, nil
	}
	every, shared, err := c.sharedOpen(ctx)
	if !shared || err != nil {
		return nil, shared, err
	}
	matching := make([]WorkItem, 0, len(every))
	for _, item := range every {
		if slices.Contains(statuses, item.Status) {
			matching = append(matching, item)
		}
	}
	return matching, true, nil
}
