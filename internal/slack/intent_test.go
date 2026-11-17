package slack

// What a reply is for, before anything is recorded from it. The fixture is the
// operator's screenshot of 2026-08-30: a question asked in a work item's thread,
// recorded as a standing instruction, and acknowledged with the phrase it was
// asking about.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The screenshot, replayed. The question is not recorded; a receipt says it was
// heard as a question; and the product manager's answer follows it into the
// same thread, tagged to whoever asked, in the product manager's own name. The
// receipt does not restate the phrase the question was about: that was the
// screenshot's second defect, and it is ruled out rather than merely avoided.
func TestAQuestionInAThreadIsAnsweredByTheProductManagerRatherThanRecorded(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000200"
	const question = "What does 'in force from now' mean?"
	talker := &scriptedConversation{text: "It means the instruction applies from the moment it was recorded, and nothing waits on it."}
	sink, directives, posts := newDiscriminatingSink(t, talker)
	say(sink, reply(testOperator, question, askedTS))

	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want the directive record to hold no question", recorded)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want a receipt and then the answer", posts.requests)
	}
	receipt, answer := posts.requests[0], posts.requests[1]

	if receipt.ThreadTS != testThreadTS || !strings.HasPrefix(receipt.Text, "<@"+testOperator+"> ") {
		t.Fatalf("receipt = %#v, want it in the thread the question was asked in, tagging whoever asked", receipt)
	}
	if !strings.Contains(receipt.Text, "Heard as a question") || !strings.Contains(receipt.Text, "nothing was recorded") {
		t.Fatalf("receipt = %q, want it to say the reply was heard as a question and that nothing was recorded", receipt.Text)
	}
	if strings.Contains(receipt.Text, "in force") {
		t.Fatalf("receipt = %q, want it not to restate the phrase the question was about", receipt.Text)
	}
	if strings.Contains(receipt.Text, "Recorded") {
		t.Fatalf("receipt = %q, want no directive receipt for a question", receipt.Text)
	}

	if answer.ThreadTS != testThreadTS || !strings.HasPrefix(answer.Text, "<@"+testOperator+"> ") {
		t.Fatalf("answer = %#v, want it in the same thread, tagged to whoever asked", answer)
	}
	if !strings.Contains(answer.Text, "applies from the moment it was recorded") {
		t.Fatalf("answer = %q, want the product manager's own answer", answer.Text)
	}
	wantName := sink.appearance.Identity(notify.Persona(domain.RoleProductManager, "")).Name
	if answer.Username != wantName {
		t.Fatalf("username = %q, want the product manager's own name %q", answer.Username, wantName)
	}
	// What the product manager was asked is the operator's question, with where it
	// was asked in front of it: a question in an item's thread is usually about
	// that item, and the conversation has no other way to know which one.
	if said := talker.saidTo(); !strings.HasSuffix(said, question) || !strings.Contains(said, testItem) {
		t.Fatalf("said = %q, want the question framed with the item whose thread it was asked in", said)
	}
	// Answered is settled: the mark on the question moves once the answer is in
	// the thread, and nothing is left wearing the thinking face.
	if worn := posts.wearing[askedTS]; !worn[notify.ReceiptSettled.Symbol()] || worn[notify.ReceiptUnderConsideration.Symbol()] {
		t.Fatalf("the question wears %#v, want it marked settled once the answer landed", worn)
	}
}

// The second misrecord from the item's notes, and the shape of every one like
// it: a short question that opens with an auxiliary and ends on its mark.
func TestDidYouRestartIsAQuestionAndNotAStandingInstruction(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "Yes — the session restarted at 06:12 onto the build installed over it."}
	sink, directives, posts := newDiscriminatingSink(t, talker)
	say(sink, reply(testOperator, "Did you restart?", "1750000001.000200"))

	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want no directive that 'applies from now on' for a question", recorded)
	}
	if talker.turns() != 1 {
		t.Fatalf("the product manager was asked %d time(s), want the question carried once", talker.turns())
	}
	if len(posts.requests) != 2 || !strings.Contains(posts.requests[1].Text, "restarted at 06:12") {
		t.Fatalf("posts = %#v, want the product manager's answer after the receipt", posts.requests)
	}
}

