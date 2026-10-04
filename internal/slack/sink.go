package slack

// The sink itself: read the durable records, post what has not been posted, and
// remember how far it got.
//
// The ordering here is the whole of the delivery guarantee. A message is posted
// and then its cursor is advanced, so a process killed between the two repeats
// one message when it comes back. The other order would lose one instead, and a
// lost message is a lie about the work while a repeated one is only a repetition
// — the durable record is authoritative, and this is a view of it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

const (
	// DefaultPollInterval is how often the sink reads the durable records. It is
	// seconds rather than milliseconds because nothing here is a live feed: the
	// records are files other processes write, and a channel that is a few
	// seconds behind a terminal is a channel nobody is waiting on.
	DefaultPollInterval = 15 * time.Second
	// maxTextBytes bounds one posted message. It is far inside what Slack
	// accepts, and deliberately so: the bound that matters is the one past which
	// a message stops being readable in a channel, not the one past which the
	// API refuses it. The notifier bounds the body before this ever sees it, so
	// what this catches is the reference line pushing one over; either way a
	// message is cut with a marker naming the record that holds the whole, and
	// never split into a flood of messages.
	maxTextBytes = 3800
	// passBackoff is how long the sink waits after a pass that could not finish.
	// Nothing is lost by waiting: the cursors are on disk, so the next pass
	// resumes exactly where this one stopped.
	passBackoff = 30 * time.Second
	// refusalBackoff is how long the sink waits after a refusal that will still
	// be a refusal next time — an app nobody invited to the channel, a token
	// somebody revoked. It is minutes rather than seconds because nothing about
	// waiting less would help: what clears it is a person, and asking a
	// workspace to refuse the same call every fifteen seconds until they get to
	// it is noise in their audit log and ours. The wait is still finite, so an
	// operator who fixes it gets reporting back without restarting anything.
	refusalBackoff = 5 * time.Minute
)

// Options is everything the sink needs. Every field is required except the
// optional ones named as such, because a sink assembled with a missing piece
// would discover it while a run was in flight.
type Options struct {
	// Channel is where threads are opened.
	Channel string
	// Avatars is the project's per-speaker override of the picture beside a name.
	// It is optional, and a sink given none posts every persona under the avatar
	// the harness ships.
	Avatars notify.Avatars
	// Store holds the thread map and the cursors, and says which product they are
	// for. That product is what every speaker's name is qualified by, so a
	// workspace reading two harnesses can tell which one is talking without
	// opening a thread. It is taken from the store rather than named again here:
	// a sink holding one product's state and another's name is a channel of
	// misattributed messages, and a name that cannot be given separately cannot
	// disagree with the state it is posted beside.
	Store *Store
	// API posts.
	API *API
	// Feed says what there is to post.
	Feed Feed
	// Titles is what an item is called, asked for at the one moment no record can
	// answer: opening a thread for an item whose first appearance in the channel
	// is an event that carried no title. It is optional, and a sink assembled
	// without one heads such a thread with the identifier alone — which is a name
	// somebody has to go and resolve, so every sink the harness builds is given
	// one.
	Titles Titles
	// Poll is the interval between passes; zero takes the default.
	Poll time.Duration
	// Log is where the sink says what it is doing. It is the operator's only
	// window onto a process that otherwise runs silently, and it is never given
	// a token to print.
	Log func(format string, args ...any)
	// Identity is what this process records about itself while it is running, so
	// something other than the sink can tell a sink that is merely alive from one
	// that is the right build, reading the right configuration, launched with
	// this project's secrets. The sink fills in what only it knows -- its pid,
	// the channel, the workspace -- so a caller supplies the rest. It carries no
	// credential; see Presence.
	Identity Presence
	// Directives is where a thread reply is recorded, which is the existing
	// directive record every run already consults. It is optional: a sink
	// assembled without one reports and does not steer, and every reply on its
	// connection is acknowledged to Slack and read no further.
	Directives Directives
	// Standing is where the harness stands, as the read model derives it. The
	// sink reads it for two things the feed does not: answering somebody who
	// asked, in the channel, where things stand, and — for a reply settling what a
	// thread is waiting on without naming it — which directives are holding that
	// thread's item. It is the same value the feed is given rather than a second
	// assembly of one, because a channel and a terminal disagreeing about one
	// standing is a disagreement only the operator could adjudicate, and the same
	// holds for which pause is in force.
	//
	// It is optional, and a sink assembled without one says so when it is asked
	// rather than answering with an empty standing.
	Standing *readmodel.Sources
	// Operators is the allow-list of Slack member ids whose replies are acted on,
	// derived by the configuration from the humans it granted direct-work. It
	// defaults to empty, which is a product nobody may steer from a thread — so a
	// workspace changes nothing about how it behaves until an operator names
	// themselves.
	Operators []string
	// Recognized is every Slack member id the operators mapping binds, whatever
	// the human holding it was granted, and Contacts is what the mapping calls
	// those humans. Together they are who this project knows and how to ask for
	// them: a message from anybody else is a stranger's, and is answered once per
	// thread with a refusal naming the contacts rather than acted on or read.
	//
	// They are wider than Operators on purpose. A colleague the project
	// recognizes but has granted nothing may steer nothing and is still not a
	// stranger, so what they get for a reply is the refusal naming the grant they
	// are missing, and what they get for a question is the answer.
	//
	// Both default to empty, and an empty Recognized is read as everybody rather
	// than nobody: a project that recognizes nobody has drawn no boundary and has
	// nobody to name as a contact, so it behaves exactly as it did before there
	// was a refusal to give.
	Recognized []string
	Contacts   []string
	// Conversation is the durable product-manager conversation this channel is a
	// client of. It is what makes a message addressed to this app something the
	// product manager answers rather than something the door has to refuse, and
	// it is the same conversation `yoyo chat` holds rather than a second one: one
	// begun at the terminal carries on here and back, because both project the
	// one durable record.
	//
	// It is optional, and a sink assembled without one answers where things stand
	// and says outright that everything else is driven from the terminal — which
	// is exactly what this door did before there was a conversation behind it.
	Conversation Conversation
	// ConversationDeadline bounds one turn taken from this channel. Zero takes
	// DefaultConversationDeadline, and the bound is the point: an operator asking
	// from a phone must be told something, and the conversation's own call can
	// wait far longer than anybody watching a thread will.
	ConversationDeadline time.Duration
	// Inbound is what to do with a message that arrives on the connection. It is
	// how a test drives the connection without a workspace; a sink the harness
	// builds leaves it unset and gets the steering above.
	Inbound InboundHandler
	// Dial opens the websocket transport. It is optional and exists so a test
	// can exercise the connection without a network.
	Dial dialFunc
	// Now is read for the watermark this product's reporting begins at, and again
	// on each pass for which end of a deep backlog is still recent enough to post
	// in full. It is optional and exists so a test can say when a sink was first
	// pointed at a product, or how far behind it is, without waiting for real time
	// to pass.
	Now func() time.Time
}

