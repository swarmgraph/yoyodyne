package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/backlogrepair"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A status left over from a stoppage that ended is corrected without anybody
// invoking a verb, and the item says afterwards what was changed, what made the
// old state stale, and why — which is what makes a correction made wrongly
// something a reader finds rather than something that happened quietly.
func TestClearingAStaleStatusRecordsWhatItCorrectedAndWhy(t *testing.T) {
	t.Parallel()

	stale := beads.WorkItem{ID: "yoyodyne-ifd.60", Title: "Its blocker landed", Status: "blocked",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.4", Type: beads.BlocksDependency, Status: "closed"}}}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{stale.ID: stale},
		blockedItems: []beads.WorkItem{stale},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Its blocker closed weeks ago; clearing the status.",
			`{"action":"repair","id":"yoyodyne-ifd.60","state":"status","reason":"nothing unfinished is behind it and the queue is passing it over"}`)},
		{SessionID: "session-1", FinalText: "Cleared, and it is back in the order."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.unblocked) != 1 || tracker.unblocked[0][0] != stale.ID {
		t.Fatalf("cleared = %#v, want the stale item's status cleared once", tracker.unblocked)
	}
	note := tracker.unblocked[0][1]
	for _, want := range []string{
		"Blocked status cleared as stale",
		"What made the old state stale:",
		"nothing unfinished blocks it",
		"nothing unfinished is behind it and the queue is passing it over",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the note recorded on the item is %q, want it to say %q", note, want)
		}
	}
}

// The dependency and the attribution are corrected the same way, each against
// the record that says the old state stopped being true.
func TestTheOtherTwoKindsOfStaleStateAreCorrectedAgainstTheirOwnRecords(t *testing.T) {
	t.Parallel()

	t.Run("a dependency on work that closed", func(t *testing.T) {
		t.Parallel()

		item := beads.WorkItem{ID: "yoyodyne-ifd.61", Title: "Waiting on nothing", Status: "open",
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.5", Type: beads.BlocksDependency, Status: "closed"}}}
		closed := beads.WorkItem{ID: "yoyodyne-ifd.5", Title: "Done", Status: "closed"}
		tracker := &fakeTracker{
			items: map[string]beads.WorkItem{item.ID: item, closed.ID: closed},
			open:  []beads.WorkItem{item},
		}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("The work it waits for is done.",
				`{"action":"repair","id":"yoyodyne-ifd.61","state":"dependency","depends_on":"yoyodyne-ifd.5","reason":"the link is the last thing saying it waits"}`)},
			{SessionID: "session-1", FinalText: "Retired the link."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Held = readHolds(nil)
		options.Directives = &fakeDirectives{}
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Tidy the queue.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
			t.Fatalf("actions = %#v", reply.Actions)
		}
		if len(tracker.unlinks) != 1 || tracker.unlinks[0] != [2]string{item.ID, closed.ID} {
			t.Fatalf("unlinks = %#v, want the dead link retired", tracker.unlinks)
		}
		if len(tracker.updates) != 1 || !strings.Contains(tracker.updates[0].change.AppendNotes, "which is closed") {
			t.Fatalf("updates = %#v, want the item to record why the link was dead", tracker.updates)
		}
	})

	t.Run("an attribution the goals no longer state", func(t *testing.T) {
		t.Parallel()

		item := beads.WorkItem{ID: "yoyodyne-ifd.62", Title: "Attributed before the goal was reworded", Status: "open",
			Notes: "Goal served: Run development almost without a person."}
		tracker := &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}, open: []beads.WorkItem{item}}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("The goal was reworded; the item still names the old wording.",
				`{"action":"repair","id":"yoyodyne-ifd.62","state":"attribution","goal":"Run development nearly autonomously","reason":"the amendment reworded the goal this has always served"}`)},
			{SessionID: "session-1", FinalText: "Re-attributed."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Held = readHolds(nil)
		options.Directives = &fakeDirectives{}
		options.Goals = recordedGoals("Run development nearly autonomously")
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Tidy the queue.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
			t.Fatalf("actions = %#v", reply.Actions)
		}
		if len(tracker.updates) != 1 {
			t.Fatalf("updates = %#v, want one re-attribution", tracker.updates)
		}
		notes := tracker.updates[0].change.AppendNotes
		for _, want := range []string{"the goals no longer state it", "Run development nearly autonomously"} {
			if !strings.Contains(notes, want) {
				t.Errorf("the note recorded on the item is %q, want it to say %q", notes, want)
			}
		}
		// And the repair takes. The item as the tracker leaves it — the old note
		// still there, this one appended after it — resolves, and a survey over it
		// offers nothing: the newest attribution line is the current claim, and the
		// tracker re-records the witness from the same write — which
		// TestAWrittenGoalIsWitnessedWhereReplacingTheNotesCannotReachIt in
		// internal/beads pins — so nothing about the item reads as orphaned any more.
		repaired := item
		repaired.Notes = item.Notes + "\n\n" + notes
		if attribution := options.Goals.AttributionOf(repaired.Notes, repaired.GoalWitness); !attribution.Resolved() {
			t.Fatalf("after the repair the item is %#v, want it attributed", attribution)
		}
		report := backlogrepair.Survey(backlogrepair.Records{
			Admitted: []beads.WorkItem{repaired},
			Held:     backlog.ReadHolds(nil),
			Goals:    options.Goals,
		})
		if report.Anything() {
			t.Fatalf("after the repair the survey still reports %#v / %#v", report.Repairs, report.Holds)
		}
	})
}

