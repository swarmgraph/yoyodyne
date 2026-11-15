package beads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// maxItemsPerShow is how many work items one batched `bd show` names. It keeps
// the command line and bd's answer a modest size however many items a caller
// asks for; a caller asking for more is answered by one call per batch.
const maxItemsPerShow = 50

// noneOfTheItems is what bd says when it holds none of the items a `bd show`
// named: `Error fetching <id>: no issue found matching "<id>"` on its standard
// error for each, and an error naming none of them on its standard output. It
// exits non-zero for that and for nothing else a batched read can meet that is
// not the tracker failing: an id it holds no item under among others it does
// hold is said on its standard error, and the rest are answered with a
// successful exit (TestShowManyConformance). The client keeps standard error
// where bd wrote any, so both forms are looked for.
var noneOfTheItems = []string{"no issue found matching", "no issues found matching the provided IDs"}

// ShowMany reads the work items named, in one `bd show` for each
// maxItemsPerShow of them, and reports what it read by id. An id the tracker
// holds no item under is simply absent from the answer: a caller that needs to
// know why reads it with Show, which tells a missing item from a tracker that
// did not answer.
//
// It exists because bd lets one command into its store at a time, reads
// included, so on a busy store what a read costs is the wait to be let in
// rather than the reading (docs/diagnoses/yoyodyne-ifd-428-126-4-14-1-reconcile-tracker-waits.md):
// eleven items read together wait once, where eleven reads of one item wait
// eleven times. bd answers each item exactly as a `bd show` of that item alone
// does.
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
	read := make(map[string]WorkItem, len(wanted))
	for start := 0; start < len(wanted); start += maxItemsPerShow {
		batch := wanted[start:min(start+maxItemsPerShow, len(wanted))]
		args := append([]string{"show"}, batch...)
		data, err := c.run(ctx, append(args, "--json")...)
		if err != nil {
			var refused processFailure
			if errors.As(err, &refused) && refused.status != execution.ProcessTimedOut && foundNone(refused.message) {
				continue
			}
			return nil, err
		}
		items, err := decodeWorkItems(data)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if seen[item.ID] {
				read[item.ID] = item
			}
		}
	}
	return read, nil
}

// foundNone reports a refusal that is bd saying it holds none of the items
// named, rather than failing to answer.
func foundNone(message string) bool {
	for _, said := range noneOfTheItems {
		if strings.Contains(message, said) {
			return true
		}
	}
	return false
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

// describeShow says what a `bd show` was asked for: one item by its id, or how
// many items read together and the first of them.
func describeShow(args []string) string {
	var ids []string
	for _, arg := range args[1:] {
		if ValidIssueID(arg) && strings.Contains(arg, "-") {
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
// against, the item as it stands now rather than as it was read ahead.
func (c Client) live() Client {
	c.Reads = nil
	return c
}
