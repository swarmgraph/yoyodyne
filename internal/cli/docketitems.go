package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// itemLister is a tracker listing, as the docket's readers ask for one.
type itemLister interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
}

// itemShower is a tracker that can also read one work item, which is how an
// item a cut listing left out is read. beads.Client is one; a tracker that is
// not leaves such an item unread.
type itemShower interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
}

// maxItemReadsPastACut bounds how many items left out of a cut listing are read
// one at a time. Each is its own tracker call, so the bound is what keeps a
// listing cut far short of the docket from turning one reading into a long
// walk; an item past it is named as unread, not taken for open.
const maxItemReadsPastACut = 40

// docketItems is what the tracker says about the work items a docket names.
type docketItems struct {
	// items are the work items read: the listing's rows, and where the listing
	// was cut, the named items it left out that were read one at a time.
	items []beads.WorkItem
	// cut says, in plain words, that the listing stopped at bd's default limit
	// and what was done about it, and is empty where the listing was whole.
	cut string
	// unread are the named items neither the listing nor a reading of their own
	// could say anything about. An entry on one is listed, because its item is
	// not known to be closed.
	unread []string
}

// readDocketItems lists the tracker's work items with the status given, and
// where bd would not lift its default limit, uses the rows it did return and
// reads each named item they left out on its own. Whether a docket entry's
// item is closed is then read from the item itself rather than from a listing
// that may have stopped short of it, and a listing that stopped short is said
// rather than taken for the whole. Any other listing failure is returned as it
// came.
//
// status narrows the result exactly as the listing does: an item read on its
// own is kept only where its status is the one asked for.
func readDocketItems(ctx context.Context, tracker itemLister, status string, named []string) (docketItems, error) {
	items, err := tracker.List(ctx, status)
	if err == nil {
		return docketItems{items: items}, nil
	}
	var cut *beads.ListingCut
	if !errors.As(err, &cut) {
		return docketItems{}, err
	}
	read := docketItems{items: append([]beads.WorkItem(nil), cut.Items...)}
	listed := make(map[string]bool, len(cut.Items))
	for _, item := range cut.Items {
		listed[strings.TrimSpace(item.ID)] = true
	}
	shower, canShow := tracker.(itemShower)
	asked := 0
	for _, id := range named {
		id = strings.TrimSpace(id)
		if id == "" || listed[id] {
			continue
		}
		listed[id] = true
		if !canShow || asked >= maxItemReadsPastACut {
			read.unread = append(read.unread, id)
			continue
		}
		asked++
		item, err := shower.Show(ctx, id)
		if err != nil {
			read.unread = append(read.unread, id)
			continue
		}
		if status == "" || strings.TrimSpace(item.Status) == status {
			read.items = append(read.items, item)
		}
	}
	read.cut = fmt.Sprintf("the tracker's listing stopped at bd's default limit after %d work item(s), so the %d item(s) the docket names that it left out were read one at a time", len(cut.Items), asked)
	if len(read.unread) > 0 {
		read.cut += fmt.Sprintf("; %d could not be read (%s), so whether they are closed is not known and their entries are listed", len(read.unread), strings.Join(read.unread, ", "))
	}
	return read, nil
}

// docketItemIDs is the work item of every entry a docket holds, including the
// entries folded beneath another.
func docketItemIDs(docket []triage.Entry) []string {
	var ids []string
	for _, entry := range docket {
		ids = append(ids, entry.WorkItemID)
		for _, earlier := range entry.Earlier {
			ids = append(ids, earlier.WorkItemID)
		}
	}
	return ids
}

// docketItemsUnknown is what the docket's window is told about items whose
// state could not be read, and empty where every named item was. The rows that
// were read still decide which entries are on closed work.
func docketItemsUnknown(read docketItems) string {
	if len(read.unread) == 0 {
		return ""
	}
	return read.cut
}
