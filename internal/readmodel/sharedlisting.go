package readmodel

// One standing reads the tracker once for its listings.
//
// A standing used to make five tracker commands one after another: the open
// items, the blocked items, the tracker's account of what is ready, the claimed
// items, and every item for the titles. Every bd process opens the whole store
// under one exclusive lock, so each command paid the store's opening cost and
// queued behind whatever else held the lock, and the five together were most of
// what a standing cost. A listing of every item carries every field a narrower
// listing carries, item for item, so the narrow listings are read out of the
// one wide one, and the wide one and the readiness query — the only two
// commands left — are started together, at the top of the reading, so they run
// beside every record it reads from disk.
//
// Where the sources carry the product's shared reading (TrackerReadings), the
// two commands are not this reading's own: every status reading on the machine
// shares one made within sharedReadingFresh, and the callers that arrive while
// one is being made wait for it (internal/runstate, trackerreading.go). That is
// what keeps `yoyo status`, the dashboard, Slack, and the passes from each
// holding the tracker's store for the same two listings.
//
// The shared reading does not list every item each time it is made. Closed work
// is most of the store and almost none of what changes, and a listing of it on
// every refresh — the dashboard makes one every ten seconds — held the store for
// the whole of it six times a minute. So a reading lists every item, closed work
// included, only where no reading is kept to carry forward from or the last
// whole listing is wholeReadingEvery old. Between whole listings a reading lists
// the unfinished items, and carries the closed items forward from the reading
// before it; it asks the tracker for the closed items changed since the last
// time it did only where an item it had listed as unfinished is no longer
// listed so, or closedRefresh has passed (readEvery).

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// sharedReadingFresh is how old a shared reading may be and still be handed to
// a status reading. It is the dashboard's own interval between standings, so a
// reading shared at it is never staler than the page that shows it already is.
const sharedReadingFresh = 10 * time.Second

// wholeReadingEvery is how often the shared reading lists every item, closed
// work included, rather than carrying closed items forward. The listing of
// changed closed work catches every closed item that changed; only an item
// removed from the tracker outright is carried until this listing drops it.
const wholeReadingEvery = time.Hour

// closedRefresh is how long a reading carries the closed items forward before
// asking for the closed items changed since, where nothing it had listed as
// unfinished has left the listing to prompt it sooner. An item closed since the
// reading before leaves the unfinished listing, so it is listed in the same
// reading; this bounds how long a later change to work already closed — a cost
// or a note recorded on it — waits to be seen.
const closedRefresh = 2 * time.Minute

// closedOverlap is how far before the last time the closed items were brought
// up to date the listing of changed closed work reaches back. Items listed
// again are only listed again, so the overlap is generous on purpose: it
// covers a clock or zone that disagrees with this one by any number of hours,
// and a change committed a moment after the listing that should have held it.
const closedOverlap = 24 * time.Hour

// incrementalTracker is a tracker that can list its unfinished work and its
// closed work changed since a time apart. It is satisfied by beads.Client. A
// tracker that cannot is listed whole each time.
type incrementalTracker interface {
	ListUnfinished(ctx context.Context) ([]beads.WorkItem, error)
	ListClosedSince(ctx context.Context, since time.Time) ([]beads.WorkItem, error)
}

// sharedReadingWait bounds a wait for the shared reading where the sources set
// no bound of their own: the reading being waited for is bounded by its
// tracker client's own limits, and this only keeps a caller from waiting
// forever on a holder that never lets go.
const sharedReadingWait = 3 * time.Minute

// TrackerReadings is the product's one shared reading of the tracker. It is
// satisfied by *runstate.TrackerReadingStore.
type TrackerReadings interface {
	Share(ctx context.Context, fresh time.Duration, read func(kept *runstate.TrackerReading) (runstate.TrackerReading, bool)) (runstate.TrackerReading, error)
}

// sharedTracker answers every listing of one reading from a single listing of
// every item, and asks the tracker what is ready once. Both are started the
// first time either is wanted, or earlier by start, and every caller waits for
// the same answer; a caller whose own context ends first stops waiting without
// stopping the read.
type sharedTracker struct {
	tracker  Tracker
	bound    func(context.Context) (context.Context, context.CancelFunc)
	readings TrackerReadings
	wait     time.Duration
	now      func() time.Time

	once  sync.Once
	every sharedRead
	ready sharedRead
}

// sharedRead is one tracker command's answer, kept for every caller.
type sharedRead struct {
	done  chan struct{}
	items []beads.WorkItem
	err   error
}

