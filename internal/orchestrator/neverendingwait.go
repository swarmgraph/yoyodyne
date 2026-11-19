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

// EndNeverEndingWaits is the reconcile sweep's half of refusing a wait that can
// never end: a decision to wait on the stopped item's own parent, or an item
// above it, is refused when it is recorded (chat's refuseFinishedWaitsOn), and
// this puts back on the development manager's docket every such wait recorded
// before that refusal existed. It reads the tracker through the sweep's own
// Tracker and leaves the docket alone where it has no tracker or docket.
func (r Reconciler) EndNeverEndingWaits(ctx context.Context) (int, error) {
	if r.Docket == nil || r.Docket.Docket == nil || r.Tracker == nil {
		return 0, nil
	}
	return r.Docket.EndWaitsOnAncestors(func(id string) (beads.WorkItem, bool, error) {
		showCtx, cancel := trackerCallContext(ctx)
		defer cancel()
		item, err := r.Tracker.Show(showCtx, id)
		if err != nil {
			return beads.WorkItem{}, false, err
		}
		return item, true, nil
	})
}

// EndWaitsOnAncestors puts back on the docket every entry a standing wait holds
// on its own item's parent or an item above it, and reports how many it put
// back.
//
// An item cannot close while work under it is still open, so such a wait never
// ends and the stoppage it holds is off the development manager's docket for
// good. Each is answered by a closure that holds nothing
// (triage.WaitNeverEndsDecision), carrying her earlier reasoning and saying why
// the wait cannot end, so the next build lists it as undecided and she decides
// it again. lookup answers an item by identifier; each item is read once. An
// item that cannot be read leaves its entry's wait standing for the next sweep
// rather than ending it on a guess, and is reported.
func (d Docketer) EndWaitsOnAncestors(lookup func(id string) (beads.WorkItem, bool, error)) (int, error) {
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
		if chain == nil {
			continue
		}
		// Strictly after the wait, so the docket reads this as the closure standing
		// over the entry rather than one made at the same moment as the wait.
		endedAt := now
		if !endedAt.After(entry.Closed.ClosedAt) {
			endedAt = entry.Closed.ClosedAt.Add(time.Second)
		}
		reason := fmt.Sprintf("%s decided to wait on %s at %s: %s; that wait can never end, because %s, and an item cannot close while work under it is still open, so this stoppage needs a new decision",
			strings.TrimSpace(entry.Closed.DecidedBy), waitedOn,
			entry.Closed.ClosedAt.UTC().Format(time.RFC3339), strings.TrimSpace(entry.Closed.Reason),
			beads.DescribeAncestry(entry.WorkItemID, chain))
		took, err := d.Docket.Close(triage.Closure{
			SchemaVersion: triage.ClosureSchemaVersion,
			Key:           entry.Key,
			ProductID:     entry.ProductID,
			RunID:         entry.RunID,
			WorkItemID:    entry.WorkItemID,
			Decision:      triage.WaitNeverEndsDecision,
			Reason:        singleLine(reason, triage.MaxMessageBytes),
			DecidedBy:     "the harness, finding the wait names an item above the stopped one",
			ClosedAt:      endedAt,
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("put the %s entry of %s back on the docket, since its wait on %s can never end: %w", entry.Class, entry.WorkItemID, waitedOn, err))
			continue
		}
		if took {
			ended++
		}
	}
	return ended, errors.Join(problems...)
}