// Titles says what one work item is called. It is the sink's last resort for
// naming a thread, asked once per thread and never on the path of anything: the
// durable records carry titles, and this is for the items whose first mention in
// the channel is a record that carried none. A tracker that will not answer
// costs a header its title and nothing else.
type Titles interface {
	Title(ctx context.Context, workItemID string) (string, error)
}

// Sink is the long-running reporting process for one product.
//
// Three goroutines run inside it and all of them post: the delivery loop, which
// reads the durable records and says what has happened; the connection, which
// answers a reply in a thread; and, for as long as one turn lasts, the
// conversation, which carries what somebody said to the product manager and
// posts back what it said. The division between them is a rule rather than a
// lock, and it is worth stating because a lock would only be as good as the next
// thing somebody adds to any side:
//
// Everything above `pace` is written once, by New, and only read afterwards.
// Everything below it belongs to exactly one goroutine — `marking` to the
// delivery pass, which is the only thing that reconciles a status mark, and
// `steering`'s own memory to the connection, which guards it itself.
//
// What they touch in common is three things, and each one carries its own
// answer. The pacer is shared on purpose, since a pace two callers each advanced
// from their own reading of it is not a pace, and it holds a mutex for exactly
// that. The API is an HTTP client, which is safe to use from several goroutines.
// And the store is read from all of them and written from one per record: the
// delivery pass owns the thread map and the decision map, and neither the
// acknowledgment path nor a conversation turn can write either — the poster each
// is given cannot open a thread (see `poster.opens`), and answering an ask
// records a directive rather than revising the ask.
//
// The conversation runs off the connection rather than on it for one reason: the
// connection is a read loop that Slack expects to keep reading, and a turn takes
// minutes. A turn held on that loop would stall every other message arriving in
// the channel behind one operator's question, which is the shape of outage this
// whole door exists to prevent.
type Sink struct {
	channel string
	// appearance is how this product's speakers appear here: its own id after
	// every name, and the pictures the project chose.
	appearance notify.Appearance
	store      *Store
	api        *API
	feed       Feed
	// titles resolves what an item is called for a thread whose record carried no
	// title. It is optional, and nil is a sink that heads such a thread by the
	// identifier alone.
	titles Titles
	poll   time.Duration
	// identity is what this sink records about itself while it runs.
	identity Presence
	// refusal is how long to wait after a refusal only a person can clear. It is
	// a field only so a test can drive that path without spending the wait;
	// every sink the harness builds gets refusalBackoff.
	refusal time.Duration
	// wait is how the delivery loop spends the time between one pass and the
	// next, reporting whether the wait finished rather than the context ending.
	// Every sink the harness builds sleeps; a test supplies one that lets the
	// loop through a pass at a time, so a test about how the loop behaves over
	// several passes drives them rather than polling at a millisecond and
	// hoping a loaded machine gets round to it.
	wait func(ctx context.Context, wait time.Duration) bool
	// now is the clock the watermark is taken from, injected for the same reason.
	now func() time.Time
	// sources is where the four lines are read from when somebody asks for them
	// in the channel, and where a thread settling its own pause reads which
	// directives are holding its item. It is read and never written, from either
	// goroutine, which is what makes it safe to sit above the divide with the rest
	// of what New wrote once.
	sources *readmodel.Sources
	// citing is what every post's text is read through on its way out, so each
	// work item it names is beside its title. It is nil on a sink with no tracker
	// to list, which posts text as it was written.
	citing *titleIndex
	// pace is what keeps a catch-up from being posted faster than Slack will
	// carry it. Every post goes through it, including the message that opens a
	// thread: what the workspace counts is messages, not what they are for.
	pace *pacer
	// marking is the refusal the status marks are currently getting, empty when
	// they are working. A mark is reconciled on every pass, so a workspace that
	// refuses one refuses it every fifteen seconds, and the first refusal is news
	// while the tenth is noise — the same rule the delivery loop follows for the
	// same reason.
	//
	// It is the delivery pass's alone, and nothing on the acknowledgment path
	// reads or writes it: an acknowledgment is a message rather than a mark.
	marking string
	log     func(format string, args ...any)
	// operators is the Slack member ids this project granted direct-work, held
	// here as well as inside the steering so a sink assembled without a directive
	// record can still reach them. They are who a reply is authorized against, and
	// they are also who is told directly when the harness itself is degraded: the
	// same people either way, named once by the configuration.
	operators []string
	// steering is the inbound half, nil on a sink that was given nowhere to record
	// a directive. It is held as well as handed to the connection so the sink can
	// say at startup whether replies steer anything and who may send them.
	steering   *steering
	connection *connection
}