// sharing is the sources with their tracker answering from one listing and one
// readiness query. A source with no tracker, or one already shared, is returned
// as it is.
func (s Sources) sharing() (Sources, *sharedTracker) {
	if s.Tracker == nil {
		return s, nil
	}
	if shared, ok := s.Tracker.(*sharedTracker); ok {
		return s, shared
	}
	shared := &sharedTracker{tracker: s.Tracker, bound: s.bounded, readings: s.TrackerReadings, wait: sharedReadingWait, now: s.now}
	// A caller waiting for the shared reading waits out the one being made,
	// which its own bound ends, and then may have to make its own.
	if s.TrackerTimeout > 0 {
		shared.wait = 2 * s.TrackerTimeout
	}
	s.Tracker = shared
	return s, shared
}

// start begins both tracker commands, each under its own bound and under the
// reading's context, so that they run beside everything else the reading
// does. It is safe to call more than once.
func (t *sharedTracker) start(ctx context.Context) {
	if t == nil {
		return
	}
	t.once.Do(func() {
		t.every.done = make(chan struct{})
		t.ready.done = make(chan struct{})
		if t.readings != nil {
			go t.share(ctx)
			return
		}
		go t.every.read(ctx, t.bound, func(ctx context.Context) ([]beads.WorkItem, error) { return t.tracker.List(ctx, "") })
		go t.ready.read(ctx, t.bound, t.tracker.Ready)
	})
}

// finish waits for both tracker commands to end, so that nothing a reading
// started is still running — and still recording how the tracker answered —
// after the reading has returned. A reading that stopped early on one failed
// command waits out the other, which its own bound ends.
func (t *sharedTracker) finish() {
	if t == nil || t.every.done == nil {
		return
	}
	<-t.every.done
	<-t.ready.done
}

// share answers both commands from the product's shared reading, making it
// with the listing and the readiness query under way together where no reading
// fresh enough is kept. A reading that could not be had at all costs both
// commands, each saying why;
// one the tracker did not answer is said as the tracker said it.
func (t *sharedTracker) share(ctx context.Context) {
	defer close(t.every.done)
	defer close(t.ready.done)
	waiting, cancel := context.WithTimeout(ctx, t.wait)
	defer cancel()
	reading, err := t.readings.Share(waiting, sharedReadingFresh, func(kept *runstate.TrackerReading) (runstate.TrackerReading, bool) {
		var ready sharedRead
		ready.done = make(chan struct{})
		go ready.read(ctx, t.bound, t.tracker.Ready)
		reading := t.readEvery(ctx, kept)
		<-ready.done
		reading.Ready = ready.items
		if ready.err != nil {
			reading.Ready, reading.ReadyFailure = nil, ready.err.Error()
		}
		// A reading whose own caller gave up says nothing about the tracker, so
		// it is not handed to anybody else.
		return reading, ctx.Err() == nil
	})
	if err != nil {
		err = fmt.Errorf("the tracker reading every status reading shares could not be had: %w", err)
		t.every.err, t.ready.err = err, err
		return
	}
	t.every.items, t.ready.items = reading.Every, reading.Ready
	if reading.EveryFailure != "" {
		t.every.items, t.every.err = nil, errors.New(reading.EveryFailure)
	}
	if reading.ReadyFailure != "" {
		t.ready.items, t.ready.err = nil, errors.New(reading.ReadyFailure)
	}
}

// readEvery is the shared reading's account of every item: a listing of every
// item where nothing can be carried forward from kept, and otherwise the
// unfinished items listed now beside the closed items kept carries, brought up
// to date where something has closed since or they are closedRefresh old. A
// listing that failed is said in EveryFailure, with the closed items it would
// have carried kept in Closed for the reading after it.
func (t *sharedTracker) readEvery(ctx context.Context, kept *runstate.TrackerReading) runstate.TrackerReading {
	started := t.now()
	incremental, canCarry := t.tracker.(incrementalTracker)
	closed, carried := carriedClosed(kept)
	if !canCarry || !carried || kept.ClosedAt.IsZero() || kept.WholeAt.IsZero() || started.Sub(kept.WholeAt) >= wholeReadingEvery {
		items, err := t.boundedList(ctx, func(ctx context.Context) ([]beads.WorkItem, error) { return t.tracker.List(ctx, "") })
		if err != nil {
			return runstate.TrackerReading{EveryFailure: err.Error()}
		}
		return runstate.TrackerReading{Every: items, WholeAt: started, ClosedAt: started}
	}

	reading := runstate.TrackerReading{WholeAt: kept.WholeAt, ClosedAt: kept.ClosedAt}
	unfinished, err := t.boundedList(ctx, incremental.ListUnfinished)
	if err != nil {
		reading.EveryFailure, reading.Closed = err.Error(), closed
		return reading
	}
	listed := make(map[string]bool, len(unfinished))
	for _, item := range unfinished {
		listed[item.ID] = true
	}
	// An item listed unfinished now is not closed whatever was carried for it:
	// it was reopened.
	closed = slices.DeleteFunc(closed, func(item beads.WorkItem) bool { return listed[item.ID] })

	if left(kept, listed) || started.Sub(kept.ClosedAt) >= closedRefresh {
		asked := t.now()
		changed, err := t.boundedList(ctx, func(ctx context.Context) ([]beads.WorkItem, error) {
			return incremental.ListClosedSince(ctx, kept.ClosedAt.Add(-closedOverlap))
		})
		if err != nil {
			reading.EveryFailure, reading.Closed = err.Error(), closed
			return reading
		}
		closed = mergeClosed(closed, changed, listed)
		reading.ClosedAt = asked
	}
	reading.Every = append(unfinished, closed...)
	return reading
}

