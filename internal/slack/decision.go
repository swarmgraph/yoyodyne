package slack

// The inbound half of the decision tier: a reply in the thread the ask was made
// in is the decision.
//
// There is no other way to answer, and that is the point rather than a
// limitation. A button would be a control surface with its own vocabulary, an
// approval link would be a second authority over the work, and a command to type
// somewhere else would be a decision made at a terminal — which is the terminal
// the operator was not sitting at, which is why they were asked here. The reply
// is in the thread that asked, so what was decided is beside what was asked, and
// anybody reading it afterwards has both.
//
// It creates no machinery, exactly as a reply in a work item's thread creates
// none. What a decision records is one operational directive in the durable
// record every run consults: the same kind, the same store, the same reading,
// and no pause — the operator's answer is in force from the moment it is
// recorded and nothing waits on it. The one difference from a thread reply is
// the scope, and it is the honest one. A reply in an item's thread is about that
// item; this was asked about the whole line, so what it records is unscoped, and
// an unscoped directive is what the record already means by direction that
// reaches everything.
//
// Nothing here carries the decision out. The harness does not lift its own
// operator hold because somebody typed 1, and it must not: the switches are the
// operator's, the records are what agents read, and a surface that acted on its
// own reading of a chat message would be a second thing deciding what the
// harness does.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
)

// optionWord is the one word the grammar knows, so "option 2" and "2" are the
// same answer. Somebody replying to a numbered list writes both, and refusing
// one of them would be the channel correcting an operator's typing.
const optionWord = "option"

// choice is what one reply to an ask chose.
type choice struct {
	// option is the offered answer the reply named, numbered from one, and zero
	// where the reply named none. Zero is ordinary rather than a failure: the
	// options are shortcuts, and an operator who says something the list did not
	// anticipate has still decided.
	option int
	// said is the operator's own words, kept whole whichever they typed. It is
	// what is recorded, because a directive that said only "option 2" would be a
	// decision nobody reading the record later could reconstruct.
	said string
}

// parseDecision reads one reply to an ask, against the number of options that
// ask offered.
//
// The grammar is one rule and one refusal. A reply that opens with one of the
// offered numbers chose it; everything else is a decision in the operator's own
// words, which is the reading that cannot silently discard what somebody said.
//
// The refusal is the number that names no option. It would be read as prose
// under the rule above — "5" recorded verbatim is a perfectly good directive —
// and that is exactly the failure worth refusing: somebody who typed 5 was
// answering the list, believed they had, and would never find out that what the
// harness recorded was the digit rather than the answer. So it says how many
// there are, and nothing is written down until they say again.
func parseDecision(raw string, options int) (choice, error) {
	said := strings.TrimSpace(raw)
	if said == "" {
		return choice{}, errors.New("the reply said nothing that could be recorded as a decision")
	}
	word, rest := firstWord(said)
	if strings.EqualFold(word, optionWord) {
		named, _ := firstWord(rest)
		if named == "" {
			return choice{}, fmt.Errorf("`option <1-%d>` — say which one; they are numbered in the message this replies to, or say what you want in your own words", options)
		}
		word = named
	}
	// The list is read the way it is written, so the trailing punctuation of
	// somebody quoting an entry back — "2." or "2)" — is the numbering rather than
	// part of what they said.
	chosen, err := strconv.Atoi(strings.TrimRight(word, ".):"))
	if err != nil {
		return choice{said: said}, nil
	}
	if chosen < 1 || chosen > options {
		if options == 0 {
			return choice{}, errors.New("this ask offered no numbered options; say what you want in your own words")
		}
		return choice{}, fmt.Errorf("there is no option %d; they are numbered 1 to %d, or say what you want in your own words", chosen, options)
	}
	return choice{option: chosen, said: said}, nil
}

