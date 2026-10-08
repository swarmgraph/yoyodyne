package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// stateRootTracker holds the 2026-09-28 shape: the architect's closed design for
// the configurable state root, under the parent its build is admitted beneath.
func stateRootTracker(designStatus string) *fakeTracker {
	return &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.434": {ID: "yoyodyne-ifd.434", Title: "The harness's state lives where the operator says", Status: "open"},
		},
		all: []beads.WorkItem{
			{ID: "yoyodyne-ifd.434", Title: "The harness's state lives where the operator says", Status: "open"},
			{
				ID:     "yoyodyne-ifd.434.2",
				Title:  "The architect designs a configurable state root for the harness",
				Parent: "yoyodyne-ifd.434",
				Status: designStatus,
			},
		},
	}
}

const stateRootBuild = `"title":"Build the configurable state root the architect designed for the harness","description":"Implement the design.","parent":"yoyodyne-ifd.434","goal":"` + theGoal + `"`

// The build of a closed design is admitted citing the design, with what is
// separate written onto the new item, and nothing is proposed to anybody.
func TestTheBuildOfAClosedDesignIsAdmittedNamingTheDesign(t *testing.T) {
	t.Parallel()

	tracker := stateRootTracker("closed")
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the build.",
			`{"action":"create","kind":"feature",`+stateRootBuild+`,"distinct_from":{"id":"yoyodyne-ifd.434.2","separate":"434.2 recorded the design; this builds it."},"reason":"the design is settled"}`)},
		{SessionID: "session-1", FinalText: "It is in the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit the build of the state root.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, created = %#v, want the build admitted", reply.Actions, tracker.created)
	}
	notes := tracker.created[0].Notes
	for _, want := range []string{"Distinct from yoyodyne-ifd.434.2 (closed)", "which the duplicate check matched", "this builds it"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("created notes = %q, want %q", notes, want)
		}
	}
	if !strings.Contains(reply.Actions[0].Summary, "recorded as distinct from yoyodyne-ifd.434.2 (closed)") {
		t.Fatalf("summary = %q, want the distinction reported", reply.Actions[0].Summary)
	}
	if pending := session.Proposals(); len(pending) != 0 {
		t.Fatalf("proposals = %#v, want nothing put to the operator", pending)
	}
}

// The same build without the field is refused, and the refusal names the field
// rather than sending the role to propose.
func TestAClosedMatchRefusalNamesTheDistinctionRatherThanAProposal(t *testing.T) {
	t.Parallel()

	tracker := stateRootTracker("closed")
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the build.",
			`{"action":"create","kind":"feature",`+stateRootBuild+`,"reason":"the design is settled"}`)},
		{SessionID: "session-1", FinalText: "Refused."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit the build of the state root.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the build refused without the distinction", reply.Actions)
	}
	failure := reply.Actions[0].Failure
	if !strings.Contains(failure, `"distinct_from"`) || strings.Contains(failure, "propose") {
		t.Fatalf("failure = %q, want the distinction named and no proposal offered for closed work", failure)
	}
}

// A distinction against open work is refused, and the open-work remedy — act on
// it, or propose — is what the role is told.
func TestADistinctionFromOpenWorkIsRefused(t *testing.T) {
	t.Parallel()

	tracker := stateRootTracker("open")
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the build.",
			`{"action":"create","kind":"feature",`+stateRootBuild+`,"distinct_from":{"id":"yoyodyne-ifd.434.2","separate":"it is different"},"reason":"r"}`)},
		{SessionID: "session-1", FinalText: "Refused."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want a distinction from open work refused", reply.Actions)
	}
	if failure := reply.Actions[0].Failure; !strings.Contains(failure, "rather than closed") || !strings.Contains(failure, "propose") {
		t.Fatalf("failure = %q, want the open item named and the proposal offered", failure)
	}
}

