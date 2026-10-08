package chat

// The program manager's lane, held at the authority table: every tracker write
// it holds is scoped to its lane label, read from the tracker as the action
// runs, and its admissions go through approvals.work_items exactly as the
// product manager's do. See "The lane, and how it is enforced" in
// docs/designs/program-manager.md.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const testLane = "reliability"

// laneSession opens a program manager instance's conversation over a lane, with
// the tracker and the replies the test gives it.
func laneSession(t *testing.T, tracker *fakeTracker, admission Admission, replies ...string) *Session {
	t.Helper()
	results := make([]backendapi.RunResult, 0, len(replies))
	for _, reply := range replies {
		results = append(results, backendapi.RunResult{SessionID: "session-pm-lane", FinalText: reply})
	}
	options := testOptions(t, &fakeBackend{results: results})
	options.Role = domain.RoleProgramManager
	options.Agent = "reliability-pm"
	options.Lane = testLane
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = admission
	return openTestSession(t, options)
}

var automatic = Admission{WorkItems: domain.ApprovalAutomatic}

// An instance's creation carries the lane label in the write that admits it,
// whether or not the reply named it, and the item's notes say whose lane it is
// in. It is an admission like the product manager's: the duplicate-admission
// guard reads the tracker first, as it does for hers.
func TestALaneCreationCarriesTheLaneLabelInTheWriteThatAdmitsIt(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	session := laneSession(t, tracker, automatic,
		trackerReply("Admitting two stall fixes into the lane.",
			`{"action":"create","kind":"feature","title":"The sweep never clears a preserved-branch stoppage","description":"Fix it.","goal":"`+recordedGoal+`","priority":1,"reason":"the same stoppage has stopped runs twice"}`,
			`{"action":"create","title":"Root-cause the checks timing out","description":"Diagnose it.","goal":"`+recordedGoal+`","labels":["bug","reliability"],"reason":"prevention"}`),
		"Both are in the lane.")

	reply, err := session.Send(context.Background(), "admit the stall fixes")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 || !reply.Actions[0].Applied || !reply.Actions[1].Applied {
		t.Fatalf("actions = %#v, want both creations carried out", reply.Actions)
	}
	if len(tracker.created) != 2 {
		t.Fatalf("created = %#v, want two admissions", tracker.created)
	}
	if got := strings.Join(tracker.created[0].Labels, " "); got != testLane {
		t.Errorf("first creation labels = %q, want the lane label applied though the reply named none", got)
	}
	if got := strings.Join(tracker.created[1].Labels, " "); got != "reliability bug" {
		t.Errorf("second creation labels = %q, want the lane label once, with the other label beside it", got)
	}
	if tracker.created[0].Priority == nil || *tracker.created[0].Priority != 1 {
		t.Errorf("first creation priority = %v, want the priority it named", tracker.created[0].Priority)
	}
	for _, created := range tracker.created {
		if !strings.Contains(created.Notes, "Lane: reliability, admitted by the program manager instance reliability-pm.") ||
			!strings.Contains(created.Notes, "Admitted to the backlog in lane reliability by the program manager") {
			t.Errorf("notes = %q, want the instance and the lane recorded", created.Notes)
		}
	}
	if !strings.Contains(reply.Actions[0].Summary, "to the backlog in lane reliability") {
		t.Errorf("summary = %q, want the lane named where the admission is reported", reply.Actions[0].Summary)
	}
	if !slices.Contains(tracker.listed, "") {
		t.Errorf("listed = %v, want the duplicate-admission guard to have read the whole tracker", tracker.listed)
	}
}

// Work already in the lane is writable by its instance through every action the
// lane scopes: the placeholder that refused them all is gone.
func TestALaneItemIsWritableByItsInstance(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.1": {ID: "yoyodyne-ifd.1", Title: "In the lane", Status: "open", Labels: []string{testLane}},
		"yoyodyne-ifd.2": {ID: "yoyodyne-ifd.2", Title: "The lane's parent", Status: "open", Labels: []string{"bug", testLane}},
		"yoyodyne-ifd.9": {ID: "yoyodyne-ifd.9", Title: "Outside the lane", Status: "open"},
	}}
	session := laneSession(t, tracker, automatic,
		trackerReply("Shaping the lane.",
			`{"action":"update","id":"yoyodyne-ifd.1","note":"seen twice now","reason":"record it"}`,
			`{"action":"attribute","id":"yoyodyne-ifd.1","goal":"`+recordedGoal+`","reason":"attribute it"}`,
			`{"action":"label","id":"yoyodyne-ifd.1","add":"bug","reason":"it is a bug"}`,
			`{"action":"reprioritize","id":"yoyodyne-ifd.1","priority":0,"reason":"it stops the line"}`,
			`{"action":"park","id":"yoyodyne-ifd.1","reason":"waiting on the forge"}`,
			`{"action":"unpark","id":"yoyodyne-ifd.1","reason":"the forge answered"}`,
			`{"action":"reparent","id":"yoyodyne-ifd.1","parent":"yoyodyne-ifd.2","reason":"it belongs under the parent"}`,
			`{"action":"link","id":"yoyodyne-ifd.1","depends_on":"yoyodyne-ifd.9","reason":"it waits on outside work"}`,
			`{"action":"unlink","id":"yoyodyne-ifd.1","depends_on":"yoyodyne-ifd.9","reason":"it no longer does"}`),
		"Done.")

	reply, err := session.Send(context.Background(), "tidy the lane")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 9 {
		t.Fatalf("actions = %d, want 9", len(reply.Actions))
	}
	for _, outcome := range reply.Actions {
		if !outcome.Applied {
			t.Errorf("%s on a lane item = %q, want it carried out", outcome.Action.Action, outcome.Failure)
		}
	}
	if len(tracker.updates) != 7 || len(tracker.links) != 1 || len(tracker.unlinks) != 1 {
		t.Fatalf("updates %d, links %v, unlinks %v; want every write made", len(tracker.updates), tracker.links, tracker.unlinks)
	}
}

