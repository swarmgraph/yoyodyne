package orchestrator

// One pull reads each tracker listing once.
//
// Every bd command opens the whole store under one exclusive lock, reads
// included, and what a command costs is opening the store rather than the size
// of its answer (docs/diagnoses/yoyodyne-ifd-428-99-11-clean-up-closed-items-read.md).
// A watch pulls on every poll, and each pull used to make four commands one
// after another: the claimed items for the claim audit, the open and the
// blocked items for the queue, and the tracker's account of what is ready.
// Those listings were most of what held the store when other callers' tracker
// calls ran out their limits on October 17 and 18.
//
// So a pull makes its listings through one shared reading. The three statuses
// it reads are asked for in one command where the tracker can list several at
// once (beads.Client.ListStatuses), and each status is read out of that answer;
// readiness is asked once. Every part of the pull that asks is given the same
// answer, failures included: a listing that ran out its attempts is not asked
// again in the same pull, because asking again is another turn in the queue
// that made it fail, and the session already reads again after its wait, with
// a new pull and new listings. So a pull holds the store for two commands at
// most, whatever its parts ask for.
//
// What this gives up is freshness inside one pull. An item the claim audit
// releases is still read as claimed by the queue of the same pull, and is
// pulled from the next one, a poll later.

import (
	"context"
	"slices"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// statusesLister is a tracker that can list several statuses in one command.
// beads.Client is one. A tracker that is not is asked once per status instead.
type statusesLister interface {
	ListStatuses(ctx context.Context, statuses []string) ([]beads.WorkItem, error)
}

// cycleStatuses are the statuses one pull lists: what the queue is assembled
// from and what has left it by being pulled.
func cycleStatuses() []string {
	return append(append([]string(nil), scheduledStatuses...), claimedStatus)
}

// cycleListings is one pull's reading of the tracker's listings, kept for
// every part of the pull that asks.
type cycleListings struct {
	tracker ScheduleTracker

	mu sync.Mutex
	// unfinished is the one listing of every status in cycleStatuses, where the
	// tracker can make it.
	unfinished *cycleRead
	// byStatus is each status's own listing, where it cannot.
	byStatus map[string]*cycleRead
	ready    *cycleRead
}

// cycleRead is one tracker command's answer. done is closed once it is in, so
// a caller that arrives while it is being read waits for it rather than asking
// again.
type cycleRead struct {
	done  chan struct{}
	items []beads.WorkItem
	err   error
}

// sharingListings is the pull with its listings shared for the rest of it.
func (p Pull) sharingListings() Pull {
	if p.Tracker != nil {
		p.listings = &cycleListings{
			tracker:    p.Tracker,
			unfinished: &cycleRead{},
			byStatus:   map[string]*cycleRead{},
			ready:      &cycleRead{},
		}
	}
	return p
}

// list is the work items holding status, from the pull's shared reading where
// it has one.
func (p Pull) list(ctx context.Context, status string) ([]beads.WorkItem, error) {
	if p.listings == nil {
		return p.Tracker.List(ctx, status)
	}
	return p.listings.list(ctx, status)
}

// ready is the tracker's account of what can be pulled, from the pull's shared
// reading where it has one.
func (p Pull) ready(ctx context.Context) ([]beads.WorkItem, error) {
	if p.listings == nil {
		return p.Tracker.Ready(ctx)
	}
	items, err := p.listings.read(ctx, p.listings.ready, p.listings.tracker.Ready)
	return append([]beads.WorkItem(nil), items...), err
}

func (c *cycleListings) list(ctx context.Context, status string) ([]beads.WorkItem, error) {
	lister, several := c.tracker.(statusesLister)
	if !several || !slices.Contains(cycleStatuses(), status) {
		c.mu.Lock()
		slot := c.byStatus[status]
		if slot == nil {
			slot = &cycleRead{}
			c.byStatus[status] = slot
		}
		c.mu.Unlock()
		items, err := c.read(ctx, slot, func(ctx context.Context) ([]beads.WorkItem, error) { return c.tracker.List(ctx, status) })
		return append([]beads.WorkItem(nil), items...), err
	}
	items, err := c.read(ctx, c.unfinished, func(ctx context.Context) ([]beads.WorkItem, error) {
		return lister.ListStatuses(ctx, cycleStatuses())
	})
	if err != nil {
		return nil, err
	}
	matching := make([]beads.WorkItem, 0, len(items))
	for _, item := range items {
		if item.Status == status {
			matching = append(matching, item)
		}
	}
	return matching, nil
}

// read answers from the slot, making the command the first time it is asked.
// The command runs under the asking caller's context, as it did when every
// caller made its own; a caller that arrives while it is running waits for its
// answer, or for its own context to end.
func (c *cycleListings) read(ctx context.Context, read *cycleRead, command func(context.Context) ([]beads.WorkItem, error)) ([]beads.WorkItem, error) {
	c.mu.Lock()
	first := read.done == nil
	if first {
		read.done = make(chan struct{})
	}
	c.mu.Unlock()
	if first {
		read.items, read.err = command(ctx)
		close(read.done)
		return read.items, read.err
	}
	select {
	case <-read.done:
		return read.items, read.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
