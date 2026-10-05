// Package exchange is the inter-role ask channel: one role puts a question to
// another through the harness, and the whole of what they say to each other is
// recorded where anybody can read it.
//
// It exists because a one-question exchange used to cost either an operator
// relaying it by hand or a whole work-item cycle. Three properties are what make
// it safe to let roles talk to each other directly, and every one of them is
// enforced here rather than asked for in a persona:
//
// It is durable and visible. An exchange is a record under the product's state,
// written before each round is taken and read by `yoyo exchange`, so there are
// no side conversations: two roles cannot say anything to each other that
// nobody else can see afterwards.
//
// It is judgment-only. Both halves have read-only access, and an answer is
// advice rather than an independently validated result. An answer that carries any harness block at all is
// refused rather than acted on, and work that needs something verified is
// commissioned as developer work exactly as it was before.
//
// It is decisionless. No authority moves through an ask. Nothing an answering
// role says admits work, reorders a backlog, edits a document, or resolves
// anything; decisions still land as amendments, proposals, and directives.
//
// The channel is bidirectional and both directions are first-class. The product
// manager asking the architect "what does this goal cost, and what am I
// missing?" and the architect asking the product manager "if we sacrifice some
// performance, is that an unacceptable trade-off from the user's standpoint?"
// are the same mechanism with the parties swapped, and every property above
// holds identically either way.
package exchange

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// SchemaVersion is 1 and has never changed. An exchange is written as it goes:
// each round is recorded before it is taken and revised once the answer is in,
// and the record is closed exactly once.
const SchemaVersion = 1

// Fence opens the one block a reply may ask through. It is a distinct language
// tag for the same reason the report and proposal fences are: an ask can never
// be confused with JSON the conversation happens to be discussing.
const Fence = "```yoyodyne-ask"

// The bounds on one ask. A question is a question rather than a document, and
// the context around it is the asker's own framing rather than an attempt to
// re-brief the role being asked — both are generous for what they are and small
// enough that nothing can push a specification through this channel.
const (
	MaxQuestionBytes = 4 << 10
	MaxContextBytes  = 8 << 10
	// MaxAnswerBytes bounds what an answering role says back. It is larger than
	// the question because judgment is the thing being asked for, and it is still
	// bounded: an answer that runs past this is truncated in the record rather
	// than being allowed to become the whole of a turn.
	MaxAnswerBytes = 16 << 10
	// MaxSettledBytes bounds what the asker records as the outcome when it closes
	// an exchange itself.
	MaxSettledBytes = 4 << 10
	// MaxBlockBytes bounds the untrusted ask payload one turn may carry.
	MaxBlockBytes = 16 << 10
)

// DefaultMaxRounds is the hard limit on rounds in one exchange where a project
// configures none. Ten is far more than any real question needs and small enough
// that the pathological case — two judgment models deferring to each other
// politely for ever — costs a bounded amount before it becomes one legible
// question for the operator.
const DefaultMaxRounds = 10

// Outcome is how an exchange ended. An exchange that has not ended carries none,
// which is the ordinary state of one being conducted.
type Outcome string

const (
	// OutcomeResolved is the ordinary ending: the asker had what it needed and
	// said so.
	OutcomeResolved Outcome = "resolved"
	// OutcomeUnresolved is the cap being reached. It is deliberately not a silent
	// cutoff: the exchange closes as this, and the harness escalates it to the
	// operator, so a conversation that would have gone round for ever becomes a
	// rare question somebody can answer instead.
	OutcomeUnresolved Outcome = "unresolved-after-rounds"
)

func (o Outcome) Valid() bool {
	switch o {
	case OutcomeResolved, OutcomeUnresolved:
		return true
	default:
		return false
	}
}

// Party is one side of an exchange: the role whose authority is in play, the
// configured agent that filled it, and the durable conversation it spoke from.
// The role and the agent are both recorded for the reason a report records both:
// a project may configure two architects, and "which architect said this" is a
// different question from "the architect said this".
type Party struct {
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	// Conversation is the record the party spoke from, where it spoke from one.
	// The asking side always has one; the answering side is a provider invocation
	// the harness makes for the exchange rather than a conversation an operator
	// holds, so it names none.
	Conversation string `json:"conversation,omitempty"`
}

