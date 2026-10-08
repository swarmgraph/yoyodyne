package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

const recordedGoal = "Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification."

func TestAdmittingWorkUnderAGoalTheGoalsDoNotStateIsRefused(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the plugin marketplace.",
			`{"action":"create","kind":"feature","title":"A plugin marketplace","description":"Third-party extensions.","goal":"Grow the ecosystem.","reason":"the operator asked"}`)},
		{SessionID: "session-1", FinalText: "It was refused; no goal says that."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	// The refusal names what was claimed and where the harness looked, so the
	// answer is to quote the goal rather than to try the same sentence again.
	failure := reply.Actions[0].Failure
	if !strings.Contains(failure, "Grow the ecosystem.") || !strings.Contains(failure, "v1-goals") {
		t.Fatalf("failure = %q", failure)
	}
	// Nothing reached the tracker. An item admitted under a goal nothing states
	// asserts a traceability that is not there, and it is refused before it
	// exists rather than reported afterwards.
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v", tracker.created)
	}
}

func TestAdmittedWorkRecordsTheGoalItResolvedTo(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"Resolve a work item's goal","description":"Make the attribution mean something.","goal":"`+recordedGoal+`","reason":"the chain breaks at its last link"}`)},
		{SessionID: "session-1", FinalText: "Admitted."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	// The goal was checked, so nothing says it was not.
	if strings.Contains(reply.Actions[0].Summary, "nothing checked") {
		t.Fatalf("summary = %q", reply.Actions[0].Summary)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v", tracker.created)
	}
	// What the harness writes is what it reads back: the item it just created
	// resolves to the goal it was admitted under.
	if !options.Goals.AttributionOf(tracker.created[0].Notes, goal.Witness{}).Resolved() {
		t.Fatalf("the admitted item does not resolve to a goal: %q", tracker.created[0].Notes)
	}
}

// The admission is where the item's attribution is written, so it is where
// identity has to be recorded or it is recorded nowhere. The product manager
// names the goal in the words they were reading; what goes onto the item is the
// goal, named by the identifier that survives the next amendment to those words.
func TestAdmittedWorkNamesItsGoalByIdentityAndSurvivesTheGoalBeingReworded(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"Give a goal an identity","description":"Stop a re-wording orphaning the work.","goal":"`+recordedGoal+`","reason":"three amendments have broken attributions"}`)},
		{SessionID: "session-1", FinalText: "Admitted."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoalsWithIdentity("traceable-chain", recordedGoal)

	if _, err := openTestSession(t, options).Send(context.Background(), "admit it"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v", tracker.created)
	}
	if !strings.Contains(tracker.created[0].Notes, goal.Note("[traceable-chain] "+recordedGoal)) {
		t.Fatalf("the admitted item does not name its goal by identity: %q", tracker.created[0].Notes)
	}
	// The operator directs a re-wording, and the same item still resolves — to the
	// goal as it now reads, which is the whole of what the identity is for.
	reworded := recordedGoalsWithIdentity("traceable-chain", recordedGoal+" Verification included.")
	attribution := reworded.AttributionOf(tracker.created[0].Notes, goal.Witness{})
	if !attribution.Resolved() || attribution.Identity != "traceable-chain" {
		t.Fatalf("a re-wording orphaned the item it had just admitted: %#v", attribution)
	}
}

