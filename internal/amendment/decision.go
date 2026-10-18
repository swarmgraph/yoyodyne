package amendment

// What becomes of a proposal, and who is entitled to say so.
//
// A decision is the only thing this mechanism produces. It writes no document:
// approving records that the owner's authority came down in favour of the
// change, and the change itself is then made by the owner, in the artifact,
// under a revision that says the owner made it. Keeping those two apart is what
// stops an approval from being a way to paste text into somebody else's
// document, and it is why an approval here is safe to record even when nobody
// gets round to the edit for a week: nothing has been asserted about the
// document that is not true of it.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Verdict is what was decided. There are two, and neither of them is "later": a
// proposal nobody has decided is pending, which is the absence of a verdict
// rather than one of them.
type Verdict string

const (
	VerdictApproved Verdict = "approved"
	VerdictDeclined Verdict = "declined"
)

func (v Verdict) Valid() bool {
	return v == VerdictApproved || v == VerdictDeclined
}

// Decider is who actually made the decision, as against whose authority it was
// made under. The two are recorded apart because they come apart in practice:
// the operator may direct any role and decides in the stead of an owner that
// does not run yet, and a record that only named the authority would make those
// look like the same event.
type Decider string

const (
	// DeciderOperator is the person, deciding a proposal reserved to him (see
	// Proposal.ReservedForOperator) or overriding the owning role on any other,
	// through `yoyo amendment`.
	DeciderOperator Decider = "operator"
	// DeciderOwner is the owning role itself, deciding about its own document from
	// its conversation: the harness carries the decision out from the role's
	// reply, as it carries out the role's tracker actions, and DecideAsOwner is
	// the only way to make one. It is recorded apart from the operator because
	// the Authority field alone could not tell an operator standing in for the
	// role apart from the role having answered.
	DeciderOwner Decider = "owner"
)

func (d Decider) Valid() bool {
	return d == DeciderOperator || d == DeciderOwner
}

// MaxReasonBytes bounds what a decision says about itself. It is the size of a
// paragraph, because a decision explains itself rather than restating the
// proposal.
const MaxReasonBytes = 4 << 10

// Decision is one settled proposal. It names the proposal rather than repeating
// it, so what was decided can never drift from what was proposed.
type Decision struct {
	SchemaVersion int     `json:"schema_version"`
	ProposalID    string  `json:"proposal_id"`
	Verdict       Verdict `json:"verdict"`
	// Authority is the role the decision was taken under, which is the artifact's
	// owner. Decider is who exercised it.
	Authority domain.AgentRole `json:"authority"`
	Decider   Decider          `json:"decider"`
	// Reason is why. It is required on a decline, because a proposal turned down
	// with no reason is one the same argument arrives to make again, and optional
	// on an approval, where what follows is the owner's own revision saying why
	// the document changed.
	Reason string `json:"reason,omitempty"`
	// Conversation is the conversation an owning role decided from, so the
	// decision leads back to the turn that argued it. It is set on an owner's
	// decision and on nothing else.
	Conversation string    `json:"conversation,omitempty"`
	DecidedAt    time.Time `json:"decided_at"`
}

// Validate reports every contract violation in the decision at once.
func (d Decision) Validate() error {
	var problems []error
	if d.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !idPattern.MatchString(d.ProposalID) {
		problems = append(problems, errors.New("proposal id is invalid"))
	}
	if !d.Verdict.Valid() {
		problems = append(problems, fmt.Errorf("verdict %q must be %q or %q", d.Verdict, VerdictApproved, VerdictDeclined))
	}
	if err := domain.ValidateIdentifier("authority", string(d.Authority)); err != nil {
		problems = append(problems, err)
	}
	if !d.Decider.Valid() {
		problems = append(problems, fmt.Errorf("decider %q must be %q or %q", d.Decider, DeciderOperator, DeciderOwner))
	}
	switch reason := strings.TrimSpace(d.Reason); {
	case reason == "" && d.Verdict == VerdictDeclined:
		problems = append(problems, errors.New("a declined proposal records why it was turned down"))
	case len(reason) > MaxReasonBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), MaxReasonBytes))
	}
	switch conversation := strings.TrimSpace(d.Conversation); {
	case d.Decider == DeciderOwner && conversation == "":
		problems = append(problems, errors.New("an owning role's decision names the conversation it was made in"))
	case d.Decider != DeciderOwner && conversation != "":
		problems = append(problems, errors.New("only an owning role's decision names a conversation"))
	case len(conversation) > MaxReasonBytes:
		problems = append(problems, fmt.Errorf("conversation is %d bytes, limit is %d", len(conversation), MaxReasonBytes))
	}
	if d.DecidedAt.IsZero() {
		problems = append(problems, errors.New("decided_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid decision on %q: %w", d.ProposalID, err)
	}
	return nil
}

