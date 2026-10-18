// Package amendment carries a proposed change to a canonical artifact from a
// role that does not own it to the role that does and to the operator.
//
// Ownership is an authorization boundary: a developer that finds the design
// wrong cannot edit the design, and a role with no way to say so either has
// only two moves left, both bad — implement against intent it believes is
// wrong, or edit the document anyway. This is the third: it says what should
// change and why, to the people who may decide it.
//
// A proposal is emphatically not a deferred edit. It carries no replacement
// prose, nothing in it is ever written into the artifact, and approving one
// changes no document: what an approval records is that the owner's authority
// was exercised in favour of the change, after which the owner makes it and the
// artifact's own revision log says the owner did. That is what keeps this from
// becoming the slow path by which a downstream role redefines upstream intent —
// the proposal is an argument, and the only thing it can produce on its own is
// a decision.
//
// The shape is the one the work-item proposal path already established, because
// the questions are the same: what is proposed, why, who decides, and what the
// record says afterwards. What it adds is an owner. A work item is proposed to
// the operator alone; a change to an artifact is proposed to the role that owns
// the artifact as well, because that role is the one whose intent is being
// argued with.
package amendment

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

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// SchemaVersion is versioned independently of run and conversation state, for
// the same reason a report's is: a proposed amendment outlives the run that
// raised it, and it is written once and decided once rather than revised.
const SchemaVersion = 1

// Fence opens the one block a reply may propose amendments in. It is a distinct
// language tag rather than plain JSON so a proposal can never be confused with
// JSON an agent happens to be discussing, and a distinct one from the report
// fence because the two ask for different things: a report decides nothing, and
// this waits on a decision.
const Fence = "```yoyodyne-amendment"

// MaxProposalsPerReply bounds how many changes one reply may propose, and
// MaxBlockBytes bounds the untrusted payload the block is decoded from. Every
// proposal costs somebody a decision, so a reply that proposes a rewrite of the
// upstream documents is refused rather than turned into a queue of prompts
// nobody works through. maxProposalsPerReplyText is the same bound as the
// contract states it; a test keeps the number an agent is told equal to the one
// enforced here.
const (
	MaxProposalsPerReply     = 3
	maxProposalsPerReplyText = "3"
	MaxBlockBytes            = 16 << 10
)

// MaxTextBytes bounds one proposal's change and its reasoning. It is generous
// for the paragraph each of them is, and small enough that no proposal can
// carry a replacement document — which is the one thing a proposal must never
// be, since text that large is an edit waiting to be pasted rather than a case
// for one.
const MaxTextBytes = 4 << 10

// Entry is one proposed change exactly as an agent wrote it: the artifact, what
// should change about it, and why. Everything else — which role proposed it,
// which run it came from, who owns the artifact — is what the harness knows and
// the agent does not get to assert.
type Entry struct {
	// Artifact is the id of the document the change is to. It names an existing
	// artifact, because a change to a document nobody records has no owner to
	// decide it.
	Artifact string `json:"artifact"`
	// Change is what should become true of the artifact, and Why is what makes
	// the case for it. Both are required: a proposal with no reasoning asks its
	// owner to decide on an assertion, and a proposal that only complains asks
	// them to guess what would settle it.
	Change string `json:"change"`
	Why    string `json:"why"`
	// Intent is the proposer's claim about what the change does to what the
	// document intends, in the terms of the operator's test: "consistent" where
	// the goals would admit and refuse exactly the work they did before, and
	// "fundamental" where they would not. It is optional, and it matters only on
	// the brief and the goals: a change to one of those that does not say it is
	// consistent is decided by the operator rather than by its owner. See
	// ReservedForOperator.
	Intent artifact.Intent `json:"intent,omitempty"`
}

// Validate reports every contract violation in the entry at once.
func (e Entry) Validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("artifact", strings.TrimSpace(e.Artifact)); err != nil {
		problems = append(problems, err)
	}
	if e.Intent != "" && !e.Intent.Valid() {
		problems = append(problems, fmt.Errorf("intent %q must be %q or %q, or left out", e.Intent, artifact.IntentConsistent, artifact.IntentFundamental))
	}
	problems = append(problems, boundText("change", e.Change), boundText("why", e.Why))
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid proposed amendment: %w", err)
	}
	return nil
}

func boundText(field, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(trimmed) > MaxTextBytes {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), MaxTextBytes)
	}
	return nil
}