func TestADecompositionChildKeepsTheGoalItWasCreatedUnder(t *testing.T) {
	t.Parallel()

	// A decomposition is the development manager's only creation, and the goal it
	// names has to survive exactly as an admission's does. The audit reads an
	// item's goal off the notes the tracker holds, so a creation that validated
	// the goal and then wrote it nowhere would orphan every child of every
	// decomposition — silently, because the action itself reported success, and at
	// scale, because decomposition is where most items now come from.
	//
	// Nothing here is a test double standing in for the tracker. The session acts
	// through the real beads client, so the note the chat layer writes has to
	// survive being turned into a bd command line and read back out of bd's
	// answer — which is the boundary an in-package fake cannot speak for.
	bd := &recordingBD{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("One child, under the admitted item.",
			`{"action":"create","kind":"feature","title":"Triage docket","description":"Stopped work reaches the development manager.","goal":"`+recordedGoal+`","parent":"yoyodyne-ifd.102","priority":1,"reason":"nothing routes stopped work today"}`)},
		{SessionID: "session-1", FinalText: "Filed."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}
	options.Goals = recordedGoals(recordedGoal)

	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "decompose ifd.102")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !reply.Actions[0].Applied {
		t.Fatalf("the creation was not applied: %s", reply.Actions[0].Failure)
	}

	// The creation reached bd as a decomposition under the admitted parent, and it
	// carried the note. A --notes bd never receives is an attribution that exists
	// only in the harness's own account of what it did.
	create := bd.command("create")
	if create == nil {
		t.Fatalf("no bd create was run: %#v", bd.args)
	}
	if !slices.Contains(create, "--parent=yoyodyne-ifd.102") {
		t.Fatalf("the child was not created under its parent: %#v", create)
	}

	// The item is then read back the way the audit reads it — off a bd listing,
	// through the same client — and asked the same question yoyo goals
	// attribution asks. Nothing between here and there is stubbed: the notes in
	// bd's listing are the ones bd was told to store.
	listed, err := beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}.List(context.Background(), "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %#v", listed)
	}
	attribution := options.Goals.AttributionOf(listed[0].Notes, listed[0].GoalWitness)
	if !attribution.Resolved() || attribution.Goal.ArtifactID != "v1-goals" {
		t.Fatalf("the decomposition child does not resolve to a goal: %#v\nnotes:\n%s", attribution, listed[0].Notes)
	}
	// The provenance and the goal are one note, and the goal is the part that has
	// been lost before: an item recording only who created it says nothing about
	// what the work is for.
	if !strings.Contains(listed[0].Notes, "Created under yoyodyne-ifd.102, decomposing it") {
		t.Fatalf("the child lost its provenance:\n%s", listed[0].Notes)
	}
	// And the tracker was told, outside those notes, which goal was written here.
	// That is what survives somebody replacing them, and it is why the loss would
	// be reported next time rather than read as work nobody has attributed yet.
	if listed[0].GoalWitness.Statement != recordedGoal {
		t.Fatalf("the creation left no witness of the goal it recorded: %#v", listed[0].GoalWitness)
	}
	wiped := beads.WorkItem{Notes: "Constraints from the architect.", GoalWitness: listed[0].GoalWitness}
	lost := options.Goals.AttributionOf(wiped.Notes, wiped.GoalWitness)
	if lost.State != goal.StateLost {
		t.Fatalf("replacing the child's notes reads as %q rather than a lost attribution", lost.State)
	}
	// And what it takes to put it back travels with the loss, so the child is
	// recoverable from the tracker rather than from somebody's memory of the
	// decomposition that made it.
	if lost.Recorded != recordedGoal {
		t.Fatalf("the loss does not say which goal to put back: %#v", lost)
	}
}

// recordingBD stands in for the bd binary rather than for the tracker: it keeps
// whatever a creation stored and gives it back on a listing, exactly as bd does
// — verified against a real bd, which returns the notes and metadata a create
// was given both in its own answer and in a later list --json. It invents
// nothing. An item listed with no notes is an item created with none, which is
// what makes the round trip through the command line worth asserting on.
type recordingBD struct {
	args  [][]string
	items []map[string]any
}

