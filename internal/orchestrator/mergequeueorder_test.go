package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var orderNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// orderTracker answers each item's priority and blockers from a table.
type orderTracker struct {
	WorkTracker
	items map[string]beads.WorkItem
	down  bool
}

func (t orderTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	item, ok := t.items[id]
	if t.down || !ok {
		return beads.WorkItem{}, errors.New("the tracker is not answering")
	}
	return item, nil
}

// orderRuns is the run records: each entry's run records the base commit.
type orderRuns struct {
	StateStore
}

func (orderRuns) Load(runID string) (runstate.State, error) {
	return runstate.State{RunID: runID, BaseCommit: "base-" + runID}, nil
}

// orderPaths answers the paths each entry's change makes, by its head.
type orderPaths struct {
	MergeQueueCandidates
	paths map[string][]string
}

func (c orderPaths) CandidatePaths(_ context.Context, _, head string) ([]string, error) {
	return c.paths[head], nil
}

type orderEntry struct {
	id       string
	age      time.Duration
	priority int
	paths    []string
	unread   bool
	blocker  string
	started  bool
	// recordedAsDocument gives the entry the identifiers a document's entry is
	// recorded under (its item is its run's own identifier).
	recordedAsDocument bool
	// outsider is an open item outside the queue its item depends on.
	outsider string
	// placedAgo, where not zero, is how long ago the Lead Product Manager
	// placed the entry's item first among its priority.
	placedAgo time.Duration
}

func chooseFrom(t *testing.T, tracker bool, entries ...orderEntry) MergeQueueChoice {
	t.Helper()
	items, paths := map[string]beads.WorkItem{}, map[string][]string{}
	var waiting []runstate.MergeQueueEntry
	started := map[string]bool{}
	for i, e := range entries {
		waiting = append(waiting, runstate.MergeQueueEntry{
			EntryID: e.id, Order: uint64(i + 1), WorkItemID: "item-" + e.id, RunID: e.id, ApprovedHead: e.id,
			AdmittedAt: orderNow.Add(-e.age),
		})
		if e.recordedAsDocument {
			waiting[i].WorkItemID = e.id
		}
		paths[e.id] = e.paths
		started[e.id] = e.started
		if !e.unread {
			item := beads.WorkItem{ID: "item-" + e.id, Priority: e.priority}
			if e.recordedAsDocument {
				item.ID = e.id
			}
			if e.blocker != "" {
				item.Dependencies = []beads.Dependency{{ID: "item-" + e.blocker, Type: beads.BlocksDependency, Status: "open"}}
			}
			if e.placedAgo != 0 {
				item.Placement = &beads.Placement{By: domain.RoleProductManager, At: orderNow.Add(-e.placedAgo), Priority: e.priority, Reason: "promised first"}
			}
			if e.outsider != "" {
				item.Dependencies = append(item.Dependencies, beads.Dependency{ID: e.outsider, Type: beads.BlocksDependency, Status: "open"})
			}
			items[waiting[i].WorkItemID] = item
		}
	}
	var cfg config.Config
	cfg.Product.Specifications, cfg.Product.Designs, cfg.Product.Decisions, cfg.Product.Invariants = "docs/product", "docs/designs", "docs/decisions", "docs/decisions/invariants"
	o := queueOrder{
		pipeline: &Pipeline{Config: cfg, Store: orderRuns{}, Tracker: orderTracker{items: items, down: !tracker}},
		paths:    orderPaths{paths: paths}.CandidatePaths,
		now:      orderNow,
	}
	return o.choose(context.Background(), waiting, started)
}

var (
	code   = []string{"internal/x.go"}
	design = []string{"docs/designs/a.md"}
	intent = []string{"docs/product/goals/v1-goals.md"}
)

