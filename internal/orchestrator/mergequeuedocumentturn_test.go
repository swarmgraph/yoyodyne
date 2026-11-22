package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// After a document candidate lands, ordinary code waiting goes next: the
// document rule is passed over for that one choice, and the choice says so.
func TestAfterADocumentLandsOrdinaryCodeIsChosenNext(t *testing.T) {
	entries := []orderEntry{
		{id: "doc-a", paths: intent, age: 2 * time.Hour},
		{id: "p2", paths: code, priority: 2, age: time.Hour},
		{id: "doc-b", paths: design, age: 30 * time.Minute},
	}
	got := chooseAfter(t, true, false, entries...)
	if got.Entry.EntryID != "doc-a" || got.Rule != MergeQueueRuleDocument || !got.Document || got.DocumentPassedOver {
		t.Fatalf("chose %s by %q (document %v, passed over %v), want the document when the last landing was code", got.Entry.EntryID, got.Rule, got.Document, got.DocumentPassedOver)
	}
	got = chooseAfter(t, true, true, entries...)
	if got.Entry.EntryID != "p2" || got.Rule != MergeQueueRulePriority || got.Document || !got.DocumentPassedOver {
		t.Fatalf("chose %s by %q (passed over %v), want the ordinary code after a document landing", got.Entry.EntryID, got.Rule, got.DocumentPassedOver)
	}
	if !strings.Contains(got.Why(), "documents never land twice in a row while code waits") {
		t.Fatalf("reason %q does not say the document rule was passed over", got.Why())
	}
	if selection := got.Selection(); selection.Document || !selection.DocumentPassedOver || selection.PlacedFirst {
		t.Fatalf("selection = %#v, want it to record the document rule passed over", selection)
	}
	if strings.Join(got.Order, ",") != "p2,doc-a,doc-b" {
		t.Fatalf("order %v, want the code, then the documents", got.Order)
	}

	// Code whose priority could not be read is ordinary code too.
	got = chooseAfter(t, true, true,
		orderEntry{id: "doc", paths: intent, age: 2 * time.Hour},
		orderEntry{id: "unread", paths: code, unread: true, age: time.Hour},
	)
	if got.Entry.EntryID != "unread" || got.Rule != MergeQueueRuleUnreadable || !got.DocumentPassedOver {
		t.Fatalf("chose %s by %q (passed over %v), want the unreadable code after a document landing", got.Entry.EntryID, got.Rule, got.DocumentPassedOver)
	}

	// With only documents waiting, the next document goes, and nothing was
	// passed over.
	got = chooseAfter(t, true, true, orderEntry{id: "doc-a", paths: intent}, orderEntry{id: "doc-b", paths: design})
	if got.Entry.EntryID != "doc-a" || got.Rule != MergeQueueRuleDocument || got.DocumentPassedOver {
		t.Fatalf("chose %s by %q (passed over %v), want the document when no code waits", got.Entry.EntryID, got.Rule, got.DocumentPassedOver)
	}
}

// Rule 1 and rule 2 still come first after a document landing.
func TestAfterADocumentLandsTheAgeRuleAndPriorityZeroStillComeFirst(t *testing.T) {
	got := chooseAfter(t, true, true,
		orderEntry{id: "doc", paths: intent, age: 5 * time.Hour},
		orderEntry{id: "p0", paths: code, priority: 0, age: time.Hour},
		orderEntry{id: "p2", paths: code, priority: 2, age: time.Hour},
	)
	if got.Entry.EntryID != "doc" || got.Rule != MergeQueueRuleAged || got.DocumentPassedOver {
		t.Fatalf("chose %s by %q, want the document that waited four hours", got.Entry.EntryID, got.Rule)
	}
	got = chooseAfter(t, true, true,
		orderEntry{id: "doc", paths: intent, age: 2 * time.Hour},
		orderEntry{id: "p2", paths: code, priority: 2, age: time.Hour},
		orderEntry{id: "p0", paths: code, priority: 0, age: time.Hour},
	)
	if got.Entry.EntryID != "p0" || got.Rule != MergeQueueRuleUrgent || got.DocumentPassedOver {
		t.Fatalf("chose %s by %q, want the priority 0 code", got.Entry.EntryID, got.Rule)
	}
}

// A placement still orders entries within one priority when the document rule
// is passed over.
func TestAPlacementStillOrdersWithinAPriorityAfterADocumentLands(t *testing.T) {
	got := chooseAfter(t, true, true,
		orderEntry{id: "doc", paths: intent, age: 3 * time.Hour},
		orderEntry{id: "p1", paths: code, priority: 1, age: 2 * time.Hour},
		orderEntry{id: "p1-placed", paths: code, priority: 1, age: time.Hour, placedAgo: 30 * time.Minute},
	)
	if got.Entry.EntryID != "p1-placed" || got.Rule != MergeQueueRulePriority || !got.PlacedFirst || !got.DocumentPassedOver {
		t.Fatalf("chose %s by %q (placed %v, passed over %v), want the placed entry", got.Entry.EntryID, got.Rule, got.PlacedFirst, got.DocumentPassedOver)
	}
	if selection := got.Selection(); !selection.PlacedFirst || !selection.DocumentPassedOver {
		t.Fatalf("selection = %#v, want the placement and the passed-over document rule recorded", selection)
	}
}

