package orchestrator

// The order the merge queue takes its next entry in
// (docs/designs/integration-through-a-merge-queue.md, "The order the queue
// takes entries in"). The queue chooses each time it builds a candidate, never
// at admission, so a priority changed while an entry waits counts.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// errNoQueueTracker is the tracker read of a queue given no tracker, which
// chooses in admission order as an unreadable tracker does.
var errNoQueueTracker = errors.New("no tracker is configured for the merge queue")

// mergeQueueAgeLimit is how long an entry waits, since admission, before it
// goes ahead of everything else whatever its priority.
const mergeQueueAgeLimit = 8 * time.Hour

// Rules that can choose an entry, in the words a record of the choice carries.
const (
	MergeQueueRuleInProgress  = "already being checked or reviewed"
	MergeQueueRuleAged        = "waited eight hours or more"
	MergeQueueRuleUrgent      = "priority 0 code, or the architect's oldest document"
	MergeQueueRuleDocument    = "document"
	MergeQueueRulePriority    = "code, by priority"
	MergeQueueRuleUnreadable  = "code whose priority could not be read"
	MergeQueueRuleAdmission   = "admission order, because the tracker could not be read"
	MergeQueueRuleOnlyWaiting = "the only entry waiting"
)

// MergeQueueChoice is which entry the queue chose, by which rule, and which
// waiting entries it passed over to do so.
type MergeQueueChoice struct {
	Entry  runstate.MergeQueueEntry
	Rule   string
	Passed []string
	// TrackerUnread says why priorities were not used, and is empty where the
	// tracker was read.
	TrackerUnread string
	// PlacedFirst says the Lead Product Manager's placement of the chosen
	// entry's work item first among its priority decided the choice: without
	// it, an entry admitted earlier under the same rule would have been chosen.
	PlacedFirst bool
}

// rankedEntry is a waiting entry with what the order decides by.
type rankedEntry struct {
	entry      runstate.MergeQueueEntry
	inProgress bool
	document   bool
	architect  bool
	priority   int
	readable   bool
	// placed is when the Lead Product Manager placed the entry's work item
	// first among its priority (docs/designs/placing-an-item-first.md), and nil
	// where no placement orders it.
	placed *time.Time
}

// queueOrder is what the order is chosen with. The worker that verifies the
// next entry and the promoter that lands it both choose through it, so the two
// never disagree about which entry is next: a promoter that took the earliest
// admitted entry while the worker verified a later one would refuse the one
// and never land the other.
type queueOrder struct {
	pipeline *Pipeline
	// paths lists what a change makes against its base.
	paths func(ctx context.Context, base, candidate string) ([]string, error)
	now   time.Time
}

func (o queueOrder) choose(ctx context.Context, waiting []runstate.MergeQueueEntry, started map[string]bool) MergeQueueChoice {
	return o.pick(ctx, waiting, started)
}

// documentKind says from the paths the entry's change makes against its
// recorded base, never from its recorded kind, whether the entry is a
// document and whether it is the architect's. It asks the same question of
// the paths as the document landing route (changeIsDocument). A change whose
// paths cannot be read is a code entry, which is checked as code is.
func (o queueOrder) documentKind(ctx context.Context, entry runstate.MergeQueueEntry) (document, architect bool) {
	if o.pipeline.Store == nil || o.paths == nil {
		return false, false
	}
	run, err := o.pipeline.Store.Load(entry.RunID)
	if err != nil || run.BaseCommit == "" || entry.ApprovedHead == "" {
		return false, false
	}
	changed, err := o.paths(ctx, run.BaseCommit, entry.ApprovedHead)
	if err != nil {
		return false, false
	}
	// A blank line in a listing of paths is no path.
	changed = slices.DeleteFunc(slices.Clone(changed), func(changedPath string) bool { return strings.TrimSpace(changedPath) == "" })
	return changeIsDocument(o.pipeline.Config, changed), changeIsArchitects(o.pipeline.Config, changed)
}

// entryInProgress is an entry whose newest generation has begun its checks or
// review and still stands: one still earning them, and one that has earned
// both and waits only for the landing step. Either is never stopped for an
// entry that ranks higher, so the checks and review it has are not thrown
// away because the order's answer changed while it was checked, as when
// another entry reaches eight hours of waiting in the meantime.
//
// A candidate checked ahead (mergequeueahead.go) stands on the candidate of the
// entry it was built on, so it counts only once that entry no longer waits
// (unlanded says which do): until then it is not the next thing to land, and a
// stale one must not jump the order.
func entryInProgress(generations []runstate.MergeQueueGeneration, unlanded map[string]bool) bool {
	count := len(generations)
	if count == 0 {
		return false
	}
	latest := generations[count-1]
	if latest.Invalidated != nil || latest.WaitsOnTarget() || (latest.CheckRun == nil && latest.Review == nil) {
		return false
	}
	return latest.BuiltAhead == nil || !unlanded[latest.BuiltAhead.EntryID]
}

