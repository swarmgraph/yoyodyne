package slack

// The product manager's own conversation, reached from this channel.
//
// The door in mention.go answers where things stand and, before this, said of
// everything else that it could not do it yet. This is what it does instead: an
// operator says something to this app and the product manager answers, in the
// thread they said it in and in its own voice.
//
// It creates no conversation of its own, and that is the whole of why a channel
// is allowed to hold one at all. What it speaks into is the durable conversation
// `yoyo chat` opens, keyed by the agent that holds it, recorded in the same state
// root and resumed from the same provider session — so a question asked at a
// terminal is answered from a phone and carried back to the terminal, because
// there is one conversation and two clients of it rather than two conversations
// that have to be reconciled. Nothing here keeps a transcript, a session, or a
// turn count: what this sink stores about a turn is what it always stored about
// a message, which is that it has already acted on it.
//
// Four rules hold the channel to that.
//
// Authority is the thread reply's, unchanged. Talking to the product manager
// admits work, reorders the queue, and spends the operator's money, so it is for
// the humans the project granted direct-work who bound a Slack member id and for
// nobody else. Somebody the project recognizes and granted nothing is told which
// grant they are missing; a stranger is stranger.go's, and neither is answered
// with silence.
//
// The wait is bounded, and bounded here rather than asked of the callee. A
// conversation turn can take many minutes, and steering work from inside one can
// wait on a provider's usage limit for hours — which is right at a terminal,
// where somebody chose to wait, and wrong in a channel, where the only thing
// distinguishable from a slow answer is a dead sink. So one turn gets
// DefaultConversationDeadline and then the thread is told what happened, whether
// that is capacity, the conversation being held elsewhere, or anything else.
//
// The shape of that bound is the point, and a deadline on a context is not it. A
// context is a request to stop, and a callee that does not answer one — a
// blocking read, a sleep somebody wrote without a select, a subprocess wait —
// leaves the caller waiting exactly as long as it would have anyway. So the call
// runs beside the wait rather than inside it: the turn is answered when the
// deadline passes whether or not anything underneath has returned, which is what
// makes this a bound on what the operator waits for rather than a hope about
// somebody else's code.
//
// What that costs is stated rather than hidden. A call nobody is listening to
// any more is still running and still holding the durable conversation, so the
// turn's own guard is given back by the call when it finishes rather than by the
// wait when it gives up — a message arriving in between is told the product
// manager is still answering, which is true, instead of being sent to contend
// for a lease it cannot win. The terminal meets the same hold: `yoyo chat`
// queues behind a turn in flight and says so, so the overdue answer says that
// rather than sending the operator to a client that will make them wait without
// having been told why. The abandoned call is deliberately not something the
// sink's shutdown waits on: waiting on it is the hang this exists to remove.
//
// One turn at a time, said out loud. The durable conversation admits one holder,
// so a second turn started while one is running would be refused by the lease
// rather than queued; this refuses it a moment earlier and in words, which is the
// same answer with somebody actually told.
//
// And it runs off the connection. The connection is a read loop Slack expects to
// keep reading, so a turn taken on it would hold every other message in the
// channel behind one operator's question.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Conversation is the durable operator conversation this door speaks into: one
// thing said, one answer back.
//
// It is deliberately this one method. Opening the conversation, holding it,
// resuming its provider session, and releasing it are the caller's, because they
// are what makes this the same conversation as the terminal's rather than a
// second one — and a sink that could hold it open between messages would be
// holding the lease against the operator's own `yoyo chat` for as long as the
// sink ran.
type Conversation interface {
	Say(ctx context.Context, said string) (Answer, error)
}

// Answer is one reply from the conversation, as this channel posts it.
//
// The prose is what goes in the thread. Everything else a turn did — work it
// admitted, items it reprioritized, a question it put to another role — reaches
// this channel through the ordinary feed, which reads the conversation's own
// durable log, so nothing here repeats it and nothing here is the only account
// of it.
//
// ConversationID and Turns say which conversation answered and where it has got
// to. They are not posted: an operator reading a thread wants the answer, and
// which durable record it came out of is the sink's log's business.
//
// Harness marks an answer nothing said — a decision the harness carried out on a
// proposal the conversation was waiting on, which spends no turn and is not the
// product manager speaking. It decides whose name and face the answer is posted
// under, because a message in a persona's voice that the persona did not say is
// attribution nobody can check.
type Answer struct {
	Text           string
	ConversationID string
	Turns          int
	Harness        bool
}

