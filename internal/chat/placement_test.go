package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func placement(priority int, at time.Time) *beads.Placement {
	return &beads.Placement{By: domain.RoleProductManager, At: at, Priority: priority, Reason: "promised first"}
}

// The Lead Product Manager places an item first among its priority with a
// reason, and the placement is written to the tracker with who, when, the
// priority and the reason, beside a note saying so. Removing one writes the
// ending and a note naming what it had been.
func TestTheLeadProductManagerPlacesAnItemFirstAndRemovesAPlacement(t *testing.T) {
	t.Parallel()

	placedAt := time.Date(2026, 10, 29, 13, 50, 0, 0, time.UTC)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-u6ei": {ID: "yoyodyne-u6ei", Title: "The dedicated slot configuration", Status: "open", Priority: 0},
		"yoyodyne-old":  {ID: "yoyodyne-old", Title: "An earlier promise", Status: "open", Priority: 0, Placement: placement(0, placedAt)},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Placing the promised item first and removing the old promise.",
			`{"action":"place","id":"yoyodyne-u6ei","reason":"promised first among priority 0 by the operator"}`,
			`{"action":"unplace","id":"yoyodyne-old","reason":"that promise was kept another way"}`)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Make sure the dedicated slot work goes first.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.updates) != 2 {
		t.Fatalf("updates = %#v, want the placement and the removal", tracker.updates)
	}
	placed := tracker.updates[0]
	if placed.id != "yoyodyne-u6ei" || placed.change.Placement == nil {
		t.Fatalf("place recorded %#v", placed)
	}
	written := *placed.change.Placement
	if written.By != domain.RoleProductManager || written.Priority != 0 || written.At.IsZero() ||
		written.Reason != "promised first among priority 0 by the operator" {
		t.Fatalf("placement written = %#v", written)
	}
	for _, required := range []string{"Placed first among priority 0", "by the Lead Product Manager in conversation", "promised first among priority 0 by the operator"} {
		if !strings.Contains(placed.change.AppendNotes, required) {
			t.Fatalf("place notes = %q, want %q", placed.change.AppendNotes, required)
		}
	}
	removed := tracker.updates[1]
	if removed.id != "yoyodyne-old" || removed.change.Placement == nil || *removed.change.Placement != (beads.Placement{}) {
		t.Fatalf("unplace recorded %#v, want the placement ended", removed)
	}
	for _, required := range []string{"Placement first among priority 0 removed", "It had been placed first among priority 0 by the Lead Product Manager"} {
		if !strings.Contains(removed.change.AppendNotes, required) {
			t.Fatalf("unplace notes = %q, want %q", removed.change.AppendNotes, required)
		}
	}
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	for _, required := range []string{"placed yoyodyne-u6ei first among priority 0", "removed the placement of yoyodyne-old"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered outcomes = %q, want %q", rendered, required)
		}
	}
}

// A fourth placement in one priority is refused, nothing is written, and the
// refusal names the three already placed and when each was placed.
func TestAFourthPlacementInOnePriorityIsRefusedNamingTheThree(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC)
	placedItems := []beads.WorkItem{
		{ID: "yoyodyne-a", Title: "First promise", Status: "open", Priority: 0, Placement: placement(0, base)},
		{ID: "yoyodyne-b", Title: "Second promise", Status: "open", Priority: 0, Placement: placement(0, base.Add(time.Hour))},
		// A placement among another priority does not count against this one.
		{ID: "yoyodyne-p1", Title: "Priority 1 promise", Status: "open", Priority: 1, Placement: placement(1, base)},
	}
	blocked := []beads.WorkItem{
		{ID: "yoyodyne-c", Title: "Third promise", Status: "blocked", Priority: 0, Placement: placement(0, base.Add(2*time.Hour))},
	}
	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-d": {ID: "yoyodyne-d", Title: "A fourth", Status: "open", Priority: 0},
		},
		open:         placedItems,
		blockedItems: blocked,
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Placing it.", `{"action":"place","id":"yoyodyne-d","reason":"promised too"}`)},
		{SessionID: "session-1", FinalText: "Refused."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Put the fourth first too.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("a fourth placement was written: %#v", tracker.updates)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the placement refused", reply.Actions)
	}
	failure := reply.Actions[0].Failure
	for _, required := range []string{"priority 0 already has 3 items placed first", "yoyodyne-a (First promise), placed at", "yoyodyne-b", "yoyodyne-c", "unplace"} {
		if !strings.Contains(failure, required) {
			t.Fatalf("refusal = %q, want %q", failure, required)
		}
	}
	if strings.Contains(failure, "yoyodyne-p1") {
		t.Fatalf("refusal = %q names a placement among another priority", failure)
	}
	if first, second := strings.Index(failure, "yoyodyne-a"), strings.Index(failure, "yoyodyne-c"); first > second {
		t.Fatalf("refusal = %q, want the placements earliest first", failure)
	}
}

