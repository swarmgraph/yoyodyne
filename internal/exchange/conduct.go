package exchange

// Conducting an exchange: opening one, taking a round of it, closing it, and
// escalating the one ending nobody wants.
//
// The conductor is the harness's own hand. Neither role reaches the record, the
// cap, or the escalation — an asking role emits a block and an answering role
// writes prose, and everything between them happens here. That is what makes the
// channel's three properties enforceable rather than requested: the durability,
// the tools nobody has, and the authority nothing carries are all decided on
// this side of the boundary.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Store is the durable home of exchanges. It is satisfied by
// runstate.ExchangeStore.
type Store interface {
	Save(recorded Exchange) error
	Load(id string) (Exchange, error)
}

// Leases is who may carry an exchange right now. It is satisfied by
// runstate.ExchangeStore, and it is a second interface rather than two more
// methods on Store because holding an exchange and recording one are different
// questions: a listing reads the records and takes nothing.
type Leases interface {
	// Hold takes the exclusive lease on one exchange without waiting, reporting
	// whether it got it. A lease it could not take belongs to a live process.
	Hold(id string) (Release, bool, error)
}

// Release gives a lease back. It is what runstate.Lease already is, named here
// so the durable package can satisfy this one rather than this one depending on
// it.
type Release interface{ Release() error }

// Reports is the collected pile an exchange escalates into when it runs out of
// rounds. It is the same pile every role's reports land in, because an operator
// reading what the harness noticed should not have to know that this one came
// from a conversation between two agents.
type Reports interface {
	Append(reported report.Report) error
}

// Voice is the answering half of the channel: one read-only provider invocation
// that puts a question to a role and returns its judgment. It is an interface so
// that what conducts an exchange does not depend on which provider answers, and
// so a test can conduct a whole thread without one.
type Voice interface {
	Answer(ctx context.Context, question Question) (Spoken, error)
}

// Question is one round as the answering side receives it.
type Question struct {
	ExchangeID string
	// Role is who answers and Asker is who is asking. The answering side is told
	// which role is asking because that is part of what it is being asked: what
	// the product manager needs from an architect is not what a development
	// manager needs from one.
	Role  domain.AgentRole
	Asker domain.AgentRole
	// Round is which round this is and MaxRounds the exchange's durable cap.
	// Both are said to the answering role, so it knows whether it is being asked
	// to open something up or to land it.
	Round     int
	MaxRounds int
	Question  string
	Context   string
	// Earlier is what has already been said in this exchange, oldest first. It is
	// supplied on every round; a voice that continues a provider session may
	// ignore it, and one that cannot has everything it needs to render the thread.
	Earlier []Round
	// SessionID is the provider session this exchange has been answered in so
	// far, empty on the first round.
	SessionID           string
	SessionBackend      domain.Backend
	SessionAccountAlias string
}

// Spoken is what the answering side produced.
type Spoken struct {
	// Answer is the reply as the provider wrote it. It is checked by ReadAnswer
	// before anything is recorded, so a voice never has to enforce the boundary
	// itself.
	Answer string
	// Agent is the configured agent that answered, recorded so an exchange says
	// which architect it was.
	Agent string
	// SessionID is the provider session a later round continues.
	SessionID string
	// CostUSD is what the provider charged for this invocation, as it reported
	// it. Nothing here works any of it out.
	CostUSD float64
	// What served the invocation, carried back so the round can be pinned to it:
	// the backend, the selector that was asked for and the model the provider
	// reported serving it, the account it was answered on, the configuration
	// revision in force while it was, and the harness build that made the call. A
	// voice reports them whether or not it got an answer, because they are facts
	// about the invocation rather than about what came back, and the durable
	// record needs them either way.
	Backend           domain.Backend
	Model             string
	ResolvedModel     string
	Effort            string
	ResolvedEffort    string
	EffortDescription string
	EffortReported    bool
	AccountAlias      string
	ConfigRevision    string
	Build             string
}

