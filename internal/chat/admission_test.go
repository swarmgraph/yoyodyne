package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// unapprovedGoals is a repository whose goals document nobody has approved, and
// amendedGoals one approved and edited since. They are the two ways a goal can
// be stated and still not be something the operator agreed to, and they are the
// whole difference between moving the gate up and removing it.
func unapprovedGoals(statements ...string) goal.Set {
	return goalsApproved(artifact.ApprovalUnapproved, statements...)
}

func amendedGoals(statements ...string) goal.Set {
	return goalsApproved(artifact.ApprovalAmended, statements...)
}

func goalsApproved(state artifact.ApprovalState, statements ...string) goal.Set {
	set := recordedGoals(statements...)
	for index := range set.Goals {
		set.Goals[index].Approval = state
	}
	return set
}

func proposeRecordedGoal(title string) string {
	return `{"kind":"feature","title":"` + title + `","description":"What done means.","rationale":"You asked for it.","goal":"` + recordedGoal + `"}`
}

// Work that traces to a goal the operator approved goes into the queue without
// being put to them. That is the whole of what moving approval up to the goals
// buys: the operator approves what the product should do, and the items that
// serve it stop being one prompt each.
func TestWorkServingAnApprovedGoalIsAdmittedWithoutAskingTheOperator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("Two follow it.",
			proposeRecordedGoal("Resolve a work item's goal"),
			proposeRecordedGoal("Report what was admitted")),
	}}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what follows from that")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 2 {
		t.Fatalf("admitted = %#v", reply.Admitted)
	}
	// Nothing is waiting on the operator, in the reply or in the session: work
	// that passed the gate at the goals is not put to them a second time.
	if len(reply.Proposals) != 0 {
		t.Fatalf("proposals = %#v", reply.Proposals)
	}
	if pending := session.Proposals(); len(pending) != 0 {
		t.Fatalf("admitted proposals still await a decision: %#v", pending)
	}
	if len(tracker.created) != 2 {
		t.Fatalf("created = %#v", tracker.created)
	}

	// The item says what put it in the queue. An item admitted without a prompt
	// that recorded an approval nobody gave would be the one claim this whole
	// arrangement has to be able to prove it did not make.
	notes := tracker.created[0].Notes
	if strings.Contains(notes, "approved by the operator") {
		t.Fatalf("an admitted item claims an approval nobody gave: %q", notes)
	}
	for _, required := range []string{
		"without asking the operator",
		"v1-goals",
		goal.Note(recordedGoal),
	} {
		if !strings.Contains(notes, required) {
			t.Fatalf("admitted item notes = %q, want them to state %q", notes, required)
		}
	}

	// And the durable record says the same. An admission is its own event rather
	// than an approval with a different reason.
	counted := countEvents(t, root, session)
	if counted[execution.EventProposalAdmitted] != 2 || counted[execution.EventProposalCreated] != 2 {
		t.Fatalf("recorded events = %#v", counted)
	}
	if counted[execution.EventProposalApproved] != 0 {
		t.Fatalf("work nobody was asked about was recorded as approved: %#v", counted)
	}
}

// The operator finds out. Autonomy nobody can see afterwards is what work
// happening behind their back looks like, whatever the reason for it was.
func TestWhatWasAdmittedWithoutAskingIsReportedToTheOperator(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("This one follows.", proposeRecordedGoal("Resolve a work item's goal")),
	}}})
	options.Tracker = &fakeTracker{}
	options.Goals = recordedGoals(recordedGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what follows")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	var out strings.Builder
	session.reportAdmitted(&out, reply)
	for _, required := range []string{
		"admitted to the queue without asking you",
		"yoyodyne-1",
		"Resolve a work item's goal",
		recordedGoal,
	} {
		if !strings.Contains(out.String(), required) {
			t.Fatalf("what the operator reads = %q, want it to state %q", out.String(), required)
		}
	}
	// The product manager is told the same thing, because an agent that never
	// learns what became of its own proposals describes them wrongly next turn.
	if len(session.notices) == 0 || !strings.Contains(strings.Join(session.notices, "\n"), "without asking the operator") {
		t.Fatalf("notices = %#v", session.notices)
	}
}

