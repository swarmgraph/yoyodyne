package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// theGoal is the statement every admission in this file names, so the goal gate
// is satisfied and what these tests exercise is the duplicate guard behind it.
const theGoal = "Run development nearly autonomously."

// filedReport is one report in the pile, as a role would have filed it.
func filedReport(id, message string) report.Report {
	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		RunID:         "run-1",
		WorkItemID:    "yoyodyne-ifd.229",
		ProductID:     "yoyodyne",
		RepositoryID:  "repo",
		Severity:      report.SeverityWarning,
		Message:       message,
		RecordedAt:    fixedClock{}.Now(),
	}
}

// The 274/229 shape, replayed through the door it came through: the product
// manager admitting work from a developer report it has already admitted work
// from, after that work had landed.
func TestWorkIsNotAdmittedTwiceFromOneReport(t *testing.T) {
	t.Parallel()

	const filed = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	tracker := &fakeTracker{all: []beads.WorkItem{{
		ID:     "yoyodyne-ifd.229",
		Title:  "The refreshed export cannot become a run's own change: the skip-worktree guard is enforced, not assumed",
		Status: "closed",
		Notes:  "Admitted to the backlog by the product manager.\n\nAdmitted from report " + filed + ", filed at \"warning\" by the developer.",
	}}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the export guard.",
			`{"action":"create","kind":"feature","title":"The tracker export cannot be smuggled into a run's committed change","description":"Refuse the export in a run's diff.","goal":"`+
				theGoal+`","report":"`+filed+`","reason":"the developer reported it"}`)},
		{SessionID: "session-1", FinalText: "It is already done."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{appended: []report.Report{filedReport(filed, "the skip-worktree bit can be flipped")}}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "The developer reported the export can be smuggled in.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v, want nothing admitted a second time from one report", tracker.created)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation refused", reply.Actions)
	}
	// What is caught reaches whoever admits, naming the item rather than only the
	// fact: acting on the existing item needs its identifier.
	for _, want := range []string{"yoyodyne-ifd.229", "closed", filed} {
		if !strings.Contains(reply.Actions[0].Failure, want) {
			t.Fatalf("failure = %q, want it to name %q", reply.Actions[0].Failure, want)
		}
	}
	// The duplicate is of work that has already landed, so the remedy is not
	// "fold it in": a run made for it could not contain anything.
	if !strings.Contains(reply.Actions[0].Failure, "already done") {
		t.Fatalf("failure = %q, want it to say the matched work is done", reply.Actions[0].Failure)
	}
}

// The same guard, the other way round: a report that has produced no work yet
// admits work, and the item records which report it came from, which is what the
// next admission citing it is checked against.
func TestWorkAdmittedFromAReportRecordsTheReport(t *testing.T) {
	t.Parallel()

	const filed = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"The export is refused in a run's diff","description":"Refuse it.","goal":"`+
				theGoal+`","report":"`+filed+`","reason":"the developer reported it"}`)},
		{SessionID: "session-1", FinalText: "It is in the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{appended: []report.Report{filedReport(filed, "the skip-worktree bit can be flipped")}}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit what the developer reported.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created work items = %#v, want one admission", tracker.created)
	}
	if !strings.Contains(tracker.created[0].Notes, "Admitted from report "+filed) {
		t.Fatalf("created notes = %q, want the report cited on the item", tracker.created[0].Notes)
	}
	if !strings.Contains(reply.Actions[0].Summary, "admitted from report "+filed) {
		t.Fatalf("summary = %q, want the citation reported", reply.Actions[0].Summary)
	}
}