// ErrRoundsSpent is what a refusal past an exchange's cap unwraps to, so a
// caller can tell "this exchange has had its rounds" from a store that could not
// be read without matching on the words of either.
var ErrRoundsSpent = errors.New("exchange round cap reached")

// CapError reports an exchange that reached its cap. The exchange itself is
// closed by the time this is returned and the operator has been told, so this is
// the asker being informed rather than an action it can retry.
type CapError struct {
	ExchangeID string
	Rounds     int
	Cap        int
}

func (e *CapError) Error() string {
	return fmt.Sprintf("%s has spent all %d of its permitted rounds and is closed as unresolved; the operator has been told",
		e.ExchangeID, e.Cap)
}

func (e *CapError) Unwrap() error { return ErrRoundsSpent }

// ErrNoExchange reports an identifier that names nothing recorded.
var ErrNoExchange = errors.New("no exchange is recorded under that identifier")

// Conductor carries questions between two roles and keeps the record of what
// they said.
type Conductor struct {
	Store Store
	// Leases is what makes one exchange take its rounds one at a time. A
	// conductor wired without one still works and is what a test conducting a
	// thread on its own gets; wired, a round on a thread another process is
	// carrying is refused rather than written over that process's round.
	Leases  Leases
	Voice   Voice
	Reports Reports
	// MaxRounds is the cap a newly opened exchange is given. An exchange already
	// in flight is held to what it recorded when it opened rather than to this,
	// so changing the configuration cannot lengthen a thread that is already
	// running long.
	MaxRounds    int
	ProductID    domain.ProductID
	RepositoryID string
	// Holder names this process on every round it takes. A conductor wired
	// without one still takes rounds; what is lost is that a pass reclaiming an
	// interrupted round can say who was carrying it rather than only that
	// somebody was.
	Holder string
	Now    func() time.Time
	NewID  func() (string, error)
}

func (c Conductor) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c Conductor) newID() (string, error) {
	if c.NewID != nil {
		return c.NewID()
	}
	return NewID()
}

func (c Conductor) cap() int {
	if c.MaxRounds < 1 {
		return DefaultMaxRounds
	}
	return c.MaxRounds
}

