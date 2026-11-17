package slack

// Messages to the Lead Product Manager that wait for a turn already in progress.
//
// The conversation admits one turn at a time, and the turn holding it may be
// this sink's own, a `yoyo chat` answering at a terminal, or the harness
// delivering something. A message that arrives meanwhile used to be refused with
// a request to say it again once that turn ended, which left the operator
// watching for the end of a turn they could not see. It is queued instead: kept
// in this sink's state, told so in its thread, and passed on as soon as the
// conversation is free, in the order the messages arrived.
//
// The queue is a file in the sink's own state directory rather than memory, so
// a sink restarted while something is queued delivers it once it is running
// again. It is written by whoever adds to it or takes from it under one mutex,
// and that same mutex is what keeps a new message from overtaking one already
// waiting: a message is carried straight to the conversation only when nothing
// is queued and the sink's own turn is free, and both are decided together.
//
// Nothing is dropped without a word. A message the conversation keeps refusing
// for some reason other than being busy is tried maxDeliveryFailures times and
// then taken off the queue with the reason said once in its thread. Being busy
// is not a failure and is waited out however long it lasts, because a turn ends
// on its own.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// DefaultQueueRetry is how often a sink with something queued asks whether
	// the conversation has come free. A turn held at a terminal ends without this
	// process hearing about it, so it is asked rather than told; the sink's own
	// turns ending wake the queue at once and do not wait for this.
	DefaultQueueRetry = 10 * time.Second
	// maxDeliveryFailures is how many times a queued message is offered to a
	// conversation that refuses it for a reason other than being busy before it
	// is taken off the queue and the reason is said in its thread.
	maxDeliveryFailures = 3
)

// What a thread is told about a message that waits.
const (
	// conversationQueued is said the moment a message is queued. ahead is how
	// many were queued before it, which is said only where there are any.
	conversationQueued = "The Lead Product Manager is in the middle of another turn — this app answering something else, a `yoyo chat` answering at a terminal, or the harness delivering something to it — so your message is queued rather than sent now. It is passed on as soon as that turn ends, and the answer comes here; it stays queued if this app restarts in the meantime."
	queuedAhead        = " %d queued message(s) ahead of it go first."
	// conversationDelivered is said once a queued message has reached the
	// conversation, just before the answer it got.
	conversationDelivered = "The turn ahead of your queued message has ended, and the message has now been passed to the Lead Product Manager, %s after you sent it. The answer follows."
	// conversationUndeliverable is said once, when a queued message is given up
	// on, with the reason the conversation last gave.
	conversationUndeliverable = "Your queued message could not be passed to the Lead Product Manager after %d tries, so it has been taken off the queue rather than tried again: %s. Nothing was said to the Lead Product Manager; say it again, or carry on at `yoyo chat`."
	// conversationUnqueued is a message that could not even be kept, which is a
	// fault in this sink's own state directory. It is the old refusal, said with
	// the reason, because it is the one case where saying it again is the only
	// thing left to do.
	conversationUnqueued = "The Lead Product Manager is in the middle of another turn, and your message could not be queued to follow it, so nothing was said to the Lead Product Manager: %v. Say it again once that turn ends, or carry on at `yoyo chat`."
)

// Queued is one message waiting for the conversation, with everything needed
// to answer it in its own thread after a restart.
type Queued struct {
	// Member is who sent it, which is who the answer is addressed to.
	Member string `json:"member"`
	// Channel, Message and Thread say where it was said: the conversation, the
	// message's own timestamp, and the thread it was said in, empty for a message
	// at the top of the channel.
	Channel string `json:"channel"`
	Message string `json:"message"`
	Thread  string `json:"thread,omitempty"`
	// Typed is what the person typed, and Said is what goes to the conversation:
	// a question in a work item's thread is said with the item named in front of
	// it, and a mention without the mention.
	Typed string `json:"typed"`
	Said  string `json:"said"`
	// QueuedAt is when it was queued, which is how long it waited when it is
	// delivered.
	QueuedAt time.Time `json:"queued_at"`
	// Failures counts refusals other than the conversation being busy, and
	// LastFailure is the most recent one's reason.
	Failures    int    `json:"failures,omitempty"`
	LastFailure string `json:"last_failure,omitempty"`
}

