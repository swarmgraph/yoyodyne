package slack

// The inbound half: a reply in a work item's thread reaches the work as a
// durable directive.
//
// It creates no directive machinery, and that is the whole of why a thread is
// allowed to steer anything. A reply is a new *receiver* for the record `yoyo
// directive record` already writes — same kinds, same pause semantics, same
// resolution, same store the run pipeline consults before it starts a run,
// before it resumes one, and before it puts a change through its gate. A
// directive recorded from a thread is indistinguishable downstream from one
// typed at a terminal, which is what "enforceable regardless of which agent
// received it" costs and all it costs. Nothing here can weaken directive
// governance, because nothing here has governance of its own to weaken.
//
// Four rules hold the channel to that:
//
// Only an instruction reaches the record. A reply is read for what it is for
// before anything is written down — the directive package's own stated rule,
// not a classifier — and a question is carried to the product manager's
// conversation instead, with her answer returned to the thread; a reply the
// rule will not decide is asked back in one line. The record used to take
// everything, and on 2026-08-30 it took the operator's question about a phrase
// in a receipt as a standing instruction and acknowledged it with the same
// phrase. A question in the directive record is a directive nobody gave, and
// enforceability presumes there are none.
//
// The pausing kinds are stated, never inferred. A reply opening `ambiguous:` or
// `artifact:` records that kind and must say what is unresolved; everything else
// is operational. A classifier deciding which sentences stop work is exactly
// what the command line already refused, and it would be worse here, where the
// input is chat. The intent reading above cannot stop work either: at most it
// answers, or asks.
//
// Authority defaults closed. A reply acts only when its Slack member id belongs
// to a human the project granted direct-work, and the derivation of that list is
// the configuration's rather than this file's. An unlisted reply is answered in
// the thread saying it was not acted on, because a channel that silently ignores
// some people looks broken rather than closed — and a reply from somebody the
// project does not recognize at all is a different case with a different answer,
// which is stranger.go's.
//
// Every message this reads is answered in its own thread — the directive as
// recorded, or the refusal with its reason — and the answer tags whoever wrote
// it. The answer names no identifier anywhere: the person being answered typed
// what is being read back to them, and a slug in an acknowledgment is the one
// thing in it they would have to go and resolve. The identifier the record keeps
// is for the processes that read directives, which is where it stays.
//
// Nothing this reads asks for one either. A reply settles what this thread is
// waiting on by saying how it was settled, and which directive that is comes out
// of the read model — because a channel that showed nobody an identifier and then
// required one would send an operator to a terminal to look up the name of
// something they paused from their phone.
//
// An operator who steers from a phone has nothing else to tell them whether they
// were heard, and a thread they are not looking at is
// indistinguishable from silence. The reply itself wears where its directive
// stands as well: heard while the directive is open, settled once it is, refused
// where nothing was recorded at all.
//
// What is recorded here is also remembered here: which thread each directive was
// said in, by whom, and in which message. The durable directive record holds none
// of it, deliberately — it is the product's record of what was directed, the same
// whichever way it arrived — so the sink keeps its own note, and the delivery pass
// reads it to say what later became of what somebody asked for, where they asked
// and to them.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Directives is the durable directive record as the inbound half writes to it:
// recording one, and settling one an operator names. It is an interface here for
// the reason the feed is — the sink is assembled from the parts it needs rather
// than reaching for the state root itself — and it is deliberately these two
// methods and no more. Reading directives is the run pipeline's, and a reporting
// process that could revise one would be a second authority over the record.
//
// A reply that settles what this thread is waiting on without naming it needs to
// know which directive that is, and that read is not here: it is the read model's,
// through the same source the four lines are assembled from (readmodel.Pausing).
// The two halves stay apart on purpose — this one writes what the operator said,
// and the derivation of which directives hold an item is one reading that the run
// pipeline enforces on and every surface projects.
type Directives interface {
	Record(recorded directive.Directive) error
	Resolve(reference, resolution string, at time.Time) (directive.Directive, error)
}

const (
	// The stated openings. A reply beginning with one of them records that kind
	// and has to say what is unresolved; a reply beginning with neither is
	// operational, which is the ordinary case and the one that stops nothing.
	ambiguousOpening = "ambiguous:"
	artifactOpening  = "artifact:"
	// resolveVerb settles a directive somebody recorded. A reply that names one
	// settles it by any prefix of its identifier that names exactly one — the same
	// rule the command line follows, because it is the same store answering — and a
	// reply that names none settles whatever single directive is holding this
	// thread's own item. The second form is the ordinary one here: no message
	// posted in this channel carries an identifier, so a thread that could only
	// settle a named directive would send the operator to a terminal to read the
	// name of the thing they paused from their phone.
	resolveVerb = "resolve"
	// maxHeldBytes bounds how much of one directive's unresolved question the
	// refusal for an ambiguous settlement carries. Several of them go into one
	// sentence, and what each is waiting on is a paragraph somebody wrote at
	// whatever length they wanted.
	maxHeldBytes = 160
	// socketEventsAPI is the envelope type an event arrives in. Everything else on
	// the connection — a hello, a disconnect, an interaction this app does not
	// enable — is not a message and is not read here.
	socketEventsAPI = "events_api"
	// maxRememberedReplies bounds what one process remembers having acted on. It
	// exists for one case: Slack redelivers an envelope whose acknowledgment did
	// not reach it, and a redelivered reply recorded twice is two directives an
	// operator has to resolve for one thing they said. A few hundred is far more
	// than a redelivery window holds, and the memory is per process rather than
	// durable because the redelivery is too.
	maxRememberedReplies = 256
)

