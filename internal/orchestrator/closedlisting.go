package orchestrator

// Which work items are closed, read once for a whole sweep.
//
// Two steps of the reconcile sweep need the tracker's closed work: the check of
// runs recorded as merged against their target branch (FindMergeDisagreements)
// and the closing of docket entries whose item has closed. Asked separately,
// that is two listings of every closed item the tracker holds, each one a bd
// process that waits for and then holds the store's one exclusive lock, which
// every other bd process on the machine is queued behind as well. A listing
// that waits past its limit behind them is what failed the sweep
// (docs/diagnoses/yoyodyne-ifd-428-99-11-clean-up-closed-items-read.md), so the
// sweep asks once and both steps read the same answer.
//
// The answer is a few minutes old by the time the later step reads it. An item
// that closed in between is read as open by that step, which leaves its docket
// entry standing for the next sweep to close: that is the cost of asking once,
// and it is a cost the next pass pays back.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// ClosedListingTracker is the tracker a ClosedListing reads through: a listing
// by status, and one item, which is how the items a cut listing left out are
// read. beads.Client is one.
type ClosedListingTracker interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
	Show(ctx context.Context, id string) (beads.WorkItem, error)
}

// ClosedListing is a tracker whose listing of closed work is read at most once
// and then answered from what was read. Every other listing, and every item, is
// asked of the tracker as it would have been. A listing that failed is not kept,
// so the next step that needs it asks again; a listing bd cut at its default
// limit is kept as the cut it was, because asking again would be cut the same.
type ClosedListing struct {
	Tracker ClosedListingTracker

	mu    sync.Mutex
	read  bool
	items []beads.WorkItem
	cut   *beads.ListingCut
}

// List answers a listing of closed work from the one reading, taking it on the
// first call, and passes any other status to the tracker.
func (l *ClosedListing) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	if strings.TrimSpace(status) != "closed" {
		return l.Tracker.List(ctx, status)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.read {
		items, err := l.Tracker.List(ctx, status)
		var cut *beads.ListingCut
		switch {
		case err == nil:
		case errors.As(err, &cut):
		default:
			return nil, err
		}
		l.read, l.items, l.cut = true, items, cut
	}
	if l.cut != nil {
		return nil, &beads.ListingCut{Verb: l.cut.Verb, Items: append([]beads.WorkItem(nil), l.cut.Items...)}
	}
	return append([]beads.WorkItem(nil), l.items...), nil
}

// Show reads one item from the tracker.
func (l *ClosedListing) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	return l.Tracker.Show(ctx, id)
}

// ClosedItemsUnread is a sweep step that could not read which work items are
// closed, and so did nothing of what it needed them for. It is a type so that
// the sweep can tell this failure from the others it joins and report it on its
// first occurrence (reconcile's reportUnreadClosedItems), rather than leaving it
// in the pass's record until the pass has failed often enough to be raised.
type ClosedItemsUnread struct {
	// Need is what the step read them for, as the clause after "to".
	Need string
	Err  error
}

func (e *ClosedItemsUnread) Error() string {
	return fmt.Sprintf("read which work items are closed to %s: %v", e.Need, e.Err)
}

func (e *ClosedItemsUnread) Unwrap() error { return e.Err }

// UnreadClosedItems is every ClosedItemsUnread in an error, however it was
// joined or wrapped, outermost first.
func UnreadClosedItems(err error) []*ClosedItemsUnread {
	if err == nil {
		return nil
	}
	if unread, ok := err.(*ClosedItemsUnread); ok {
		return []*ClosedItemsUnread{unread}
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		var found []*ClosedItemsUnread
		for _, inner := range wrapped.Unwrap() {
			found = append(found, UnreadClosedItems(inner)...)
		}
		return found
	case interface{ Unwrap() error }:
		return UnreadClosedItems(wrapped.Unwrap())
	}
	return nil
}
