package beads

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// listingLockName is the lock file, beside the ones each item's writes queue
// on (writelock.go), that one listing holds while bd reads the store.
const listingLockName = "listings.lock"

// listingKey names one listing: the same store, the same bd, the same
// arguments. Two clients that agree on all of it are asking for one answer.
type listingKey struct {
	store   string
	binary  string
	runner  any
	server  bool
	command string
}

// listingFlight is a listing being read, and everything a second request for
// it needs to be answered from that read.
type listingFlight struct {
	done chan struct{}
	data []byte
	err  error
	// asked is the caller whose context the read ran under. A read that ended
	// because that caller gave up says nothing about the tracker, so a waiter
	// that has not given up reads again rather than inheriting it.
	asked context.Context
}

var (
	listingFlightsMu sync.Mutex
	listingFlights   = map[listingKey]*listingFlight{}
)

// sharedListingKey is the key for a listing this client may share, and false
// for a client that cannot name its store or whose runner cannot be compared:
// sharing there could hand one client another's answer.
func (c Client) sharedListingKey(args []string) (listingKey, bool) {
	if c.Dir == "" || c.Runner == nil || !reflect.TypeOf(c.Runner).Comparable() {
		return listingKey{}, false
	}
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return listingKey{}, false
	}
	return listingKey{store: store, binary: c.Binary, runner: c.Runner, server: c.serverMode, command: fmt.Sprint(args)}, true
}

// runListing is one bd listing read under two rules, so the tracker's store,
// which admits one bd process at a time and makes every other wait its turn
// (docs/diagnoses/yoyodyne-ifd-428-126-4-14-1-reconcile-tracker-waits.md), is
// asked for each listing as seldom as possible.
//
// Within this process, a request for a listing already being read waits for
// that read and is answered from it, its failure included: every waiter gets
// the one answer or the one error. Across processes, the read holds an advisory
// lock inside the store, which the operating system drops when its holder
// dies, so no two harness processes have the store open for a listing at once.
// The answer is the bytes bd wrote, so each caller decodes its own copy.
func (c Client) runListing(ctx context.Context, args ...string) ([]byte, error) {
	key, shared := c.sharedListingKey(args)
	if !shared {
		return c.runLocked(ctx, args...)
	}
	for {
		listingFlightsMu.Lock()
		flight, reading := listingFlights[key]
		if !reading {
			flight = &listingFlight{done: make(chan struct{}), asked: ctx}
			listingFlights[key] = flight
		}
		listingFlightsMu.Unlock()
		if !reading {
			flight.data, flight.err = c.runLocked(ctx, args...)
			listingFlightsMu.Lock()
			delete(listingFlights, key)
			listingFlightsMu.Unlock()
			close(flight.done)
			return flight.data, flight.err
		}
		select {
		case <-flight.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if flight.err != nil && flight.asked.Err() != nil && ctx.Err() == nil {
			continue
		}
		return flight.data, flight.err
	}
}

// runLocked is one listing under the store's listing lock. Where no store is
// found there is nothing to lock and bd is left to say so itself.
func (c Client) runLocked(ctx context.Context, args ...string) ([]byte, error) {
	unlock, err := c.lockListings(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return c.run(ctx, args...)
}

// lockListings takes the store's listing lock, waiting for whichever process
// holds it for as long as one bd call may run, and returns what releases it.
func (c Client) lockListings(ctx context.Context) (func(), error) {
	if c.Dir == "" {
		return func() {}, nil
	}
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return func() {}, nil
	}
	root, err := repowrite.NewRoot(store)
	if err != nil {
		return nil, fmt.Errorf("lock listings: %w", err)
	}
	file, err := root.OpenAppend(writeLockDirectory+"/"+listingLockName, 0o600, 0o700)
	if err != nil {
		return nil, fmt.Errorf("lock listings: %w", err)
	}
	waiting, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	if err := lockWriteFile(waiting, file); err != nil {
		_ = file.Close()
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			held := c.whoHeld("the listing lock", filepath.Join(store, writeLockDirectory, listingLockName))
			return nil, busyWrite{fmt.Errorf("another listing held the store for longer than %s, so this one was not made (%s): %w", c.timeout(), held, err)}
		}
		return nil, fmt.Errorf("lock listings: %w", err)
	}
	return func() {
		_ = unlockWriteFile(file)
		_ = file.Close()
	}, nil
}