// clock is when this sink says it is. It is a function rather than time.Now so a
// test can put a product's watermark where it needs it.
func (s *Sink) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func New(options Options) (*Sink, error) {
	var problems []error
	if strings.TrimSpace(options.Channel) == "" {
		problems = append(problems, errors.New("a channel is required"))
	}
	// The store is what says which product this sink reports on, so a sink
	// without one has no name to post under as well as nowhere to keep its
	// cursors.
	if options.Store == nil {
		problems = append(problems, errors.New("the sink's own state store is required; it is where the product every speaker is named for comes from"))
	}
	if options.API == nil {
		problems = append(problems, errors.New("a Slack client is required"))
	}
	if options.Feed == nil {
		problems = append(problems, errors.New("a feed is required; a sink with nothing to read reports nothing"))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("the Slack sink cannot start: %w", err)
	}
	log := options.Log
	if log == nil {
		log = func(string, ...any) {}
	}
	poll := options.Poll
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	sink := &Sink{
		channel: strings.TrimSpace(options.Channel),
		appearance: notify.Appearance{
			Product: options.Store.Product(),
			Avatars: options.Avatars,
		},
		store:    options.Store,
		api:      options.API,
		feed:     options.Feed,
		titles:   options.Titles,
		poll:     poll,
		identity: options.Identity,
		refusal:  refusalBackoff,
		wait:     sleepUntil,
		now:      options.Now,
		sources:  options.Standing,
		// A product that named nobody is told nothing directly, which is the same
		// answer it gets for steering: the workspace changes nothing about how this
		// behaves until an operator names themselves.
		operators: options.Operators,
		pace: &pacer{
			every: DefaultPostInterval,
			now:   time.Now,
			sleep: sleepContext,
		},
		log: log,
		connection: &connection{
			api:  options.API,
			dial: options.Dial,
			log:  log,
		},
	}
	if read := sourcesTitles(options.Standing); read != nil {
		sink.citing = &titleIndex{read: read}
	}
	// A sink with somewhere to record a directive steers, and one with the durable
	// conversation behind it carries what somebody says to the product manager;
	// one with neither reports and no more. Either is enough to read what arrives
	// on the connection, because the two doors are separate — a reply in a thread
	// and a message addressed to this app — and a sink that could do one of them
	// must not be silent at the other. The handler a caller supplied wins, because
	// the only caller that can name that type is a test driving the connection
	// itself.
	if options.Directives != nil || options.Conversation != nil {
		sink.steering = newSteering(sink, options)
		sink.connection.handle = sink.steering.handle
	}
	if options.Inbound != nil {
		sink.connection.handle = options.Inbound
	}
	return sink, nil
}

// Run holds the connection open and posts until the context ends.
//
// It takes the product's sink lease first and refuses to start without it. Two
// sinks are not redundancy: each holds its own thread map, so the second opens
// its own threads and posts everything a second time, and an operator who
// started one twice would see a channel that looks broken rather than one that
// is doubled.
func (s *Sink) Run(ctx context.Context) error {
	// A sink stopped before it started does nothing at all, and finds that out
	// before it does anything: the lease, the identity call, and the presence
	// record are each a write or a round trip, and a stop is judged by how
	// promptly it lands. The delivery loop below reads its context first for
	// the same reason; this is the startup agreeing with it.
	if ctx.Err() != nil {
		return nil
	}
	release, err := s.hold()
	if err != nil {
		return err
	}
	defer release()

	// A sink handed a context that has already ended was stopped before it
	// started, and it goes no further than the lease it has just let go of. It
	// does not ask the workspace who it is, say it is reporting, or record
	// itself as running: a presence written and removed again is a record of a
	// sink that never was, and the write behind it is fsynced — on a machine
	// whose disk is busy that alone can hold a stop for seconds, at exactly the
	// moment somebody has decided to intervene. The delivery loop below reads
	// the context the same way before its first pass; this is startup agreeing.
	if ctx.Err() != nil {
		return nil
	}

	presence := s.identity
	presence.PID = os.Getpid()
	presence.Channel = s.channel
	presence.StartedAt = s.clock().UTC()
	if identity, err := s.api.Identify(ctx); err != nil {
		// A workspace that will not say who this is may still accept posts, and
		// reporting is never a gate, so this is said and not obeyed.
		s.log("Slack did not confirm this app's identity: %v", err)
	} else {
		presence.Team, presence.TeamID = identity.Team, identity.TeamID
		// The same answer says which member id a message has to name to be
		// addressed to this app. It is handed over here so the ordinary running
		// sink never spends a second call on it; a sink whose call was refused asks
		// again the first time somebody mentions anybody, because being answered is
		// the one thing that must not be lost to a call that failed at startup.
		if s.steering != nil {
			s.steering.learn(identity.UserID)
		}
		s.log("reporting to %s as %s in workspace %s", s.channel, identity.User, identity.Team)
	}
	// What this sink is is recorded for whoever asks later, and a sink that could
	// not record it still reports: the record is a diagnostic, and refusing to
	// post because a diagnostic could not be written would make reporting a gate
	// on its own bookkeeping.
	if err := s.store.SavePresence(presence); err != nil {
		s.log("this sink could not record what it is running as, so `yoyo doctor` will not see it: %v", err)
	}
	defer func() {
		if err := s.store.ClearPresence(); err != nil {
			s.log("this sink could not forget what it was running as: %v", err)
		}
	}()

	// Whether a reply steers anything is the one thing about this process an
	// operator cannot see from the channel until they try it, and the answer for
	// a workspace that has named nobody is "no". Saying it at startup is how
	// somebody following the setup document finds that out before they rely on it.
	s.log("%s", s.steer())
	s.log("%s", s.converses())

	connected := make(chan struct{})
	go func() {
		defer close(connected)
		s.connection.run(ctx)
	}()

	err = s.deliver(ctx)
	<-connected
	// A turn the connection set off outlives the read loop that started it, so it
	// is waited for rather than left running past the process. Its own context is
	// already ended by here, so the wait is what it takes the turn to notice and
	// say so in its thread — not the deadline it was given.
	if s.steering != nil {
		s.steering.settle()
	}
	return err
}

// Once makes a single pass and returns. It is what a setup document can tell
// somebody to run: it posts whatever is already due and leaves nothing behind
// it. It holds the same lease a running sink does, because a pass posts, and
// two things posting from two thread maps is exactly what the lease is for.
func (s *Sink) Once(ctx context.Context) error {
	release, err := s.hold()
	if err != nil {
		return err
	}
	defer release()
	return s.pass(ctx)
}