// landingRecords is the queue's records of entries, candidates and landings,
// as a process that has just started reads them.
type landingRecords struct {
	entries     []runstate.MergeQueueEntry
	generations map[string][]runstate.MergeQueueGeneration
	landings    map[string]runstate.MergeQueueLanding
}

func (r landingRecords) Entries(runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error) {
	return r.entries, nil
}

func (r landingRecords) Generations(_ runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueGeneration, error) {
	return r.generations[entryID], nil
}

func (r landingRecords) Landing(_ runstate.MergeQueueKey, entryID string) (runstate.MergeQueueLanding, bool, error) {
	landing, found := r.landings[entryID]
	return landing, found, nil
}

// Whether the last landing was a document candidate is read from the records:
// the landing the queue confirmed last and the choice recorded on its
// candidate, so a process that has just started chooses the same.
func TestWhetherTheLastLandingWasADocumentIsReadFromTheRecords(t *testing.T) {
	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	landed := func(id string, at time.Time, selection *runstate.MergeQueueSelection) (runstate.MergeQueueEntry, runstate.MergeQueueGeneration, runstate.MergeQueueLanding) {
		entry := runstate.MergeQueueEntry{EntryID: id}
		generation := runstate.MergeQueueGeneration{Number: 1, EntryID: id, Selection: selection}
		landing := runstate.MergeQueueLanding{
			EntryID:    id,
			Attempts:   []runstate.MergeQueuePromotionAttempt{{Number: 1, Generation: 1, Landed: &runstate.MergeQueueLanded{ConfirmedAt: at}}},
			Completion: &runstate.MergeQueueCompletion{Attempt: 1, Generation: 1, QueueAt: at},
		}
		return entry, generation, landing
	}
	records := func(landings ...func() (runstate.MergeQueueEntry, runstate.MergeQueueGeneration, runstate.MergeQueueLanding)) landingRecords {
		r := landingRecords{generations: map[string][]runstate.MergeQueueGeneration{}, landings: map[string]runstate.MergeQueueLanding{}}
		for _, made := range landings {
			entry, generation, landing := made()
			r.entries = append(r.entries, entry)
			r.generations[entry.EntryID] = []runstate.MergeQueueGeneration{generation}
			r.landings[entry.EntryID] = landing
		}
		return r
	}
	documentLanding := func() (runstate.MergeQueueEntry, runstate.MergeQueueGeneration, runstate.MergeQueueLanding) {
		return landed("doc", orderNow.Add(-time.Minute), &runstate.MergeQueueSelection{Rule: MergeQueueRuleDocument, Why: "a document", Document: true})
	}
	codeLanding := func() (runstate.MergeQueueEntry, runstate.MergeQueueGeneration, runstate.MergeQueueLanding) {
		return landed("code", orderNow.Add(-time.Hour), &runstate.MergeQueueSelection{Rule: MergeQueueRulePriority, Why: "code"})
	}
	// The document landed after the code, so the last landing was a document.
	after := records(codeLanding, documentLanding)
	if !lastLandingWasDocument(after, key, "") {
		t.Fatal("the last landing was a document candidate and the records were read as code")
	}
	// A fresh order, as a restarted process builds it from the same records,
	// takes the ordinary code.
	got := chooseAfter(t, true, lastLandingWasDocument(after, key, ""),
		orderEntry{id: "doc-next", paths: intent, age: 2 * time.Hour},
		orderEntry{id: "p3", paths: code, priority: 3, age: time.Hour},
	)
	if got.Entry.EntryID != "p3" || !got.DocumentPassedOver {
		t.Fatalf("after a restart chose %s (passed over %v), want the ordinary code", got.Entry.EntryID, got.DocumentPassedOver)
	}
	// The code landed last: no document landing to take turns with.
	codeLast := records(documentLanding, func() (runstate.MergeQueueEntry, runstate.MergeQueueGeneration, runstate.MergeQueueLanding) {
		return landed("code", orderNow, &runstate.MergeQueueSelection{Rule: MergeQueueRulePriority, Why: "code"})
	})
	if lastLandingWasDocument(codeLast, key, "") {
		t.Fatal("the last landing was code and the records were read as a document")
	}
	// A document chosen by the age rule is still a document candidate.
	aged := records(func() (runstate.MergeQueueEntry, runstate.MergeQueueGeneration, runstate.MergeQueueLanding) {
		return landed("doc", orderNow, &runstate.MergeQueueSelection{Rule: MergeQueueRuleAged, Why: "aged", Document: true})
	})
	if !lastLandingWasDocument(aged, key, "") {
		t.Fatal("a document chosen by the age rule was read as code")
	}
	// A choice recorded before the record said so is read by its rule.
	older := records(func() (runstate.MergeQueueEntry, runstate.MergeQueueGeneration, runstate.MergeQueueLanding) {
		return landed("doc", orderNow, &runstate.MergeQueueSelection{Rule: MergeQueueRuleDocument, Why: "a document"})
	})
	if !lastLandingWasDocument(older, key, "") {
		t.Fatal("an older record of a document choice was read as code")
	}
	// Nothing landed yet, and checking ahead behind a document entry.
	if lastLandingWasDocument(records(), key, "") {
		t.Fatal("a queue that landed nothing was read as having landed a document")
	}
	if !lastLandingWasDocument(after, key, "doc") || lastLandingWasDocument(after, key, "code") {
		t.Fatal("the choice made as if an entry had just landed did not read that entry's own record")
	}
}

