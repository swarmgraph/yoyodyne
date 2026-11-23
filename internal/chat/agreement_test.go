package chat

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/designagreement"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// agreementStood is when the tracker last changed the item the tests agree
// with, which is earlier than the conversation's clock, so a note naming the
// time it was written rather than the time the item last changed is caught.
var agreementStood = time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC)

// agreementWaiting is a revision of the design the architect has written and
// not yet published, named as the list of overlaps names it.
var agreementWaiting = runstate.PendingWrite{ID: "document-7.1", Artifact: "agreed-design", Action: "revise", Body: "# Design\n\nThree.\n", Reason: "third", Answers: []string{"yoyodyne-ifd.9"}, WrittenAt: agreementStood.Add(-time.Hour)}

// agreementConversation opens an architect's conversation over a repository
// holding one design with two revisions, a third revision of it waiting to be
// published, and a tracker holding the item given, and has the architect's one
// turn carry the actions given.
func agreementConversation(t *testing.T, role domain.AgentRole, item beads.WorkItem, actions ...string) (*fakeTracker, Reply) {
	t.Helper()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}}
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
	// The waiting revision is put in the conversation's record, which is where a
	// conversation finds the documents it has written and not yet published.
	identity := runstate.ConversationIdentity{Agent: string(role), Role: role}
	openTestSession(t, options)
	recorded, err := options.Store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	recorded.PendingWrites = append(recorded.PendingWrites, agreementWaiting)
	if err := options.Store.Save(recorded); err != nil {
		t.Fatal(err)
	}
	other := recorded
	if other.ConversationID, err = runstate.NewConversationID(); err != nil {
		t.Fatal(err)
	}
	other.Agent, other.Role = "lead-product-manager", domain.RoleProductManager
	goals := runstate.PendingWrite{ID: "document-4.1", Artifact: "waiting-goals", Action: "create", Body: "# Goals\n", Reason: "first"}
	back := goals
	back.ID = "document-5.1"
	other.PendingWrites = []runstate.PendingWrite{goals}
	other.HandedBack = []runstate.HandedBackDocument{runstate.HandedBackFrom(back, "run-1", "returned", agreementStood)}
	if err := options.Store.Save(other); err != nil {
		t.Fatal(err)
	}
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Does the design still agree with the item?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return tracker, reply
}

func agreementItem() beads.WorkItem {
	return beads.WorkItem{ID: "yoyodyne-ifd.9", Title: "Record a decision", Status: "open", Notes: "Decision: keep it as is.", UpdatedAt: agreementStood}
}

// The architect's agreement action has the harness add one note naming the
// document, its revision, and the time the tracker last changed the item as she
// checked it, and nothing else on the item changes.
func TestTheArchitectsAgreementIsWrittenAsOneNoteOnTheItem(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		revision string
		named    string
	}{
		"a landed revision by number":          {`1`, "1"},
		"a waiting revision by its identifier": {`"document-7.1"`, "document-7.1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker, reply := agreementConversation(t, domain.RoleArchitect, agreementItem(),
				`{"action":"agree","id":"yoyodyne-ifd.9","document":"agreed-design","revision":`+test.revision+`,"item_changed":"2026-08-14T02:30:00-07:00","reason":"the design keeps the decision"}`)

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
			want := designagreement.Note("agreed-design", test.named, agreementStood, fixedClock{}.Now())
			if change.AppendNotes != want {
				t.Fatalf("the note = %q, want %q", change.AppendNotes, want)
			}
			if !strings.Contains(change.AppendNotes, "as it stood at 2026-08-14T09:30:00Z") {
				t.Fatalf("the note %q does not name the time the item last changed", change.AppendNotes)
			}
		})
	}
}

func agreeAction(revision string) string {
	return `{"action":"agree","id":"yoyodyne-ifd.9","document":"agreed-design","revision":"` + revision + `","item_changed":"2026-08-14T02:30:00-07:00","reason":"agrees"}`
}

// A second document is agreed with the item as she read it although the first
// agreement's own note has changed the item since; a change made between is not
// taken for one.
func TestAnEarlierAgreementNoteIsNotAChangeToTheItem(t *testing.T) {
	t.Parallel()

	recorded := agreementStood.Add(time.Hour)
	item := agreementItem()
	item.FramedNotes = item.Notes + "\n" + beads.FrameNote(designagreement.Note("other-design", "document-6.1", agreementStood, recorded))
	item.UpdatedAt = recorded.Add(time.Second)
	if _, reply := agreementConversation(t, domain.RoleArchitect, item, agreeAction("document-7.1")); len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("outcomes = %+v, want the agreement applied", reply.Actions)
	}
	item.UpdatedAt = recorded.Add(time.Hour)
	if tracker, reply := agreementConversation(t, domain.RoleArchitect, item, agreeAction("document-7.1")); len(reply.Actions) != 1 || reply.Actions[0].Applied || len(tracker.updates) != 0 {
		t.Fatalf("outcomes = %+v, want the changed item refused", reply.Actions)
	}
}

// A document another role has waiting, and one handed back to it, are agreed
// with as the architect's own are, because the list of overlaps names them too.
func TestTheArchitectAgreesADocumentWaitingInAnotherRolesConversation(t *testing.T) {
	t.Parallel()

	for _, revision := range []string{"document-4.1", "document-5.1"} {
		action := strings.Replace(agreeAction(revision), "agreed-design", "waiting-goals", 1)
		tracker, reply := agreementConversation(t, domain.RoleArchitect, agreementItem(), action)
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
			t.Fatalf("outcomes = %+v, want the agreement with %s applied", reply.Actions, revision)
		}
		if want := designagreement.Note("waiting-goals", revision, agreementStood, fixedClock{}.Now()); tracker.updates[0].change.AppendNotes != want {
			t.Fatalf("the note = %q, want %q", tracker.updates[0].change.AppendNotes, want)
		}
	}
}