// steer says what a reply in one of this sink's threads will do, which is
// nothing at all for a product that has granted nobody. The refusal an unlisted
// reply gets is visible in the thread, but only to whoever sent it; this is the
// same fact where an operator setting the sink up is looking.
func (s *Sink) steer() string {
	switch {
	case s.steering == nil || s.steering.directives == nil:
		return "replies in these threads are read and not acted on: this sink was assembled without the directive record"
	case len(s.steering.operators) == 0:
		return "replies in these threads are acknowledged and not acted on: no human in this project holds direct-work with a bound Slack member id"
	default:
		return fmt.Sprintf("replies in these threads steer the work, from the %d Slack member(s) this project granted direct-work", len(s.steering.operators))
	}
}

// converses says what a message addressed to this app will get, which is the
// other half of the same question `steer` answers and is asked at startup for
// the same reason: whether this channel reaches the product manager is invisible
// from the channel until somebody tries it.
func (s *Sink) converses() string {
	switch {
	case s.steering == nil || s.steering.conversation == nil:
		return "a message addressed to this app is answered with where things stand and no more: this sink was assembled without the durable conversation"
	case len(s.steering.operators) == 0:
		return "a message addressed to this app is answered with where things stand: no human in this project holds direct-work with a bound Slack member id, so nobody may talk to the Lead Product Manager from here"
	default:
		return fmt.Sprintf("a message addressed to this app reaches the Lead Product Manager, from the %d Slack member(s) this project granted direct-work; it is the same conversation `yoyo chat` continues", len(s.steering.operators))
	}
}

// hold makes this process the product's only sink and reports how to stop
// being it.
func (s *Sink) hold() (func(), error) {
	lease, held, err := s.store.Lease()
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, errors.New("another Slack sink is already running for this product; one sink per product is what keeps one thread per work item")
	}
	return func() {
		if err := lease.Release(); err != nil {
			s.log("the Slack sink lease could not be released: %v", err)
		}
	}, nil
}

// deliver runs passes until the context ends.
//
// A refusal only a person can clear — an app nobody invited to the channel, a
// revoked token, a missing scope — is said once and then not said again while it
// stands. The alternative is the same line every pass for as long as the process
// runs, which is how a log stops being read: the operator has to fix it, and
// repeating the instruction every fifteen seconds does not make them fix it
// sooner. It is still retried, because what clears it is somebody doing
// something in Slack rather than anything here, and reporting has to come back
// on its own when they do — which is the other half of why it is said again when
// it does.
func (s *Sink) deliver(ctx context.Context) error {
	// standing is the refusal currently being waited out, empty when posting
	// works. It is what makes the first refusal news and the tenth silence.
	standing := ""
	// The context is read before the first pass as well as after it, which is the
	// same shape the connection's own loop beside this one already has. A sink
	// handed a context that has already ended has been stopped before it started
	// — the operator pressed Ctrl-C during startup, or a caller is shutting the
	// process down — and it must not read the records and try to post once on the
	// way out. The cost of getting this wrong is not a wasted pass but the thing
	// the stop is judged by: a sink whose Run returns only after a full pass looks
	// hung for as long as that pass takes, at exactly the moment somebody has
	// decided to intervene.
	for ctx.Err() == nil {
		wait := s.poll
		err := s.heldPass(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err == nil:
			if standing != "" {
				s.log("Slack is accepting messages again; reporting has caught up")
				standing = ""
			}
		case PermanentError(err):
			wait = s.refusal
			if refusal := err.Error(); refusal != standing {
				standing = refusal
				s.log("Slack will keep refusing this until somebody changes something in the workspace; it is retried quietly every %s: %v", wait, err)
			}
		default:
			standing = ""
			s.log("this pass over the records could not finish; the cursors are unchanged and it will be retried: %v", err)
			wait = passBackoff
		}
		if !s.wait(ctx, wait) {
			return nil
		}
	}
	return nil
}

// heldPass is one pass under the pass lease, so the product's supervisor can
// restart this sink between two passes rather than in the middle of one. A lease
// held by somebody else is the supervisor about to stop this process, and the
// pass is skipped rather than waited for. A lease that cannot be asked about at
// all does not stop the pass: reporting is never a gate on its own bookkeeping.
func (s *Sink) heldPass(ctx context.Context) error {
	lease, held, err := s.store.PassLease()
	if err != nil {
		s.log("this pass is made without the pass lease, so a restart into a deployed build could land in the middle of it: %v", err)
		return s.pass(ctx)
	}
	if !held {
		return nil
	}
	defer func() {
		if err := lease.Release(); err != nil {
			s.log("the pass lease could not be released: %v", err)
		}
	}()
	return s.pass(ctx)
}

