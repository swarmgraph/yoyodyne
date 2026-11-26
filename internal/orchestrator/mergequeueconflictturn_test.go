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

// The next turn a code change brought up to date after a conflict at landing
// takes (conflictTurns): decision 1 of
// docs/designs/approved-changes-that-no-longer-merge.md.

// turnRuns is the run records, each with the conflict hand-backs it was given.
type turnRuns struct {
	StateStore
	handedBack map[string][]time.Time
}

func (r turnRuns) Load(runID string) (runstate.State, error) {
	state := runstate.State{RunID: runID, BaseCommit: "base-" + runID}
	for i, at := range r.handedBack[runID] {
		state.ConflictHandbacks = append(state.ConflictHandbacks, runstate.ConflictHandback{
			ID: runID + "-conflict", Number: i + 1, From: runstate.ConflictFromMergeQueue, At: at, TargetBranch: "main",
		})
	}
	return state, nil
}

// turnRecords is the queue's records: every entry ever admitted, which of them
// had a candidate built, and the landing record of each that was handed back.
type turnRecords struct {
	entries  []runstate.MergeQueueEntry
	built    map[string]bool
	landings map[string]runstate.MergeQueueLanding
	down     bool
}

func (r turnRecords) Entries(runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error) {
	if r.down {
		return nil, errors.New("the queue's record will not read")
	}
	return r.entries, nil
}

func (r turnRecords) Generations(_ runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueGeneration, error) {
	if r.built[entryID] {
		return []runstate.MergeQueueGeneration{{Number: 1}}, nil
	}
	return nil, nil
}

func (r turnRecords) Landing(_ runstate.MergeQueueKey, entryID string) (runstate.MergeQueueLanding, bool, error) {
	landing, found := r.landings[entryID]
	return landing, found, nil
}

// turnEntry is one entry ever admitted. Entries that do not wait are the
// earlier entries of a run that has since rejoined.
type turnEntry struct {
	id, run  string
	age      time.Duration
	priority int
	paths    []string
	waits    bool
	built    bool
	started  bool
	blocker  string
}

type turnQueue struct {
	entries    []turnEntry
	handedBack map[string][]time.Time
	landings   map[string]runstate.MergeQueueLanding
	down       bool
}

func (q turnQueue) choose(t *testing.T) MergeQueueChoice {
	t.Helper()
	items, paths := map[string]beads.WorkItem{}, map[string][]string{}
	records := turnRecords{built: map[string]bool{}, landings: q.landings, down: q.down}
	var waiting []runstate.MergeQueueEntry
	started := map[string]bool{}
	for i, e := range q.entries {
		entry := runstate.MergeQueueEntry{
			EntryID: e.id, Order: uint64(i + 1), TargetBranch: "main", WorkItemID: "item-" + e.run, RunID: e.run, ApprovedHead: e.id,
			AdmittedAt: orderNow.Add(-e.age),
		}
		for _, earlier := range records.entries {
			if earlier.RunID == e.run {
				entry.Supersedes = earlier.EntryID
			}
		}
		records.entries = append(records.entries, entry)
		records.built[e.id] = e.built
		paths[e.id] = e.paths
		if !e.waits {
			continue
		}
		item := beads.WorkItem{ID: entry.WorkItemID, Priority: e.priority}
		if e.blocker != "" {
			item.Dependencies = []beads.Dependency{{ID: "item-" + e.blocker, Type: beads.BlocksDependency, Status: "open"}}
		}
		items[entry.WorkItemID] = item
		started[e.id] = e.started
		waiting = append(waiting, entry)
	}
	var cfg config.Config
	cfg.Product.Specifications, cfg.Product.Designs, cfg.Product.Decisions, cfg.Product.Invariants = "docs/product", "docs/designs", "docs/decisions", "docs/decisions/invariants"
	o := queueOrder{
		pipeline: &Pipeline{Config: cfg, Store: turnRuns{handedBack: q.handedBack}, Tracker: orderTracker{items: items}},
		paths:    orderPaths{paths: paths}.CandidatePaths,
		now:      orderNow,
		records:  records,
	}
	return o.choose(context.Background(), waiting, started)
}