// DefaultConversationDeadline bounds one turn taken from this channel.
//
// It is generous against a turn and mean against a wait. One provider invocation
// is allowed a quarter of an hour and a single message may take several rounds of
// them, so a long answer can genuinely be cut off here — and that is the right
// trade, because the turn is recorded as it goes and the conversation is where it
// was left, while an operator watching a thread for an hour has been told
// nothing. What must never happen is the unbounded case: a turn that steers work
// can wait on an exhausted usage limit for as long as the configuration allows a
// run to, which is hours.
const DefaultConversationDeadline = 10 * time.Minute

// What the thread is told when a turn does not produce an answer. Each names
// where the conversation actually is, because the whole point of it being one
// conversation is that a client that could not answer is not the end of it.
const (
	conversationBusy = "The Lead Product Manager is already answering something else in this conversation — say it again once that lands, or carry on at `yoyo chat`."
	// conversationHeld is a turn in flight in some other client — a `yoyo chat`
	// answering at a terminal, or the harness delivering something. A `yoyo chat`
	// waiting at its prompt is not one: what the conversation holds exclusive is a
	// turn rather than a window, so what ends this is that turn landing rather
	// than anybody closing anything.
	conversationHeld = "The Lead Product Manager is mid-turn with another client right now — a `yoyo chat` answering at a terminal, or the harness delivering something to it — so nothing was said to it. Say it again once that turn lands; a `yoyo chat` waiting at its prompt does not hold the conversation, only one that is answering does."
	// conversationOverdue is the bounded wait running out. The turn may yet land,
	// and until it does it holds the conversation: `yoyo chat` does not read a
	// turn that is still being written, it queues behind it and says so, and
	// continues from wherever the turn got to once it has landed. That is said
	// plainly rather than as an invitation to go and look, because an operator
	// sent to `yoyo chat` while the abandoned turn still runs finds a wait, and a
	// wait nobody told them about reads as a second thing that has hung.
	conversationOverdue = "I waited %s for the Lead Product Manager and it had not answered, so I stopped waiting rather than leave you with nothing. The turn may still be running, and it holds the conversation until it lands: `yoyo chat` waits behind it, says so, and then continues the same conversation from wherever that turn got to. `yoyo agent list` says whether it is still mid-turn without waiting on it."
	// conversationFailed carries the reason rather than summarizing it. A
	// provider out of capacity says so in its own words, and the durable record it
	// leaves is reported to this channel at warning severity by the feed, so this
	// is the person who asked being told as well as the channel.
	conversationFailed = "The Lead Product Manager could not answer: %v"
	// decisionFailed is the same for a decision the harness carried out and that
	// failed — an approval the tracker refused, a proposal nothing holds any more.
	// The product manager was never asked, so the line does not name it as the
	// speaker: a tracker refusal attributed to a role that never spoke is a
	// failure the operator then looks for in the wrong place. What the reason
	// says about whether any part of it landed is the harness's own account, and
	// the decisions that did land are posted ahead of this line.
	decisionFailed = "That decision could not be carried out, and the Lead Product Manager was not asked: %v"
	// conversationSilent is a turn that came back with nothing to say. It is an
	// answer rather than an absence for the reason every other line here is one.
	conversationSilent = "The Lead Product Manager answered without saying anything. `yoyo chat` continues the same conversation."
	// conversationElsewhere is where the whole of a cut answer is. It is the
	// conversation rather than a command's output, because that is what holds it.
	conversationElsewhere = "`yoyo chat` continues the same conversation and shows the whole of it"
)