// The architect's read of an item prints the time her agreement gives back.
func TestTheArchitectsReadPrintsWhenTheItemLastChanged(t *testing.T) {
	t.Parallel()

	_, reply := agreementConversation(t, domain.RoleArchitect, agreementItem(), `{"action":"read","id":"yoyodyne-ifd.9"}`)
	want := "item_changed: " + designagreement.ItemChanged(agreementStood)
	if len(reply.Actions) != 1 || !strings.Contains(reply.Actions[0].Detail, want) {
		t.Fatalf("outcomes = %+v, want %q in the read", reply.Actions, want)
	}
}

// The note the action writes is the one the list of overlaps reads as answering
// the overlap: recorded on the item, it takes the overlap off the list.
func TestTheAgreementNoteAnswersTheOverlap(t *testing.T) {
	t.Parallel()

	tracker, reply := agreementConversation(t, domain.RoleArchitect, agreementItem(),
		`{"action":"agree","id":"yoyodyne-ifd.9","document":"agreed-design","revision":"document-7.1","item_changed":"2026-08-14T02:30:00-07:00","reason":"agrees"}`)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("outcomes = %+v, want the agreement applied", reply.Actions)
	}
	item := agreementItem()
	if listed := orchestrator.FindDocumentOverlaps([]runstate.PendingWrite{agreementWaiting}, []beads.WorkItem{item}, nil); len(listed) != 1 {
		t.Fatalf("before the note, overlaps = %+v, want one", listed)
	}
	notes := beads.FrameNote(item.Notes) + "\n" + beads.FrameNote(tracker.updates[0].change.AppendNotes)
	item.Notes, item.FramedNotes = beads.PlainNotes(notes), notes
	item.UpdatedAt = fixedClock{}.Now()
	if listed := orchestrator.FindDocumentOverlaps([]runstate.PendingWrite{agreementWaiting}, []beads.WorkItem{item}, nil); len(listed) != 0 {
		t.Fatalf("after the note, overlaps = %+v, want none", listed)
	}
}

// An item or a document that does not exist, a revision the document does not
// have, or an item whose last change the tracker does not date, is refused and
// nothing is written.
func TestTheAgreementIsRefusedForWhatDoesNotExist(t *testing.T) {
	t.Parallel()

	undated := agreementItem()
	undated.UpdatedAt = time.Time{}
	changed := agreementItem()
	changed.UpdatedAt = agreementStood.Add(time.Second)
	noted := agreementItem()
	noted.FramedNotes = noted.Notes + "\n" + beads.FrameNote("Another decision.")
	noted.UpdatedAt = agreementStood.Add(time.Hour)
	for name, test := range map[string]struct {
		item   beads.WorkItem
		action string
		want   string
	}{
		"no such item": {agreementItem(),
			`{"action":"agree","id":"yoyodyne-ifd.404","document":"agreed-design","revision":0,"item_changed":"2026-08-14T02:30:00-07:00","reason":"agrees"}`,
			"yoyodyne-ifd.404",
		},
		"no such document": {agreementItem(),
			`{"action":"agree","id":"yoyodyne-ifd.9","document":"no-such-design","revision":0,"item_changed":"2026-08-14T02:30:00-07:00","reason":"agrees"}`,
			`there is no document "no-such-design"`,
		},
		"no such revision": {agreementItem(),
			`{"action":"agree","id":"yoyodyne-ifd.9","document":"agreed-design","revision":2,"item_changed":"2026-08-14T02:30:00-07:00","reason":"agrees"}`,
			"there is no revision 2",
		},
		"no such waiting revision": {agreementItem(),
			`{"action":"agree","id":"yoyodyne-ifd.9","document":"agreed-design","revision":"document-8.1","item_changed":"2026-08-14T02:30:00-07:00","reason":"agrees"}`,
			`no revision waiting to be published named "document-8.1"`,
		},
		"an item changed since she read it": {changed,
			agreeAction("document-7.1"),
			"read the item again",
		},
		"an item given a note since she read it": {noted,
			agreeAction("document-7.1"),
			"read the item again",
		},
		"an item the tracker does not date": {undated,
			`{"action":"agree","id":"yoyodyne-ifd.9","document":"agreed-design","revision":1,"item_changed":"2026-08-14T02:30:00-07:00","reason":"agrees"}`,
			"does not say when yoyodyne-ifd.9 last changed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker, reply := agreementConversation(t, domain.RoleArchitect, test.item, test.action)
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
		{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", ItemChanged: "2026-08-14T09:30:00Z", Revision: revisionNumber(0)},
		{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", ItemChanged: "2026-08-14T09:30:00Z", Document: "d"},
		{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", ItemChanged: "2026-08-14T09:30:00Z", Document: "d", Revision: &AgreedRevision{}},
		{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", ItemChanged: "2026-08-14T09:30:00Z", Document: "d", Revision: revisionNumber(-1)},
		{Action: actionAgree, Reason: "r", ItemChanged: "2026-08-14T09:30:00Z", Document: "d", Revision: revisionNumber(0)},
	} {
		if err := action.Validate(); err == nil {
			t.Errorf("%+v was accepted", action)
		}
	}
	if err := (TrackerAction{Action: actionAgree, ID: "yoyodyne-ifd.9", Reason: "r", ItemChanged: "2026-08-14T09:30:00Z", Document: "d", Revision: revisionNumber(0)}).Validate(); err != nil {
		t.Errorf("a complete agreement was refused: %v", err)
	}
}

func revisionNumber(n int) *AgreedRevision { return &AgreedRevision{Number: &n} }

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