func (p Party) validate(what string) error {
	if !p.Role.Valid() {
		return fmt.Errorf("%s role %q is not one of the harness's roles", what, p.Role)
	}
	return nil
}

// Round is one question and the answer to it. The question is recorded before
// the answering provider is invoked, so a process that dies between the two
// leaves a round that was spent rather than one that was taken and never
// counted — the same direction every other durable budget here fails in.
type Round struct {
	Number   int    `json:"number"`
	Question string `json:"question"`
	Context  string `json:"context,omitempty"`
	Answer   string `json:"answer,omitempty"`
	// Holder names the process that was taking this round, so a later pass
	// reading a round nobody answered can say who was carrying it. It is what
	// turns "this round was interrupted" into something an operator can act on:
	// a harness that is still running and a harness that died look identical
	// from the record otherwise. It is absent on a round recorded before holders
	// were written down, and on one no process claimed.
	Holder string `json:"holder,omitempty"`
	// Problem is why this round has no answer, where it has none. A round the
	// answering provider failed is still a round the exchange spent, and saying
	// what happened is what stops it reading as a question nobody asked.
	Problem string `json:"problem,omitempty"`
	// CostUSD is what this round cost, as the provider reported it: the answering
	// invocation, plus the asking invocation the harness took only to carry the
	// answer back. The invocation that produced the question belongs to whatever
	// the asker was already doing and is charged there.
	CostUSD    float64    `json:"cost_usd,omitempty"`
	AskedAt    time.Time  `json:"asked_at"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
	// What served the answering invocation: the backend it went to, the selector
	// it asked for and the model the provider reported serving it, the provider
	// account it was answered on, and the configuration revision in force while it
	// was. Together they are what the durable-state-is-provider-independent
	// invariant asks of every provider invocation, and an answering round is the
	// invocation with no run and no conversation behind it — so without this the
	// exchange record would name a provider session and nothing that outlives it.
	//
	// They are on the round rather than on the exchange because a thread can span
	// a configuration edit or a change of account, and what is being pinned is what
	// served each round rather than what serves the thread. They are recorded
	// whichever way the round went, since a round the provider failed was answered
	// on an account and charged for like any other.
	//
	// The build is the sixth and answers a question the other five cannot: which
	// harness binary made the call. A process that stays open runs whatever it was
	// started with, so an exchange conducted by a resident says what served it and
	// nothing about whether that resident was the harness that is deployed.
	//
	// All six are empty on a round recorded before the harness wrote them down,
	// and on one no voice with a provider behind it ever took. The build is empty
	// for one further reason: a binary built without the stamping carries no
	// revision at all, which is a comparison nobody can make rather than a round
	// that ran on what is deployed.
	Backend       domain.Backend `json:"backend,omitempty"`
	Model         string         `json:"model,omitempty"`
	ResolvedModel string         `json:"resolved_model,omitempty"`
	// Effort is the effort level the round asked the provider for, and absent
	// where the answering agent configured none.
	Effort string `json:"effort,omitempty"`
	// ResolvedEffort is provider-reported; EffortReported is false when not reported.
	ResolvedEffort string `json:"resolved_effort,omitempty"`
	EffortReported bool   `json:"effort_reported"`
	AccountAlias   string `json:"account_alias,omitempty"`
	ConfigRevision string `json:"config_revision,omitempty"`
	Build          string `json:"build,omitempty"`
}

// Exchange is one durable ask thread.
type Exchange struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	ProductID     domain.ProductID `json:"product_id"`
	RepositoryID  string           `json:"repository_id,omitempty"`
	Asker         Party            `json:"asker"`
	Answerer      Party            `json:"answerer"`
	// Question is the opening question, kept as it was asked. The rounds hold
	// every question including this one; this is here so a listing can say what an
	// exchange is about without reading the thread.
	Question string `json:"question"`
	// Refers are the durable things this question was asked against, each at the
	// revision the asking role read. They are what makes staleness derivable: a
	// question put against a goal that has since been amended may no longer be
	// the question, and without the revision nothing can tell that from a goal
	// nobody has touched. They are optional, because most questions rest on
	// nothing anybody versions.
	Refers []Reference `json:"refers,omitempty"`
	// MaxRounds is the hard limit this exchange is held to. It is copied onto the
	// exchange when it opens rather than read from the configuration each time,
	// so a process death, a configuration edit, or a second process cannot reset
	// what a thread already in flight is allowed to spend.
	MaxRounds int     `json:"max_rounds"`
	Rounds    []Round `json:"rounds,omitempty"`
	// AnswererSessionID is the provider session the answering side continues
	// across rounds, so round three is answered by something that remembers
	// rounds one and two.
	AnswererSessionID string `json:"answerer_session_id,omitempty"`
	// Outcome is empty while the exchange is open.
	Outcome Outcome `json:"outcome,omitempty"`
	// Settled is what the asker says the exchange came to, recorded when it
	// closes one itself. An exchange the cap closed has none: that is the whole
	// meaning of unresolved.
	Settled   string     `json:"settled,omitempty"`
	OpenedAt  time.Time  `json:"opened_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

var idPattern = regexp.MustCompile(`^exchange-[a-f0-9]{32}$`)

// The shape of the two things a round records about how it was served. They are
// stated here rather than imported from the configuration package for the reason
// the run record states its own: the durable schema stays independent of the code
// that produces what it stores, so a record is checked against what a record may
// hold rather than against what this version of the harness happens to write.
var (
	accountAliasPattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	configRevisionPattern = regexp.MustCompile(`^cfg-[a-f0-9]{8,}$`)
	buildPattern          = regexp.MustCompile(`^[a-f0-9]{7,64}$`)
)

func NewID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate exchange id: %w", err)
	}
	return "exchange-" + hex.EncodeToString(bytes), nil
}

