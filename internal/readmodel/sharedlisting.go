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

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// sharedReadingFresh is how old a shared reading may be and still be handed to
// a status reading. It is the dashboard's own interval between standings, so a
// reading shared at it is never staler than the page that shows it already is.
const sharedReadingFresh = 10 * time.Second

// sharedReadingWait bounds a wait for the shared reading where the sources set
// no bound of their own: the reading being waited for is bounded by its
// tracker client's own limits, and this only keeps a caller from waiting
// forever on a holder that never lets go.
const sharedReadingWait = 3 * time.Minute

// TrackerReadings is the product's one shared reading of the tracker. It is
// satisfied by *runstate.TrackerReadingStore.
type TrackerReadings interface {
	Share(ctx context.Context, fresh time.Duration, read func() (runstate.TrackerReading, bool)) (runstate.TrackerReading, error)
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
	shared := &sharedTracker{tracker: s.Tracker, bound: s.bounded, readings: s.TrackerReadings, wait: sharedReadingWait}
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
// with both commands under way together where no reading fresh enough is kept.
// A reading that could not be had at all costs both commands, each saying why;
// one the tracker did not answer is said as the tracker said it.
func (t *sharedTracker) share(ctx context.Context) {
	defer close(t.every.done)
	defer close(t.ready.done)
	waiting, cancel := context.WithTimeout(ctx, t.wait)
	defer cancel()
	reading, err := t.readings.Share(waiting, sharedReadingFresh, func() (runstate.TrackerReading, bool) {
		var every, ready sharedRead
		every.done, ready.done = make(chan struct{}), make(chan struct{})
		go every.read(ctx, t.bound, func(ctx context.Context) ([]beads.WorkItem, error) { return t.tracker.List(ctx, "") })
		go ready.read(ctx, t.bound, t.tracker.Ready)
		<-every.done
		<-ready.done
		reading := runstate.TrackerReading{Every: every.items, Ready: ready.items}
		if every.err != nil {
			reading.Every, reading.EveryFailure = nil, every.err.Error()
		}
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