// queuedFrom is one message as it is kept.
func queuedFrom(message inboundMessage, said string, at time.Time) Queued {
	return Queued{
		Member:   message.user,
		Channel:  message.channel,
		Message:  message.ts,
		Thread:   message.threadTS,
		Typed:    message.text,
		Said:     said,
		QueuedAt: at.UTC(),
	}
}

// inbound is the message a queued one was, as the doors that answer it read it.
func (q Queued) inbound() inboundMessage {
	return inboundMessage{user: q.Member, text: q.Typed, ts: q.Message, threadTS: q.Thread, channel: q.Channel}
}

// same reports whether two entries are the same message.
func (q Queued) same(other Queued) bool {
	return q.Channel == other.Channel && q.Message == other.Message
}

// Queue is what waits, oldest first, as it is stored.
type Queue struct {
	SchemaVersion int      `json:"schema_version"`
	Waiting       []Queued `json:"waiting"`
}

// LoadQueue reads the queue. A sink that has never queued anything has no file,
// which is an empty queue.
func (s *Store) LoadQueue() (Queue, error) {
	var stored Queue
	found, err := readJSON(s.queuePath(), "slack message queue", &stored)
	if err != nil {
		return Queue{}, err
	}
	if !found {
		return Queue{SchemaVersion: QueueSchemaVersion}, nil
	}
	if stored.SchemaVersion != QueueSchemaVersion {
		return Queue{}, fmt.Errorf("slack message queue schema version %d is not supported", stored.SchemaVersion)
	}
	return stored, nil
}

// SaveQueue replaces the queue durably.
func (s *Store) SaveQueue(queue Queue) error {
	queue.SchemaVersion = QueueSchemaVersion
	if queue.Waiting == nil {
		queue.Waiting = []Queued{}
	}
	return s.write(s.queuePath(), "slack message queue", queue)
}

// send carries one message to the conversation now if nothing is queued and
// the sink's own turn is free, and queues it otherwise. A message carried now
// that finds the conversation held elsewhere is queued by the call that found
// it, at the front, because nothing queued after it can have been sent first.
func (s *steering) send(ctx context.Context, message inboundMessage, said string) {
	s.queueMu.Lock()
	queue, err := s.sink.store.LoadQueue()
	// A queue that cannot be read cannot be kept in order either, so a message
	// that finds the conversation free is still carried straight to it.
	if (err != nil || len(queue.Waiting) == 0) && s.begin() {
		s.queueMu.Unlock()
		if err != nil {
			s.sink.log("the queue of messages waiting for the Lead Product Manager could not be read, so %s's message is carried straight to the conversation: %v", message.user, err)
		}
		s.carry(ctx, message, said)
		return
	}
	if err == nil {
		queue.Waiting = append(queue.Waiting, queuedFrom(message, said, s.sink.clock()))
		err = s.sink.store.SaveQueue(queue)
	}
	ahead := len(queue.Waiting) - 1
	s.queueMu.Unlock()
	if err != nil {
		s.answerOnce(ctx, message, fmt.Sprintf(conversationUnqueued, err), "why it could not be queued")
		s.markIn(ctx, s.sink.conversationOf(message), message.ts, notify.ReceiptRefused)
		return
	}
	s.markIn(ctx, s.sink.conversationOf(message), message.ts, notify.ReceiptUnderConsideration)
	s.toldQueued(ctx, message, ahead)
}

// requeue puts back at the front of the queue a message that was carried
// straight to the conversation and found it held, reporting how many are ahead
// of it, which is none. It runs before the call gives the sink's turn back, so
// nothing queued behind it can be delivered ahead of it.
func (s *steering) requeue(message inboundMessage, said string) (int, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queue, err := s.sink.store.LoadQueue()
	if err != nil {
		return 0, err
	}
	queue.Waiting = append([]Queued{queuedFrom(message, said, s.sink.clock())}, queue.Waiting...)
	return 0, s.sink.store.SaveQueue(queue)
}

// toldQueued says in the thread that a message is queued, and wakes the queue
// in case the turn it waits on has already ended.
func (s *steering) toldQueued(ctx context.Context, message inboundMessage, ahead int) {
	said := conversationQueued
	if ahead > 0 {
		said += fmt.Sprintf(queuedAhead, ahead)
	}
	s.answerOnce(ctx, message, said, "the message being queued")
	s.nudge()
}

