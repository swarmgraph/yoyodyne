package readmodel

import (
	"context"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// WaitedOnMany is a reading of waited work that can read several work items
// in one call. It is satisfied by beads.Client, whose ShowMany answers an id it
// holds no item under by leaving it out.
type WaitedOnMany interface {
	ShowMany(ctx context.Context, ids []string) (map[string]beads.WorkItem, error)
}

// maxWaitAncestry bounds how many levels above a held item its parents are
// read ahead, the depth the tracker's own holding-back is read to
// (beads.AncestorChain stops there too).
const maxWaitAncestry = 16

// waitReads is what endWaits read ahead of need: the work each standing wait
// names, and every held item with a wait and its parents up to the top, each
// level in one batched call (beads.Client.ShowMany).
//
// endWaits runs in every pull of `yoyo work --watch` and every reading of the
// holds, and asked the tracker for each of those items with a `bd show` of its
// own. bd lets one command into its store at a time, reads included, so dozens
// of reads made one after another in every pull kept the store busy and every
// listing waited behind them
// (docs/diagnoses/yoyodyne-ifd-428-126-4-24-slow-listings.md). Read together,
// they wait once for each level of the deepest chain.
//
// items holds what was read. failed is the error of a batch that went
// unanswered: every item not yet read is then said to be unreadable with it,
// rather than asked about once more each, since each of those reads would wait
// on the same store that has just not answered. An item a batch that did
// answer left out is not held by the tracker as far as it said, and is read on
// its own, which tells a missing item from a tracker that did not answer.
type waitReads struct {
	items  map[string]beads.WorkItem
	failed error
}

// readWaitsAhead reads ahead what endWaits will ask about, where the reading
// given can read several items at once. One that cannot reads nothing ahead,
// and endWaits asks for each item as it did before.
func readWaitsAhead(ctx context.Context, held map[string]backlog.Hold, waited WaitedOn) waitReads {
	reads := waitReads{items: map[string]beads.WorkItem{}}
	many, ok := waited.(WaitedOnMany)
	if !ok {
		return reads
	}
	var frontier []string
	chains := map[string]bool{}
	for workItemID, hold := range held {
		waitsOn := strings.TrimSpace(hold.WaitsOn)
		if waitsOn == "" {
			continue
		}
		frontier = append(frontier, waitsOn, workItemID)
		chains[workItemID] = true
	}
	asked := map[string]bool{}
	for depth := 0; depth <= maxWaitAncestry && len(frontier) > 0; depth++ {
		var wanted []string
		for _, id := range frontier {
			if id == "" || asked[id] || !beads.ValidIssueID(id) {
				continue
			}
			asked[id] = true
			wanted = append(wanted, id)
		}
		if len(wanted) == 0 {
			break
		}
		items, err := many.ShowMany(ctx, wanted)
		if err != nil {
			reads.failed = err
			return reads
		}
		frontier = frontier[:0]
		for _, id := range wanted {
			item, found := items[id]
			if !found {
				continue
			}
			reads.items[id] = item
			// Only a held item's chain is walked; the work a wait names is read
			// for its status alone.
			if chains[id] {
				if parent := item.DecomposedFrom(); parent != "" {
					chains[parent] = true
					frontier = append(frontier, parent)
				}
			}
		}
	}
	return reads
}

// The pull and the conversations read the holds through beads.Client; were it
// to stop reading several items at once, endWaits would go back to a read for
// each item without anything failing, so the build says so instead.
var _ WaitedOnMany = beads.Client{}