// steering is what the sink does with what arrives on its connection.
type steering struct {
	sink *Sink
	// directives is where a reply in one of this sink's threads is recorded. It is
	// nil on a sink assembled to carry the conversation and nothing else, and a
	// reply that reaches a nil one is refused in its thread rather than acted on.
	directives Directives
	// conversation is the durable product-manager conversation a message
	// addressed to this app reaches, and is nil on a sink assembled without one.
	// deadline bounds one turn taken through it; turn guards taking one at a time
	// and turns is what an ending sink waits on. All four are conversation.go's.
	conversation Conversation
	deadline     time.Duration
	turn         sync.Mutex
	taking       bool
	turns        sync.WaitGroup
	// queueMu guards the queue of messages waiting for a turn in progress, wake
	// is how a turn ending tells the queue to try again at once, and retry is how
	// often it tries anyway. All three are queue.go's.
	queueMu sync.Mutex
	wake    chan struct{}
	retry   time.Duration
	// operators is the allow-list, by Slack member id. It is derived from the
	// project's operator mapping — the humans granted direct-work who bound a
	// member id — and an empty one is a product nobody may steer from Slack,
	// which is what every workspace gets until somebody names themselves.
	operators map[string]bool
	// recognized is who the project knows, by Slack member id: every human the
	// same mapping names, whatever they were granted. It is wider than the
	// allow-list, and the difference is the whole of what separates a colleague
	// who may not steer from somebody this app has never heard of.
	recognized map[string]bool
	// contacts is what those humans are called, which is who a stranger is told
	// to reach out to instead.
	contacts []string
	// mu guards what this process has already acted on, because replies arrive on
	// the connection's goroutine while the delivery loop runs on its own.
	mu      sync.Mutex
	acted   map[string]bool
	ordered []string
	// member is this app's own Slack member id, which is what a message has to
	// name to be addressed to it. It is learned from the workspace — at startup
	// where that call worked, and lazily otherwise — and it is under the same
	// mutex for the same reason: both are what the connection remembers.
	member string
}

// newSteering wires the inbound half from the options the sink was assembled
// with. It takes them whole rather than one list at a time because three of them
// are three readings of one mapping, and a caller that could pass them
// separately is a caller that could pass them from different configurations.
func newSteering(sink *Sink, options Options) *steering {
	deadline := options.ConversationDeadline
	if deadline <= 0 {
		deadline = DefaultConversationDeadline
	}
	return &steering{
		sink:         sink,
		directives:   options.Directives,
		conversation: options.Conversation,
		deadline:     deadline,
		wake:         make(chan struct{}, 1),
		retry:        DefaultQueueRetry,
		operators:    membership(options.Operators),
		recognized:   membership(options.Recognized),
		contacts:     options.Contacts,
		acted:        map[string]bool{},
	}
}

// membership is one list of Slack member ids as it is asked about: by id, with
// whatever a configuration left blank dropped.
func membership(members []string) map[string]bool {
	set := make(map[string]bool, len(members))
	for _, member := range members {
		if trimmed := strings.TrimSpace(member); trimmed != "" {
			set[trimmed] = true
		}
	}
	return set
}

// handle reads one inbound envelope and, where it is a reply in a thread this
// sink opened, answers it in that thread. Which conversation it arrived in says
// which of the two things it is: a reply in the reporting channel steers the
// work item its thread is about, and a reply in a direct message is the answer
// to a decision this sink asked for there.
//
// It reports nothing to its caller and stops nothing when it fails. Reporting is
// an observation rather than a gate in both directions: a reply that could not be
// recorded is said in the thread it arrived in and in the sink's own log, and the
// connection carries on reading. What an operator must never get is silence, so
// every path out of here either answers in the thread or says why it could not.
func (s *steering) handle(ctx context.Context, envelope socketEnvelope) {
	message, ok := readInbound(envelope)
	if !ok {
		return
	}

	// Which conversation it arrived in is which half it belongs to, and the two
	// are not variations of each other. A reply in the reporting channel is about
	// the thread's work item and is scoped to it; a reply anywhere else is in a
	// direct message, which this sink only ever opens to ask an operator to decide
	// something about the whole line. The channel is the sink's own, so the test
	// is exact rather than a guess about what a conversation id looks like — and
	// a message in some other channel entirely is one this sink asked nothing in,
	// which decision.go leaves exactly as alone as it always was.
	if message.channel != s.sink.channel {
		s.decideThread(ctx, message)
		return
	}

	// A message at the top of the channel steers nothing — there is no thread and
	// so no item to scope a directive to — but it is not therefore nothing. One
	// that addresses this app is answered where it was said, which is mention.go's
	// and is the second stated exception to answering only inside this sink's own
	// threads.
	if message.threadTS == "" {
		s.mentioned(ctx, message)
		return
	}

	// The thread map is the correlation: a reply carries the timestamp of the
	// message its thread hangs from, and that message is the one this sink posted
	// to open a topic. A thread this sink did not open is somebody's own
	// conversation in the channel, and nothing is recorded from it — but a message
	// in one that addresses this app is still answered there, by the same door the
	// top of the channel gets.
	threads, err := s.sink.store.LoadThreads()
	if err != nil {
		s.sink.log("a reply arrived but the thread map could not be read, so it was not acted on: %v", err)
		return
	}
	key, found := topicOf(threads, s.sink.channel, message.threadTS)
	if !found {
		s.mentioned(ctx, message)
		return
	}
	// Remembered only once this is a reply in one of this sink's own threads, so
	// an ordinary busy channel cannot push the replies that steer the work out of
	// what one process remembers having acted on.
	if !s.first(message.ts) {
		return
	}
	// Somebody this project does not recognize is told so once in this thread and
	// read no further, which is stranger.go's. It is decided before the thread is
	// resolved to a topic and before anything is parsed, for the reason the
	// authority check below it is: what a stranger typed is not something this
	// harness should be reading, and the answer they are owed does not depend on
	// what they said.
	if !s.knows(message.user) {
		s.refuse(ctx, message)
		return
	}
	topic, err := notify.ParseTopic(key)
	if err != nil {
		// The thread is this sink's — it is in the map — so somebody typed into one
		// of ours and nothing can be said back: a message is addressed to a topic,
		// and this thread is recorded against something that names none. The reply
		// is marked refused rather than left bare, because a reply wearing nothing
		// is one nobody read, and this one was.
		s.sink.log("a reply arrived in a thread recorded against %q, which names no topic, so it could only be marked: %v", key, err)
		s.mark(ctx, message.ts, notify.ReceiptRefused)
		return
	}

	// The reply wears where its directive stands, on the message the operator
	// typed. The thinking face goes on before anything is decided, because the gap
	// between somebody typing and the answer landing is exactly where silence reads
	// as not-listening — and it is never a gate, so a workspace that will not take
	// a mark costs the reply its mark and nothing else.
	s.mark(ctx, message.ts, notify.ReceiptUnderConsideration)
	answered, question := s.act(ctx, topic, message, time.Now())
	s.answer(ctx, threads, message.user, answered)
	// A question goes to the product manager only once its receipt is in the
	// thread, so the answer follows the line that promised it rather than racing
	// it — or, where a turn is already running, the line saying it is queued
	// behind that turn. The turn marks the reply settled when the answer lands, or
	// refused when nothing could be said, which is conversation.go's.
	if question != "" {
		s.send(ctx, message, question)
		return
	}
	// Only a disposition that has already landed is marked here. Recording a
	// directive is not disposing of it: what settles one is somebody carrying it
	// out or deciding what it asked, which the delivery pass reads out of the
	// record and marks at the moment it says so in the thread. Until then the
	// thinking face is the true answer to where it got to.
	if receipt, settled := disposition(answered); settled {
		s.mark(ctx, message.ts, receipt)
	}
}

