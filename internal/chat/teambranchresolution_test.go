package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeTeamBranchResolutions is the record of bringing the target branch up to
// date with the team's branch, as the development manager's conversation sees
// it: the decisions it was asked to record, and what it answers with.
type fakeTeamBranchResolutions struct {
	records   []runstate.TeamBranchUpdate
	decisions []runstate.TeamBranchResolutionDecision
	refuse    error
}

func (f *fakeTeamBranchResolutions) DecideResolution(_ context.Context, decision runstate.TeamBranchResolutionDecision) (runstate.TeamBranchUpdate, error) {
	if f.refuse != nil {
		return runstate.TeamBranchUpdate{}, f.refuse
	}
	f.decisions = append(f.decisions, decision)
	for _, record := range f.records {
		if record.TargetBranch == decision.TargetBranch {
			return record, nil
		}
	}
	return runstate.TeamBranchUpdate{}, errors.New("no update of " + decision.TargetBranch + " is waiting on a conflict, so there is nothing to resolve")
}

func (f *fakeTeamBranchResolutions) All() ([]runstate.TeamBranchUpdate, error) {
	return f.records, nil
}

func waitingOnAConflict(target string, paths ...string) runstate.TeamBranchUpdate {
	return runstate.TeamBranchUpdate{
		TargetBranch: target, UpstreamBranch: "main",
		Pending: &runstate.TeamBranchUpdateAttempt{Conflict: &runstate.TeamBranchUpdateConflict{Paths: paths}},
	}
}

const resolveConflictAction = `{"action":"resolve-conflict","target":"staging","id":"yoyodyne-ifd.77","reason":"it changed the second line of app.txt on staging, so it knows both sides"}`

func resolutionItem() *fakeTracker {
	return &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.77": {ID: "yoyodyne-ifd.77", Title: "Changes the second line", Status: "open"},
	}}
}

func resolutionReply(t *testing.T, tracker Tracker, records TeamBranchResolutions, action string) Reply {
	t.Helper()

	options := triageOptions(t, tracker, nil, trackerReply("Deciding it.", action))
	options.Reports = &fakeReports{}
	options.TeamBranchResolutions = records
	return triageSend(t, options)
}

// Her decision names an item, and the record is told who decided, in which
// conversation and turn, and when; the item's notes say what she decided and why.
func TestTheDevelopmentManagerDecidesWhichItemResolvesAConflict(t *testing.T) {
	t.Parallel()

	records := &fakeTeamBranchResolutions{records: []runstate.TeamBranchUpdate{waitingOnAConflict("staging", "app.txt", "lib/x.go")}}
	tracker := resolutionItem()
	reply := resolutionReply(t, tracker, records, resolveConflictAction)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the decision recorded", reply.Actions)
	}
	if len(records.decisions) != 1 {
		t.Fatalf("decisions = %#v, want one", records.decisions)
	}
	got := records.decisions[0]
	if got.TargetBranch != "staging" || got.WorkItemID != "yoyodyne-ifd.77" || got.WorkItemTitle != "Changes the second line" ||
		got.DecidedBy != "development-manager" || got.Conversation == "" || got.At.IsZero() || got.At.After(time.Now().Add(time.Minute)) ||
		!strings.Contains(got.Reason, "knows both sides") {
		t.Fatalf("decision = %#v, want the item and its title, her role, the conversation and the time", got)
	}
	if reply.Actions[0].WorkItemID != "yoyodyne-ifd.77" || reply.Actions[0].WorkItemTitle != "Changes the second line" {
		t.Fatalf("outcome = %#v, want the item named", reply.Actions[0])
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v, want one note on the item", tracker.updates)
	}
	notes := tracker.updates[0].change.AppendNotes
	for _, want := range []string{"a run of this item resolves the conflict merging main into staging in app.txt, lib/x.go", "development manager", "Reason: it changed the second line"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the item's note is missing %q:\n%s", want, notes)
		}
	}
}

// Where one target branch is waiting, "target" may be left out and the record
// names it.
func TestAConflictDecisionWithNoTargetTakesTheBranchWaiting(t *testing.T) {
	t.Parallel()

	records := &fakeTeamBranchResolutions{records: []runstate.TeamBranchUpdate{waitingOnAConflict("staging", "app.txt")}}
	reply := resolutionReply(t, resolutionItem(), records, `{"action":"resolve-conflict","id":"yoyodyne-ifd.77","reason":"it knows the file"}`)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(records.decisions) != 1 || records.decisions[0].TargetBranch != "staging" {
		t.Fatalf("actions = %#v, decisions = %#v, want the decision on staging", reply.Actions, records.decisions)
	}
}

