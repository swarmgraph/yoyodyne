package chat

// What a single message does to a question the product manager stopped on. A
// concern raised by one `yoyo chat --message` is answered by another, so the
// question has to be in the record rather than in memory — and once a message
// can decide a proposal, an answer meant for a question must reach the question
// rather than whatever proposal happens to be undecided beside it.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// decisions unwraps what a message decided for the tests written before a
// message could also answer a question, so what they assert about proposals
// reads exactly as it did.
func decisions(decided Decided, settled bool, err error) ([]DecisionOutcome, bool, error) {
	return decided.Decisions, settled, err
}

// oneConcernTurn is a reply that stops on one question and proposes nothing.
const oneConcernTurn = `I would not put this in the queue without asking.

` + "```yoyodyne-concern" + `
{"concerns":[{"kind":"unplaceable","subject":"A plugin marketplace","detail":"No goal covers third-party extensions.","question":"Which of these should it be?","options":["Write a goal for extensions","Retire the work"]}]}
` + "```" + `
`

// concernAndProposalTurn is a reply that stops on one question and proposes one
// item in the same breath, which is the shape the defect needs: a "yes" meant
// for the question, arriving at a conversation with a proposal on the table.
const concernAndProposalTurn = `One of these is yours to decide, and the other I can propose.

` + "```yoyodyne-concern" + `
{"concerns":[{"kind":"conflict","subject":"Let an agent merge its own work","goal":"No agent pushes or merges.","detail":"It is the thing that goal exists to prevent.","question":"Do you want that goal changed?"}]}
` + "```" + `

` + "```yoyodyne-proposal" + `
{"items":[{"kind":"feature","title":"Pause on a usage limit","description":"Wait for the window and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}]}
` + "```" + `
`

// The defect, replayed: an operator answering the product manager's question
// with "yes" from the command line, while a proposal is undecided, approved the
// proposal — the approval was real, recorded, and created the item. Now the
// message names nothing while two things are waiting, so it is refused with the
// list and applied to neither.
func TestYesWithAConcernAndAProposalPendingApprovesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	asking := perItemApprovalOptions(t, root, tracker, concernAndProposalTurn)
	asked := openTestSession(t, asking)
	reply, err := asked.Send(context.Background(), "should we let agents merge?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Concerns) != 1 || len(reply.Proposals) != 1 {
		t.Fatalf("reply raised %d concern(s) and proposed %d item(s), want one of each", len(reply.Concerns), len(reply.Proposals))
	}

	for _, answer := range []string{"yes", "y", "no", "n"} {
		answering := perItemApprovalOptions(t, root, tracker, "should never be asked")
		resumed := openTestSession(t, answering)
		decided, settled, err := resumed.Decide(context.Background(), answer)
		if !settled {
			t.Fatalf("Decide(%q) passed the answer on as speech with a question and a proposal both waiting", answer)
		}
		if err == nil {
			t.Fatalf("Decide(%q) = %#v, want it refused rather than applied to either", answer, decided)
		}
		// The refusal is the list: both things waiting, each named by the
		// identifier the next message answers or decides it with.
		for _, required := range []string{"c1.1", "answer c1.1", "1.1", "approve 1.1", "Let an agent merge its own work", "Pause on a usage limit"} {
			if !strings.Contains(err.Error(), required) {
				t.Fatalf("Decide(%q) error = %v, want it to name %q", answer, err, required)
			}
		}
		if len(decided.Decisions) != 0 || len(decided.Answers) != 0 {
			t.Fatalf("Decide(%q) = %#v, want nothing decided and nothing answered", answer, decided)
		}
		if len(resumed.Concerns()) != 1 || len(resumed.Proposals()) != 1 {
			t.Fatalf("Decide(%q) left %d concern(s) and %d proposal(s) waiting, want both exactly where they were",
				answer, len(resumed.Concerns()), len(resumed.Proposals()))
		}
		if len(answering.Backend.(*fakeBackend).requests) != 0 {
			t.Fatalf("Decide(%q) spent a turn on the product manager", answer)
		}
	}
	// Nothing was approved: the tracker never heard from any of it, and the
	// record says both are still waiting.
	if len(tracker.created) != 0 {
		t.Fatalf("tracker created %#v; an answer to a question approved a proposal", tracker.created)
	}
	recorded := countEvents(t, root, asked)
	if recorded[execution.EventProposalApproved] != 0 || recorded[execution.EventConcernAnswered] != 0 {
		t.Fatalf("events = %v, want no approval and no answer recorded", recorded)
	}
	later := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	if len(later.Concerns()) != 1 || len(later.Proposals()) != 1 {
		t.Fatalf("a later process found %d concern(s) and %d proposal(s), want both still on the record",
			len(later.Concerns()), len(later.Proposals()))
	}
}