// Changing an item's priority ends its placement in the same write, and the
// note records what ended it.
func TestReprioritizingAPlacedItemEndsItsPlacement(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-u6ei": {ID: "yoyodyne-u6ei", Title: "Promised", Status: "open", Priority: 0, Placement: placement(0, time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC))},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Lowering it.", `{"action":"reprioritize","id":"yoyodyne-u6ei","priority":1,"reason":"no longer urgent"}`)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "Lower it."); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
	change := tracker.updates[0].change
	if change.Priority == nil || *change.Priority != 1 || change.Placement == nil || *change.Placement != (beads.Placement{}) {
		t.Fatalf("reprioritize wrote %#v, want the new priority and the placement ended in one write", change)
	}
	if !strings.Contains(change.AppendNotes, "Placement first among priority 0 ended: the item's priority changed to 1") {
		t.Fatalf("notes = %q, want the ending recorded", change.AppendNotes)
	}
}

// Placing an item that is already placed, or removing a placement that is not
// there, is refused and writes nothing.
func TestPlacingTwiceAndRemovingNothingAreRefused(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-placed": {ID: "yoyodyne-placed", Title: "Placed", Status: "open", Priority: 0, Placement: placement(0, time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC))},
		"yoyodyne-plain":  {ID: "yoyodyne-plain", Title: "Plain", Status: "open", Priority: 0},
		"yoyodyne-closed": {ID: "yoyodyne-closed", Title: "Closed", Status: "closed", Priority: 0},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Trying.",
			`{"action":"place","id":"yoyodyne-placed","reason":"again"}`,
			`{"action":"unplace","id":"yoyodyne-plain","reason":"no longer"}`,
			`{"action":"place","id":"yoyodyne-closed","reason":"first"}`)},
		{SessionID: "session-1", FinalText: "Refused."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Try.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v, want none", tracker.updates)
	}
	for index, want := range []string{"already placed first among priority 0", "not placed first", "closed"} {
		if reply.Actions[index].Applied || !strings.Contains(reply.Actions[index].Failure, want) {
			t.Fatalf("action %d = %#v, want it refused saying %q", index, reply.Actions[index], want)
		}
	}
}

// Only the Lead Product Manager may place or remove a placement: the order is
// hers, and every other role asks her.
func TestOnlyTheLeadProductManagerMayPlaceAnItemFirst(t *testing.T) {
	t.Parallel()

	for _, role := range domain.Roles() {
		authority, known := AuthorityFor(role)
		if !known {
			continue
		}
		want := role == domain.RoleProductManager
		for _, action := range []string{actionPlace, actionUnplace} {
			if got := authority.MayAct(action); got != want {
				t.Fatalf("the %s may %s = %v, want %v", role.Title(), action, got, want)
			}
		}
	}
}

// A survey and an item read say an item is placed in the words every listing
// uses, and an unplaced item reads as it did before.
func TestASurveyAndAReadSayAnItemIsPlaced(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 29, 13, 50, 0, 0, time.UTC)
	items := []beads.WorkItem{
		{ID: "yoyodyne-old", Title: "Older", Status: "open", Priority: 0, CreatedAt: at.Add(-48 * time.Hour)},
		{ID: "yoyodyne-u6ei", Title: "Promised", Status: "open", Priority: 0, CreatedAt: at, Placement: placement(0, at)},
	}
	survey := renderOpenQueueEvidence(items, goal.Set{})
	line := placement(0, at).Line()
	if !strings.Contains(survey, line) || strings.Count(survey, "placed first among priority") != 1 {
		t.Fatalf("survey = %q, want the one placement line %q", survey, line)
	}
	if strings.Index(survey, "- yoyodyne-u6ei") > strings.Index(survey, "- yoyodyne-old") {
		t.Fatalf("survey = %q, want the placed item listed first", survey)
	}
	head := renderWorkItemHead(items[1], goal.Set{}, false)
	if !strings.Contains(head, line) {
		t.Fatalf("item read = %q, want %q", head, line)
	}
	if strings.Contains(renderWorkItemHead(items[0], goal.Set{}, false), "placed first") {
		t.Fatal("an unplaced item read says it is placed")
	}
}
