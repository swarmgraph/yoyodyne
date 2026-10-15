package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// placingTracker is the fake tracker holding what it is asked to write as the
// tracker would: a created item exists afterwards under the parent it was
// given, and a reparent moves it. Without that, a decomposition that creates a
// child and then links its parent to it could not be followed past the
// creation, and that sequence is the one this guards.
type placingTracker struct {
	*fakeTracker
}

func (p placingTracker) Create(ctx context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	created, err := p.fakeTracker.Create(ctx, item)
	if err == nil {
		p.items[created.ID] = beads.WorkItem{ID: created.ID, Title: created.Title, Parent: created.Parent, Status: "open",
			Executor: item.Executor, Parking: item.Parking}
	}
	return created, err
}

func (p placingTracker) Update(ctx context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	updated, err := p.fakeTracker.Update(ctx, id, change)
	if err == nil && change.Parent != nil {
		item := p.items[id]
		item.Parent = *change.Parent
		p.items[id] = item
	}
	return updated, err
}

func (p placingTracker) AddBlocker(ctx context.Context, id, blockerID string) error {
	if err := p.fakeTracker.AddBlocker(ctx, id, blockerID); err != nil {
		return err
	}
	item := p.items[id]
	item.Dependencies = append(item.Dependencies, beads.Dependency{ID: blockerID, Type: beads.BlocksDependency, Status: p.items[blockerID].Status})
	p.items[id] = item
	return nil
}

// developmentManagerSession is a development manager's conversation answering
// with the replies given, in order, against tracker.
func developmentManagerSession(t *testing.T, tracker Tracker, replies ...string) *Session {
	t.Helper()
	var results []backendapi.RunResult
	for _, reply := range replies {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: reply})
	}
	options := testOptions(t, &fakeBackend{results: results})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	return openTestSession(t, options)
}

// The decomposition the scheduled-pass design was found in (yoyodyne-ifd.428.75.1,
// and three design items before it): the development manager breaks the design
// an implementation needs out of that implementation, then links the
// implementation to wait on it. The link is refused, saying why and where the
// design belongs; the design is then placed beside the implementation and the
// same link is carried out. Throughout, the design reads as held by nothing, so
// the architect can take it without the implementation, and the implementation's
// genuine wait on outside work is carried out as asked.
func TestADecompositionCannotMakeAnImplementationWaitOnTheDesignBrokenOutOfIt(t *testing.T) {
	t.Parallel()

	tracker := placingTracker{&fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.428": {ID: "yoyodyne-ifd.428", Title: "Scheduling", Status: "open", IssueType: "epic"},
		"yoyodyne-ifd.428.75": {ID: "yoyodyne-ifd.428.75", Title: "Scheduled passes run side by side", Status: "open",
			IssueType: "feature", Parent: "yoyodyne-ifd.428"},
		"yoyodyne-ifd.430.57": {ID: "yoyodyne-ifd.430.57", Title: "Outside work", Status: "open", IssueType: "task"},
	}}}
	session := developmentManagerSession(t, tracker,
		trackerReply("The design comes first.",
			`{"action":"create","kind":"feature","title":"Reconcile scheduled-pass concurrency","description":"The architect reconciles the designs.","goal":"`+recordedGoal+`","parent":"yoyodyne-ifd.428.75","executor":"conversation:architect","reason":"the implementation needs the design"}`),
		trackerReply("Now sequence it.",
			`{"action":"link","id":"yoyodyne-ifd.428.75","depends_on":"yoyodyne-1","reason":"the implementation waits on the design"}`,
			`{"action":"link","id":"yoyodyne-ifd.428.75","depends_on":"yoyodyne-ifd.430.57","reason":"it needs the outside work too"}`),
		trackerReply("Placing the design beside it.",
			`{"action":"reparent","id":"yoyodyne-1","parent":"yoyodyne-ifd.428","reason":"a prerequisite goes beside the work that waits for it"}`,
			`{"action":"link","id":"yoyodyne-ifd.428.75","depends_on":"yoyodyne-1","reason":"the implementation waits on the design"}`),
		"Sequenced.")

	reply, err := session.Send(context.Background(), "Decompose ifd.428.75.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 5 {
		t.Fatalf("actions = %d, want 5: %#v", len(reply.Actions), reply.Actions)
	}
	refused := reply.Actions[1]
	if refused.Applied {
		t.Fatal("the implementation was linked to wait on the design broken out of it")
	}
	for _, want := range []string{
		"refused, and nothing was changed: 'Scheduled passes run side by side' (yoyodyne-ifd.428.75) would wait on " +
			"'Reconcile scheduled-pass concurrency' (yoyodyne-1), which was broken out of 'Scheduled passes run side by side' (yoyodyne-ifd.428.75)",
		"would never stop waiting",
		"A prerequisite an item waits on goes beside it, under the same parent",
	} {
		if !strings.Contains(refused.Failure, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, refused.Failure)
		}
	}
	for i, outcome := range reply.Actions {
		if i != 1 && !outcome.Applied {
			t.Errorf("%s = %q, want it carried out", outcome.Action.Action, outcome.Failure)
		}
	}
	want := [][2]string{{"yoyodyne-ifd.428.75", "yoyodyne-ifd.430.57"}, {"yoyodyne-ifd.428.75", "yoyodyne-1"}}
	if len(tracker.links) != 2 || tracker.links[0] != want[0] || tracker.links[1] != want[1] {
		t.Fatalf("links = %v, want only the genuine wait and the link made once the design was placed beside: %v", tracker.links, want)
	}
	if design := tracker.items["yoyodyne-1"]; design.Parent != "yoyodyne-ifd.428" || design.Executor != "conversation:architect" {
		t.Fatalf("the design = %+v, want it beside the implementation and still the architect's", design)
	}
	blocks, err := beads.InheritedBlocks(tracker.items["yoyodyne-1"],
		func(id string) (beads.WorkItem, bool, error) {
			item, found := tracker.items[id]
			return item, found, nil
		},
		func(w beads.WorkItem) []string { return w.WaitingOn(nil) })
	if err != nil || len(blocks) != 0 {
		t.Fatalf("the design is held through its parent: %+v, %v; want the architect free to take it", blocks, err)
	}
	if implementation := tracker.items["yoyodyne-ifd.428.75"]; len(implementation.WaitingOn(nil)) != 2 {
		t.Fatalf("the implementation waits on %v, want the design and the outside work", implementation.WaitingOn(nil))
	}
}