func TestAPriorityZeroCodeEntryGoesAheadOfAnEarlierDocumentAndOtherPriorities(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "doc", paths: intent},
		orderEntry{id: "p2", paths: code, priority: 2},
		orderEntry{id: "p0", paths: code, priority: 0},
	)
	if got.Entry.EntryID != "p0" || got.Rule != MergeQueueRuleUrgent {
		t.Fatalf("chose %s by %q, want p0 by the urgent rule", got.Entry.EntryID, got.Rule)
	}
	if strings.Join(got.Passed, ",") != "doc,p2" {
		t.Fatalf("passed over %v, want doc and p2", got.Passed)
	}
}

func TestTheArchitectsOldestDocumentTravelsWithPriorityZeroCodeInAdmissionOrder(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "intent", paths: intent},
		orderEntry{id: "p1", paths: code, priority: 1},
		orderEntry{id: "design", paths: design},
		orderEntry{id: "p0", paths: code, priority: 0},
	)
	if got.Entry.EntryID != "design" || got.Rule != MergeQueueRuleUrgent {
		t.Fatalf("chose %s by %q, want the architect's design first, as admitted before p0", got.Entry.EntryID, got.Rule)
	}
	// With the priority 0 code admitted first, it goes first and the design second.
	got = chooseFrom(t, true,
		orderEntry{id: "p0", paths: code, priority: 0},
		orderEntry{id: "design", paths: design},
	)
	if got.Entry.EntryID != "p0" {
		t.Fatalf("chose %s, want p0 admitted first", got.Entry.EntryID)
	}
}

func TestOtherDocumentsGoAheadOfCodeThatIsNotPriorityZero(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "p1", paths: code, priority: 1},
		orderEntry{id: "intent", paths: intent},
	)
	if got.Entry.EntryID != "intent" || got.Rule != MergeQueueRuleDocument {
		t.Fatalf("chose %s by %q, want the document", got.Entry.EntryID, got.Rule)
	}
}

func TestOtherCodeGoesByPriorityThenAdmissionAndUnreadableLast(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "unread", paths: code, unread: true},
		orderEntry{id: "p3", paths: code, priority: 3},
		orderEntry{id: "p2b", paths: code, priority: 2},
		orderEntry{id: "p2a", paths: code, priority: 2},
	)
	if got.Entry.EntryID != "p2b" || got.Rule != MergeQueueRulePriority {
		t.Fatalf("chose %s by %q, want p2b (priority 2, admitted first of those)", got.Entry.EntryID, got.Rule)
	}
	got = chooseFrom(t, true,
		orderEntry{id: "unread", paths: code, unread: true},
		orderEntry{id: "p4", paths: code, priority: 4},
	)
	if got.Entry.EntryID != "p4" {
		t.Fatalf("chose %s, want the readable one ahead of the unreadable", got.Entry.EntryID)
	}
	got = chooseFrom(t, true,
		orderEntry{id: "first", paths: code, unread: true},
		orderEntry{id: "second", paths: code, unread: true},
		orderEntry{id: "doc", paths: intent},
	)
	if got.Entry.EntryID != "doc" {
		t.Fatalf("chose %s, want a document ahead of code whose priority is unreadable", got.Entry.EntryID)
	}
	got = chooseFrom(t, true,
		orderEntry{id: "first", paths: code, unread: true},
		orderEntry{id: "second", paths: code, unread: true},
		orderEntry{id: "p2", paths: code, priority: 2},
	)
	if got.Entry.EntryID != "p2" {
		t.Fatalf("chose %s", got.Entry.EntryID)
	}
}

func TestAnEntryWaitingEightHoursGoesBeforePriorityZeroCode(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "p0", paths: code, priority: 0, age: time.Hour},
		orderEntry{id: "young-aged", paths: code, priority: 4, age: 9 * time.Hour},
		orderEntry{id: "oldest", paths: intent, age: 20 * time.Hour},
	)
	if got.Entry.EntryID != "oldest" || got.Rule != MergeQueueRuleAged {
		t.Fatalf("chose %s by %q, want the oldest aged entry", got.Entry.EntryID, got.Rule)
	}
}

