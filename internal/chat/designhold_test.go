package chat

// The needs-design label in a conversation (docs/designs/needs-design-gate.md):
// the Lead Product Manager, the development manager, and a program manager in
// its lane put it on; the architect holds no way to take it off.

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func designHoldSession(role domain.AgentRole, lane string) *Session {
	session := &Session{options: Options{Lane: lane}}
	session.state.Role = role
	return session
}

// Each role allowed to set the label may, through its own path — the label
// action, or the labels a creation carries — and every other role is refused.
func TestOnlyTheRolesAllowedToMayPutTheNeedsDesignLabelOn(t *testing.T) {
	t.Parallel()

	add := TrackerAction{Action: actionLabel, ID: "yoyodyne-ifd.1", Add: domain.NeedsDesignLabel, Reason: "how should the retry behave?"}
	parent := "yoyodyne-ifd.1"
	create := TrackerAction{Action: actionCreate, Title: "Build", Description: "d", Goal: recordedGoal, Parent: &parent,
		Labels: []string{domain.NeedsDesignLabel}, Kind: domain.WorkItemKindFeature, Reason: "the behaviour is undecided"}
	for _, setter := range []struct {
		role domain.AgentRole
		lane string
	}{
		{domain.RoleProductManager, ""},
		{domain.RoleDevelopmentManager, ""},
		{domain.RoleProgramManager, testLane},
	} {
		session := designHoldSession(setter.role, setter.lane)
		for _, action := range []TrackerAction{add, create} {
			if err := session.authorize(parsedReply{Actions: []TrackerAction{action}}); err != nil {
				t.Errorf("the %s putting the label on through %q: authorize() = %v, want it permitted", setter.role, action.Action, err)
			}
		}
	}
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDeveloper, domain.RoleReviewer} {
		err := designHoldSession(role, "").authorize(parsedReply{Actions: []TrackerAction{add}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) {
			t.Errorf("the %s putting the label on: authorize() = %v, want it refused", role, err)
		}
	}
}

// The architect holds no way to change the label: no label action of either
// kind, and no write to an item but the note recording that a document agrees
// with it, which takes no label to add or remove. The development manager, who
// removes it for now, may.
func TestTheArchitectHoldsNoWayToRemoveTheNeedsDesignLabel(t *testing.T) {
	t.Parallel()

	remove := TrackerAction{Action: actionLabel, ID: "yoyodyne-ifd.1", Remove: domain.NeedsDesignLabel, Reason: "the design landed"}
	err := designHoldSession(domain.RoleArchitect, "").authorize(parsedReply{Actions: []TrackerAction{remove}})
	var refusal *AuthorityError
	if !errors.As(err, &refusal) {
		t.Fatalf("the architect taking the label off: authorize() = %v, want it refused", err)
	}
	authority, _ := AuthorityFor(domain.RoleArchitect)
	if len(authority.LaneActions) != 0 {
		t.Errorf("the architect's lane actions = %v, want none", authority.LaneActions)
	}
	for _, action := range authority.TrackerActions {
		if action != actionRead && action != actionSurvey && action != actionAmendment && action != actionAgree {
			t.Errorf("the architect may ask for %q, want only reading, deciding amendments, and recording agreement", action)
		}
	}
	agree := TrackerAction{Action: actionAgree, ID: "yoyodyne-ifd.1", Document: "retry-design",
		Revision: &AgreedRevision{ID: "document-1.1"}, ItemChanged: "2026-11-20T11:30:37-08:00", Reason: "they agree"}
	if err := agree.Validate(); err != nil {
		t.Fatalf("an agreement changing no label: validate() = %v, want it accepted", err)
	}
	for name, change := range map[string]func(*TrackerAction){
		"remove": func(a *TrackerAction) { a.Remove = domain.NeedsDesignLabel },
		"add":    func(a *TrackerAction) { a.Add = domain.NeedsDesignLabel },
		"labels": func(a *TrackerAction) { a.Labels = []string{domain.NeedsDesignLabel} },
	} {
		labelled := agree
		change(&labelled)
		if err := labelled.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("an agreement carrying %q: validate() = %v, want it refused", name, err)
		}
	}
	if err := designHoldSession(domain.RoleDevelopmentManager, "").authorize(parsedReply{Actions: []TrackerAction{remove}}); err != nil {
		t.Errorf("the development manager taking the label off: authorize() = %v, want it permitted", err)
	}
}

// The Lead Product Manager's label records the question beside the label, which
// is what the architect reads.
func TestTheLeadProductManagerPutsTheLabelOnWithTheQuestion(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.431.5": {ID: "yoyodyne-ifd.431.5", Title: "Atomic JSON replacement", Status: "open"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Holding it for the architect.",
			`{"action":"label","id":"yoyodyne-ifd.431.5","add":"needs-design","reason":"should a replacement survive a crash mid-write, and how?"}`)},
		{SessionID: "session-1", FinalText: "Held."},
	}})
	options.Tracker = tracker
	reply, err := openTestSession(t, options).Send(context.Background(), "431.5 needs a design")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("actions = %#v, updates = %#v, want the label put on", reply.Actions, tracker.updates)
	}
	change := tracker.updates[0].change
	if strings.Join(change.AddLabels, " ") != domain.NeedsDesignLabel ||
		!strings.Contains(change.AppendNotes, "Labelled needs-design by the Lead Product Manager") ||
		!strings.Contains(change.AppendNotes, "should a replacement survive a crash mid-write") {
		t.Fatalf("change = %#v, want the label, who set it, and the question recorded", change)
	}
}

// Every role that may set the label is told what it does and that the
// development manager takes it off, and the architect is told of no label action.
func TestTheContractsSayWhoSetsAndWhoRemovesTheLabel(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleProgramManager} {
		contract := SystemPrompt(role, Admission{}, nil, "")
		if !strings.Contains(contract, "The development manager takes it off, for now") {
			t.Errorf("the %s contract does not say who removes the needs-design label", role)
		}
	}
	if architect := SystemPrompt(domain.RoleArchitect, Admission{}, nil, ""); strings.Contains(architect, `"action":"label"`) {
		t.Error("the architect's contract offers her a label action")
	}
}