// Put carries one ask. It opens an exchange or continues one, takes the round,
// and returns the exchange as it now stands — the last round of which holds
// either the answer or why there is none.
//
// An ask that settles closes the exchange instead, which is the ordinary way one
// ends.
func (c Conductor) Put(ctx context.Context, ask Ask, asker Party) (Exchange, error) {
	if err := ask.Validate(); err != nil {
		return Exchange{}, err
	}
	if err := asker.validate("asker"); err != nil {
		return Exchange{}, err
	}
	// A thread already open is held for the whole of what happens to it here,
	// whether that is another round or the close. Two processes acting on one
	// thread at once would each load the record and the second write would take
	// the first away — a round somebody paid for and nothing recorded, against a
	// cap counting one where two were spent.
	if reference := strings.TrimSpace(ask.Exchange); reference != "" {
		release, err := c.hold(reference)
		if err != nil {
			return Exchange{}, err
		}
		defer release()
	}
	if ask.Closing() {
		return c.settle(ask.Exchange, ask.Settled, asker)
	}
	// A follow-up names no role, and the one that does is held to the thread it is
	// continuing rather than allowed to redirect it. Only an ask that opens an
	// exchange decides who is being asked, so only that one can pick itself.
	if ask.Role != "" && ask.Role == asker.Role {
		return Exchange{}, fmt.Errorf("the %s cannot ask itself; ask the role that holds the judgement you are missing", asker.Role.Title())
	}

	recorded, err := c.begin(ask, asker)
	if err != nil {
		return Exchange{}, err
	}
	// An exchange this ask is opening has an identifier nothing else can have yet,
	// so the lease cannot be contended. It is taken anyway, so that every round
	// written here is written under one and there is no second rule for the first
	// round of a thread.
	if strings.TrimSpace(ask.Exchange) == "" {
		release, err := c.hold(recorded.ID)
		if err != nil {
			return Exchange{}, err
		}
		defer release()
	}
	// The cap is asked before anything is spent, and reaching it is not a silent
	// cutoff: the exchange closes, the operator is told, and the asker is
	// refused with the reason rather than being left waiting on a round that
	// will never be taken.
	if recorded.Spent() >= recorded.MaxRounds {
		closed, err := c.exhaust(recorded)
		if err != nil {
			return recorded, err
		}
		return closed, &CapError{ExchangeID: closed.ID, Rounds: closed.Spent(), Cap: closed.MaxRounds}
	}

	// The round is recorded before the provider is invoked, which is the same
	// direction every durable budget here fails in: a process that dies between
	// the two has spent a round it did not take, rather than taken one it did not
	// count. A cap that a crash could reset is not a cap.
	asked := c.now()
	recorded.Rounds = append(recorded.Rounds, Round{
		Number:   recorded.Spent() + 1,
		Question: strings.TrimSpace(ask.Question),
		Context:  strings.TrimSpace(ask.Context),
		// The process taking the round is named on it before the provider is
		// reached, for the same reason the round itself is written first: what a
		// later pass needs to know about a round nobody answered is who was
		// carrying it, and the answer that never came back cannot carry that.
		Holder:  strings.TrimSpace(c.Holder),
		AskedAt: asked,
	})
	recorded.UpdatedAt = asked
	if err := c.save(recorded); err != nil {
		return Exchange{}, err
	}

	round := &recorded.Rounds[len(recorded.Rounds)-1]
	spoken, err := c.speak(ctx, recorded, *round)
	answered := c.now()
	round.AnsweredAt = &answered
	round.CostUSD = spoken.CostUSD
	// What served the round is pinned beside what it cost, and for the same reason
	// it is: both are facts about an invocation that happened, so a round the
	// provider failed records them exactly as one that answered does.
	round.Backend = spoken.Backend
	round.Model = spoken.Model
	round.ResolvedModel = spoken.ResolvedModel
	round.Effort = spoken.Effort
	round.ResolvedEffort = spoken.ResolvedEffort
	round.EffortDescription = spoken.EffortDescription
	round.EffortReported = spoken.EffortReported
	round.AccountAlias = spoken.AccountAlias
	round.ConfigRevision = spoken.ConfigRevision
	round.Build = spoken.Build
	recorded.UpdatedAt = answered
	previousBackend, previousAccount := previousSessionEndpoint(recorded.Rounds[:len(recorded.Rounds)-1])
	if spoken.Backend != "" && (spoken.Backend != previousBackend || (spoken.AccountAlias != "" && spoken.AccountAlias != previousAccount)) {
		recorded.AnswererSessionID = ""
	}
	if spoken.SessionID != "" {
		recorded.AnswererSessionID = spoken.SessionID
	}
	if strings.TrimSpace(spoken.Agent) != "" {
		recorded.Answerer.Agent = strings.TrimSpace(spoken.Agent)
	}
	switch {
	case err != nil:
		round.Problem = singleLine(err.Error())
	default:
		round.Answer = spoken.Answer
	}
	// The round is saved whichever way it went. A round that produced no answer
	// still happened, was still charged for, and still counts against the cap, so
	// a record that omitted it would be a budget nothing spent.
	if saveErr := c.save(recorded); saveErr != nil {
		return recorded, errors.Join(err, saveErr)
	}
	return recorded, err
}

