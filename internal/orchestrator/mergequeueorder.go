package orchestrator

// The order the merge queue takes its next entry in
// (docs/designs/integration-through-a-merge-queue.md, "The order the queue
// takes entries in"). The queue chooses each time it builds a candidate, never
// at admission, so a priority changed while an entry waits counts.

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

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
}

// rankedEntry is a waiting entry with what the order decides by.
type rankedEntry struct {
	entry      runstate.MergeQueueEntry
	inProgress bool
	document   bool
	architect  bool
	priority   int
	readable   bool
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

// entryInProgress is an entry whose newest generation is still earning its
// checks or its review: it is never stopped for one that ranks higher.
func entryInProgress(p *Pipeline, generations []runstate.MergeQueueGeneration, entry runstate.MergeQueueEntry) bool {
	count := len(generations)
	if count == 0 {
		return false
	}
	latest := generations[count-1]
	if latest.Invalidated != nil || latest.WaitsOnTarget() || (latest.CheckRun == nil && latest.Review == nil) {
		return false
	}
	return latest.Gate(p.mergeQueueChecks(entry)) != nil
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
		item, err := o.pipeline.Tracker.Show(ctx, entry.WorkItemID)
		if err != nil {
			readErrors++
			if firstReadError == nil {
				firstReadError = err
			}
		} else {
			r.priority, r.readable = item.Priority, true
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
	chosen, rule := rank(candidates, o.now)
	return MergeQueueChoice{Entry: chosen.entry, Rule: rule, Passed: queuePassedOver(waiting, chosen.entry)}
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
// the first entry the first applicable rule picks. candidates is not empty.
func rank(candidates []rankedEntry, now time.Time) (rankedEntry, string) {
	for _, r := range candidates {
		if r.inProgress {
			return r, MergeQueueRuleInProgress
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
		return aged[0], MergeQueueRuleAged
	}
	oldestArchitect := -1
	for i, r := range candidates {
		if r.document && r.architect {
			oldestArchitect = i
			break
		}
	}
	for i, r := range candidates {
		if i == oldestArchitect || (!r.document && r.readable && r.priority == 0) {
			return r, MergeQueueRuleUrgent
		}
	}
	for _, r := range candidates {
		if r.document {
			return r, MergeQueueRuleDocument
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
		return code[0], MergeQueueRulePriority
	}
	return candidates[0], MergeQueueRuleUnreadable
}