// Naming what the message answers is what lets it through with both waiting:
// the concern by its identifier answers the concern and leaves the proposal
// untouched, and the proposal by its identifier decides the proposal and leaves
// the question open.
func TestAMessageThatNamesWhatItAnswersReachesOnlyThat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	asked := openTestSession(t, perItemApprovalOptions(t, root, tracker, concernAndProposalTurn))
	if _, err := asked.Send(context.Background(), "should we let agents merge?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	answering := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	decided, settled, err := answering.Decide(context.Background(), "answer c1.1 no, the goal stands")
	if !settled || err != nil {
		t.Fatalf("Decide() = %#v, %t, %v; want the named concern answered", decided, settled, err)
	}
	if len(decided.Answers) != 1 || decided.Answers[0].ConcernID != "c1.1" || decided.Answers[0].Answer != "no, the goal stands" {
		t.Fatalf("answers = %#v, want the operator's words recorded against c1.1", decided.Answers)
	}
	if len(decided.Decisions) != 0 || len(tracker.created) != 0 {
		t.Fatalf("answering the question decided %#v and created %#v", decided.Decisions, tracker.created)
	}
	if len(answering.Concerns()) != 0 || len(answering.Proposals()) != 1 {
		t.Fatalf("%d concern(s) and %d proposal(s) waiting, want the question answered and the proposal untouched",
			len(answering.Concerns()), len(answering.Proposals()))
	}
	rendered := decided.Answers[0].Render()
	if !strings.Contains(rendered, "[c1.1] answered") || !strings.Contains(rendered, "no, the goal stands") {
		t.Fatalf("rendered = %q, want the answer said back", rendered)
	}

	deciding := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	if pending := deciding.Concerns(); len(pending) != 0 {
		t.Fatalf("an answered concern came back as waiting: %#v", pending)
	}
	decided, settled, err = deciding.Decide(context.Background(), "approve 1.1")
	if !settled || err != nil || len(decided.Decisions) != 1 || !decided.Decisions[0].Approved {
		t.Fatalf("Decide() = %#v, %t, %v; want the named proposal approved", decided, settled, err)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v, want the approved item", tracker.created)
	}

	// The proposal can be named first, too, with the question still open.
	root2 := t.TempDir()
	tracker2 := &fakeTracker{}
	asked2 := openTestSession(t, perItemApprovalOptions(t, root2, tracker2, concernAndProposalTurn))
	if _, err := asked2.Send(context.Background(), "should we let agents merge?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	deciding2 := openTestSession(t, perItemApprovalOptions(t, root2, tracker2, "nothing to say"))
	decided, settled, err = deciding2.Decide(context.Background(), "decline 1.1 not this quarter")
	if !settled || err != nil || len(decided.Decisions) != 1 || decided.Decisions[0].Approved {
		t.Fatalf("Decide() = %#v, %t, %v; want the named proposal declined", decided, settled, err)
	}
	if len(deciding2.Concerns()) != 1 {
		t.Fatalf("deciding the proposal answered the question: %#v", deciding2.Concerns())
	}
}

// A question raised by one process is answered by another. The concern comes
// back from the record with the answers it offered, an answer picked by number
// is recorded in the words it was offered in, and the product manager hears the
// answer on its next turn — which is what stopping to ask was for.
func TestAConcernOutlivesTheProcessThatRaisedItSoALaterMessageCanAnswerIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	asked := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneConcernTurn))
	reply, err := asked.Send(context.Background(), "what should we do about the marketplace?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Concerns) != 1 {
		t.Fatalf("reply raised %d concern(s), want one to answer", len(reply.Concerns))
	}

	answering := perItemApprovalOptions(t, root, tracker, "Retired, then.")
	resumed := openTestSession(t, answering)
	open := resumed.Concerns()
	if len(open) != 1 || open[0].ID != "c1.1" {
		t.Fatalf("concerns came back as %#v, want the unanswered one named c1.1", open)
	}
	if open[0].Concern.Question != "Which of these should it be?" || len(open[0].Concern.Options) != 2 {
		t.Fatalf("concern came back as %#v, want what the operator was asked, answers included", open[0].Concern)
	}

	decided, settled, err := resumed.Decide(context.Background(), "answer c1.1 2")
	if !settled || err != nil {
		t.Fatalf("Decide() = %#v, %t, %v; want the answer carried out", decided, settled, err)
	}
	if len(decided.Answers) != 1 || decided.Answers[0].Answer != "Retire the work" {
		t.Fatalf("answers = %#v, want the chosen answer in the words it was offered in", decided.Answers)
	}
	if len(resumed.Concerns()) != 0 {
		t.Fatalf("%d concern(s) still waiting after being answered", len(resumed.Concerns()))
	}
	payload := onlyEventPayload(t, root, resumed, execution.EventConcernAnswered)
	if !strings.Contains(payload, `"answer":"Retire the work"`) {
		t.Fatalf("answer event = %s", payload)
	}
	// The process that answered exits without taking a turn, which is what a
	// one-shot message always does. The record it left has to carry the answer
	// itself and not only the question's absence: what the next process keeps
	// of the concerns is the unanswered ones, so an answer conveyed only through
	// this process's memory of the concern would be gone by the next turn, and
	// the product manager would find its question cleared and never hear what
	// was decided.
	if answering.Backend.(*fakeBackend).requests != nil {
		t.Fatalf("answering the question spent a turn on the product manager")
	}
	speaking := perItemApprovalOptions(t, root, tracker, "Retired, then.")
	third := openTestSession(t, speaking)
	if open := third.Concerns(); len(open) != 0 {
		t.Fatalf("an answered concern came back as waiting: %#v", open)
	}
	if _, err := third.Send(context.Background(), "so?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	told := speaking.Backend.(*fakeBackend).requests[0].Prompt
	if !strings.Contains(told, "answered concern c1.1") || !strings.Contains(told, "Retire the work") {
		t.Fatalf("the next turn, taken by a different process, was told %q; want the answer to its question", told)
	}
	// Told once: a fourth process finds the account delivered rather than
	// delivering it again.
	fourth := perItemApprovalOptions(t, root, tracker, "nothing to say")
	again := openTestSession(t, fourth)
	if _, err := again.Send(context.Background(), "and?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if retold := fourth.Backend.(*fakeBackend).requests[0].Prompt; strings.Contains(retold, "answered concern c1.1") {
		t.Fatalf("the answer was delivered a second time: %q", retold)
	}
	// And a second answer to the same question is refused out loud rather than
	// said to the agent.
	decided, settled, err = again.Decide(context.Background(), "answer c1.1 the other one")
	if !settled || err == nil || !strings.Contains(err.Error(), "c1.1") {
		t.Fatalf("Decide() = %#v, %t, %v; want the answered concern refused by name", decided, settled, err)
	}
}