// A distinction that does not say what is separate is refused before anything
// runs, and so is one on an action other than a creation.
func TestADistinctionWithoutASentenceIsRefused(t *testing.T) {
	t.Parallel()

	parent := "yoyodyne-ifd.434"
	create := TrackerAction{
		Action: actionCreate, Title: "t", Description: "d", Goal: theGoal, Parent: &parent, Reason: "r",
		DistinctFrom: &Distinction{ID: "yoyodyne-ifd.434.2"},
	}
	if err := create.Validate(); err == nil || !strings.Contains(err.Error(), `requires "separate"`) {
		t.Fatalf("Validate() error = %v, want the missing sentence refused", err)
	}
	create.DistinctFrom = &Distinction{ID: "yoyodyne-ifd.434.2", Separate: "one line\nand another"}
	if err := create.Validate(); err == nil || !strings.Contains(err.Error(), "cannot span lines") {
		t.Fatalf("Validate() error = %v, want a multi-line sentence refused", err)
	}
	update := TrackerAction{
		Action: actionUpdate, ID: "yoyodyne-ifd.1", Note: "n", Reason: "r",
		DistinctFrom: &Distinction{ID: "yoyodyne-ifd.434.2", Separate: "s"},
	}
	if err := update.Validate(); err == nil || !strings.Contains(err.Error(), `does not take "distinct_from"`) {
		t.Fatalf("Validate() error = %v, want distinct_from refused on an update", err)
	}
}

// The Lead Product Manager takes back a proposal of her own: it leaves the
// operator's list, the record says she withdrew it and why, and nothing is
// recorded as the operator's decline.
func TestTheProductManagerWithdrawsHerOwnProposal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: proposalReply("A suggestion.",
			`{"kind":"feature","title":"Build the configurable state root","description":"Build it.","rationale":"The design is settled.","goal":"Support development in any language."}`)},
		{SessionID: "session-1", FinalText: trackerReply("Taking it back.",
			`{"action":"withdraw","proposal":"1.1","reason":"the admission was mine to make; the guard's fallback proposed it"}`)},
		{SessionID: "session-1", FinalText: "Withdrawn."},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "Anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if pending := session.Proposals(); len(pending) != 1 {
		t.Fatalf("proposals = %#v, want the proposal awaiting the operator first", pending)
	}
	reply, err := session.Send(context.Background(), "Take that back.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Summary, "withdrew proposal 1.1") {
		t.Fatalf("actions = %#v, want the withdrawal applied", reply.Actions)
	}
	if pending := session.Proposals(); len(pending) != 0 {
		t.Fatalf("proposals = %#v, want the withdrawn proposal off the operator's list", pending)
	}
	if len(session.state.PendingProposals) != 0 {
		t.Fatalf("recorded pending proposals = %#v, want the record to stop listing it", session.state.PendingProposals)
	}
	payload := onlyEventPayload(t, root, session, execution.EventProposalWithdrawn)
	if !strings.Contains(payload, "the admission was mine to make") || !strings.Contains(payload, `"by":"product-manager"`) {
		t.Fatalf("withdrawal event = %s, want the reason and who withdrew it", payload)
	}
	if counted := countEvents(t, root, session); counted[execution.EventProposalRejected] != 0 {
		t.Fatalf("events = %v, want no decline recorded for a withdrawal", counted)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want nothing created by a withdrawal", tracker.created)
	}
}

// Only an undecided proposal of this conversation can be withdrawn.
func TestWithdrawingAProposalNobodyHoldsIsRefused(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Taking it back.",
			`{"action":"withdraw","proposal":"959.1","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It was not there."},
	}})
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Take 959.1 back.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "awaiting a decision") {
		t.Fatalf("actions = %#v, want the withdrawal refused", reply.Actions)
	}
	for _, bad := range []TrackerAction{
		{Action: actionWithdraw, Reason: "r"},
		{Action: actionWithdraw, Proposal: "proposal one", Reason: "r"},
		{Action: actionWithdraw, Proposal: "1.1"},
		{Action: actionWithdraw, ID: "yoyodyne-ifd.1", Proposal: "1.1", Reason: "r"},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("Validate(%#v) = nil, want it refused", bad)
		}
	}
}
