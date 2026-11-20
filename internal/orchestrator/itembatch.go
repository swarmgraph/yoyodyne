package orchestrator

import (
	"context"
	"maps"

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