// The boundary the whole capability is bounded by: work somebody still has to
// release is reported with its reason and left exactly as it is, however stale
// its state looks.
func TestARepairOfHeldWorkChangesNothing(t *testing.T) {
	t.Parallel()

	stopped := beads.WorkItem{ID: "yoyodyne-ifd.63", Title: "Its change is still on a branch", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{stopped.ID: stopped},
		blockedItems: []beads.WorkItem{stopped},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Nothing unfinished blocks it as far as I can see.",
			`{"action":"repair","id":"yoyodyne-ifd.63","state":"status","reason":"the queue has been passing it over"}`)},
		{SessionID: "session-1", FinalText: "It is held; I left it alone."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(map[string]string{
		stopped.ID: "run run-9 stopped on it and its change is preserved, so a fresh run would start over on top of work that is still there",
	})
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "its change is preserved") {
		t.Fatalf("failure = %q, want it to restate what somebody has to release", reply.Actions[0].Failure)
	}
	if len(tracker.unblocked) != 0 || len(tracker.updates) != 0 {
		t.Fatalf("a held item was written to: cleared %#v, updated %#v", tracker.unblocked, tracker.updates)
	}
}

// The sharpest hold of the three, wired the way the harness wires it. Triage
// blocks an item in order to escalate it and leaves no dependency behind, so an
// escalated item reads as a blocked status with nothing at all standing behind
// it — which is exactly what a stale status looks like. What separates them is
// the hold, and this holds the real derivation over a real escalation record
// rather than a hold reason a test wrote out.
func TestAnItemAwaitingADecisionOnItsEscalationIsReportedAndLeftAlone(t *testing.T) {
	t.Parallel()

	escalated := beads.WorkItem{ID: "yoyodyne-ifd.72", Title: "Its stoppage is waiting on a decision", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{escalated.ID: escalated},
		blockedItems: []beads.WorkItem{escalated},
	}
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Escalations().Attempt(context.Background(), runstate.Escalation{
		DocketKey:  "stopped-run:run-13",
		RunID:      "run-13",
		WorkItemID: escalated.ID,
	}); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Nothing unfinished blocks it and it has no links at all.",
			`{"action":"repair","id":"yoyodyne-ifd.72","state":"status","reason":"the queue has been passing it over for days"}`)},
		{SessionID: "session-1", FinalText: "It is waiting on a decision; I left it."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = heldFromRunRecords{store: store}
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "stoppage") {
		t.Fatalf("failure = %q, want it to restate that the stoppage is waiting on a decision", reply.Actions[0].Failure)
	}
	if len(tracker.unblocked) != 0 || len(tracker.updates) != 0 {
		t.Fatalf("an escalated item was written to: cleared %#v, updated %#v", tracker.unblocked, tracker.updates)
	}

	// And the survey says the same thing about it, since a pass that reported it
	// as correctable is a pass that would go on asking.
	surveyProvider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Surveying.", `{"action":"survey"}`)},
		{SessionID: "session-1", FinalText: "One item held."},
	}}
	surveyOptions := testOptions(t, surveyProvider)
	surveyOptions.Tracker = tracker
	surveyOptions.Held = heldFromRunRecords{store: store}
	surveyOptions.Directives = &fakeDirectives{}
	surveyed := openTestSession(t, surveyOptions)
	surveyReply, err := surveyed.Send(context.Background(), "What is stale?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	detail := surveyReply.Actions[0].Detail
	if strings.Contains(detail, "State the records have made stale, which \"repair\" corrects") {
		t.Fatalf("the survey offers an escalated item as correctable: %q", detail)
	}
	for _, want := range []string{"Held until whoever each reason names releases it", "yoyodyne-ifd.72 [status]"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the survey says %q, want it to carry %q", detail, want)
		}
	}
}