func (r *recordingBD) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.args = append(r.args, append([]string(nil), command.Args...))
	answer := "[]"
	switch {
	case len(command.Args) > 0 && command.Args[0] == "create":
		item := map[string]any{
			"id":         fmt.Sprintf("yoyodyne-ifd.102.%d", len(r.items)+2),
			"status":     "open",
			"priority":   1,
			"issue_type": "task",
		}
		for _, argument := range command.Args {
			for flag, field := range map[string]string{"--title=": "title", "--description=": "description", "--notes=": "notes"} {
				if value, carried := strings.CutPrefix(argument, flag); carried {
					item[field] = value
				}
			}
			// bd takes an item's whole metadata as JSON at creation and stores what
			// it is given, so it is decoded here rather than pattern-matched: what a
			// listing carries afterwards is that object.
			if value, carried := strings.CutPrefix(argument, "--metadata="); carried {
				var metadata map[string]any
				if err := json.Unmarshal([]byte(value), &metadata); err != nil {
					return execution.ProcessResult{}, err
				}
				item["metadata"] = metadata
			}
		}
		r.items = append(r.items, item)
		encoded, err := json.Marshal(item)
		if err != nil {
			return execution.ProcessResult{}, err
		}
		answer = string(encoded)
	case len(command.Args) > 0 && command.Args[0] == "list":
		encoded, err := json.Marshal(r.items)
		if err != nil {
			return execution.ProcessResult{}, err
		}
		answer = string(encoded)
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: answer}, nil
}

// command returns the arguments of the one bd invocation with this verb, or nil
// when there was none.
func (r *recordingBD) command(verb string) []string {
	for _, args := range r.args {
		if len(args) > 0 && args[0] == verb {
			return args
		}
	}
	return nil
}

func TestWorkAdmittedBeforeGoalsWereCheckedCanAcquireOne(t *testing.T) {
	t.Parallel()

	legacy := "Admitted to the backlog by the product manager in conversation chat-1, after turn 4.\n\nReason: the operator asked for it."
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.4": {ID: "yoyodyne-ifd.4", Title: "Add configurable management roles", Status: "open", Notes: legacy},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("It serves the traceability goal.",
			`{"action":"attribute","id":"yoyodyne-ifd.4","goal":"`+recordedGoal+`","reason":"the roles exist to keep intent traceable"}`)},
		{SessionID: "session-1", FinalText: "Attributed."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "attribute ifd.4")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
	change := tracker.updates[0].change
	// Nothing already on the item is rewritten: the goal an item was admitted
	// under cannot be edited, and the attribution is appended to what is there.
	if change.Title != "" || change.Description != "" || change.Priority != nil || change.Parent != nil {
		t.Fatalf("attributing rewrote something: %#v", change)
	}
	if !strings.Contains(change.AppendNotes, "Attributed to a goal by the Lead Product Manager") {
		t.Fatalf("appended notes carry no provenance: %q", change.AppendNotes)
	}
	// The item, with what was appended, now resolves.
	attribution := options.Goals.AttributionOf(legacy+"\n\n"+change.AppendNotes, goal.Witness{})
	if !attribution.Resolved() || attribution.Goal.ArtifactID != "v1-goals" {
		t.Fatalf("attribution = %#v", attribution)
	}
}

func TestAttributingWorkToAGoalTheGoalsDoNotStateIsRefused(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.4": {ID: "yoyodyne-ifd.4", Title: "Add configurable management roles", Status: "open"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Attributing it.",
			`{"action":"attribute","id":"yoyodyne-ifd.4","goal":"Make the harness pleasant to use.","reason":"it feels right"}`)},
		{SessionID: "session-1", FinalText: "It was refused."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "attribute ifd.4")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	// An attribution nothing supports is exactly as wrong as an admission under
	// one, so it is refused the same way and the item is left saying nothing.
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
}

