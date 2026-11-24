package chat

// An item a role raised as unmeetable, as the conversations that answer it see
// it: the development manager deciding what becomes of the raising run's change,
// and the item's owner amending the item and releasing the raise's parking.
//
// yoyodyne-ifd.437.13 is what this is for. Its raise was answered with a repair
// the harness could never carry out, a re-run refused for want of a stopped run,
// and an escalation; and after its owner amended it and released the parking it
// still read blocked, because the status written while the raise stood was
// nobody's to clear.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

const raisedItemID = "yoyodyne-ifd.437.13"

// raisedRuns is the run records with stoppedRun as a raise of raisedItemID and
// otherStoppedRun as an ordinary stoppage of it.
func raisedRuns() fakeStoppedRuns {
	return fakeStoppedRuns{
		items:  map[string]string{stoppedRun: raisedItemID, otherStoppedRun: raisedItemID},
		raised: map[string]bool{stoppedRun: true},
	}
}

// raiseParked is the item as the raise leaves it: parked by the raise and, where
// the escalation after it blocked it, blocked.
func raiseParked(status string) beads.WorkItem {
	return beads.WorkItem{
		ID: raisedItemID, Title: "README rewrite", Status: status,
		Parking: domain.WorkItemParking(runstate.RaiseParking(stoppedRun, "the architect's review is not recorded")),
	}
}

// A repair continues a run that stopped, and a raise did not stop. Recording one
// spends a grant nothing will ever carry out, so it is refused before anything is
// spent, in a sentence naming the two decisions that do apply.
func TestARepairOnARaiseIsRefusedNamingTheTwoDecisionsThatApply(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{raisedItemID: raiseParked("open")}}
	reply := triageReplyAbout(t, tracker, budgets, raisedRuns(), trackerReply("Repairing it.",
		`{"action":"triage","id":"`+raisedItemID+`","run":"`+stoppedRun+`","decision":"repair","reason":"the two docs-map findings are all that is left"}`))

	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	failure := reply.Actions[0].Failure
	for _, want := range []string{"raised its item as one that cannot be met", `"rerun"`, `"retire-raise"`, "nothing was spent"} {
		if !strings.Contains(failure, want) {
			t.Fatalf("the refusal does not say %q: %s", want, failure)
		}
	}
	counters := budgets.counters(t, raisedItemID)
	if counters.RepairGrants != 0 || len(counters.Decisions) != 0 {
		t.Fatalf("a refused repair spent or recorded something: %#v", counters)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("a refused repair wrote to the item: %#v", tracker.updates)
	}

	// The same repair on an ordinary stoppage of the same item is still a repair.
	reply = triageReplyAbout(t, tracker, budgets, raisedRuns(), trackerReply("Repairing it.",
		`{"action":"triage","id":"`+raisedItemID+`","run":"`+otherStoppedRun+`","decision":"repair","reason":"every finding names a file and a line"}`))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want a repair of a stopped run recorded", reply.Actions)
	}
}

// Retiring a raise means nothing on a run that raised nothing.
func TestARaiseIsRetiredOnlyWhereARunRaisedOne(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{raisedItemID: raiseParked("blocked")}}
	reply := triageReplyAbout(t, tracker, budgets, raisedRuns(), trackerReply("Retiring it.",
		`{"action":"triage","id":"`+raisedItemID+`","run":"`+otherStoppedRun+`","decision":"retire-raise","reason":"moot"}`))

	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "no raise to retire") {
		t.Fatalf("actions = %#v, want retiring a raise refused on a stopped run", reply.Actions)
	}
	if len(tracker.updates) != 0 || len(tracker.unblocked) != 0 {
		t.Fatalf("a refused retirement wrote to the item: updates %#v, unblocked %#v", tracker.updates, tracker.unblocked)
	}
}

// Retiring a raise the amendment made moot puts the item back in the queue: the
// raise's own parking is lifted, the blocked status it left is cleared, and the
// raise's entry leaves the docket.
func TestRetiringARaiseLiftsItsParkingAndTheStatusItLeft(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{raisedItemID: raiseParked("blocked")}}
	docket := &fakeDocket{closes: 1}
	options := triageOptions(t, tracker, budgets, trackerReply("The amendment rewrote the item; the old change is moot.",
		`{"action":"triage","id":"`+raisedItemID+`","run":"`+stoppedRun+`","decision":"retire-raise","reason":"the amended done-means asks for a different document, so the raising run's rewrite is moot"}`))
	options.Reports = &fakeReports{}
	options.Stoppages = raisedRuns()
	options.Docket = docket
	reply := triageSend(t, options)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the raise retired", reply.Actions)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].change.Parking == nil || tracker.updates[0].change.Parking.Parked() {
		t.Fatalf("updates = %#v, want the raise's parking lifted", tracker.updates)
	}
	if !strings.Contains(tracker.updates[0].change.AppendNotes, "unmeetable raise retired") {
		t.Fatalf("the item's note does not say the raise was retired: %s", tracker.updates[0].change.AppendNotes)
	}
	if len(tracker.unblocked) != 1 || !strings.Contains(tracker.unblocked[0][1], stoppedRun) {
		t.Fatalf("unblocked = %#v, want the blocked status the raise left cleared", tracker.unblocked)
	}
	if len(docket.closed) != 1 || !classesEqual(docket.closed[0].Classes, []triage.Class{triage.ClassEscalation}) {
		t.Fatalf("closed = %#v, want the raise's entry closed", docket.closed)
	}
	decision, found := budgets.counters(t, raisedItemID).DecisionOf(stoppedRun)
	if !found || decision.Decision != runstate.TriageDecisionRetireRaise {
		t.Fatalf("decision = %#v, want the retirement on the durable record", decision)
	}
}