// Charge attributes to an exchange what the asking side spent carrying an answer
// back. The invocation that produced a question belongs to whatever the asker
// was already doing; the one it takes only because an answer arrived belongs to
// the exchange, and adding it here is what makes the figure beside the rounds
// the whole of what the conversation between the two roles cost.
func (c Conductor) Charge(id string, costUSD float64) (Exchange, error) {
	if costUSD <= 0 {
		return c.Store.Load(id)
	}
	recorded, err := c.Store.Load(id)
	if err != nil {
		return Exchange{}, err
	}
	if len(recorded.Rounds) == 0 {
		return recorded, nil
	}
	recorded.Rounds[len(recorded.Rounds)-1].CostUSD += costUSD
	recorded.UpdatedAt = c.now()
	if err := c.save(recorded); err != nil {
		return Exchange{}, err
	}
	return recorded, nil
}

// begin loads the exchange an ask continues, or opens the one it starts.
func (c Conductor) begin(ask Ask, asker Party) (Exchange, error) {
	reference := strings.TrimSpace(ask.Exchange)
	if reference == "" {
		id, err := c.newID()
		if err != nil {
			return Exchange{}, err
		}
		opened := c.now()
		return Exchange{
			SchemaVersion: SchemaVersion,
			ID:            id,
			ProductID:     c.ProductID,
			RepositoryID:  c.RepositoryID,
			Asker:         asker,
			Answerer:      Party{Role: ask.Role},
			Question:      strings.TrimSpace(ask.Question),
			Refers:        ask.Refers,
			MaxRounds:     c.cap(),
			OpenedAt:      opened,
			UpdatedAt:     opened,
		}, nil
	}
	recorded, err := c.Store.Load(reference)
	if err != nil {
		return Exchange{}, err
	}
	if !recorded.Open() {
		return Exchange{}, fmt.Errorf("%s closed %s and cannot be continued; open a new exchange if there is more to ask",
			recorded.ID, recorded.Outcome)
	}
	// Who is in an exchange is fixed when it opens. A thread another role picked
	// up half way through, or one redirected to a third role, is two conversations
	// wearing one identifier — and the record would no longer say who said what.
	if recorded.Asker.Role != asker.Role {
		return Exchange{}, fmt.Errorf("%s is the %s's exchange, so the %s cannot continue it",
			recorded.ID, recorded.Asker.Role.Title(), asker.Role.Title())
	}
	// A follow-up that named no role takes the one the exchange was opened with,
	// which is the whole reason it need not restate it. One that named a different
	// role is refused rather than redirected: the answering side has a provider
	// session holding this thread, and half a thread answered by somebody else is
	// a record that no longer says who said what.
	if ask.Role != "" && recorded.Answerer.Role != ask.Role {
		return Exchange{}, fmt.Errorf("%s is asking the %s, so it cannot be continued to the %s",
			recorded.ID, recorded.Answerer.Role.Title(), ask.Role.Title())
	}
	return recorded, nil
}

// settle closes an exchange the asker is finished with, recording what it took
// from it.
func (c Conductor) settle(reference, settled string, asker Party) (Exchange, error) {
	recorded, err := c.Store.Load(strings.TrimSpace(reference))
	if err != nil {
		return Exchange{}, err
	}
	if !recorded.Open() {
		return recorded, fmt.Errorf("%s already closed %s", recorded.ID, recorded.Outcome)
	}
	if recorded.Asker.Role != asker.Role {
		return Exchange{}, fmt.Errorf("%s is the %s's exchange, so the %s cannot close it",
			recorded.ID, recorded.Asker.Role.Title(), asker.Role.Title())
	}
	closed := c.now()
	recorded.Outcome = OutcomeResolved
	recorded.Settled = strings.TrimSpace(settled)
	recorded.ClosedAt = &closed
	recorded.UpdatedAt = closed
	if err := c.save(recorded); err != nil {
		return Exchange{}, err
	}
	return recorded, nil
}

