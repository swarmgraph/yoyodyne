package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const refusedBlockHeading = "# A block in your last reply was refused"

// A block the harness cannot read loses only that block: the tracker action in
// the same reply is carried out, the loss is on the reply and in the event log,
// and the role is told of it once, in the round this message hands back.
func TestAnUnreadableMemoryBlockLosesOnlyThatBlock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.138": {ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: "open"},
	}}
	answer := trackerReply("Noting it and remembering it.",
		`{"action":"update","id":"yoyodyne-ifd.138","note":"the brief is promoted","reason":"the architect carried it out"}`) +
		"\n" + memoryBlock(`{"memories": not json`)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: answer},
		{SessionID: "session-1", FinalText: "Understood."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Store = newTestStore(t, root)
	options.Memories = memories
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Record it.")
	if err != nil {
		t.Fatalf("Send() error = %v, want only the memory block refused", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("actions = %#v, updates = %#v, want the tracker action carried out", reply.Actions, tracker.updates)
	}
	if len(reply.RefusedBlocks) != 1 || !strings.Contains(reply.RefusedBlocks[0], "memory block was refused and nothing in it was carried out") {
		t.Fatalf("refused blocks = %q, want one naming the memory block", reply.RefusedBlocks)
	}
	if len(reply.Memories) != 0 {
		t.Fatalf("memories = %#v, want nothing remembered from an unreadable block", reply.Memories)
	}
	payload := onlyEventPayload(t, root, session, execution.EventReplyBlockRefused)
	if !strings.Contains(payload, `"block":"memory"`) {
		t.Fatalf("refusal event = %s, want it to name the memory block", payload)
	}

	// The tracker's result sent the message on another round, which carries the
	// refusal with it; nothing is left over for the turn after.
	if len(provider.requests) != 2 || strings.Count(provider.requests[1].Prompt, refusedBlockHeading) != 1 {
		t.Fatalf("requests = %d, want the continuation to carry the refusal once", len(provider.requests))
	}
	if !strings.Contains(provider.requests[1].Prompt, "do not describe it as done") {
		t.Fatalf("continuation = %s", provider.requests[1].Prompt)
	}
	if session.state.PendingTrackerResults != "" {
		t.Fatalf("results still pending: %q", session.state.PendingTrackerResults)
	}
}

// Where the round hands nothing back, the refusal waits for the role's next turn
// and reaches it there once.
func TestARefusedBlockWaitsForTheNextTurnWhenNothingIsHandedBack(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Remembering.\n\n" + memoryBlock(`{"memories":[{"action":"unknown"}]}`)},
		{SessionID: "session-1", FinalText: "Sent again."},
	}}
	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Store = newTestStore(t, root)
	options.Memories = memories
	session := openTestSession(t, options)

	if reply, err := session.Send(context.Background(), "Remember."); err != nil || len(reply.RefusedBlocks) != 1 {
		t.Fatalf("Send() = %q, %v, want the memory block refused alone", reply.RefusedBlocks, err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("requests = %d, want the message to end", len(provider.requests))
	}
	if _, err := session.Send(context.Background(), "Well?"); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if strings.Count(provider.requests[1].Prompt, refusedBlockHeading) != 1 {
		t.Fatalf("next prompt = %s, want the refusal once", provider.requests[1].Prompt)
	}
	if session.state.PendingTrackerResults != "" {
		t.Fatalf("results still pending: %q", session.state.PendingTrackerResults)
	}
}

// A role with no memory is refused whole for an unreadable memory block, as it is
// for a readable one: nothing in the reply is carried out.
func TestAnUnreadableBlockFromARoleWithoutAuthorityRefusesTheWholeReply(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			provider := &fakeBackend{results: []backendapi.RunResult{{
				SessionID: "session-1",
				FinalText: "Noted.\n\n" + memoryBlock(`{"memories": not json`),
			}}}
			options := testOptions(t, provider)
			options.Role = role
			options.Agent = string(role)
			options.Store = newTestStore(t, root)
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "Remember this.")
			var refused *AuthorityError
			if !errors.As(err, &refused) {
				t.Fatalf("Send() error = %v, want an AuthorityError", err)
			}
			if len(reply.RefusedBlocks) != 0 {
				t.Fatalf("refused blocks = %q, want the whole reply refused instead", reply.RefusedBlocks)
			}
			if events := eventsOfType(t, root, session, execution.EventReplyBlockRefused); len(events) != 0 {
				t.Fatalf("%d block refusal event(s), want none: the reply was refused whole", len(events))
			}
		})
	}
}

