package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
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
		paths[e.id] = e.paths
		started[e.id] = e.started
		if !e.unread {
			item := beads.WorkItem{ID: "item-" + e.id, Priority: e.priority}
			if e.blocker != "" {
				item.Dependencies = []beads.Dependency{{ID: "item-" + e.blocker, Type: beads.BlocksDependency, Status: "open"}}
			}
			items["item-"+e.id] = item
		}
	}
	var cfg config.Config
	cfg.Product.Specifications, cfg.Product.Designs, cfg.Product.Decisions, cfg.Product.Invariants = "docs/product", "docs/designs", "docs/decisions", "docs/decisions/invariants"
	w := MergeQueueWorker{
		Pipeline:   &Pipeline{Config: cfg, Store: orderRuns{}, Tracker: orderTracker{items: items, down: !tracker}, Clock: fixedOrderClock{}},
		Candidates: orderPaths{paths: paths},
	}
	return w.choose(context.Background(), waiting, started)
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

func TestATrackerThatCannotBeReadGivesAdmissionOrderAndSaysSo(t *testing.T) {
	got := chooseFrom(t, false,
		orderEntry{id: "p3", paths: code, priority: 3},
		orderEntry{id: "p0", paths: code, priority: 0},
	)
	if got.Entry.EntryID != "p3" || got.Rule != MergeQueueRuleAdmission || got.TrackerUnread == "" {
		t.Fatalf("chose %s by %q (%q), want admission order with the tracker named", got.Entry.EntryID, got.Rule, got.TrackerUnread)
	}
}

func TestAnEntryRecordedAsADocumentIsACodeEntryWhenItsPathsAreCode(t *testing.T) {
	// Both entries here are recorded as the same kind (item id equal to run id
	// would mark a document); only the paths decide.
	got := chooseFrom(t, true,
		orderEntry{id: "mixed", paths: append(append([]string(nil), intent...), code...), priority: 3},
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