// ValidID reports an identifier of the shape this package issues. A store names
// a file after it, so it is checked before anything built from outside is used
// as a path.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Open reports whether this exchange is still being conducted.
func (e Exchange) Open() bool { return e.Outcome == "" }

// Spent is how many rounds this exchange has taken.
func (e Exchange) Spent() int { return len(e.Rounds) }

// lastBuild is the harness revision the latest round that recorded one was
// spoken under, which is the build an exchange's own report is about. A round
// written before rounds carried it records none, and an exchange of only those
// reports none rather than a guess.
func (e Exchange) lastBuild() string {
	for i := len(e.Rounds) - 1; i >= 0; i-- {
		if e.Rounds[i].Build != "" {
			return e.Rounds[i].Build
		}
	}
	return ""
}

// RoundsRemaining is how many further rounds this exchange may still take. It is
// never negative: an exchange at its cap has nothing remaining rather than a
// debt.
func (e Exchange) RoundsRemaining() int {
	if remaining := e.MaxRounds - len(e.Rounds); remaining > 0 {
		return remaining
	}
	return 0
}

// Interrupted is the round that was asked and never came back, where there is
// one: no answer, and no account of why there is none.
//
// It is the crash window made readable. A round is on disk before the answering
// provider is invoked and revised the moment anything comes back, so a round in
// this state is one whose process died between the two — and a round that failed
// is not one, because a failure records the problem. Only the last round can be
// in it, which Validate holds the record to.
//
// Whether the process is actually gone is not a question the record answers: a
// round being taken right now looks exactly like this. The lease is what settles
// it, which is why nothing here is acted on without one.
func (e Exchange) Interrupted() (Round, bool) {
	if len(e.Rounds) == 0 {
		return Round{}, false
	}
	last := e.Rounds[len(e.Rounds)-1]
	if last.AnsweredAt == nil && strings.TrimSpace(last.Answer) == "" && strings.TrimSpace(last.Problem) == "" {
		return last, true
	}
	return Round{}, false
}