// Only the latest pass keeps the order the rules chose; the history leaves
// it out, so twelve hours of passes do not each carry the queue's order.
func TestThePassRecordsTheChosenOrderAndTheHistoryLeavesItOut(t *testing.T) {
	store, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	progress := &queueProgress{}
	ctx := withQueueProgress(t.Context(), progress)
	queueChose(ctx, MergeQueueChoice{Entry: runstate.MergeQueueEntry{EntryID: "b"}, Rule: MergeQueueRulePriority, DocumentPassedOver: true, Order: []string{"b", "a"}})
	pass := runstate.MergeQueuePass{At: orderNow, Outcome: runstate.MergeQueuePassWaiting, Says: "waiting", Order: progress.order()}
	if err := store.RecordPass(t.Context(), key, pass); err != nil {
		t.Fatal(err)
	}
	last, found, err := store.LastPass(key)
	if err != nil || !found || last.Order == nil || strings.Join(last.Order.Entries, ",") != "b,a" || last.Order.Rule != MergeQueueRulePriority || !strings.Contains(last.Order.Why, "passed over") {
		t.Fatalf("last pass = %#v, %t, %v; want the chosen order with its rule and reason", last.Order, found, err)
	}
	history, err := store.PassHistory(key)
	if err != nil || len(history) != 1 || history[0].Order != nil {
		t.Fatalf("history = %#v, %v; want one pass without the order", history, err)
	}
}

// A document chosen because it was the only entry waiting is recorded as a
// document, so the choice made after it lands passes over a waiting document
// for ordinary code, as after any other document landing.
func TestADocumentChosenAloneIsRecordedAsADocument(t *testing.T) {
	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	alone := chooseFrom(t, true, orderEntry{id: "doc", paths: intent})
	if alone.Rule != MergeQueueRuleOnlyWaiting || !alone.Document || strings.Join(alone.Order, ",") != "doc" {
		t.Fatalf("chose %s by %q (document %v, order %v), want the lone document recorded as one", alone.Entry.EntryID, alone.Rule, alone.Document, alone.Order)
	}
	if selection := alone.Selection(); !selection.Document {
		t.Fatalf("selection = %#v, want it to record a document", selection)
	}
	if codeAlone := chooseFrom(t, true, orderEntry{id: "p2", paths: code, priority: 2}); codeAlone.Document {
		t.Fatal("lone code was recorded as a document")
	}
	at := orderNow.Add(-time.Minute)
	records := landingRecords{
		entries:     []runstate.MergeQueueEntry{{EntryID: "doc"}},
		generations: map[string][]runstate.MergeQueueGeneration{"doc": {{Number: 1, EntryID: "doc", Selection: alone.Selection()}}},
		landings: map[string]runstate.MergeQueueLanding{"doc": {
			EntryID:    "doc",
			Attempts:   []runstate.MergeQueuePromotionAttempt{{Number: 1, Generation: 1, Landed: &runstate.MergeQueueLanded{ConfirmedAt: at}}},
			Completion: &runstate.MergeQueueCompletion{Attempt: 1, Generation: 1, QueueAt: at},
		}},
	}
	got := chooseAfter(t, true, lastLandingWasDocument(records, key, ""),
		orderEntry{id: "doc-next", paths: design, age: 2 * time.Hour},
		orderEntry{id: "p2", paths: code, priority: 2, age: time.Hour},
	)
	if got.Entry.EntryID != "p2" || !got.DocumentPassedOver {
		t.Fatalf("after the lone document landed chose %s (passed over %v), want the ordinary code", got.Entry.EntryID, got.DocumentPassedOver)
	}
}
