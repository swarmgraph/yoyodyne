package slack

// A message to the Lead Product Manager that arrives while another turn holds
// the conversation is queued, told so, and delivered once the turn ends.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A message that arrives mid-turn is queued and said to be, and once the turn
// ends it is delivered, the thread is told so, and the answer follows in the
// same thread.
func TestAMessageArrivingMidTurnIsDeliveredOnceTheTurnEnds(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000100"
	talker := &turnHeldConversation{held: true}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+"> what should we build next?", askedTS))

	queued := onlyPost(t, posts)
	if !strings.Contains(queued.Text, conversationQueued) || queued.ThreadTS != askedTS {
		t.Fatalf("post = %#v, want the message said to be queued, in its own thread", queued)
	}
	if worn := posts.wearing[askedTS]; !worn[notify.ReceiptUnderConsideration.Symbol()] {
		t.Fatalf("the message wears %#v, want the thinking mark while it waits", worn)
	}

	// Still held: nothing is said and nothing is lost.
	sink.steering.drain(context.Background())
	if len(posts.requests) != 1 || len(queuedMessages(t, sink)) != 1 {
		t.Fatalf("posts = %#v, want nothing said and the message still queued while the turn holds", posts.requests)
	}

	talker.release()
	sink.steering.drain(context.Background())

	if said := talker.delivered(); len(said) != 1 || said[0] != "what should we build next?" {
		t.Fatalf("delivered = %q, want the queued message said once", said)
	}
	if len(posts.requests) != 3 {
		t.Fatalf("posts = %#v, want the queued line, the delivery line, and the answer", posts.requests)
	}
	if !strings.Contains(posts.requests[1].Text, "has now been passed to the Lead Product Manager") {
		t.Fatalf("post = %q, want the thread told the message was delivered", posts.requests[1].Text)
	}
	if answer := posts.requests[2]; !strings.Contains(answer.Text, "answered: what should we build next?") || answer.ThreadTS != askedTS {
		t.Fatalf("post = %#v, want the answer in the same thread", answer)
	}
	if worn := posts.wearing[askedTS]; !worn[notify.ReceiptSettled.Symbol()] {
		t.Fatalf("the message wears %#v, want it settled once answered", worn)
	}
	if waiting := queuedMessages(t, sink); len(waiting) != 0 {
		t.Fatalf("queue = %+v, want it empty once delivered", waiting)
	}
}

// Two messages queued behind one turn are delivered in the order they arrived,
// and the second is told one is ahead of it.
func TestTwoQueuedMessagesAreDeliveredInTheOrderTheyArrived(t *testing.T) {
	t.Parallel()

	talker := &turnHeldConversation{held: true}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+"> first", "1750000001.000100"))
	say(sink, topLevel(testOperator, "<@"+testApp+"> second", "1750000002.000100"))

	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want each message told it is queued", posts.requests)
	}
	if want := fmt.Sprintf(queuedAhead, 1); !strings.Contains(posts.requests[1].Text, want) {
		t.Fatalf("post = %q, want the second told one message is ahead of it", posts.requests[1].Text)
	}
	// The second was never offered: a message does not overtake one waiting.
	if attempts := talker.attempts(); attempts != 1 {
		t.Fatalf("the conversation was offered %d message(s) while one was queued, want only the first", attempts)
	}

	talker.release()
	sink.steering.drain(context.Background())

	if said := talker.delivered(); len(said) != 2 || said[0] != "first" || said[1] != "second" {
		t.Fatalf("delivered = %q, want first then second", said)
	}
	if !strings.Contains(posts.requests[3].Text, "answered: first") || posts.requests[3].ThreadTS != "1750000001.000100" {
		t.Fatalf("post = %#v, want the first answer in the first thread", posts.requests[3])
	}
	if !strings.Contains(posts.requests[5].Text, "answered: second") || posts.requests[5].ThreadTS != "1750000002.000100" {
		t.Fatalf("post = %#v, want the second answer in the second thread", posts.requests[5])
	}
}