// hold takes the lease on one exchange for the duration of what is about to be
// done to it, and returns how to give it back.
//
// A conductor with no leases wired takes nothing and returns a release that does
// nothing, which is what a test conducting a thread on its own gets. A lease a
// live process holds is a refusal rather than a wait: what is owned here is one
// thread's turn, and queueing for it would mean two processes taking turns
// writing the same round rather than one process having it.
func (c Conductor) hold(id string) (func(), error) {
	if c.Leases == nil {
		return func() {}, nil
	}
	lease, taken, err := c.Leases.Hold(id)
	if err != nil {
		return nil, err
	}
	if !taken {
		return nil, fmt.Errorf("%s is being carried by another process; it takes its rounds one at a time", id)
	}
	// A lease that will not come back is the operating system's to sort out when
	// this process exits, which is what an advisory lock is for. Nothing here can
	// act on it, and failing the round over it would throw away an answer already
	// paid for.
	return func() { _ = lease.Release() }, nil
}

// Reclaim closes a round that was asked and never came back, saying why.
//
// It is recovery and not delivery. The round was spent the moment it was
// written, so nothing here gives it back; what it does is stop the record
// reading as a question somebody is still working on, and put the reason where
// the answer would have been. The exchange stays open, and whether it is asked
// again is the next pass's decision.
//
// The caller holds the exchange's lease, and the record it hands in was read
// under that lease. Both halves are needed and only the first is obvious. Without
// the lease this cannot tell a round whose process died from one a process is
// taking right now. With the lease but a record read before it, the carrier may
// have finished and released in between — so the copy still shows the round
// unanswered, and reclaiming it writes over an answer that had already come back.
func (c Conductor) Reclaim(recorded Exchange, because string) (Exchange, error) {
	round, interrupted := recorded.Interrupted()
	if !interrupted {
		return recorded, fmt.Errorf("%s has no round waiting to be reclaimed", recorded.ID)
	}
	if !recorded.Open() {
		return recorded, fmt.Errorf("%s closed %s, so there is nothing to reclaim", recorded.ID, recorded.Outcome)
	}
	reclaimed := c.now()
	at := len(recorded.Rounds) - 1
	recorded.Rounds[at].AnsweredAt = &reclaimed
	recorded.Rounds[at].Problem = singleLine(strings.TrimSpace(because))
	recorded.UpdatedAt = reclaimed
	if err := c.save(recorded); err != nil {
		return Exchange{}, fmt.Errorf("reclaim round %d of %s: %w", round.Number, recorded.ID, err)
	}
	return recorded, nil
}

// Exhaust closes an exchange that has spent every round it was opened with and
// tells the operator, for a thread nobody asked anything further.
//
// The conductor already does this the moment somebody asks past the cap, and
// that covers the exchange whose asker came back. This covers the one that did
// not: a thread whose last round died, or whose asker moved on, would otherwise
// sit open for ever with its rounds spent and nobody told — which is the same
// silence the cap exists to break.
//
// The caller holds the exchange's lease, for the reason Reclaim's does.
func (c Conductor) Exhaust(recorded Exchange) (Exchange, error) {
	if !recorded.Open() {
		return recorded, fmt.Errorf("%s already closed %s", recorded.ID, recorded.Outcome)
	}
	if recorded.Spent() < recorded.MaxRounds {
		return recorded, fmt.Errorf("%s has %d of its %d rounds left, so it has not run out",
			recorded.ID, recorded.RoundsRemaining(), recorded.MaxRounds)
	}
	return c.exhaust(recorded)
}

// exhaust closes an exchange that reached its cap and escalates it. The two
// happen together and in that order: the exchange is closed first so nothing can
// take another round while the operator is being told, and the escalation is the
// whole reason the cap is worth having — a limit that ended a conversation
// quietly would turn a loop nobody noticed into an answer nobody got.
func (c Conductor) exhaust(recorded Exchange) (Exchange, error) {
	closed := c.now()
	recorded.Outcome = OutcomeUnresolved
	recorded.ClosedAt = &closed
	recorded.UpdatedAt = closed
	if err := c.save(recorded); err != nil {
		return Exchange{}, err
	}
	if err := c.escalate(recorded, closed); err != nil {
		// The exchange is closed either way. A failed escalation is reported to
		// the asker rather than swallowed, because an unresolved exchange nobody
		// was told about is exactly what this ending exists to prevent.
		return recorded, fmt.Errorf("%s closed unresolved and the operator could not be told: %w", recorded.ID, err)
	}
	return recorded, nil
}