// The gate moved up to the goals, so it is only a gate where a goal was
// actually approved. Each of these is work that names a goal and still is not
// work anybody agreed to, and every one of them is put to the operator.
func TestWorkThatDoesNotTraceToAnApprovedGoalStillStopsAndAsks(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		goals goal.Set
		want  string
	}{
		{
			name:  "a goals document nobody approved",
			goals: unapprovedGoals(recordedGoal),
			want:  "records no approval",
		},
		{
			name:  "a goals document amended since it was approved",
			goals: amendedGoals(recordedGoal),
			want:  "amended since the operator approved it",
		},
		{
			name: "a repository with no goals to check against",
			// Nothing to resolve against is not permission. An attribution nobody
			// could check is exactly the case the gate exists for.
			goals: goal.Set{},
			want:  "nothing to check it against",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tracker := &fakeTracker{}
			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
				SessionID: "session-1",
				FinalText: proposalReply("This one follows.", proposeRecordedGoal("Resolve a work item's goal")),
			}}})
			options.Tracker = tracker
			options.Goals = testCase.goals
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "what follows")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Admitted) != 0 || len(tracker.created) != 0 {
				t.Fatalf("work reached the queue without an approved goal: admitted %#v, created %#v", reply.Admitted, tracker.created)
			}
			if len(reply.Proposals) != 1 {
				t.Fatalf("proposals = %#v", reply.Proposals)
			}
			// The operator is told why they are being asked, on the proposal itself,
			// because being asked without being told what is missing leaves them
			// deciding blind.
			if !strings.Contains(reply.Proposals[0].Asking, testCase.want) {
				t.Fatalf("asking = %q, want it to state %q", reply.Proposals[0].Asking, testCase.want)
			}
			if !strings.Contains(reply.Proposals[0].Render(), "asking you because") {
				t.Fatalf("the operator is not told why they are deciding: %q", reply.Proposals[0].Render())
			}
		})
	}
}

// Work the product manager cannot place under a goal at all never reaches this
// gate: it is a concern, and a concern stops and waits whatever the admission
// policy says. Moving approval up a level did not move this.
func TestWorkThatTracesToNoGoalIsStillAQuestionRatherThanAnAdmission(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: "I cannot place this.\n\n" + concernFence + "\n" +
			`{"concerns":[{"kind":"unplaceable","subject":"A plugin marketplace","detail":"No goal reaches it.","question":"Is there a goal I am missing?"}]}` + "\n```\n",
	}}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what about a marketplace")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Concerns) != 1 {
		t.Fatalf("concerns = %#v", reply.Concerns)
	}
	if len(reply.Admitted) != 0 || len(tracker.created) != 0 {
		t.Fatalf("a concern put work in the queue: admitted %#v, created %#v", reply.Admitted, tracker.created)
	}
	if len(session.Concerns()) != 1 {
		t.Fatalf("the question was not left open: %#v", session.Concerns())
	}
}

// Per-item approval is still there for a project that wants it, and turning it
// on covers both ways work reaches the queue. A policy that governed proposals
// while the product manager admitted work directly would be a setting that says
// one thing and does another.
func TestPerItemApprovalRemainsAvailableAndCoversBothWaysWorkIsAdmitted(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: proposalReply("This one follows.", proposeRecordedGoal("Resolve a work item's goal"))},
		{SessionID: "session-1", FinalText: trackerReply("Filing it myself, then.",
			`{"action":"create","kind":"feature","title":"Resolve a work item's goal","description":"Make it mean something.","goal":"`+recordedGoal+`","reason":"it follows"}`)},
		{SessionID: "session-1", FinalText: "Refused, then."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalHuman}
	session := openTestSession(t, options)

	// The proposal waits on the operator even though it serves an approved goal.
	reply, err := session.Send(context.Background(), "what follows")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 0 || len(reply.Proposals) != 1 {
		t.Fatalf("admitted %#v, proposals %#v", reply.Admitted, reply.Proposals)
	}
	// The reason is the policy rather than anything about the work, so it is not
	// written onto the proposal as though it were.
	if reply.Proposals[0].Asking != "" {
		t.Fatalf("asking = %q, want the policy left off the proposal", reply.Proposals[0].Asking)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v", tracker.created)
	}

	// And admitting it directly is refused, naming the setting that refused it.
	direct, err := session.Send(context.Background(), "just file it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(direct.Actions) != 1 || direct.Actions[0].Applied {
		t.Fatalf("actions = %#v", direct.Actions)
	}
	for _, required := range []string{"approvals.work_items", "propose it instead"} {
		if !strings.Contains(direct.Actions[0].Failure, required) {
			t.Fatalf("failure = %q, want it to state %q", direct.Actions[0].Failure, required)
		}
	}
	if len(tracker.created) != 0 {
		t.Fatalf("work was admitted under per-item approval: %#v", tracker.created)
	}
}

