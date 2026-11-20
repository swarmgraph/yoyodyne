package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// ReconcileChildren lists the unfinished items broken out of an item, which is
// what says a wait on that item lasts until a whole area of work is done. It is
// satisfied by beads.Client.
type ReconcileChildren interface {
	UnfinishedChildren(ctx context.Context, parent string) ([]beads.WorkItem, error)
}

// EndNeverEndingWaits is the reconcile sweep's half of refusing a wait that
// cannot end on the one piece of work it meant: a decision to wait on the
// stopped item's own parent, an item above it, or any item that still has open
// child items, is refused when it is recorded (chat's refuseFinishedWaitsOn),
// and this puts back on the development manager's docket every such wait
// recorded before that refusal existed. It reads the tracker through the
// sweep's own Tracker and Children, leaves the docket alone where it has no
// tracker or docket, and reads no children where it has no Children.
func (r Reconciler) EndNeverEndingWaits(ctx context.Context) (int, error) {
	if r.Docket == nil || r.Docket.Docket == nil || r.Tracker == nil {
		return 0, nil
	}
	var children func(id string) ([]beads.WorkItem, error)
	if r.Children != nil {
		children = func(id string) ([]beads.WorkItem, error) {
			listCtx, cancel := trackerCallContext(ctx)
			defer cancel()
			return r.Children.UnfinishedChildren(listCtx, id)
		}
	}
	return r.Docket.EndWaitsOnAncestors(func(id string) (beads.WorkItem, bool, error) {
		showCtx, cancel := trackerCallContext(ctx)
		defer cancel()
		item, err := r.Tracker.Show(showCtx, id)
		if err != nil {
			return beads.WorkItem{}, false, err
		}
		return item, true, nil
	}, children)
}

// EndWaitsOnAncestors puts back on the docket every entry a standing wait holds
// on its own item's parent or an item above it, or on any other item that still
// has open child items, and reports how many it put back.
//
// An item cannot close while work under it is still open, so a wait on the
// stopped item's own parent or above never ends, and a wait on an item with
// open children ends only once that whole area of work is done: either way the
// stoppage is off the development manager's docket for as long as the area
// runs, which is what hid handed-back changes behind waits on whole areas. Each
// is answered by a closure that holds nothing (triage.WaitNeverEndsDecision),
// carrying her earlier reasoning and saying why the wait does not end on the
// work it meant, so the next build lists it as undecided and she decides it
// again. lookup answers an item by identifier, and children lists an item's
// unfinished children; each item is read once, and a nil children reads none.
// An item that cannot be read leaves its entry's wait standing for the next
// sweep rather than ending it on a guess, and is reported.
func (d Docketer) EndWaitsOnAncestors(lookup func(id string) (beads.WorkItem, bool, error), children func(id string) ([]beads.WorkItem, error)) (int, error) {
	entries, err := d.Docket.List()
	if err != nil {
		return 0, fmt.Errorf("read the triage docket to find waits that can never end: %w", err)
	}
	type reading struct {
		item  beads.WorkItem
		found bool
		err   error
	}
	read := make(map[string]reading)
	cached := func(id string) (beads.WorkItem, bool, error) {
		if known, seen := read[id]; seen {
			return known.item, known.found, known.err
		}
		item, found, err := lookup(id)
		read[id] = reading{item: item, found: found, err: err}
		return item, found, err
	}
	type listing struct {
		children []beads.WorkItem
		err      error
	}
	listed := make(map[string]listing)
	childrenOf := func(id string) ([]beads.WorkItem, error) {
		if known, seen := listed[id]; seen {
			return known.children, known.err
		}
		found, err := children(id)
		listed[id] = listing{children: found, err: err}
		return found, err
	}
	now := d.now().UTC()
	ended := 0
	var problems []error
	for _, entry := range entries {
		if entry.Closed == nil || !entry.Closed.WaitsOnWork() || !entry.Closed.Holds(now) {
			continue
		}
		waitedOn := strings.TrimSpace(entry.Closed.WaitsOn)
		chain, err := beads.AncestorChain(entry.WorkItemID, waitedOn, cached)
		if err != nil {
			problems = append(problems, fmt.Errorf("whether the wait on %s over the %s entry of %s can ever end could not be read, so it stands until the next sweep: %w", waitedOn, entry.Class, entry.WorkItemID, err))
			continue
		}
		earlier := fmt.Sprintf("%s decided to wait on %s at %s: %s",
			strings.TrimSpace(entry.Closed.DecidedBy), waitedOn,
			entry.Closed.ClosedAt.UTC().Format(time.RFC3339), strings.TrimSpace(entry.Closed.Reason))
		var reason, decidedBy string
		switch {
		case chain != nil:
			reason = fmt.Sprintf("%s; that wait can never end, because %s, and an item cannot close while work under it is still open, so this stoppage needs a new decision",
				earlier, beads.DescribeAncestry(entry.WorkItemID, chain))
			decidedBy = "the harness, finding the wait names an item above the stopped one"
		case children != nil:
			open, err := childrenOf(waitedOn)
			if err != nil {
				problems = append(problems, fmt.Errorf("whether %s, which the %s entry of %s waits on, still has open work under it could not be read, so the wait stands until the next sweep: %w", waitedOn, entry.Class, entry.WorkItemID, err))
				continue
			}
			if len(open) == 0 {
				continue
			}
			reason = fmt.Sprintf("%s; %s still has %s, and it closes only when all of its work is done, so that wait would keep this stoppage off the docket until that whole area is finished; this stoppage needs a new decision, waiting on a specific item if it waits on anything",
				earlier, waitedOn, beads.DescribeOpenChildren(open))
			decidedBy = "the harness, finding the wait names an item that still has open child items"
		default:
			continue
		}
		// Strictly after the wait, so the docket reads this as the closure standing
		// over the entry rather than one made at the same moment as the wait.
		endedAt := now
		if !endedAt.After(entry.Closed.ClosedAt) {
			endedAt = entry.Closed.ClosedAt.Add(time.Second)
		}
		took, err := d.Docket.Close(triage.Closure{
			SchemaVersion: triage.ClosureSchemaVersion,
			Key:           entry.Key,
			ProductID:     entry.ProductID,
			RunID:         entry.RunID,
			WorkItemID:    entry.WorkItemID,
			Decision:      triage.WaitNeverEndsDecision,
			Reason:        singleLine(reason, triage.MaxMessageBytes),
			DecidedBy:     decidedBy,
			ClosedAt:      endedAt,
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("put the %s entry of %s back on the docket, since its wait on %s does not end on the work it meant: %w", entry.Class, entry.WorkItemID, waitedOn, err))
			continue
		}
		if took {
			ended++
		}
	}
	return ended, errors.Join(problems...)
}