func TestAPriorityRaisedAfterAdmissionChangesTheNextChoice(t *testing.T) {
	entries := []orderEntry{{id: "a", paths: code, priority: 2}, {id: "b", paths: code, priority: 2}}
	if got := chooseFrom(t, true, entries...); got.Entry.EntryID != "a" {
		t.Fatalf("chose %s, want a", got.Entry.EntryID)
	}
	entries[1].priority = 0
	if got := chooseFrom(t, true, entries...); got.Entry.EntryID != "b" {
		t.Fatalf("chose %s, want b once its priority is 0", got.Entry.EntryID)
	}
}

func TestAnEntryBeingCheckedIsNotPassedForAHigherOne(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "p3", paths: code, priority: 3, started: true},
		orderEntry{id: "p0", paths: code, priority: 0},
	)
	if got.Entry.EntryID != "p3" || got.Rule != MergeQueueRuleInProgress {
		t.Fatalf("chose %s by %q, want the entry already being checked", got.Entry.EntryID, got.Rule)
	}
}

func TestAnEntryWhoseItemDependsOnAnAdmittedEntryWaitsForIt(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "child", paths: code, priority: 0, blocker: "parent"},
		orderEntry{id: "parent", paths: code, priority: 3},
	)
	if got.Entry.EntryID != "parent" {
		t.Fatalf("chose %s, want the entry the other depends on", got.Entry.EntryID)
	}
}

func TestADependencyOnAnOpenItemOutsideTheQueueHoldsNothingBack(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "a", paths: code, priority: 3, outsider: "never-admitted"},
		orderEntry{id: "b", paths: code, priority: 4},
	)
	if got.Entry.EntryID != "a" {
		t.Fatalf("chose %s, want a: an item outside the queue does not hold it back", got.Entry.EntryID)
	}
}

func TestATrackerThatCannotBeReadGivesAdmissionOrderAndSaysSo(t *testing.T) {
	got := chooseFrom(t, false,
		orderEntry{id: "p3", paths: code, priority: 3},
		orderEntry{id: "p0", paths: code, priority: 0},
	)
	if got.Entry.EntryID != "p3" || got.Rule != MergeQueueRuleUnreadable || got.TrackerUnread == "" {
		t.Fatalf("chose %s by %q (%q), want the oldest, as code whose priority could not be read, with the tracker named", got.Entry.EntryID, got.Rule, got.TrackerUnread)
	}
}

func TestAnEntryRecordedAsADocumentIsACodeEntryWhenItsPathsAreCode(t *testing.T) {
	// Both entries here are recorded as the same kind (item id equal to run id
	// would mark a document); only the paths decide.
	got := chooseFrom(t, true,
		orderEntry{id: "mixed", paths: append(append([]string(nil), intent...), code...), priority: 3, recordedAsDocument: true},
		orderEntry{id: "p1", paths: code, priority: 1},
	)
	if got.Entry.EntryID != "p1" || got.Rule != MergeQueueRulePriority {
		t.Fatalf("chose %s by %q, want the priority 1 code ahead of a change that also touches code", got.Entry.EntryID, got.Rule)
	}
}

func TestTheChoiceRecordsTheEntriesItPassedOver(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "a", paths: code, priority: 2},
		orderEntry{id: "b", paths: code, priority: 1},
		orderEntry{id: "c", paths: code, priority: 0},
	)
	if got.Entry.EntryID != "c" || strings.Join(got.Passed, ",") != "a,b" {
		t.Fatalf("chose %s passing %v", got.Entry.EntryID, got.Passed)
	}
}

type fixedOrderClock struct{}

func (fixedOrderClock) Now() time.Time { return orderNow }