// Decomposition is not admission. A role that may only create underneath work
// somebody already admitted is building structure under an existing decision,
// and holding it to the admission gate would gate the wrong act.
func TestDecompositionIsNotHeldToTheAdmissionGate(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("One child, under the admitted item.",
			`{"action":"create","kind":"feature","title":"Triage docket","description":"Stopped work reaches the manager.","goal":"`+recordedGoal+`","parent":"yoyodyne-ifd.102","reason":"nothing routes stopped work"}`)},
		{SessionID: "session-1", FinalText: "Filed."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	created := &fakeTracker{}
	options.Tracker = created
	// The strictest policy there is, and the one goal state that refuses an
	// admission outright. Neither touches a decomposition.
	options.Admission = Admission{WorkItems: domain.ApprovalHuman}
	options.Goals = unapprovedGoals(recordedGoal)

	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "decompose ifd.102")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(created.created) != 1 || created.created[0].Parent != "yoyodyne-ifd.102" {
		t.Fatalf("created = %#v", created.created)
	}
}

// A tracker that will not create leaves the work with the operator rather than
// losing it. Nothing was created, so being asked is the outcome that can still
// be acted on.
func TestAProposalTheTrackerRefusesIsLeftForTheOperatorRatherThanLost(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("This one follows.", proposeRecordedGoal("Resolve a work item's goal")),
	}}})
	options.Tracker = &fakeTracker{err: errors.New("bd create failed: the tracker is unavailable")}
	options.Goals = recordedGoals(recordedGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what follows")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 0 {
		t.Fatalf("admitted = %#v", reply.Admitted)
	}
	if len(reply.Proposals) != 1 || len(session.Proposals()) != 1 {
		t.Fatalf("the work was lost rather than left for the operator: reply %#v, pending %#v", reply.Proposals, session.Proposals())
	}
}

// The contract the product manager is sent says what this project will do with
// the work it names. A role that does not know whether its proposals will be
// admitted or put to somebody describes what it is doing wrongly.
func TestTheContractStatesTheAdmissionPolicyInForce(t *testing.T) {
	t.Parallel()

	automatic := SystemPrompt(domain.RoleProductManager, Admission{WorkItems: domain.ApprovalAutomatic}, nil, "")
	if !strings.Contains(automatic, "admits work that traces to a goal the operator approved") {
		t.Fatalf("the contract does not state that work is admitted: %q", automatic)
	}
	perItem := SystemPrompt(domain.RoleProductManager, Admission{WorkItems: domain.ApprovalHuman}, nil, "")
	if !strings.Contains(perItem, `"create" is refused`) {
		t.Fatalf("the contract does not state that admission is refused: %q", perItem)
	}
	if strings.Contains(perItem, "admits work that traces to a goal the operator approved") {
		t.Fatalf("the per-item contract still promises admission without asking")
	}
	// A role that cannot admit work at all is told nothing about admission: the
	// clause is about an authority it does not have.
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer} {
		prompt := SystemPrompt(role, Admission{WorkItems: domain.ApprovalAutomatic}, nil, "")
		if strings.Contains(prompt, "admits work that traces to a goal the operator approved") {
			t.Fatalf("%s was told an admission policy it cannot act on", role)
		}
	}
}