// mark puts one receipt on the reply it is about, and says so in the sink's log
// where the workspace refused. Nothing waits on it: what a reply did is in the
// thread, in words, and this is the same fact where somebody scrolling their own
// messages is looking.
func (s *steering) mark(ctx context.Context, messageTS string, receipt notify.Receipt) {
	s.markIn(ctx, s.sink.channel, messageTS, receipt)
}

// markIn is mark on a message in a named conversation, for the doors that answer
// a message wherever it was addressed to this app rather than only in the
// reporting channel.
func (s *steering) markIn(ctx context.Context, channel, messageTS string, receipt notify.Receipt) {
	if err := s.sink.receiptIn(ctx, channel, messageTS, receipt); err != nil {
		// A sink being shut down mid-mark is not a workspace refusing anything, and
		// the line below is the one the setup document teaches an operator to read
		// as a missing scope.
		if ctx.Err() != nil {
			return
		}
		s.sink.log("a reply could not be marked as %s, so where its directive stands is only said in the thread: %v", receipt, err)
	}
}

// disposition is the mark a reply wears now, and whether it has one yet at all.
//
// Two of the three land here. A refusal recorded nothing and is over the moment
// it is said, and a reply that settled a directive is itself the settlement. A
// reply that recorded one is neither: the directive it wrote down is open, so
// the thinking face stands and the check mark is the outcome half's to put on
// when the record says somebody settled it.
func disposition(answer notify.Notification) (notify.Receipt, bool) {
	switch answer.Event.Kind {
	case notify.KindDirectiveRefused:
		return notify.ReceiptRefused, true
	case notify.KindDirectiveResolved, notify.KindDirectiveCarriedOut:
		return notify.ReceiptSettled, true
	default:
		return "", false
	}
}

// act decides what one reply does, and produces the message that says so. Every
// branch produces one: the acknowledgment is the whole of what an operator has
// to go on, and a path that produced nothing would be the channel quietly
// ignoring somebody.
//
// The second value is the one thing a reply can ask for beyond its
// acknowledgment: a question to carry to the product manager, framed for her,
// once the receipt is posted. It is empty for everything else, and whoever posts
// the receipt owes the conversation exactly one send, which carries it now or
// queues it behind a turn in progress.
func (s *steering) act(ctx context.Context, topic notify.Topic, message inboundMessage, at time.Time) (notify.Notification, string) {
	// Authority first, and before the reply is even read: what an unlisted person
	// typed is not something the harness should be parsing, let alone recording —
	// or carrying to the product manager, which admits work and spends money and
	// is held to the same grant. Whoever reaches here is somebody the project
	// recognizes, so the refusal names the grant they are missing rather than
	// telling a colleague this app has never heard of them.
	if !s.operators[message.user] {
		return refused(topic, at, "the reply is from somebody this project has not granted direct-work with a bound Slack member id, so nothing was recorded; `operators` in .yoyodyne/config.yaml is where that grant lives"), ""
	}
	parsed, err := parseSteer(message.text)
	if err != nil {
		return refused(topic, at, err.Error()), ""
	}
	// What the reply is for is decided before anything about recording it is,
	// because a question never reaches the record and an uncertain reply is asked
	// about rather than written down. The fixture this exists for is a question
	// recorded as a standing instruction and acknowledged with the phrase it was
	// asking about; neither of these branches can produce that.
	switch {
	case parsed.asks():
		return s.ask(topic, message, parsed.text, at)
	case parsed.uncertain():
		return refused(topic, at, askedBack), ""
	}
	// A sink assembled to carry the conversation and nothing else reads replies —
	// it has to, or a message addressed to this app inside a thread would reach
	// nobody — and has nowhere to write one down. That is said here rather than
	// left as a reply that vanished, which is the failure the whole inbound half
	// is built not to have.
	if s.directives == nil {
		return refused(topic, at, "this sink was assembled without the directive record, so a reply in a thread steers nothing; `yoyo directive record` is how one is recorded"), ""
	}
	// A directive from a thread is scoped to the thread's work item, so a thread
	// that is not about a work item has nothing to scope one to. Recording it
	// unscoped would reach every item in the product, which is a far larger thing
	// than anybody replying to an exchange meant.
	if topic.Kind != notify.TopicWorkItem {
		return refused(topic, at, "this thread is not a work item's, and a directive from a thread is scoped to the item the thread is about; `yoyo directive record` is how one is recorded against something else"), ""
	}
	if parsed.settles() {
		// A reply that named no directive is settling what this thread's own item is
		// waiting on, which is a question about the record rather than about the
		// reply — so it is asked of the read model, and a thread with nothing or with
		// several things open is told so in words rather than settling something the
		// operator did not name.
		reference := parsed.resolves
		if reference == "" {
			held, err := s.holding(topic)
			if err != nil {
				return refused(topic, at, err.Error()), ""
			}
			reference = held
		}
		resolved, err := s.directives.Resolve(reference, parsed.resolution, at)
		if err != nil {
			return refused(topic, at, err.Error()), ""
		}
		// What became of it is said here, by this reply, so the delivery pass must
		// not say it again when it reads the same settlement out of the record —
		// unless this is not the thread that asked for it, in which case the thread
		// that did has still heard nothing and is still owed the outcome.
		s.said(ctx, resolved.ID, topic)
		return acknowledged(topic, settledKind(resolved), resolved, at), ""
	}
	recorded, err := s.record(topic, message, parsed, at)
	if err != nil {
		return refused(topic, at, err.Error()), ""
	}
	return acknowledged(topic, notify.KindDirectiveRecorded, recorded, at), ""
}

