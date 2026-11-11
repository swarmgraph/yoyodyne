// Package directive is what the operator told an agent to do, recorded where
// every agent has to meet it.
//
// The design has said from the beginning that a directive applies regardless of
// which agent received it, that it stays discoverable until it is superseded or
// retired, and that one which changes a governed artifact or which nobody can
// act on unambiguously pauses the work it affects until the operator settles it.
// What existed was only the first half of the first sentence: direction could be
// written down beside one work item, and a directive that changed what an
// in-flight item was supposed to be reached nothing. The work carried on against
// the intent it was given.
//
// A directive that is durably recorded and enforces nothing is the failure this
// package exists to end, so a record here carries the two things enforcement
// needs and nothing decorative: what work it affects, and what is unresolved
// about it. The run pipeline reads both — before it starts a run, before it
// resumes one, and before it puts a change through the gate — and a directive
// that pauses work is why a run stops short of finishing rather than something
// noted after it finished anyway.
//
// Nothing here is attached to whoever received the directive. The receiving role
// is recorded, because "the reviewer was told this" is worth knowing, but it is
// an attribute of the record rather than where the record lives: a directive
// kept inside the conversation that heard it, or inside the run that was told
// it, would reach exactly that conversation or that run.
package directive

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// SchemaVersion is versioned independently of run, conversation, and report
// state. A directive is none of those: it outlives every run it pauses, it is
// revised only by the acts that settle or end it, and it belongs to the product
// rather than to any invocation.
const SchemaVersion = 1

const (
	// MaxTextBytes bounds what the operator said, MaxUnresolvedBytes bounds what
	// has to be settled about it, MaxResolutionBytes bounds how it was settled,
	// and MaxWithdrawalBytes bounds why it was taken back. All four are generous
	// for the paragraph each actually is and small enough that nothing can push a
	// document into the record.
	MaxTextBytes       = 4 << 10
	MaxUnresolvedBytes = 4 << 10
	MaxResolutionBytes = 4 << 10
	MaxWithdrawalBytes = 4 << 10
	// maxLineBytes bounds the values that are read as one line: the artifact a
	// directive changes, and each work item in its scope.
	maxLineBytes = 200
	// maxScopeItems bounds how much work one directive may name. A directive
	// that has to list more work than this is one whose scope is really "all of
	// it", which is what an unscoped directive already says.
	maxScopeItems = 50
)

// Kind is what sort of directive this is, which is the whole of what decides
// whether work stops for it. The three are kept apart rather than flattened into
// a single "important" flag because they are settled by different acts: an
// operational directive is settled by being carried out, an artifact-changing
// one by somebody deciding the change to the canonical document, and an
// ambiguous one by the operator saying what they meant.
type Kind string

const (
	// KindOperational is a directive that takes effect immediately. It changes
	// how work is done rather than what the work is, so nothing waits for it and
	// nothing about it is unresolved. What settles it is somebody carrying it
	// out, recorded as its outcome — which is the whole of what ever says an
	// operational directive was acted on rather than filed.
	KindOperational Kind = "operational"
	// KindArtifact changes a governed artifact: the brief, a goal, a design, a
	// specification. Work derived from that artifact cannot be judged against
	// intent that is being rewritten underneath it, so it pauses until the change
	// is decided and the canonical chain is consistent again.
	KindArtifact Kind = "artifact"
	// KindAmbiguous is a directive nobody can act on without deciding something
	// the operator did not. Work that would be done differently depending on the
	// answer must not be done while nobody has it, so it pauses until the
	// operator answers.
	KindAmbiguous Kind = "ambiguous"
)

// kinds lists the kinds in the order the contract states them, so a refusal
// names exactly what was available.
var kinds = []Kind{KindOperational, KindArtifact, KindAmbiguous}

