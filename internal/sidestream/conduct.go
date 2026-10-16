package sidestream

// Holding one side conversation: opening it beside a main thread, taking its
// turns, and concluding it into the merge.
//
// The runner is the harness's own hand, as the exchange's conductor is. No role
// reaches the record, the lease, the turn cap, or the merge — a side thread is
// put a question and writes prose back, and everything between the two happens
// here. That is what makes the properties this package promises enforceable
// rather than requested, and two of them are decided by where the writes go
// rather than by anything a turn does:
//
// A turn is taken under the stream's own lease and writes the stream's own
// record and the stream's own event log, all three named for a `side-`
// identifier. The main thread's lease, record, and log are named for a `chat-`
// one and are not reached from here at all. So a side turn taken while the main
// thread is held is not a turn that got past the main thread's lease; it is one
// that never asked for it.
//
// And the authority is Permitted above, which the reply is held to here: a side
// turn is a toolless invocation with no harness block it may ask through, so
// what it produces is judgment and the drafts it wants the main thread to
// ratify. Nothing it says is carried out, here or anywhere.
//
// Concluding merges. A side thread that ended without merging is a thread whose
// whole substance is on a disk nothing reads, and it looks exactly like a thread
// that found nothing out — so the two happen together rather than as two calls a
// caller has to remember to pair. The turn that answers what the thread was
// opened for and the turn that spends its last both go through the merge before
// Put returns, which is the design's two endings: a side thread concludes by
// finishing, or by exhausting its budget.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The bounds on one question put to a side thread and on what comes back. They
// are what an exchange's are, and for the same reason: a question is a question
// rather than a document, and judgment is the thing being asked for, so the
// answer is allowed to be longer than the question and is still bounded.
const (
	MaxQuestionBytes = 4 << 10
	MaxAnswerBytes   = 16 << 10
)

// ErrTurnsSpent is what a refusal past a stream's turn cap unwraps to, so a
// caller can tell "this thread has had its turns" from a store that could not be
// read without matching on the words of either.
var ErrTurnsSpent = errors.New("side stream turn cap reached")

// SpentError reports a side thread asked something after it had spent every turn
// it was opened with. It is a record left behind rather than the ordinary
// ending: the turn that spends the last one concludes the thread on its way out,
// so a stream still open with nothing remaining is one whose last turn produced
// nothing to conclude with — a process that died between counting the turn and
// merging, a provider that failed the turn, or a reply refused whole with no
// prose around its block — and what it needs is to be concluded rather than
// asked again.
type SpentError struct {
	StreamID string
	Cap      int
}

func (e *SpentError) Error() string {
	return fmt.Sprintf("%s has spent all %d of its permitted turns and was never concluded; conclude it rather than asking it again",
		e.StreamID, e.Cap)
}

func (e *SpentError) Unwrap() error { return ErrTurnsSpent }

// Store is the durable home of side streams, narrowed to what holding one needs.
// It is satisfied by *runstate.SideStreamStore.
type Store interface {
	Open(stream Stream, bound int) error
	Save(stream Stream) error
	Load(id string) (Stream, error)
	AppendEvent(event execution.Event) error
}

// Merge is where a concluded side conversation's substance goes: the agent's own
// memory, written through the agent-memory machinery — budgeted, redacted,
// audited, revisioned, citing this stream rather than copying its transcript —
// for the main thread's next turn to read and ratify.
//
// It is an interface for the reason Voice is one, and for one more: the package
// that composes that revision writes memory through `internal/agentcontext`,
// which reaches this package and cannot be reached from it. So what the runner
// holds is the narrowed statement of what concluding does, and whoever wires a
// runner wires the write.
type Merge interface {
	// Conclude stamps the outcome and the moment on the stream, writes the
	// substance into the agent's memory, and records the stream as ended. It
	// answers the concluded record.
	//
	// The outcome and the moment are passed rather than read off the stream,
	// because a caller that could hand in a stream it had already stamped could
	// hand in one stamped as concluded that never was.
	Conclude(ctx context.Context, stream Stream, substance string, commitments []string, outcome Outcome, at time.Time) (Stream, error)
}

// Voice is the answering half: one toolless provider invocation that puts a
// question to a role on its side thread and returns what it said. It is an
// interface so that holding a side conversation does not depend on which
// provider answers, and so a test can hold a whole thread without one.
type Voice interface {
	Answer(ctx context.Context, question Question) (Spoken, error)
}

