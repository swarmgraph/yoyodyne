package slack

// The outbound half of the decision tier: a line that has stopped is put to the
// operators where they will actually see it, as a question with an answer.
//
// Everything else this sink says is addressed to a channel, and a channel is the
// right place for an account of work: it is a narrative somebody reads when they
// come to it. A stopped line is not that. It is the one thing the harness cannot
// get past on its own, it is addressed to particular people rather than to
// whoever is reading, and the difference between it being seen in ten minutes
// and in ten hours is the whole cost of the state. So it is a direct message,
// and it is sent to every operator rather than to one: a decision addressed to a
// room is one each of them can reasonably assume somebody else is making.
//
// It is the degraded class of direct message the slack-reporting design admits
// — the system choosing nothing over ready work, which only a person fixes — and
// the state it is about is exactly the one the read model derives and the
// heartbeat says in the channel. Nothing here decides that the line is stopped;
// the feed hands this the ask beside the channel line, from the one derivation.
//
// The shape is two messages and it is deliberate. The top line is brief and
// carries the ask, because it is what a phone shows on a lock screen and what a
// mention in a sidebar has room for; the context, the age, what is waiting
// behind it and the options are threaded underneath, where somebody who has
// decided to deal with it is looking. An operator who reads only the first line
// still knows they are being waited on, which is the failure this whole tier
// exists to end.
//
// Nothing here acts on the answer. What a reply does is record a directive in
// the same durable record `yoyo directive record` writes and every run consults,
// which is what makes this a way of reaching the existing controls rather than a
// second set of them: no state is decided here, no role is invoked from here,
// and an operator who never replies has lost nothing but the message.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
)

// Ask is one decision the operators are owed: what stopped the line, since when,
// how much is waiting behind it, and the answers that are offered.
//
// It is derived where the line itself is derived and carried here unchanged. A
// surface that composed its own account of what a held line means would be a
// second opinion about the record, which is exactly the disagreement an operator
// cannot adjudicate.
type Ask struct {
	// Mark is the standing state this is about, in the same words the heartbeat
	// names it by, so one state is one ask however many hours it stands.
	Mark string
	// Stopped is what has stopped the line, in the words the channel is using for
	// it at the same moment.
	Stopped string
	// Since is when it became that way. The age is what makes the state worth
	// putting to somebody rather than leaving in a channel.
	Since time.Time
	// Ready is how much admitted work the tracker calls ready behind it, which is
	// what the decision actually costs while it is not made.
	Ready int
	// Options are the answers offered, numbered by their order here.
	Options []string
}

// ask puts one stopped line to every operator who has not been asked about it.
//
// It is never a gate and never reports an error to its caller, for the reason
// nothing else in this package is: the messages of this pass are already posted
// and their cursors written, the durable records are unaffected either way, and
// a workspace that will not take a direct message must not stop the sink
// reporting into the channel. What it costs is that one message, said in the
// sink's own log.
func (s *Sink) ask(ctx context.Context, asking *Ask) {
	// A sink assembled without the directive record steers nothing, so a reply to
	// this could not be recorded and the ask would be a question with nowhere for
	// the answer to go. A product that has granted nobody has nobody to ask.
	if asking == nil || s.steering == nil || len(s.operators) == 0 {
		return
	}
	asked, err := s.store.LoadDecisions()
	if err != nil {
		s.log("the line has been stopped since %s but what has already been asked could not be read, so nobody was asked again: %v",
			asking.Since.UTC().Format(time.RFC3339), err)
		return
	}
	// This is the only thing that adds to that map, so it is the only place that
	// has to bound it. The state being asked about now is never forgotten, and
	// what goes is the oldest of the rest — asks whose state cleared long enough
	// ago that nobody is still about to answer them.
	//
	// A write that fails here is read past rather than returned on: the pruning is
	// housekeeping, the asking is the point, and the map carried on with below is
	// the pruned one either way — so the next entry recorded persists it.
	if asked.Forget(asking.Mark, maxRememberedAsks) {
		if err := s.store.SaveDecisions(asked); err != nil {
			s.log("the oldest asks could not be forgotten, so the decision map is larger than it should be: %v", err)
		}
	}
	for _, member := range s.operators {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		// One message per person per state. The channel says it again every hour
		// while it stands, which is the cadence for a room somebody scrolls; a
		// direct message repeated hourly is the nagging that gets an app muted, and
		// a muted app is silence exactly where this needed to be heard.
		if asked.AskedOf(asking.Mark, member) {
			continue
		}
		put, err := s.put(ctx, member, *asking)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// One operator's direct message failing says nothing about the next
			// one's — a person who has never opened this app is a refusal about
			// them alone — so the rest are still asked.
			s.log("the stopped line could not be put to %s, so they were not asked; it is put to them again at the next heartbeat: %v", member, err)
			continue
		}
		asked.Record(put)
		if err := s.store.SaveDecisions(asked); err != nil {
			// The question is already in front of them and the answer is what
			// cannot be read: this map is the only thing that says that thread was
			// an ask, so a reply into it would be a number nothing can interpret.
			// It is said here and asked again at the next heartbeat, which puts a
			// thread somebody can answer in front of them a second time rather than
			// leaving the first one dead.
			s.log("%s was asked about the stopped line, but that they were asked could not be remembered, so a reply in that thread will not be read as the decision: %v", member, err)
			return
		}
	}
}