func turnAgo(d time.Duration) time.Time { return orderNow.Add(-d) }

func TestAChangeBroughtUpToDateAfterAConflictTakesTheNextTurnAheadOfEverything(t *testing.T) {
	got := turnQueue{
		entries: []turnEntry{
			{id: "aged", run: "aged", age: 5 * time.Hour, priority: 2, paths: code, waits: true},
			{id: "doc", run: "doc", age: 3 * time.Hour, paths: design, waits: true},
			{id: "p0", run: "p0", age: 2 * time.Hour, priority: 0, paths: code, waits: true},
			{id: "first", run: "back", age: 90 * time.Minute, priority: 3, paths: code, built: true},
			{id: "back", run: "back", age: 10 * time.Minute, priority: 3, paths: code, waits: true},
		},
		handedBack: map[string][]time.Time{"back": {turnAgo(time.Hour)}},
	}.choose(t)
	if got.Entry.EntryID != "back" || got.Rule != MergeQueueRuleReturned {
		t.Fatalf("chose %s by %q, want the change brought up to date by its own rule", got.Entry.EntryID, got.Rule)
	}
	if strings.Join(got.Order, ",") != "back,aged,p0,doc" {
		t.Fatalf("order %v, want the returned change first and the rest as the numbered rules take them", got.Order)
	}
	why := got.Selection().Why
	if !strings.Contains(why, "brought up to date") || !strings.Contains(why, "last handed back for a conflict at") || !strings.Contains(why, "went ahead of 3 entries") {
		t.Fatalf("the record of the choice says %q, want it to say why and when it was handed back", why)
	}
	if got.Selection().Rule != MergeQueueRuleReturned || got.Document {
		t.Fatalf("selection %#v, want the returned rule on a code choice", got.Selection())
	}
}

func TestOfTwoReturnedChangesTheOneHandedBackEarlierGoesFirst(t *testing.T) {
	q := turnQueue{
		entries: []turnEntry{
			{id: "b-old", run: "b", age: 3 * time.Hour, paths: code, built: true},
			{id: "a-old", run: "a", age: 3 * time.Hour, paths: code, built: true},
			// a rejoins first, yet b was handed back first.
			{id: "a", run: "a", age: 20 * time.Minute, paths: code, waits: true},
			{id: "b", run: "b", age: 10 * time.Minute, paths: code, waits: true},
			{id: "p0", run: "p0", age: 30 * time.Minute, priority: 0, paths: code, waits: true},
		},
		handedBack: map[string][]time.Time{"a": {turnAgo(40 * time.Minute)}, "b": {turnAgo(2 * time.Hour), turnAgo(50 * time.Minute)}},
	}
	got := q.choose(t)
	if got.Entry.EntryID != "b" || got.Rule != MergeQueueRuleReturned {
		t.Fatalf("chose %s by %q, want b, whose latest hand-back came earlier", got.Entry.EntryID, got.Rule)
	}
	if strings.Join(got.Order, ",") != "b,a,p0" {
		t.Fatalf("order %v, want both returned changes before priority 0 code, earlier hand-back first", got.Order)
	}
}