func TestAdmittingWorkWithNoRecordedGoalsIsRefusedRatherThanUnchecked(t *testing.T) {
	t.Parallel()

	// A project that has not written its goals down yet has approved no goal, so
	// there is nothing for an admission without asking to rest on. This is the
	// gate at the goals doing its one job: work whose attribution nobody could
	// check is not work anybody agreed to, and admitting it anyway would move the
	// gate up and remove it in the same change. The work is not lost -- it is
	// proposed, and the operator decides -- which is what makes refusing here
	// cost nothing.
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"Write the goals","description":"There are none yet.","goal":"Have goals to work from.","reason":"nothing traces anywhere"}`)},
		{SessionID: "session-1", FinalText: "Refused, then."},
	}})
	options.Tracker = tracker

	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v", tracker.created)
	}
	// The refusal says what was missing and what to do instead, because the
	// product manager has somewhere else to put this work.
	for _, required := range []string{
		"there is nothing to check it against",
		"a goal the operator approved",
		"propose it",
	} {
		if !strings.Contains(reply.Actions[0].Failure, required) {
			t.Fatalf("failure = %q, want it to state %q", reply.Actions[0].Failure, required)
		}
	}
}

func TestAProjectWithNoGoalsWrittenDownCanStillFileTheWorkOfWritingThem(t *testing.T) {
	t.Parallel()

	// This is the other half of the refusal above, and the reason refusing there
	// costs nothing. A repository with no goals is the documented ordinary
	// starting case, so a harness that could not file the work of writing its own
	// goals from the conversation would have refused the one thing such a project
	// needs to do first. It cannot admit that work without asking -- nobody has
	// approved anything for it to rest on -- but it can propose it, and the
	// operator approving is what creates it.
	//
	// The re-check that approval makes is deliberately about the goal still being
	// there rather than about it having been approved: an attribution nothing
	// could check is not an attribution that resolved to something wrong, so it
	// does not stand in the way of the operator's own decision.
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n" + proposalFence +
			"\n{\"items\":[{\"kind\":\"feature\",\"title\":\"Write the goals\",\"description\":\"There are none yet.\",\"rationale\":\"Nothing traces anywhere.\",\"goal\":\"Have goals to work from.\"}]}\n```\n"},
	}})
	options.Tracker = tracker
	// No Goals at all, which is what a fresh repository has.
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what next")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	// Nothing was admitted without asking, and the work became a question rather
	// than being lost.
	if len(reply.Admitted) != 0 {
		t.Fatalf("admitted = %#v, want nothing admitted where no goal is approved", reply.Admitted)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v, want the work put to the operator", reply.Proposals)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want nothing created before the operator decided", tracker.created)
	}

	item, err := session.Approve(context.Background(), reply.Proposals[0].ID)
	if err != nil {
		t.Fatalf("Approve() error = %v, want a goal-less repository to be able to file this work", err)
	}
	if item.WorkItemID == "" || len(tracker.created) != 1 {
		t.Fatalf("item = %#v, created = %#v", item, tracker.created)
	}
	// It is recorded as the operator's decision, because it was one. Nothing here
	// may claim a goal approved it.
	if notes := tracker.created[0].Notes; !strings.Contains(notes, "approved by the operator") {
		t.Fatalf("notes = %q, want the operator's approval recorded as the authority", notes)
	}
}