// Where the one question is all the conversation is waiting on, a bare answer
// word names it — the way a bare yes names the only proposal on the table.
func TestABareAnswerReachesTheOneQuestionThatIsAllThatIsWaiting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	asked := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneConcernTurn))
	if _, err := asked.Send(context.Background(), "what should we do about the marketplace?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	decided, settled, err := resumed.Decide(context.Background(), "no")
	if !settled || err != nil || len(decided.Answers) != 1 || decided.Answers[0].Answer != "no" {
		t.Fatalf("Decide() = %#v, %t, %v; want the bare word to answer the one question", decided, settled, err)
	}
	if len(resumed.Concerns()) != 0 || len(tracker.created) != 0 {
		t.Fatalf("%d concern(s) waiting and %d item(s) created; want the question answered and nothing approved",
			len(resumed.Concerns()), len(tracker.created))
	}

	// A proposal's grammar with no proposal to read it against is refused rather
	// than applied to the question: "decline all" is about proposals.
	root2 := t.TempDir()
	asked2 := openTestSession(t, perItemApprovalOptions(t, root2, tracker, oneConcernTurn))
	if _, err := asked2.Send(context.Background(), "what should we do about the marketplace?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	resumed2 := openTestSession(t, perItemApprovalOptions(t, root2, tracker, "nothing to say"))
	decided, settled, err = resumed2.Decide(context.Background(), "decline all")
	if !settled || err == nil || !strings.Contains(err.Error(), "answer c1.1") {
		t.Fatalf("Decide() = %#v, %t, %v; want it refused with how to name the question", decided, settled, err)
	}
	if len(resumed2.Concerns()) != 1 {
		t.Fatalf("a refused message answered the question: %#v", resumed2.Concerns())
	}
	// And speech is still speech: a sentence goes to the product manager and
	// leaves the question open, exactly as it does with a proposal waiting.
	decided, settled, err = resumed2.Decide(context.Background(), "no, let's look at the resolver instead")
	if settled || err != nil {
		t.Fatalf("Decide() = %#v, %t, %v; want a sentence said to the product manager", decided, settled, err)
	}
}

// An answer that names a concern needs an answer in it, and one that names a
// concern nobody is waiting on is refused out loud rather than passed on as a
// sentence: the operator answered something whose settlement they did not see.
func TestAnAnswerNamingAConcernIsRefusedWhereItCannotBeCarriedOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	asked := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneConcernTurn))
	if _, err := asked.Send(context.Background(), "what should we do about the marketplace?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	for message, wanted := range map[string]string{
		"answer c1.1":            "needs an answer",
		"c1.1":                   "needs an answer",
		"answer c9.9 not this":   "c9.9",
		"c2.1 write the goal up": "c2.1",
	} {
		decided, settled, err := resumed.Decide(context.Background(), message)
		if !settled {
			t.Fatalf("Decide(%q) passed an answer naming a concern on as speech", message)
		}
		if err == nil || !strings.Contains(err.Error(), wanted) {
			t.Fatalf("Decide(%q) = %#v, %v; want it refused saying %q", message, decided, err, wanted)
		}
	}
	if len(resumed.Concerns()) != 1 {
		t.Fatalf("a refused answer settled the question: %#v", resumed.Concerns())
	}
}