// One record can genuinely prompt more than one piece of work — an operator's
// directive routinely does, and the contract has always said to name it on the
// item that answers it. So the refusal for a source names the way through as well
// as the match, rather than walling off something the role is told to do.
func TestARefusalForASourceSaysHowASecondPieceOfWorkIsAdmitted(t *testing.T) {
	t.Parallel()

	const filed = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	tracker := &fakeTracker{all: []beads.WorkItem{{
		ID:     "yoyodyne-ifd.229",
		Title:  "The refreshed export cannot become a run's own change",
		Status: "open",
		Notes:  "Admitted from report " + filed + ".",
	}}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the second half.",
			`{"action":"create","kind":"feature","title":"The export hold is re-checked on every attempt","description":"d","goal":"`+
				theGoal+`","report":"`+filed+`","reason":"the same report asked for both"}`)},
		{SessionID: "session-1", FinalText: "I will admit it without the citation."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{appended: []report.Report{filedReport(filed, "the skip-worktree bit can be flipped")}}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit the rest of what that report asked for.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, created = %#v, want the creation refused", reply.Actions, tracker.created)
	}
	if !strings.Contains(reply.Actions[0].Failure, "without citing "+filed) {
		t.Fatalf("failure = %q, want the way through named", reply.Actions[0].Failure)
	}
}