// CostUSD is what conducting this exchange has cost, summed from what the
// provider reported for each round. It is reported beside the rounds wherever an
// exchange is read, because what a conversation between two roles spent is
// exactly the thing an operator cannot otherwise see.
func (e Exchange) CostUSD() float64 {
	var total float64
	for _, round := range e.Rounds {
		total += round.CostUSD
	}
	return total
}

// Validate reports every contract violation in the exchange at once.
func (e Exchange) Validate() error {
	var problems []error
	if e.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("exchange schema version %d is not supported", e.SchemaVersion))
	}
	if !ValidID(e.ID) {
		problems = append(problems, fmt.Errorf("exchange id %q is invalid", e.ID))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	problems = append(problems, e.Asker.validate("asker"), e.Answerer.validate("answerer"))
	if e.Asker.Role == e.Answerer.Role && e.Asker.Role.Valid() {
		problems = append(problems, errors.New("an exchange is between two roles; a role does not ask itself"))
	}
	problems = append(problems, boundedText("question", e.Question, MaxQuestionBytes, true))
	problems = append(problems, validateReferences("refers", e.Refers))
	if e.MaxRounds < 1 {
		problems = append(problems, fmt.Errorf("max rounds is %d; an exchange is allowed at least one round", e.MaxRounds))
	}
	if len(e.Rounds) > e.MaxRounds {
		problems = append(problems, fmt.Errorf("%d rounds are recorded against a cap of %d", len(e.Rounds), e.MaxRounds))
	}
	for i, round := range e.Rounds {
		if round.Number != i+1 {
			problems = append(problems, fmt.Errorf("rounds[%d] is numbered %d", i, round.Number))
		}
		problems = append(problems, boundedText(fmt.Sprintf("rounds[%d] question", i), round.Question, MaxQuestionBytes, true))
		problems = append(problems, boundedText(fmt.Sprintf("rounds[%d] context", i), round.Context, MaxContextBytes, false))
		problems = append(problems, boundedText(fmt.Sprintf("rounds[%d] answer", i), round.Answer, MaxAnswerBytes, false))
		problems = append(problems, boundedText(fmt.Sprintf("rounds[%d] holder", i), round.Holder, MaxReferenceBytes, false))
		if round.AskedAt.IsZero() {
			problems = append(problems, fmt.Errorf("rounds[%d] records no moment it was asked", i))
		}
		// Only the last round can be the one still being taken. An earlier one left
		// unanswered behind a later one is a record two processes wrote over each
		// other, and reclaiming against it would be exactly the double delivery the
		// lease exists to prevent.
		if round.AnsweredAt == nil && i != len(e.Rounds)-1 {
			problems = append(problems, fmt.Errorf("rounds[%d] was never answered and a later round was taken behind it", i))
		}
		if round.CostUSD < 0 {
			problems = append(problems, fmt.Errorf("rounds[%d] cost cannot be negative", i))
		}
		// What served the round is absent from every round recorded before it was
		// carried, so what is checked is the shape of what is there rather than
		// that it is there: an exchange written by an older build must still load,
		// and a round naming an account or a configuration nothing could have
		// produced says less than one naming neither, because it reads as evidence.
		if round.Backend != "" && !round.Backend.Valid() {
			problems = append(problems, fmt.Errorf("rounds[%d] backend %q is not a backend identifier", i, round.Backend))
		}
		if round.AccountAlias != "" && !accountAliasPattern.MatchString(round.AccountAlias) {
			problems = append(problems, fmt.Errorf("rounds[%d] account alias %q is not an account alias", i, round.AccountAlias))
		}
		if round.ConfigRevision != "" && !configRevisionPattern.MatchString(round.ConfigRevision) {
			problems = append(problems, fmt.Errorf("rounds[%d] config revision %q is not a configuration revision", i, round.ConfigRevision))
		}
		if round.Build != "" && !buildPattern.MatchString(round.Build) {
			problems = append(problems, fmt.Errorf("rounds[%d] build %q is not a revision", i, round.Build))
		}
	}
	if e.Outcome != "" && !e.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("outcome %q is not one an exchange ends with", e.Outcome))
	}
	problems = append(problems, boundedText("settled", e.Settled, MaxSettledBytes, false))
	// An exchange the cap closed is exactly the one nothing settled, so a record
	// claiming both is one the conductor cannot have written.
	if e.Outcome == OutcomeUnresolved && strings.TrimSpace(e.Settled) != "" {
		problems = append(problems, errors.New("an exchange closed unresolved settled nothing"))
	}
	if (e.Outcome == "") != (e.ClosedAt == nil) {
		problems = append(problems, errors.New("an outcome and the moment it was reached are recorded together"))
	}
	if e.OpenedAt.IsZero() {
		problems = append(problems, errors.New("opened at is required"))
	}
	if e.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid exchange: %w", err)
	}
	return nil
}

