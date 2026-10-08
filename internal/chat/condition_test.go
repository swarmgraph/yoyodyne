package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
)

// testHomes is the artifact homes a project on the recommended layout has, owning
// the design the incidents behind this gate were about.
func testHomes() protectedpath.Homes {
	return protectedpath.ArtifactHomes(config.Config{Product: config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}}, protectedpath.Document{ID: "observability-and-dashboard", Path: "docs/designs/observability-and-dashboard.md"})
}

// The condition the reviewer caught after yoyodyne-ifd.141.1 had spent itself,
// as the product manager would write it into an admission.
const designCondition = "The read model needs one query. Done means: the read model exposes the capacity-blocked state; and the observability-and-dashboard design's query list marks the query as existing."

// The clause is caught where the item is written, on every door that carries
// an item's text: admitting work, rewriting admitted work, and proposing it.
// The refusal quotes the clause and names both fixes, so the role's next turn
// corrects the item rather than wording the condition differently.
func TestADoneConditionNamingAnUngrantedDesignIsRefusedAtEveryDoorIntoTheQueue(t *testing.T) {
	t.Parallel()

	wanted := []string{"query list marks the query as existing", "docs/designs/observability-and-dashboard.md", "governed path", protectedpath.GrantMarker}

	t.Run("create", func(t *testing.T) {
		t.Parallel()
		tracker := &fakeTracker{}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("Admitting the first child.",
				`{"action":"create","kind":"feature","title":"Add the capacity-blocked state","description":"`+designCondition+`","goal":"Run development nearly autonomously.","reason":"the operator directed it"}`)},
			{SessionID: "session-1", FinalText: "Refused; I will reword it."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Goals = recordedGoals("Run development nearly autonomously.")
		options.ArtifactHomes = testHomes()
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Admit the first dashboard child.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(tracker.created) != 0 {
			t.Fatalf("created = %#v, want nothing admitted against a condition no run can meet", tracker.created)
		}
		if len(reply.Actions) != 1 || reply.Actions[0].Applied {
			t.Fatalf("actions = %#v, want the creation refused", reply.Actions)
		}
		for _, want := range wanted {
			if !strings.Contains(reply.Actions[0].Failure, want) {
				t.Fatalf("refusal %q never says %q", reply.Actions[0].Failure, want)
			}
		}
	})

	t.Run("update", func(t *testing.T) {
		t.Parallel()
		tracker := &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.141.1": {ID: "yoyodyne-ifd.141.1", Title: "Add the capacity-blocked state", Description: "The read model needs one query.", Status: "open"},
		}}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("Tightening what done means.",
				`{"action":"update","id":"yoyodyne-ifd.141.1","description":"`+designCondition+`","reason":"the design has to say the query exists"}`)},
			{SessionID: "session-1", FinalText: "Refused; the architect records it."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.ArtifactHomes = testHomes()
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Say in 141.1 that the design has to mark the query.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(tracker.updates) != 0 {
			t.Fatalf("updates = %#v, want the item left as it was", tracker.updates)
		}
		if len(reply.Actions) != 1 || reply.Actions[0].Applied {
			t.Fatalf("actions = %#v, want the update refused", reply.Actions)
		}
		for _, want := range wanted {
			if !strings.Contains(reply.Actions[0].Failure, want) {
				t.Fatalf("refusal %q never says %q", reply.Actions[0].Failure, want)
			}
		}
	})

	t.Run("proposal", func(t *testing.T) {
		t.Parallel()
		tracker := &fakeTracker{}
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1",
			FinalText: proposalReply("One item follows.",
				`{"kind":"feature","title":"Add the capacity-blocked state","description":"`+designCondition+`","rationale":"the dashboard needs it","goal":"`+recordedGoal+`"}`),
		}}})
		options.Tracker = tracker
		options.Goals = recordedGoals(recordedGoal)
		options.ArtifactHomes = testHomes()
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "what follows")
		var unmeetable *ProposalConditionError
		if !errors.As(err, &unmeetable) {
			t.Fatalf("Send() error = %v, want a ProposalConditionError", err)
		}
		for _, want := range wanted {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal %q never says %q", err, want)
			}
		}
		if len(reply.Proposals) != 0 || len(reply.Admitted) != 0 || len(tracker.created) != 0 {
			t.Fatalf("proposals = %#v, admitted = %#v, created = %#v; want nothing put to the operator or the queue", reply.Proposals, reply.Admitted, tracker.created)
		}
	})
}