// A status is cleared on evidence about every link the item records. The
// admitted work is the open and the blocked, so an item a run is working on
// right now is in neither listing — and the harness reads it rather than reading
// its absence as work that finished.
func TestAStatusIsNotClearedWhileABlockerIsBeingWorkedOn(t *testing.T) {
	t.Parallel()

	waiting := beads.WorkItem{ID: "yoyodyne-ifd.73", Title: "Its blocker is being worked on", Status: "blocked",
		// A listing that records the relation and says nothing about what became of
		// the work, which is what a Beads export frequently carries.
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.74", Type: beads.BlocksDependency}}}
	claimed := beads.WorkItem{ID: "yoyodyne-ifd.74", Title: "A run has it right now", Status: "in_progress"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{waiting.ID: waiting, claimed.ID: claimed},
		blockedItems: []beads.WorkItem{waiting},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Its blocker is in neither listing, so I read it as finished.",
			`{"action":"repair","id":"yoyodyne-ifd.73","state":"status","reason":"nothing in the queue blocks it"}`)},
		{SessionID: "session-1", FinalText: "The blocker is in progress; the status stands."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "yoyodyne-ifd.74") {
		t.Fatalf("failure = %q, want it to name the work nothing said was finished", reply.Actions[0].Failure)
	}
	if len(tracker.unblocked) != 0 {
		t.Fatalf("a status was cleared over a blocker a run is working on: %#v", tracker.unblocked)
	}
}

// A re-attribution names a goal like any other action that names one, and the
// same refusal covers it: a goal the goals do not state changes nothing, which
// is what stops a repair minting the orphan the next survey would report.
func TestARepairNamingAGoalTheGoalsDoNotStateWritesNothing(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{ID: "yoyodyne-ifd.75", Title: "Named a goal that was reworded", Status: "open",
		Notes: "Goal served: Run development almost without a person."}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}, open: []beads.WorkItem{item}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Re-attributing it to what I think the goal says now.",
			`{"action":"repair","id":"yoyodyne-ifd.75","state":"attribution","goal":"Run development with no people at all","reason":"the amendment reworded it"}`)},
		{SessionID: "session-1", FinalText: "The goals do not state that."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	options.Directives = &fakeDirectives{}
	options.Goals = recordedGoals("Run development nearly autonomously")
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "Run development with no people at all") {
		t.Fatalf("failure = %q, want it to name the goal nothing states", reply.Actions[0].Failure)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("an item was re-attributed to a goal the goals do not state: %#v", tracker.updates)
	}
}

// A conversation with no reading of what is held corrects nothing, rather than
// correcting everything it cannot see a hold on.
func TestARepairWithNothingToReadTheHoldsFromChangesNothing(t *testing.T) {
	t.Parallel()

	stale := beads.WorkItem{ID: "yoyodyne-ifd.64", Title: "Blocked by nothing", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{stale.ID: stale},
		blockedItems: []beads.WorkItem{stale},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Clearing it.",
			`{"action":"repair","id":"yoyodyne-ifd.64","state":"status","reason":"nothing unfinished is behind it"}`)},
		{SessionID: "session-1", FinalText: "It could not be judged."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "holding back after stopped runs") {
		t.Fatalf("failure = %q, want it to name the reading that did not happen", reply.Actions[0].Failure)
	}
	if len(tracker.unblocked) != 0 {
		t.Fatalf("a status was cleared with nothing to read the holds from: %#v", tracker.unblocked)
	}
}

