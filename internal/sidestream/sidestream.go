// Package sidestream is one durable side conversation: a bounded thread an agent
// holds beside its main conversation, with an identity, a lease, and a transcript
// of its own.
//
// It exists because a main thread serializes. One conversation takes its turns
// one at a time behind a single-holder lease, which is what stops two processes
// interleaving a transcript — and it means a held main thread queues every
// inbound question behind whatever it is doing. A side stream is the same
// machinery given a second identity, so a question answered beside a long turn
// waits for nothing.
//
// Three properties are what make that safe, and every one of them is enforced
// here rather than asked for in a persona:
//
// It never takes the main thread's lease. The single-holder rule governs the
// main conversation, and a side stream holds a lease of its own, on its own file,
// under its own identifier. That is the whole of the concurrency answer: the main
// thread's serialization is untouched because nothing here is a second holder of
// it.
//
// Its transcript cannot interleave with the main thread's. The two logs are named
// for two identifiers with two shapes, and each store refuses an event belonging
// to the other, so an event written into the wrong log is a failure rather than a
// transcript with two conversations in it.
//
// It takes no action. A side thread judges, answers, and tentatively plans, with
// full read access to the tracker and to the evidence its role is given. Every
// intent it forms is a draft: no work-item mutation, no admission, no proposal
// raised, no directive. Permits below is that statement in Go, and it narrows a
// role's authority and can never widen it, which is what
// `configuration-never-grants-authority` requires of the per-agent knob that
// chooses this behavior at all.
//
// "Side" names where the thread runs and never what anybody can see of it. A
// stream is a record under the product's state like every other, read back by
// whoever reads the product, and the merge that carries its substance back into
// the agent's context is audited memory rather than a transcript nobody kept.
package sidestream

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// SchemaVersion is 1 and has never changed. A stream is written as it goes: once
// when it opens, again as each turn lands, and once more when it concludes.
const SchemaVersion = 1

// The bounds on one side stream.
const (
	// MaxTopicBytes bounds what a stream says it is about. A topic is a line an
	// operator reads in a listing, not the question itself.
	MaxTopicBytes = 1 << 10
	// DefaultMaxTurns is the cap a stream is opened with where a project
	// configures none. A side thread is bounded by design — it concludes by
	// finishing or by exhausting its budget — and eight turns is more than an
	// answer needs and few enough that the pathological case costs little.
	DefaultMaxTurns = 8
	// DefaultMaxPerAgent is how many side streams one agent may have open at once
	// where a project configures no number. The design settles the count as
	// configuration; this is what a caller that has none passes, so the bound is
	// never absent.
	DefaultMaxPerAgent = 3
)

// ErrNoStream reports an identifier that names nothing recorded, which is a plain
// answer rather than a failure to look.
var ErrNoStream = errors.New("no such side stream")

// ErrTooManyStreams reports an agent already holding as many side streams as it
// is allowed. It is its own error because it is the ordinary answer at a busy
// moment rather than a fault: what a caller does with it is wait or queue the
// question, which it cannot decide from a message.
var ErrTooManyStreams = errors.New("already holding as many side streams as it is allowed")

// Outcome is how a side stream ended. One still being held carries none, which is
// the ordinary state of an open thread.
type Outcome string

const (
	// OutcomeConcluded is the ordinary ending: the side thread finished what it
	// was opened for.
	OutcomeConcluded Outcome = "concluded"
	// OutcomeSpent is the budget being reached. It is not a silent cutoff: the
	// stream closes as this, and what it had reached still merges, so a thread cut
	// off half way is legible as one rather than as a thread that said nothing.
	OutcomeSpent Outcome = "spent-its-budget"
)

func (o Outcome) Valid() bool {
	switch o {
	case OutcomeConcluded, OutcomeSpent:
		return true
	default:
		return false
	}
}