// Question is one side turn as the answering side receives it.
type Question struct {
	// StreamID is the record this invocation belongs to, and is what the provider
	// is told the invocation is. A side turn has no run, and the conversation it
	// is held beside is not its own.
	StreamID string
	Role     domain.AgentRole
	Agent    string
	// Conversation is the main thread this side thread is beside, and Topic what
	// the thread was opened about. Both are said to the answering role because
	// what is being asked beside what is part of the question.
	Conversation string
	Topic        string
	// Turn is which turn this is and MaxTurns the stream's durable cap. Both are
	// said to the answering role, so it knows whether it is being asked to open
	// something up or to land it.
	Turn     int
	MaxTurns int
	Question string
	// SessionID is the provider session this stream has been held in so far,
	// empty on the first turn.
	SessionID           string
	SessionBackend      domain.Backend
	SessionAccountAlias string
	// LastSequence is where this stream's own event log has reached, and Events
	// is where this invocation's events are recorded. Both are the stream's own:
	// an event written into the main thread's log would be the interleaved
	// transcript the design forbids, and the store refuses one either way.
	LastSequence uint64
	Events       func(event execution.Event) error
}

// Spoken is what the side thread produced.
type Spoken struct {
	// Answer is the reply as the provider wrote it. It is checked by ReadReply
	// before anything is recorded, so a voice never has to enforce the boundary
	// itself.
	Answer string
	// SessionID is the provider session a later turn continues.
	SessionID string
	// CostUSD is what the provider charged for this invocation, as it reported
	// it. Nothing here works any of it out.
	CostUSD float64
	// What served the invocation, carried back so the stream can be pinned to it:
	// the backend, the selector that was asked for and the model the provider
	// reported serving it, the account it was answered on, the configuration in
	// force while it was, and the harness build that made the call. They are what
	// `durable-state-is-provider-independent` asks of every provider invocation,
	// and a voice reports them whether or not it got an answer, because they are
	// facts about the invocation rather than about what came back.
	Backend       domain.Backend
	Model         string
	ResolvedModel string
	Effort        string
	// ResolvedEffort is provider-reported; EffortReported is false when not reported.
	ResolvedEffort    string `json:"resolved_effort,omitempty"`
	EffortDescription string `json:"effort_description,omitempty"`
	EffortReported    bool   `json:"effort_reported"`
	AccountAlias      string
	ConfigRevision    string
	Build             string
	// LastEvent is the highest sequence this invocation wrote to the stream's
	// log, for a voice that numbers events without going through the sink.
	LastEvent uint64
}

// Ask is one question put to a side thread.
type Ask struct {
	// Stream names a side thread already open, and is empty for a question that
	// opens one.
	Stream string
	// Agent and Role are who is being asked. On a question opening a thread they
	// are who the thread is with, and required. On a question continuing one they
	// are who the asker believes it is with, and optional: a stream is held by the
	// agent that opened it, so a continuation naming another agent is refused
	// rather than carried to the wrong one's provider and merged into the wrong
	// one's memory. A surface that addresses one agent by name says so here, and
	// the runner holds the stream to it.
	Agent string
	Role  domain.AgentRole
	// Conversation and Topic describe the thread being opened and are refused on
	// a question continuing one: what a side thread is beside was settled when
	// it opened.
	Conversation string
	Topic        string
	Question     string
}