// A message queued when the sink stops is delivered by the sink started again
// on the same state, with no new message to set it off.
func TestAQueuedMessageSurvivesARestart(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000100"
	root := t.TempDir()
	before, posts := newConversingSinkAt(t, root, &turnHeldConversation{held: true})
	say(before, topLevel(testOperator, "<@"+testApp+"> what should we build next?", askedTS))
	if len(posts.requests) != 1 || len(queuedMessages(t, before)) != 1 {
		t.Fatalf("posts = %#v, want the message queued before the restart", posts.requests)
	}

	talker := &turnHeldConversation{}
	after, posts := newConversingSinkAt(t, root, talker)
	// The sink's log says when the answer has been posted, which is the moment
	// there is something to read.
	answered := make(chan struct{})
	var once sync.Once
	after.log = func(format string, _ ...any) {
		if strings.Contains(format, "was answered") {
			once.Do(func() { close(answered) })
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		after.steering.serveQueue(ctx)
	}()
	<-answered
	cancel()
	<-served

	if said := talker.delivered(); len(said) != 1 || said[0] != "what should we build next?" {
		t.Fatalf("delivered = %q, want the message queued before the restart said after it", said)
	}
	if len(posts.requests) != 2 || !strings.Contains(posts.requests[1].Text, "answered: what should we build next?") || posts.requests[1].ThreadTS != askedTS {
		t.Fatalf("posts = %#v, want the delivery line and the answer in the original thread", posts.requests)
	}
	if waiting := queuedMessages(t, after); len(waiting) != 0 {
		t.Fatalf("queue = %+v, want it empty once delivered", waiting)
	}
}

// A queued message the conversation keeps refusing for a reason other than
// being busy is given up on once, with the reason, and is not tried again.
func TestAQueuedMessageThatCannotBeDeliveredIsReportedOnceWithTheReason(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000100"
	talker := &turnHeldConversation{held: true}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+"> what should we build next?", askedTS))

	talker.failWith(errors.New("usage limit reached, resets at 18:00"))
	for try := 1; try < maxDeliveryFailures; try++ {
		sink.steering.drain(context.Background())
		if len(posts.requests) != 1 {
			t.Fatalf("after try %d posts = %#v, want nothing said while it is still being tried", try, posts.requests)
		}
		if waiting := queuedMessages(t, sink); len(waiting) != 1 || waiting[0].Failures != try {
			t.Fatalf("after try %d queue = %+v, want it still queued with the failure counted", try, waiting)
		}
	}
	sink.steering.drain(context.Background())
	sink.steering.drain(context.Background())

	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want the queued line and one line giving up", posts.requests)
	}
	gaveUp := posts.requests[1]
	if !strings.Contains(gaveUp.Text, "usage limit reached, resets at 18:00") || !strings.Contains(gaveUp.Text, "taken off the queue") || gaveUp.ThreadTS != askedTS {
		t.Fatalf("post = %#v, want the reason said once in the message's thread", gaveUp)
	}
	if attempts := talker.attempts(); attempts != 1+maxDeliveryFailures {
		t.Fatalf("the conversation was offered the message %d time(s), want the first try and %d more", attempts, maxDeliveryFailures)
	}
	if worn := posts.wearing[askedTS]; !worn[notify.ReceiptRefused.Symbol()] {
		t.Fatalf("the message wears %#v, want the refusal mark", worn)
	}
	if waiting := queuedMessages(t, sink); len(waiting) != 0 {
		t.Fatalf("queue = %+v, want it off the queue once given up on", waiting)
	}
}