// The October 15 arrangement, made by the development manager settling a
// parent rather than decomposing it: the item coordinating the orchestrator
// test move was linked to wait on its own closing measure, which waits on two
// of its siblings, so all three read as blocked through their parent. The link
// is refused and nothing is written; the measure's genuine waits on its siblings
// are carried out as asked.
func TestAParentCannotBeLinkedToWaitOnItsOwnChild(t *testing.T) {
	t.Parallel()

	tracker := placingTracker{&fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.429": {ID: "yoyodyne-ifd.429", Title: "Race binary", Status: "open"},
		"yoyodyne-ifd.429.14": {ID: "yoyodyne-ifd.429.14", Title: "Move the orchestrator tests", Status: "open",
			Parent: "yoyodyne-ifd.429"},
		"yoyodyne-ifd.429.14.2": {ID: "yoyodyne-ifd.429.14.2", Title: "Branch-protection group", Status: "open", Parent: "yoyodyne-ifd.429.14"},
		"yoyodyne-ifd.429.14.3": {ID: "yoyodyne-ifd.429.14.3", Title: "Re-take the race-binary measure", Status: "open", Parent: "yoyodyne-ifd.429.14"},
		"yoyodyne-ifd.429.14.5": {ID: "yoyodyne-ifd.429.14.5", Title: "Remaining-tests group", Status: "open", Parent: "yoyodyne-ifd.429.14"},
	}}}
	session := developmentManagerSession(t, tracker,
		trackerReply("Settling the move.",
			`{"action":"link","id":"yoyodyne-ifd.429.14.3","depends_on":"yoyodyne-ifd.429.14.2","reason":"the measure waits on every group"}`,
			`{"action":"link","id":"yoyodyne-ifd.429.14.3","depends_on":"yoyodyne-ifd.429.14.5","reason":"the measure waits on every group"}`,
			`{"action":"link","id":"yoyodyne-ifd.429.14","depends_on":"yoyodyne-ifd.429.14.3","reason":"the move is done when the measure is"}`),
		"Settled.")

	reply, err := session.Send(context.Background(), "Settle ifd.429.14.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 3 || !reply.Actions[0].Applied || !reply.Actions[1].Applied {
		t.Fatalf("actions = %#v, want the measure's waits on its siblings carried out", reply.Actions)
	}
	if parent := reply.Actions[2]; parent.Applied || !strings.Contains(parent.Failure,
		"'Move the orchestrator tests' (yoyodyne-ifd.429.14) would wait on 'Re-take the race-binary measure' (yoyodyne-ifd.429.14.3), "+
			"which was broken out of 'Move the orchestrator tests' (yoyodyne-ifd.429.14)") {
		t.Fatalf("the parent's link = applied %v, %q; want it refused naming the loop", parent.Applied, parent.Failure)
	}
	if len(tracker.links) != 2 {
		t.Fatalf("links = %v, want only the measure's two", tracker.links)
	}
	for _, id := range []string{"yoyodyne-ifd.429.14.2", "yoyodyne-ifd.429.14.3", "yoyodyne-ifd.429.14.5"} {
		blocks, _ := beads.InheritedBlocks(tracker.items[id],
			func(id string) (beads.WorkItem, bool, error) {
				item, found := tracker.items[id]
				return item, found, nil
			},
			func(w beads.WorkItem) []string { return w.WaitingOn(nil) })
		if len(blocks) != 0 {
			t.Errorf("%s is blocked through its parent: %s", id, beads.DescribeInheritedBlocks(blocks))
		}
	}
}