// askedBack is the one line a reply gets when the reading would not say whether
// it was a question or an instruction. It asks rather than guesses, because
// either guess is a real cost: a question recorded is a directive nobody gave,
// and an instruction answered is direction that never reached the work. Nothing
// is recorded, and the reply is marked refused, which is what a reply that
// recorded nothing wears.
const askedBack = "that reads as either a question or an instruction, so nothing was done with it — end it with a question mark to ask the Lead Product Manager, or say it as an instruction to record it"

// ask is a question in a thread: the receipt that says it was heard as one, and
// the question itself framed for the product manager, whose answer follows the
// receipt into the same thread.
//
// The receipt is a kind of its own rather than a refusal, because nothing was
// refused. It names the item's thread rather than the question — the question is
// the message directly above it, typed by the person being answered, and reading
// it back would restate the very phrase they may be asking about, which is the
// second defect in the screenshot this exists for.
//
// A sink with no conversation behind it refuses, and says to ask at `yoyo chat`
// instead. A product manager already mid-turn is not a refusal: the question is
// queued behind that turn by whoever posts the receipt, so the promise of an
// answer still holds.
func (s *steering) ask(topic notify.Topic, message inboundMessage, question string, at time.Time) (notify.Notification, string) {
	if s.conversation == nil {
		return refused(topic, at, "that reads as a question, and this sink was started without the Lead Product Manager's conversation to carry it to; nothing was recorded, and `yoyo chat` is where to ask it"), ""
	}
	s.sink.log("a question arrived in the thread of %s from %s, saying %q, and is being taken to the Lead Product Manager rather than recorded",
		topic.Key(), message.user, singleLine(message.text, maxAskedBytes))
	return heard(topic, question, at), framed(topic, question)
}

// framed is the question as the product manager hears it: the operator's own
// words, with where they were asked in front of them. A question in a work
// item's thread is about that item more often than not — "did you restart?",
// "what does this receipt mean?" — and the conversation has no other way to
// know which item a thread is about.
func framed(topic notify.Topic, question string) string {
	if topic.Kind == notify.TopicWorkItem {
		return fmt.Sprintf("Asked in the Slack thread for work item %s: %s", topic.ID, question)
	}
	return fmt.Sprintf("Asked in the Slack thread for %s: %s", topic.Key(), question)
}

// heard is the receipt for a question: heard, not recorded, and answered next.
// The question rides on the event's text for the record, and the voice line
// deliberately does not read it back.
func heard(topic notify.Topic, question string, at time.Time) notify.Notification {
	return notify.Notification{
		Topic:   topic,
		Speaker: notify.Harness(),
		Event: notify.Event{
			Kind:     notify.KindQuestionHeard,
			At:       at.UTC(),
			Severity: report.SeverityNote,
			Refs:     notify.Refs{WorkItemID: workItemOf(topic)},
			Text:     question,
		},
	}
}

// holding is the directive this thread's item is waiting on, for a reply that
// settled one without naming it. It reports the identifier the record answers to,
// or the sentence saying why nothing was settled.
//
// The reading is the read model's rather than this file's, and that is the
// architect's standing ruling rather than a choice made here: an answer this sink
// gives in a thread comes from the read model, per the stated exception in
// decision 5 of docs/designs/slack-reporting-design.md, and the
// surfaces-project-one-read-model invariant holds every surface to the shared
// derivation rather than to one of its own. Which directives hold an item is what
// the run pipeline enforces on, so a channel that worked it out its own way could
// lift a pause the pipeline still holds. Nothing here revises anything — what
// settles a directive is still the store's Resolve, given a name this only read.
//
// A thread with nothing open and a thread with several open are both refused in
// words. Settling something the operator did not name would be the channel
// picking which of their pauses to lift, which is the one thing a bare settlement
// must not do, so the refusal says what is open by what each is waiting on rather
// than by an identifier nothing in this channel ever showed them.
func (s *steering) holding(topic notify.Topic) (string, error) {
	// A sink with no read model at all and a sink whose read model was given no
	// directive record are the same thing to whoever is typing: nobody can say what
	// is holding this item up, and the next step is at a terminal either way.
	if s.sink.sources == nil || s.sink.sources.Directives == nil {
		return "", errors.New("what is holding this item up cannot be read from here, because this sink was started without the record it would read; " + namesThem)
	}
	held, err := readmodel.Pausing(*s.sink.sources, topic.ID)
	if err != nil {
		return "", fmt.Errorf("what is holding this item up could not be read, so nothing was settled: %v; %s", err, namesThem)
	}
	switch len(held) {
	case 0:
		return "", errors.New("nothing is holding this item up, so there is nothing here to settle; a reply that does not open with `resolve` is recorded as a directive of its own")
	case 1:
		return held[0].ID, nil
	default:
		return "", fmt.Errorf("%d things are holding this item up and the reply named none of them, so nothing was settled — %s. %s",
			len(held), waitingOn(held), namesThem)
	}
}