// nudge wakes the queue without waiting for it.
func (s *steering) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// heldOnly reports a call that said nothing because another turn holds the
// conversation, which is waited out rather than counted as a failure.
func heldOnly(came spoken) bool {
	return errors.Is(came.err, runstate.ErrConversationHeld) && strings.TrimSpace(came.answer.Text) == ""
}

// serveQueue delivers what is queued for as long as the sink runs: at once, on
// every nudge, and every retry interval in case a turn held elsewhere has ended.
func (s *steering) serveQueue(ctx context.Context) {
	ticker := time.NewTicker(s.retry)
	defer ticker.Stop()
	for {
		s.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

// drain delivers queued messages oldest first until one has to wait.
func (s *steering) drain(ctx context.Context) {
	for ctx.Err() == nil && s.deliverNext(ctx) {
	}
}

// deliverNext offers the oldest queued message to the conversation, and reports
// whether it left the queue so the next one may be tried at once.
func (s *steering) deliverNext(ctx context.Context) bool {
	if s.conversation == nil || !s.begin() {
		return false
	}
	s.queueMu.Lock()
	queue, err := s.sink.store.LoadQueue()
	s.queueMu.Unlock()
	if err != nil {
		s.finish()
		s.sink.log("the queue of messages waiting for the Lead Product Manager could not be read, so none was delivered: %v", err)
		return false
	}
	if len(queue.Waiting) == 0 {
		s.finish()
		return false
	}
	next := queue.Waiting[0]
	message := next.inbound()
	// The call gives the turn back when it finishes.
	came, bounded, landed := s.call(ctx, message, next.Said, false)
	switch {
	case ctx.Err() != nil:
		s.sink.log("the sink stopped while delivering %s's queued message %q, so it stays queued and is delivered once the sink is running again",
			message.user, singleLine(message.text, maxAskedBytes))
		return false
	case !landed:
		// The wait ran out with the turn still going, so the message was said.
		s.dequeue(next)
		s.gaveUp(ctx, message, bounded)
		return true
	case heldOnly(came):
		return false
	case came.err != nil && strings.TrimSpace(came.answer.Text) == "" && !came.answer.Harness &&
		!errors.Is(bounded.Err(), context.DeadlineExceeded):
		next.Failures++
		next.LastFailure = came.err.Error()
		if next.Failures < maxDeliveryFailures {
			s.update(next)
			s.sink.log("%s's queued message could not be passed to the Lead Product Manager (try %d of %d), so it stays queued: %v",
				message.user, next.Failures, maxDeliveryFailures, came.err)
			return false
		}
		s.dequeue(next)
		s.answerOnce(ctx, message, fmt.Sprintf(conversationUndeliverable, next.Failures, next.LastFailure), "why its queued message was given up on")
		s.markIn(ctx, s.sink.conversationOf(message), message.ts, notify.ReceiptRefused)
		return true
	default:
		s.dequeue(next)
		// A decision the harness carried out is its own account of what happened
		// and was never said to the Lead Product Manager, so it is not announced
		// as having been passed on.
		if !came.answer.Harness {
			waited := s.sink.clock().Sub(next.QueuedAt).Round(time.Second)
			s.answerOnce(ctx, message, fmt.Sprintf(conversationDelivered, waited), "its queued message being delivered")
		}
		s.came(ctx, message, bounded, came)
		return true
	}
}

// dequeue takes one message off the queue. A queue that cannot be written leaves
// it there, to be offered again, which is said in the log because the person
// who sent it may then get a second answer.
func (s *steering) dequeue(gone Queued) {
	s.rewrite(gone, func(waiting []Queued, at int) []Queued {
		return append(waiting[:at:at], waiting[at+1:]...)
	}, "taken off")
}

// update records a failed try against one queued message.
func (s *steering) update(tried Queued) {
	s.rewrite(tried, func(waiting []Queued, at int) []Queued {
		waiting[at] = tried
		return waiting
	}, "updated on")
}

func (s *steering) rewrite(one Queued, change func([]Queued, int) []Queued, done string) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queue, err := s.sink.store.LoadQueue()
	if err == nil {
		for at, waiting := range queue.Waiting {
			if waiting.same(one) {
				queue.Waiting = change(queue.Waiting, at)
				err = s.sink.store.SaveQueue(queue)
				break
			}
		}
	}
	if err != nil {
		s.sink.log("%s's queued message could not be %s the queue, so it may be offered to the Lead Product Manager again: %v", one.Member, done, err)
	}
}