// A directive in force is one of the three governance holds, so the record it is
// read from fails the way the holds do: a conversation with no directives to read
// corrects nothing rather than deciding that none is in force. The two halves of
// one hold failing in opposite directions is a hold with a hole in it.
func TestARepairWithNothingToReadTheDirectivesFromChangesNothing(t *testing.T) {
	t.Parallel()

	stale := beads.WorkItem{ID: "yoyodyne-ifd.76", Title: "Blocked by nothing", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{stale.ID: stale},
		blockedItems: []beads.WorkItem{stale},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Clearing it.",
			`{"action":"repair","id":"yoyodyne-ifd.76","state":"status","reason":"nothing unfinished is behind it"}`)},
		{SessionID: "session-1", FinalText: "It could not be judged."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "recorded directives") {
		t.Fatalf("failure = %q, want it to name the reading that did not happen", reply.Actions[0].Failure)
	}
	if len(tracker.unblocked) != 0 {
		t.Fatalf("a status was cleared with nothing to read the directives from: %#v", tracker.unblocked)
	}
}

// The staleness is the harness's judgement over the records as they stand as the
// act runs, not the role's assertion about a listing it read earlier.
func TestARepairTheRecordsDoNotSupportChangesNothing(t *testing.T) {
	t.Parallel()

	waiting := beads.WorkItem{ID: "yoyodyne-ifd.65", Title: "Genuinely waiting", Status: "blocked",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.66", Type: beads.BlocksDependency, Status: "open"}}}
	blocker := beads.WorkItem{ID: "yoyodyne-ifd.66", Title: "The work it waits for", Status: "open"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{waiting.ID: waiting, blocker.ID: blocker},
		open:         []beads.WorkItem{blocker},
		blockedItems: []beads.WorkItem{waiting},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("The listing I have says its blocker landed.",
			`{"action":"repair","id":"yoyodyne-ifd.65","state":"status","reason":"the queue is passing it over"}`)},
		{SessionID: "session-1", FinalText: "It is genuinely waiting after all."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "yoyodyne-ifd.66") {
		t.Fatalf("failure = %q, want it to name the unfinished work behind the status", reply.Actions[0].Failure)
	}
	if len(tracker.unblocked) != 0 {
		t.Fatalf("a status the records still call right was cleared: %#v", tracker.unblocked)
	}
}

// The pass that corrects this is the pass that takes a survey, so the survey is
// where both lists have to be: what may be corrected, and what is held with the
// reason restated.
func TestASurveyListsTheStaleStateAndWhatIsHeld(t *testing.T) {
	t.Parallel()

	open := beads.WorkItem{ID: "yoyodyne-ifd.67", Title: "A dead link", Status: "open",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.6", Type: beads.BlocksDependency, Status: "closed"}}}
	held := beads.WorkItem{ID: "yoyodyne-ifd.68", Title: "Its change is on a branch", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{open.ID: open, held.ID: held},
		open:         []beads.WorkItem{open},
		blockedItems: []beads.WorkItem{held},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Taking a survey before I correct anything.", `{"action":"survey"}`)},
		{SessionID: "session-1", FinalText: "One link to retire, one item held."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(map[string]string{
		held.ID: "run run-11 stopped on it and its change is preserved",
	})
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is stale?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	detail := reply.Actions[0].Detail
	for _, want := range []string{
		"State the records have made stale",
		"yoyodyne-ifd.67 [dependency]",
		"Held until whoever each reason names releases it",
		"yoyodyne-ifd.68 [status]",
		"its change is preserved",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the survey says %q, want it to carry %q", detail, want)
		}
	}
}

// A survey read by a role that cannot correct any of this says nothing about it:
// stale state in front of a role with no authority over it is work to route to
// somebody else, which is the relaying this was meant to end.
func TestASurveyByARoleThatMayNotRepairSaysNothingAboutStaleState(t *testing.T) {
	t.Parallel()

	stale := beads.WorkItem{ID: "yoyodyne-ifd.69", Title: "A dead link", Status: "open",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.7", Type: beads.BlocksDependency, Status: "closed"}}}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{stale.ID: stale}, open: []beads.WorkItem{stale}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Surveying.", `{"action":"survey"}`)},
		{SessionID: "session-1", FinalText: "One open item."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is open?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(reply.Actions[0].Detail, "State the records have made stale") {
		t.Fatalf("the development manager's survey carries state it cannot correct: %q", reply.Actions[0].Detail)
	}
	if slices := tracker.listed; len(slices) != 1 || slices[0] != openWorkItemStatus {
		t.Fatalf("statuses surveyed = %#v, want the open items alone", tracker.listed)
	}
}

// Correcting backlog state is the product manager's, and a role that does not
// hold the capability is refused before anything is carried out.
func TestOnlyARoleHoldingTheCapabilityMayRepair(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{}}
	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1",
		FinalText: trackerReply("Clearing it.",
			`{"action":"repair","id":"yoyodyne-ifd.70","state":"status","reason":"it looks stale"}`)}}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	_, err := session.Send(context.Background(), "Tidy the queue.")
	var refusal *AuthorityError
	if !errors.As(err, &refusal) {
		t.Fatalf("Send() error = %v, want an authority refusal", err)
	}
	if len(tracker.unblocked) != 0 {
		t.Fatalf("a refused repair still wrote to the tracker: %#v", tracker.unblocked)
	}
}