// pass reads the records once and posts what is due. It is the whole of the
// sink's behavior: staying open is this, repeated.
func (s *Sink) pass(ctx context.Context) error {
	cursors, err := s.store.LoadCursors()
	if err != nil {
		return err
	}
	// The watermark is taken once, on the first pass this product ever gets, and
	// written before anything is read. Taking it afresh on every start would move
	// it past every outage, and a report filed while the sink was down would then
	// be older than the restart and be read past as history — which is exactly
	// the record somebody coming back most needs to see.
	if cursors.Since.IsZero() {
		cursors.Since = s.clock().UTC()
		if err := s.store.SaveCursors(cursors); err != nil {
			return err
		}
		s.log("reporting on this product from %s; what it recorded before that is left in the durable records", cursors.Since.Format(time.RFC3339))
	}
	threads, err := s.store.LoadThreads()
	if err != nil {
		return err
	}
	batch, err := s.feed.Poll(ctx, cursors)
	if err != nil {
		return err
	}

	// Rendering is the notifier's, posting is the sink's, and this is the seam
	// between them: what a message says is decided once, by the package that
	// knows the personas, whatever ends up carrying it.
	into := &poster{sink: s, threads: &threads, opens: true}
	notifier := notify.New(into, s.appearance)

	// How deep the backlog is has to be decided over the whole batch before any of
	// it is posted, because a digest says how many events it stands for. An
	// ordinary pass plans nothing and posts every delivery as it is.
	plan := planCatchUp(batch.Deliveries, s.clock())
	if len(plan.digest) > 0 {
		s.log("catching up on a deep backlog: %d threads are digested rather than replayed, and the durable records hold what they stand for", len(plan.digest))
	}

	for index, delivery := range batch.Deliveries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if notification, say := plan.say(index, delivery); say {
			into.reached = false
			// Almost every delivery is for whoever is reading the channel and carries
			// nobody. The exception is what became of something one person asked for,
			// which is said to them by name: it is set per delivery rather than per
			// poster because the poster is one and the deliveries are many.
			into.mention = delivery.Mention
			// The one class of message that is about the harness being degraded
			// rather than about any work goes to the operators as well as to the
			// channel, because a channel is somewhere somebody chooses to look.
			into.direct = nil
			if delivery.Direct {
				into.direct = s.operators
			}
			// And the one class that is both important and the operators' to act
			// on is said to them by name in the channel, which is what makes the
			// workspace notify them: a provider nobody is logged into is ended by
			// a person and nothing else.
			into.tags = nil
			if delivery.Tag {
				into.tags = s.operators
			}
			if err := notification.Notify(ctx, notifier); err != nil {
				if into.reached {
					return err
				}
				// The notifier refused to say it at all, which is a record
				// nothing can be said about rather than a workspace that is
				// down. Stopping would hold up every later message on every
				// stream forever, so it is said once here and read past.
				s.log("a notification could not be said and was skipped: %v", err)
			}
		}
		// What became of a directive is the disposition of the reply that asked for
		// it, so the message that asked stops wearing the thinking face at the
		// moment the outcome is said and not a moment earlier. Every other delivery
		// carries no reply to mark.
		if delivery.Reply != "" {
			s.settle(ctx, delivery.Reply)
		}
		if err := s.advance(&cursors, delivery); err != nil {
			return err
		}
	}

	// The marks go on after the messages, so a thread this pass opened is marked
	// in the same pass rather than on the next one. They are reconciled against
	// the record rather than driven by what was posted: a run that finished with
	// nothing left to say still has to stop reading as working.
	s.mark(ctx, &threads, batch.Statuses)

	// A line that has stopped is put to the operators themselves, after the
	// channel has been told the same thing. The order is that way round because
	// the channel is the account and this is the ask: somebody who opens the
	// direct message and goes to look should find the state already said where the
	// work is discussed, rather than arriving ahead of it.
	s.ask(ctx, batch.Asking)

	// Cursors for streams that no longer exist are dropped only after a pass
	// that read them all, so a feed that failed halfway, or read a record past
	// because it could not read it, never looks like a product whose runs have
	// gone away.
	if len(batch.Streams) > 0 && !batch.Partial && cursors.Keep(batch.Streams) {
		if err := s.store.SaveCursors(cursors); err != nil {
			return err
		}
	}
	return nil
}

// mark puts each item's current status on the message its thread hangs from, so
// the channel's top level reads as a status board: a scan says what is working,
// what is with the reviewer, what is blocked, and what landed, without a single
// thread being opened. A status that has not moved is left alone, which is what
// keeps this quiet — most passes mark nothing.
//
// It reports no error, and the reasons are the sink's own. The messages of this
// pass are posted and their cursors written, so failing here would repeat all of
// them next time in exchange for an emoji; and a mark stands for a thread whose
// whole account is already in the thread, so a workspace that will not take one
// costs a reader a glance rather than the record.
//
// What makes an interrupted marking settle is that a change takes every other
// mark off rather than the one the record happens to name, and writes the record
// last. The record is therefore only ever a claim that the thread is already
// where it should be, never evidence about which symbol is on the message — so a
// process killed after the workspace took a mark and before the write landed is
// a thread whose record is merely out of date, and the next change sweeps the
// symbol it actually wears off with the rest. Trusting the record for that is
// what would strand one: the sweep is affordable exactly because the vocabulary
// is four words and cannot grow.
func (s *Sink) mark(ctx context.Context, threads *ThreadMap, statuses map[string]notify.Status) {
	topics := make([]string, 0, len(statuses))
	for topic := range statuses {
		topics = append(topics, topic)
	}
	// Sorted, so what a pass does is the same twice over rather than whatever
	// order a map handed back.
	sort.Strings(topics)

	for _, topic := range topics {
		if ctx.Err() != nil {
			return
		}
		status := statuses[topic]
		// A status this build has no symbol for is left unsaid rather than marked
		// with nothing, which is the same refusal an unrecognized kind gets: a
		// record written by a newer harness is read past, not mistranslated.
		if !status.Valid() {
			continue
		}
		// A topic with no thread is one nothing has been said about yet. There is
		// no message to mark, and opening one to carry a status would put a thread
		// in the channel that says nothing happened.
		thread, found := threads.Lookup(s.channel, topic)
		if !found || thread.Status == status {
			continue
		}
		if err := s.remark(ctx, thread, status); err != nil {
			// A sink being shut down mid-mark is not a workspace refusing
			// anything, and the line below is the one the setup document teaches
			// an operator to read as a missing scope. The wait inside is where a
			// shutdown is actually met, since it is the only blocking call here.
			if ctx.Err() != nil {
				return
			}
			if refusal := err.Error(); refusal != s.marking {
				s.marking = refusal
				s.log("the status mark on %s could not be set, so the channel's top level is stale until it can be; the messages are unaffected: %v", topic, err)
			}
			continue
		}
		s.marking = ""
		thread.Status = status
		threads.Record(topic, thread)
		if err := s.store.SaveThreads(*threads); err != nil {
			// The mark is already on the message; what could not be written is the
			// note that it is. The next pass sets the same mark again, which is
			// four calls rather than a wrong answer — and a store that will not
			// take a write is not going to take the next thread's either, so this
			// stops rather than spending the workspace on the rest of them.
			s.log("the status mark on %s could not be remembered, so the next pass sets it again: %v", topic, err)
			return
		}
		s.log("marked %s as %s", topic, status)
	}
}