// A try that has to wait does not set off another try at once. A queue that
// woke itself after every try would read its file and open the conversation
// over and over with no pause for as long as a terminal held the turn, and use
// up its tries against a refusal in moments. So after a try that waits, and
// after a look at an empty queue, nothing is left to wake the queue before the
// retry interval; only a turn carried straight from the channel wakes it.
func TestATryThatHasToWaitDoesNotWakeTheQueueAgain(t *testing.T) {
	t.Parallel()

	talker := &turnHeldConversation{held: true}
	sink, _ := newConversingSink(t, talker)

	sink.steering.drain(context.Background())
	if woken := len(sink.steering.wake); woken != 0 {
		t.Fatalf("looking at an empty queue left %d wake(s), want none", woken)
	}
	if attempts := talker.attempts(); attempts != 0 {
		t.Fatalf("an empty queue offered the conversation %d message(s)", attempts)
	}

	// A message carried straight from the channel finds the turn held and is
	// queued; that turn ending wakes the queue, once.
	say(sink, topLevel(testOperator, "<@"+testApp+"> what should we build next?", "1750000001.000100"))
	if woken := len(sink.steering.wake); woken != 1 {
		t.Fatalf("a turn from the channel ending left %d wake(s), want one", woken)
	}
	<-sink.steering.wake

	for try := 1; try <= 3; try++ {
		sink.steering.drain(context.Background())
		if attempts := talker.attempts(); attempts != 1+try {
			t.Fatalf("drain %d offered the conversation %d message(s) in all, want one more each time", try, attempts)
		}
		if woken := len(sink.steering.wake); woken != 0 {
			t.Fatalf("a try that had to wait left %d wake(s), want none, so the next try waits for the retry interval", woken)
		}
	}

	talker.failWith(errors.New("usage limit reached"))
	sink.steering.drain(context.Background())
	if woken := len(sink.steering.wake); woken != 0 {
		t.Fatalf("a failed try left %d wake(s), want none, so the tries against a refusal are spread out", woken)
	}
	if waiting := queuedMessages(t, sink); len(waiting) != 1 || waiting[0].Failures != 1 {
		t.Fatalf("queue = %+v, want one failure counted per try", waiting)
	}
}

// The case the operator described: a message sent straight on finds a
// `yoyo chat` answering at a terminal. It goes back at the front of the queue,
// ahead of a message that arrived while it was being tried, and is delivered
// first once the terminal's turn ends.
func TestAMessageThatFindsATerminalTurnGoesBackInFrontAndIsDeliveredFirst(t *testing.T) {
	t.Parallel()

	talker := &turnHeldConversation{held: true, gate: make(chan struct{}), running: make(chan struct{})}
	sink, posts := newConversingSink(t, talker)
	sink.steering.handle(context.Background(), topLevel(testOperator, "<@"+testApp+"> first", "1750000001.000100"))
	<-talker.running
	// The first is still being tried, so the second is queued behind it.
	sink.steering.handle(context.Background(), topLevel(testOperator, "<@"+testApp+"> second", "1750000002.000100"))
	close(talker.gate)
	sink.steering.settle()

	waiting := queuedMessages(t, sink)
	if len(waiting) != 2 || waiting[0].Said != "first" || waiting[1].Said != "second" {
		t.Fatalf("queue = %+v, want the first put back in front of the second", waiting)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want each message told it is queued", posts.requests)
	}

	talker.release()
	sink.steering.drain(context.Background())

	if said := talker.delivered(); len(said) != 2 || said[0] != "first" || said[1] != "second" {
		t.Fatalf("delivered = %q, want first then second", said)
	}
}

// queuedMessages is what is waiting in a sink's queue, read from its state.
func queuedMessages(t *testing.T, sink *Sink) []Queued {
	t.Helper()
	queue, err := sink.store.LoadQueue()
	if err != nil {
		t.Fatalf("LoadQueue() error = %v", err)
	}
	return queue.Waiting
}

// turnHeldConversation is a conversation another turn holds until released,
// and that then answers whatever it is told, or fails with a set reason.
type turnHeldConversation struct {
	// gate, where set, holds the first call until it is closed, and running is
	// closed as that call starts, so a second message can arrive mid-call.
	gate    chan struct{}
	running chan struct{}
	mu      sync.Mutex
	held    bool
	fail    error
	offered int
	said    []string
}

func (c *turnHeldConversation) Say(_ context.Context, said string) (Answer, error) {
	c.mu.Lock()
	c.offered++
	first := c.offered == 1
	c.mu.Unlock()
	if first && c.gate != nil {
		close(c.running)
		<-c.gate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held {
		return Answer{}, fmt.Errorf("the product-manager conversation is %w", runstate.ErrConversationHeld)
	}
	if c.fail != nil {
		return Answer{}, c.fail
	}
	c.said = append(c.said, said)
	return Answer{Text: "answered: " + said, ConversationID: testConversation, Turns: len(c.said)}, nil
}

func (c *turnHeldConversation) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held = false
}

func (c *turnHeldConversation) failWith(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held = false
	c.fail = err
}

func (c *turnHeldConversation) attempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offered
}

func (c *turnHeldConversation) delivered() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.said...)
}