// An instruction is what the record is for, and the discrimination leaves it
// exactly as it was: recorded, acknowledged, and never spoken to the product
// manager.
func TestAnInstructionStillReachesTheRecordAndNotTheProductManager(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "I would not say this."}
	sink, directives, posts := newDiscriminatingSink(t, talker)
	say(sink, reply(testOperator, "prefer the smaller change here — don't refactor the store as well", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if recorded.Text != "prefer the smaller change here — don't refactor the store as well" {
		t.Fatalf("text = %q, want the instruction recorded in the operator's own words", recorded.Text)
	}
	if talker.turns() != 0 {
		t.Fatalf("an instruction reached the product manager %d time(s); it is the directive path", talker.turns())
	}
	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, "Recorded") {
		t.Fatalf("answer = %q, want the directive receipt", answer.Text)
	}
}

// A reply the reading will not decide is asked back in one line, and nothing is
// recorded or asked on the operator's behalf. Either guess costs something real
// — a question recorded is a directive nobody gave, an instruction answered is
// direction that never reached the work — so the line says how to say which.
func TestAnAmbiguousReplyIsAskedBackInOneLine(t *testing.T) {
	t.Parallel()

	const replyTS = "1750000001.000200"
	talker := &scriptedConversation{text: "I would not say this."}
	sink, directives, posts := newDiscriminatingSink(t, talker)
	say(sink, reply(testOperator, "what does this mean use the smaller change", replyTS))

	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want nothing recorded from a reply nobody could read", recorded)
	}
	if talker.turns() != 0 {
		t.Fatalf("an uncertain reply reached the product manager %d time(s); it is asked back instead", talker.turns())
	}
	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, askedBack) {
		t.Fatalf("answer = %q, want the one line asking which was meant", answer.Text)
	}
	if !strings.HasPrefix(answer.Text, "<@"+testOperator+"> ") {
		t.Fatalf("answer = %q, want it addressed to whoever wrote the reply", answer.Text)
	}
	if worn := posts.wearing[replyTS]; !worn[notify.ReceiptRefused.Symbol()] || len(worn) != 1 {
		t.Fatalf("the reply wears %#v, want the refusal mark: nothing was recorded", worn)
	}
}

// A stated kind is the operator saying what the reply is for, so its words are
// not read for intent: `ambiguous: which did you mean?` is the operator's own
// question, recorded as one on purpose, and it pauses the work.
func TestAStatedKindIsNotReadForIntent(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "I would not say this."}
	sink, directives, posts := newDiscriminatingSink(t, talker)
	say(sink, reply(testOperator, "ambiguous: which of the two publishing behaviours did you mean?", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if !recorded.Pauses() {
		t.Fatalf("recorded = %+v, want the stated ambiguous directive, pausing the work", recorded)
	}
	if talker.turns() != 0 {
		t.Fatalf("a stated directive reached the product manager %d time(s)", talker.turns())
	}
	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, "Recorded") {
		t.Fatalf("answer = %q, want the directive receipt", answer.Text)
	}
}

// A question from somebody without direct-work is refused for the grant, before
// it is read: talking to the product manager admits work and spends money, and
// it is held to the same grant a directive is.
func TestAQuestionIsHeldToTheSameGrantAsADirective(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "I would not say this."}
	sink, _, posts := newDiscriminatingSink(t, talker)
	recognize(sink, testColleague)
	say(sink, reply(testColleague, "Did you restart?", "1750000001.000200"))

	if talker.turns() != 0 {
		t.Fatalf("somebody without direct-work reached the product manager %d time(s)", talker.turns())
	}
	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, "direct-work") {
		t.Fatalf("answer = %q, want the refusal naming the grant", answer.Text)
	}
}

// A sink with no conversation behind it cannot answer, and says so rather than
// recording the question or falling silent: nothing is recorded, and the
// refusal says where the question can be asked.
func TestAQuestionWithNoConversationToCarryItIsSaidRatherThanRecorded(t *testing.T) {
	t.Parallel()

	const replyTS = "1750000001.000200"
	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), reply(testOperator, "Did you restart?", replyTS))

	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want no question recorded for want of somewhere to ask it", recorded)
	}
	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, "reads as a question") || !strings.Contains(answer.Text, "yoyo chat") {
		t.Fatalf("answer = %q, want it to say the question could not be carried and where to ask it", answer.Text)
	}
	if worn := posts.wearing[replyTS]; !worn[notify.ReceiptRefused.Symbol()] {
		t.Fatalf("the reply wears %#v, want the refusal mark", worn)
	}
}

