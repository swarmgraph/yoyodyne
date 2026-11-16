package orchestrator

// The order the merge queue takes its next entry in
// (docs/designs/integration-through-a-merge-queue.md, "The order the queue
// takes entries in"). The queue chooses each time it builds a candidate, never
// at admission, so a priority changed while an entry waits counts.

import (
	"context"
	"fmt"
	"path"
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

// documentHomes are the directories whose contents make a change a document:
// product intent, designs and decisions, with the invariants home inside them
// or beside them. Every path under them is a document path, directory indexes
// included.
func (p *Pipeline) documentHomes() (all, architect []string) {
	architect = []string{p.Config.Product.Designs, p.Config.Product.Decisions, p.Config.Product.Invariants}
	all = append([]string{p.Config.Product.Specifications}, architect...)
	return all, architect
}

// onlyUnder reports whether every path lies under one of the directories, and
// there is at least one path.
func onlyUnder(paths, directories []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, changed := range paths {
		changed = path.Clean(strings.ReplaceAll(changed, "\\", "/"))
		inside := false
		for _, directory := range directories {
			directory = path.Clean(strings.TrimSpace(directory))
			if directory == "." || directory == "" {
				continue
			}
			if changed == directory || strings.HasPrefix(changed, directory+"/") {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
	}
	return true
}

// documentKind says from the paths the entry's change makes against its
// recorded base, never from its recorded kind, whether the entry is a
// document and whether it is the architect's. A change whose paths cannot be
// read is a code entry, which is checked as code is.
func (w MergeQueueWorker) documentKind(ctx context.Context, entry runstate.MergeQueueEntry) (document, architect bool) {
	if w.Pipeline.Store == nil {
		return false, false
	}
	run, err := w.Pipeline.Store.Load(entry.RunID)
	if err != nil || run.BaseCommit == "" || entry.ApprovedHead == "" {
		return false, false
	}
	changed, err := w.Candidates.CandidatePaths(ctx, run.BaseCommit, entry.ApprovedHead)
	if err != nil {
		return false, false
	}
	all, own := w.Pipeline.documentHomes()
	return onlyUnder(changed, all), onlyUnder(changed, own)
}

// inProgress is an entry whose newest generation is still earning its checks
// or its review: it is never stopped for one that ranks higher.
func (w MergeQueueWorker) inProgress(generations []runstate.MergeQueueGeneration, entry runstate.MergeQueueEntry) bool {
	count := len(generations)
	if count == 0 {
		return false
	}
	latest := generations[count-1]
	if latest.Invalidated != nil || latest.WaitsOnTarget() || (latest.CheckRun == nil && latest.Review == nil) {
		return false
	}
	return latest.Gate(w.Pipeline.mergeQueueChecks(entry)) != nil
}

// choose is the entry to work next among the waiting ones given in admission
// order, and the record of why.
func (w MergeQueueWorker) choose(ctx context.Context, waiting []runstate.MergeQueueEntry, started map[string]bool) MergeQueueChoice {
	if len(waiting) == 1 {
		return MergeQueueChoice{Entry: waiting[0], Rule: MergeQueueRuleOnlyWaiting}
	}
	unfinished := map[string]struct{}{}
	for _, entry := range waiting {
		unfinished[entry.WorkItemID] = struct{}{}
	}
	ranked := make([]rankedEntry, 0, len(waiting))
	blocked := map[string]bool{}
	readErrors, reads := 0, 0
	var firstReadError error
	for _, entry := range waiting {
		r := rankedEntry{entry: entry, inProgress: started[entry.EntryID]}
		r.document, r.architect = w.documentKind(ctx, entry)
		item, err := w.Pipeline.Tracker.Show(ctx, entry.WorkItemID)
		reads++
		if err != nil {
			readErrors++
			if firstReadError == nil {
				firstReadError = err
			}
		} else {
			r.priority, r.readable = item.Priority, true
			for _, id := range item.WaitingOn(unfinished) {
				if id != entry.WorkItemID {
					blocked[entry.EntryID] = true
				}
			}
		}
		ranked = append(ranked, r)
	}
	if readErrors == reads {
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
	chosen, rule := rank(candidates, w.now())
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
