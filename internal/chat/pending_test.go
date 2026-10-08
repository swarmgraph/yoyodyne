package chat

// What survives the process that proposed the work. A proposal made by one
// `yoyo chat --message` is decided by another, so everything a decision needs —
// the proposal itself, the identifier that names it, and the account the agent
// is owed afterwards — has to be in the record rather than in memory.

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// oneProposalTurn is a reply that proposes a single work item under a project
// that asks about every one of them, so nothing is created and the operator is
// left with something to decide.
const oneProposalTurn = `We could wait out the limit rather than failing the run.

` + "```yoyodyne-proposal" + `
{"items":[{"kind":"feature","title":"Pause on a usage limit","description":"Wait for the window and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}]}
` + "```" + `
`

// The defect: a proposal lived only in the process that made it, so a later one
// had nothing an approval could name. The operator's "y" became ordinary speech
// to a role that cannot create work, and nothing reached the queue.
func TestAProposalOutlivesTheProcessThatMadeItSoALaterOneCanDecideIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}

	proposing := perItemApprovalOptions(t, root, tracker, oneProposalTurn)
	proposed := openTestSession(t, proposing)
	reply, err := proposed.Send(context.Background(), "what should we do about usage limits?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("reply proposed %d item(s), want one to decide", len(reply.Proposals))
	}
	if len(tracker.created) != 0 {
		t.Fatalf("%d item(s) were created before anybody approved anything", len(tracker.created))
	}

	// A second process, holding nothing but the record the first one left.
	deciding := perItemApprovalOptions(t, root, tracker, "It is in the queue now.")
	resumed := openTestSession(t, deciding)
	pending := resumed.Proposals()
	if len(pending) != 1 {
		t.Fatalf("%d proposal(s) came back from the record, want the undecided one", len(pending))
	}
	if pending[0].ID != reply.Proposals[0].ID {
		t.Fatalf("proposal came back as %q, want %q; an approval names it by this", pending[0].ID, reply.Proposals[0].ID)
	}
	if pending[0].Proposal.Title != "Pause on a usage limit" || pending[0].Proposal.Rationale == "" {
		t.Fatalf("proposal came back as %#v, want what the operator was shown", pending[0].Proposal)
	}

	outcomes, decided, err := decisions(resumed.Decide(context.Background(), "y"))
	if err != nil || !decided {
		t.Fatalf("Decide() = %v, %t, %v; want the bare approval to decide the one proposal on the table", outcomes, decided, err)
	}
	if len(outcomes) != 1 || !outcomes[0].Approved || outcomes[0].WorkItemID == "" {
		t.Fatalf("outcomes = %#v, want one approval that created an item", outcomes)
	}
	if len(tracker.created) != 1 || tracker.created[0].Title != "Pause on a usage limit" {
		t.Fatalf("tracker created %#v, want the proposed work", tracker.created)
	}
	if len(resumed.Proposals()) != 0 {
		t.Fatalf("%d proposal(s) are still pending after being decided", len(resumed.Proposals()))
	}

	// The decision is on the record, not only in the tracker: what the operator
	// approved and what came of it are both events a later reader can find.
	recorded := countEvents(t, root, resumed)
	for _, wanted := range []execution.EventType{execution.EventProposalRecorded, execution.EventProposalApproved, execution.EventProposalCreated} {
		if recorded[wanted] != 1 {
			t.Fatalf("%s recorded %d time(s), want once; events = %v", wanted, recorded[wanted], recorded)
		}
	}

	// And the agent hears about it. It proposed the work and was never told what
	// became of it, which is how a product manager comes to describe a queue it
	// has not seen.
	if _, err := resumed.Send(context.Background(), "is it in the queue?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	told := deciding.Backend.(*fakeBackend).requests[0].Prompt
	if !strings.Contains(told, "approved proposal") || !strings.Contains(told, "yoyodyne-1") {
		t.Fatalf("the next turn was told %q, want an account of the approval and the item it created", told)
	}
}