// pick is the entry to work next among the waiting ones given in admission
// order, and the record of why.
func (o queueOrder) pick(ctx context.Context, waiting []runstate.MergeQueueEntry, started map[string]bool) MergeQueueChoice {
	if len(waiting) == 1 {
		return MergeQueueChoice{Entry: waiting[0], Rule: MergeQueueRuleOnlyWaiting}
	}
	admitted := map[string]struct{}{}
	for _, entry := range waiting {
		admitted[entry.WorkItemID] = struct{}{}
	}
	ranked := make([]rankedEntry, 0, len(waiting))
	blocked := map[string]bool{}
	readErrors := 0
	var firstReadError error
	for _, entry := range waiting {
		r := rankedEntry{entry: entry, inProgress: started[entry.EntryID]}
		r.document, r.architect = o.documentKind(ctx, entry)
		var item beads.WorkItem
		err := errNoQueueTracker
		if o.pipeline.Tracker != nil {
			item, err = o.pipeline.Tracker.Show(ctx, entry.WorkItemID)
		}
		if err != nil {
			readErrors++
			if firstReadError == nil {
				firstReadError = err
			}
		} else {
			r.priority, r.readable = item.Priority, true
			if placement := item.PlacedFirst(); placement != nil {
				at := placement.At
				r.placed = &at
			}
			// WaitingOn also names open items that are not in the queue; only
			// another admitted entry's item holds this one back.
			for _, id := range item.WaitingOn(admitted) {
				if _, inQueue := admitted[id]; inQueue && id != entry.WorkItemID {
					blocked[entry.EntryID] = true
				}
			}
		}
		ranked = append(ranked, r)
	}
	if readErrors == len(waiting) {
		return MergeQueueChoice{Entry: waiting[0], Rule: MergeQueueRuleAdmission, Passed: queuePassedOver(waiting, waiting[0]),
			TrackerUnread: fmt.Sprintf("the tracker could not be read: %v", firstReadError)}
	}
	// An entry waiting on another waiting entry's item waits for it, unless
	// every entry does, which would be a cycle that nothing could break.
	candidates := ranked[:0:0]
	for _, r := range ranked {
		if !blocked[r.entry.EntryID] {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		candidates = ranked
	}
	chosen, rule, placed := rank(candidates, o.now)
	return MergeQueueChoice{Entry: chosen.entry, Rule: rule, Passed: queuePassedOver(waiting, chosen.entry), PlacedFirst: placed}
}

func queuePassedOver(waiting []runstate.MergeQueueEntry, chosen runstate.MergeQueueEntry) []string {
	var passed []string
	for _, entry := range waiting {
		if entry.EntryID == chosen.EntryID {
			break
		}
		passed = append(passed, entry.EntryID)
	}
	return passed
}

// rank applies the rules to candidates given in admission order and returns
// the first entry the first applicable rule picks, and whether a placement
// decided it. candidates is not empty.
//
// Within rule 2, and within each priority under rule 4, an entry whose work
// item was placed first goes ahead, earliest placement first, and the rest
// follow in admission order. A placement never lifts an entry past an entry
// already in progress or one that has waited eight hours, nor into a higher
// rule, because each rule's members are chosen before placement is looked at.
func rank(candidates []rankedEntry, now time.Time) (rankedEntry, string, bool) {
	for _, r := range candidates {
		if r.inProgress {
			return r, MergeQueueRuleInProgress, false
		}
	}
	var aged []rankedEntry
	for _, r := range candidates {
		if now.Sub(r.entry.AdmittedAt) >= mergeQueueAgeLimit {
			aged = append(aged, r)
		}
	}
	if len(aged) > 0 {
		sort.SliceStable(aged, func(i, j int) bool {
			if !aged[i].entry.AdmittedAt.Equal(aged[j].entry.AdmittedAt) {
				return aged[i].entry.AdmittedAt.Before(aged[j].entry.AdmittedAt)
			}
			return aged[i].entry.Order < aged[j].entry.Order
		})
		return aged[0], MergeQueueRuleAged, false
	}
	oldestArchitect := -1
	for i, r := range candidates {
		if r.document && r.architect {
			oldestArchitect = i
			break
		}
	}
	var urgent []rankedEntry
	for i, r := range candidates {
		if i == oldestArchitect || (!r.document && r.readable && r.priority == 0) {
			urgent = append(urgent, r)
		}
	}
	if len(urgent) > 0 {
		chosen, placed := placedFirst(urgent)
		return chosen, MergeQueueRuleUrgent, placed
	}
	for _, r := range candidates {
		if r.document {
			return r, MergeQueueRuleDocument, false
		}
	}
	var code []rankedEntry
	for _, r := range candidates {
		if r.readable {
			code = append(code, r)
		}
	}
	if len(code) > 0 {
		sort.SliceStable(code, func(i, j int) bool { return code[i].priority < code[j].priority })
		var same []rankedEntry
		for _, r := range code {
			if r.priority == code[0].priority {
				same = append(same, r)
			}
		}
		chosen, placed := placedFirst(same)
		return chosen, MergeQueueRulePriority, placed
	}
	return candidates[0], MergeQueueRuleUnreadable, false
}

// placedFirst is the first of one rule's or one priority's entries, given in
// admission order: the one placed earliest where any is placed, and otherwise
// the one admitted first. It also says whether the placement decided it, which
// is so only where an entry admitted earlier was passed over for it.
func placedFirst(group []rankedEntry) (rankedEntry, bool) {
	chosen := -1
	for i, r := range group {
		if r.placed == nil {
			continue
		}
		if chosen < 0 || r.placed.Before(*group[chosen].placed) {
			chosen = i
		}
	}
	if chosen < 0 {
		return group[0], false
	}
	return group[chosen], chosen > 0
}