// boundedList is one tracker command under the reading's bound.
func (t *sharedTracker) boundedList(ctx context.Context, command func(context.Context) ([]beads.WorkItem, error)) ([]beads.WorkItem, error) {
	bounded, cancel := t.bound(ctx)
	defer cancel()
	return command(bounded)
}

// carriedClosed is the closed items kept carries forward, and whether it
// carries any account of them at all: a reading whose listing succeeded carries
// the closed items among every item, and one whose listing failed carries them
// in Closed where it had them to carry.
func carriedClosed(kept *runstate.TrackerReading) ([]beads.WorkItem, bool) {
	if kept == nil {
		return nil, false
	}
	if kept.EveryFailure != "" {
		return slices.Clone(kept.Closed), len(kept.Closed) > 0
	}
	closed := make([]beads.WorkItem, 0, len(kept.Every))
	for _, item := range kept.Every {
		if item.Status == trackerClosed {
			closed = append(closed, item)
		}
	}
	return closed, true
}

// left reports whether an item kept listed as unfinished is not listed so now,
// which is an item closed since — or removed — and so a reason to ask for the
// closed items changed since. A kept reading whose listing failed says nothing
// about what was unfinished, so it is read as something having left.
func left(kept *runstate.TrackerReading, listed map[string]bool) bool {
	if kept.EveryFailure != "" {
		return true
	}
	for _, item := range kept.Every {
		if item.Status != trackerClosed && !listed[item.ID] {
			return true
		}
	}
	return false
}

// mergeClosed is the carried closed items with each changed one in place of
// the carried copy, or added where none was carried, leaving out anything
// listed unfinished now.
func mergeClosed(closed, changed []beads.WorkItem, listed map[string]bool) []beads.WorkItem {
	at := make(map[string]int, len(closed))
	for index, item := range closed {
		at[item.ID] = index
	}
	for _, item := range changed {
		if listed[item.ID] || item.Status != trackerClosed {
			continue
		}
		if index, found := at[item.ID]; found {
			closed[index] = item
			continue
		}
		at[item.ID] = len(closed)
		closed = append(closed, item)
	}
	return closed
}

// trackerClosed is the status the tracker gives finished work.
const trackerClosed = "closed"

func (r *sharedRead) read(ctx context.Context, bound func(context.Context) (context.Context, context.CancelFunc), command func(context.Context) ([]beads.WorkItem, error)) {
	bounded, cancel := bound(ctx)
	defer cancel()
	r.items, r.err = command(bounded)
	close(r.done)
}

func (r *sharedRead) wait(ctx context.Context) ([]beads.WorkItem, error) {
	select {
	case <-r.done:
		return r.items, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// List is the items holding status, read out of the one listing of every
// item; no status is that whole listing. The slice is the caller's own.
func (t *sharedTracker) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	t.start(context.WithoutCancel(ctx))
	items, err := t.every.wait(ctx)
	if err != nil {
		return nil, err
	}
	matching := make([]beads.WorkItem, 0, len(items))
	for _, item := range items {
		if status == "" || item.Status == status {
			matching = append(matching, item)
		}
	}
	return matching, nil
}

// Ready is the tracker's own account of what can be pulled, asked once.
func (t *sharedTracker) Ready(ctx context.Context) ([]beads.WorkItem, error) {
	t.start(context.WithoutCancel(ctx))
	items, err := t.ready.wait(ctx)
	if err != nil {
		return nil, err
	}
	return append([]beads.WorkItem(nil), items...), nil
}