// A re-run recorded on a raise is recorded as a re-run, and says it will start
// from the raising run's change once the owner has released the item.
func TestARerunOnARaiseSaysItStartsFromThePreservedChange(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{raisedItemID: raiseParked("open")}}
	reply := triageReplyAbout(t, tracker, budgets, raisedRuns(), trackerReply("Run it again from its change.",
		`{"action":"triage","id":"`+raisedItemID+`","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is sound; lift it and make the two docs-map fixes"}`))

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the re-run recorded", reply.Actions)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, "starts from the raising run's preserved change") {
		t.Fatalf("the outcome does not say where the re-run starts:\n%s", rendered)
	}
	if budgets.counters(t, raisedItemID).Reruns != 1 {
		t.Fatalf("the re-run did not spend the item's re-run budget")
	}
}

// The owner's release of a raise's parking ends the raise, and clears the
// blocked status written while it stood, so a pull can select the item. A
// parking anybody else placed is released exactly as it always was.
func TestReleasingARaisesParkingEndsTheRaiseAndClearsItsStatus(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		raisedItemID: raiseParked("blocked"),
		"yoyodyne-ifd.77": {
			ID: "yoyodyne-ifd.77", Title: "Deferred", Status: "blocked",
			Parking: "deferred until team mode is scoped",
		},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Amended; releasing both.",
			`{"action":"unpark","id":"`+raisedItemID+`","reason":"the done-means is amended to the Lead Product Manager's review"}`,
			`{"action":"unpark","id":"yoyodyne-ifd.77","reason":"team mode is scoped"}`)},
		{SessionID: "session-1", FinalText: "Released."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	reply, err := openTestSession(t, options).Send(context.Background(), "The README item is amended.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if len(reply.Actions) != 2 || !reply.Actions[0].Applied || !reply.Actions[1].Applied {
		t.Fatalf("actions = %#v, want both released", reply.Actions)
	}
	if len(tracker.unblocked) != 1 || tracker.unblocked[0][0] != raisedItemID {
		t.Fatalf("unblocked = %#v, want only the raised item's status cleared", tracker.unblocked)
	}
	if !strings.Contains(tracker.unblocked[0][1], "unmeetable raise of run "+stoppedRun) {
		t.Fatalf("the cleared status does not say what the raise was: %s", tracker.unblocked[0][1])
	}
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	for _, want := range []string{
		"that ends the unmeetable raise of run " + stoppedRun,
		"the blocked status it left is cleared",
		`"retire-raise"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the release does not say %q:\n%s", want, rendered)
		}
	}
}

func classesEqual(left, right []triage.Class) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// A re-run recorded on an item somebody else parked says, in what the
// development manager is told she recorded, that the run waits until the item is
// unparked and who parked it, so she is not left expecting a run that will not
// start yet. A re-run of a raise over the raise's own parking says what it
// always said, and nothing about waiting on somebody else.
func TestARerunOnAnItemSomebodyElseParkedSaysItWaitsAndWhoParkedIt(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	item := beads.WorkItem{
		ID: raisedItemID, Title: "README rewrite", Status: "open",
		Parking: "held until the Slack route is decided",
		Notes:   "Parked by the Lead Product Manager in conversation chat-91253e0e, after turn 5747.\n\nReason: held until the Slack route is decided",
	}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{raisedItemID: item}}
	reply := triageReplyAbout(t, tracker, budgets, raisedRuns(), trackerReply("Run it again once it is released.",
		`{"action":"triage","id":"`+raisedItemID+`","run":"`+otherStoppedRun+`","decision":"rerun","reason":"the ground moved; start it again"}`))

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the re-run recorded", reply.Actions)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	for _, want := range []string{"parked by the Lead Product Manager in conversation chat-91253e0e", "will not start while it stays parked", "waits until the item is unparked"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the outcome does not say %q:\n%s", want, rendered)
		}
	}

	raised := &fakeTracker{items: map[string]beads.WorkItem{raisedItemID: raiseParked("open")}}
	reply = triageReplyAbout(t, raised, newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2), raisedRuns(), trackerReply("Run it again from its change.",
		`{"action":"triage","id":"`+raisedItemID+`","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is sound; lift it"}`))
	if rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions); strings.Contains(rendered, "will not start while it stays parked") {
		t.Fatalf("a re-run of a raise over its own parking was told it waits on somebody else's:\n%s", rendered)
	}
}