// A question that arrives while the product manager is mid-turn keeps its
// promise: the receipt says an answer follows, the thread is told the question
// is queued behind the turn, and the answer comes once the turn ends.
func TestAQuestionWhileTheProductManagerIsBusyIsQueuedAndAnsweredAfter(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000200"
	talker := &scriptedConversation{text: "Yes, at 09:00."}
	sink, directives, posts := newDiscriminatingSink(t, talker)
	if !sink.steering.begin() {
		t.Fatalf("could not take the turn to stand in for one in flight")
	}
	say(sink, reply(testOperator, "Did you restart?", askedTS))

	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want no question recorded", recorded)
	}
	if talker.turns() != 0 {
		t.Fatalf("the product manager was asked %d time(s) while mid-turn", talker.turns())
	}
	if len(posts.requests) != 2 || !strings.Contains(posts.requests[1].Text, conversationQueued) {
		t.Fatalf("posts = %#v, want the receipt and then the line saying the question is queued", posts.requests)
	}

	sink.steering.finish()
	sink.steering.drain(context.Background())

	if talker.turns() != 1 || !strings.Contains(talker.saidTo(), "Did you restart?") {
		t.Fatalf("said = %q over %d turn(s), want the queued question said once the turn ended", talker.saidTo(), talker.turns())
	}
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %#v, want the delivery line and the answer after the first two", posts.requests)
	}
	if !strings.Contains(posts.requests[2].Text, "has now been passed to the Lead Product Manager") {
		t.Fatalf("post = %q, want the thread told the queued question was delivered", posts.requests[2].Text)
	}
	if !strings.Contains(posts.requests[3].Text, "Yes, at 09:00.") || posts.requests[3].ThreadTS != testThreadTS {
		t.Fatalf("post = %#v, want the answer in the item's thread", posts.requests[3])
	}
	if worn := posts.wearing[askedTS]; !worn[notify.ReceiptSettled.Symbol()] {
		t.Fatalf("the question wears %#v, want it settled once answered", worn)
	}
}

// The receipt promises an answer and the turn then fails: the thread is told
// what happened, in the same words a mention gets, and the question is marked
// refused rather than left wearing the thinking face.
func TestAQuestionWhoseTurnFailsIsToldSoInTheThread(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000200"
	talker := &scriptedConversation{err: context.DeadlineExceeded}
	sink, _, posts := newDiscriminatingSink(t, talker)
	say(sink, reply(testOperator, "Did you restart?", askedTS))

	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want the receipt and then what stopped the answer", posts.requests)
	}
	if !strings.Contains(posts.requests[1].Text, "could not answer") {
		t.Fatalf("answer = %q, want the thread told why no answer came", posts.requests[1].Text)
	}
	if worn := posts.wearing[askedTS]; !worn[notify.ReceiptRefused.Symbol()] {
		t.Fatalf("the question wears %#v, want the refusal mark once the turn failed", worn)
	}
}

// newDiscriminatingSink is a sink with both halves of the inbound side wired —
// the directive record an instruction lands in, and the product manager's
// conversation a question is carried to — which is how a running sink is
// assembled, and the assembly the fixture has to be replayed against.
func newDiscriminatingSink(t *testing.T, talker Conversation) (*Sink, *runstate.DirectiveStore, *recordedPosts) {
	t.Helper()

	root := t.TempDir()
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	directives, err := runstate.NewDirectiveStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewDirectiveStore() error = %v", err)
	}
	posts := &recordedPosts{}
	sink, err := New(Options{
		Channel:      "C1",
		Store:        store,
		API:          newTestAPI(t, posts.handle),
		Feed:         &fixedFeed{},
		Directives:   directives,
		Conversation: talker,
		Operators:    []string{testOperator},
		Recognized:   []string{testOperator},
		Contacts:     []string{testContact},
		Standing:     &readmodel.Sources{},
		Log:          func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	sink.pace.sleep = func(context.Context, time.Duration) error { return nil }
	threads, err := store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	topic, err := notify.WorkItem(testItem)
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	threads.Record(topic.Key(), Thread{Channel: "C1", ThreadTS: testThreadTS})
	if err := store.SaveThreads(threads); err != nil {
		t.Fatalf("SaveThreads() error = %v", err)
	}
	return sink, directives, posts
}