// An argument the named state has no use for is refused rather than ignored: a
// repair that carried the goal for a link it was retiring was misunderstood, and
// correcting the part that parsed would correct something nobody named.
func TestARepairIsRefusedWhereItDoesNotSayWhatItIsCorrecting(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		action TrackerAction
		says   string
	}{
		{
			name:   "naming no state",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", Reason: "why"},
			says:   "repair requires \"state\"",
		},
		{
			name:   "naming a state nothing recognizes",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", State: "priority", Reason: "why"},
			says:   "is not a kind of stale state",
		},
		{
			name:   "retiring a link without naming it",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", State: backlogrepair.ClassDependency, Reason: "why"},
			says:   "requires \"depends_on\"",
		},
		{
			name: "re-attributing without a goal",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71",
				State: backlogrepair.ClassAttribution, Reason: "why"},
			says: "goal",
		},
		{
			name: "clearing a status and naming a goal",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", State: backlogrepair.ClassStatus,
				Goal: "Run development nearly autonomously", Reason: "why"},
			says: "takes no \"goal\"",
		},
		{
			name:   "correcting a state with no reason",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", State: backlogrepair.ClassStatus},
			says:   "reason",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.action.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %#v", testCase.action)
			}
			if !strings.Contains(err.Error(), testCase.says) {
				t.Fatalf("Validate() refused with %q, want it to say %q", err, testCase.says)
			}
		})
	}
}

// readHolds is what the harness is holding back after stopped runs, as a conversation
// reads it, keyed by item with the reason each is held. A nil map is a reading
// that found nothing held, which is not the same answer as no reading at all.
func readHolds(reasons map[string]string) HeldWork {
	var held map[string]backlog.Hold
	if reasons != nil {
		held = make(map[string]backlog.Hold, len(reasons))
		for id, reason := range reasons {
			held[id] = backlog.Hold{Reason: reason}
		}
	}
	return fakeHeldWork{holds: backlog.ReadHolds(held)}
}

type fakeHeldWork struct {
	holds backlog.Holds
	err   error
}

// heldFromRunRecords is the wiring the harness itself uses: the shared read-model
// derivation over the harness's own record of stopped work. It is here rather
// than a hold reason written out by hand because what has to be established is
// that an escalation reaches this refusal at all, which a written-out reason
// would assume rather than show.
type heldFromRunRecords struct {
	store *runstate.Store
	// remains is the repository the derivation asks whether a stopped run's
	// change is still there, and it is a fake here for the reason the store is
	// real: what has to be established is that the answer decides the hold.
	remains readmodel.Remains
}