func TestASurveySaysWhichAdmittedWorkNamesNoGoal(t *testing.T) {
	t.Parallel()

	goals := recordedGoals(recordedGoal)
	rendered := renderOpenQueueEvidence([]beads.WorkItem{
		{ID: "yoyodyne-ifd.1", Title: "Attributed work", Status: "open", Notes: goal.Note(recordedGoal)},
		{ID: "yoyodyne-ifd.2", Title: "Legacy work", Status: "open", Notes: "Admitted long ago."},
		{ID: "yoyodyne-ifd.3", Title: "Misattributed work", Status: "open", Notes: goal.Note("Grow the ecosystem.")},
		// An item whose notes were replaced by something that did not carry its
		// goal forward. It is not legacy work, and the survey must not offer it as
		// something to attribute afresh: the goal it served is in the record of
		// what was written on it.
		{ID: "yoyodyne-ifd.4", Title: "Overwritten work", Status: "open", Notes: "Constraints from the architect.", GoalWitness: goal.Witness{Recorded: true, Statement: recordedGoal}},
	}, goals)

	// The survey is where the product manager would go to attribute the backlog,
	// so it says which items are waiting for one, which carry a claim that is
	// wrong, and which lost what they recorded, without turning the listing into a
	// per-item report.
	for _, want := range []string{
		"1 of 4 name a goal the goals state, 1 name none, 1 name a goal the goals do not state, and 1 recorded a goal and lost it",
		"yoyodyne-ifd.2",
		"yoyodyne-ifd.3",
		"a record destroyed rather than work to attribute afresh: yoyodyne-ifd.4",
		`"attribute" records a goal`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("survey = %q, want it to contain %q", rendered, want)
		}
	}

	// A survey of a long queue is cut at its end, so what the queue is for has to
	// survive the cut: an account of its traceability that was cut off would read
	// as a queue with nothing to say about it.
	long := make([]beads.WorkItem, 0, maxTrackerSurveyItems)
	for index := range maxTrackerSurveyItems {
		long = append(long, beads.WorkItem{
			ID:     "yoyodyne-ifd." + strconv.Itoa(index),
			Title:  strings.Repeat("a long title ", 16),
			Status: "open",
		})
	}
	cut := renderOpenQueueEvidence(long, goals)
	if !strings.Contains(cut, "cut at") {
		t.Fatalf("a survey of %d long items was not cut", len(long))
	}
	if !strings.Contains(cut, "0 of 200 name a goal the goals state") {
		t.Fatalf("the cut survey lost what the queue is for: %q", cut)
	}

	// With nothing to check against, nothing is counted: a tally taken against
	// goals nobody could read would report traceability that was never confirmed.
	unchecked := renderOpenQueueEvidence([]beads.WorkItem{{ID: "yoyodyne-ifd.1", Status: "open"}}, goal.Set{})
	if !strings.Contains(unchecked, "What the queue is for was not checked") {
		t.Fatalf("survey = %q", unchecked)
	}
	// An item that lost its goal is said even then, because saying so rests on the
	// tracker's own record of what was written rather than on any goals document.
	lost := renderOpenQueueEvidence([]beads.WorkItem{
		{ID: "yoyodyne-ifd.1", Status: "open"},
		{ID: "yoyodyne-ifd.4", Status: "open", Notes: "Constraints from the architect.", GoalWitness: goal.Witness{Recorded: true, Statement: recordedGoal}},
	}, goal.Set{})
	if !strings.Contains(lost, "recorded a goal and no longer carry it: yoyodyne-ifd.4") {
		t.Fatalf("survey = %q", lost)
	}
}

func TestReadingAnItemSaysWhatItIsFor(t *testing.T) {
	t.Parallel()

	goals := recordedGoals(recordedGoal)
	attributed := renderWorkItemEvidence(beads.WorkItem{ID: "yoyodyne-ifd.1", Notes: goal.Note(recordedGoal)}, goals)
	if !strings.Contains(attributed, "attribution: it serves a goal v1-goals states") {
		t.Fatalf("rendered item = %q", attributed)
	}
	legacy := renderWorkItemEvidence(beads.WorkItem{ID: "yoyodyne-ifd.2", Notes: "Admitted long ago."}, goals)
	if !strings.Contains(legacy, "attribution: none recorded") {
		t.Fatalf("rendered item = %q", legacy)
	}
	wrong := renderWorkItemEvidence(beads.WorkItem{ID: "yoyodyne-ifd.3", Notes: goal.Note("Grow the ecosystem.")}, goals)
	// Told apart, because they are not the same thing to do about it: one is
	// work to attribute and the other is a claim to correct.
	if !strings.Contains(wrong, "Grow the ecosystem.") || strings.Contains(wrong, "attribution: none recorded") {
		t.Fatalf("rendered item = %q", wrong)
	}
	// An item whose notes lost their goal is read as having lost it, and the words
	// the tracker kept are quoted: restoring an attribution is naming that goal
	// again, and this is the line the product manager acts on. Reading the item is
	// what the survey sends them to for exactly this.
	destroyed := renderWorkItemEvidence(beads.WorkItem{
		ID: "yoyodyne-ifd.4", Notes: "Constraints from the architect.",
		GoalWitness: goal.Witness{Recorded: true, Statement: recordedGoal},
	}, goals)
	if !strings.Contains(destroyed, "attribution: recorded and lost") || !strings.Contains(destroyed, recordedGoal) {
		t.Fatalf("rendered item = %q", destroyed)
	}
	// And where the tracker kept no words, it says so rather than implying a
	// record somebody could go and read.
	bare := renderWorkItemEvidence(beads.WorkItem{
		ID: "yoyodyne-ifd.5", Notes: "Constraints from the architect.",
		GoalWitness: goal.Witness{Recorded: true},
	}, goals)
	if !strings.Contains(bare, "recovered from outside the tracker") {
		t.Fatalf("rendered item = %q", bare)
	}
}