// The shapes that answer a concern by name, and the ones that do not. The
// identifier is the thing nobody types into ordinary speech, so it is the whole
// of what tells an answer from a sentence that mentions a question.
func TestTheShapesThatNameAConcern(t *testing.T) {
	t.Parallel()

	for message, wanted := range map[string][2]string{
		"answer c3.1 the goal stands": {"c3.1", "the goal stands"},
		"Answer c3.1 2":               {"c3.1", "2"},
		"c3.1 write it up":            {"c3.1", "write it up"},
		"  c12.4   yes ":              {"c12.4", "yes"},
		"c3.1":                        {"c3.1", ""},
	} {
		id, answer, names := namesAConcern(message)
		if !names || id != wanted[0] || answer != wanted[1] {
			t.Errorf("namesAConcern(%q) = %q, %q, %t; want %q, %q", message, id, answer, names, wanted[0], wanted[1])
		}
	}
	for _, message := range []string{
		"answer",
		"answer 3.1 the goal stands",
		"approve c3.1",
		"the answer to c3.1 is no",
		"c3 write it up",
		"cx.1 no",
		"yes",
		"",
	} {
		if id, answer, names := namesAConcern(message); names {
			t.Errorf("namesAConcern(%q) = %q, %q, true; want it read as something other than an answer by name", message, id, answer)
		}
	}
}

// The interactive half of the same fix: a conversation that opens with a
// question still waiting puts it to the operator before anything else, and the
// answer is durable without a turn being taken.
func TestAConversationOpensByPuttingAWaitingQuestionToTheOperator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	asked := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneConcernTurn))
	if _, err := asked.Send(context.Background(), "what should we do about the marketplace?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	var out strings.Builder
	// One line in: the answer to what was already waiting, and then the input
	// ends, so no turn is ever taken.
	if err := resumed.Converse(context.Background(), testConsole(strings.NewReader("1\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	for _, required := range []string{"still waiting on you", "Which of these should it be?", "answered c1.1"} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	if len(resumed.Concerns()) != 0 {
		t.Fatalf("%d concern(s) still waiting after being answered", len(resumed.Concerns()))
	}
	payload := onlyEventPayload(t, root, resumed, execution.EventConcernAnswered)
	if !strings.Contains(payload, `"answer":"Write a goal for extensions"`) {
		t.Fatalf("answer event = %s", payload)
	}
	third := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	if open := third.Concerns(); len(open) != 0 {
		t.Fatalf("an answered question came back as waiting: %#v", open)
	}
}

// A conversation that keeps stopping on questions nobody answers keeps the most
// recent of them: the record is bounded, and the oldest is what goes.
func TestTheUnansweredQuestionsARecordCarriesAreBounded(t *testing.T) {
	t.Parallel()

	results := make([]backendapi.RunResult, 0, 3)
	for i := 0; i < 3; i++ {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: concernReply("Five more.",
			`{"kind":"unplaceable","subject":"one","detail":"d","question":"q?"}`,
			`{"kind":"unplaceable","subject":"two","detail":"d","question":"q?"}`,
			`{"kind":"unplaceable","subject":"three","detail":"d","question":"q?"}`,
			`{"kind":"unplaceable","subject":"four","detail":"d","question":"q?"}`,
			`{"kind":"unplaceable","subject":"five","detail":"d","question":"q?"}`,
		)})
	}
	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: results})
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)
	for i := 0; i < 3; i++ {
		if _, err := session.Send(context.Background(), "and?"); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}
	if len(session.Concerns()) != 15 {
		t.Fatalf("%d concern(s) open in the process that raised them, want all 15", len(session.Concerns()))
	}
	resumed := openTestSession(t, testOptionsWithStore(t, root))
	open := resumed.Concerns()
	if len(open) != 10 || open[0].ID != "c2.1" || open[9].ID != "c3.5" {
		t.Fatalf("a later process found %d concern(s) from %s to %s, want the ten most recent", len(open), open[0].ID, open[len(open)-1].ID)
	}
}

// testOptionsWithStore is a second process over the same recorded conversation
// with nothing to say.
func testOptionsWithStore(t *testing.T, root string) Options {
	t.Helper()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "nothing to say"}}})
	options.Store = newTestStore(t, root)
	return options
}