// converse carries one thing somebody said to the product manager, on a
// goroutine of its own, and answers in the thread they said it in.
//
// It reports nothing to its caller and stops nothing when it fails, which is the
// rule the whole inbound half follows: the connection goes on reading, and every
// path out of the turn either answers in the thread or says in the sink's log why
// it could not.
func (s *steering) converse(ctx context.Context, message inboundMessage, said string) {
	if !s.begin() {
		s.answerOnce(ctx, message, conversationBusy, "a turn already in flight")
		return
	}
	s.sink.log("this app was addressed by %s outside its own threads, saying %q, and is taking it to the Lead Product Manager",
		message.user, singleLine(message.text, maxAskedBytes))
	s.carry(ctx, message, said)
}

// carry takes one thing said to the product manager, on a goroutine of its own,
// with the turn already begun by whoever is calling. It is the half of converse
// a question in a work item's thread shares: that door takes the turn before it
// posts the receipt promising an answer, so that a turn it cannot take is a
// refusal and never a promise.
//
// Only the wait is counted. What the sink's shutdown waits for is the thread
// being answered, which always happens; the call underneath may still be
// running, and waiting on that is the hang this whole bound exists to remove.
func (s *steering) carry(ctx context.Context, message inboundMessage, said string) {
	s.turns.Add(1)
	go func() {
		defer s.turns.Done()
		s.take(ctx, message, said)
	}()
}

// spoken is one finished call, carried back from the goroutine that made it.
type spoken struct {
	answer Answer
	err    error
}

// take is one turn: the thinking face while it runs, the answer when it lands,
// and the deadline when it does not.
func (s *steering) take(ctx context.Context, message inboundMessage, said string) {
	// The mark goes on before the turn starts, because the minutes between
	// somebody typing and an answer landing are exactly where silence reads as
	// not-listening. It is never a gate: a workspace that will not take a mark
	// costs the message its mark and nothing else.
	s.markIn(ctx, s.sink.conversationOf(message), message.ts, notify.ReceiptUnderConsideration)
	bounded, stop := context.WithTimeout(ctx, s.deadline)
	// The call goes beside the wait rather than inside it, so what the operator
	// waits for is this function's own deadline and not the callee's willingness
	// to observe a cancelled context. The channel holds one so a call that comes
	// back after nobody is listening still finishes rather than blocking on the
	// send for as long as the process lives.
	spoke := make(chan spoken, 1)
	go func() {
		// Both of these belong to the call rather than to the wait: the context is
		// released when the call it bounds is actually over, and the turn's guard
		// is given back then too, so a message arriving while an abandoned call is
		// still running is told the product manager is busy instead of being sent
		// to contend for a conversation it cannot have.
		defer stop()
		defer s.finish()
		answer, err := s.conversation.Say(bounded, said)
		spoke <- spoken{answer: answer, err: err}
	}()
	select {
	case came := <-spoke:
		s.came(ctx, message, bounded, came)
	case <-bounded.Done():
		// An answer that landed in the same instant the wait ran out is still an
		// answer, and a select between two ready cases picks at random. So the call
		// is asked once more before the thread is told nothing came.
		select {
		case came := <-spoke:
			s.came(ctx, message, bounded, came)
		default:
			s.gaveUp(ctx, message, bounded)
		}
	}
}

// came reports one call that finished: what it said, and what stopped it.
func (s *steering) came(ctx context.Context, message inboundMessage, bounded context.Context, came spoken) {
	// What the turn managed to say before it failed is worth reading, so it goes
	// into the thread ahead of the account of the failure rather than behind it.
	if text := strings.TrimSpace(came.answer.Text); text != "" {
		s.reply(ctx, message, came.answer, text)
	}
	if came.err != nil {
		s.failed(ctx, message, bounded, came)
		return
	}
	if strings.TrimSpace(came.answer.Text) == "" {
		s.answerOnce(ctx, message, conversationSilent, "a turn that said nothing")
		return
	}
	s.sink.log("%s was answered %s", message.user, from(came.answer))
	s.markIn(ctx, s.sink.conversationOf(message), message.ts, notify.ReceiptSettled)
}

