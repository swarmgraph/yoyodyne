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
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// errNoQueueTracker is the tracker read of a queue given no tracker, which
// chooses in admission order as an unreadable tracker does.
var errNoQueueTracker = errors.New("no tracker is configured for the merge queue")

// mergeQueueAgeLimit is how long an entry waits, since admission, before it
// goes ahead of everything else whatever its priority.
const mergeQueueAgeLimit = 4 * time.Hour

// Rules that can choose an entry, in the words a record of the choice carries.
const (
	MergeQueueRuleInProgress  = "already being checked or reviewed"
	MergeQueueRuleAged        = "waited four hours or more"
	MergeQueueRuleUrgent      = "priority 0 code"
	MergeQueueRuleDocument    = "document"
	MergeQueueRulePriority    = "code, by priority"
	MergeQueueRuleUnreadable  = "code whose priority could not be read"
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
	// Priority is the chosen entry's priority where it was known, and
	// PriorityLastRead says it is the one last read from the tracker rather than
	// read now.
	Priority         int
	PriorityKnown    bool
	PriorityLastRead bool
	// Document says the chosen entry is a document entry. It is what a later
	// choice reads, from the record of this one, to learn whether the last
	// candidate the queue landed was a document candidate.
	Document bool
	// DocumentPassedOver says documents were waiting and the document rule was
	// passed over for this one choice, because the last candidate the queue
	// landed was a document candidate and ordinary code was waiting.
	DocumentPassedOver bool
	// Order is every waiting entry's id in the order the rules would take them,
	// the chosen entry first, supposing each lands in turn. It is what yoyo status
	// and the dashboard show the waiting entries in.
	Order []string
}

// QueueReadings remembers, for each work item the queue holds, what the
// tracker last said that the order uses — its priority, whether the Lead
// Product Manager placed it first, and which other items hold it back — and
// when it said it. A pick made while the tracker cannot be read orders entries
// by these, under the same rules as if they had just been read. One value is
// shared by every pass of a watch session, so a pass that cannot read the
// tracker still has what the passes before it read; a process that has read
// nothing yet remembers nothing, and its entries then go last, in admission
// order, as code whose priority could not be read.
type QueueReadings struct {
	mu   sync.Mutex
	seen map[string]queueReading
}

// queueReading is one work item as the tracker last said it.
type queueReading struct {
	priority int
	placed   *time.Time
	// blockers are the items the tracker named as holding this one back, in the
	// listing's own terms; which of them are queue entries is decided at use.
	blockers []string
	readAt   time.Time
}

func (q *QueueReadings) remember(id string, reading queueReading) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.seen == nil {
		q.seen = map[string]queueReading{}
	}
	q.seen[id] = reading
}

