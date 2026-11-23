package chat

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// agreementConversation opens an architect's conversation over a repository
// holding one design with two revisions and a tracker holding one open item, and
// has the architect's one turn carry the actions given.
func agreementConversation(t *testing.T, role domain.AgentRole, actions ...string) (*fakeTracker, Reply, string) {
	t.Helper()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.9": {ID: "yoyodyne-ifd.9", Title: "Record a decision", Status: "open", Notes: "Decision: keep it as is."},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Checked the design against the item.", actions...)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options, _ := documentOptions(t, provider)
	options.Role = role
	options.Agent = string(role)
	options.Tracker = tracker
	store := options.Documents.(artifact.Store)
	now := fixedClock{}.Now()
	if _, err := store.Create(domain.RoleArchitect, artifact.Draft{ID: "agreed-design", Kind: artifact.KindDesign, Title: "A design", Directory: "docs/designs", Body: "# Design\n\nOne.\n", Reason: "first"}, now); err != nil {
		t.Fatal(err)
	}
	second := "# Design\n\nTwo.\n"
	if _, err := store.Amend(domain.RoleArchitect, "agreed-design", artifact.Amendment{Body: &second, Reason: "second"}, now); err != nil {
		t.Fatal(err)
	}
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Does the design still agree with the item?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return tracker, reply, session.state.ConversationID
}

// The architect's agreement action has the harness add one note naming the
// document, its revision, and the time the item was read, and nothing else on
// the item changes.
func TestTheArchitectsAgreementIsWrittenAsOneNoteOnTheItem(t *testing.T) {
	t.Parallel()

	tracker, reply, conversation := agreementConversation(t, domain.RoleArchitect,
		`{"action":"agree","id":"yoyodyne-ifd.9","document":"agreed-design","revision":1,"reason":"the design keeps the decision"}`)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("outcomes = %+v, want the agreement applied", reply.Actions)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].id != "yoyodyne-ifd.9" {
		t.Fatalf("updates = %+v, want one on yoyodyne-ifd.9", tracker.updates)
	}
	change := tracker.updates[0].change
	if !reflect.DeepEqual(change, beads.WorkItemChange{AppendNotes: change.AppendNotes}) {
		t.Fatalf("the update changed more than the notes: %+v", change)
	}
	for _, want := range []string{
		"revision 1 of the document agreed-design",
		"as it stood at",
		"2026-08-15T12:00:00Z",
		"found that they agree",
		"conversation " + conversation,
		"Reason: the design keeps the decision",
	} {
		if !strings.Contains(change.AppendNotes, want) {
			t.Errorf("the note %q does not contain %q", change.AppendNotes, want)
		}
	}
}

// An item or a document that does not exist, or a revision the document does
// not have, is refused and nothing is written.
func TestTheAgreementIsRefusedForWhatDoesNotExist(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		action string
		want   string
	}{
		"no such item": {
			`{"action":"agree","id":"yoyodyne-ifd.404","document":"agreed-design","revision":0,"reason":"agrees"}`,
			"yoyodyne-ifd.404",
		},
		"no such document": {
			`{"action":"agree","id":"yoyodyne-ifd.9","document":"no-such-design","revision":0,"reason":"agrees"}`,
			`there is no document "no-such-design"`,
		},
		"no such revision": {
			`{"action":"agree","id":"yoyodyne-ifd.9","document":"agreed-design","revision":2,"reason":"agrees"}`,
			"there is no revision 2",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker, reply, _ := agreementConversation(t, domain.RoleArchitect, test.action)
			if len(reply.Actions) != 1 || reply.Actions[0].Applied {
				t.Fatalf("outcomes = %+v, want the agreement refused", reply.Actions)
			}
			if !strings.Contains(reply.Actions[0].Failure, test.want) {
				t.Fatalf("failure = %q, want it to contain %q", reply.Actions[0].Failure, test.want)
			}
			if len(tracker.updates) != 0 {
				t.Fatalf("a refused agreement wrote %+v", tracker.updates)
			}
		})
	}
}

// Only a role holding the agreement capability may ask for it, and the
// architect holds no other write on an item.
func TestOnlyTheArchitectMayRecordAgreement(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager} {
		if authorities[role].MayAct(actionAgree) {
			t.Errorf("%s may record agreement", role)
		}
	}
	architect := authorities[domain.RoleArchitect]
	if !architect.MayAct(actionAgree) {
		t.Fatal("the architect may not record agreement")
	}
	for _, action := range architect.TrackerActions {
		switch action {
		case actionRead, actionSurvey, actionAmendment, actionAgree:
		default:
			t.Errorf("the architect may also %q on the tracker", action)
		}
	}
}

// The action needs a document and a revision.
func TestAnAgreementNamesADocumentAndARevision(t *testing.T) {
	t.Parallel()

	for _, action := range []TrackerAction{
		{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", Revision: intPtr(0)},
		{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", Document: "d"},
		{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", Document: "d", Revision: intPtr(-1)},
		{Action: actionAgree, Reason: "r", Document: "d", Revision: intPtr(0)},
	} {
		if err := action.Validate(); err == nil {
			t.Errorf("%+v was accepted", action)
		}
	}
	if err := (TrackerAction{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", Document: "d", Revision: intPtr(0)}).Validate(); err != nil {
		t.Errorf("a complete agreement was refused: %v", err)
	}
}

func intPtr(n int) *int { return &n }

// The architect's instructions name the agreement action among the ones she may
// ask for, and no longer say reading is all she may do to the tracker, so the
// action is not presented to her as one that would be refused.
func TestTheArchitectsInstructionsOfferTheAgreementAction(t *testing.T) {
	t.Parallel()

	for _, want := range []string{`"action":"agree"`, "Those four are the only actions you may ask for", agreementClause} {
		if !strings.Contains(architectContract, want) {
			t.Errorf("the architect's contract does not carry %q", want)
		}
	}
	for _, stale := range []string{"Those three are the only actions", "Those two are the only", "looking is the whole of what you may do"} {
		if strings.Contains(architectContract, stale) {
			t.Errorf("the architect's contract still says %q", stale)
		}
	}
}