// Every lane-scoped action on an item not carrying the lane label is refused,
// naming the label, and nothing is written. The reverse link — making an item
// outside the lane wait on one inside it — is among them and says so.
func TestAnActionOnAnItemOutsideTheLaneIsRefusedNamingTheLabel(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.1": {ID: "yoyodyne-ifd.1", Title: "In the lane", Status: "open", Labels: []string{testLane}},
		"yoyodyne-ifd.9": {ID: "yoyodyne-ifd.9", Title: "Outside the lane", Status: "open", Labels: []string{"bug"}},
	}}
	session := laneSession(t, tracker, automatic,
		trackerReply("Reaching outside the lane.",
			`{"action":"update","id":"yoyodyne-ifd.9","note":"n","reason":"r"}`,
			`{"action":"attribute","id":"yoyodyne-ifd.9","goal":"`+recordedGoal+`","reason":"r"}`,
			`{"action":"label","id":"yoyodyne-ifd.9","add":"reliability","reason":"pull it into the lane"}`,
			`{"action":"reprioritize","id":"yoyodyne-ifd.9","priority":0,"reason":"r"}`,
			`{"action":"park","id":"yoyodyne-ifd.9","reason":"r"}`,
			`{"action":"unpark","id":"yoyodyne-ifd.9","reason":"r"}`,
			`{"action":"reparent","id":"yoyodyne-ifd.9","parent":"yoyodyne-ifd.1","reason":"r"}`,
			`{"action":"unlink","id":"yoyodyne-ifd.9","depends_on":"yoyodyne-ifd.1","reason":"r"}`,
			`{"action":"link","id":"yoyodyne-ifd.9","depends_on":"yoyodyne-ifd.1","reason":"make it wait on the lane"}`),
		"All refused.")

	reply, err := session.Send(context.Background(), "reach outside")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 9 {
		t.Fatalf("actions = %d, want 9", len(reply.Actions))
	}
	for _, outcome := range reply.Actions {
		if outcome.Applied || !strings.Contains(outcome.Failure, `yoyodyne-ifd.9 does not carry the lane label "reliability"`) {
			t.Errorf("%s outside the lane = applied %v, %q; want it refused naming the label", outcome.Action.Action, outcome.Applied, outcome.Failure)
		}
	}
	if link := reply.Actions[8]; !strings.Contains(link.Failure, "an item outside the lane may not be made to wait on one") {
		t.Errorf("the reverse link's refusal = %q, want it to say which direction is refused", link.Failure)
	}
	if len(tracker.updates) != 0 || len(tracker.links) != 0 || len(tracker.unlinks) != 0 {
		t.Fatalf("updates %v, links %v, unlinks %v; want nothing written outside the lane", tracker.updates, tracker.links, tracker.unlinks)
	}
}

// A listing that has moved does not widen the lane: the survey shows the item
// carrying the label, the tracker changes it before the act, and the act is
// judged against the item as the tracker holds it then.
func TestAListingThatHasMovedDoesNotWidenTheLane(t *testing.T) {
	t.Parallel()

	surveyed := beads.WorkItem{ID: "yoyodyne-ifd.5", Title: "Was in the lane", Status: "open", Priority: 2, IssueType: "task", Labels: []string{testLane}}
	now := surveyed
	now.Labels = nil
	tracker := &fakeTracker{
		open:  []beads.WorkItem{surveyed},
		items: map[string]beads.WorkItem{"yoyodyne-ifd.5": now},
	}
	session := laneSession(t, tracker, automatic,
		trackerReply("Looking first.", `{"action":"survey"}`),
		trackerReply("It is in the lane, so updating it.", `{"action":"update","id":"yoyodyne-ifd.5","note":"n","reason":"r"}`),
		"Refused: it left the lane.")

	reply, err := session.Send(context.Background(), "update ifd.5")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 || !reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Detail, "label reliability") {
		t.Fatalf("survey = %#v, want it to show the item in the lane", reply.Actions)
	}
	if reply.Actions[1].Applied || !strings.Contains(reply.Actions[1].Failure, `does not carry the lane label "reliability" as the tracker holds it now`) {
		t.Fatalf("update after the label went = %#v, want it refused", reply.Actions[1])
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v, want nothing written", tracker.updates)
	}
}