// The grant admits the condition exactly as it admits the diff, and an update
// is judged as the item will read once it lands: the grant may sit in a field
// the action does not carry. An update that rewrites no description writes no
// condition, so an item admitted before this gate with one in its criteria can
// still be written on — which is what the product manager did to
// yoyodyne-ifd.63 on 2026-09-19, and what the run refusing to start relies on.
func TestAGrantAdmitsTheConditionAndAnUpdateIsJudgedOnWhatItRewrites(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		// The grant is in the design guidance, which no action writes; the
		// description the update brings names the design.
		"yoyodyne-ifd.141.1": {
			ID: "yoyodyne-ifd.141.1", Title: "Add the capacity-blocked state", Status: "open",
			Description: "The read model needs one query.",
			Design:      protectedpath.GrantMarker + " docs/designs/observability-and-dashboard.md\n",
		},
		// The condition is in the acceptance criteria already, and the note says
		// so; the note is applied, and a description that repeats the condition
		// is not.
		"yoyodyne-ifd.63": {
			ID: "yoyodyne-ifd.63", Title: "Fold the status script into the verb", Status: "open",
			Description:        "The script is not installed.",
			AcceptanceCriteria: "Documentation reconciles, including docs/designs/v1-harness-design.md's yoyo status entry.",
		},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Three updates.",
			`{"action":"update","id":"yoyodyne-ifd.141.1","description":"`+designCondition+`","reason":"the grant is on the item"}`,
			`{"action":"update","id":"yoyodyne-ifd.63","note":"the done-means clause names a protected path this item grants nothing on, so no run can satisfy it","reason":"so the item says why"}`,
			`{"action":"update","id":"yoyodyne-ifd.63","description":"The script is not installed. Done means the script is folded in and docs/designs/v1-harness-design.md's yoyo status entry reconciles.","reason":"restating what done means"}`)},
		{SessionID: "session-1", FinalText: "Two landed, one refused."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.ArtifactHomes = testHomes()
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Update all three.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 3 {
		t.Fatalf("actions = %#v, want three", reply.Actions)
	}
	if !reply.Actions[0].Applied {
		t.Fatalf("the granted update was refused: %q", reply.Actions[0].Failure)
	}
	if !reply.Actions[1].Applied {
		t.Fatalf("the note onto an item whose criteria carry the condition was refused: %q", reply.Actions[1].Failure)
	}
	if reply.Actions[2].Applied || !strings.Contains(reply.Actions[2].Failure, "docs/designs/v1-harness-design.md") {
		t.Fatalf("the description repeating the condition = %#v, want it refused naming the design", reply.Actions[2])
	}
	if len(tracker.updates) != 2 || tracker.updates[0].id != "yoyodyne-ifd.141.1" || tracker.updates[1].change.AppendNotes == "" {
		t.Fatalf("updates = %#v, want the granted description and the note written and nothing else", tracker.updates)
	}
}

// A conversation wired no homes checks nothing, which is what every admission
// did before this existed, and a citation outside the done-conditions is not a
// condition however the item is wired.
func TestACitationOrAnUnwiredConversationRefusesNothing(t *testing.T) {
	t.Parallel()

	for _, variant := range []struct {
		name        string
		description string
		homes       protectedpath.Homes
	}{
		{name: "no homes wired", description: designCondition},
		{name: "a citation", description: "The observability-and-dashboard design requires one query, per docs/designs/observability-and-dashboard.md. Done means the read model exposes it and the run's summary names it for the architect to record.", homes: testHomes()},
	} {
		t.Run(variant.name, func(t *testing.T) {
			t.Parallel()
			tracker := &fakeTracker{}
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
					`{"action":"create","kind":"feature","title":"Add the capacity-blocked state","description":"`+variant.description+`","goal":"Run development nearly autonomously.","reason":"the operator directed it"}`)},
				{SessionID: "session-1", FinalText: "Admitted."},
			}}
			options := testOptions(t, provider)
			options.Tracker = tracker
			options.Goals = recordedGoals("Run development nearly autonomously.")
			options.ArtifactHomes = variant.homes
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "Admit it.")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v, want the creation applied", reply.Actions)
			}
			if len(tracker.created) != 1 {
				t.Fatalf("created = %#v, want the item admitted", tracker.created)
			}
		})
	}
}