// Validate reports every contract violation in the ask at once.
func (a Ask) Validate() error {
	var problems []error
	problems = append(problems, boundedText("question", a.Question, MaxQuestionBytes, true))
	continuing := strings.TrimSpace(a.Stream) != ""
	if !continuing || strings.TrimSpace(a.Agent) != "" {
		if err := domain.ValidateIdentifier("agent", strings.TrimSpace(a.Agent)); err != nil {
			problems = append(problems, err)
		}
	}
	if !continuing || a.Role != "" {
		if !a.Role.Valid() {
			problems = append(problems, fmt.Errorf("side thread role %q is not one of the harness's roles", a.Role))
		}
	}
	if !continuing {
		if !conversationPattern.MatchString(strings.TrimSpace(a.Conversation)) {
			problems = append(problems, fmt.Errorf("side thread conversation %q does not name a main thread", a.Conversation))
		}
		problems = append(problems, boundedText("topic", a.Topic, MaxTopicBytes, true))
	} else {
		if !ValidID(strings.TrimSpace(a.Stream)) {
			problems = append(problems, fmt.Errorf("side stream id %q is invalid", a.Stream))
		}
		if strings.TrimSpace(a.Conversation) != "" || strings.TrimSpace(a.Topic) != "" {
			problems = append(problems, errors.New("a question continuing a side thread names the stream and who is asked, and nothing of what it is beside; that was settled when it opened"))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid side thread question: %w", err)
	}
	return nil
}

// Answer is what one side turn produced, as whoever asked reads it.
type Answer struct {
	// Stream is the record as it now stands, concluded where this turn ended the
	// thread.
	Stream Stream
	// Prose is what the side thread said.
	Prose string
	// Commitments are what it said it would do or have done. Every one is a
	// draft: the design makes a side thread's commitments best effort until the
	// main thread confirms them, so whatever carries this answer onward says so.
	Commitments []string
}

// Tentative reports an answer that promised something the main thread has yet to
// ratify. It is here rather than left to each surface to work out, because the
// surface saying so is the design's requirement rather than a nicety.
func (a Answer) Tentative() bool { return len(a.Commitments) > 0 }

// Runner holds an agent's side conversations: it opens them, carries questions
// to them, and concludes them into the merge.
type Runner struct {
	Store Store
	// Leases is what makes one side stream take its turns one at a time, and it
	// is required. A runner without one is a runner with no exclusion at all: two
	// processes would each load the record and the second write would take the
	// first away, which is a turn somebody paid for and nothing recorded.
	//
	// It is required rather than optional because that exclusion is the whole of
	// what makes this safe to have, and an optional field is one a later caller
	// forgets. The merge above is refused when it is missing for the same reason,
	// and this fails the same way rather than quietly working without it.
	//
	// It is the stream's lease and never the main thread's, which is the whole of
	// the concurrency answer: nothing here is a second holder of what the main
	// conversation serializes on.
	Leases Leases
	Voice  Voice
	Merge  Merge
	// MaxTurns is the cap a newly opened side stream is given, and MaxPerAgent
	// how many side streams one agent may hold at once. A stream already in
	// flight is held to what it recorded when it opened rather than to this, so
	// changing the configuration cannot lengthen a thread that is already running
	// long. Zero takes this package's own default for each.
	MaxTurns     int
	MaxPerAgent  int
	ProductID    domain.ProductID
	RepositoryID string
	Now          func() time.Time
	NewID        func() (string, error)
}

// wired reports a runner assembled without something it cannot work without,
// before anything is recorded.
//
// The record and the lease are both checked here rather than where each is first
// reached, because the alternative writes a stream and then discovers there is
// nothing to hold it with — an orphan record for a turn that was never taken.
// The merge is not checked here: a thread that never concludes never needs one,
// and refusing to open a thread over a merge nothing has asked for yet would
// refuse the case this is for.
func (r Runner) wired() error {
	switch {
	case r.Store == nil:
		return errors.New("no side stream store is wired, so nothing said on a side thread could be recorded")
	case r.Leases == nil:
		return errors.New("no leases are wired, so a turn on a side thread would exclude nothing")
	default:
		return nil
	}
}

func (r Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r Runner) newID() (string, error) {
	if r.NewID != nil {
		return r.NewID()
	}
	return NewID()
}

func (r Runner) turnCap() int {
	if r.MaxTurns < 1 {
		return DefaultMaxTurns
	}
	return r.MaxTurns
}

func (r Runner) perAgent() int {
	if r.MaxPerAgent < 1 {
		return DefaultMaxPerAgent
	}
	return r.MaxPerAgent
}

// Put carries one question to a side thread: it opens a side stream or continues
// one, takes the turn under that stream's own lease, and answers what the role
// said.
//
// A thread the role has answered, and one whose last turn this was, conclude
// here — the substance goes through the merge and the record is closed — so the
// two endings the design names are both reached without a caller pairing
// anything.
func (r Runner) Put(ctx context.Context, ask Ask) (Answer, error) {
	if err := ask.Validate(); err != nil {
		return Answer{}, err
	}
	if err := r.wired(); err != nil {
		return Answer{}, err
	}
	// A thread already open is held for the whole of what happens to it here.
	// Two processes taking turns on one stream at once would each load the record
	// and the second write would take the first away — a turn somebody paid for
	// and nothing recorded, against a cap counting one where two were spent.
	if reference := strings.TrimSpace(ask.Stream); reference != "" {
		release, err := r.hold(reference)
		if err != nil {
			return Answer{}, err
		}
		defer release()
	}
	stream, err := r.begin(ask)
	if err != nil {
		return Answer{}, err
	}
	// A stream this question is opening has an identifier nothing else can have
	// yet, so its lease cannot be contended. It is taken anyway, so that every
	// turn written here is written under one and there is no second rule for the
	// first turn of a thread.
	if strings.TrimSpace(ask.Stream) == "" {
		release, err := r.hold(stream.ID)
		if err != nil {
			return Answer{}, err
		}
		defer release()
	}
	// A continuation that says who it is asking is held to the stream's own record
	// of who holds it. The stream is the authority: it was opened for one agent,
	// its turns are served on that agent's account and its merge is written into
	// that agent's memory, and a caller that named another agent would have all
	// three land on the wrong one. Nothing is written before this is decided.
	if err := stream.heldBy(ask); err != nil {
		return Answer{Stream: stream}, err
	}
	if !stream.Open() {
		return Answer{}, fmt.Errorf("%s ended as %q and cannot be continued; open another side thread if there is more to ask",
			stream.ID, stream.Outcome)
	}
	if stream.TurnsRemaining() == 0 {
		return Answer{Stream: stream}, &SpentError{StreamID: stream.ID, Cap: stream.MaxTurns}
	}

	// The turn is counted before the provider is invoked, which is the direction
	// every durable budget here fails in: a process that dies between the two has
	// spent a turn it did not take rather than taken one it did not count. A cap a
	// crash could reset is not a cap.
	asked := r.now()
	stream.Turns++
	stream.UpdatedAt = asked
	if err := r.Store.Save(stream); err != nil {
		return Answer{}, err
	}

	spoken, lastSequence, speakErr := r.speak(ctx, stream, ask.Question)
	stream.UpdatedAt = r.now()
	stream.LastSequence = lastSequence
	// What served the turn is pinned beside what it cost, and for the same reason
	// it is: both are facts about an invocation that happened, so a turn the
	// provider failed records them exactly as one that answered does.
	stream.CostUSD += spoken.CostUSD
	if spoken.SessionID != "" {
		stream.ProviderSessionID = spoken.SessionID
	}
	if spoken.Backend != "" {
		stream.Backend = spoken.Backend
	}
	if spoken.Model != "" {
		stream.ProviderModel = spoken.Model
	}
	if spoken.ResolvedModel != "" {
		stream.ProviderResolvedModel = spoken.ResolvedModel
	}
	// The level is written whatever it is, empty included, so a stream whose
	// agent stopped naming one does not go on reporting the last level it had.
	stream.ProviderEffort = spoken.Effort
	stream.ProviderResolvedEffort = spoken.ResolvedEffort
	stream.ProviderEffortDescription = spoken.EffortDescription
	stream.ProviderEffortReported = spoken.EffortReported
	if spoken.AccountAlias != "" {
		stream.AccountAlias = spoken.AccountAlias
	}
	if spoken.ConfigRevision != "" {
		stream.ConfigRevision = spoken.ConfigRevision
	}
	if spoken.Build != "" {
		stream.Build = spoken.Build
	}
	// The record is saved whichever way the turn went. A turn that produced no
	// answer still happened, was still charged for, and still counts against the
	// cap, so a record that omitted it would be a budget nothing spent.
	if saveErr := r.Store.Save(stream); saveErr != nil {
		return Answer{Stream: stream}, errors.Join(speakErr, saveErr)
	}
	if speakErr != nil {
		return Answer{Stream: stream}, speakErr
	}

	reply, err := ReadReply(spoken.Answer)
	answer := Answer{Stream: stream, Prose: reply.Prose}
	if err != nil {
		// The prose is still handed back where there was any: a block the harness
		// could not read belongs to an answer somebody wrote, and the turn is spent
		// either way. What is refused is the block, which decides nothing here.
		//
		// A refused reply on the thread's last turn still ends the thread, where
		// there is prose to end it with: the cap is reached whichever way the turn
		// was read, and a stream left open with nothing remaining over a block the
		// harness would not carry out is a thread whose substance reaches nobody.
		// What merges is the prose alone — the block asked for something and was
		// refused, so it drafted nothing the main thread is asked to ratify.
		if stream.TurnsRemaining() == 0 && strings.TrimSpace(reply.Prose) != "" {
			concluded, mergeErr := r.mergeBack(ctx, stream, reply.Prose, nil, OutcomeSpent)
			if mergeErr != nil {
				return answer, errors.Join(err, mergeErr)
			}
			answer.Stream = concluded
		}
		return answer, err
	}
	answer.Commitments = reply.Commitments

	outcome := Outcome("")
	switch {
	case reply.Concluded:
		outcome = OutcomeConcluded
	case stream.TurnsRemaining() == 0:
		// Reaching the cap is not a silent cutoff: what the thread had reached
		// still merges, so a thread cut off half way is legible as one rather than
		// as a thread that said nothing.
		outcome = OutcomeSpent
	}
	if outcome == "" {
		return answer, nil
	}
	concluded, err := r.mergeBack(ctx, stream, reply.Prose, reply.Commitments, outcome)
	if err != nil {
		return answer, err
	}
	answer.Stream = concluded
	return answer, nil
}

// Conclude ends a side thread that Put did not end itself, merging what it
// reached exactly as Put's own ending does.
//
// It is recovery and the caller's own stop, in one operation: a stream left open
// with its turns spent by a process that died, and a thread whoever asked has
// finished with, both need the same thing done to them. What it is not is a
// second way to write memory — the merge is the merge, and this reaches it under
// the stream's own lease so nothing concludes a thread another process is
// carrying.
func (r Runner) Conclude(ctx context.Context, id, substance string, commitments []string, outcome Outcome) (Stream, error) {
	if err := r.wired(); err != nil {
		return Stream{}, err
	}
	if !outcome.Valid() {
		return Stream{}, fmt.Errorf("outcome %q is not one a side stream ends with", outcome)
	}
	release, err := r.hold(strings.TrimSpace(id))
	if err != nil {
		return Stream{}, err
	}
	defer release()
	stream, err := r.Store.Load(strings.TrimSpace(id))
	if err != nil {
		return Stream{}, err
	}
	if !stream.Open() {
		return stream, fmt.Errorf("%s already ended as %q", stream.ID, stream.Outcome)
	}
	return r.mergeBack(ctx, stream, substance, commitments, outcome)
}

// mergeBack hands a concluded side thread's substance to the merge, which writes
// it into the agent's memory and records the stream as ended.
//
// A runner with no merge wired refuses rather than closing the stream quietly.
// Ending a thread whose substance reaches nobody is the one failure this whole
// mechanism exists to prevent, and a record saying "concluded" over it would be
// the only trace left.
func (r Runner) mergeBack(ctx context.Context, stream Stream, substance string, commitments []string, outcome Outcome) (Stream, error) {
	if r.Merge == nil {
		return stream, fmt.Errorf("no merge is wired to %s, so what it concluded would reach the %s agent's context nowhere",
			stream.ID, stream.Agent)
	}
	concluded, err := r.Merge.Conclude(ctx, stream, substance, commitments, outcome, r.now())
	if err != nil {
		return stream, fmt.Errorf("merge %s back into the %s agent's context: %w", stream.ID, stream.Agent, err)
	}
	return concluded, nil
}

// ErrNotThisAgents is what a continuation naming an agent the stream is not held
// by unwraps to, so a surface can tell "this is somebody else's thread" from a
// stream that does not exist without matching on the words.
var ErrNotThisAgents = errors.New("the side stream is held by another agent")

// heldBy refuses a question that names an agent or a role the stream is not held
// by. A question naming neither is held to nothing here: it is a continuation by
// a caller that trusts the identifier, which is what the runner's own tests do.
func (s Stream) heldBy(ask Ask) error {
	asker := strings.TrimSpace(ask.Agent)
	switch {
	case asker != "" && asker != s.Agent:
		return fmt.Errorf("%w: %s is the %s agent %s's thread and not %s's", ErrNotThisAgents, s.ID, s.Role, s.Agent, asker)
	case ask.Role != "" && ask.Role != s.Role:
		return fmt.Errorf("%w: %s is the %s's thread and not the %s's", ErrNotThisAgents, s.ID, s.Role, ask.Role)
	default:
		return nil
	}
}

// begin loads the side stream a question continues, or opens the one it starts.
func (r Runner) begin(ask Ask) (Stream, error) {
	if reference := strings.TrimSpace(ask.Stream); reference != "" {
		return r.Store.Load(reference)
	}
	id, err := r.newID()
	if err != nil {
		return Stream{}, err
	}
	opened := r.now()
	stream := Stream{
		SchemaVersion: SchemaVersion,
		ID:            id,
		ProductID:     r.ProductID,
		RepositoryID:  r.RepositoryID,
		Agent:         strings.TrimSpace(ask.Agent),
		Role:          ask.Role,
		Conversation:  strings.TrimSpace(ask.Conversation),
		Topic:         strings.TrimSpace(ask.Topic),
		// The cap is copied on as the stream opens, so a configuration edit or a
		// second process cannot lengthen a thread already in flight.
		MaxTurns:  r.turnCap(),
		OpenedAt:  opened,
		UpdatedAt: opened,
	}
	// Opening is where the per-agent bound is enforced, under the store's own
	// lease over the agent's opening: counting what an agent holds and adding to
	// it is one decision, and two processes each counting the same open streams
	// would each open.
	if err := r.Store.Open(stream, r.perAgent()); err != nil {
		return Stream{}, err
	}
	return stream, nil
}

// speak takes one turn to the role and returns what it said, together with where
// the stream's own event log reached. The reply is checked by the caller rather
// than here, so a turn that was spent is recorded as spent whether or not what
// came back was readable.
func (r Runner) speak(ctx context.Context, stream Stream, question string) (Spoken, uint64, error) {
	lastSequence := stream.LastSequence
	if r.Voice == nil {
		return Spoken{}, lastSequence, fmt.Errorf("no voice is wired to %s, so there is nobody to ask", stream.ID)
	}
	sink := func(event execution.Event) error {
		// The event is recorded before it is counted, so the sequence the record
		// carries is never ahead of the log it describes.
		if err := r.Store.AppendEvent(event); err != nil {
			return err
		}
		if event.Sequence > lastSequence {
			lastSequence = event.Sequence
		}
		return nil
	}
	spoken, err := r.Voice.Answer(ctx, Question{
		StreamID:            stream.ID,
		Role:                stream.Role,
		Agent:               stream.Agent,
		Conversation:        stream.Conversation,
		Topic:               stream.Topic,
		Turn:                stream.Turns,
		MaxTurns:            stream.MaxTurns,
		Question:            strings.TrimSpace(question),
		SessionID:           stream.ProviderSessionID,
		SessionBackend:      stream.Backend,
		SessionAccountAlias: stream.AccountAlias,
		LastSequence:        stream.LastSequence,
		Events:              sink,
	})
	if spoken.LastEvent > lastSequence {
		lastSequence = spoken.LastEvent
	}
	return spoken, lastSequence, err
}

// hold takes the lease on one side stream for the duration of what is about to
// be done to it, and returns how to give it back.
//
// A runner with no leases wired refuses rather than taking a turn nothing
// excludes, which is the same way a missing merge is answered: the two are the
// guarantees this package exists to hold, and one held only where somebody
// remembered to wire it is not held. A lease a live process holds is a refusal
// rather than a wait: what is owned here is one thread's turn, and queueing for
// it would mean two processes taking turns writing the same turn rather than one
// process having it.
func (r Runner) hold(id string) (func(), error) {
	if r.Leases == nil {
		return nil, fmt.Errorf("no leases are wired to this runner, so a turn on %s would exclude nothing", id)
	}
	lease, taken, err := r.Leases.Hold(id)
	if err != nil {
		return nil, err
	}
	if !taken {
		return nil, fmt.Errorf("%s is being carried by another process; a side thread takes its turns one at a time", id)
	}
	// A lease that will not come back is the operating system's to sort out when
	// this process exits, which is what an advisory lock is for. Nothing here can
	// act on it, and failing the turn over it would throw away an answer already
	// paid for.
	return func() { _ = lease.Release() }, nil
}