func TestAPlacedPriorityZeroEntryGoesAheadOfAnOlderUnplacedOne(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "older", paths: code, priority: 0, age: 3 * time.Hour},
		orderEntry{id: "placed", paths: code, priority: 0, age: time.Hour, placedAgo: 30 * time.Minute},
	)
	if got.Entry.EntryID != "placed" || got.Rule != MergeQueueRuleUrgent || !got.PlacedFirst {
		t.Fatalf("chose %s by %q (placement decided: %v), want the placed entry chosen by its placement", got.Entry.EntryID, got.Rule, got.PlacedFirst)
	}
}

func TestTwoPlacedEntriesGoInTheOrderTheyWerePlaced(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "placed-later", paths: code, priority: 0, age: 3 * time.Hour, placedAgo: time.Hour},
		orderEntry{id: "placed-earlier", paths: code, priority: 0, age: 2 * time.Hour, placedAgo: 2 * time.Hour},
	)
	if got.Entry.EntryID != "placed-earlier" || !got.PlacedFirst {
		t.Fatalf("chose %s (placement decided: %v), want the entry placed earlier", got.Entry.EntryID, got.PlacedFirst)
	}
	// Within one priority under the code rule, the same holds.
	got = chooseFrom(t, true,
		orderEntry{id: "p2", paths: code, priority: 2, age: 3 * time.Hour},
		orderEntry{id: "p2-placed-later", paths: code, priority: 2, age: 2 * time.Hour, placedAgo: time.Hour},
		orderEntry{id: "p2-placed-earlier", paths: code, priority: 2, age: time.Hour, placedAgo: 2 * time.Hour},
		orderEntry{id: "p3-placed", paths: code, priority: 3, age: 4 * time.Hour, placedAgo: 4 * time.Hour},
	)
	if got.Entry.EntryID != "p2-placed-earlier" || got.Rule != MergeQueueRulePriority || !got.PlacedFirst {
		t.Fatalf("chose %s by %q (placement decided: %v), want the earliest placed priority 2 entry", got.Entry.EntryID, got.Rule, got.PlacedFirst)
	}
}

func TestAPlacementMadeAfterAdmissionChangesTheNextChoice(t *testing.T) {
	entries := []orderEntry{{id: "a", paths: code, priority: 0, age: 2 * time.Hour}, {id: "b", paths: code, priority: 0, age: time.Hour}}
	if got := chooseFrom(t, true, entries...); got.Entry.EntryID != "a" || got.PlacedFirst {
		t.Fatalf("chose %s (placement decided: %v), want a in admission order", got.Entry.EntryID, got.PlacedFirst)
	}
	entries[1].placedAgo = time.Minute
	if got := chooseFrom(t, true, entries...); got.Entry.EntryID != "b" || !got.PlacedFirst {
		t.Fatalf("chose %s (placement decided: %v), want b once it is placed first", got.Entry.EntryID, got.PlacedFirst)
	}
}

func TestAPlacedEntryNeverGoesAheadOfOneWaitingEightHours(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "placed", paths: code, priority: 0, age: time.Hour, placedAgo: 30 * time.Minute},
		orderEntry{id: "aged", paths: code, priority: 4, age: 9 * time.Hour},
	)
	if got.Entry.EntryID != "aged" || got.Rule != MergeQueueRuleAged || got.PlacedFirst {
		t.Fatalf("chose %s by %q (placement decided: %v), want the entry waiting eight hours", got.Entry.EntryID, got.Rule, got.PlacedFirst)
	}
}