// A third process must not be able to spend the same approval twice. The
// decision left the pending set when it was made, so what a later conversation
// resumes is a conversation with nothing on the table.
func TestADecidedProposalDoesNotComeBackFromTheRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	deciding := openTestSession(t, perItemApprovalOptions(t, root, tracker, "declined"))
	if _, _, err := deciding.Decide(context.Background(), "decline 1.1 we already handle this"); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	third := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	if pending := third.Proposals(); len(pending) != 0 {
		t.Fatalf("a declined proposal came back as pending: %#v", pending)
	}
	// A bare yes with nothing on the table names no proposal, so it is somebody
	// talking and is said to the agent exactly as it always was.
	outcomes, decided, err := decisions(third.Decide(context.Background(), "y"))
	if decided || err != nil || len(outcomes) != 0 {
		t.Fatalf("Decide() = %v, %t, %v; want nothing on the table to decide", outcomes, decided, err)
	}
	// An answer that names the decided proposal is not ambiguous in that way, and
	// it is the answer that must never go quietly to the agent: this is an
	// operator approving something whose decision they did not see, and telling
	// them the approval was said to a product manager as chat is the whole defect.
	outcomes, decided, err = decisions(third.Decide(context.Background(), "approve 1.1"))
	if !decided {
		t.Fatalf("an approval naming a proposal was passed on as speech with nothing on the table")
	}
	if err == nil || !strings.Contains(err.Error(), "1.1") {
		t.Fatalf("Decide() error = %v, want it to name the proposal that is not awaiting a decision", err)
	}
	if len(outcomes) != 0 || len(tracker.created) != 0 {
		t.Fatalf("outcomes = %#v and %d creation(s); want a decided proposal decided once", outcomes, len(tracker.created))
	}
}

// twoProposalTurn proposes two items, so one answer decides a batch and the
// record has more than one decision to keep straight.
const twoProposalTurn = `Two things follow from that.

` + "```yoyodyne-proposal" + `
{"items":[{"kind":"feature","title":"Pause on a usage limit","description":"Wait for the window and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."},{"kind":"feature","title":"Report what was admitted","description":"Say what went into the queue.","rationale":"Autonomy nobody can see is indistinguishable from work behind their back.","goal":"Run development nearly autonomously."}]}
` + "```" + `
`

// Every decision in a batch is durable as it is made, not when the batch ends.
// What the state carries is the proposals still awaiting a decision, so a batch
// that decided two and saved once at the end would, if anything went wrong
// between them, leave the record listing work this process had already created —
// and the next process would put it back on the table to be approved twice.
func TestEveryDecisionInABatchIsDurableAsItIsMade(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, twoProposalTurn))
	reply, err := proposed.Send(context.Background(), "what should we do about usage limits?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Proposals) != 2 {
		t.Fatalf("reply proposed %d item(s), want two to decide in one answer", len(reply.Proposals))
	}

	deciding := openTestSession(t, perItemApprovalOptions(t, root, tracker, "both are settled"))
	outcomes, decided, err := decisions(deciding.Decide(context.Background(), "approve 1.1 and decline 1.2 not this quarter"))
	if err != nil || !decided {
		t.Fatalf("Decide() = %v, %t, %v; want the batch carried out", outcomes, decided, err)
	}
	if len(outcomes) != 2 || !outcomes[0].Approved || outcomes[1].Approved {
		t.Fatalf("outcomes = %#v, want the approval and the decline", outcomes)
	}
	if len(tracker.created) != 1 || tracker.created[0].Title != "Pause on a usage limit" {
		t.Fatalf("created = %#v, want only the approved item", tracker.created)
	}

	// The record a later process reads holds neither of them. A decline is the
	// half that used to depend on the save at the end of the batch: Reject marks
	// the proposal decided after its own event is written, so nothing but a save
	// taken afterwards takes it off the table.
	later := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	if pending := later.Proposals(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want a decided batch to stay decided", pending)
	}
	// And neither can be spent a second time, which is what the record is for.
	for _, named := range []string{"approve 1.1", "approve 1.2"} {
		outcomes, decided, err := decisions(later.Decide(context.Background(), named))
		if !decided || err == nil {
			t.Fatalf("Decide(%q) = %v, %t, %v; want a decided proposal refused out loud", named, outcomes, decided, err)
		}
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v, want the approval spent exactly once", tracker.created)
	}
}

