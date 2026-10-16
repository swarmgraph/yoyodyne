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

import (
	"context"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// sharedTracker answers every listing of one reading from a single listing of
// every item, and asks the tracker what is ready once. Both are started the
// first time either is wanted, or earlier by start, and every caller waits for
// the same answer; a caller whose own context ends first stops waiting without
// stopping the read.
type sharedTracker struct {
	tracker Tracker
	bound   func(context.Context) (context.Context, context.CancelFunc)

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
	shared := &sharedTracker{tracker: s.Tracker, bound: s.bounded}
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