// A citation nothing checked is a guard that has quietly stopped working, so a
// creation naming a report nobody filed admits nothing.
func TestAdmittingFromAReportNobodyFiledCreatesNothing(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"Something a report asked for","description":"d","goal":"`+
				theGoal+`","report":"report-00000000000000000000000000000000","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It was refused."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v, want nothing created against a report nobody filed", tracker.created)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied ||
		!strings.Contains(reply.Actions[0].Failure, "no report in the pile is") {
		t.Fatalf("actions = %#v, want the creation refused for the citation", reply.Actions)
	}
}

// The 241.2/241.4 shape, replayed through the door it came through: the
// development manager decomposing one parent a second time into a child the
// parent already has.
func TestOneParentIsNotDecomposedTwiceIntoTheSameChild(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.241": {ID: "yoyodyne-ifd.241", Title: "Bundle-improvement notices speak unprompted", Status: "open"},
		},
		all: []beads.WorkItem{
			{ID: "yoyodyne-ifd.241", Title: "Bundle-improvement notices speak unprompted, and each new one DMs the operator once", Status: "blocked"},
			{
				ID:     "yoyodyne-ifd.241.2",
				Title:  "Each newly-available bundle improvement DMs the operator once, per the architect's ruling",
				Parent: "yoyodyne-ifd.241",
				Status: "open",
			},
			{
				ID:     "yoyodyne-ifd.209.14",
				Title:  "The reviewer's evidence carries the invariants the work item was given",
				Parent: "yoyodyne-ifd.209",
				Status: "closed",
			},
		},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Breaking it down.",
			`{"action":"create","kind":"feature","parent":"yoyodyne-ifd.241","title":"Each newly-available improvement DMs the operator once, deduplicated, per the ruling",`+
				`"description":"d","goal":"`+theGoal+`","reason":"decomposing the parent"}`)},
		{SessionID: "session-1", FinalText: "It is already carved out."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Decompose 241.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v, want the second decomposition refused", tracker.created)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation refused", reply.Actions)
	}
	for _, want := range []string{"yoyodyne-ifd.241.2", "yoyodyne-ifd.241"} {
		if !strings.Contains(reply.Actions[0].Failure, want) {
			t.Fatalf("failure = %q, want it to name %q", reply.Actions[0].Failure, want)
		}
	}
	// The matched child is open, so the remedy is the item rather than a claim
	// that the work is done.
	if !strings.Contains(reply.Actions[0].Failure, "Act on that item") {
		t.Fatalf("failure = %q, want the open-work remedy", reply.Actions[0].Failure)
	}
	// The refusal is about the work rather than about the role: a decomposition
	// refused as an admission would name an authority this role does not have.
	if !strings.Contains(reply.Actions[0].Failure, "carve out of yoyodyne-ifd.241") {
		t.Fatalf("failure = %q, want the act named as decomposition", reply.Actions[0].Failure)
	}
}

// The 2026-09-18 shape: the listing the duplicate check needs timed out under an
// admission, and the admission went in unchecked. The guard cost two runs to
// earn, and a listing that still fails once the recovery rule has retried it is
// not a tracker that was briefly unavailable — so the creation is refused with
// the reason rather than written on the strength of a guard that never ran. The
// role can ask again once the tracker answers; a duplicate cannot be un-admitted.
func TestAnAdmissionIsRefusedWhenTheDuplicateCheckCouldNotRun(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{listErr: errors.New("bd list failed with status timed_out and exit code -1: ")}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"Something worth doing","description":"d","goal":"`+theGoal+`","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "The tracker would not answer, so nothing was admitted."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v, want nothing admitted past a check that did not run", tracker.created)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation refused", reply.Actions)
	}
	// The listing was retried under the recovery rule before the refusal, and the
	// refusal says so, with the reason the tracker gave.
	if len(tracker.listed) < 2 {
		t.Fatalf("listings = %#v, want the listing asked for again before the creation was refused", tracker.listed)
	}
	for _, want := range []string{"nothing checked whether this is already in it", "did not outlast it", "timed_out"} {
		if !strings.Contains(reply.Actions[0].Failure, want) {
			t.Fatalf("failure = %q, want it to say %q", reply.Actions[0].Failure, want)
		}
	}
}

// The other door, the same guard. A proposal the goals would have admitted
// unasked is admitted on the strength of the duplicate check as much as of the
// goal, so a listing the recovery rule could not get an answer from puts the
// proposal to the operator with the gap named rather than into the queue with
// nobody looking.
func TestAProposalIsPutToTheOperatorWhenTheDuplicateCheckCouldNotRun(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{listErr: errors.New("bd list failed with status timed_out and exit code -1: ")}
	proposal := `{"items":[{"kind":"feature","title":"Something worth doing","description":"d","rationale":"r","goal":"` + theGoal + `"}]}`
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n```yoyodyne-proposal\n" + proposal + "\n```"},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is worth doing?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 0 || len(tracker.created) != 0 {
		t.Fatalf("admitted = %#v, created = %#v, want the proposal put to the operator instead", reply.Admitted, tracker.created)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v, want one awaiting a decision", reply.Proposals)
	}
	for _, want := range []string{"nothing checked whether this is already in it", "timed_out"} {
		if !strings.Contains(reply.Proposals[0].Asking, want) {
			t.Fatalf("asking = %q, want the unrun check named with its reason", reply.Proposals[0].Asking)
		}
	}
}

// The guard reads the whole tracker rather than the open queue, because the
// duplicate that costs a run is a duplicate of work that has already landed.
func TestTheDuplicateCheckReadsClosedWorkToo(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"Something worth doing","description":"d","goal":"`+theGoal+`","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "Admit it."); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	listedEverything := false
	for _, status := range tracker.listed {
		if status == "" {
			listedEverything = true
		}
	}
	if !listedEverything {
		t.Fatalf("listings = %#v, want one that names no status so closed work is read", tracker.listed)
	}
}

// A proposal is never refused for a resemblance. What happens instead is that
// the harness does not admit it on a goal's authority: it goes to the operator
// with the item it looks like named on it.
func TestAProposalThatLooksLikeAdmittedWorkIsPutToTheOperator(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.241": {ID: "yoyodyne-ifd.241", Title: "Bundle-improvement notices", Status: "open"},
		},
		all: []beads.WorkItem{{
			ID:     "yoyodyne-ifd.241.2",
			Title:  "Each newly-available bundle improvement DMs the operator once, per the architect's ruling",
			Parent: "yoyodyne-ifd.241",
			Status: "open",
		}},
	}
	proposal := `{"items":[{"kind":"feature","title":"Each newly-available improvement DMs the operator once, deduplicated, per the ruling",` +
		`"description":"d","rationale":"the notices are still silent","goal":"` + theGoal + `","parent":"yoyodyne-ifd.241"}]}`
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n```yoyodyne-proposal\n" + proposal + "\n```"},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	// The project admits work that traces to an approved goal, which is the case
	// where a duplicate would otherwise reach the queue with nobody looking.
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is left on the notices?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 0 || len(tracker.created) != 0 {
		t.Fatalf("admitted = %#v, created = %#v, want the proposal put to the operator instead", reply.Admitted, tracker.created)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v, want one awaiting a decision", reply.Proposals)
	}
	if !strings.Contains(reply.Proposals[0].Asking, "yoyodyne-ifd.241.2") {
		t.Fatalf("asking = %q, want the item it looks like named", reply.Proposals[0].Asking)
	}
	// The operator reads the rendered card rather than the field, so the match has
	// to survive into it.
	if !strings.Contains(reply.Proposals[0].Render(), "yoyodyne-ifd.241.2") {
		t.Fatalf("rendered proposal = %q, want the match in front of the operator", reply.Proposals[0].Render())
	}
}

// A proposal that looks like nothing is admitted exactly as it was before, so
// the guard costs the ordinary case nothing.
func TestAProposalThatLooksLikeNothingIsStillAdmitted(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	proposal := `{"items":[{"kind":"feature","title":"Stall detection runs without Slack","description":"d",` +
		`"rationale":"the watchdog is wired to a surface","goal":"` + theGoal + `"}]}`
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n```yoyodyne-proposal\n" + proposal + "\n```"},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What about the watchdog?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 1 || len(tracker.created) != 1 {
		t.Fatalf("admitted = %#v, created = %#v, want the ordinary admission unchanged", reply.Admitted, tracker.created)
	}
}

// landingTracker holds what it creates, as the store does, so a later listing in
// the same turn reads it back — which the plain fake does not. Its first few
// creations can be made to fail the way a bd killed at its timeout does, either
// after the store took the write or before it did.
type landingTracker struct {
	*fakeTracker
	// timeouts is how many creations fail as a killed bd, and landed is whether
	// each of those reached the store before it was killed.
	timeouts int
	landed   bool
	creates  int
	// edgeOnly stores the parent as a parent-child edge and leaves the field
	// empty, as a listing that states parentage only as the edge would.
	edgeOnly bool
}

func (l *landingTracker) Create(ctx context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	l.creates++
	failing := l.creates <= l.timeouts
	if failing && !l.landed {
		return beads.WorkItem{}, errors.New("bd create failed with status timed_out and exit code -1:")
	}
	if _, err := l.fakeTracker.Create(ctx, item); err != nil {
		return beads.WorkItem{}, err
	}
	stored := beads.WorkItem{
		ID:     fmt.Sprintf("yoyodyne-ifd.428.%d", 20+len(l.created)),
		Title:  item.Title,
		Parent: item.Parent,
		Notes:  item.Notes,
		Status: "open",
	}
	if l.edgeOnly && stored.Parent != "" {
		stored.Dependencies = []beads.Dependency{{IssueID: stored.ID, ID: stored.Parent, Type: "parent-child"}}
		stored.Parent = ""
	}
	l.all = append(l.all, stored)
	if failing {
		return beads.WorkItem{}, errors.New("bd create failed with status timed_out and exit code -1: " + `{"id":"` + stored.ID + `"`)
	}
	return stored, nil
}

// landingOptions is the product manager admitting one item under
// yoyodyne-ifd.428 from report-0d79ada8, as it did on 2026-09-24.
func landingOptions(t *testing.T, tracker Tracker, actions ...string) Options {
	t.Helper()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.", actions...)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	var sleeps []time.Duration
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{appended: []report.Report{filedReport(reported428, "two surfaces still name a resume that refuses")}}
	options.Sleep = recordingSleep(&sleeps)
	return options
}

const reported428 = "report-0d79ada8c4d92d9561f1d4a6ee81186e"

func admission428(title string) string {
	return `{"action":"create","kind":"feature","parent":"yoyodyne-ifd.428","title":"` + title + `","description":"d","goal":"` +
		theGoal + `","report":"` + reported428 + `","reason":"the developer reported it"}`
}

func tracker428() *fakeTracker {
	return &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.428": {ID: "yoyodyne-ifd.428", Title: "Integration stops name one next mover", Status: "open"},
	}}
}