// An item the tracker will not describe is outside the lane, rather than
// attempted as the product manager's actions are: attempting it is the
// widening the lane exists to refuse.
func TestAnItemTheTrackerWillNotDescribeIsOutsideTheLane(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{}}
	session := laneSession(t, tracker, automatic,
		trackerReply("Updating it.", `{"action":"update","id":"yoyodyne-ifd.77","note":"n","reason":"r"}`),
		"Refused.")
	reply, err := session.Send(context.Background(), "update ifd.77")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "treated as outside the lane") {
		t.Fatalf("actions = %#v, want the unreadable item refused", reply.Actions)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v, want nothing written", tracker.updates)
	}
}

// The lane label is never removed by its owner: the block is refused whole,
// whatever the item carries. Another label on a lane item comes off as usual.
func TestTheLaneLabelIsNeverRemovedByItsOwner(t *testing.T) {
	t.Parallel()

	session := &Session{options: Options{Lane: testLane}}
	session.state.Role = domain.RoleProgramManager
	remove := TrackerAction{Action: actionLabel, ID: "yoyodyne-ifd.1", Remove: testLane, Reason: "done with it"}
	err := session.authorize(parsedReply{Actions: []TrackerAction{remove}})
	var refusal *AuthorityError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), `the lane label "reliability" removed`) {
		t.Fatalf("authorize() of removing the lane label = %v, want it refused", err)
	}
	other := TrackerAction{Action: actionLabel, ID: "yoyodyne-ifd.1", Remove: "bug", Reason: "not a bug"}
	if err := session.authorize(parsedReply{Actions: []TrackerAction{other}}); err != nil {
		t.Fatalf("authorize() of removing another label = %v, want it permitted", err)
	}

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.1": {ID: "yoyodyne-ifd.1", Title: "In the lane", Status: "open", Labels: []string{testLane, "bug"}},
	}}
	reply, err := laneSession(t, tracker, automatic,
		trackerReply("Taking the bug label off.", `{"action":"label","id":"yoyodyne-ifd.1","remove":"bug","reason":"not a bug"}`),
		"Done.").Send(context.Background(), "untag it")
	if err != nil || len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("Send() = %#v, %v; want the other label removed", reply.Actions, err)
	}
	if got := strings.Join(tracker.items["yoyodyne-ifd.1"].Labels, " "); got != testLane {
		t.Fatalf("labels = %q, want the lane label left on", got)
	}
}

// A reparent whose new parent lacks the label is refused, and so is a creation
// under such a parent: either would put a lane item under work outside the lane.
func TestAParentOutsideTheLaneIsRefused(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.1": {ID: "yoyodyne-ifd.1", Title: "In the lane", Status: "open", Labels: []string{testLane}},
		"yoyodyne-ifd.9": {ID: "yoyodyne-ifd.9", Title: "Outside the lane", Status: "open"},
	}}
	session := laneSession(t, tracker, automatic,
		trackerReply("Moving it.",
			`{"action":"reparent","id":"yoyodyne-ifd.1","parent":"yoyodyne-ifd.9","reason":"r"}`,
			`{"action":"create","kind":"feature","title":"A child of outside work","description":"d","goal":"`+recordedGoal+`","parent":"yoyodyne-ifd.9","reason":"r"}`),
		"Refused.")
	reply, err := session.Send(context.Background(), "move it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	for _, outcome := range reply.Actions {
		if outcome.Applied || !strings.Contains(outcome.Failure, `the parent it names is outside the lane: yoyodyne-ifd.9 does not carry the lane label "reliability"`) {
			t.Errorf("%s under a parent outside the lane = %#v, want it refused", outcome.Action.Action, outcome)
		}
	}
	if len(tracker.updates) != 0 || len(tracker.created) != 0 {
		t.Fatalf("updates %v, created %v; want nothing written", tracker.updates, tracker.created)
	}
}