// decideThread answers one message that arrived somewhere other than the channel
// this sink reports in, which is a direct message.
//
// A conversation this sink asked nothing in is left alone, for the reason a
// channel thread it never opened is: somebody messaging the app about something
// else is having their own conversation, and an app that answered it would be
// talking over them. The decision map is the whole of the correlation — it is
// what says a thread was an ask, which state it was about, and what the numbers
// in it stood for.
//
// A message typed in the conversation rather than in the ask's thread is
// answered rather than dropped, and it is the case worth being careful about. It
// is the likeliest way somebody answers at all: a phone notification opens the
// flat conversation, so the reply that took the least effort to send is the one
// that lands outside the thread. Nothing can be recorded from it — which ask it
// answers is exactly what the thread was carrying, and a conversation may hold
// several — so what it gets is the one thing that is never acceptable to
// withhold: being told it was not recorded, and where to say it again.
//
// A message that @-mentions this app is the other thing never met with silence,
// here as in the reporting channel: outside an ask's thread it goes through the
// mention door exactly as it would at the top of the channel, answered in the
// conversation it was said in. That is read ahead of the decision map, so a map
// that cannot be read never costs somebody who addressed the app their answer.
// Inside an ask's thread the reply is the decision, mention or not, because that
// thread exists to take one.
func (s *steering) decideThread(ctx context.Context, message inboundMessage) {
	if message.threadTS == "" && s.addressed(ctx, message) {
		s.mentioned(ctx, message)
		return
	}
	decisions, err := s.sink.store.LoadDecisions()
	if err != nil {
		s.sink.log("a reply arrived in a direct message but what was asked there could not be read, so it was not acted on: %v", err)
		return
	}
	if message.threadTS == "" {
		latest, found := decisions.Latest(message.channel)
		if !found {
			return
		}
		if !s.first(message.ts) {
			return
		}
		standing, known := s.standingMark()
		s.answerDecision(ctx, message, misplaced(latest, standing, known))
		return
	}
	asked, found := decisions.Lookup(message.channel, message.threadTS)
	if !found {
		// A thread this sink asked nothing in is somebody's own, and nothing is
		// recorded from it; but a message in it that addresses this app is still
		// answered there, as it would be in a channel thread this sink never opened.
		s.mentioned(ctx, message)
		return
	}
	// Remembered only once this is a reply into one of this sink's own asks, for
	// the reason a channel reply is: an ordinary conversation must not push the
	// replies that decide something out of what one process remembers.
	if !s.first(message.ts) {
		return
	}
	s.answerDecision(ctx, message, s.decide(asked, message, time.Now()))
}

// standingMark is the state the heartbeat is currently standing on, nothing
// where it is standing on none, and false where it cannot say.
//
// It is read from the heartbeat's own cursor rather than derived again here,
// because the cursor is where the delivery pass records which state it is
// repeating — the same mark every ask is remembered under — so the two halves of
// this connection cannot disagree about whether an ask is still the live one.
// A cursor that cannot be read is reported as that rather than as nothing
// standing: "nothing standing" is a positive claim that the line is moving, and
// it is one the sink cannot make about a line it could not read.
func (s *steering) standingMark() (string, bool) {
	cursors, err := s.sink.store.LoadCursors()
	if err != nil {
		s.sink.log("which state the line is standing on could not be read, so a misplaced reply was answered without it: %v", err)
		return "", false
	}
	return cursors.Streams[heartbeatStream].Standing, true
}

// misplaced is what somebody is told when they answered in the conversation
// instead of in the thread that asked.
//
// It says what was not recorded before it says what to do, because the first
// thing they need is that the decision they believe they made has not been
// made. What comes after depends on whether the ask it would point them at is
// still the live one. Where it is, naming it is what makes the instruction
// followable in a conversation that holds more than one: "reply in the thread"
// is useless advice when there are three of them. Where it is not, the pointer
// would send somebody to a thread about a line that is already moving — a
// state that cleared weeks ago and a thread about to be forgotten — so what is
// said instead is that it has cleared and that nothing is waiting on them here.
// Where whether it still stands could not be read, neither is claimed: the
// thread is named, and so is the fact that the sink could not tell, because
// "it has cleared" said of a line that is still stopped sends somebody away from
// the one decision the line is waiting on.
func misplaced(latest Decision, standing string, known bool) string {
	const unrecorded = "Nothing was recorded: a decision has to be a reply inside the thread that asked, and this was said in the conversation instead. "
	if !known {
		return unrecorded + fmt.Sprintf("The last ask here was about %s. Whether it still stands could not be read just now; if it does, reply under that message and what you say is recorded.", latest.Stopped)
	}
	if latest.Mark == standing {
		return unrecorded + fmt.Sprintf("The ask about %s is above — reply under that message and what you say is recorded.", latest.Stopped)
	}
	return unrecorded + fmt.Sprintf("The last ask here, about %s, has since cleared, so nothing is waiting on a decision from you now; "+
		"when the line stops again you are asked in a new thread. `yoyo directive record` at the terminal is how a direction is recorded meanwhile.", latest.Stopped)
}

