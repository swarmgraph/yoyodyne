package chat

import (
	"context"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// ancestorReadsMaxAge is how long a parent read for an item's inherited-block
// line is reused. The line reports a state for a reader to weigh and changes
// nothing about readiness, so a parent read a few seconds ago answers it as well
// as one read now; a minute is short enough that a parent another process
// changed is seen on the next read after that.
const ancestorReadsMaxAge = time.Minute

// ancestorReads remembers the parents (and the work they wait on) read for the
// inherited-block line of an item read.
//
// It exists because bd lets one command into its store at a time, so every
// single-item read costs the wait to be let in
// (docs/diagnoses/yoyodyne-ifd-428-126-4-24-slow-listings.md). One read of an
// item walks its whole ancestry, and the walk is made twice and once more for
// each parent's blocker; a pass that reads dozens of items under the same few
// parents then made hundreds of `bd show` calls an hour for the same handful of
// parents, and every listing queued behind them.
//
// A write this session makes drops everything held, so a role that changes a
// parent and reads its child next is never told the parent as it stood before.
// The zero value is ready to use.
type ancestorReads struct {
	mu    sync.Mutex
	items map[string]heldAncestor
}

type heldAncestor struct {
	item beads.WorkItem
	at   time.Time
}

func (a *ancestorReads) get(id string, now time.Time) (beads.WorkItem, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	held, ok := a.items[id]
	if !ok || now.Sub(held.at) >= ancestorReadsMaxAge || now.Before(held.at) {
		delete(a.items, id)
		return beads.WorkItem{}, false
	}
	return held.item, true
}

// put holds an answered read under the id it was asked by. A failed read is
// never held, so it is asked again.
func (a *ancestorReads) put(id string, item beads.WorkItem, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.items == nil {
		a.items = map[string]heldAncestor{}
	}
	a.items[id] = heldAncestor{item: item, at: now}
}

// clear forgets everything held.
func (a *ancestorReads) clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = nil
}

// readAncestor reads one item for an inherited-block line, from what this
// session read a moment ago where it has, and from the tracker where it has not.
func (s *Session) readAncestor(ctx context.Context, id string) (beads.WorkItem, error) {
	now := s.options.clock().Now()
	if item, ok := s.ancestors.get(id, now); ok {
		return item, nil
	}
	item, err := s.options.Tracker.Show(ctx, id)
	if err != nil {
		return beads.WorkItem{}, err
	}
	s.ancestors.put(id, item, now)
	return item, nil
}