// namesThem ends every refusal that could not settle for want of knowing which
// directive was meant. None of them can name one in the thread — no message
// posted here carries an identifier — so each says the two places the operator's
// next step is.
const namesThem = "`yoyo directive list` at the terminal names what is open, and `resolve <name> <how it was settled>` back here settles the one you name"

// waitingOn says what several open directives are each waiting on, in the words
// whoever recorded them used, and how far each one reaches. It is what a refusal
// names them by: the identifier is what the record answers to and is nowhere a
// person reading this channel could have got it from.
//
// How far one reaches is said because they do not all reach the same distance. A
// directive that named no scope is holding every item in the product, and one
// that named several is holding those, so a reader choosing between them is
// choosing between pauses of different sizes and has nothing else here to tell
// them apart.
func waitingOn(held []directive.Directive) string {
	waiting := make([]string, 0, len(held))
	for _, one := range held {
		reach := "one is"
		switch {
		case len(one.Scope) == 0:
			reach = "one is holding every item in this product and is"
		case len(one.Scope) > 1:
			reach = fmt.Sprintf("one is holding %d items and is", len(one.Scope))
		}
		waiting = append(waiting, fmt.Sprintf("%s waiting on %q", reach, singleLine(one.Unresolved, maxHeldBytes)))
	}
	return strings.Join(waiting, ", and ")
}

// record writes one directive where every process that acts on this product's
// work reads it. The scope is the thread's item and nothing wider: a reply in one
// item's thread is about that item, and an unscoped directive — which is what an
// empty scope means — would pause the whole product from a message about one
// piece of it.
func (s *steering) record(topic notify.Topic, message inboundMessage, parsed steer, at time.Time) (directive.Directive, error) {
	id, err := directive.NewID()
	if err != nil {
		return directive.Directive{}, err
	}
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     s.sink.store.Product(),
		Kind:          parsed.kind,
		ReceivedBy:    parsed.receivedBy,
		ReceivedAt:    at.UTC(),
		Text:          parsed.text,
		Artifact:      parsed.artifact,
		Unresolved:    parsed.unresolved,
		Scope:         []string{topic.ID},
	}
	// Every bound a directive is held to is the directive package's, checked on
	// the way into the store. A reply too long to be one is refused in the thread
	// with the record's own words rather than with a second opinion about them.
	if err := s.directives.Record(recorded); err != nil {
		return directive.Directive{}, err
	}
	s.remember(recorded, topic, message)
	return recorded, nil
}

// remember keeps where one directive was said, who said it, and in which
// message, which is the whole of what the durable directive record deliberately
// does not hold: it is the product's record of what was directed, the same
// whichever way it arrived, and a Slack member id is a fact about this workspace.
//
// It is what lets the delivery pass answer somebody later — in the thread they
// asked in, by name — when the record says what became of what they asked for,
// and what lets the mark on their own message move when it does. Failing to keep
// it costs exactly that and nothing else: the directive is already recorded and
// already enforced, so this is said and read past rather than turned into a reply
// that reports a failure the operator cannot act on.
//
// The map is the connection's to write. This runs on the goroutine that reads
// replies, which is the only thing that writes it, so a read and a write with
// nothing between them is safe here in the way it would not be for the thread
// map the delivery pass owns.
func (s *steering) remember(recorded directive.Directive, topic notify.Topic, message inboundMessage) {
	steers, err := s.sink.store.LoadSteers()
	if err != nil {
		s.sink.log("directive %s was recorded from a thread, but where it was said could not be read, so what becomes of it will not be answered there: %v", recorded.ID, err)
		return
	}
	steers.Record(recorded.ID, Steer{
		Member:     message.user,
		Topic:      topic.Key(),
		Message:    message.ts,
		RecordedAt: recorded.ReceivedAt,
	})
	if err := s.sink.store.SaveSteers(steers); err != nil {
		s.sink.log("directive %s was recorded from a thread, but where it was said could not be remembered, so what becomes of it will not be answered there: %v", recorded.ID, err)
	}
}

// said marks a directive whose outcome this half has already put in the thread
// that asked for it, so the delivery pass reading the same settlement out of the
// record leaves it alone — and moves the mark on the message that asked from
// heard to settled, which is the same flip the pass makes when it is the one
// that says the outcome.
//
// Which thread the settlement was said in is the whole of the test. A directive
// nothing here recorded is not in the map and needs no mark. One settled from
// some other item's thread is answered there, where somebody asked about it, and
// the thread that actually asked for it has heard nothing — so it is left unsaid
// and the pass still owes it the outcome, which is the case this exists for.
func (s *steering) said(ctx context.Context, directiveID string, topic notify.Topic) {
	steers, err := s.sink.store.LoadSteers()
	if err != nil {
		s.sink.log("directive %s was settled from a thread, but what this sink has already said could not be read, so the settlement may be said there twice: %v", directiveID, err)
		return
	}
	steer, found := steers.Lookup(directiveID)
	if !found || steer.Topic != topic.Key() {
		return
	}
	steer.Said = true
	steers.Record(directiveID, steer)
	if err := s.sink.store.SaveSteers(steers); err != nil {
		s.sink.log("directive %s was settled from a thread, but that this sink said so could not be remembered, so it may be said there twice: %v", directiveID, err)
	}
	// The reply that asked for it has been wearing the thinking face since it
	// arrived, and this is the moment it stops being true. It is marked whether or
	// not the line above could be written down: the settlement is what the mark is
	// about, and a mark set twice is the same mark.
	if steer.Message != "" {
		s.mark(ctx, steer.Message, notify.ReceiptSettled)
	}
}