func (q *QueueReadings) recall(id string) (queueReading, bool) {
	if q == nil {
		return queueReading{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	reading, ok := q.seen[id]
	return reading, ok
}

// Why says in ordinary words why the rule chose this entry: what the rule is,
// which waiting entries it went ahead of, and what could not be read.
func (c MergeQueueChoice) Why() string {
	var why string
	switch c.Rule {
	case MergeQueueRuleOnlyWaiting:
		why = "it was the only entry waiting"
	case MergeQueueRuleInProgress:
		why = "its checks or review had already begun, and they are not thrown away for another entry"
	case MergeQueueRuleAged:
		why = "it had waited four hours or more, and an entry that has waited that long goes ahead of everything else whatever its priority"
	case MergeQueueRuleUrgent:
		why = "it is priority 0 code, which goes ahead of documents and other code"
	case MergeQueueRuleDocument:
		why = "it is a document, and documents go ahead of code that is not priority 0"
	case MergeQueueRulePriority:
		why = "it is code, and the most urgent priority waiting is taken first"
	case MergeQueueRuleUnreadable:
		why = "it is code whose priority could not be read, so the oldest is taken"
	default:
		why = c.Rule
	}
	if c.PriorityKnown {
		source := "read now"
		if c.PriorityLastRead {
			source = "as last read, because the tracker could not be read this time"
		}
		why += fmt.Sprintf("; its priority is %d (%s)", c.Priority, source)
	}
	if c.DocumentPassedOver {
		why += "; the last change the queue landed was a document, so the documents waiting were passed over this once and code was taken, so that documents never land twice in a row while code waits"
	}
	if c.PlacedFirst {
		why += "; the Lead Product Manager placed its work item first among its priority, which put it ahead of entries admitted earlier"
	}
	if n := len(c.Passed); n > 0 {
		noun := "entries"
		if n == 1 {
			noun = "entry"
		}
		why += fmt.Sprintf("; it went ahead of %d %s admitted earlier (%s)", n, noun, strings.Join(c.Passed, ", "))
	}
	if c.TrackerUnread != "" {
		why += "; " + c.TrackerUnread
	}
	return why
}

// Selection is the choice as a generation records it.
func (c MergeQueueChoice) Selection() *runstate.MergeQueueSelection {
	why := oneline.Bound(c.Why(), runstate.MaxMergeQueueTextBytes)
	return &runstate.MergeQueueSelection{Rule: c.Rule, Why: why, Document: c.Document, PlacedFirst: c.PlacedFirst, DocumentPassedOver: c.DocumentPassedOver}
}

// rankedEntry is a waiting entry with what the order decides by.
type rankedEntry struct {
	entry      runstate.MergeQueueEntry
	inProgress bool
	document   bool
	priority   int
	readable   bool
	// lastRead says the priority is the one remembered from an earlier read,
	// because the tracker could not be read for this entry now.
	lastRead bool
	readAt   time.Time
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
	// lastLandedDocument says the last candidate the queue landed was a
	// document candidate, as the recorded choices say (lastLandingWasDocument).
	lastLandedDocument bool
}

func (o queueOrder) choose(ctx context.Context, waiting []runstate.MergeQueueEntry, started map[string]bool) MergeQueueChoice {
	return o.pick(ctx, waiting, started)
}

// documentKind says from the paths the entry's change makes against its
// recorded base, never from its recorded kind, whether the entry is a
// document, whoever wrote it. It asks the same question of the paths as the
// document landing route (changeIsDocument). A change whose paths cannot be
// read is a code entry, which is checked as code is.
func (o queueOrder) documentKind(ctx context.Context, entry runstate.MergeQueueEntry) bool {
	if o.pipeline.Store == nil || o.paths == nil {
		return false
	}
	run, err := o.pipeline.Store.Load(entry.RunID)
	if err != nil || run.BaseCommit == "" || entry.Head() == "" {
		return false
	}
	changed, err := o.paths(ctx, run.BaseCommit, entry.Head())
	if err != nil {
		return false
	}
	// A blank line in a listing of paths is no path.
	changed = slices.DeleteFunc(slices.Clone(changed), func(changedPath string) bool { return strings.TrimSpace(changedPath) == "" })
	return changeIsDocument(o.pipeline.Config, changed)
}

// entryInProgress is an entry whose newest generation has begun its checks or
// review and still stands: one still earning them, and one that has earned
// both and waits only for the landing step. Either is never stopped for an
// entry that ranks higher, so the checks and review it has are not thrown
// away because the order's answer changed while it was checked, as when
// another entry reaches four hours of waiting in the meantime.
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
		// Whether it is a document is recorded even here, because the choice
		// made after it lands reads that from this record.
		return MergeQueueChoice{Entry: waiting[0], Rule: MergeQueueRuleOnlyWaiting, Document: o.documentKind(ctx, waiting[0]), Order: []string{waiting[0].EntryID}}
	}
	admitted := map[string]struct{}{}
	for _, entry := range waiting {
		admitted[entry.WorkItemID] = struct{}{}
	}
	ranked := make([]rankedEntry, 0, len(waiting))
	blocked := map[string]bool{}
	var firstReadError error
	var remembered, forgotten []string
	read := o.readEntryItems(ctx, waiting)
	for _, entry := range waiting {
		r := rankedEntry{entry: entry, inProgress: started[entry.EntryID]}
		r.document = o.documentKind(ctx, entry)
		var reading queueReading
		item, err := read.item(ctx, o.pipeline.Tracker, entry.WorkItemID)
		if err == nil {
			reading = queueReading{priority: item.Priority, placed: nil, blockers: item.WaitingOn(admitted), readAt: o.now}
			if placement := item.PlacedFirst(); placement != nil {
				at := placement.At
				reading.placed = &at
			}
			o.pipeline.QueueReadings.remember(entry.WorkItemID, reading)
			r.readable = true
		} else {
			if firstReadError == nil {
				firstReadError = err
			}
			var known bool
			if reading, known = o.pipeline.QueueReadings.recall(entry.WorkItemID); known {
				r.readable, r.lastRead, r.readAt = true, true, reading.readAt
				remembered = append(remembered, fmt.Sprintf("%s (priority %d, read at %s)", entry.EntryID, reading.priority, reading.readAt.Local().Format("15:04 MST")))
			} else {
				forgotten = append(forgotten, entry.EntryID)
			}
		}
		if r.readable {
			r.priority, r.placed = reading.priority, reading.placed
			// WaitingOn also names open items that are not in the queue; only
			// another admitted entry's item holds this one back.
			for _, id := range reading.blockers {
				if _, inQueue := admitted[id]; inQueue && id != entry.WorkItemID {
					blocked[entry.EntryID] = true
				}
			}
		}
		ranked = append(ranked, r)
	}
	unread := ""
	if firstReadError != nil {
		unread = fmt.Sprintf("the tracker could not be read: %v", firstReadError)
		if len(remembered) > 0 {
			unread += "; ordered by what was last read for entries " + strings.Join(remembered, ", ")
		}
		if len(forgotten) > 0 {
			unread += "; nothing had been read for entries " + strings.Join(forgotten, ", ") + ", which go last among code, oldest first"
		}
	}
	// An entry waiting on another waiting entry's item waits for it, unless
	// every entry does, which would be a cycle that nothing could break.
	var candidates, held []rankedEntry
	for _, r := range ranked {
		if blocked[r.entry.EntryID] {
			held = append(held, r)
		} else {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		candidates, held = ranked, nil
	}
	chosen, rule, placed, passedOver := rank(candidates, o.now, o.lastLandedDocument)
	// The entries held back by another entry's item follow the rest: they can
	// go only once what holds them has landed.
	order := append(rankAll(candidates, o.now, o.lastLandedDocument), rankAll(held, o.now, false)...)
	return MergeQueueChoice{Entry: chosen.entry, Rule: rule, Passed: queuePassedOver(waiting, chosen.entry), PlacedFirst: placed,
		Priority: chosen.priority, PriorityKnown: chosen.readable, PriorityLastRead: chosen.lastRead, TrackerUnread: unread,
		Document: chosen.document, DocumentPassedOver: passedOver, Order: order}
}

// rankAll is every candidate's entry id in the order the rules take them,
// supposing each lands in turn: after a document, the next is chosen as the
// document rule says it is after a document landing.
func rankAll(candidates []rankedEntry, now time.Time, lastDocument bool) []string {
	remaining := slices.Clone(candidates)
	order := make([]string, 0, len(remaining))
	for len(remaining) > 0 {
		chosen, _, _, _ := rank(remaining, now, lastDocument)
		order = append(order, chosen.entry.EntryID)
		lastDocument = chosen.document
		remaining = slices.DeleteFunc(remaining, func(r rankedEntry) bool { return r.entry.EntryID == chosen.entry.EntryID })
	}
	return order
}

// entryItems is what pick read of the waiting entries' work items together.
//
// The order is chosen every time the worker or the promoter looks for the next
// entry, in every pass of a queue, and a waiting queue goes round once a
// minute (mergeQueueAgainEvery). Reading each entry's item with a `bd show` of
// its own made a queue of ten entries twenty reads a pass, one after another,
// and on bd's one-command-at-a-time store every listing waited behind them
// (docs/diagnoses/yoyodyne-ifd-428-126-4-24-slow-listings.md). Read together,
// a choice asks once.
//
// failed is the error of a batch that went unanswered: every entry is then
// unread with it, rather than read again one at a time from a store that has
// just not answered, and the choice falls to admission order as it does when
// no entry could be read. An entry the batch left out is read on its own,
// which says why.
type entryItems struct {
	items  map[string]beads.WorkItem
	failed error
}

// readEntryItems reads the waiting entries' items in one call where the
// queue's tracker can, and reads nothing ahead where it cannot.
func (o queueOrder) readEntryItems(ctx context.Context, waiting []runstate.MergeQueueEntry) entryItems {
	batch, ok := o.pipeline.Tracker.(itemBatchReader)
	if !ok {
		return entryItems{}
	}
	ids := make([]string, 0, len(waiting))
	for _, entry := range waiting {
		// An id the tracker would refuse to be asked about would refuse the
		// whole batch; it is read on its own, which says so for that entry.
		if beads.ValidIssueID(entry.WorkItemID) {
			ids = append(ids, entry.WorkItemID)
		}
	}
	if len(ids) == 0 {
		return entryItems{}
	}
	items, err := batch.ShowMany(ctx, ids)
	if err != nil {
		return entryItems{failed: err}
	}
	return entryItems{items: items}
}

// item is one entry's work item: as the batch read it, the batch's failure,
// or read on its own where the batch did not answer for it.
func (e entryItems) item(ctx context.Context, tracker WorkTracker, id string) (beads.WorkItem, error) {
	if item, read := e.items[id]; read {
		return item, nil
	}
	if e.failed != nil {
		return beads.WorkItem{}, e.failed
	}
	if tracker == nil {
		return beads.WorkItem{}, errNoQueueTracker
	}
	return tracker.Show(ctx, id)
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
// the first entry the first applicable rule picks, whether a placement decided
// it, and whether the document rule was passed over. candidates is not empty.
//
// Within rule 2, and within each priority under rule 4, an entry whose work
// item was placed first goes ahead, earliest placement first, and the rest
// follow in admission order. A placement never lifts an entry past an entry
// already in progress or one that has waited four hours, nor into a higher
// rule, because each rule's members are chosen before placement is looked at.
//
// lastDocument says the last candidate the queue landed was a document
// candidate. Then, where any code entry waits under rule 4 or rule 5, the
// document rule is passed over for this one choice, so a document candidate
// never lands twice in a row while ordinary code waits.
func rank(candidates []rankedEntry, now time.Time, lastDocument bool) (rankedEntry, string, bool, bool) {
	for _, r := range candidates {
		if r.inProgress {
			return r, MergeQueueRuleInProgress, false, false
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
		return aged[0], MergeQueueRuleAged, false, false
	}
	var urgent []rankedEntry
	for _, r := range candidates {
		if !r.document && r.readable && r.priority == 0 {
			urgent = append(urgent, r)
		}
	}
	if len(urgent) > 0 {
		chosen, placed := placedFirst(urgent)
		return chosen, MergeQueueRuleUrgent, placed, false
	}
	// No priority 0 code is waiting here, so any code entry is ordinary code
	// under rule 4 or rule 5.
	codeWaits := slices.ContainsFunc(candidates, func(r rankedEntry) bool { return !r.document })
	passedOver := false
	for _, r := range candidates {
		if !r.document {
			continue
		}
		if lastDocument && codeWaits {
			passedOver = true
			break
		}
		return r, MergeQueueRuleDocument, false, false
	}
	var code []rankedEntry
	for _, r := range candidates {
		if !r.document && r.readable {
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
		return chosen, MergeQueueRulePriority, placed, passedOver
	}
	for _, r := range candidates {
		if !r.document {
			return r, MergeQueueRuleUnreadable, false, passedOver
		}
	}
	return candidates[0], MergeQueueRuleUnreadable, false, false
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

// queueLandingRecords is what the order reads the queue's landings and the
// choices recorded on their candidates from. The worker's and the promoter's
// records both satisfy it.
type queueLandingRecords interface {
	Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error)
	Generations(key runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueGeneration, error)
	Landing(key runstate.MergeQueueKey, entryID string) (runstate.MergeQueueLanding, bool, error)
}

// lastLandingWasDocument says whether the last candidate the queue landed on
// key's target was a document candidate. It is read from the records alone —
// the landings the queue confirmed, and the choice recorded on the candidate
// that landed — so a process that has just started chooses as the one before
// it would have. Where after names an entry, the answer is for the choice made
// as if that entry had just landed (checking ahead, mergequeueahead.go).
//
// A record that cannot be read, and a queue that has landed nothing, answer
// false: the document rule is then applied as written rather than passed over.
func lastLandingWasDocument(records queueLandingRecords, key runstate.MergeQueueKey, after string) bool {
	if records == nil {
		return false
	}
	if after != "" {
		generations, err := records.Generations(key, after)
		if err != nil || len(generations) == 0 {
			return false
		}
		return selectedDocument(generations[len(generations)-1].Selection)
	}
	entries, err := records.Entries(key)
	if err != nil {
		return false
	}
	var latestEntry string
	var latestAt time.Time
	var latestGeneration uint64
	for _, entry := range entries {
		landing, found, err := records.Landing(key, entry.EntryID)
		if err != nil || !found || landing.Completion == nil {
			continue
		}
		completion := landing.Completion
		at := completion.QueueAt
		for _, attempt := range landing.Attempts {
			if attempt.Number == completion.Attempt && attempt.Landed != nil {
				at = attempt.Landed.LandedAt()
			}
		}
		if latestEntry == "" || at.After(latestAt) {
			latestEntry, latestAt, latestGeneration = entry.EntryID, at, completion.Generation
		}
	}
	if latestEntry == "" {
		return false
	}
	generations, err := records.Generations(key, latestEntry)
	if err != nil || len(generations) == 0 {
		return false
	}
	landed := generations[len(generations)-1]
	for _, generation := range generations {
		if generation.Number == latestGeneration {
			landed = generation
		}
	}
	return selectedDocument(landed.Selection)
}

// selectedDocument says a recorded choice chose a document entry. A choice
// recorded before the record said so is read by its rule.
func selectedDocument(selection *runstate.MergeQueueSelection) bool {
	return selection != nil && (selection.Document || selection.Rule == MergeQueueRuleDocument)
}