func TestAPlacementNeverLiftsAnEntryIntoAHigherRuleOrPastOneInProgress(t *testing.T) {
	got := chooseFrom(t, true,
		orderEntry{id: "p0", paths: code, priority: 0},
		orderEntry{id: "p2-placed", paths: code, priority: 2, placedAgo: time.Hour},
	)
	if got.Entry.EntryID != "p0" || got.PlacedFirst {
		t.Fatalf("chose %s (placement decided: %v), want priority 0 ahead of a placed priority 2 entry", got.Entry.EntryID, got.PlacedFirst)
	}
	got = chooseFrom(t, true,
		orderEntry{id: "checking", paths: code, priority: 3, started: true},
		orderEntry{id: "p0-placed", paths: code, priority: 0, placedAgo: time.Hour},
	)
	if got.Entry.EntryID != "checking" || got.Rule != MergeQueueRuleInProgress {
		t.Fatalf("chose %s by %q, want the entry already being checked", got.Entry.EntryID, got.Rule)
	}
}

func TestAVerifiedCandidateCountsAsInProgressAndOneCheckedAheadOnlyOnceItsBaseHasLanded(t *testing.T) {
	finished := orderNow
	verified := runstate.MergeQueueGeneration{
		Number:   1,
		CheckRun: &runstate.MergeQueueCheckEvidence{FinishedAt: &finished},
		Review:   &runstate.MergeQueueReviewEvidence{FinishedAt: &finished},
	}
	if !entryInProgress([]runstate.MergeQueueGeneration{verified}, map[string]bool{}) {
		t.Fatal("a candidate that passed its checks and review and waits to land is not counted as in progress")
	}
	ahead := verified
	ahead.BuiltAhead = &runstate.MergeQueueAhead{EntryID: "base-entry"}
	if entryInProgress([]runstate.MergeQueueGeneration{ahead}, map[string]bool{"base-entry": true}) {
		t.Fatal("a candidate checked ahead of an entry that still waits is counted as in progress")
	}
	if !entryInProgress([]runstate.MergeQueueGeneration{ahead}, map[string]bool{}) {
		t.Fatal("a candidate checked ahead of an entry that has landed is not counted as in progress")
	}
	if entryInProgress([]runstate.MergeQueueGeneration{{Number: 1}}, map[string]bool{}) {
		t.Fatal("a candidate whose checks have not begun is counted as in progress")
	}
}

// batchOrderTracker is orderTracker that can also read several items in one
// call, as beads.Client can, and counts what it is asked.
type batchOrderTracker struct {
	orderTracker
	batches *[][]string
	singles *[]string
}

func (t batchOrderTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	*t.singles = append(*t.singles, id)
	return t.orderTracker.Show(ctx, id)
}

func (t batchOrderTracker) ShowMany(_ context.Context, ids []string) (map[string]beads.WorkItem, error) {
	*t.batches = append(*t.batches, append([]string(nil), ids...))
	if t.down {
		return nil, errors.New("bd show: timed out waiting for the store")
	}
	read := map[string]beads.WorkItem{}
	for _, id := range ids {
		if item, found := t.items[id]; found {
			read[id] = item
		}
	}
	return read, nil
}