// An approval the tracker will not carry out creates nothing and leaves the
// proposal awaiting a decision, and says both. Nothing here is a broken
// conversation: the operator can approve it again once the tracker answers, and
// what they must not be told is that the item exists.
func TestAnApprovalTheTrackerRefusesLeavesTheProposalWaitingAndSaysSo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	proposed := openTestSession(t, perItemApprovalOptions(t, root, &fakeTracker{}, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	unreachable := &fakeTracker{err: errors.New("bd is unreachable")}
	resumed := openTestSession(t, perItemApprovalOptions(t, root, unreachable, "nothing to say"))
	outcomes, decided, err := decisions(resumed.Decide(context.Background(), "y"))
	if !decided || err != nil {
		t.Fatalf("Decide() = %t, %v; want the approval carried out and its failure reported in the outcome", decided, err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %#v, want the one approval", outcomes)
	}
	outcome := outcomes[0]
	if !outcome.Approved || !outcome.Undecided || outcome.WorkItemID != "" {
		t.Fatalf("outcome = %#v, want an approval that created nothing", outcome)
	}
	if !strings.Contains(outcome.Problem, "bd is unreachable") {
		t.Fatalf("problem = %q, want what the tracker said", outcome.Problem)
	}
	rendered := outcome.Render()
	if !strings.Contains(rendered, "not created") || !strings.Contains(rendered, "still awaiting a decision") {
		t.Fatalf("rendered = %q, want it to say nothing exists and the decision is still open", rendered)
	}
	// Still on the table, in this process and in the record, so approving it again
	// once the tracker answers asks for the same item rather than losing it.
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) pending, want the one the tracker refused", len(resumed.Proposals()))
	}
	later := openTestSession(t, perItemApprovalOptions(t, root, &fakeTracker{}, "nothing to say"))
	if pending := later.Proposals(); len(pending) != 1 || pending[0].ID != "1.1" {
		t.Fatalf("pending = %#v, want the refused proposal still decidable by a later process", pending)
	}
}

// An answer that is not a decision decides nothing. A prompt may read anything
// it cannot understand as a decline, because it asked the question; a message
// was never asked, and reading it that way would turn work down that the
// operator never mentioned.
func TestAMessageThatDecidesNothingLeavesEveryProposalWhereItWas(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "two of them are about capacity"))
	outcomes, decided, err := decisions(resumed.Decide(context.Background(), "what else is open?"))
	if decided || err != nil || len(outcomes) != 0 {
		t.Fatalf("Decide() = %v, %t, %v; want a question to be a question", outcomes, decided, err)
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) are pending, want the undecided one exactly where it was", len(resumed.Proposals()))
	}
}

// And an ordinary reply that happens to open with a decision word is speech too.
// This is the sharp edge of putting a prompt's grammar on a message: at a prompt
// the operator was just asked, so words after their verb are about the question,
// while here the proposal may be hours and several messages old and they are
// usually talking. A message that turned down work nobody mentioned, and was
// never said to the product manager either, would be the same silent drop this
// item exists to remove, pointing the other way.
func TestAConversationalReplyThatOpensWithADecisionWordIsStillSpeech(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	for _, speech := range []string{
		"no, let's look at the resolver instead",
		"no thanks, we do this already",
		"yes, and can you also check the attribution counts",
		"decline 2 too vague",
	} {
		resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "understood"))
		outcomes, decided, err := decisions(resumed.Decide(context.Background(), speech))
		if decided || err != nil || len(outcomes) != 0 {
			t.Fatalf("Decide(%q) = %v, %t, %v; want it said to the product manager rather than decided", speech, outcomes, decided, err)
		}
		if len(resumed.Proposals()) != 1 {
			t.Fatalf("Decide(%q) left %d proposal(s) pending, want the undecided one exactly where it was", speech, len(resumed.Proposals()))
		}
	}
	if len(tracker.created) != 0 {
		t.Fatalf("speech created %d item(s)", len(tracker.created))
	}
}

// The shapes that are decisions, all of which a prompt accepts and none of which
// prose takes: a proposal named by its identifier with the operator's words after
// it, and an answer with no prose in it at all.
func TestTheDecisionShapesAMessageAcceptsAreDecidedAndNothingElseIs(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{"y", "yes", "n", "no", "approve 1.1", "decline 1.1 too vague", "decline all", "approve 1,2", "approve 1 and 2"} {
		if !decidesAsAMessage(answer) {
			t.Errorf("decidesAsAMessage(%q) = false, want a decision", answer)
		}
	}
	for _, answer := range []string{
		"no, let's look at the resolver instead",
		"yes please, and also check X",
		"decline 2 too vague",
		"what else is open?",
		"approve the resolver work",
		"",
	} {
		if decidesAsAMessage(answer) {
			t.Errorf("decidesAsAMessage(%q) = true, want it read as speech", answer)
		}
	}
}

