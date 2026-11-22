package beads

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// maxItemsPerShow is how many work items one batched read names. It keeps the
// command line and bd's answer a modest size however many items a caller asks
// for; a caller asking for more is answered by one call per batch.
const maxItemsPerShow = 50

// itemsByID is the `bd list` flag that narrows a listing to the items named,
// comma-separated. bd matches each id exactly and leaves out one it holds
// nothing under, answering the rest (TestShowManyConformance).
const itemsByID = "--id="

// ShowMany reads the work items named and reports what it read by id, each
// exactly as Show reads it alone. An id the tracker holds no item under is
// simply absent from the answer: a caller that needs to know why reads it with
// Show, which tells a missing item from a tracker that did not answer.
//
// It reads them with `bd list --id=…` and not with `bd show` of every id,
// because bd lets one command into its store at a time, reads included, and
// `bd show` holds the store for about half a second for each item it names: a
// show of twenty items held it for over ten seconds, and every listing waited
// behind it, where a listing of the same twenty holds it for under half a
// second (docs/diagnoses/yoyodyne-ifd-428-126-4-24-slow-listings.md). A listing
// states each link as an edge, naming the item linked but not its status or
// title, so the items linked to are read the same way in one more call and each
// link is given what `bd show` gives it (asShown).
//
// It asks once and does not ask again on a timeout: its callers read ahead of
// need (PrefetchItems), and one whose batch went unanswered reads each item as
// it would have without it, so a second wait would only add to the first.
func (c Client) ShowMany(ctx context.Context, ids []string) (map[string]WorkItem, error) {
	wanted := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if err := validateIssueID(id); err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			wanted = append(wanted, id)
		}
	}
	read, err := c.listByID(ctx, wanted)
	if err != nil {
		return nil, err
	}
	for id := range read {
		if !seen[id] {
			delete(read, id)
		}
	}
	linked := make(map[string]WorkItem, len(read))
	var unread []string
	for _, item := range read {
		for _, dependency := range ownDependencies(item) {
			if _, have := read[dependency.ID]; have || seen[dependency.ID] {
				continue
			}
			seen[dependency.ID] = true
			unread = append(unread, dependency.ID)
		}
	}
	if len(unread) > 0 {
		if linked, err = c.listByID(ctx, unread); err != nil {
			return nil, err
		}
	}
	for id, item := range read {
		read[id] = asShown(item, read, linked)
	}
	return read, nil
}

// Statuses reads the status of each work item named, in one `bd list --id=…`
// for each fifty, and reports it by id. It is for a caller that needs only
// whether items are closed: unlike ShowMany it does not read the items they
// link to, so it costs the store one listing however many items it names. An id
// the tracker holds no item under is absent from the answer, as in ShowMany.
//
// The merge queue's carry-out, its re-arming and reconcile each ask whether the
// items a merge withdrawn for a red target waits on are closed, every pass; read
// one at a time with Show, that was a call per item each pass, and in the hour
// from 03:00 on November 21 those reads were one tracker call in six
// (docs/diagnoses/yoyodyne-ifd-428-126-4-24-slow-listings.md).
func (c Client) Statuses(ctx context.Context, ids []string) (map[string]string, error) {
	wanted := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if err := validateIssueID(id); err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			wanted = append(wanted, id)
		}
	}
	if len(wanted) == 0 {
		return map[string]string{}, nil
	}
	read, err := c.listByID(ctx, wanted)
	if err != nil {
		return nil, err
	}
	statuses := make(map[string]string, len(read))
	for id, item := range read {
		if seen[id] {
			statuses[id] = item.Status
		}
	}
	return statuses, nil
}

// listByID reads the items named with `bd list`, maxItemsPerShow to a call,
// closed ones included. Validated ids carry no comma, so each is one id to bd.
func (c Client) listByID(ctx context.Context, ids []string) (map[string]WorkItem, error) {
	read := make(map[string]WorkItem, len(ids))
	for start := 0; start < len(ids); start += maxItemsPerShow {
		batch := ids[start:min(start+maxItemsPerShow, len(ids))]
		data, err := c.run(ctx, "list", "--json", unboundedListing, everyStatus, itemsByID+strings.Join(batch, ","))
		if err != nil {
			return nil, err
		}
		items, err := decodeWorkListing(data, "list", c.writerZone())
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			read[item.ID] = item
		}
	}
	return read, nil
}

// ownDependencies is the links a listing carried for item that are item's own:
// an edge naming another item as the one it belongs to is that item's link,
// which `bd show` of item does not list.
func ownDependencies(item WorkItem) []Dependency {
	own := make([]Dependency, 0, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		if dependency.IssueID == "" || dependency.IssueID == item.ID {
			own = append(own, dependency)
		}
	}
	return own
}

// asShown gives item's links the form `bd show` gives them: the linked item's
// status and title, and neither the edge's owner nor its stamp, which a show
// does not carry. A link to an item neither reading found keeps its id and
// type and says nothing of its status, rather than being dropped.
func asShown(item WorkItem, read, linked map[string]WorkItem) WorkItem {
	own := ownDependencies(item)
	shown := make([]Dependency, 0, len(own))
	for _, dependency := range own {
		target, found := read[dependency.ID]
		if !found {
			target = linked[dependency.ID]
		}
		shown = append(shown, Dependency{ID: dependency.ID, Type: dependency.Type, Status: target.Status, Title: target.Title})
	}
	item.Dependencies = shown
	return item
}