// The order is chosen by the worker and the promoter in every pass of a queue,
// once a minute while it waits, and read each entry's item with a `bd show` of
// its own, which on bd's one-command-at-a-time store kept every listing waiting
// behind the queue (yoyodyne-ifd.428.126.4.24). Through the harness's own
// tracker, which is how the watch wires the queue, the entries' items are read
// in one call and choose as they did read alone; an item the batch left out is
// read alone, and a batch that went unanswered chooses in admission order,
// saying so, without asking each entry again.
func TestTheQueueReadsItsEntriesItemsInOneCall(t *testing.T) {
	items := map[string]beads.WorkItem{
		"item-p3": {ID: "item-p3", Priority: 3},
		"item-p1": {ID: "item-p1", Priority: 1},
		"item-p2": {ID: "item-p2", Priority: 2},
	}
	waiting := []runstate.MergeQueueEntry{
		{EntryID: "p3", Order: 1, WorkItemID: "item-p3", RunID: "p3", ApprovedHead: "p3", AdmittedAt: orderNow.Add(-time.Hour)},
		{EntryID: "p1", Order: 2, WorkItemID: "item-p1", RunID: "p1", ApprovedHead: "p1", AdmittedAt: orderNow.Add(-time.Hour)},
		{EntryID: "p2", Order: 3, WorkItemID: "item-p2", RunID: "p2", ApprovedHead: "p2", AdmittedAt: orderNow.Add(-time.Hour)},
		{EntryID: "gone", Order: 4, WorkItemID: "item-gone", RunID: "gone", ApprovedHead: "gone", AdmittedAt: orderNow.Add(-time.Hour)},
	}
	choose := func(down bool) (MergeQueueChoice, [][]string, []string) {
		var batches [][]string
		var singles []string
		tracker := batchOrderTracker{orderTracker: orderTracker{items: items, down: down}, batches: &batches, singles: &singles}
		o := queueOrder{
			pipeline: &Pipeline{Store: orderRuns{}, Tracker: HarnessRunItems{WorkTracker: tracker, Runs: orderRuns{}}},
			paths:    orderPaths{paths: map[string][]string{"p3": code, "p1": code, "p2": code, "gone": code}}.CandidatePaths,
			now:      orderNow,
		}
		return o.choose(context.Background(), waiting, map[string]bool{}), batches, singles
	}

	got, batches, singles := choose(false)
	if got.Entry.EntryID != "p1" || got.Rule != MergeQueueRulePriority {
		t.Fatalf("chose %s by %q, want p1 by priority", got.Entry.EntryID, got.Rule)
	}
	if len(batches) != 1 || len(batches[0]) != 4 {
		t.Fatalf("batches = %v, want every entry's item read in one call", batches)
	}
	if len(singles) != 1 || singles[0] != "item-gone" {
		t.Fatalf("read %v alone, want only the item the batch left out", singles)
	}

	got, batches, singles = choose(true)
	if got.Entry.EntryID != "p3" || got.Rule != MergeQueueRuleUnreadable || !strings.Contains(got.TrackerUnread, "timed out waiting for the store") {
		t.Fatalf("chose %s by %q (%q), want the oldest naming the tracker's error", got.Entry.EntryID, got.Rule, got.TrackerUnread)
	}
	if len(batches) != 1 || len(singles) != 0 {
		t.Fatalf("asked %d batches and %v alone, want one batch and nothing after it", len(batches), singles)
	}
}