// gaveUp says in the thread that this client stopped waiting, and is the one
// path here that runs while something underneath may still be going.
//
// A sink being shut down is not a wait running out, and the two are told apart
// because only one of them is worth a message: a workspace call made with a dead
// context cannot land anyway, so the stopping case is put in the log where
// whoever is watching this process will see it.
func (s *steering) gaveUp(ctx context.Context, message inboundMessage, bounded context.Context) {
	if !errors.Is(bounded.Err(), context.DeadlineExceeded) {
		s.sink.log("the sink stopped while the Lead Product Manager was answering %s, so the answer will not be posted; `yoyo chat` continues the conversation and shows where it got to", message.user)
		return
	}
	s.answerOnce(ctx, message, fmt.Sprintf(conversationOverdue, s.deadline), "the wait running out")
	s.markIn(ctx, s.sink.conversationOf(message), message.ts, notify.ReceiptRefused)
}

// from names the conversation an answer came out of, for a log line. A command
// this client refused opened no conversation, so there is none to name and
// saying so beats printing an empty one.
func from(answer Answer) string {
	if answer.ConversationID == "" {
		return "without anything being said to the Lead Product Manager"
	}
	return fmt.Sprintf("from conversation %s, which stands at %d turn(s)", answer.ConversationID, answer.Turns)
}

// reply posts what came back, in the product manager's own name and face where
// the product manager is what said it.
func (s *steering) reply(ctx context.Context, message inboundMessage, answer Answer, text string) {
	speaker := notify.Persona(domain.RoleProductManager, "")
	if answer.Harness {
		speaker = notify.Harness()
	}
	// What the Lead Product Manager answered is a role's own words, which no
	// rendering has passed through, so each work item it names is shown with
	// its state. What the harness answered for a command is the read model's
	// rendering already, and is posted as it is.
	if !answer.Harness {
		text = s.sink.citing.cite(ctx, text)
	}
	if err := s.sink.answerAs(ctx, speaker, message, text, conversationElsewhere); err != nil {
		if ctx.Err() != nil {
			return
		}
		// The turn happened and is recorded whichever way this went, so what failed
		// is the account of it rather than the act. The line below is what points
		// whoever is watching this log at where the answer actually is.
		s.sink.log("%s was answered %s and the answer could not be posted, so it reads as silence there; `yoyo chat` continues the conversation: %v",
			message.user, from(answer), err)
	}
}

// failed says in the thread why a turn produced no answer, telling the four
// cases apart because they are four different things for the operator to do.
//
// The last two share a shape and differ in who is blamed. A turn that failed is
// the product manager not answering, in the provider's own words. A decision
// that failed never reached the product manager at all — the harness carried it
// out, or could not — so it is said as the harness's own failure, under the
// harness's own name, and the product manager is not named as the speaker of a
// refusal it never made.
func (s *steering) failed(ctx context.Context, message inboundMessage, bounded context.Context, came spoken) {
	switch {
	case errors.Is(bounded.Err(), context.DeadlineExceeded):
		s.answerOnce(ctx, message, fmt.Sprintf(conversationOverdue, s.deadline), "the wait running out")
	case errors.Is(came.err, runstate.ErrConversationHeld):
		s.answerOnce(ctx, message, conversationHeld, "the conversation being held elsewhere")
	case came.answer.Harness:
		s.answerOnce(ctx, message, fmt.Sprintf(decisionFailed, came.err), "why the decision could not be carried out")
	default:
		s.answerOnce(ctx, message, fmt.Sprintf(conversationFailed, came.err), "why it could not answer")
	}
	s.markIn(ctx, s.sink.conversationOf(message), message.ts, notify.ReceiptRefused)
}

// begin takes the one turn this sink runs at a time, reporting whether it got it.
func (s *steering) begin() bool {
	s.turn.Lock()
	defer s.turn.Unlock()
	if s.taking {
		return false
	}
	s.taking = true
	return true
}

// finish gives it back.
func (s *steering) finish() {
	s.turn.Lock()
	defer s.turn.Unlock()
	s.taking = false
}

// settle waits for a turn the connection set off to finish. A sink that returned
// while one was still running would leave a goroutine writing to a log its
// process is done with, and would exit before the thread was told anything.
func (s *steering) settle() {
	s.turns.Wait()
}