func TestAReturnedChangeStopsNoCandidateBeingCheckedAndWaitsForWhatItDependsOn(t *testing.T) {
	entries := func(started bool, blocker string) []turnEntry {
		return []turnEntry{
			{id: "busy", run: "busy", age: time.Hour, priority: 4, paths: code, waits: true, started: started},
			{id: "dep", run: "dep", age: time.Hour, priority: 4, paths: code, waits: true},
			{id: "old", run: "back", age: 2 * time.Hour, paths: code, built: true},
			{id: "back", run: "back", age: 5 * time.Minute, paths: code, waits: true, blocker: blocker},
		}
	}
	handedBack := map[string][]time.Time{"back": {turnAgo(10 * time.Minute)}}
	if got := (turnQueue{entries: entries(true, ""), handedBack: handedBack}).choose(t); got.Entry.EntryID != "busy" || got.Rule != MergeQueueRuleInProgress {
		t.Fatalf("chose %s by %q, want the candidate already being checked", got.Entry.EntryID, got.Rule)
	}
	got := (turnQueue{entries: entries(false, "dep"), handedBack: handedBack}).choose(t)
	if got.Entry.EntryID == "back" || got.Order[len(got.Order)-1] != "back" {
		t.Fatalf("chose %s with order %v, want the returned change to wait for the entry its item depends on", got.Entry.EntryID, got.Order)
	}
}

func TestTheTurnIsKeptThroughARepairAndSpentOnceItsCandidateIsBuilt(t *testing.T) {
	handedBack := map[string][]time.Time{"back": {turnAgo(3 * time.Hour)}}
	others := []turnEntry{{id: "p0", run: "p0", age: time.Hour, priority: 0, paths: code, waits: true}}
	cases := []struct {
		name    string
		run     []turnEntry
		want    string
		why     string
		handled map[string][]time.Time
	}{{
		name: "rejoined after a repair that built no candidate",
		run: []turnEntry{
			{id: "conflicted", run: "back", age: 5 * time.Hour, paths: code, built: true},
			{id: "repaired", run: "back", age: 2 * time.Hour, paths: code},
			{id: "back", run: "back", age: 30 * time.Minute, priority: 2, paths: code, waits: true},
		},
		want: "back", why: "a repair before it rejoins builds no candidate, so the turn is kept",
	}, {
		name: "its own candidate was built and set aside when the target moved",
		run: []turnEntry{
			{id: "conflicted", run: "back", age: 5 * time.Hour, paths: code, built: true},
			{id: "back", run: "back", age: 2 * time.Hour, priority: 2, paths: code, waits: true, built: true},
		},
		want: "back", why: "the entry whose candidate was built keeps the turn while it waits",
	}, {
		name: "its candidate failed a check and it rejoined",
		run: []turnEntry{
			{id: "conflicted", run: "back", age: 5 * time.Hour, paths: code, built: true},
			{id: "failed", run: "back", age: 2 * time.Hour, paths: code, built: true},
			{id: "back", run: "back", age: 30 * time.Minute, priority: 2, paths: code, waits: true},
		},
		want: "p0", why: "a candidate that failed spent the turn, so the change rejoins under the ordinary order",
	}, {
		name: "its candidate conflicted again and it rejoined",
		run: []turnEntry{
			{id: "conflicted", run: "back", age: 5 * time.Hour, paths: code, built: true},
			{id: "again", run: "back", age: 2 * time.Hour, paths: code, built: true},
			{id: "back", run: "back", age: 30 * time.Minute, priority: 2, paths: code, waits: true},
		},
		handled: map[string][]time.Time{"back": {turnAgo(3 * time.Hour), turnAgo(time.Hour)}},
		want:    "back", why: "the new hand-back for a conflict gives a new turn",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			given := handedBack
			if c.handled != nil {
				given = c.handled
			}
			got := turnQueue{entries: append(append([]turnEntry(nil), others...), c.run...), handedBack: given}.choose(t)
			if got.Entry.EntryID != c.want {
				t.Fatalf("chose %s by %q, want %s: %s", got.Entry.EntryID, got.Rule, c.want, c.why)
			}
		})
	}
}