func (h heldFromRunRecords) HeldForAPerson(ctx context.Context) (backlog.Holds, error) {
	return readmodel.HeldForAPerson(ctx, h.store, h.store.Triage(), h.remains, nil)
}

func (f fakeHeldWork) HeldForAPerson(context.Context) (backlog.Holds, error) {
	if f.err != nil {
		return backlog.Holds{}, f.err
	}
	return f.holds, nil
}

// The yoyodyne-ifd.372 shape, 2026-09-19, end to end. The run stopped in the
// developing phase on a provider fault with its change preserved; the item's
// notes said the branch and worktree were checked and there; and the product
// manager's repair cleared the blocked status because the survey had listed the
// item under the state a repair corrects rather than under held. What decides
// the hold now is the repository rather than the run's removal flags: a stopped
// run whose branch or worktree exists is held, the survey lists it only under
// held with the run named, and a repair on it is refused with the same sentence.
func TestAStoppedRunWhoseChangeTheRepositoryHoldsIsNeverRepairedAndTheSurveySaysSo(t *testing.T) {
	t.Parallel()

	const runID = "run-192522d857b8f1f1b7624dccc7e4eb70"
	stopped := beads.WorkItem{ID: "yoyodyne-ifd.372", Title: "A recurring task that skips says why, and resumes on its own", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{stopped.ID: stopped},
		blockedItems: []beads.WorkItem{stopped},
	}
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// The record as the sweep left it: both artifacts flagged removed, each
	// dated. The repository below says otherwise, and the repository is what is
	// asked.
	completed := time.Date(2026, 9, 16, 10, 5, 41, 0, time.UTC)
	swept := time.Date(2026, 9, 19, 13, 0, 57, 0, time.UTC)
	if err := store.Create(runstate.State{
		SchemaVersion:   runstate.StateSchemaVersion,
		RunID:           runID,
		ProductID:       "yoyodyne",
		RepositoryID:    "yoyodyne",
		WorkItemID:      stopped.ID,
		WorkItemTitle:   stopped.Title,
		Backend:         "claude-code",
		Status:          runstate.StatusFailed,
		Phase:           runstate.PhaseDeveloping,
		StartedAt:       completed.Add(-2 * time.Hour),
		UpdatedAt:       completed,
		CompletedAt:     &completed,
		WorktreePath:    "/state/worktrees/yoyodyne-ifd-372-192522d8",
		Branch:          "yoyodyne/yoyodyne-ifd-372/192522d8",
		BranchRemoved:   true,
		BranchSweptAt:   &swept,
		WorktreeRemoved: true,
		WorktreeSweptAt: &swept,
		BaseCommit:      "449b375812b5f92a3d64efc33f7fcf26b41584e3",
		TargetBranch:    "main",
		Blocker:         "Yoyodyne stopped this item: the provider kept ending its invocations without judging the work, and the relaunch budget is spent.",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	held := heldFromRunRecords{store: store, remains: repositoryHolding{runID: {BranchExists: true, WorktreePresent: true}}}

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Nothing unfinished blocks it and it is no longer held behind a preserved run.",
			`{"action":"repair","id":"yoyodyne-ifd.372","state":"status","reason":"status says blocked, nothing unfinished blocks it, and it is no longer held behind a preserved run"}`)},
		{SessionID: "session-1", FinalText: "It is held; I left it alone."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = held
	options.Directives = &fakeDirectives{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	refusal := reply.Actions[0].Failure
	for _, want := range []string{"is held and nothing here touches it", runID, "its change is preserved (branch and worktree checked and there)"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("failure = %q, want it to say %q", refusal, want)
		}
	}
	if len(tracker.unblocked) != 0 || len(tracker.updates) != 0 {
		t.Fatalf("a held item was written to: cleared %#v, updated %#v", tracker.unblocked, tracker.updates)
	}

	// The survey lists it under held, naming the run, and nowhere else — with the
	// sentence the repair was refused with, so the pass that reads the survey
	// and the act it then asks for cannot come to different answers.
	surveyProvider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Surveying.", `{"action":"survey"}`)},
		{SessionID: "session-1", FinalText: "One item held."},
	}}
	surveyOptions := testOptions(t, surveyProvider)
	surveyOptions.Tracker = tracker
	surveyOptions.Held = held
	surveyOptions.Directives = &fakeDirectives{}
	surveyed := openTestSession(t, surveyOptions)
	surveyReply, err := surveyed.Send(context.Background(), "What is stale?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	detail := surveyReply.Actions[0].Detail
	if strings.Contains(detail, "State the records have made stale, which \"repair\" corrects") {
		t.Fatalf("the survey offers a preserved stoppage as correctable: %q", detail)
	}
	for _, want := range []string{
		"Held until whoever each reason names releases it",
		"- yoyodyne-ifd.372 [status]",
		"held because run " + runID + " stopped on it and its change is preserved (branch and worktree checked and there)",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the survey says %q, want it to carry %q", detail, want)
		}
	}
}