// A pick made while the tracker cannot be read orders entries by the priority
// last read for each, rather than by admission alone, so a priority 0 fix is
// not passed over for an older priority 1 change because the tracker was slow.
func TestAnUnreadableTrackerOrdersEntriesByTheirLastReadPriority(t *testing.T) {
	items := map[string]beads.WorkItem{
		"item-p1": {ID: "item-p1", Priority: 1},
		"item-p0": {ID: "item-p0", Priority: 0},
	}
	waiting := []runstate.MergeQueueEntry{
		{EntryID: "p1", Order: 1, WorkItemID: "item-p1", RunID: "p1", ApprovedHead: "p1", AdmittedAt: orderNow.Add(-2 * time.Hour)},
		{EntryID: "p0", Order: 2, WorkItemID: "item-p0", RunID: "p0", ApprovedHead: "p0", AdmittedAt: orderNow.Add(-time.Hour)},
	}
	memory := &QueueReadings{}
	choose := func(down bool, memory *QueueReadings) MergeQueueChoice {
		o := queueOrder{
			pipeline: &Pipeline{Store: orderRuns{}, QueueReadings: memory, Tracker: orderTracker{items: items, down: down}},
			paths:    orderPaths{paths: map[string][]string{"p1": code, "p0": code}}.CandidatePaths,
			now:      orderNow,
		}
		return o.choose(context.Background(), waiting, map[string]bool{})
	}

	// Nothing has been read yet: admission order, and it says so.
	if got := choose(true, memory); got.Entry.EntryID != "p1" || got.Rule != MergeQueueRuleUnreadable || got.TrackerUnread == "" {
		t.Fatalf("chose %s by %q (%q), want the oldest before any priority was read", got.Entry.EntryID, got.Rule, got.TrackerUnread)
	}
	// A read while the tracker answers is remembered.
	if got := choose(false, memory); got.Entry.EntryID != "p0" || got.Rule != MergeQueueRuleUrgent || got.TrackerUnread != "" {
		t.Fatalf("chose %s by %q (%q), want the priority 0 entry with the tracker read", got.Entry.EntryID, got.Rule, got.TrackerUnread)
	}
	got := choose(true, memory)
	if got.Entry.EntryID != "p0" || got.Rule != MergeQueueRuleUrgent || !got.PriorityKnown || !got.PriorityLastRead {
		t.Fatalf("chose %s by %q, want the priority 0 entry by its last read priority", got.Entry.EntryID, got.Rule)
	}
	why := got.Why()
	for _, want := range []string{"priority 0 code", "as last read", "the tracker could not be read", "ordered by what was last read for entries", "p0 (priority 0, read at"} {
		if !strings.Contains(why, want) {
			t.Fatalf("reason %q does not say %q", why, want)
		}
	}
	if got.Selection() == nil || got.Selection().Rule != MergeQueueRuleUrgent || got.Selection().Why != strings.Join(strings.Fields(why), " ") {
		t.Fatalf("selection = %#v, want the rule and reason recorded", got.Selection())
	}
	// A pick with the tracker unreadable and nothing remembered for p0 still
	// takes the entry whose priority is remembered.
	other := &QueueReadings{}
	other.remember("item-p1", queueReading{priority: 1, readAt: orderNow})
	if got := choose(true, other); got.Entry.EntryID != "p1" || got.Rule != MergeQueueRulePriority {
		t.Fatalf("chose %s by %q, want the entry with a remembered priority", got.Entry.EntryID, got.Rule)
	}
}

func TestEveryRuleThatChoosesAnEntryHasAReasonInPlainWords(t *testing.T) {
	for rule, want := range map[string]string{
		MergeQueueRuleOnlyWaiting: "only entry waiting",
		MergeQueueRuleInProgress:  "already begun",
		MergeQueueRuleAged:        "waited eight hours or more",
		MergeQueueRuleUrgent:      "priority 0 code",
		MergeQueueRuleDocument:    "it is a document",
		MergeQueueRulePriority:    "most urgent priority",
		MergeQueueRuleUnreadable:  "priority could not be read",
			} {
		choice := MergeQueueChoice{Rule: rule, Passed: []string{"a", "b"}}
		why := choice.Why()
		if !strings.Contains(why, want) || !strings.Contains(why, "went ahead of 2 entries admitted earlier (a, b)") {
			t.Errorf("reason for %q = %q, want it to say %q and what it passed over", rule, why, want)
		}
		if selection := choice.Selection(); selection == nil || selection.Rule != rule || selection.Why == "" {
			t.Errorf("selection for %q = %#v, want the rule and a reason", rule, selection)
		}
	}
}

// A pick with the tracker unreadable applies every rule as it would have with
// the tracker read: an entry with nothing remembered goes last among code, and
// an aged entry or a document is still taken ahead of it.
func TestAnUnreadableTrackerWithNothingRememberedStillAppliesTheOtherRules(t *testing.T) {
	got := chooseFrom(t, false,
		orderEntry{id: "code", paths: code, age: time.Hour},
		orderEntry{id: "doc", paths: intent, age: time.Hour},
	)
	if got.Entry.EntryID != "doc" || got.Rule != MergeQueueRuleDocument || got.TrackerUnread == "" {
		t.Fatalf("chose %s by %q, want the document ahead of code whose priority is unknown", got.Entry.EntryID, got.Rule)
	}
	got = chooseFrom(t, false,
		orderEntry{id: "new", paths: code, age: time.Hour},
		orderEntry{id: "aged", paths: code, age: 9 * time.Hour},
	)
	if got.Entry.EntryID != "aged" || got.Rule != MergeQueueRuleAged {
		t.Fatalf("chose %s by %q, want the entry that waited eight hours", got.Entry.EntryID, got.Rule)
	}
	got = chooseFrom(t, false,
		orderEntry{id: "new", paths: code, age: time.Hour},
		orderEntry{id: "checking", paths: code, age: 2 * time.Hour, started: true},
	)
	if got.Entry.EntryID != "checking" || got.Rule != MergeQueueRuleInProgress {
		t.Fatalf("chose %s by %q, want the entry already being checked", got.Entry.EntryID, got.Rule)
	}
}

