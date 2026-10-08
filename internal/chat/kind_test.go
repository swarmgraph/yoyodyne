package chat

// The kind half of admission: every item the harness admits says whether it is
// a bug fix or a feature, in the tracker's type field, because the rework rate
// is read from it. An admission that says neither is refused with the kind
// named as what is missing, and the survey names the open items that have none.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// admitWith runs one turn whose reply is a single create action and returns
// what the turn did and what reached the tracker.
func admitWith(t *testing.T, action string) (Reply, *fakeTracker) {
	t.Helper()
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.", action)},
		{SessionID: "session-1", FinalText: "Done."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return reply, tracker
}

// Each kind becomes the created item's tracker type, and an admission carrying
// the bug label with no kind is a bug, which is what that label always meant.
func TestAnAdmissionRecordsItsKindAsTheTrackerType(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		fields string
		want   string
	}{
		{name: "bug", fields: `"kind":"bug",`, want: "bug"},
		{name: "feature", fields: `"kind":"feature",`, want: "feature"},
		{name: "bug label", fields: `"labels":["bug"],`, want: "bug"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reply, tracker := admitWith(t, `{"action":"create",`+test.fields+`"title":"Typed work","description":"d","goal":"`+recordedGoal+`","reason":"r"}`)
			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v, want the creation carried out", reply.Actions)
			}
			if len(tracker.created) != 1 || tracker.created[0].Type != test.want {
				t.Fatalf("created = %#v, want type %q", tracker.created, test.want)
			}
		})
	}
}

// A creation that states no kind creates nothing, and the refusal names the
// kind as the thing to add.
func TestAnAdmissionWithNoKindIsRefusedNamingTheKind(t *testing.T) {
	t.Parallel()

	reply, tracker := admitWith(t, `{"action":"create","title":"Untyped work","description":"d","goal":"`+recordedGoal+`","reason":"r"}`)
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want nothing created", tracker.created)
	}
	// The block is refused whole and handed back to the role to correct.
	if len(reply.HandedBack) != 1 || !strings.Contains(reply.HandedBack[0], "kind is missing") {
		t.Fatalf("handed back = %#v, want a refusal naming the kind", reply.HandedBack)
	}
}

// A kind that is neither, and a feature that is labelled a bug, are refused for
// the same reason: the rate would count them as something they did not say.
func TestAnAdmissionWithAnUnknownOrContradictoryKindIsRefused(t *testing.T) {
	t.Parallel()

	for _, action := range []TrackerAction{
		{Action: actionCreate, Title: "t", Description: "d", Goal: recordedGoal, Kind: "chore", Reason: "r"},
		{Action: actionCreate, Title: "t", Description: "d", Goal: recordedGoal, Kind: domain.WorkItemKindFeature, Labels: []string{"bug"}, Reason: "r"},
	} {
		if err := action.Validate(); err == nil || !strings.Contains(err.Error(), "kind") {
			t.Fatalf("Validate(%#v) = %v, want a refusal about the kind", action, err)
		}
	}
}

// A proposal is held to the same requirement as a creation, and the item an
// approved proposal creates carries its kind as its type.
func TestAProposalWithNoKindIsRefusedNamingTheKind(t *testing.T) {
	t.Parallel()

	untyped := Proposal{Title: "t", Description: "d", Rationale: "r", Goal: recordedGoal}
	if err := untyped.Validate(); err == nil || !strings.Contains(err.Error(), "kind is missing") {
		t.Fatalf("Validate() = %v, want a refusal naming the kind", err)
	}
	for _, kind := range domain.WorkItemKinds {
		typed := untyped
		typed.Kind = kind
		if err := typed.Validate(); err != nil {
			t.Fatalf("Validate() with kind %q = %v", kind, err)
		}
		if typed.issueType() != string(kind) {
			t.Fatalf("issueType() = %q, want %q", typed.issueType(), kind)
		}
	}
	// A proposal recorded before kinds were required keeps its place on the
	// operator's list, and the item approved from it is created untyped.
	restored := restoredProposal("conversation-1", (PendingProposal{ID: "1.1", Turn: 1, Proposal: untyped}).recorded())
	if restored.Proposal.issueType() != untypedIssueType {
		t.Fatalf("issueType() = %q, want %q", restored.Proposal.issueType(), untypedIssueType)
	}
	typed := untyped
	typed.Kind = domain.WorkItemKindBug
	if back := restoredProposal("conversation-1", (PendingProposal{ID: "1.1", Turn: 1, Proposal: typed}).recorded()); back.Proposal.Kind != domain.WorkItemKindBug {
		t.Fatalf("restored kind = %q, want the kind recorded with the proposal", back.Proposal.Kind)
	}
}

// The survey names the open items with no kind, leaves out the typed ones, the
// ones labelled a bug, and epics, which never merge as changes of their own.
func TestTheSurveyNamesOpenItemsWithNoKind(t *testing.T) {
	t.Parallel()

	detail := renderOpenQueueEvidence([]beads.WorkItem{
		{ID: "yoyodyne-1", Title: "A feature", Status: "open", IssueType: "feature"},
		{ID: "yoyodyne-2", Title: "A bug", Status: "open", IssueType: "bug"},
		{ID: "yoyodyne-3", Title: "Labelled a bug", Status: "open", IssueType: "task", Labels: []string{"bug"}},
		{ID: "yoyodyne-4", Title: "Nobody typed this", Status: "open", IssueType: "task"},
		{ID: "yoyodyne-5", Title: "A grouping", Status: "open", IssueType: "epic"},
	}, recordedGoals(recordedGoal))
	at := strings.Index(detail, "Open items with no kind recorded")
	if at < 0 {
		t.Fatalf("survey = %q, want the untyped items named", detail)
	}
	line := detail[at : at+strings.Index(detail[at:], "\n")]
	if !strings.Contains(line, "yoyodyne-4") {
		t.Fatalf("untyped line = %q, want yoyodyne-4 named", line)
	}
	for _, typed := range []string{"yoyodyne-1", "yoyodyne-2", "yoyodyne-3", "yoyodyne-5"} {
		if strings.Contains(line, typed) {
			t.Fatalf("untyped line = %q, names %s", line, typed)
		}
	}

	allTyped := renderOpenQueueEvidence([]beads.WorkItem{{ID: "yoyodyne-1", Title: "A feature", Status: "open", IssueType: "feature"}}, recordedGoals(recordedGoal))
	if strings.Contains(allTyped, "no kind recorded") {
		t.Fatalf("survey = %q, want nothing named where every item has a kind", allTyped)
	}
}

// An item admitted untyped acquires a kind through an update, with the reason
// recorded beside it.
func TestAnUpdateGivesAnUntypedItemItsKind(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.7": {ID: "yoyodyne-ifd.7", Title: "Admitted before kinds", Status: "open", IssueType: "task"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Typing it.",
			`{"action":"update","id":"yoyodyne-ifd.7","kind":"feature","reason":"it is planned work"}`)},
		{SessionID: "session-1", FinalText: "Typed."},
	}})
	options.Tracker = tracker
	reply, err := openTestSession(t, options).Send(context.Background(), "type it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the update carried out", reply.Actions)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].change.Kind != domain.WorkItemKindFeature ||
		!strings.Contains(tracker.updates[0].change.AppendNotes, "Recorded the kind as feature") {
		t.Fatalf("updates = %#v, want the kind set with its reason", tracker.updates)
	}
}