// remark makes one thread's opener wear one status and no other: every other
// mark in the vocabulary comes off, and then the one that is true goes on.
//
// It sweeps rather than removing the one the record names, because what the
// record says and what the message wears can differ — a process killed between
// the workspace taking a mark and the record being written leaves exactly that,
// and a targeted removal would take off a symbol that is not there and leave the
// one that is on the message forever. A sweep cannot: whatever the opener wears,
// it is one of four and three of them are being removed. The removals that hit
// nothing are answered no_reaction and cost a call each, which is what the
// vocabulary being small and fixed buys, and they happen only when a status has
// actually moved.
//
// Every call goes through the pacer that holds the sink to what the workspace
// sustains. Slack counts a reaction against a different allowance from a message,
// but a sink that is catching up is the one moment it can least afford to be
// suppressed, and nothing about a status mark is urgent.
func (s *Sink) remark(ctx context.Context, thread Thread, status notify.Status) error {
	for _, stale := range notify.Statuses() {
		if stale == status {
			continue
		}
		if err := s.pace.wait(ctx); err != nil {
			return err
		}
		if err := s.api.Unreact(ctx, thread.Channel, thread.ThreadTS, stale.Symbol()); err != nil {
			return err
		}
	}
	if err := s.pace.wait(ctx); err != nil {
		return err
	}
	return s.api.React(ctx, thread.Channel, thread.ThreadTS, status.Symbol())
}

// receipt marks one reply of somebody's with where its directive stands, on
// their own message. It is the twin of the status a thread's opener wears: the
// opener says what the item is doing, and this says what became of what they
// said.
//
// The arrival mark goes on alone, because it goes on a message nothing has
// marked yet and because the acknowledgment behind it is what the operator is
// actually waiting for — two removals that hit nothing would put the pace's wait
// between somebody typing and being answered. A disposition sweeps instead, for
// the reason a status change does: what the message is wearing is the question,
// and a sink killed between the workspace taking a mark and the disposition
// landing leaves exactly that, so the sweep is what settles it.
//
// It reports its error rather than logging, because its callers are the two
// halves that already know how to say what a reply could not be told.
func (s *Sink) receipt(ctx context.Context, messageTS string, receipt notify.Receipt) error {
	return s.receiptIn(ctx, s.channel, messageTS, receipt)
}

// receiptIn is the same mark on a message in a named conversation, which is how
// a message addressed to this app in a direct message wears one: a mark put on
// by channel and timestamp lands on whatever that pair names, and the reporting
// channel holds no message at a direct message's timestamp.
func (s *Sink) receiptIn(ctx context.Context, channel, messageTS string, receipt notify.Receipt) error {
	if !receipt.Valid() {
		return fmt.Errorf("%q is not one of the marks a reply can wear", receipt)
	}
	if receipt != notify.ReceiptUnderConsideration {
		for _, stale := range notify.Receipts() {
			if stale == receipt {
				continue
			}
			if err := s.pace.wait(ctx); err != nil {
				return err
			}
			if err := s.api.Unreact(ctx, channel, messageTS, stale.Symbol()); err != nil {
				return err
			}
		}
	}
	if err := s.pace.wait(ctx); err != nil {
		return err
	}
	return s.api.React(ctx, channel, messageTS, receipt.Symbol())
}

// settle moves the mark on the message that asked for a directive now that what
// became of it has been said in its thread. It is the far end of the receipt the
// inbound half puts on as a reply arrives, and it is where the thinking face
// stops being true.
//
// It is never a gate, for the reason the status marks are not: the outcome is
// already in the thread in words, this pass's messages are posted, and failing
// here would repeat all of them next time in exchange for an emoji.
func (s *Sink) settle(ctx context.Context, messageTS string) {
	if err := s.receipt(ctx, messageTS, notify.ReceiptSettled); err != nil {
		// A sink being shut down mid-mark is not a workspace refusing anything.
		if ctx.Err() != nil {
			return
		}
		s.log("the reply that asked for this could not be marked as settled, so what became of it is only said in the thread: %v", err)
	}
}

// post puts one message in the workspace at the pace the workspace sustains.
// Every message the sink sends goes through here rather than to the client
// directly, because what Slack suppresses is an application posting too fast and
// it does not care which of its own messages were the interesting ones.
func (s *Sink) post(ctx context.Context, message Message) (string, error) {
	if err := s.pace.wait(ctx); err != nil {
		return "", err
	}
	message.Text = s.citing.cite(ctx, message.Text)
	if s.sources != nil {
		message.Text = readmodel.ReadTextTerms(s.sources.Repository).Render(message.Text)
	}
	return s.api.Post(ctx, message)
}

// advance records that one delivery has been posted, durably, before anything
// else is posted. What the cursor becomes is the feed's to decide, because it is
// the feed that knows what reading each stream was posted from.
func (s *Sink) advance(cursors *Cursors, delivery Delivery) error {
	if cursors.Streams == nil {
		cursors.Streams = map[string]Cursor{}
	}
	cursors.Streams[delivery.Stream] = delivery.Cursor
	return s.store.SaveCursors(*cursors)
}

// poster carries a rendered message into the workspace. It is the seam the
// notifier hands its work to: everything above it decided what is said and whose
// account it is, and everything below it knows about a channel, a connection,
// and a thread map.
type poster struct {
	sink    *Sink
	threads *ThreadMap
	// opens says this poster may open a thread for a topic nothing has been said
	// about yet, and write the map that remembers it. Only the delivery pass may:
	// it is the one thing that owns the thread map, and it runs while the
	// connection is answering replies on its own goroutine. An acknowledgment was
	// correlated through a thread that already exists, so a lookup that failed
	// there would mean the map had moved underneath it — and opening a second
	// thread from that stale copy, and writing it back over the map the pass owns,
	// is the one way this path could damage anything. It cannot, because it is not
	// given the capability rather than because it never takes it.
	opens bool
	// mention is the Slack member id this message is for, empty for the ordinary
	// message that is for whoever is reading the channel. It is set where a
	// message answers one person — the acknowledgment of their reply, and what
	// later became of what they asked for — because a thread they are not looking
	// at is indistinguishable from silence, and being told is the whole point of
	// both.
	//
	// It is carried here rather than in the envelope because it is Slack's own
	// shape: who a message is about is the record's, and how a workspace pokes
	// somebody is this surface's.
	mention string
	// tags is the member ids this message names in the channel, empty for every
	// message that is for whoever is reading it. It is set on the class of
	// message that is both important and the operators' to act on — the provider
	// answering nobody, a line stopped for reasons no record names, a brake hold
	// handed to the operator — because the communication rule says a message that
	// is theirs to act on is tagged to them, and a member id is what makes the
	// workspace notify a person rather than only print their name.
	tags []string
	// direct is the member ids this message is also said to privately, empty for
	// every message that is only for the channel. It is set on what is about the
	// harness being degraded rather than about any work: the channel still carries
	// the account, and this is what puts it where somebody will see it at three in
	// the morning.
	//
	// It is carried here rather than in the envelope for the reason the mention
	// is. What is worth telling somebody about is the record's; how a workspace
	// reaches a person is this surface's.
	direct []string
	// reached records that a message got as far as the workspace. It is what lets
	// the pass tell a record nothing could be said about — which it reads past —
	// from a workspace that refused it, which it retries. Without it the two are
	// one error and one of them would be treated as the other.
	reached bool
}

