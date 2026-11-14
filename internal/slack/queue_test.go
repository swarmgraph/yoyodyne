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
	mu      sync.Mutex
	held    bool
	fail    error
	offered int
	said    []string
}

func (c *turnHeldConversation) Say(_ context.Context, said string) (Answer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offered++
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