func TestADocumentWhosePublicationConflictedNeverTakesTheTurn(t *testing.T) {
	got := turnQueue{
		entries: []turnEntry{
			{id: "p0", run: "p0", age: time.Hour, priority: 0, paths: code, waits: true},
			{id: "doc-old", run: "doc", age: 3 * time.Hour, paths: design, built: true},
			{id: "doc", run: "doc", age: 5 * time.Minute, paths: design, waits: true},
		},
		handedBack: map[string][]time.Time{"doc": {turnAgo(time.Hour)}},
	}.choose(t)
	if got.Entry.EntryID != "p0" || got.Rule != MergeQueueRuleUrgent {
		t.Fatalf("chose %s by %q, want priority 0 code: a document is chosen under the numbered rules", got.Entry.EntryID, got.Rule)
	}
}

func TestAnEntryAdmittedBeforeItsHandBackHoldsNoTurn(t *testing.T) {
	got := turnQueue{
		entries: []turnEntry{
			{id: "p0", run: "p0", age: time.Hour, priority: 0, paths: code, waits: true},
			{id: "back", run: "back", age: 2 * time.Hour, priority: 2, paths: code, waits: true},
		},
		handedBack: map[string][]time.Time{"back": {turnAgo(30 * time.Minute)}},
	}.choose(t)
	if got.Entry.EntryID != "p0" {
		t.Fatalf("chose %s by %q, want p0: an entry not admitted again since its hand-back is ordinary", got.Entry.EntryID, got.Rule)
	}
}

// The queue writes its own hand-back before the run's record carries it, so a
// change that rejoined between the two writes still takes its turn.
func TestTheQueuesOwnHandBackGivesTheTurnBeforeTheRunRecordsIt(t *testing.T) {
	landings := map[string]runstate.MergeQueueLanding{"conflicted": {
		EntryID:  "conflicted",
		Handback: &runstate.MergeQueueHandback{At: turnAgo(20 * time.Minute), Conflict: &runstate.MergeQueueConflict{}},
	}}
	got := turnQueue{
		entries: []turnEntry{
			{id: "p0", run: "p0", age: time.Hour, priority: 0, paths: code, waits: true},
			{id: "conflicted", run: "back", age: 3 * time.Hour, paths: code, built: true},
			{id: "back", run: "back", age: 5 * time.Minute, priority: 2, paths: code, waits: true},
		},
		landings: landings,
	}.choose(t)
	if got.Entry.EntryID != "back" || got.Rule != MergeQueueRuleReturned {
		t.Fatalf("chose %s by %q, want the rejoined change by its turn", got.Entry.EntryID, got.Rule)
	}
}

// A restart builds a new order from the same records and makes the same
// choice, since nothing the turn depends on is kept anywhere else; a queue
// whose records will not read gives no turn and chooses by the numbered rules.
func TestARestartGoesOnFromTheRecordsAndUnreadableRecordsGiveNoTurn(t *testing.T) {
	q := turnQueue{
		entries: []turnEntry{
			{id: "aged", run: "aged", age: 5 * time.Hour, priority: 2, paths: code, waits: true},
			{id: "x-old", run: "x", age: 4 * time.Hour, paths: code, built: true},
			{id: "y-old", run: "y", age: 4 * time.Hour, paths: code, built: true},
			{id: "x", run: "x", age: 30 * time.Minute, priority: 1, paths: code, waits: true},
			{id: "y", run: "y", age: 20 * time.Minute, priority: 1, paths: code, waits: true},
		},
		handedBack: map[string][]time.Time{"x": {turnAgo(time.Hour)}, "y": {turnAgo(2 * time.Hour)}},
	}
	before, after := q.choose(t), q.choose(t)
	if before.Entry.EntryID != "y" || after.Entry.EntryID != "y" || strings.Join(after.Order, ",") != strings.Join(before.Order, ",") {
		t.Fatalf("chose %s then %s (orders %v and %v), want y both times", before.Entry.EntryID, after.Entry.EntryID, before.Order, after.Order)
	}
	q.down = true
	if got := q.choose(t); got.Entry.EntryID != "aged" || got.Rule != MergeQueueRuleAged {
		t.Fatalf("with the queue's records unreadable chose %s by %q, want the aged entry", got.Entry.EntryID, got.Rule)
	}
}