// Post puts one message in its topic's thread, opening the thread if this is the
// first thing anybody has said about that topic.
func (p *poster) Post(ctx context.Context, message notify.Message) error {
	sink := p.sink
	topic, err := notify.ParseTopic(message.Topic)
	if err != nil {
		return err
	}
	// The key says which thread; what the topic is called travels beside it,
	// because a header is read by somebody who has not read the tracker.
	topic = topic.WithTitle(message.TopicTitle)

	p.reached = true
	threadTS := ""
	// The product is the one topic that is not a thread: what is about the whole
	// line rather than any one item goes to the top of the channel, because
	// burying it in some item's thread would misfile it.
	if topic.Kind != notify.TopicProduct {
		thread, found := p.threads.Lookup(sink.channel, message.Topic)
		if !found {
			if !p.opens {
				return fmt.Errorf("say %s about %s: its thread is not in the map this was given, and this poster does not open one", message.Kind, message.Topic)
			}
			opened, err := sink.openThread(ctx, topic)
			if err != nil {
				return err
			}
			thread = opened
			p.threads.Record(message.Topic, thread)
			// The map is written before the message that goes in the thread, so
			// a sink killed between them replies into the thread it opened
			// rather than opening a second one for the same topic.
			if err := sink.store.SaveThreads(*p.threads); err != nil {
				return err
			}
		}
		threadTS = thread.ThreadTS
	}

	emoji, url := icon(message.Identity.Avatar)
	if _, err := sink.post(ctx, Message{
		Channel:   sink.channel,
		Text:      tagged(p.mention, taggedAll(p.tags, renderText(message))),
		ThreadTS:  threadTS,
		Broadcast: broadcast(message.Reach),
		Username:  message.Identity.Name,
		IconEmoji: emoji,
		IconURL:   url,
	}); err != nil {
		return err
	}
	// What was posted is said in the sink's own log, by kind and topic and never
	// by body: a process that runs for days with no output cannot be told from
	// one that has quietly stopped working, and somebody following the setup
	// document needs to see that the first message actually went.
	sink.log("posted %s about %s", message.Kind, label(topic))
	p.deliver(ctx, message, emoji, url)
	return nil
}

// deliver says the same message privately to each operator this delivery is for,
// after the channel already has it.
//
// It reports no error, which is the same judgement the status marks are made
// under and for a stronger reason: the account is already posted and its cursor
// is about to be written, so failing here would repeat the channel message on the
// next pass in exchange for a direct message that will fail again. A workspace
// that will not open a direct message — a missing scope, somebody who has left —
// costs the escalation and not the record, and the refusal is said in the sink's
// own log where an operator setting this up is looking.
//
// It is two calls per operator, which is what Slack documents: the conversation
// is opened and the message goes to the channel that comes back. Both go through
// the pacer, because what a workspace counts is calls and it does not care which
// of them were the interesting ones.
func (p *poster) deliver(ctx context.Context, message notify.Message, emoji, url string) {
	sink := p.sink
	for _, member := range p.direct {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		conversation, err := sink.openConversation(ctx, member)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			sink.log("a direct conversation with %s could not be opened, so %s stands in the channel alone: %v", member, message.Kind, err)
			continue
		}
		// A message that is tagged in the channel is tagged in the direct
		// conversation too: the communication rule says a message that is theirs
		// to act on names them by member id, wherever it is said.
		text := renderText(message)
		if slices.Contains(p.tags, member) {
			text = tagged(member, text)
		}
		if _, err := sink.post(ctx, Message{
			Channel:   conversation,
			Text:      text,
			Username:  message.Identity.Name,
			IconEmoji: emoji,
			IconURL:   url,
		}); err != nil {
			if ctx.Err() != nil {
				return
			}
			sink.log("%s could not be said to %s directly, so it stands in the channel alone: %v", message.Kind, member, err)
			continue
		}
		sink.log("said %s to %s directly", message.Kind, member)
	}
}

// openConversation asks the workspace for the direct conversation with one
// member, at the pace the workspace sustains.
func (s *Sink) openConversation(ctx context.Context, member string) (string, error) {
	if err := s.pace.wait(ctx); err != nil {
		return "", err
	}
	return s.api.OpenConversation(ctx, member)
}

// openThread posts the message a topic's thread hangs from. It names the topic
// and nothing else: the thread's first reply is the first thing that actually
// happened, and a header that summarized it would be a summary written before
// the events it summarizes. Naming it is the identifier and what the record
// calls the topic, because a channel is read by people and an identifier on its
// own is a name somebody has to go and resolve before they know what the thread
// is about. It is opened by the harness rather than by whichever persona
// happened to speak first, because opening a thread is not anybody's account of
// anything.
func (s *Sink) openThread(ctx context.Context, topic notify.Topic) (Thread, error) {
	topic = s.named(ctx, topic)
	identity := s.appearance.Identity(notify.Harness())
	emoji, url := icon(identity.Avatar)
	ts, err := s.post(ctx, Message{
		Channel:   s.channel,
		Text:      fmt.Sprintf("*%s*", header(topic)),
		Username:  identity.Name,
		IconEmoji: emoji,
		IconURL:   url,
	})
	if err != nil {
		return Thread{}, fmt.Errorf("open the thread for %s: %w", topic.Key(), err)
	}
	return Thread{Channel: s.channel, ThreadTS: ts, OpenedAt: time.Now().UTC()}, nil
}