// MayDecideAsOwner reports why a role may not decide this proposal from its own
// conversation, or nil where it may. Two things refuse it: the document is not
// the role's, which is the boundary this whole mechanism holds, and the
// proposal is reserved to the operator, which is the one decision the owner of
// the brief and the goals does not hold.
func (p Proposal) MayDecideAsOwner(role domain.AgentRole) error {
	if role != p.Owner {
		return fmt.Errorf("%s proposes a change to %s, a %s document, which is the %s's to decide and not the %s's",
			p.ID, p.Artifact, p.Kind, p.Owner.Title(), role.Title())
	}
	if p.ReservedForOperator() {
		return fmt.Errorf("%s is the operator's to decide, because %s; say what you recommend and he decides it",
			p.ID, p.reservedReason())
	}
	return nil
}

// DecideAsOwner settles a proposal from the owning role's own conversation. It
// writes the same decision `yoyo amendment approve` and `decline` write, under
// the same authority, and records that the owner made it and where.
func (p Proposal) DecideAsOwner(role domain.AgentRole, verdict Verdict, reason, conversation string, now time.Time) (Decision, error) {
	if err := p.MayDecideAsOwner(role); err != nil {
		return Decision{}, err
	}
	decision := Decision{
		SchemaVersion: SchemaVersion,
		ProposalID:    p.ID,
		Verdict:       verdict,
		Authority:     p.Owner,
		Decider:       DeciderOwner,
		Reason:        strings.TrimSpace(reason),
		Conversation:  strings.TrimSpace(conversation),
		DecidedAt:     now.UTC(),
	}
	if err := decision.Validate(); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

// Decide settles a proposal, under the authority of the role that owns the
// artifact. Nothing else may: a decision taken under any other role's authority
// would be the boundary crossed by the very mechanism that exists to hold it.
func (p Proposal) Decide(verdict Verdict, decider Decider, reason string, now time.Time) (Decision, error) {
	decision := Decision{
		SchemaVersion: SchemaVersion,
		ProposalID:    p.ID,
		Verdict:       verdict,
		Authority:     p.Owner,
		Decider:       decider,
		Reason:        strings.TrimSpace(reason),
		DecidedAt:     now.UTC(),
	}
	if err := decision.Validate(); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

// Render describes one decision for whoever is reading what became of a
// proposal.
func (d Decision) Render() string {
	var rendered strings.Builder
	who := "the operator"
	if d.Decider == DeciderOwner {
		who = "the " + string(d.Authority)
	}
	fmt.Fprintf(&rendered, "  %s by %s under the %s's authority, %s\n",
		d.Verdict, who, d.Authority, d.DecidedAt.UTC().Format(time.RFC3339))
	if conversation := strings.TrimSpace(d.Conversation); conversation != "" {
		fmt.Fprintf(&rendered, "      decided in conversation %s\n", conversation)
	}
	if reason := strings.TrimSpace(d.Reason); reason != "" {
		for _, line := range indent(reason) {
			rendered.WriteString(line)
		}
	}
	if d.Verdict == VerdictApproved {
		// Said every time an approval is read, because the one way this mechanism
		// could quietly become an edit is somebody believing the document already
		// says what was approved.
		fmt.Fprintf(&rendered, "      nothing was written to the artifact; the %s makes the change\n", d.Authority)
	}
	return rendered.String()
}

// Record is one line of the durable log: a proposal raised, or a decision on
// one. They share a log rather than living in two because the order between
// them is the thing that matters — a decision means nothing without the
// proposal it settles, and a proposal is pending exactly until a decision
// follows it.
type Record struct {
	Proposal *Proposal `json:"proposal,omitempty"`
	Decision *Decision `json:"decision,omitempty"`
}

// Validate refuses a record that is neither or both. A line that carries
// nothing says nothing happened, and one that carries both would be a proposal
// decided in the same breath it was raised, which no path here can produce.
func (r Record) Validate() error {
	switch {
	case r.Proposal == nil && r.Decision == nil:
		return errors.New("a record carries either a proposal or a decision, and this carries neither")
	case r.Proposal != nil && r.Decision != nil:
		return errors.New("a record carries either a proposal or a decision, and this carries both")
	case r.Proposal != nil:
		return r.Proposal.Validate()
	default:
		return r.Decision.Validate()
	}
}

// Proposals returns every proposal in the log, in the order it was raised.
func Proposals(records []Record) []Proposal {
	proposals := make([]Proposal, 0, len(records))
	for _, record := range records {
		if record.Proposal != nil {
			proposals = append(proposals, *record.Proposal)
		}
	}
	return proposals
}

// Find returns one proposal by id.
func Find(records []Record, id string) (Proposal, bool) {
	for _, record := range records {
		if record.Proposal != nil && record.Proposal.ID == id {
			return *record.Proposal, true
		}
	}
	return Proposal{}, false
}

// minNamedPrefix is the fewest hexadecimal characters a proposal may be named
// by, which is the short form reports and notes already cite proposals in.
const minNamedPrefix = 8

// Named returns the one proposal a role named, by its whole id or by a prefix
// of the hexadecimal part at least eight characters long, with or without the
// "amendment-" in front. A prefix that names more than one proposal is refused
// rather than resolved to the first, because a decision on the wrong document is
// worse than a refusal that asks for the whole id.
func Named(records []Record, named string) (Proposal, error) {
	trimmed := strings.TrimSpace(named)
	if found, ok := Find(records, trimmed); ok {
		return found, nil
	}
	hex := strings.TrimPrefix(trimmed, "amendment-")
	if len(hex) < minNamedPrefix || strings.Trim(hex, "0123456789abcdef") != "" {
		return Proposal{}, fmt.Errorf("%q names no proposed change; name one by its whole id, such as amendment-0123456789abcdef0123456789abcdef, or by at least %d characters of it", trimmed, minNamedPrefix)
	}
	var matched []Proposal
	for _, proposal := range Proposals(records) {
		if strings.HasPrefix(strings.TrimPrefix(proposal.ID, "amendment-"), hex) {
			matched = append(matched, proposal)
		}
	}
	switch len(matched) {
	case 0:
		return Proposal{}, fmt.Errorf("no proposed change %q was raised for this product", trimmed)
	case 1:
		return matched[0], nil
	default:
		return Proposal{}, fmt.Errorf("%q names %d proposed changes; name the one you mean by its whole id", trimmed, len(matched))
	}
}

// DecisionOn returns what was decided about a proposal, and whether anything
// was. The first decision is the one that stands: a log is append-only and two
// decisions on one proposal can only be a race or a mistake, and treating the
// later one as an override would let a second answer quietly replace the
// decision somebody already acted on.
func DecisionOn(records []Record, id string) (Decision, bool) {
	for _, record := range records {
		if record.Decision != nil && record.Decision.ProposalID == id {
			return *record.Decision, true
		}
	}
	return Decision{}, false
}

// Pending returns the proposals nobody has decided, oldest first, which is the
// order they were raised in and so the order they have been waiting in.
func Pending(records []Record) []Proposal {
	var pending []Proposal
	for _, proposal := range Proposals(records) {
		if _, decided := DecisionOn(records, proposal.ID); decided {
			continue
		}
		pending = append(pending, proposal)
	}
	return pending
}

// PendingFor returns the undecided proposals against one role's documents,
// which is what that role is shown when it runs: the ones it decides, and the
// ones reserved to the operator that it argues for him.
func PendingFor(records []Record, owner domain.AgentRole) []Proposal {
	var pending []Proposal
	for _, proposal := range Pending(records) {
		if proposal.Owner == owner {
			pending = append(pending, proposal)
		}
	}
	return pending
}