// Placement and dependencies are remembered with the priority, and a pick with
// the tracker unreadable uses them, and names the entries it ordered that way
// with when each was read.
func TestAnUnreadableTrackerUsesRememberedPlacementAndDependencies(t *testing.T) {
	readAt := orderNow.Add(-10 * time.Minute)
	placedAt := orderNow.Add(-time.Hour)
	waiting := []runstate.MergeQueueEntry{
		{EntryID: "a", Order: 1, WorkItemID: "item-a", RunID: "a", ApprovedHead: "a", AdmittedAt: orderNow.Add(-3 * time.Hour)},
		{EntryID: "b", Order: 2, WorkItemID: "item-b", RunID: "b", ApprovedHead: "b", AdmittedAt: orderNow.Add(-2 * time.Hour)},
		{EntryID: "c", Order: 3, WorkItemID: "item-c", RunID: "c", ApprovedHead: "c", AdmittedAt: orderNow.Add(-time.Hour)},
	}
	pick := func(memory *QueueReadings) MergeQueueChoice {
		o := queueOrder{
			pipeline: &Pipeline{Store: orderRuns{}, QueueReadings: memory, Tracker: orderTracker{down: true}},
			paths:    orderPaths{paths: map[string][]string{"a": code, "b": code, "c": code}}.CandidatePaths,
			now:      orderNow,
		}
		return o.choose(context.Background(), waiting, map[string]bool{})
	}

	memory := &QueueReadings{}
	memory.remember("item-a", queueReading{priority: 2, readAt: readAt})
	memory.remember("item-b", queueReading{priority: 2, placed: &placedAt, readAt: readAt})
	memory.remember("item-c", queueReading{priority: 2, readAt: readAt})
	got := pick(memory)
	if got.Entry.EntryID != "b" || got.Rule != MergeQueueRulePriority || !got.PlacedFirst {
		t.Fatalf("chose %s by %q (placed first %v), want the remembered placement to put b first within its priority", got.Entry.EntryID, got.Rule, got.PlacedFirst)
	}
	if want := "b (priority 2, read at " + readAt.Local().Format("15:04 MST") + ")"; !strings.Contains(got.TrackerUnread, want) || !strings.Contains(got.Why(), want) {
		t.Fatalf("record %q does not name the entry and when it was read, want %q", got.Why(), want)
	}

	memory = &QueueReadings{}
	memory.remember("item-a", queueReading{priority: 2, blockers: []string{"item-b"}, readAt: readAt})
	memory.remember("item-b", queueReading{priority: 2, readAt: readAt})
	if got := pick(memory); got.Entry.EntryID != "b" {
		t.Fatalf("chose %s, want b: a remembered dependency still holds a back", got.Entry.EntryID)
	}

	// An entry nothing is remembered for goes last, and the record says so.
	memory = &QueueReadings{}
	memory.remember("item-b", queueReading{priority: 3, readAt: readAt})
	got = pick(memory)
	if got.Entry.EntryID != "b" || !strings.Contains(got.TrackerUnread, "nothing had been read for entries a, c") {
		t.Fatalf("chose %s (%q), want b ahead of entries with nothing remembered, named", got.Entry.EntryID, got.TrackerUnread)
	}
}
