package cli

// The Slack door as a client of the one durable conversation.
//
// What is tested here is the property the whole item exists for: a conversation
// begun in `yoyo chat` carries on in Slack and back, without losing state,
// because both are clients of one durable record rather than two conversations
// that would have to be reconciled. The two clients are two sessions over one
// state root, which is what two processes talking to one recorded conversation
// actually is.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
)

// A question asked at a terminal, answered from a channel, and carried back to
// the terminal: one conversation throughout, with the turns accumulating on it
// and the provider session resumed rather than restarted.
func TestAConversationBegunAtTheTerminalCarriesOnInSlackAndBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}

	// The terminal opens it and takes the first turn.
	terminal := openTestChatSession(t, root, &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-1", FinalText: "The brief names no audience."},
	}, tracker)
	began := terminal.Evidence().ConversationID
	if terminal.Resumed() {
		t.Fatalf("the first client resumed something, and there was nothing to resume")
	}
	if _, err := terminal.Send(context.Background(), "what is missing from the brief?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Slack picks up the same conversation, from a different process, and takes
	// the next turn through the door's own dispatch.
	fromSlack := openTestChatSession(t, root, &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-2", FinalText: "Who it is for, and what finished looks like."},
	}, tracker)
	if !fromSlack.Resumed() {
		t.Fatalf("the Slack client started a new conversation instead of continuing the terminal's")
	}
	answer, err := sayToConversation(context.Background(), fromSlack, "say more about the audience", discardLog)
	if err != nil {
		t.Fatalf("sayToConversation() error = %v", err)
	}
	if answer.ConversationID != began {
		t.Fatalf("conversation = %q, want the one the terminal began, %q", answer.ConversationID, began)
	}
	if !strings.Contains(answer.Text, "Who it is for") {
		t.Fatalf("answer = %q, want what the product manager said", answer.Text)
	}
	if answer.Turns != 2 {
		t.Fatalf("turns = %d, want the terminal's turn and this one on the same conversation", answer.Turns)
	}
	if answer.Harness {
		t.Fatalf("answer = %#v, want something the product manager said rather than the harness's own", answer)
	}

	// And back to the terminal, which finds the Slack turn already on the record
	// rather than a conversation that forked while it was away.
	returned := openTestChatSession(t, root, &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-3", FinalText: "Understood."},
	}, tracker)
	evidence := returned.Evidence()
	if !returned.Resumed() || evidence.ConversationID != began {
		t.Fatalf("evidence = %#v, want the terminal back in the conversation it began, %q", evidence, began)
	}
	if evidence.Turns != 2 {
		t.Fatalf("turns = %d, want the turn taken from Slack to be on the record the terminal reads", evidence.Turns)
	}
	// The provider session the Slack turn recorded is what the terminal resumes,
	// which is the whole of why it is one conversation rather than two.
	if evidence.SessionID != "session-2" {
		t.Fatalf("session = %q, want the one the turn taken from Slack recorded", evidence.SessionID)
	}
}

// A proposal made in one client and approved from the other. It is the sharpest
// case of one conversation with two clients — the operator's "y" has to decide
// the proposal rather than be said to the product manager as speech — and the
// door dispatches it exactly as `yoyo chat --message` does, because a
// conversation whose two clients dispatched differently would be one where the
// same word did different things.
func TestAProposalMadeAtTheTerminalIsApprovedFromSlack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}

	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	terminal := openTestChatSession(t, root, proposing, tracker)
	if _, err := terminal.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(terminal.Proposals()) != 1 {
		t.Fatalf("proposals = %#v, want the one the turn proposed", terminal.Proposals())
	}

	// The approval arrives through the Slack door, in another process.
	answering := &recordingChatBackend{}
	fromSlack := openTestChatSession(t, root, answering, tracker)
	answer, err := sayToConversation(context.Background(), fromSlack, "y", discardLog)
	if err != nil {
		t.Fatalf("sayToConversation() error = %v", err)
	}
	if !answer.Harness {
		t.Fatalf("answer = %#v, want a decision to read as the harness's own rather than as speech", answer)
	}
	if !strings.Contains(answer.Text, "You decided 1 proposal(s)") {
		t.Fatalf("answer = %q, want what the decision did", answer.Text)
	}
	if answering.turns != 0 {
		t.Fatalf("the approval was said to the product manager %d time(s); it is a decision the harness carries out", answering.turns)
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("creations = %#v, want the approved proposal to have reached the queue", tracker.creations)
	}
	// And the proposal is no longer waiting on anybody, in either client.
	if pending := fromSlack.Proposals(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want the decided proposal off the table", pending)
	}
	back := openTestChatSession(t, root, &recordingChatBackend{}, tracker)
	if pending := back.Proposals(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want the terminal to find the proposal decided rather than still waiting", pending)
	}
}