// Stream is one durable side conversation.
type Stream struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	ProductID     domain.ProductID `json:"product_id"`
	RepositoryID  string           `json:"repository_id,omitempty"`
	// Agent and Role are whose side thread this is: the configured agent that
	// holds it and the role whose authority it carries, narrowed. Both are
	// recorded for the reason a conversation records both — a project may
	// configure two architects, and "which architect" is a different question from
	// "the architect".
	Agent string           `json:"agent"`
	Role  domain.AgentRole `json:"role"`
	// Conversation is the main thread this stream belongs to. It is required: a
	// side conversation is beside something, the merge writes into that agent's
	// context for that thread's next turn to read, and a stream naming no main
	// thread is one nothing would ever ratify.
	Conversation string `json:"conversation"`
	// Topic is what this stream is about, recorded when it opens so a listing can
	// say what an agent is holding beside its main thread without reading the
	// transcript.
	Topic string `json:"topic"`
	// What served this stream's invocations. They are what
	// `durable-state-is-provider-independent` asks of every provider invocation,
	// and a side thread is one: without them the record would name a provider
	// session and nothing that outlives it. They are empty until the first turn is
	// taken.
	Backend               domain.Backend `json:"backend,omitempty"`
	ProviderSessionID     string         `json:"provider_session_id,omitempty"`
	ProviderModel         string         `json:"provider_model,omitempty"`
	ProviderResolvedModel string         `json:"provider_resolved_model,omitempty"`
	ProviderEffort        string         `json:"provider_effort,omitempty"`
	// ProviderResolvedEffort is provider-reported; ProviderEffortReported is false when not reported.
	ProviderResolvedEffort    string `json:"provider_resolved_effort,omitempty"`
	ProviderEffortDescription string `json:"provider_effort_description,omitempty"`
	ProviderEffortReported    bool   `json:"provider_effort_reported"`
	AccountAlias              string `json:"account_alias,omitempty"`
	ConfigRevision            string `json:"config_revision,omitempty"`
	Build                     string `json:"build,omitempty"`
	// MaxTurns is the cap this stream is held to. It is copied on when the stream
	// opens rather than read from the configuration each time, so a configuration
	// edit or a second process cannot lengthen a thread already in flight.
	MaxTurns int `json:"max_turns"`
	Turns    int `json:"turns"`
	// CostUSD is what this stream has cost, as the provider reported it. A side
	// thread is priced beside the conversations because what a thread nobody was
	// watching spent is exactly what an operator cannot otherwise see.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// LastSequence is where this stream's own transcript has reached. It is the
	// stream's rather than the main thread's, which is the same separation the two
	// logs are: a sequence shared between them would be two writers on one counter.
	LastSequence uint64 `json:"last_sequence"`
	// Outcome is empty while the stream is open.
	Outcome   Outcome    `json:"outcome,omitempty"`
	OpenedAt  time.Time  `json:"opened_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

var idPattern = regexp.MustCompile(`^side-[a-f0-9]{32}$`)

// conversationPattern is the shape of the main thread an identifier names. It is
// stated here rather than imported from the durable store for the reason the
// exchange record states its own patterns: the schema stays independent of the
// code that writes it, so a record is checked against what a record may hold.
var conversationPattern = regexp.MustCompile(`^chat-[a-f0-9]{32}$`)

func NewID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate side stream id: %w", err)
	}
	return "side-" + hex.EncodeToString(raw), nil
}

// ValidID reports an identifier of the shape this package issues. A store names a
// file and a lease after it, so it is checked before anything built from outside
// is used as a path.
//
// It is deliberately a different shape from a conversation's. The two never name
// each other's records, and a store handed the wrong one refuses it rather than
// reading a transcript that belongs to something else.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Open reports whether this stream is still being held.
func (s Stream) Open() bool { return s.Outcome == "" }

// TurnsRemaining is how many further turns this stream may take. It is never
// negative: a stream at its cap has nothing remaining rather than a debt.
func (s Stream) TurnsRemaining() int {
	if remaining := s.MaxTurns - s.Turns; remaining > 0 {
		return remaining
	}
	return 0
}

// Validate reports every contract violation in the stream at once.
func (s Stream) Validate() error {
	var problems []error
	if s.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("side stream schema version %d is not supported", s.SchemaVersion))
	}
	if !ValidID(s.ID) {
		problems = append(problems, fmt.Errorf("side stream id %q is invalid", s.ID))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("agent", s.Agent); err != nil {
		problems = append(problems, err)
	}
	if !s.Role.Valid() {
		problems = append(problems, fmt.Errorf("side stream role %q is not one of the harness's roles", s.Role))
	}
	if !conversationPattern.MatchString(s.Conversation) {
		problems = append(problems, fmt.Errorf("side stream conversation %q does not name a main thread", s.Conversation))
	}
	problems = append(problems, boundedText("topic", s.Topic, MaxTopicBytes, true))
	if s.MaxTurns < 1 {
		problems = append(problems, fmt.Errorf("max turns is %d; a side stream is allowed at least one turn", s.MaxTurns))
	}
	if s.Turns < 0 {
		problems = append(problems, fmt.Errorf("turns is %d and cannot be negative", s.Turns))
	}
	if s.Turns > s.MaxTurns {
		problems = append(problems, fmt.Errorf("%d turns are recorded against a cap of %d", s.Turns, s.MaxTurns))
	}
	if s.CostUSD < 0 {
		problems = append(problems, errors.New("cost cannot be negative"))
	}
	if s.Backend != "" && !s.Backend.Valid() {
		problems = append(problems, fmt.Errorf("backend %q is not a backend identifier", s.Backend))
	}
	if s.Outcome != "" && !s.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("outcome %q is not one a side stream ends with", s.Outcome))
	}
	if (s.Outcome == "") != (s.ClosedAt == nil) {
		problems = append(problems, errors.New("an outcome and the moment it was reached are recorded together"))
	}
	if s.OpenedAt.IsZero() {
		problems = append(problems, errors.New("opened at is required"))
	}
	if s.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid side stream: %w", err)
	}
	return nil
}

// Release gives a lease back. It is what runstate.Lease already is, named here so
// the durable package can satisfy this one rather than this one depending on it.
type Release interface{ Release() error }

// Leases is who may carry one side stream right now. It is satisfied by
// runstate.SideStreamStore, and it is its own interface rather than more methods
// on a record store for the reason the exchange channel's is: holding a stream and
// recording one are different questions.
type Leases interface {
	// Hold takes the exclusive lease on one side stream without waiting,
	// reporting whether it got it. A lease it could not take belongs to a live
	// process.
	Hold(id string) (Release, bool, error)
}

// permitted is the whole of what a side thread may ask for, in the vocabulary
// every other authority here is written in. It is a list in Go and nothing reads
// it from configuration: the per-agent knob chooses whether an agent holds side
// threads at all, and no value of it reaches this list.
//
// Reading the tracker and reading the evidence the role was given are on it
// because judging is what a side thread is for. Everything else is off it, and
// the ones worth naming are the near misses. Raising a proposal or a concern
// reaches the operator with something to decide. Commissioning research reaches
// outside this machine and spends. Recording an evaluation writes what the
// product's own record says it was advised. Putting an ask to another role
// commits a second role's turn. Each is an act rather than a judgment, and each
// belongs to the main thread, which is the only path an intent this thread forms
// can be ratified through.
var permitted = []capability.Capability{
	capability.WorkItemRead,
	capability.RepositoryRead,
}

// Permitted is what a side thread may ask for, in declaration order.
func Permitted() []capability.Capability { return slices.Clone(permitted) }

// Permits reports whether a side thread may ask for one capability. It answers
// about the thread and never about the role: a role that holds a capability does
// not hold it here, which is what narrowing means.
func Permits(required capability.Capability) bool {
	return slices.Contains(permitted, required)
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