// itemReadsMaxAge is how long an item read ahead of need is given out for. A
// read of one item is a snapshot of it whenever it is made, so an answer read
// a little earlier in the same pass is the same kind of answer; one read much
// earlier than that is read again instead, so nothing is decided on an item as
// it stood long before.
const itemReadsMaxAge = 5 * time.Minute

// ItemReads holds work items a client read ahead of need, in one batched call,
// for the reads of one item that follow. Each item is given out once, to the
// first Show of it, and every later Show of it asks the tracker as it always
// did; so a read made again to confirm a write, or to check an item before
// changing it, is never answered from here. A command of this client that
// names an item (a write, a claim, a note) drops what is held for it before it
// runs, and nothing is given out past itemReadsMaxAge.
//
// A client carries it by pointer, so every copy of the client made from one
// sharing it reads from it. The zero value is ready to use.
type ItemReads struct {
	mu    sync.Mutex
	items map[string]heldItem
	// now is the clock the age is read on. Nil is the wall clock; it is set
	// only by this package's tests.
	now func() time.Time
}

type heldItem struct {
	item WorkItem
	read time.Time
}

func (r *ItemReads) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// hold keeps items read together at one moment.
func (r *ItemReads) hold(items map[string]WorkItem, read time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = make(map[string]heldItem, len(items))
	}
	for id, item := range items {
		r.items[id] = heldItem{item: item, read: read}
	}
}

// take gives out the item held for id, once, while it is young enough.
func (r *ItemReads) take(id string) (WorkItem, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	held, ok := r.items[id]
	if !ok {
		return WorkItem{}, false
	}
	delete(r.items, id)
	if r.clock().Sub(held.read) > itemReadsMaxAge {
		return WorkItem{}, false
	}
	return held.item, true
}

// forget drops what is held for every item a command names: an argument that
// is the item's id, or a flag whose value is, such as `--parent=<id>`. An id is
// matched whole, so a write naming yoyodyne-ifd.1 leaves yoyodyne-ifd.10 held.
func (r *ItemReads) forget(args []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, arg := range args {
		named := arg
		if flag, value, isFlag := strings.Cut(arg, "="); isFlag && strings.HasPrefix(flag, "-") {
			named = value
		}
		for _, id := range strings.Split(named, ",") {
			delete(r.items, id)
		}
	}
}

// clear drops everything held.
func (r *ItemReads) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = nil
}

// Held reports how many items are held and not yet given out.
func (r *ItemReads) Held() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

// PrefetchItems reads the items named in one batched call (ShowMany) and holds
// them for the first Show of each, and returns what ends the hold: a caller
// reads ahead for one stretch of work and calls it once that is over, so
// nothing read for it is given out to anything after. A client carrying no
// ItemReads reads nothing and holds nothing. A batch that fails holds nothing
// and changes nothing else: each item is then read on its own, as it would
// have been, and the failed call is told to the client's call observer like
// any other, so a slow one is still said.
func (c Client) PrefetchItems(ctx context.Context, ids []string) (release func()) {
	if c.Reads == nil || len(ids) == 0 {
		return func() {}
	}
	read := c.Reads.clock()
	items, err := c.ShowMany(ctx, ids)
	if err == nil {
		c.Reads.hold(items, read)
	}
	return c.Reads.clear
}

// heldRead is the item read ahead of need for id, where there is one.
func (c Client) heldRead(id string) (WorkItem, bool) {
	if c.Reads == nil {
		return WorkItem{}, false
	}
	return c.Reads.take(id)
}

// readsOnly reports a bd verb that changes nothing in the store.
func readsOnly(verb string) bool {
	switch verb {
	case "show", "list", "ready", "export":
		return true
	}
	return false
}

// describeShow says what a read of named items was asked for: one item by its
// id, or how many items read together and the first of them. The items are the
// ids a `bd show` names, or the ones a `bd list` is narrowed to (ShowMany); a
// listing narrowed to items is a read of those items and not a listing of open
// work, so it is described as one and the listing bound does not hold it.
func describeShow(args []string) string {
	var ids []string
	for _, arg := range args[1:] {
		if named, narrowed := strings.CutPrefix(arg, itemsByID); narrowed && args[0] == "list" {
			ids = append(ids, strings.Split(named, ",")...)
			continue
		}
		if args[0] == "show" && ValidIssueID(arg) && strings.Contains(arg, "-") {
			ids = append(ids, arg)
		}
	}
	switch len(ids) {
	case 0:
		return ""
	case 1:
		return "reading work item " + ids[0]
	}
	return fmt.Sprintf("reading %d work items in one call, %s first", len(ids), ids[0])
}

// live is the client with nothing held: what a write reads an item through
// before or after changing it, so what it writes is decided on, and confirmed
// against, the item as it stands now rather than as it was read ahead. It lists
// the open and ready work afresh too, rather than from the listings this
// process shares (sharedlistings.go).
func (c Client) live() Client {
	c.Reads = nil
	c.unshared = true
	return c
}

// ShowAfresh reads one item as it stands now, never from an item read ahead
// of need. A caller that is about to change an item it found in a listing reads
// it through this first, so a shared listing (sharedlistings.go), which may be
// up to sharedListingFresh old, never decides what is changed.
func (c Client) ShowAfresh(ctx context.Context, id string) (WorkItem, error) {
	return c.live().Show(ctx, id)
}