// named is the topic with a name on it, resolved from the tracker where the
// record that reached here carried none. Most records carry one and this does
// nothing; what it is for is the item whose first appearance in the channel is a
// bookkeeping event — a priority changed, a goal recorded — which says what
// happened to an item without saying what the item is. Every item admitted
// before the channel existed is one of those, so leaving it to the records would
// leave a live class of threads named by an identifier alone.
//
// It is asked at most once per thread, because the thread map is durable and a
// thread is opened once. A tracker that will not answer costs the header its
// title and nothing else: the thread opens either way, since reporting is never
// a gate and a thread nobody opened is a whole narrative missing rather than a
// name.
func (s *Sink) named(ctx context.Context, topic notify.Topic) notify.Topic {
	// An exchange is addressed by an identifier the tracker has never heard of,
	// and the product opens no thread at all, so the work item is the one topic
	// there is anywhere to ask about.
	if s.titles == nil || topic.Kind != notify.TopicWorkItem || topic.Title != "" {
		return topic
	}
	title, err := s.titles.Title(ctx, topic.ID)
	if err != nil {
		s.log("the tracker would not say what %s is called, so its thread is headed by the identifier alone: %v", topic.ID, err)
		return topic
	}
	return topic.WithTitle(title)
}

// tagged puts a Slack mention in front of a message that is for one person, and
// leaves every other message exactly as it was rendered.
//
// The mention is the member id in Slack's own syntax, which is what makes the
// workspace notify them: a message that named them in words would read the same
// and reach nobody who was not already looking at the thread. A member id is
// what the operators mapping binds, so nothing here has to resolve anybody.
func tagged(member, text string) string {
	trimmed := strings.TrimSpace(member)
	if trimmed == "" {
		return text
	}
	return "<@" + trimmed + "> " + text
}

// taggedAll is tagged for every operator at once, for the one message that is
// all of theirs to act on. An empty list leaves the text exactly as rendered.
func taggedAll(members []string, text string) string {
	for index := len(members) - 1; index >= 0; index-- {
		text = tagged(members[index], text)
	}
	return text
}

// icon splits one avatar into the two fields Slack takes for it: a shortcode
// into icon_emoji, an image into icon_url. The split is here rather than in the
// notifier because it is Slack's shape and not the persona's — nothing about
// which field carries the picture is a fact about who is speaking.
func icon(avatar string) (emoji, url string) {
	trimmed := strings.TrimSpace(avatar)
	if strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "http://") {
		return "", trimmed
	}
	return trimmed, ""
}

// header supplies an explicit work item reference for the current tracker
// reading at posting time. Other topics use the name their record carries.
func header(topic notify.Topic) string {
	if topic.Kind == notify.TopicWorkItem {
		// The post resolves this explicit reference from the current listing.
		// A historical topic title never substitutes for that reading.
		var titles *readmodel.WorkItemTitles
		return titles.Name(topic.ID)
	}
	named := label(topic)
	if topic.Title == "" {
		return named
	}
	return named + " — " + topic.Title
}

// label names a topic in the sink's own log and inside a header: the item's own
// identifier, and an exchange said as one so a thread of questions is not
// mistaken for an item.
func label(topic notify.Topic) string {
	switch topic.Kind {
	case notify.TopicExchange:
		return "exchange " + topic.ID
	case notify.TopicProduct:
		return "this product"
	default:
		return topic.ID
	}
}

// broadcast reports whether a thread reply should also be shown in the main
// channel view. A thread is where a narrative belongs, and the channel view
// hiding replies is what keeps three items in flight readable — but it hides a
// run that parked out of tokens exactly as thoroughly as it hides a check that
// passed, and the parked run is the ten-silent-hours problem at a different
// layer.
//
// So the line is the reach the envelope already carries: what is important or
// needs the operator is shown where somebody who has opened no threads is
// looking, and everything else stays where the narrative is. It was the severity
// until the measurement that produced the reach — three quarters of 2,250 posts
// were per-event narration, and every warning-severity milestone among them was
// broadcast over the forty that actually needed somebody. No judgment is made
// here: a surface that decided this for itself would be a second posting policy
// disagreeing with the recorded one.
func broadcast(reach notify.Reach) bool {
	return reach == notify.ReachChannel
}

// renderText is the message as it appears in the channel: what the persona said,
// and the records it was read from.
//
// The words are the notifier's and nothing here changes them — the severity is
// already said in them, and already bounded, because how a message reads is
// decided once by the package that knows the personas. What is added is the way
// back to the evidence, so a message leads to the record rather than standing in
// for it.
func renderText(message notify.Message) string {
	text := strings.TrimSpace(message.Body)
	if reference := renderRefs(message.Refs); reference != "" {
		text += "\n" + reference
	}
	return truncate(text, message.Refs)
}

// renderRefs names the durable records this message was read from.
//
// The conversation and the directive are not among them, and they are the two
// identifiers this line deliberately leaves out. What is left is what a reader
// follows rather than resolves — a run, an exchange they answer. The
// conversation an event happened to be recorded in was never one of those, and
// neither is the directive: what a person gets for a reply they typed is the
// acknowledgment above this line, which says what was recorded and what it does
// to the work in words. The envelope still carries both and the durable records
// still hold them, so nothing anybody traces afterwards is lost to leaving them
// unsaid.
func renderRefs(refs notify.Refs) string {
	parts := make([]string, 0, 2)
	for _, reference := range []struct{ label, value string }{
		{label: "run", value: refs.RunID},
		{label: "exchange", value: refs.ExchangeID},
	} {
		if strings.TrimSpace(reference.value) != "" {
			parts = append(parts, reference.label+" "+reference.value)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "_" + strings.Join(parts, " · ") + "_"
}

// truncate keeps a message inside Slack's limit, and says where the rest is. The
// notifier already bounds the body it renders, so nothing that arrives here in
// good order is near this; what it guards is a reference line long enough to
// push one over, which Slack would refuse forever rather than once. A message
// too long to post is one whose record holds the whole of it, so the marker
// names that record rather than apologizing.
func truncate(text string, refs notify.Refs) string {
	if len(text) <= maxTextBytes {
		return text
	}
	marker := "\n… truncated; the whole of it is in " + refs.Record()
	cut := maxTextBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + marker
}