// put opens the direct message with one operator and asks them, reporting what
// was asked and where so a reply in that thread can be read as the answer.
//
// The two messages are posted in the order they are read, and the map entry is
// written by the caller only once both have landed. A sink killed between them
// leaves a top line with no options under it and nothing recorded, so the next
// heartbeat asks again — a repeated question rather than a thread whose numbers
// mean nothing, which is the same trade the cursors take and for the same
// reason.
func (s *Sink) put(ctx context.Context, member string, asking Ask) (Decision, error) {
	channel, err := s.openConversation(ctx, member)
	if err != nil {
		return Decision{}, fmt.Errorf("open the direct message with %s: %w", member, err)
	}
	identity := s.appearance.Identity(notify.Harness())
	emoji, url := icon(identity.Avatar)
	opened, err := s.postCited(ctx, Message{
		Channel:   channel,
		Text:      topLine(asking),
		Username:  identity.Name,
		IconEmoji: emoji,
		IconURL:   url,
	})
	if err != nil {
		return Decision{}, err
	}
	if _, err := s.postCited(ctx, Message{
		Channel:   channel,
		Text:      threadedAsk(asking),
		ThreadTS:  opened,
		Username:  identity.Name,
		IconEmoji: emoji,
		IconURL:   url,
	}); err != nil {
		return Decision{}, err
	}
	s.log("asked %s about the line being stopped: %s", member, asking.Stopped)
	return Decision{
		Mark:     asking.Mark,
		Member:   member,
		Channel:  channel,
		ThreadTS: opened,
		Stopped:  asking.Stopped,
		Options:  asking.Options,
		AskedAt:  s.clock().UTC(),
	}, nil
}

// topLine is the whole of what somebody sees before they decide whether to open
// this: that the harness is stopped, what by, and that it is waiting on them.
//
// It says the ask rather than describing the situation, because a notification
// that reads as information is one people learn to leave for later, and the
// state it is about is one that costs an hour of the machine doing nothing for
// every hour it is left.
func topLine(asking Ask) string {
	return fmt.Sprintf("*Nothing is being started, and it is waiting on you.* %s. %s — reply in this thread to decide.",
		asking.Stopped, waitingBehind(asking.Ready))
}

// waitingBehind says what the decision costs while it is not made, in items
// rather than in adjectives.
func waitingBehind(ready int) string {
	if ready == 1 {
		return "1 admitted item is ready to pull behind it"
	}
	return fmt.Sprintf("%d admitted items are ready to pull behind it", ready)
}

// threadedAsk is the context and the options, threaded under the top line: the
// state in full, how long it has stood, and what can be answered.
//
// The options are numbered because a number is what somebody types one-handed on
// a phone, and each one is a sentence rather than a word because the reply is
// recorded verbatim into a durable record other people read later — "1" on its
// own would be a decision nobody can reconstruct, so what is recorded is the
// sentence the number stood for.
func threadedAsk(asking Ask) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Stopped by: %s\n", asking.Stopped)
	fmt.Fprintf(&rendered, "Since: %s\n", asking.Since.UTC().Format(time.RFC3339))
	fmt.Fprintf(&rendered, "Ready to pull: %d\n\n", asking.Ready)
	if len(asking.Options) > 0 {
		rendered.WriteString("Reply with a number:\n")
		for index, option := range asking.Options {
			fmt.Fprintf(&rendered, "%d. %s\n", index+1, option)
		}
		rendered.WriteString("\n")
	}
	rendered.WriteString("Anything else you type is recorded in your own words instead. " +
		"Either way the reply is the decision: it is recorded as an operational directive " +
		"in the same record `yoyo directive` writes and every run reads, and nothing here " +
		"carries it out on its own.")
	return rendered.String()
}