// Extract splits a reply into what the agent said and the changes it proposed.
// Proposals come only from the fenced block: prose describing a document that
// ought to change is not a proposal and reaches nobody who can decide it, which
// is the whole point of having a block for it.
//
// What the agent said comes back without the block whichever way that goes, for
// the same reason a report does: the reply is the role's actual output, and a
// channel that decides nothing about this run must not cost it.
func Extract(reply string) (string, []Entry, error) {
	block, err := fenced.Split(reply, Fence, "amendment")
	if err != nil {
		return block.Before, nil, err
	}
	if !block.Found {
		return block.Before, nil, nil
	}
	entries, err := Decode(block.Payload)
	if err != nil {
		return block.Before, nil, err
	}
	return block.Rest, entries, nil
}

// document is the payload shape of the fenced block. It always carries a list,
// so proposing one change and proposing two are the same protocol.
type document struct {
	Proposals []Entry `json:"proposals"`
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated: what an owner is asked
// to decide has to be exactly what the agent wrote.
func Decode(payload string) ([]Entry, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode proposed amendments: the amendment block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return nil, fmt.Errorf("decode proposed amendments: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var decoded document
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode proposed amendments: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode proposed amendments: unexpected trailing content after the proposals")
	}
	if len(decoded.Proposals) == 0 {
		return nil, errors.New("decode proposed amendments: an amendment block must propose at least one change")
	}
	if len(decoded.Proposals) > MaxProposalsPerReply {
		return nil, fmt.Errorf("decode proposed amendments: %d changes proposed in one reply, limit is %d",
			len(decoded.Proposals), MaxProposalsPerReply)
	}
	var problems []error
	for i, entry := range decoded.Proposals {
		if err := entry.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("proposals[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid proposed amendments: %w", errors.Join(problems...))
	}
	return decoded.Proposals, nil
}

// Proposal is one durable proposed change: the entry an agent wrote, the
// attribution the harness supplied, and the artifact's kind and owner as the
// repository recorded them when it was raised. The owner is resolved once, here,
// rather than at every reading, because who was asked to decide is part of what
// happened: a document whose kind changes later does not retroactively send this
// proposal to somebody else.
type Proposal struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// Role is the contract the proposer worked under and Agent is the configured
	// agent that filled it, recorded apart for the same reason a report records
	// both: "which developer said this" is a different question from "a
	// developer said this".
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	// RunID is the invocation the proposal came out of, and WorkItemID the item
	// its proposer was working on. They are what a proposal leads back to once
	// the run that raised it is long over, which is the whole reason it is
	// written down rather than left in a summary.
	RunID        string           `json:"run_id"`
	WorkItemID   string           `json:"work_item_id,omitempty"`
	ProductID    domain.ProductID `json:"product_id"`
	RepositoryID string           `json:"repository_id"`
	// Artifact is the document the change is to, Kind is what sort of document
	// that is, and Owner is the role that may make the change. The kind is
	// recorded beside the owner because the kind is what decides the owner, and
	// a record that named only the role would not say why that role was asked.
	Artifact string           `json:"artifact"`
	Kind     artifact.Kind    `json:"kind"`
	Owner    domain.AgentRole `json:"owner"`
	Change   string           `json:"change"`
	Why      string           `json:"why"`
	// Intent is what the proposer said the change does to the document's intent,
	// exactly as Entry.Intent, and empty where it said nothing.
	Intent   artifact.Intent `json:"intent,omitempty"`
	RaisedAt time.Time       `json:"raised_at"`
}

// ReservedForOperator reports whether this proposal is the operator's to decide
// rather than its owner's.
//
// The owning role decides an amendment to its own document: the architect a
// design, a specification, or a decision record, and the Lead Product Manager a
// document in the product home. What stays with the operator is a change of
// fundamental intent, which the brief reserves to him: a change to the brief
// or the goals, unless the proposal says it is
// consistent with what they intend. A proposal that says nothing stays his,
// because the default is the operator's and it takes a recorded claim to move
// off it — the rule internal/artifact/rewording.go holds for revisions.
func (p Proposal) ReservedForOperator() bool {
	switch p.Kind {
	case artifact.KindBrief, artifact.KindGoals:
		return p.Intent != artifact.IntentConsistent
	default:
		return false
	}
}

// reservedReason says why a reserved proposal is the operator's, in the words a
// refusal and a listing both use.
func (p Proposal) reservedReason() string {
	if p.Intent == artifact.IntentFundamental {
		return fmt.Sprintf("it is a change to a %s document that says it changes what the goals intend", p.Kind)
	}
	return fmt.Sprintf("it is a change to a %s document that does not say whether it changes what the goals intend", p.Kind)
}

var idPattern = regexp.MustCompile(`^amendment-[a-f0-9]{32}$`)

// ValidID reports whether an id has the shape NewID mints, so a package that
// reads proposal ids from elsewhere checks them against this one shape rather
// than a copy of it.
func ValidID(id string) bool { return idPattern.MatchString(id) }