// The 428.21/428.22 shape, replayed: one creation, citing one report, whose bd
// was killed at its timeout after the store had taken the write. The retry made
// the second item. What the retry does instead is find the first, so the store
// holds one item and the admission reports it.
func TestACreationThatTimedOutAfterLandingIsNotMadeTwice(t *testing.T) {
	t.Parallel()

	for _, edgeOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent stated only as an edge=%t", edgeOnly), func(t *testing.T) {
			t.Parallel()
			replayTimedOutLandedCreation(t, edgeOnly)
		})
	}
}

func replayTimedOutLandedCreation(t *testing.T, edgeOnly bool) {
	t.Helper()

	tracker := &landingTracker{fakeTracker: tracker428(), timeouts: 1, landed: true, edgeOnly: edgeOnly}
	options := landingOptions(t, tracker, admission428("The repair refusal and the channel line name the next mover the docket does"))
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit what the developer reported.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.all) != 1 || tracker.creates != 1 {
		t.Fatalf("items held = %#v after %d creation(s), want the one the timed-out creation left", tracker.all, tracker.creates)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || reply.Actions[0].WorkItemID != "yoyodyne-ifd.428.21" {
		t.Fatalf("actions = %#v, want the admission reported as the item that landed", reply.Actions)
	}
}