// A decision the harness cannot carry out whole carries out none of it, and is
// reported as a decision rather than passed on as speech: an approval naming a
// proposal that is not there is exactly the answer that must not go missing.
func TestADecisionNamingAProposalThatIsNotThereFailsRatherThanDecidingNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	outcomes, decided, err := decisions(resumed.Decide(context.Background(), "approve 9.9"))
	if !decided {
		t.Fatalf("Decide() reported nothing decided, want an approval that failed rather than speech")
	}
	if err == nil || !strings.Contains(err.Error(), "9.9") {
		t.Fatalf("Decide() error = %v, want it to name what could not be decided", err)
	}
	if len(outcomes) != 0 || len(tracker.created) != 0 {
		t.Fatalf("outcomes = %#v and %d creation(s); want none of it carried out", outcomes, len(tracker.created))
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) are pending, want the real one still waiting", len(resumed.Proposals()))
	}
}

// perItemApprovalOptions is a conversation under the gate the harness starts
// with: the operator is asked about every work item, so a proposal is something
// to decide rather than something already admitted. The store is the caller's,
// so two of these are two processes talking to one recorded conversation.
func perItemApprovalOptions(t *testing.T, root string, tracker Tracker, reply string) Options {
	t.Helper()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: reply}}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	options.Admission = Admission{WorkItems: domain.ApprovalHuman}
	return options
}

// A conversation that opens with a proposal still waiting puts it to the
// operator rather than carrying it silently to the end and naming it as
// undecided a second time. It is the interactive half of the same fix: a
// proposal made by a single message is decidable wherever the operator turns up
// next.
func TestAConversationOpensByPuttingWhatIsStillWaitingToTheOperator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	var out strings.Builder
	// One line in: the approval of what was already waiting, and then the input
	// ends, so no turn is ever taken.
	if err := resumed.Converse(context.Background(), testConsole(strings.NewReader("y\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	if !strings.Contains(transcript, "still waiting on you") || !strings.Contains(transcript, "Pause on a usage limit") {
		t.Fatalf("transcript = %q, want the waiting proposal put to the operator", transcript)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v, want the approved item", tracker.created)
	}
	if len(resumed.Proposals()) != 0 {
		t.Fatalf("%d proposal(s) still pending after being approved", len(resumed.Proposals()))
	}
	// And it is decided in the record, not only in this process. The operator
	// approved what a resumed conversation put to them and then left without
	// taking a turn, so nothing but the decision itself was ever going to save
	// this — and a third process reading a proposal that already exists would
	// put it back on the table and create the item a second time.
	third := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	if pending := third.Proposals(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want an approved proposal to stay approved across processes", pending)
	}
	outcomes, decided, err := decisions(third.Decide(context.Background(), "approve 1.1"))
	if !decided || err == nil {
		t.Fatalf("Decide() = %v, %t, %v; want the decided proposal refused out loud", outcomes, decided, err)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v, want the approval spent exactly once", tracker.created)
	}
}

// A decline made at the prompt is durable the same way, and it is the half that
// nothing else was going to save. An approval writes an event after it marks the
// proposal decided, so the record catches up on its way past; Reject writes its
// event first and marks the proposal decided after, so only a save taken
// afterwards takes it off the table. Without one, an operator who declined what a
// resumed conversation put to them and then left would be asked again by the next
// process — and the work they turned down would be waiting for them.
func TestADeclineMadeAtThePromptIsDurableWithoutATurnBeingTaken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	var out strings.Builder
	// The proposal is put up front, turned down, and the input ends: no turn is
	// taken, so nothing after this decision was ever going to write the record.
	if err := resumed.Converse(context.Background(), testConsole(strings.NewReader("no\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want a declined proposal to create nothing", tracker.created)
	}
	if len(resumed.Proposals()) != 0 {
		t.Fatalf("%d proposal(s) still pending after being declined", len(resumed.Proposals()))
	}

	third := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	if pending := third.Proposals(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want a declined proposal to stay declined across processes", pending)
	}
}