func TestWorkProposedUnderAGoalTheGoalsDoNotStateIsNotPutToTheOperator(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n" + proposalFence +
			"\n{\"items\":[{\"kind\":\"feature\",\"title\":\"A plugin marketplace\",\"description\":\"Third-party extensions.\",\"rationale\":\"You raised it.\",\"goal\":\"Grow the ecosystem.\"}]}\n```\n"},
	}})
	options.Tracker = &fakeTracker{}
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "what about a marketplace")
	var unrecorded *ProposalGoalError
	if !errors.As(err, &unrecorded) {
		t.Fatalf("Send() error = %v, want a ProposalGoalError", err)
	}
	// The operator is never asked. An approval that the creation then refuses has
	// already spent the decision it was asking for.
	if len(reply.Proposals) != 0 {
		t.Fatalf("proposals = %#v", reply.Proposals)
	}
	if !strings.Contains(err.Error(), "Grow the ecosystem.") {
		t.Fatalf("error = %v", err)
	}
}

func TestApprovingAProposalWhoseGoalHasSinceGoneRefusesRatherThanCreating(t *testing.T) {
	t.Parallel()

	// The goals are read from the repository rather than from the conversation,
	// so between the proposal and the approval the goal can be reworded or
	// retired. What must not happen is an item admitted under a goal that is no
	// longer there.
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n" + proposalFence +
			"\n{\"items\":[{\"kind\":\"feature\",\"title\":\"Resolve a work item's goal\",\"description\":\"Make it mean something.\",\"rationale\":\"You raised it.\",\"goal\":\"" + recordedGoal + "\"}]}\n```\n"},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	// Per-item approval, so there is a proposal for the operator to approve at
	// all: this is about the re-check that approval makes, not about what a
	// project that admits work itself would have done with it.
	options.Admission = Admission{WorkItems: domain.ApprovalHuman}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what next")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v", reply.Proposals)
	}
	session.options.Goals = recordedGoals("Something else entirely.")
	if _, err := session.Approve(context.Background(), reply.Proposals[0].ID); err == nil {
		t.Fatalf("Approve() created an item under a goal that is no longer recorded")
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v", tracker.created)
	}
}

// recordedGoals is a repository whose goals document states what a test names,
// so a test about attribution says only that.
// recordedGoalsWithIdentity is the same repository with the one goal carrying
// the identity a goals document assigns it, which is what an attribution
// resolves by and what a re-wording leaves alone.
func recordedGoalsWithIdentity(identity, statement string) goal.Set {
	set := recordedGoals(statement)
	set.Goals[0].Identity = identity
	return set
}

func recordedGoals(statements ...string) goal.Set {
	set := goal.Set{Sources: []string{"v1-goals"}}
	for _, statement := range statements {
		set.Goals = append(set.Goals, goal.Goal{
			Statement:  statement,
			ArtifactID: "v1-goals",
			Path:       "docs/product/goals/v1-goals.md",
			InForce:    true,
			// Approved as the document now stands, which is what lets work naming
			// one of these reach the queue without the operator being asked again.
			Approval: artifact.ApprovalApproved,
		})
	}
	return set
}