// answer posts one acknowledgment into the thread the reply arrived in,
// addressed to whoever wrote it.
//
// It tags them rather than relying on them to come back and look. A thread is
// where the narrative belongs, but a thread nobody has open is silence, and this
// is the one message in the channel that exists because a person said something
// and is waiting to hear whether it landed.
//
// The thread already exists by construction — the reply was correlated through
// it — so the poster is not given the capability to open one. That is what keeps
// this goroutine off the thread map the delivery pass owns: the map here is a
// copy this goroutine loaded, and writing it back over a map that has moved since
// would lose whatever thread the pass opened in between.
func (s *steering) answer(ctx context.Context, threads ThreadMap, member string, notification notify.Notification) {
	into := &poster{sink: s.sink, threads: &threads, mention: member}
	if err := notification.Notify(ctx, notify.New(into, s.sink.appearance)); err != nil {
		// The record is already written at this point wherever one was written, so
		// what failed is the account of it rather than the act. An operator who saw
		// no answer has `yoyo directive list`, and this line is what points them at
		// it.
		s.sink.log("a reply was acted on but could not be acknowledged in its thread; `yoyo directive list` says what was recorded: %v", err)
	}
}

// first reports a reply this process has not acted on yet, and remembers it. A
// redelivered envelope is Slack repeating itself rather than an operator saying
// something twice, and recording it twice would leave two directives to resolve
// for one instruction.
func (s *steering) first(ts string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acted[ts] {
		return false
	}
	s.acted[ts] = true
	s.ordered = append(s.ordered, ts)
	if len(s.ordered) > maxRememberedReplies {
		delete(s.acted, s.ordered[0])
		s.ordered = s.ordered[1:]
	}
	return true
}

// inboundMessage is one Slack message event reduced to what this reads: who said
// it, what they said, which thread they said it in, and which conversation that
// thread is in. A message at the top of a conversation is in no thread, and its
// thread is empty rather than its own timestamp — what it is not in is the whole
// of what the caller reads it for.
//
// The conversation is carried rather than checked away here because it is what
// says which half of this connection the message belongs to: the reporting
// channel is a reply about a work item, and anything else is a direct message
// answering an ask. Deciding that inside the reader would put the routing in the
// one place that cannot see either map.
type inboundMessage struct {
	user     string
	text     string
	ts       string
	threadTS string
	channel  string
}