// A proposal under a goal nothing states no longer ends the message: it is
// refused as a block while the memory beside it is carried out.
func TestAProposalUnderAnUnstatedGoalIsRefusedAloneBesideAMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	tracker := &fakeTracker{}
	answer := proposalReply("Two things.",
		`{"kind":"feature","title":"A plugin marketplace","description":"Third-party extensions.","rationale":"You raised it.","goal":"Grow the ecosystem."}`) +
		"\n" + memoryBlock(`{"memories":[{"action":"remember","memory":"marketplace","text":"The operator raised a marketplace."}]}`)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: answer},
		{SessionID: "session-1", FinalText: "Understood."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Memories = memories
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What about a marketplace?")
	if err != nil {
		t.Fatalf("Send() error = %v, want the proposal refused alone", err)
	}
	if len(reply.RefusedBlocks) != 1 || !strings.Contains(reply.RefusedBlocks[0], "proposal block was refused") || !strings.Contains(reply.RefusedBlocks[0], "Grow the ecosystem.") {
		t.Fatalf("refused blocks = %q, want the proposal refused for its goal", reply.RefusedBlocks)
	}
	if len(reply.Proposals) != 0 || len(reply.Admitted) != 0 || len(tracker.created) != 0 {
		t.Fatalf("proposals = %#v, admitted = %#v, created = %#v, want nothing from the refused block", reply.Proposals, reply.Admitted, tracker.created)
	}
	if len(reply.Memories) != 1 || !reply.Memories[0].Recorded {
		t.Fatalf("memories = %#v, want the memory beside it carried out", reply.Memories)
	}
	onlyEventPayload(t, root, session, execution.EventReplyBlockRefused)

	if _, err := session.Send(context.Background(), "Well?"); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if strings.Count(provider.requests[1].Prompt, refusedBlockHeading) != 1 {
		t.Fatalf("next prompt = %s, want the refusal once", provider.requests[1].Prompt)
	}
}

// An unreadable tracker block loses only itself: the memory beside it is carried
// out, the refusal is handed back within the same message together with what the
// rest of the reply came to, and the corrected actions in the answer that follows
// are applied.
func TestAnUnreadableTrackerBlockIsHandedBackBesideTheRestOfTheReply(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.138": {ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: "open"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Settling it and remembering it.",
			`{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`) +
			"\n" + memoryBlock(`{"memories":[{"action":"remember","memory":"settling","text":"Reports are settled by identifier."}]}`)},
		{SessionID: "session-1", FinalText: trackerReply("Again, correctly.",
			`{"action":"update","id":"yoyodyne-ifd.138","note":"the brief is promoted","reason":"the architect carried it out"}`)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Store = newTestStore(t, root)
	options.Memories = memories
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Settle it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Memories) != 1 || !reply.Memories[0].Recorded {
		t.Fatalf("memories = %#v, want the memory beside the refused tracker block carried out", reply.Memories)
	}
	if len(reply.HandedBack) != 1 || len(provider.requests) != 3 {
		t.Fatalf("handed back %#v over %d call(s), want the refusal handed back within the message", reply.HandedBack, len(provider.requests))
	}
	handed := provider.requests[1].Prompt
	for _, want := range []string{"# The tracker block in your last reply was refused", "Everything else your last reply asked the harness for was carried out", "only the tracker actions have to be issued again"} {
		if strings.Count(handed, want) != 1 {
			t.Fatalf("hand-back prompt lacks %q exactly once:\n%s", want, handed)
		}
	}
	if !strings.Contains(handed, "Reports are settled by identifier") && !strings.Contains(handed, "settling") {
		t.Fatalf("hand-back prompt does not carry the memory's result:\n%s", handed)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v, want the corrected action applied", tracker.updates)
	}
	if session.state.PendingTrackerResults != "" {
		t.Fatalf("results still pending: %q", session.state.PendingTrackerResults)
	}
}

// A refusal on the last round is not handed back, but the rest of its reply is
// still carried out, the message still fails with the refusal, and what the round
// owes the role waits with the refusal for its next turn.
func TestAnUnhandedTrackerRefusalStillCarriesOutTheRestOfTheReply(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	results := make([]backendapi.RunResult, 0, maxTrackerRounds)
	for round := 1; round < maxTrackerRounds; round++ {
		results = append(results, backendapi.RunResult{SessionID: "session-1",
			FinalText: trackerReply("Reading.", `{"action":"read","id":"yoyodyne-ifd.404"}`)})
	}
	results = append(results, backendapi.RunResult{SessionID: "session-1",
		FinalText: trackerReply("Settling the report.", `{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`) +
			"\n" + memoryBlock(`{"memories":[{"action":"remember","memory":"settling","text":"Reports are settled by identifier."}]}`)})
	backend := &fakeBackend{results: results}
	options := testOptions(t, backend)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Store = newTestStore(t, root)
	options.Memories = memories
	options.Tracker = &fakeTracker{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "look into it and settle the report")
	var unreadable *TrackerError
	if !errors.As(err, &unreadable) {
		t.Fatalf("Send() error = %v, want the last round's refusal to end the message", err)
	}
	if len(reply.HandedBack) != 0 || len(backend.requests) != maxTrackerRounds {
		t.Fatalf("handed back %#v over %d call(s), want nothing handed back past the last round", reply.HandedBack, len(backend.requests))
	}
	if len(reply.Memories) != 1 || !reply.Memories[0].Recorded {
		t.Fatalf("memories = %#v, want the memory beside the refused block carried out", reply.Memories)
	}
	recorded := loadTestConversation(t, root, options)
	if recorded.RefusedBlock == nil || !strings.Contains(recorded.PendingTrackerResults, unreadable.Error()) {
		t.Fatalf("refused block = %#v, pending = %q, want the refusal waiting for the next turn", recorded.RefusedBlock, recorded.PendingTrackerResults)
	}
	if !strings.Contains(recorded.PendingTrackerResults, "settling") {
		t.Fatalf("pending = %q, want the memory's result written down too", recorded.PendingTrackerResults)
	}
}