// A question the product manager stopped on at the terminal is answered from
// Slack by name, and a bare "yes" with that question and a proposal both waiting
// approves nothing: the door dispatches exactly as `yoyo chat --message` does,
// so the same word is refused with the list in both clients rather than
// approving the proposal in one of them.
func TestAQuestionAskedAtTheTerminalIsAnsweredFromSlackByName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}

	asking := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: concernAndProposalReply}}
	terminal := openTestChatSession(t, root, asking, tracker)
	if _, err := terminal.Send(context.Background(), "should we let agents merge?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(terminal.Concerns()) != 1 || len(terminal.Proposals()) != 1 {
		t.Fatalf("concerns = %#v and proposals = %#v, want one of each waiting", terminal.Concerns(), terminal.Proposals())
	}

	answering := &recordingChatBackend{}
	fromSlack := openTestChatSession(t, root, answering, tracker)
	answer, err := sayToConversation(context.Background(), fromSlack, "yes", discardLog)
	if err == nil || !answer.Harness {
		t.Fatalf("sayToConversation(\"yes\") = %#v, %v; want it refused as the harness's own answer", answer, err)
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("creations = %#v; a yes meant for the question approved the proposal", tracker.creations)
	}

	answer, err = sayToConversation(context.Background(), fromSlack, "answer c1.1 no, the goal stands", discardLog)
	if err != nil {
		t.Fatalf("sayToConversation() error = %v", err)
	}
	if !answer.Harness || !strings.Contains(answer.Text, "You answered 1 question(s)") || !strings.Contains(answer.Text, "no, the goal stands") {
		t.Fatalf("answer = %#v, want what the answer did, as the harness's own", answer)
	}
	if answering.turns != 0 {
		t.Fatalf("the answer was said to the product manager %d time(s); it is carried out by the harness", answering.turns)
	}
	if len(fromSlack.Concerns()) != 0 || len(fromSlack.Proposals()) != 1 || len(tracker.creations) != 0 {
		t.Fatalf("concerns = %#v, proposals = %#v, creations = %#v; want the question answered and the proposal untouched",
			fromSlack.Concerns(), fromSlack.Proposals(), tracker.creations)
	}
	back := openTestChatSession(t, root, &recordingChatBackend{}, tracker)
	if open := back.Concerns(); len(open) != 0 {
		t.Fatalf("concerns = %#v, want the terminal to find the question answered rather than still waiting", open)
	}
}

// concernAndProposalReply stops on one question and proposes one item in the
// same turn, which is the shape a misdirected "yes" needs.
const concernAndProposalReply = `One of these is yours to decide.

` + "```yoyodyne-concern" + `
{"concerns":[{"kind":"conflict","subject":"Let an agent merge its own work","goal":"No agent pushes or merges.","detail":"It is the thing that goal exists to prevent.","question":"Do you want that goal changed?"}]}
` + "```" + `

` + "```yoyodyne-proposal" + `
{"items":[{"kind":"feature","title":"Pause on a usage limit","description":"Wait for the window and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}]}
` + "```" + `
`

// Everything a turn said travels back even when the turn then failed, because a
// partial answer is worth reading and the door posts it ahead of the account of
// the failure.
func TestAFailedTurnStillCarriesBackWhatItManagedToSay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// A provider with no answer to give fails the turn, which is what a refusal
	// for want of capacity looks like from here.
	session := openTestChatSession(t, root, &recordingChatBackend{}, &recordingChatTracker{})
	answer, err := sayToConversation(context.Background(), session, "what should we build next?", discardLog)
	if err == nil {
		t.Fatalf("sayToConversation() error = nil, want the refused turn reported")
	}
	if answer.ConversationID == "" {
		t.Fatalf("answer = %#v, want the conversation named even where the turn failed", answer)
	}
}

// A command typed at the app is refused rather than said to the product manager.
//
// It reaches this client with its slash intact — the message arrives
// mention-first, so `@yoyodyne /backlog` has the mention stripped and is a
// command again — and saying one to a role that cannot carry it out is the
// defect `yoyo chat --message` already had once: a confused answer bought with a
// turn the operator paid for.
// It is settled before the conversation is opened, which is the other half of
// the rule. A turn is exclusive, so a command decided after opening would be
// answered "mid-turn with another client" whenever a `yoyo chat` is answering
// — the wrong answer, reached by making a command contend for the operator's
// own lease to get it.
func TestACommandFromSlackIsRefusedRatherThanSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"/backlog", "/work yoyodyne-ifd.70", "/refresh", "  /stop  "} {
		answer, refused := refuseCommand(command)
		if !refused {
			t.Fatalf("refuseCommand(%q) = false, want a command refused before anything is opened", command)
		}
		if !answer.Harness {
			t.Fatalf("%q = %#v, want the refusal to read as the harness's own rather than as something said", command, answer)
		}
		if !strings.Contains(answer.Text, "commands are not carried out from here") {
			t.Fatalf("%q answered %q, want it refused with where to type it instead", command, answer.Text)
		}
		// Nothing was opened, so there is no conversation to name. Reporting one
		// would be inventing the only evidence this door carries.
		if answer.ConversationID != "" {
			t.Fatalf("%q = %#v, want no conversation named by an answer that opened none", command, answer)
		}
	}
	// And what is not a command is left for the conversation to answer, so the
	// refusal above cannot swallow ordinary speech.
	for _, said := range []string{"what is missing from the brief?", "y", "put the installer epic first", ""} {
		if _, refused := refuseCommand(said); refused {
			t.Fatalf("refuseCommand(%q) = true, want everything that is not a command left alone", said)
		}
	}
}

// And the interception is a slash rather than a mood: a sentence that merely
// mentions a command still reaches the product manager, which is the other half
// of the rule the terminal follows.
func TestAMessageThatOnlyTalksAboutACommandStillReachesTheProductManager(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-1", FinalText: "It starts the run you name."},
	}
	session := openTestChatSession(t, t.TempDir(), provider, &recordingChatTracker{})
	answer, err := sayToConversation(context.Background(), session, "what does /work do?", discardLog)
	if err != nil {
		t.Fatalf("sayToConversation() error = %v", err)
	}
	if provider.turns != 1 {
		t.Fatalf("the product manager was asked %d time(s), want the question put to it", provider.turns)
	}
	if answer.Harness || !strings.Contains(answer.Text, "It starts the run you name.") {
		t.Fatalf("answer = %#v, want what the product manager said", answer)
	}
}

// discardLog is the sink's log for a test that is not reading it.
func discardLog(string, ...any) {}
