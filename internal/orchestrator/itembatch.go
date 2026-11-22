package orchestrator

import (
	"context"
	"maps"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// itemBatchReader is a tracker that reads several work items in one call. It
// is satisfied by beads.Client, whose ShowMany answers an id it holds no item
// under by leaving it out, and by HarnessRunItems over one.
//
// bd lets one command into its store at a time, reads included, so a caller
// that reads many items on a busy store waits once for each read; reading them
// together waits once
// (docs/diagnoses/yoyodyne-ifd-428-126-4-24-slow-listings.md).
type itemBatchReader interface {
	ShowMany(ctx context.Context, ids []string) (map[string]beads.WorkItem, error)
}

// ShowMany reads the items named together: a bookkeeping item of the harness's
// own from its run's record, as Show does, and the rest in one batched read of
// the tracker it carries where that tracker can make one. An item it could not
// answer is left out, as beads.Client.ShowMany leaves out an item the tracker
// does not hold, and a caller reads that one with Show, which says why.
func (t HarnessRunItems) ShowMany(ctx context.Context, ids []string) (map[string]beads.WorkItem, error) {
	read := make(map[string]beads.WorkItem, len(ids))
	var tracked []string
	for _, id := range ids {
		state, ours, err := t.landing(id)
		if !ours {
			tracked = append(tracked, id)
			continue
		}
		if err == nil {
			read[id] = t.item(state, outsideFileItemStatus(state))
		}
	}
	batch, ok := t.WorkTracker.(itemBatchReader)
	if len(tracked) == 0 || !ok {
		return read, nil
	}
	items, err := batch.ShowMany(ctx, tracked)
	if err != nil {
		return nil, err
	}
	maps.Copy(read, items)
	return read, nil
}

// The watch's queue reads through HarnessRunItems over beads.Client; were
// either to stop reading several items at once, the queue would go back to a
// read for each entry without anything failing, so the build says so instead.
var (
	_ itemBatchReader = beads.Client{}
	_ itemBatchReader = HarnessRunItems{}
)

// List is the wrapped tracker's own listing, so a sweep given this tracker still
// lists the closed items in one read rather than finding it cannot list and
// reading each item on its own. A bookkeeping item of the harness's own is
// never in a listing: the tracker does not hold it. Over a tracker that cannot
// list, it lists nothing.
func (t HarnessRunItems) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	lister, ok := t.WorkTracker.(closedItemLister)
	if !ok {
		return nil, nil
	}
	return lister.List(ctx, status)
}

// The abilities a sweep asks of its tracker with a type assertion. A tracker
// that wraps another has to carry each one over, or the assertion quietly finds
// it cannot and the sweep falls back to a slower read, or to doing nothing.
var (
	_ closedItemLister       = HarnessRunItems{}
	_ itemPrefetcher         = HarnessRunItems{}
	_ itemStatusReader       = HarnessRunItems{}
	_ schedulingWaitRecorder = HarnessRunItems{}
)

// PrefetchItems holds the items the tracker holds together, where the wrapped
// tracker can, and skips a bookkeeping item of the harness's own, which the
// tracker does not hold. Over a tracker that cannot, it holds nothing.
func (t HarnessRunItems) PrefetchItems(ctx context.Context, ids []string) func() {
	prefetcher, ok := t.WorkTracker.(itemPrefetcher)
	if !ok {
		return func() {}
	}
	tracked := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ours := runOfItem(id); !ours {
			tracked = append(tracked, id)
		}
	}
	return prefetcher.PrefetchItems(ctx, tracked)
}

// Statuses is the status of each item: a bookkeeping item of the harness's own
// from its run's record, and the rest in one read of the wrapped tracker where
// it can make one. An item not answered for is left out, as the tracker's own
// batched read leaves it out, and its caller reads it alone.
func (t HarnessRunItems) Statuses(ctx context.Context, ids []string) (map[string]string, error) {
	statuses := make(map[string]string, len(ids))
	var tracked []string
	for _, id := range ids {
		state, ours, err := t.landing(id)
		switch {
		case !ours:
			tracked = append(tracked, id)
		case err == nil:
			statuses[id] = outsideFileItemStatus(state)
		}
	}
	if len(tracked) == 0 {
		return statuses, nil
	}
	if reader, ok := t.WorkTracker.(itemStatusReader); ok {
		found, err := reader.Statuses(ctx, tracked)
		if err != nil {
			return nil, err
		}
		maps.Copy(statuses, found)
		return statuses, nil
	}
	return statuses, nil
}

// RecordSchedulingWait keeps an item's scheduling reason on the wrapped tracker
// where it can hold one. A bookkeeping item of the harness's own has none to
// keep, and the call does nothing for it.
func (t HarnessRunItems) RecordSchedulingWait(ctx context.Context, id, class, reason string, at time.Time) (beads.WorkItem, error) {
	if _, ours := runOfItem(id); ours {
		return beads.WorkItem{}, nil
	}
	recorder, ok := t.WorkTracker.(schedulingWaitRecorder)
	if !ok {
		return beads.WorkItem{}, nil
	}
	return recorder.RecordSchedulingWait(ctx, id, class, reason, at)
}