// A repair reports what the write left behind, not what the read before it
// found. Twice in one week the outcome said "cleared" and, on the same line,
// that the item was blocked as the tracker held it now — the pre-write reading,
// appended as if it were the result. The result is the item as the tracker
// returns it after the write: open, and said so; or not open, and then the
// write did not land and the repair failed.
func TestAClearedStatusIsReportedFromTheWriteAndAWriteThatDidNotLandFails(t *testing.T) {
	t.Parallel()

	stale := beads.WorkItem{ID: "yoyodyne-ifd.346", Title: "A recorded triage decision carries itself out", Status: "blocked",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.4", Type: beads.BlocksDependency, Status: "closed"}}}
	repairAction := trackerReply("Nothing unfinished blocks it.",
		`{"action":"repair","id":"yoyodyne-ifd.346","state":"status","reason":"no run holds it"}`)

	t.Run("landed", func(t *testing.T) {
		t.Parallel()

		tracker := &fakeTracker{items: map[string]beads.WorkItem{stale.ID: stale}, blockedItems: []beads.WorkItem{stale}}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: repairAction},
			{SessionID: "session-1", FinalText: "Cleared."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Held = readHolds(nil)
		options.Directives = &fakeDirectives{}
		reply, err := openTestSession(t, options).Send(context.Background(), "Tidy the queue.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
			t.Fatalf("actions = %#v, want the repair applied", reply.Actions)
		}
		summary := reply.Actions[0].Summary
		if !strings.Contains(summary, "the tracker holds it open") {
			t.Errorf("summary = %q, want the status the write left", summary)
		}
		if strings.Contains(summary, "is blocked as the tracker holds it now") {
			t.Errorf("summary = %q, which reports the pre-write reading as the result", summary)
		}
		if reply.Actions[0].TargetStatus != "open" {
			t.Errorf("target status = %q, want the item as the write left it", reply.Actions[0].TargetStatus)
		}
	})

	t.Run("did not land", func(t *testing.T) {
		t.Parallel()

		tracker := &fakeTracker{items: map[string]beads.WorkItem{stale.ID: stale}, blockedItems: []beads.WorkItem{stale}, unblockLeaves: "blocked"}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: repairAction},
			{SessionID: "session-1", FinalText: "It did not clear."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Held = readHolds(nil)
		options.Directives = &fakeDirectives{}
		reply, err := openTestSession(t, options).Send(context.Background(), "Tidy the queue.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Actions) != 1 || reply.Actions[0].Applied {
			t.Fatalf("actions = %#v, want the repair reported as failed", reply.Actions)
		}
		if failure := reply.Actions[0].Failure; !strings.Contains(failure, `holds yoyodyne-ifd.346 at "blocked"`) || !strings.Contains(failure, "did not land") {
			t.Errorf("failure = %q, want the write that did not land named", failure)
		}
		if strings.Contains(reply.Actions[0].Summary, "cleared") {
			t.Errorf("summary = %q, which says cleared over a write that did not land", reply.Actions[0].Summary)
		}
	})
}

// repositoryHolding is a repository that holds exactly the artifacts it lists,
// by run.
type repositoryHolding map[string]gitworktree.Survival

func (r repositoryHolding) Survives(_ context.Context, worktree gitworktree.Worktree) (gitworktree.Survival, error) {
	return r[worktree.RunID], nil
}