// Under approvals.work_items human the instance's creation is put to the
// operator as a proposal naming the lane, and nothing is created until they
// approve it. The approved item is created in the lane, recording the instance.
func TestUnderPerItemApprovalALaneCreationIsPutToTheOperator(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	session := laneSession(t, tracker, Admission{},
		trackerReply("Admitting the stall fix.",
			`{"action":"create","kind":"feature","title":"The sweep never clears a preserved-branch stoppage","description":"Fix it.","goal":"`+recordedGoal+`","priority":1,"reason":"it stopped runs twice"}`),
		"It is with the operator.")

	reply, err := session.Send(context.Background(), "admit the stall fix")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want nothing admitted without the operator", tracker.created)
	}
	if len(reply.Actions) != 1 || !strings.Contains(reply.Actions[0].Summary, "put to the operator as proposal 1.1 in lane reliability") ||
		!strings.Contains(reply.Actions[0].Summary, perItemApprovalReason) ||
		!strings.Contains(reply.Actions[0].Summary, "a proposal does not carry priority") {
		t.Fatalf("actions = %#v, want the creation reported as put to the operator", reply.Actions)
	}
	if len(reply.Proposals) != 1 || reply.Proposals[0].Lane != testLane || !strings.Contains(reply.Proposals[0].Render(), "lane: reliability") {
		t.Fatalf("proposals = %#v, want one naming the lane", reply.Proposals)
	}
	if pending := session.Proposals(); len(pending) != 1 || pending[0].ID != "1.1" {
		t.Fatalf("pending = %#v, want the proposal awaiting a decision", pending)
	}

	created, err := session.Approve(context.Background(), "1.1")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if created.WorkItemID == "" || len(tracker.created) != 1 {
		t.Fatalf("Approve() = %#v, created %#v", created, tracker.created)
	}
	item := tracker.created[0]
	if strings.Join(item.Labels, " ") != testLane {
		t.Errorf("approved item labels = %v, want it created in the lane", item.Labels)
	}
	for _, required := range []string{"Proposed by the program manager", "approved by the operator", "Lane: reliability, admitted by the program manager instance reliability-pm."} {
		if !strings.Contains(item.Notes, required) {
			t.Errorf("approved item notes = %q, want %q", item.Notes, required)
		}
	}
}

// Under automatic, a lane creation naming a goal whose document nobody approved
// is not admitted: it is put to the operator, as the product manager's would be.
func TestUnderAutomaticAnUnapprovedGoalIsPutToTheOperator(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	results := []backendapi.RunResult{
		{SessionID: "s", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"t","description":"d","goal":"`+recordedGoal+`","reason":"r"}`)},
		{SessionID: "s", FinalText: "With the operator."},
	}
	options := testOptions(t, &fakeBackend{results: results})
	options.Role = domain.RoleProgramManager
	options.Agent = "reliability-pm"
	options.Lane = testLane
	options.Tracker = tracker
	options.Goals = unapprovedGoals(recordedGoal)
	options.Admission = automatic
	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 || len(reply.Proposals) != 1 || reply.Proposals[0].Lane != testLane {
		t.Fatalf("created %#v, proposals %#v; want it put to the operator in the lane", tracker.created, reply.Proposals)
	}
	if reply.Proposals[0].Asking == "" {
		t.Errorf("the proposal does not say why the operator is being asked")
	}
}

// An instance configured with no lane has nothing inside one, so every
// lane-scoped write is refused whole. Close and retire are refused everywhere,
// on an item in the lane as much as outside it, because the role holds neither.
func TestWithoutALaneNothingIsWritableAndCloseAndRetireNeverAre(t *testing.T) {
	t.Parallel()

	laneless := &Session{}
	laneless.state.Role = domain.RoleProgramManager
	authority, _ := AuthorityFor(domain.RoleProgramManager)
	for _, action := range authority.LaneActions {
		err := laneless.authorize(parsedReply{Actions: []TrackerAction{{Action: action, ID: "yoyodyne-ifd.1", Reason: "why"}}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "configured with no lane") {
			t.Errorf("authorize() of %q with no lane = %v, want it refused", action, err)
		}
	}

	inLane := &Session{options: Options{Lane: testLane}}
	inLane.state.Role = domain.RoleProgramManager
	for _, action := range []string{actionClose, actionRetire} {
		err := inLane.authorize(parsedReply{Actions: []TrackerAction{{Action: action, ID: "yoyodyne-ifd.1", Reason: "why"}}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) {
			t.Errorf("authorize() of %q = %v, want it refused", action, err)
		}
	}

	// And the contract states the lane's rules and the admission it goes through.
	contract := SystemPrompt(domain.RoleProgramManager, Admission{}, nil, "")
	for _, required := range []string{
		"puts your lane label on it in the same write",
		"at the moment the action runs",
		"You never remove your own lane label",
		"Making an item outside your lane wait on one of yours is refused",
		"a proposal naming your lane",
	} {
		if !strings.Contains(contract, required) {
			t.Errorf("the program manager's contract does not state %q", required)
		}
	}
}