// Ask is one question a role puts to another, as the fenced block carries it.
type Ask struct {
	// Role is who is being asked, and it is a role rather than an agent: which
	// configured agent fills it is the harness's to resolve, exactly as it is when
	// an operator addresses one.
	//
	// It is required to open an exchange and optional to continue or close one,
	// because a thread already says who is in it: the exchange was opened naming
	// the answering role and that is fixed for its whole life, so a follow-up that
	// restated it could only ever agree or contradict. One that states it anyway
	// is held to matching, which is the contradiction being refused rather than
	// silently redirecting the thread.
	Role domain.AgentRole `json:"role,omitempty"`
	// Question is what is being asked. It is required and it has to ask
	// something: a statement put through this channel is an opinion posted at
	// another role, and nobody answers one.
	Question string `json:"question"`
	// Context is the asker's own framing — what it already believes, and what it
	// is about to decide with the answer. It is optional and it is never
	// evidence: the role being asked reads it as what the asker thinks.
	Context string `json:"context,omitempty"`
	// Refers are the durable things the asker read before it asked, each at the
	// revision it read. They are optional and they are not evidence either: what
	// they buy is that a question asked against something that has since moved can
	// be found afterwards, instead of being answered as though nothing had
	// changed. They are recorded when the exchange opens; a follow-up in the same
	// thread is asked against what the thread already names.
	Refers []Reference `json:"refers,omitempty"`
	// Exchange continues a thread already open, named by its identifier. An ask
	// naming none opens a new exchange.
	Exchange string `json:"exchange,omitempty"`
	// Settled closes the named exchange instead of asking it anything further,
	// recording what the asker took from it. It is the ordinary way an exchange
	// ends, and it requires the exchange it closes to be named.
	Settled string `json:"settled,omitempty"`
}

// Closing reports an ask that ends its exchange rather than continuing it.
func (a Ask) Closing() bool { return strings.TrimSpace(a.Settled) != "" }