// readInbound reads a message a person typed, and reports whether the envelope
// was one at all.
//
// What it refuses is as important as what it accepts. A message with a subtype
// or a bot id is not a person typing — an edit, a join, a file share, and above
// all this sink's own posts, which arrive back on the same connection and would
// otherwise be read as instructions the harness gave itself. Which conversation
// it was said in is reported rather than refused, and the caller routes on it:
// a message in another channel is still not this sink's business even when one
// workspace runs two, and it is the caller that knows which channel is its own.
//
// A message at the top of the channel is read rather than refused, which it was
// not until an operator asked the app three questions there and got silence.
// Reading it is not acting on it: what steers work is still only a reply in one
// of this sink's own threads, and what a top-level message gets is an answer.
func readInbound(envelope socketEnvelope) (inboundMessage, bool) {
	if envelope.Type != socketEventsAPI || len(envelope.Payload) == 0 {
		return inboundMessage{}, false
	}
	var payload struct {
		Event struct {
			Type     string `json:"type"`
			Subtype  string `json:"subtype"`
			User     string `json:"user"`
			BotID    string `json:"bot_id"`
			Text     string `json:"text"`
			TS       string `json:"ts"`
			ThreadTS string `json:"thread_ts"`
			Channel  string `json:"channel"`
		} `json:"event"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return inboundMessage{}, false
	}
	event := payload.Event
	if event.Type != "message" || event.Subtype != "" || strings.TrimSpace(event.BotID) != "" {
		return inboundMessage{}, false
	}
	if strings.TrimSpace(event.Channel) == "" || strings.TrimSpace(event.User) == "" {
		return inboundMessage{}, false
	}
	// A message that carries its own timestamp as its thread's is the message a
	// thread hangs from rather than a reply into one, so it is read as what it is:
	// something said at the top of the conversation.
	threadTS := strings.TrimSpace(event.ThreadTS)
	if threadTS == event.TS {
		threadTS = ""
	}
	return inboundMessage{
		user:     strings.TrimSpace(event.User),
		text:     event.Text,
		ts:       event.TS,
		threadTS: threadTS,
		channel:  strings.TrimSpace(event.Channel),
	}, true
}

// topicOf finds the topic a thread belongs to. The map is kept the other way
// round — a topic is looked up to post into its thread — and it is walked here
// rather than indexed because it is one entry per topic this product has ever
// reported on, read once per reply somebody types.
func topicOf(threads ThreadMap, channel, threadTS string) (string, bool) {
	for key, thread := range threads.Threads {
		if thread.Channel == channel && thread.ThreadTS == threadTS {
			return key, true
		}
	}
	return "", false
}

// steer is what one reply asks for, once it has been read.
type steer struct {
	// resolution is set for exactly the resolve verb: how the directive was
	// settled. resolves is which directive to settle, and it is empty where the
	// reply named none — which is a reply settling whatever single directive is
	// holding this thread's item, read at the moment it is acted on rather than
	// here.
	resolves   string
	resolution string
	// The rest describe a directive to record. The kind is operational unless the
	// reply stated one of the two that pause work.
	kind       directive.Kind
	receivedBy domain.AgentRole
	artifact   string
	unresolved string
	text       string
	// intent is what a reply that stated nothing is for, read by the directive
	// package's own rule: an instruction to record, a question to answer, or
	// something to ask back about. A reply that stated a kind or settled a
	// directive said what it was for, so it is an instruction here whatever shape
	// its words take — `ambiguous: which did you mean?` is the operator's own
	// question, recorded as one on purpose.
	intent directive.Intent
}

// settles reports a reply that settles a directive rather than recording one. How
// it was settled is the whole of the test: it is set for the resolve verb and for
// nothing else, while the directive it settles is the thread's own wherever the
// reply named none.
func (parsed steer) settles() bool { return parsed.resolution != "" }

// asks reports a reply that is a question, which is answered rather than
// recorded. uncertain reports one the reading would not decide, which is asked
// back rather than guessed at.
func (parsed steer) asks() bool      { return parsed.intent == directive.IntentQuestion }
func (parsed steer) uncertain() bool { return parsed.intent == directive.IntentUncertain }

// parseSteer reads a reply. The grammar is small on purpose: everything it does
// not recognize is read for what it is for — a question to answer, or an
// instruction recorded as an operational directive in the operator's own words —
// and only the second is the reading that can reach the record. Neither can
// silently stop work: the pausing kinds are stated by the person or not at all.
//
// A refusal names what to type instead, because the person reading it is in a
// chat client rather than at a terminal with the usage text in front of them.
func parseSteer(raw string) (steer, error) {
	said, receivedBy := addressed(raw)
	if said == "" {
		return steer{}, errors.New("the reply said nothing that could be recorded")
	}
	parsed := steer{kind: directive.KindOperational, receivedBy: receivedBy, text: said, intent: directive.IntentInstruction}
	lowered := strings.ToLower(said)
	switch {
	case lowered == resolveVerb || strings.HasPrefix(lowered, resolveVerb+" "):
		settlement := strings.TrimSpace(said[len(resolveVerb):])
		if settlement == "" {
			return steer{}, errors.New("`resolve <how it was settled>` — say how it was settled; the work resumes on the answer rather than on the act of answering")
		}
		// A reply that opens with something that could name a directive is settling
		// that one, which is how a thread settles something recorded elsewhere. A
		// reply that does not is settling what this thread is waiting on, and the
		// whole of it is how it was settled: nothing posted in this channel carries
		// an identifier, so the ordinary reply here has none to give.
		if reference, rest := firstWord(settlement); directive.ValidReference(reference) {
			if rest == "" {
				return steer{}, errors.New("`resolve <how it was settled>` — say how it was settled; the work resumes on the answer rather than on the act of answering")
			}
			return steer{resolves: reference, resolution: rest}, nil
		}
		return steer{resolution: settlement}, nil
	case strings.HasPrefix(lowered, ambiguousOpening):
		// What was said is recorded whole, and the part after the opening is what
		// has to be settled. They are the same words here because a reply that says
		// it is ambiguous is saying what nobody can act on.
		parsed.kind = directive.KindAmbiguous
		parsed.unresolved = strings.TrimSpace(said[len(ambiguousOpening):])
		if parsed.unresolved == "" {
			return steer{}, errors.New("`ambiguous: <what nobody can act on without deciding>` — say what is unresolved; a pause nobody can name a reason for is a pause nobody can lift")
		}
	case strings.HasPrefix(lowered, artifactOpening):
		parsed.kind = directive.KindArtifact
		parsed.artifact, parsed.unresolved = firstWord(said[len(artifactOpening):])
		if parsed.artifact == "" {
			return steer{}, errors.New("`artifact: <name> <what has to be decided about it>` — name the governed document this changes")
		}
		if parsed.unresolved == "" {
			return steer{}, errors.New("`artifact: <name> <what has to be decided about it>` — say what has to be decided; work derived from that document waits until somebody does")
		}
	default:
		// A reply that stated nothing is the one whose words have to say what it
		// is for. The screenshot this exists for was a question recorded as a
		// standing instruction and acknowledged with the phrase it asked about;
		// reading the intent here is what keeps a question out of the record.
		parsed.intent = directive.ReadIntent(said)
	}
	return parsed, nil
}

// addressed reads who a reply is for and returns what is left of it.
//
// The persona is named in the plain `@role` a reader would type, because the
// personas are display names on one app's messages rather than members of the
// workspace: there is nothing for Slack's own mention to point at. A reply that
// names no role is the product manager's, which is where a directive that named
// no receiver already goes.
//
// Who received a directive is attribution and never routing — the record reaches
// every role whichever one is named — so a mention that is not a role is left in
// the words rather than refused. Somebody who wrote a name the harness does not
// know still said something, and dropping their sentence over its first word
// would be the channel deciding it knew better.
func addressed(raw string) (string, domain.AgentRole) {
	said := strings.TrimSpace(raw)
	// A Slack mention of the app itself arrives as <@Uxxxx>. It addresses the bot,
	// which is not a persona and not a role anything records, so it is dropped
	// rather than read as an addressee.
	if strings.HasPrefix(said, "<@") {
		if _, after, found := strings.Cut(said, ">"); found {
			said = strings.TrimSpace(after)
		}
	}
	word, rest := firstWord(said)
	if named, mentioned := strings.CutPrefix(word, "@"); mentioned {
		if role := domain.AgentRole(strings.ToLower(strings.Trim(named, ":,"))); role.Valid() {
			return rest, role
		}
	}
	return said, domain.RoleProductManager
}

// firstWord splits the first word off a value and returns what follows it, both
// trimmed. It is how the two openings that take a name read theirs: a directive
// id and an artifact name are single words, and everything after is prose.
func firstWord(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	index := strings.IndexFunc(trimmed, unicode.IsSpace)
	if index < 0 {
		return trimmed, ""
	}
	return trimmed[:index], strings.TrimSpace(trimmed[index:])
}

// acknowledged is the message that says what a reply did: the directive as
// recorded or as settled, in the thread it was said in.
//
// A directive that pauses work is a warning rather than a note, so it is shown in
// the channel as well as in the thread. Work stopping is the news somebody who
// has opened no threads most needs, and it is stopped by their own instruction,
// which is exactly when the confirmation matters.
//
// The operator's own words go back into the channel they came from. Nothing is
// redacted on this path and nothing needs to be: the text is already in the
// thread, said by the person it belongs to, and this is the record quoting it
// back with what became of it. The envelope still carries the directive's
// identifier in its references, which is where the delivery pass and anything
// else tracing the record find it — the message rendered from it says none.
func acknowledged(topic notify.Topic, kind notify.Kind, recorded directive.Directive, at time.Time) notify.Notification {
	severity := report.SeverityNote
	text := recorded.Text
	if settlement(kind) {
		// The directive's own words are already in the thread, said by the person
		// who typed them; what they are owed now is what became of it — and, where
		// what was lifted was holding more than this item, that it was.
		text = recorded.Resolution
		if reach := reached(recorded); reach != "" {
			text += " " + reach
		}
	} else if recorded.Pauses() {
		severity = report.SeverityWarning
	}
	return notify.Notification{
		Topic:   topic,
		Speaker: notify.Harness(),
		Event: notify.Event{
			Kind:     kind,
			At:       at.UTC(),
			Severity: severity,
			Refs:     notify.Refs{WorkItemID: workItemOf(topic), DirectiveID: recorded.ID},
			Detail: notify.Detail{
				ReceivedBy: recorded.ReceivedBy.Title(),
				Artifact:   recorded.Artifact,
				// What the directive left unsettled is what says whether the work is
				// paused, and a resolved one has left nothing.
				Unresolved: unsettled(recorded),
			},
			Text: text,
		},
	}
}

// withdrawn is the message that says a directive somebody asked for in a thread
// was taken back, and why. It is the one account of a directive that is not a
// settlement: nothing was carried out and nothing was answered, and saying it
// as either would claim something the record refuses to. What the thread is owed
// is the withdrawal's own words — and, where what was taken back was holding
// more than this item, that it was.
//
// It is said in the voice of the role the withdrawal was made under, which is
// the conversation the operator took it back in or the agent that took it back
// at a command line; one the operator made at a terminal was made under nobody's
// persona and is said by the harness, as the operator's own switches are.
func withdrawn(topic notify.Topic, recorded directive.Directive) notify.Notification {
	text := recorded.Withdrawal
	if reach := reached(recorded); reach != "" {
		text += " " + reach
	}
	speaker := notify.Harness()
	if recorded.WithdrawnRole != "" {
		speaker = notify.Persona(recorded.WithdrawnRole, "")
	}
	return notify.Notification{
		Topic:   topic,
		Speaker: speaker,
		Event: notify.Event{
			Kind:     notify.KindDirectiveWithdrawn,
			At:       recorded.WithdrawnAt.UTC(),
			Severity: report.SeverityNote,
			Refs:     notify.Refs{WorkItemID: workItemOf(topic), DirectiveID: recorded.ID},
			Detail: notify.Detail{
				ReceivedBy: recorded.ReceivedBy.Title(),
				Artifact:   recorded.Artifact,
			},
			Text: text,
		},
	}
}

// refused is the message that says a reply recorded nothing, and why. It is said
// in the thread rather than only in the sink's log for the reason the whole
// inbound half is acknowledged: a person who steers from a phone has nothing else
// to tell them whether they were heard, and silence reads as either.
func refused(topic notify.Topic, at time.Time, why string) notify.Notification {
	return notify.Notification{
		Topic:   topic,
		Speaker: notify.Harness(),
		Event: notify.Event{
			Kind:     notify.KindDirectiveRefused,
			At:       at.UTC(),
			Severity: report.SeverityNote,
			Refs:     notify.Refs{WorkItemID: workItemOf(topic)},
			Detail:   notify.Detail{Reason: why},
		},
	}
}

// reached says what a settled pause was holding beyond the item whose thread it
// was settled in, and says nothing where it was holding that item alone.
//
// It is here because a reply need not name what it settles: the thread settles
// what is holding its own item, and a directive that named no scope is holding
// every item in the product. Somebody lifting one from their phone would
// otherwise have no way to tell a pause on this piece of work from a pause on all
// of it — the thread they are in says one item, and the record says more. It
// counts the work rather than naming it, because which items is the record's to
// hold and an identifier is the one thing this acknowledgment does not carry.
func reached(recorded directive.Directive) string {
	if !recorded.Kind.Pauses() {
		return ""
	}
	switch {
	case len(recorded.Scope) == 0:
		return "It was holding every item in this product rather than this one alone, so it is lifted for all of them."
	case len(recorded.Scope) > 1:
		return fmt.Sprintf("It was holding %d items rather than this one alone, so it is lifted for all of them.", len(recorded.Scope))
	default:
		return ""
	}
}

// settlement reports the two kinds that say what became of a directive rather
// than what it said. They are one predicate here because the acknowledgment
// treats them identically — the account is the disposition, not the operator's
// words — and only the voice they are said in differs.
func settlement(kind notify.Kind) bool {
	return kind == notify.KindDirectiveResolved || kind == notify.KindDirectiveCarriedOut
}

// settledKind is how a settled directive is reported: as a resolution where it
// paused work, and as an outcome where it never did. It is read from the record
// rather than from whoever is posting, so the two halves of this connection can
// never come to describe one settlement differently.
func settledKind(recorded directive.Directive) notify.Kind {
	if recorded.Kind.Pauses() {
		return notify.KindDirectiveResolved
	}
	return notify.KindDirectiveCarriedOut
}

// unsettled is what a directive is still waiting on, which is nothing at all once
// somebody has settled it.
func unsettled(recorded directive.Directive) string {
	if recorded.Resolved() {
		return ""
	}
	return recorded.Unresolved
}

func workItemOf(topic notify.Topic) string {
	if topic.Kind == notify.TopicWorkItem {
		return topic.ID
	}
	return ""
}