func NewID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate amendment id: %w", err)
	}
	return "amendment-" + hex.EncodeToString(raw), nil
}

// Validate reports every contract violation in the recorded proposal at once.
func (p Proposal) Validate() error {
	var problems []error
	if p.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !idPattern.MatchString(p.ID) {
		problems = append(problems, errors.New("id is invalid"))
	}
	if err := domain.ValidateIdentifier("role", string(p.Role)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(p.RunID) == "" {
		problems = append(problems, errors.New("run id is required"))
	}
	if err := domain.ValidateIdentifier("product id", string(p.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("repository id", p.RepositoryID); err != nil {
		problems = append(problems, err)
	}
	if !p.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not an artifact kind", p.Kind))
	}
	// The owner the record names has to be the one the kind actually has, so a
	// record cannot address a decision to a role that was never entitled to make
	// it.
	switch owner, known := artifact.Owner(p.Kind); {
	case !known:
		// Already reported by the kind above; naming it twice would say the same
		// thing in two voices.
	case p.Owner != owner:
		problems = append(problems, fmt.Errorf("owner %q is not the owner of a %s artifact, which is the %s's", p.Owner, p.Kind, owner))
	case p.Role == owner:
		// The boundary this exists to serve, stated as a property of the record:
		// a role that owns the document amends it, and a proposal from the owner
		// would be an owner asking somebody else's permission for their own work.
		problems = append(problems, fmt.Errorf("a %s artifact is the %s's own, so it amends one rather than proposing a change to it", p.Kind, owner))
	}
	if p.RaisedAt.IsZero() {
		problems = append(problems, errors.New("raised_at is required"))
	}
	problems = append(problems, Entry{Artifact: p.Artifact, Change: p.Change, Why: p.Why, Intent: p.Intent}.Validate())
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid proposed amendment %q: %w", p.ID, err)
	}
	return nil
}

// Attribution is what the harness knows about a proposer, which is everything
// about a proposal except the artifact it names and the case it makes.
type Attribution struct {
	Role         domain.AgentRole
	Agent        string
	RunID        string
	WorkItemID   string
	ProductID    domain.ProductID
	RepositoryID string
}

// Artifacts is the recorded set a proposal's artifact is resolved against. It
// is satisfied by artifact.Set.
type Artifacts interface {
	Find(id string) (artifact.Artifact, bool)
}

// Collect turns proposed changes into durable records, resolving each named
// artifact to the kind it is and the role that owns it.
//
// It is deliberately per-entry: an entry that cannot be turned into a proposal
// is reported and the rest are still collected, because two proposals in one
// reply are two arguments and losing a good one over a bad one beside it would
// cost exactly what this channel exists to prevent. An entry is refused when
// nothing answers to the artifact it names — there is then no owner to decide
// it — and when the proposer owns the artifact itself, which is a role asking
// permission to do its own job.
//
// The error goes to the caller, which names it on the run's outcome for the
// operator and is expected to carry it back to the role that wrote the entry, so
// a role that misnames a document is told rather than left believing somebody was
// asked. It is worded for both readers: what a refusal says is what its author
// has to answer to propose the change again. Nothing here decides who is told —
// the caller knows which role it collected from, and this does not.
func Collect(entries []Entry, attribution Attribution, artifacts Artifacts, now time.Time) ([]Proposal, error) {
	collected := make([]Proposal, 0, len(entries))
	var problems []error
	for _, entry := range entries {
		named := strings.TrimSpace(entry.Artifact)
		if artifacts == nil {
			problems = append(problems, fmt.Errorf("%s: nothing could be resolved against, so the change was not recorded", named))
			continue
		}
		found, exists := artifacts.Find(named)
		if !exists {
			problems = append(problems, fmt.Errorf("%s: no artifact answers to that id, so there is no owner to decide a change to it", named))
			continue
		}
		owner, known := artifact.Owner(found.Kind)
		if !known {
			problems = append(problems, fmt.Errorf("%s: a %s artifact has no owner, so there is nobody to decide a change to it", named, found.Kind))
			continue
		}
		if attribution.Role == owner {
			problems = append(problems, fmt.Errorf("%s: a %s artifact is the %s's own, so it amends one rather than proposing a change to it", named, found.Kind, owner))
			continue
		}
		id, err := NewID()
		if err != nil {
			problems = append(problems, err)
			continue
		}
		proposal := Proposal{
			SchemaVersion: SchemaVersion,
			ID:            id,
			Role:          attribution.Role,
			Agent:         strings.TrimSpace(attribution.Agent),
			RunID:         attribution.RunID,
			WorkItemID:    attribution.WorkItemID,
			ProductID:     attribution.ProductID,
			RepositoryID:  attribution.RepositoryID,
			Artifact:      found.ID,
			Kind:          found.Kind,
			Owner:         owner,
			Change:        strings.TrimSpace(entry.Change),
			Why:           strings.TrimSpace(entry.Why),
			Intent:        entry.Intent,
			RaisedAt:      now.UTC(),
		}
		if err := proposal.Validate(); err != nil {
			problems = append(problems, err)
			continue
		}
		collected = append(collected, proposal)
	}
	return collected, errors.Join(problems...)
}

// Render describes one proposed change for whoever is deciding it. Everything
// but the harness's own line came from a provider, so provider text is indented
// under the proposal's identifier and never printed at the margin.
func (p Proposal) Render() string {
	var rendered strings.Builder
	proposer := string(p.Role)
	if p.Agent != "" && p.Agent != string(p.Role) {
		proposer = p.Agent + " (" + string(p.Role) + ")"
	}
	fmt.Fprintf(&rendered, "  [%s] %s, proposed by the %s", p.ID, p.Artifact, proposer)
	if p.WorkItemID != "" {
		fmt.Fprintf(&rendered, " on %s", p.WorkItemID)
	}
	fmt.Fprintf(&rendered, " (%s)\n", p.RunID)
	// Who decides is said on every rendering, because the same line is shown to
	// the owning role, to the operator, and on the channel, and each of them has
	// to know from it whether the decision is theirs.
	if p.ReservedForOperator() {
		fmt.Fprintf(&rendered, "      decided by the operator, under the %s's authority, because %s; raised %s\n",
			p.Owner, p.reservedReason(), p.RaisedAt.UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintf(&rendered, "      decided by the %s, whose document it is; raised %s\n", p.Owner, p.RaisedAt.UTC().Format(time.RFC3339))
	}
	for _, line := range indent("change: " + p.Change) {
		rendered.WriteString(line)
	}
	for _, line := range indent("why: " + p.Why) {
		rendered.WriteString(line)
	}
	return rendered.String()
}

func indent(text string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	indented := make([]string, 0, len(lines))
	for _, line := range lines {
		indented = append(indented, "      "+strings.TrimSpace(line)+"\n")
	}
	return indented
}

// Contract is the section every role's immutable contract carries about
// proposing a change to a document it does not own. It is one text in one place
// because every role proposes the same way: the boundary is the same boundary,
// and three separately worded instructions would become three mechanisms that
// drift.
const Contract = `# Proposing a change to an artifact you do not own

The canonical documents — the brief, the goals and non-goals, the designs, the specifications, and the decision records — each belong to one role. If you are not that role you may not edit one, and you may not work around one you believe is wrong. What you can do is propose the change, which reaches the role that owns the document, and that role decides it. A change to the brief or the goals that would change what they admit or refuse is the operator's to decide instead, and so is one that does not say whether it would.

A proposal is not an edit you have written in advance. Nothing in it is ever put into the document, an approved one is still made by the owner rather than applied for you, and your own work carries on unchanged either way — a proposal never blocks you and never waits. So propose the change you would argue for in a sentence, not the paragraph you would like the document to say: what should become true of it, and why.

To propose, end what you say with at most one block of this shape:

` + "```" + `yoyodyne-amendment
{"proposals":[{"artifact":"artifact-id","change":"what should become true of the document","why":"what makes the case for it"}]}
` + "```" + `

"artifact" is the id of an existing document — the file name of the artifact, without its extension. A proposal naming a document nobody records reaches nobody, because there is no owner to decide it. One block carries at most ` + maxProposalsPerReplyText + ` proposals, and each one takes an artifact, a change, and a reason, and optionally an "intent", and nothing else. "intent" is "consistent" where the goals would admit and refuse exactly the work they did before the change, and "fundamental" where they would not; it matters only on a change to the brief or the goals, where leaving it out sends the change to the operator. If you are also reporting something, put this block before the report block, which is always last. Leave the block out entirely when you have nothing to propose, which is most of the time.

Writing the block is not the same as the proposal being recorded. The harness refuses one that names a document it has no record of, and one against a document you own yourself, and it can only tell you so after the reply that carried it: if it invokes you again, that turn opens with the refusal in its own words and you propose again from there — and if it does not, the refusal is on the run for the operator and you never hear it. So nothing you write may assert that a proposal has been raised: at the moment you write it you do not know, and a document, a comment, or a note claiming a proposal nobody is holding is a false record that outlives this work. Say what you propose in your reply, where it is read beside the block that carried it.`