// Validate reports every contract violation in the ask at once.
func (a Ask) Validate() error {
	var problems []error
	continuing := strings.TrimSpace(a.Exchange) != ""
	switch {
	case a.Role != "":
		if !a.Role.Valid() {
			problems = append(problems, fmt.Errorf("role %q is not one of the harness's roles; ask %s",
				a.Role, strings.Join(roleNames(), ", ")))
		}
	case !continuing:
		problems = append(problems, fmt.Errorf("role is required to open an exchange; ask %s",
			strings.Join(roleNames(), ", ")))
	}
	if continuing && !ValidID(strings.TrimSpace(a.Exchange)) {
		problems = append(problems, fmt.Errorf("exchange %q is not an exchange identifier", strings.TrimSpace(a.Exchange)))
	}
	problems = append(problems, boundedText("context", a.Context, MaxContextBytes, false))
	problems = append(problems, boundedText("settled", a.Settled, MaxSettledBytes, false))
	problems = append(problems, validateReferences("refers", a.Refers))
	// What an exchange was asked against is fixed when it opens, for the reason
	// who is in it is: a thread whose references changed half way through would be
	// two questions wearing one identifier, and staleness derived over it could no
	// longer say which of them had gone stale.
	if continuing && len(a.Refers) > 0 {
		problems = append(problems, errors.New(`"refers" is recorded when an exchange opens; a follow-up is asked against what the thread already names`))
	}
	if a.Closing() {
		// Closing is about a thread, so there has to be a thread to close, and
		// there is nothing further to ask in the same breath.
		if strings.TrimSpace(a.Exchange) == "" {
			problems = append(problems, errors.New(`"settled" closes an exchange, so it names the exchange it closes`))
		}
		if strings.TrimSpace(a.Question) != "" {
			problems = append(problems, errors.New(`an ask either asks something or settles an exchange, not both`))
		}
	} else {
		problems = append(problems, boundedText("question", a.Question, MaxQuestionBytes, true))
		if question := strings.TrimSpace(a.Question); question != "" && !strings.HasSuffix(question, "?") {
			problems = append(problems, errors.New("question must ask something and end with a question mark"))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid ask: %w", err)
	}
	return nil
}

func roleNames() []string {
	names := make([]string, 0, len(domain.Roles()))
	for _, role := range domain.Roles() {
		names = append(names, string(role))
	}
	return names
}

// askDocument is the payload shape of the fenced block. One block carries one
// ask: an exchange is a thread between two roles, and a reply that opened three
// of them at once would be a role broadcasting rather than asking.
type askDocument struct {
	Ask Ask `json:"ask"`
}

// Extract splits a reply into the prose and the one ask it carries. An ask comes
// only from the fenced block: prose that wonders what the architect would say is
// not an ask and reaches nobody, which is the whole point of having a block for
// it.
func Extract(reply string) (string, *Ask, error) {
	block, err := fenced.Split(reply, Fence, "ask")
	if err != nil {
		return block.Before, nil, err
	}
	if !block.Found {
		return block.Before, nil, nil
	}
	ask, err := Decode(block.Payload)
	if err != nil {
		return block.Before, nil, err
	}
	return block.Rest, &ask, nil
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated: what one role puts to
// another has to be exactly what it said.
func Decode(payload string) (Ask, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return Ask{}, errors.New("decode ask: the ask block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return Ask{}, fmt.Errorf("decode ask: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var document askDocument
	if err := decoder.Decode(&document); err != nil {
		return Ask{}, fmt.Errorf("decode ask: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Ask{}, errors.New("decode ask: unexpected trailing content after the ask")
	}
	if err := document.Ask.Validate(); err != nil {
		return Ask{}, err
	}
	return document.Ask, nil
}

// harnessFencePrefix opens every block the harness reads out of an agent's
// reply. An answering role is refused all of them at once rather than block by
// block, so a channel added later is refused here without this having to learn
// about it.
const harnessFencePrefix = "```yoyodyne-"

// ReadAnswer takes what an answering role said and returns it, or refuses it.
// This is where judgment-only and decisionless stop being descriptions and
// become a rule: the answering half of an exchange may say what it thinks and
// nothing else, so a reply carrying any harness block at all is refused whole.
// Nothing in it is carried out, the round records why, and the asker is told
// that its question went unanswered rather than being handed half an answer.
func ReadAnswer(reply string) (string, error) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return "", errors.New("the answering role said nothing")
	}
	if at := indexHarnessFence(trimmed); at >= 0 {
		return "", fmt.Errorf("the answering role asked for %q, and an ask carries no authority: an answer is judgment and nothing else",
			strings.TrimSpace(trimmed[at:at+lineLength(trimmed[at:])]))
	}
	if len(trimmed) > MaxAnswerBytes {
		return "", fmt.Errorf("the answer is %d bytes, limit is %d", len(trimmed), MaxAnswerBytes)
	}
	return trimmed, nil
}

// indexHarnessFence finds a harness block that opens its own line, so a fence
// quoted inside prose is text rather than a request.
func indexHarnessFence(text string) int {
	if strings.HasPrefix(text, harnessFencePrefix) {
		return 0
	}
	if at := strings.Index(text, "\n"+harnessFencePrefix); at >= 0 {
		return at + 1
	}
	return -1
}

func lineLength(text string) int {
	if at := strings.IndexByte(text, '\n'); at >= 0 {
		return at
	}
	return len(text)
}

// boundedText checks one value the record keeps verbatim.
func boundedText(field, value string, limit int, required bool) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if len(trimmed) > limit {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), limit)
	}
	return nil
}