// A reparent is the other way to make the loop: placing work under a parent
// that already waits on it. It is refused and writes nothing, so the item's
// parking — a hold somebody put on preserved work — is untouched, and a
// placement under a parent whose wait is on outside work is carried out, the
// inherited wait being a genuine one.
func TestWorkCannotBePlacedUnderAParentThatWaitsOnIt(t *testing.T) {
	t.Parallel()

	tracker := placingTracker{&fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.428": {ID: "yoyodyne-ifd.428", Title: "Scheduling", Status: "open"},
		"yoyodyne-ifd.428.75": {ID: "yoyodyne-ifd.428.75", Title: "Scheduled passes run side by side", Status: "open",
			Parent: "yoyodyne-ifd.428", Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.428.75.1", Type: beads.BlocksDependency, Status: "open"}}},
		"yoyodyne-ifd.428.75.1": {ID: "yoyodyne-ifd.428.75.1", Title: "Reconcile scheduled-pass concurrency", Status: "open",
			Parent: "yoyodyne-ifd.428", Parking: "the preserved change on run-1 is waiting on the operator"},
		"yoyodyne-ifd.428.80": {ID: "yoyodyne-ifd.428.80", Title: "Waits on outside work", Status: "open",
			Parent: "yoyodyne-ifd.428", Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.430.57", Type: beads.BlocksDependency, Status: "open"}}},
		"yoyodyne-ifd.430.57": {ID: "yoyodyne-ifd.430.57", Title: "Outside work", Status: "open"},
	}}}
	session := developmentManagerSession(t, tracker,
		trackerReply("Regrouping.",
			`{"action":"reparent","id":"yoyodyne-ifd.428.75.1","parent":"yoyodyne-ifd.428.75","reason":"it belongs to the implementation"}`,
			`{"action":"reparent","id":"yoyodyne-ifd.428.75.1","parent":"yoyodyne-ifd.428.80","reason":"group it with the other waiting work"}`),
		"Regrouped.")

	reply, err := session.Send(context.Background(), "Regroup.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if refused := reply.Actions[0]; refused.Applied || !strings.Contains(refused.Failure,
		"'Reconcile scheduled-pass concurrency' (yoyodyne-ifd.428.75.1) would be held back with its new parent "+
			"'Scheduled passes run side by side' (yoyodyne-ifd.428.75), which waits on 'Reconcile scheduled-pass concurrency' (yoyodyne-ifd.428.75.1)") {
		t.Fatalf("the placement under the waiting implementation = applied %v, %q; want it refused naming the loop", refused.Applied, refused.Failure)
	}
	if !reply.Actions[1].Applied {
		t.Fatalf("the placement under a parent waiting on outside work = %q, want it carried out", reply.Actions[1].Failure)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].change.Parent == nil || *tracker.updates[0].change.Parent != "yoyodyne-ifd.428.80" {
		t.Fatalf("updates = %+v, want only the genuine placement written", tracker.updates)
	}
	if parked := tracker.items["yoyodyne-ifd.428.75.1"].Parking; parked != "the preserved change on run-1 is waiting on the operator" {
		t.Fatalf("the parking = %q, want the hold on preserved work left as it was", parked)
	}
}