// A timed-out creation the store never took is still asked for again, and an
// item that only shares its title — admitted earlier, by another turn — is not
// taken for it.
func TestACreationThatTimedOutBeforeLandingIsAskedForAgain(t *testing.T) {
	t.Parallel()

	const title = "The repair refusal and the channel line name the next mover the docket does"
	base := tracker428()
	base.all = []beads.WorkItem{{ID: "yoyodyne-ifd.7", Title: title, Status: "closed",
		Notes: "Admitted to the backlog by the product manager in conversation chat-other, after turn 3."}}
	tracker := &landingTracker{fakeTracker: base, timeouts: 1}
	options := landingOptions(t, tracker, `{"action":"create","kind":"feature","title":"`+title+`","description":"d","goal":"`+theGoal+`","reason":"r"}`)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if tracker.creates != 2 || len(tracker.created) != 1 {
		t.Fatalf("creations asked = %d, landed = %d, want the creation asked for again and landing once", tracker.creates, len(tracker.created))
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || reply.Actions[0].WorkItemID == "yoyodyne-ifd.7" {
		t.Fatalf("actions = %#v, want a new item rather than the earlier one sharing its title", reply.Actions)
	}
}

// Two creations in one block citing one report: the second is checked against a
// listing taken after the first landed, so it is refused and names the first.
func TestTwoAdmissionsInOneBlockFromOneReportAdmitOne(t *testing.T) {
	t.Parallel()

	tracker := &landingTracker{fakeTracker: tracker428()}
	options := landingOptions(t, tracker,
		admission428("The repair refusal names the next mover the docket does"),
		admission428("The channel line for an integration stop names a re-run where the branch is gone"))
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit what the developer reported.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.all) != 1 {
		t.Fatalf("items held = %#v, want one admission from one report", tracker.all)
	}
	if len(reply.Actions) != 2 || !reply.Actions[0].Applied || reply.Actions[1].Applied {
		t.Fatalf("actions = %#v, want the first admitted and the second refused", reply.Actions)
	}
	for _, want := range []string{"yoyodyne-ifd.428.21", reported428} {
		if !strings.Contains(reply.Actions[1].Failure, want) {
			t.Fatalf("failure = %q, want it to name %q", reply.Actions[1].Failure, want)
		}
	}
}