// Two branches waiting is a choice she has to make.
func TestAConflictDecisionWithNoTargetIsRefusedWhereTwoAreWaiting(t *testing.T) {
	t.Parallel()

	records := &fakeTeamBranchResolutions{records: []runstate.TeamBranchUpdate{waitingOnAConflict("staging", "a"), waitingOnAConflict("release", "b")}}
	reply := resolutionReply(t, resolutionItem(), records, `{"action":"resolve-conflict","id":"yoyodyne-ifd.77","reason":"it knows the file"}`)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "name the one this decision is about") {
		t.Fatalf("actions = %#v, want a refusal naming \"target\"", reply.Actions)
	}
	if len(records.decisions) != 0 {
		t.Fatalf("decisions = %#v, want none", records.decisions)
	}
}

// What the record refuses is this action's refusal, in the record's words, and
// nothing is written on the item.
func TestAConflictDecisionPassesTheRecordsRefusalThrough(t *testing.T) {
	t.Parallel()

	records := &fakeTeamBranchResolutions{refuse: errors.New("all 3 rounds of resolving this conflict have been used; the update waits for a branch to move")}
	tracker := resolutionItem()
	reply := resolutionReply(t, tracker, records, resolveConflictAction)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "all 3 rounds of resolving this conflict have been used") {
		t.Fatalf("actions = %#v, want the record's refusal", reply.Actions)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v, a refused decision wrote a note", tracker.updates)
	}
}

// An item the tracker does not hold cannot resolve anything.
func TestAConflictDecisionIsRefusedForAnItemTheTrackerDoesNotHold(t *testing.T) {
	t.Parallel()

	records := &fakeTeamBranchResolutions{records: []runstate.TeamBranchUpdate{waitingOnAConflict("staging", "app.txt")}}
	reply := resolutionReply(t, &fakeTracker{items: map[string]beads.WorkItem{}}, records, resolveConflictAction)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "does not hold yoyodyne-ifd.77") {
		t.Fatalf("actions = %#v, want a refusal saying the tracker does not hold the item", reply.Actions)
	}
	if len(records.decisions) != 0 {
		t.Fatalf("decisions = %#v, want none", records.decisions)
	}
}

// A project with no team's branch has no conflict to resolve.
func TestAConflictDecisionIsRefusedWhereTheProjectNamesNoTeamsBranch(t *testing.T) {
	t.Parallel()

	reply := resolutionReply(t, resolutionItem(), nil, resolveConflictAction)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "names no team's branch") {
		t.Fatalf("actions = %#v, want a refusal saying the project names no team's branch", reply.Actions)
	}
}

// Only a role that decides about stopped work may record it.
func TestAConflictDecisionIsHeldToTheTriageCapability(t *testing.T) {
	t.Parallel()

	action := TrackerAction{Action: actionResolveConflict, ID: "yoyodyne-ifd.77", Reason: "it knows the file"}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect, domain.RoleProgramManager} {
		authority, known := AuthorityFor(role)
		if !known || authority.MayAct(actionResolveConflict) {
			t.Errorf("%s may ask for %q", role, actionResolveConflict)
		}
		session := &Session{}
		session.state.Role = role
		var refusal *AuthorityError
		if err := session.authorize(parsedReply{Actions: []TrackerAction{action}}); !errors.As(err, &refusal) {
			t.Errorf("authorize() for %s = %v, want an authority refusal", role, err)
		}
	}
	authority, _ := AuthorityFor(domain.RoleDevelopmentManager)
	if !authority.MayAct(actionResolveConflict) || !slices.Contains(authority.TrackerActions, actionResolveConflict) {
		t.Errorf("the development manager's actions = %v, want %q among them", authority.TrackerActions, actionResolveConflict)
	}
}

// The action takes a reason, an item, and at most a target branch.
func TestAConflictDecisionTakesNoOtherArguments(t *testing.T) {
	t.Parallel()

	for name, action := range map[string]TrackerAction{
		"a run":               {Action: actionResolveConflict, ID: "yoyodyne-ifd.77", Run: "run-1", Reason: "why"},
		"a decision":          {Action: actionResolveConflict, ID: "yoyodyne-ifd.77", Decision: "repair", Reason: "why"},
		"no item":             {Action: actionResolveConflict, Reason: "why"},
		"no reason":           {Action: actionResolveConflict, ID: "yoyodyne-ifd.77"},
		"a target with space": {Action: actionResolveConflict, ID: "yoyodyne-ifd.77", Target: "my branch", Reason: "why"},
	} {
		if err := action.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want it refused", name)
		}
	}
	if err := (TrackerAction{Action: actionTriage, ID: "yoyodyne-ifd.77", Target: "staging", Run: "run-1", Decision: "wait", Reason: "why"}).Validate(); err == nil {
		t.Error("triage took a target")
	}
	if err := (TrackerAction{Action: actionResolveConflict, ID: "yoyodyne-ifd.77", Target: "staging", Reason: "why"}).Validate(); err != nil {
		t.Errorf("Validate() = %v, want the action accepted", err)
	}
}