// decide is what one reply to an ask does, and the sentence that says so. Every
// branch produces one: an operator who answered and heard nothing back cannot
// tell being ignored from being unheard, and this is the one message in the
// workspace that exists because somebody was asked a question.
//
// The answer names no identifier, for the reason a channel acknowledgment names
// none: the person being answered typed what is read back to them, and a slug
// is the one thing in the answer they would have to go and resolve. What they
// are owed is which option was taken and the sentence it stood for; the
// identifier is for the processes that read directives, and `yoyo directive
// list` is where a person reads it.
func (s *steering) decide(asked Decision, message inboundMessage, at time.Time) string {
	// Authority first, and before the reply is read at all — the same rule the
	// channel holds to, because it is the same record being written. Somebody
	// forwarded this ask, or shares a machine, is not somebody the project
	// granted direct-work.
	if !s.operators[message.user] {
		return "Nothing was recorded: this project has not granted you direct-work with a bound Slack member id, and a decision is only taken from somebody who holds it. `operators` in .yoyodyne/config.yaml is where that grant lives."
	}
	chosen, err := parseDecision(message.text, len(asked.Options))
	if err != nil {
		return "Nothing was recorded. " + err.Error()
	}
	if _, err := s.recordDecision(asked, chosen, at); err != nil {
		return "Nothing was recorded. " + err.Error()
	}
	if chosen.option > 0 {
		return fmt.Sprintf("Recorded — option %d, %s. Every run reads it from here; nothing carries it out on its own, and `yoyo directive list` is where it is read back.",
			chosen.option, asked.Options[chosen.option-1])
	}
	return "Recorded, in your own words. Every run reads it from here; nothing carries it out on its own, and `yoyo directive list` is where it is read back."
}

// recordDecision writes the decision where every process that acts on this
// product's work reads it.
//
// It is unscoped, and that is the one thing that separates it from a reply in a
// work item's thread. The ask was about the line rather than about any item, so
// there is no item to scope it to and naming one would be the surface inventing
// a subject; an empty scope is what the record already means by direction that
// reaches everything, which is what a decision about the whole line is.
//
// It is operational, so it pauses nothing. An operator answering a question
// about a stopped line is unblocking the harness rather than adding a second
// thing for somebody to settle, and a decision that stopped work would be this
// tier making the state it exists to end.
func (s *steering) recordDecision(asked Decision, chosen choice, at time.Time) (directive.Directive, error) {
	id, err := directive.NewID()
	if err != nil {
		return directive.Directive{}, err
	}
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     s.sink.store.Product(),
		Kind:          directive.KindOperational,
		// A decision about the whole line is the product manager's to act on, which
		// is where a directive that named no receiver already goes. The ask named
		// no role and an operator answering a number is not addressing one.
		ReceivedBy: domain.RoleProductManager,
		ReceivedAt: at.UTC(),
		Text:       decided(asked, chosen),
	}
	// Every bound a directive is held to is the directive package's, checked on
	// the way into the store, so a reply too long to be one is refused in the
	// thread with the record's own words rather than with a second opinion.
	if err := s.directives.Record(recorded); err != nil {
		return directive.Directive{}, err
	}
	return recorded, nil
}

// decided is what the directive says: what was asked, which offered answer was
// taken, and the operator's own words.
//
// All three, because a record holding any one of them alone is unreadable later.
// The words alone lose what question they answered — "leave it" says nothing six
// weeks on. The number alone is a decision nobody can reconstruct once the
// options have been reworded. And the option's own sentence is the harness's
// wording rather than the operator's, which is the one thing a directive is not
// allowed to paraphrase.
func decided(asked Decision, chosen choice) string {
	if chosen.option == 0 {
		return fmt.Sprintf("asked about the line being stopped (%s), decided: %s", asked.Stopped, chosen.said)
	}
	return fmt.Sprintf("asked about the line being stopped (%s), chose option %d — %s — saying: %s",
		asked.Stopped, chosen.option, asked.Options[chosen.option-1], chosen.said)
}

// answerDecision says in the ask's own thread what the reply did, addressed to
// whoever wrote it.
//
// It is composed here rather than rendered by the notifier, which is the seam
// every message into the channel goes through. What that seam addresses is a
// topic, and a topic is a thread in the reporting channel: this is a
// conversation with one person about the line, which is not a topic and has no
// thread in that map. Sending it through the notifier would put the answer to a
// direct message at the top of the channel, in front of everybody except the
// person who asked for it.
func (s *steering) answerDecision(ctx context.Context, message inboundMessage, answer string) {
	identity := s.sink.appearance.Identity(notify.Harness())
	emoji, url := icon(identity.Avatar)
	if _, err := s.sink.postCited(ctx, Message{
		Channel:   message.channel,
		ThreadTS:  message.threadTS,
		Text:      tagged(message.user, answer),
		Username:  identity.Name,
		IconEmoji: emoji,
		IconURL:   url,
	}); err != nil {
		if ctx.Err() != nil {
			return
		}
		// The record is already written wherever one was written, so what failed is
		// the account of it rather than the act. `yoyo directive list` is where an
		// operator who saw no answer finds out which it was.
		s.sink.log("a decision was acted on but could not be answered in the direct message it was made in; `yoyo directive list` says what was recorded: %v", err)
	}
}