// The contracts are what a role writes an item's text from, so a rule the
// harness refuses by and no contract states is one a role learns by being
// refused. Both roles that write item text are told it.
func TestTheContractsStateTheDoneConditionRule(t *testing.T) {
	t.Parallel()

	for _, contract := range []struct {
		who  string
		text string
	}{
		{who: "product manager", text: productManagerContract},
		{who: "development manager", text: developmentManagerContract},
	} {
		for _, want := range []string{"done-condition", "governed path", protectedpath.GrantMarker, "refuses to start"} {
			if !strings.Contains(contract.text, want) {
				t.Fatalf("the %s contract never says %q", contract.who, want)
			}
		}
	}
}

// yoyodyne-ifd.330's admission replayed as the product manager made it at turn
// 468: the architect's design work by its title and its done-means, and no
// executor. It went into the queue as a developer run and was handed to one. Now
// the creation is refused with the marker named, and the same creation carrying
// the marker is admitted with it — the condition it states being exactly what
// the architect's conversation does.
func TestConversationShapedWorkIsAdmittedOnlyWithItsExecutorNamed(t *testing.T) {
	t.Parallel()

	const item330 = `"title":"The architect designs side conversations with merge-back","description":"Operator capability direction, 2026-09-07, design routed to the architect as directed. Done means the design is recorded in the governed documents - the stream shape, the merge write, the action-authority answer, the config knob - and implementation items can cite it.","goal":"Run development nearly autonomously.","reason":"operator-directed with the authority question reserved to the architect"`
	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the design item.", `{"action":"create","kind":"feature",`+item330+`}`)},
		{SessionID: "session-1", FinalText: trackerReply("Refused; marking it as the architect's.", `{"action":"create","kind":"feature",`+item330+`,"executor":"conversation:architect"}`)},
		{SessionID: "session-1", FinalText: "Admitted as the architect's."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals("Run development nearly autonomously.")
	options.ArtifactHomes = testHomes()
	session := openTestSession(t, options)

	// The refusal is put back to the role on the same send, and its correction
	// carries the marker: one send, two actions, the second the admission.
	reply, err := session.Send(context.Background(), "Admit the side-conversation design for the architect.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the unmarked creation refused and then corrected", reply.Actions)
	}
	for _, want := range []string{"the design is recorded in the governed documents", `executor "conversation:architect"`, "never selected for a developer run"} {
		if !strings.Contains(reply.Actions[0].Failure, want) {
			t.Fatalf("refusal %q never says %q", reply.Actions[0].Failure, want)
		}
	}
	if !reply.Actions[1].Applied {
		t.Fatalf("the marked creation was refused: %q", reply.Actions[1].Failure)
	}
	if len(tracker.created) != 1 || tracker.created[0].Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("created = %#v, want the item admitted once, carrying the architect's conversation", tracker.created)
	}
}

// An item already in the queue in 330's shape is brought under the guard by an
// update that sets the executor, and that update is judged as the item will
// then read: the same description that was refused unmarked is right once the
// item says the architect carries it.
func TestMarkingAnAdmittedItemIsJudgedWithTheMarkerItSets(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.330": {
			ID: "yoyodyne-ifd.330", Title: "The architect designs side conversations with merge-back", Status: "open",
			Description: "Done means the design is recorded in the governed documents and implementation items can cite it.",
		},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Marking it and tightening the description.",
			`{"action":"update","id":"yoyodyne-ifd.330","executor":"conversation:architect","description":"Done means the design is recorded in the management-and-supervision design and implementation items can cite it.","reason":"it was admitted as a run's by mistake"}`)},
		{SessionID: "session-1", FinalText: "Marked."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.ArtifactHomes = testHomes()
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Mark 330 as the architect's.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the marking update applied", reply.Actions)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].change.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("updates = %#v, want the executor written", tracker.updates)
	}
}