func (k Kind) Valid() bool {
	for _, kind := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Pauses reports the kinds that stop work. It is a property of the kind rather
// than of one directive because it is the definition of the kind: an operational
// directive that paused work, or an ambiguous one that did not, would be
// misfiled rather than unusual.
func (k Kind) Pauses() bool {
	return k == KindArtifact || k == KindAmbiguous
}

// Headline is what a paused run and an operator listing are both told this kind
// is. Each says something different, because what settles them differs.
func (k Kind) Headline() string {
	switch k {
	case KindOperational:
		return "an operational directive, in effect from the moment it was recorded"
	case KindArtifact:
		return "a directive that changes a governed artifact, so work derived from it waits until the change is decided"
	case KindAmbiguous:
		return "a directive nobody can act on unambiguously, so work it affects waits until the operator answers"
	default:
		return "a directive the harness does not recognize"
	}
}

// Directive is one durable user directive.
type Directive struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	ProductID     domain.ProductID `json:"product_id"`
	Kind          Kind             `json:"kind"`
	// ReceivedBy is the role that was told this. It is recorded rather than acted
	// on: which agent heard a directive decides nothing about where it reaches,
	// and this is here so an operator can ask who they said it to.
	ReceivedBy domain.AgentRole `json:"received_by"`
	ReceivedAt time.Time        `json:"received_at"`
	// Text is what the operator said, in their words.
	Text string `json:"text"`
	// Artifact names the governed artifact this changes. It is required on an
	// artifact-changing directive and refused on the others: a directive that
	// names a document it is not changing would pause work for a reason that is
	// not the one recorded.
	Artifact string `json:"artifact,omitempty"`
	// Unresolved is what has to be settled before the work this affects can carry
	// on: the question the operator has to answer, or the artifact change
	// somebody has to decide. It is required on every directive that pauses work,
	// because a pause that cannot say what it is waiting for is one nobody can
	// lift.
	Unresolved string `json:"unresolved,omitempty"`
	// Scope names the work items this affects. An empty scope means every item,
	// which is the safe reading rather than a lazy one: a directive that rewrites
	// the brief and names nothing affects whatever was derived from the brief,
	// and the harness cannot yet say which work that is. An operator who knows
	// better narrows it.
	Scope []string `json:"scope,omitempty"`
	// Resolution is how the directive was settled, and ResolvedAt is when. They
	// are written together and only once, and they are the one disposition a
	// record ever carries — which is the same field for every kind because it is
	// the same fact, read the way the kind says to read it. On a directive that
	// pauses work it is the answer that let the work resume; on one that pauses
	// nothing it is what came of it, which is the only thing that ever says an
	// operational directive was acted on rather than filed.
	Resolution string     `json:"resolution,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	// Withdrawal is why the operator took the directive back, WithdrawnBy is who
	// took it back, and WithdrawnAt is when. They are written together and only
	// once, and they are what ends a directive rather than what accounts for it: a
	// settlement says what became of a directive that still stands, and a
	// withdrawal says the operator no longer means it.
	//
	// Nothing about what the directive said is removed by them. A withdrawn
	// record keeps the operator's words, what it was waiting for, and whatever
	// disposition it had already collected, so what was directed and acted on
	// while it stood stays readable — the record has to be able to say that a
	// directive once applied and does not now, which deleting it could not.
	Withdrawal  string     `json:"withdrawal,omitempty"`
	WithdrawnBy string     `json:"withdrawn_by,omitempty"`
	WithdrawnAt *time.Time `json:"withdrawn_at,omitempty"`
	// WithdrawnRole is the role the withdrawal was made under: the role whose
	// conversation the operator withdrew it in, or the agent that withdrew it at a
	// command line. It is empty where no persona was involved — the operator at a
	// terminal — and it is attribution in the same sense ReceivedBy is: it decides
	// nothing about the record, and it is here so a surface that answers the
	// thread the directive came from can answer in the voice of whoever took it
	// back rather than in nobody's.
	WithdrawnRole domain.AgentRole `json:"withdrawn_role,omitempty"`
	// Became is what the directive was resolved into — the document or the work
	// item that now carries it — and BecameReason, BecameBy, BecameRole, and
	// BecameAt say why, who, under which role, and when. They are written together
	// and only once, and they end the directive: what it asked for is carried by
	// what it became from then on, so the record stops being live direction.
	//
	// It is neither a settlement nor a withdrawal. A settlement on a standing
	// instruction says what came of it while it still stands, and a withdrawal says
	// nobody means it any more; a directive written into the operating rules is
	// still meant, and it is the document that carries it now. So it is a group of
	// its own, and a directive carried out earlier keeps that outcome beside it.
	Became       string           `json:"became,omitempty"`
	BecameReason string           `json:"became_reason,omitempty"`
	BecameBy     string           `json:"became_by,omitempty"`
	BecameRole   domain.AgentRole `json:"became_role,omitempty"`
	BecameAt     *time.Time       `json:"became_at,omitempty"`
	// Factory is the factory, as its identity line names it, that recorded a
	// directive this machine copied from the shared records of a team. It is
	// empty for a directive recorded on this machine.
	Factory string `json:"factory,omitempty"`
}

var (
	idPattern = regexp.MustCompile(`^directive-[a-f0-9]{32}$`)
	// referencePattern is an identifier as somebody names one: the whole of it,
	// or any prefix of it, which is what a store resolves against what it holds.
	referencePattern = regexp.MustCompile(`^directive-[a-f0-9]{1,32}$`)
)

func NewID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate directive id: %w", err)
	}
	return "directive-" + hex.EncodeToString(bytes), nil
}

// ForSlackMessage is the identifier a directive recorded from one Slack message
// is given. The message's id, its channel and its timestamp (which Slack makes
// unique within a channel), names the directive, so every factory that records
// the same reply records the same directive, and a reply redelivered after a
// restart names the one already recorded.
func ForSlackMessage(channel, ts string) string {
	sum := sha256.Sum256([]byte("slack-message\x00" + channel + "\x00" + ts))
	return "directive-" + hex.EncodeToString(sum[:])[:32]
}

// ValidID reports an identifier of the shape this package issues. A store names
// a file after it, so it is checked before anything built from outside is used
// as a path.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// ValidReference reports a reference somebody could be naming a directive with:
// a full identifier, or a prefix of one. It says nothing about whether any
// directive answers to it, which is the store's to say and needs the records —
// this is what refuses a value that was never going to name one, before anything
// is written down against it.
func ValidReference(reference string) bool {
	return referencePattern.MatchString(strings.TrimSpace(reference))
}

// Validate reports every contract violation in the directive at once.
func (d Directive) Validate() error {
	var problems []error
	if d.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !idPattern.MatchString(d.ID) {
		problems = append(problems, errors.New("id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(d.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !d.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not a directive kind; the kinds are %s", d.Kind, strings.Join(kindNames(), ", ")))
	}
	if err := domain.ValidateIdentifier("received by", string(d.ReceivedBy)); err != nil {
		problems = append(problems, err)
	}
	if d.ReceivedAt.IsZero() {
		problems = append(problems, errors.New("received_at is required"))
	}
	problems = append(problems, boundedText("text", d.Text, MaxTextBytes, true))
	// The artifact is what says which case an artifact-changing directive is
	// about, so it is required there and refused everywhere else.
	if d.Kind == KindArtifact {
		problems = append(problems, boundedLine("artifact", d.Artifact, true))
	} else if strings.TrimSpace(d.Artifact) != "" {
		problems = append(problems, fmt.Errorf("only an %s directive names an artifact", KindArtifact))
	}
	// A pause that cannot say what it is waiting for is a pause nobody can lift,
	// which is the same dead end as a directive that enforces nothing.
	if d.Kind.Pauses() {
		problems = append(problems, boundedText("unresolved", d.Unresolved, MaxUnresolvedBytes, true))
	} else if strings.TrimSpace(d.Unresolved) != "" {
		problems = append(problems, fmt.Errorf("an %s directive has nothing unresolved; it is in effect already", KindOperational))
	}
	if len(d.Scope) > maxScopeItems {
		problems = append(problems, fmt.Errorf("scope names %d work items, limit is %d; a directive that affects more than that is unscoped", len(d.Scope), maxScopeItems))
	}
	for index, scoped := range d.Scope {
		problems = append(problems, boundedLine(fmt.Sprintf("scope[%d]", index), scoped, true))
	}
	// A resolution and the time it was made are one fact. Either alone describes
	// a directive that was half settled, which is not a state anything can act on.
	switch {
	case d.ResolvedAt != nil && d.ResolvedAt.IsZero():
		problems = append(problems, errors.New("resolved_at cannot be the zero time"))
	case d.ResolvedAt != nil:
		problems = append(problems, boundedText("resolution", d.Resolution, MaxResolutionBytes, true))
	case strings.TrimSpace(d.Resolution) != "":
		problems = append(problems, errors.New("resolution requires the time it was resolved"))
	}
	// A withdrawal, who made it, and when it was made are one fact for the same
	// reason. A record that says a directive no longer applies without saying who
	// decided that is one nobody can ask about it.
	switch {
	case d.WithdrawnAt != nil && d.WithdrawnAt.IsZero():
		problems = append(problems, errors.New("withdrawn_at cannot be the zero time"))
	case d.WithdrawnAt != nil:
		problems = append(problems, boundedText("withdrawal", d.Withdrawal, MaxWithdrawalBytes, true))
		problems = append(problems, boundedLine("withdrawn by", d.WithdrawnBy, true))
		// The role is optional, because the operator at a terminal is nobody's
		// persona; one that is named has to be a role the harness has, or a surface
		// would be asked to speak in a voice nobody wrote.
		if d.WithdrawnRole != "" && !d.WithdrawnRole.Valid() {
			problems = append(problems, fmt.Errorf("withdrawn role %q is not one of the harness's roles", d.WithdrawnRole))
		}
	case strings.TrimSpace(d.Withdrawal) != "" || strings.TrimSpace(d.WithdrawnBy) != "" || d.WithdrawnRole != "":
		problems = append(problems, errors.New("a withdrawal requires who withdrew it and the time they did"))
	}
	// What a directive became, why, who resolved it into that, and when are one
	// fact for the same reason again.
	switch {
	case d.BecameAt != nil && d.BecameAt.IsZero():
		problems = append(problems, errors.New("became_at cannot be the zero time"))
	case d.BecameAt != nil:
		problems = append(problems, boundedLine("became", d.Became, true))
		problems = append(problems, boundedText("became reason", d.BecameReason, MaxResolutionBytes, true))
		problems = append(problems, boundedLine("became by", d.BecameBy, true))
		if d.BecameRole != "" && !d.BecameRole.Valid() {
			problems = append(problems, fmt.Errorf("became role %q is not one of the harness's roles", d.BecameRole))
		}
	case strings.TrimSpace(d.Became) != "" || strings.TrimSpace(d.BecameReason) != "" || strings.TrimSpace(d.BecameBy) != "" || d.BecameRole != "":
		problems = append(problems, errors.New("what a directive became requires who resolved it into that and the time they did"))
	}
	problems = append(problems, boundedLine("factory", d.Factory, false))
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid directive: %w", err)
	}
	return nil
}

func kindNames() []string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return names
}

// Resolved reports a disposition on the record: the ambiguity answered, the
// artifact change decided, or the operational directive carried out. It says
// only that somebody has accounted for the directive, and deliberately nothing
// about whether the directive still applies — InForce is that question, and the
// two are not the same one.
//
// They were the same question while only the pausing kinds could be settled at
// all, because settling one of those is exactly what ends it. An operational
// directive is the opposite: it is a standing instruction, in force from the
// moment it was recorded, and admitting one piece of work in answer to it says
// what came of it without retiring it. Anything asking whether a directive is
// still live has to ask InForce, because reading it off this would quietly
// retire an instruction the operator never withdrew.
func (d Directive) Resolved() bool {
	return d.ResolvedAt != nil
}

// Withdrawn reports a directive the operator has taken back. It is the other
// half of Resolved and not a kind of it: settling a directive says what became
// of something the operator still means, and withdrawing one says they no longer
// mean it. A directive can be both — a standing instruction carried out months
// ago and withdrawn today — and reading either off the other would lose the
// difference.
func (d Directive) Withdrawn() bool {
	return d.WithdrawnAt != nil
}

// ResolvedInto reports a directive resolved into the document or work item that
// now carries it. Like a withdrawal it ends the directive, and unlike one it says
// the directive is still meant: it lives on in what it became.
func (d Directive) ResolvedInto() bool {
	return d.BecameAt != nil
}

// InForce reports a directive that still constrains work, which is the question
// enforcement and every listing of live direction asks.
//
// A pausing directive is in force until somebody resolves it: it is a hold, and
// answering what it was waiting for is what lifts it. A directive that pauses
// nothing is in force from the moment it was recorded and stays there — it
// changes how work is done rather than holding any of it up, so there is nothing
// for an outcome to lift. "Stop opening pull requests for documentation-only
// changes" is still the instruction after the item it prompted is admitted, and
// after that item ships.
//
// What ends a directive of any kind is the operator withdrawing it, which is the
// one act that reaches a standing instruction: an operational directive is in
// force from the moment it was recorded, and until withdrawal existed nothing
// could ever take one out again, so a record that had collected a directive
// could only ever grow the set of things it said were in force. A directive
// recorded in error stayed in force forever, and every listing of live direction
// was that much less believable for it.
//
// Resolving a directive into what now carries it ends one of any kind too: a
// standing instruction written into the operating rules is read from there, and
// leaving the record live beside it would be the same rule stated twice, in two
// places that could come to disagree.
func (d Directive) InForce() bool {
	if d.Withdrawn() || d.ResolvedInto() {
		return false
	}
	if d.Kind.Pauses() {
		return !d.Resolved()
	}
	return true
}

// Settlement is what settling this kind of directive is called, which is the
// difference between the two acts that reach the same two fields. A pausing
// directive is resolved, by somebody answering what it left unresolved; one that
// pauses nothing is carried out, and saying it was resolved would describe a
// pause it never held.
func (d Directive) Settlement() string {
	if d.Kind.Pauses() {
		return "resolved"
	}
	return "carried out"
}

// Pauses reports a directive that stops the work it affects. Only one that is
// still in force does: settling it is exactly what lets the work resume, and
// withdrawing it takes back the thing the work was waiting on, so a directive
// that is no longer in force is a record of something that happened rather than
// a live constraint.
func (d Directive) Pauses() bool {
	return d.Kind.Pauses() && d.InForce()
}

// Affects reports whether this directive reaches one work item. An unscoped
// directive reaches all of them.
func (d Directive) Affects(workItemID string) bool {
	if len(d.Scope) == 0 {
		return true
	}
	wanted := strings.TrimSpace(workItemID)
	for _, scoped := range d.Scope {
		if strings.TrimSpace(scoped) == wanted {
			return true
		}
	}
	return false
}

// Resolve settles a directive that pauses work, returning the resolved copy. It
// refuses to re-resolve one: a second resolution would overwrite the account of
// why the work resumed the first time, and the work has already resumed.
func (d Directive) Resolve(resolution string, at time.Time) (Directive, error) {
	if d.Withdrawn() {
		return Directive{}, d.alreadyWithdrawn()
	}
	if d.ResolvedInto() {
		return Directive{}, d.alreadyResolvedInto()
	}
	if d.Resolved() {
		return Directive{}, d.alreadySettled()
	}
	if !d.Kind.Pauses() {
		return Directive{}, fmt.Errorf("%s is %s and has nothing to resolve; it took effect when it was recorded, and what came of it is recorded as an outcome instead", d.ID, d.Kind)
	}
	if strings.TrimSpace(resolution) == "" {
		return Directive{}, errors.New("say how the directive was settled; work resumes on the answer rather than on the act of answering")
	}
	return d.settle(resolution, at)
}

// CarryOut records what came of a directive that pauses nothing, returning the
// settled copy. It is Resolve's sibling rather than a special case of it, and
// the two refuse each other's kinds, because they are opposite acts on the same
// two fields: resolving is somebody answering what held work up, and carrying
// out is somebody having done what was asked. An outcome written onto a pausing
// directive would settle it — lifting the pause it holds — on the strength of
// something that never answered what it was waiting for.
//
// Until this existed, an operational directive had no disposition at all: it was
// in force from the moment it was recorded and nothing ever said whether anybody
// acted on it, so what became of the commonest kind of directive lived only in
// whoever remembered. That is what an outcome is for, and it is why the outcome
// is worth naming the work it became rather than only saying it was done.
func (d Directive) CarryOut(outcome string, at time.Time) (Directive, error) {
	if d.Withdrawn() {
		return Directive{}, d.alreadyWithdrawn()
	}
	if d.ResolvedInto() {
		return Directive{}, d.alreadyResolvedInto()
	}
	if d.Resolved() {
		return Directive{}, d.alreadySettled()
	}
	if d.Kind.Pauses() {
		return Directive{}, fmt.Errorf("%s is %s and is settled by resolving what it left unresolved rather than by being carried out; an outcome on it would lift the pause it holds", d.ID, d.Kind)
	}
	if strings.TrimSpace(outcome) == "" {
		return Directive{}, errors.New("say what came of the directive; an outcome nobody wrote down is one whoever asked for it never hears")
	}
	return d.settle(outcome, at)
}

// Withdraw takes a directive back, returning the withdrawn copy. It is what the
// operator reaches for when they no longer mean what they said, or when what was
// recorded as a directive never was one — a question read as an instruction, and
// then in force forever because nothing could ever end it.
//
// It is not a settlement and not a deletion. A settlement says what became of a
// directive that still stands, so writing one here would say the operator's
// instruction was carried out when what actually happened is that they took it
// back; and deleting the record would take the operator's own words with it,
// leaving the work that was done while it stood answering nothing anybody can
// read. What withdrawal changes is exactly one thing: the directive stops being
// in force, so nothing is enforced against it and no listing of live direction
// shows it as live.
//
// Any kind can be withdrawn. On one that pauses work, withdrawal lifts the pause
// without answering what it was waiting for — which is the honest record of "I
// take it back" and the only one that fits, because there is no answer to a
// question the operator no longer means to have asked.
//
// by is who took it back, in words the record answers for; role is the persona
// it was taken back under, and is empty for the operator at a terminal.
func (d Directive) Withdraw(by string, role domain.AgentRole, reason string, at time.Time) (Directive, error) {
	if d.Withdrawn() {
		return Directive{}, d.alreadyWithdrawn()
	}
	if d.ResolvedInto() {
		return Directive{}, d.alreadyResolvedInto()
	}
	if !d.InForce() {
		// A pausing directive somebody resolved is already out of force, and
		// withdrawing it would be taking back something that has already ended —
		// after the work it held has resumed on the strength of the answer.
		return Directive{}, fmt.Errorf("%s was already %s at %s and no longer applies; there is nothing left to withdraw",
			d.ID, d.Settlement(), d.ResolvedAt.UTC().Format(time.RFC3339))
	}
	if strings.TrimSpace(by) == "" {
		return Directive{}, errors.New("say who withdrew the directive; a directive that stopped applying because of nobody is one nobody can be asked about")
	}
	if strings.TrimSpace(reason) == "" {
		return Directive{}, errors.New("say why the directive is withdrawn; the record keeps what was said, and without this it cannot say why it stopped applying")
	}
	withdrawnAt := at.UTC()
	withdrawn := d
	withdrawn.Withdrawal = strings.TrimSpace(reason)
	withdrawn.WithdrawnBy = strings.TrimSpace(by)
	withdrawn.WithdrawnAt = &withdrawnAt
	withdrawn.WithdrawnRole = domain.AgentRole(strings.TrimSpace(string(role)))
	if err := withdrawn.Validate(); err != nil {
		return Directive{}, err
	}
	return withdrawn, nil
}

// ResolveInto ends a directive by naming what now carries it — a document it was
// written into, or a work item that is its answer — returning the ended copy. It
// is the Lead Product Manager's act on a directive she carried somewhere, and it
// is refused on one that has already ended, however it ended: a second account
// of how a directive stopped applying would overwrite the first.
//
// On a directive that pauses work it also resolves the directive, with the reason
// as the resolution, so every reader that asks whether the pause was answered
// reads the same thing the ending says. A standing instruction keeps whatever
// outcome it had already collected: what came of it while it stood is still true.
//
// by is who resolved it, in words the record answers for; role is the persona it
// was resolved under.
func (d Directive) ResolveInto(became, by string, role domain.AgentRole, reason string, at time.Time) (Directive, error) {
	if d.Withdrawn() {
		return Directive{}, d.alreadyWithdrawn()
	}
	if d.ResolvedInto() {
		return Directive{}, d.alreadyResolvedInto()
	}
	if !d.InForce() {
		return Directive{}, fmt.Errorf("%s was already %s at %s and no longer applies; there is nothing left to resolve",
			d.ID, d.Settlement(), d.ResolvedAt.UTC().Format(time.RFC3339))
	}
	if strings.TrimSpace(became) == "" {
		return Directive{}, errors.New("say what the directive became: the document it was written into, or the work item that answers it")
	}
	if strings.TrimSpace(by) == "" {
		return Directive{}, errors.New("say who resolved the directive; a directive that stopped applying because of nobody is one nobody can be asked about")
	}
	if strings.TrimSpace(reason) == "" {
		return Directive{}, errors.New("say why the directive is resolved into what it became; the record keeps what was said, and without this it cannot say why it stopped applying")
	}
	endedAt := at.UTC()
	ended := d
	if d.Kind.Pauses() {
		ended.Resolution = strings.TrimSpace(reason)
		ended.ResolvedAt = &endedAt
	}
	ended.Became = strings.TrimSpace(became)
	ended.BecameReason = strings.TrimSpace(reason)
	ended.BecameBy = strings.TrimSpace(by)
	ended.BecameRole = domain.AgentRole(strings.TrimSpace(string(role)))
	ended.BecameAt = &endedAt
	if err := ended.Validate(); err != nil {
		return Directive{}, err
	}
	return ended, nil
}

// settle writes the one disposition a directive ever takes. Both acts reach it,
// having each refused the kinds they are not for, so a disposition on a record
// always means what that record's kind says it means.
func (d Directive) settle(disposition string, at time.Time) (Directive, error) {
	settledAt := at.UTC()
	settled := d
	settled.Resolution = strings.TrimSpace(disposition)
	settled.ResolvedAt = &settledAt
	if err := settled.Validate(); err != nil {
		return Directive{}, err
	}
	return settled, nil
}

// alreadySettled refuses a second disposition, in the words of the act that
// would have written it. A record carries one account of what became of it, and
// the first one is the one anything downstream has already reported.
func (d Directive) alreadySettled() error {
	return fmt.Errorf("%s was already %s at %s", d.ID, d.Settlement(), d.ResolvedAt.UTC().Format(time.RFC3339))
}

// alreadyWithdrawn refuses anything written onto a directive the operator has
// taken back. Settling one would say what became of an instruction nobody means
// any more, and withdrawing it twice would overwrite the account of who ended it
// and why.
func (d Directive) alreadyWithdrawn() error {
	return fmt.Errorf("%s was withdrawn at %s by %s and no longer applies",
		d.ID, d.WithdrawnAt.UTC().Format(time.RFC3339), d.WithdrawnBy)
}

// alreadyResolvedInto refuses anything written onto a directive already resolved
// into what carries it, for the reason alreadyWithdrawn does.
func (d Directive) alreadyResolvedInto() error {
	return fmt.Errorf("%s was resolved into %s at %s by %s and no longer applies",
		d.ID, d.Became, d.BecameAt.UTC().Format(time.RFC3339), d.BecameBy)
}

// Pausing selects the in-force directives that pause one work item. It is the
// question the run pipeline asks, in one place, so a run that starts, a run that
// resumes, and a run at its gate are all held to the same reading.
//
// In force rather than unresolved, because the two came apart when a directive
// could be withdrawn: a withdrawn one is still unresolved — nobody ever answered
// what it was waiting for — and holds nothing, because the operator took back the
// thing the work was waiting on.
func Pausing(directives []Directive, workItemID string) []Directive {
	var pausing []Directive
	for _, candidate := range directives {
		if candidate.Pauses() && candidate.Affects(workItemID) {
			pausing = append(pausing, candidate)
		}
	}
	return pausing
}

// Sort puts directives in the order they were received, and settles ties by
// identifier so a listing is stable across processes.
func Sort(directives []Directive) {
	sort.SliceStable(directives, func(i, j int) bool {
		if directives[i].ReceivedAt.Equal(directives[j].ReceivedAt) {
			return directives[i].ID < directives[j].ID
		}
		return directives[i].ReceivedAt.Before(directives[j].ReceivedAt)
	})
}

// Render describes one directive for whoever has to act on it. The operator's
// own words are indented under the harness's line, as provider text is
// everywhere else, so nothing inside a directive can dress itself up as the
// harness speaking.
func (d Directive) Render() string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "[%s] %s\n", d.ID, d.Kind.Headline())
	fmt.Fprintf(&rendered, "  received %s by the %s\n", d.ReceivedAt.UTC().Format(time.RFC3339), d.ReceivedBy)
	if d.Artifact != "" {
		fmt.Fprintf(&rendered, "  artifact: %s\n", d.Artifact)
	}
	fmt.Fprintf(&rendered, "  said: %s\n", indented(d.Text))
	if d.Unresolved != "" {
		if d.Resolved() {
			fmt.Fprintf(&rendered, "  was unresolved: %s\n", indented(d.Unresolved))
		} else {
			fmt.Fprintf(&rendered, "  unresolved: %s\n", indented(d.Unresolved))
		}
	}
	rendered.WriteString("  affects: " + d.scope() + "\n")
	// A question in the record is a directive nobody gave, and every one there
	// was recorded before questions were told from instructions. It is said on
	// the entry rather than filtered out of the listing, because the record is
	// evidence rather than a worklist: what ends it is the operator withdrawing
	// it, and this line is what tells them which entries to look at. One already
	// withdrawn or settled is over and needs no marking.
	if d.InForce() && d.ReadsAsQuestion() {
		rendered.WriteString("  reads as a question rather than an instruction: it directs nothing, and withdrawing it is what ends it\n")
	}
	// A pausing directive resolved into what carries it is resolved with the same
	// reason the line below prints, so it is said once, on that line.
	if d.Resolved() && !(d.ResolvedInto() && d.Kind.Pauses()) {
		fmt.Fprintf(&rendered, "  %s %s: %s\n", d.Settlement(), d.ResolvedAt.UTC().Format(time.RFC3339), indented(d.Resolution))
	}
	if d.ResolvedInto() {
		by := d.BecameBy
		if d.BecameRole != "" {
			by += " (as the " + d.BecameRole.Title() + ")"
		}
		fmt.Fprintf(&rendered, "  resolved into %s %s by %s, and no longer applies: %s\n",
			d.Became, d.BecameAt.UTC().Format(time.RFC3339), by, indented(d.BecameReason))
	}
	// A withdrawal is printed last because it is the last thing that happened to
	// the record and the thing that ends it. Everything above it stays on the
	// page: what the operator said, and whatever the directive collected while it
	// stood, are what make a withdrawn directive readable as withdrawn rather
	// than as a record somebody deleted the middle of.
	if d.Withdrawn() {
		by := d.WithdrawnBy
		if d.WithdrawnRole != "" {
			by += " (as the " + d.WithdrawnRole.Title() + ")"
		}
		fmt.Fprintf(&rendered, "  withdrawn %s by %s, and no longer applies: %s\n",
			d.WithdrawnAt.UTC().Format(time.RFC3339), by, indented(d.Withdrawal))
	}
	return rendered.String()
}

// Summary states in one line what a paused run is waiting for, which is what
// goes onto the work item and into what an operator reads first.
func (d Directive) Summary() string {
	return fmt.Sprintf("%s (%s): %s — unresolved: %s",
		d.ID, d.Kind, oneLine(d.Text), oneLine(d.Unresolved))
}

// scope says what the directive reaches, naming the unscoped case rather than
// printing an empty list that reads like nothing.
func (d Directive) scope() string {
	if len(d.Scope) == 0 {
		return "every work item, because the directive named none"
	}
	return strings.Join(d.Scope, ", ")
}

// indented keeps a multi-line value inside the block it was printed in, so
// operator prose with newlines in it cannot start a line at the margin.
func indented(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for index := 1; index < len(lines); index++ {
		lines[index] = "    " + strings.TrimSpace(lines[index])
	}
	return strings.Join(lines, "\n")
}

// oneLine folds a value into a single bounded line, so a directive stays one
// entry of a listing or one note on a work item whatever it contains.
func oneLine(value string) string {
	return oneline.Fold(value, maxLineBytes)
}

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

func boundedLine(field, value string, required bool) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if len(trimmed) > maxLineBytes {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), maxLineBytes)
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return fmt.Errorf("%s cannot span lines", field)
	}
	return nil
}

// Merge is how a change recorded on another machine is applied to this
// machine's copy of a directive without rewriting what the copy already says:
// it returns base with each disposition group base lacks and other has copied
// from other. A group base already carries is left as it is, whatever other
// holds.
func Merge(base, other Directive) Directive {
	if base.ResolvedAt == nil && other.ResolvedAt != nil {
		base.Resolution, base.ResolvedAt = other.Resolution, other.ResolvedAt
	}
	if base.WithdrawnAt == nil && other.WithdrawnAt != nil {
		base.Withdrawal, base.WithdrawnBy, base.WithdrawnAt, base.WithdrawnRole =
			other.Withdrawal, other.WithdrawnBy, other.WithdrawnAt, other.WithdrawnRole
	}
	if base.BecameAt == nil && other.BecameAt != nil {
		base.Became, base.BecameReason, base.BecameBy, base.BecameRole, base.BecameAt =
			other.Became, other.BecameReason, other.BecameBy, other.BecameRole, other.BecameAt
	}
	return base
}

// Dispositions names the disposition groups the directive carries, in the order
// "resolved", "withdrawn", "became". It lets a caller tell whether one copy
// carries a change another lacks.
func (d Directive) Dispositions() []string {
	var names []string
	if d.ResolvedAt != nil {
		names = append(names, "resolved")
	}
	if d.WithdrawnAt != nil {
		names = append(names, "withdrawn")
	}
	if d.BecameAt != nil {
		names = append(names, "became")
	}
	return names
}

// SameDispositions reports whether two copies of a directive carry the same
// value in each disposition group.
func SameDispositions(a, b Directive) bool {
	return sameTime(a.ResolvedAt, b.ResolvedAt) && a.Resolution == b.Resolution &&
		sameTime(a.WithdrawnAt, b.WithdrawnAt) && a.Withdrawal == b.Withdrawal &&
		a.WithdrawnBy == b.WithdrawnBy && a.WithdrawnRole == b.WithdrawnRole &&
		sameTime(a.BecameAt, b.BecameAt) && a.Became == b.Became &&
		a.BecameReason == b.BecameReason && a.BecameBy == b.BecameBy && a.BecameRole == b.BecameRole
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