// escalate files the unresolved exchange into the collected pile, at warning
// severity: nothing is broken, and two roles failed to settle something one of
// them needed, which is a question for the operator rather than news.
func (c Conductor) escalate(recorded Exchange, at time.Time) error {
	if c.Reports == nil {
		return errors.New("no report collection is wired to this exchange, so an unresolved one reaches nobody")
	}
	message := fmt.Sprintf("%s closed unresolved after %d round(s), the limit it was opened with. The %s asked the %s %q and the two did not settle it; the exchange cost %s. Read it with `yoyo exchange show %s` and decide it, or say what neither of them could.",
		recorded.ID, recorded.Spent(), recorded.Asker.Role.Title(), recorded.Answerer.Role.Title(),
		singleLine(recorded.Question), money(recorded.CostUSD()), recorded.ID)
	collected, err := report.Collect(
		[]report.Entry{{Severity: report.SeverityWarning, Message: message}},
		report.Attribution{
			Role:  recorded.Asker.Role,
			Agent: recorded.Asker.Agent,
			// The exchange is the record this leads back to, exactly as a run is for
			// a report filed inside one.
			RunID:        recorded.ID,
			Build:        recorded.lastBuild(),
			ProductID:    recorded.ProductID,
			RepositoryID: recorded.RepositoryID,
		}, at)
	if err != nil {
		return err
	}
	for _, reported := range collected {
		if err := c.Reports.Append(reported); err != nil {
			return err
		}
	}
	return nil
}

// speak takes one round to the answering role and checks what came back. The
// check is here rather than in the voice so that every provider is held to the
// same boundary: an answer carrying a harness block is refused whole, and the
// round records that its question went unanswered.
func (c Conductor) speak(ctx context.Context, recorded Exchange, round Round) (Spoken, error) {
	if c.Voice == nil {
		return Spoken{}, errors.New("no answering voice is wired to this exchange, so there is nobody to ask")
	}
	// The rounds before this one are what has already been said; the round being
	// taken is the question itself and is not part of it.
	earlier := recorded.Rounds[:len(recorded.Rounds)-1]
	sessionBackend, sessionAccount := previousSessionEndpoint(earlier)
	spoken, err := c.Voice.Answer(ctx, Question{
		ExchangeID:          recorded.ID,
		Role:                recorded.Answerer.Role,
		Asker:               recorded.Asker.Role,
		Round:               round.Number,
		MaxRounds:           recorded.MaxRounds,
		Question:            round.Question,
		Context:             round.Context,
		Earlier:             earlier,
		SessionID:           recorded.AnswererSessionID,
		SessionBackend:      sessionBackend,
		SessionAccountAlias: sessionAccount,
	})
	if err != nil {
		return spoken, err
	}
	answer, err := ReadAnswer(spoken.Answer)
	if err != nil {
		return spoken, err
	}
	spoken.Answer = answer
	return spoken, nil
}

func (c Conductor) save(recorded Exchange) error {
	if c.Store == nil {
		return errors.New("no exchange store is wired, so nothing said here could be recorded")
	}
	return c.Store.Save(recorded)
}

// singleLine flattens text that is about to be put in a one-line message.
func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func money(amount float64) string { return fmt.Sprintf("$%.4f", amount) }

// previousSessionEndpoint identifies the most recent answering invocation. A
// failed round with no provider identity does not replace the session's owner.
func previousSessionEndpoint(rounds []Round) (domain.Backend, string) {
	for i := len(rounds) - 1; i >= 0; i-- {
		if rounds[i].Backend != "" {
			return rounds[i].Backend, rounds[i].AccountAlias
		}
	}
	return "", ""
}
