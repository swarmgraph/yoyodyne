// Package artifact gives the canonical Markdown upstream of a work item a
// stable identity: the product brief, the goals, the designs and
// specifications, and the decision records.
//
// Without one, nothing can refer to any of them durably. A goal that supports
// the brief says so in prose a reader has to check by hand, a design that
// serves a goal names it by whatever wording it happened to have that week, and
// a document that is renamed silently orphans everything downstream of it. So
// no relationship between two artifacts can be validated, which is the whole
// point of writing them down in one repository rather than in five documents
// that agree by coincidence.
//
// The model is the one the invariants already ship, deliberately rather than a
// second scheme beside it: the file name is the identity, a frontmatter id that
// disagrees with it is refused rather than reconciled, the lifecycle status is
// stated rather than inferred from whether anybody still links to the file, and
// every change appends to a revision log saying who changed it and why. Two
// identity schemes in one repository is the failure to design against — a
// reader who has to know which kind of document they are holding before they
// can tell what its id is has no identity scheme at all.
//
// The relationships identity makes expressible are judged in two places,
// because they are two different questions. One file answers whether `supports`
// is a reference at all — an id shaped like one, naming something other than
// the artifact itself — and that is Validate, below. Whether the reference
// resolves, and whether anything upstream of it arrives at the brief, needs the
// whole graph rather than one document, and that is references.go, run when the
// set is loaded. Neither refuses an artifact: a dangling reference and an
// orphan are reported beside a set that still holds every document it read.
//
// Who may change one of these documents is a third question, and it is answered
// in ownership.go. Ownership is an authorization boundary rather than a prompt
// convention, so no mutation reaches the filesystem without going through
// Authorize; and a revision log recording a change by a role that does not own
// the document is reported over the loaded set, beside the broken relationships
// and for the same reason — losing a document nobody can now correct is worse
// than carrying a named problem against one that still holds.
//
// Whether the operator approved one, and approved which version of it, is a
// fourth question, and it is answered in approval.go. It is separate from
// ownership because it is a different boundary: every one of these documents is
// drafted by the role that owns it, so approval a role could record would be
// that role approving its own document.
package artifact

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Kind is what sort of artifact this is. The values are the canonical artifact
// types the intent graph runs through, and they are a closed set because a kind
// nobody recognizes is a document that cannot be validated against the rules its
// kind carries — which is what an artifact kind is for.
type Kind string

const (
	KindBrief         Kind = "brief"
	KindGoals         Kind = "goals"
	KindNonGoals      Kind = "non-goals"
	KindRules         Kind = "rules"
	KindDesign        Kind = "design"
	KindSpecification Kind = "specification"
	KindDecision      Kind = "decision"
)

func (k Kind) Valid() bool {
	switch k {
	case KindBrief, KindGoals, KindNonGoals, KindRules, KindDesign, KindSpecification, KindDecision:
		return true
	default:
		return false
	}
}

// Kinds lists every artifact kind, so a message that has to name them stays
// equal to what is accepted.
func Kinds() []Kind {
	return []Kind{KindBrief, KindGoals, KindNonGoals, KindRules, KindDesign, KindSpecification, KindDecision}
}

// Status is where an artifact is in its lifecycle. It is explicit for the same
// reason an invariant's is: an artifact that stopped applying and one that
// nobody happens to link to look identical from the outside, and deleting the
// file leaves whoever read it last month with no way to find out which of the
// two happened.
type Status string

const (
	// StatusDraft is written but not yet in force — the state an artifact is in
	// while the approval its kind requires is outstanding.
	StatusDraft Status = "draft"
	// StatusActive is in force: this is what the product currently intends.
	StatusActive Status = "active"
	// StatusSuperseded is replaced by a later artifact. Which one is not part of
	// this schema yet; the revision that recorded it says so in prose.
	StatusSuperseded Status = "superseded"
	// StatusRetired stopped applying and was not replaced.
	StatusRetired Status = "retired"
)

func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusActive, StatusSuperseded, StatusRetired:
		return true
	default:
		return false
	}
}

// Statuses lists every lifecycle status in lifecycle order.
func Statuses() []Status {
	return []Status{StatusDraft, StatusActive, StatusSuperseded, StatusRetired}
}

// Action is what one recorded revision did.
type Action string

const (
	ActionCreated    Action = "created"
	ActionAmended    Action = "amended"
	ActionSuperseded Action = "superseded"
	ActionRetired    Action = "retired"
	// ActionIdentified changed only the identities a goals document gives its
	// goals — the bracketed identifier an entry opens with — and not a word of any
	// goal. It is its own action rather than an amendment because it is not a
	// change of intent: an identifier is what an attribution resolves by, and
	// recording one leaves every goal saying what it said. So it does not move the
	// document's approval, and nothing downstream reads it as a change upstream.
	// Store.Identify is the only write that records one, and it refuses a body
	// that differs in anything else. See identity.go.
	ActionIdentified Action = "identified"
)

func (a Action) Valid() bool {
	switch a {
	case ActionCreated, ActionAmended, ActionSuperseded, ActionRetired, ActionIdentified:
		return true
	default:
		return false
	}
}

// Bounds on one artifact's metadata. The artifact's own prose is unbounded here
// — a brief is a document rather than a constraint — and only the metadata that
// every reader of the set carries is held to a size.
const (
	MaxTitleBytes    = 200
	MaxReasonBytes   = 1 << 10
	MaxSupportsCount = 20
)

// Revision is one recorded change to an artifact: what happened, which role's
// authority it happened under, when, and why. The role is recorded rather than
// assumed because artifact ownership is an authorization boundary, and a
// boundary is only auditable if the artifact says who exercised it.
type Revision struct {
	Action Action           `yaml:"action" json:"action"`
	By     domain.AgentRole `yaml:"by" json:"by"`
	At     time.Time        `yaml:"at" json:"at"`
	Reason string           `yaml:"reason" json:"reason"`
	// Intent is what the role that made a creation or amendment says it did to
	// what the document intends, and it is empty where the role said nothing.
	// See rewording.go for what it decides.
	Intent Intent `yaml:"intent,omitempty" json:"intent,omitempty"`
}

// Artifact is one canonical document's identity and metadata. The document's
// prose is not here: it stays in the file, which is the source of truth for its
// content, and this is what everything downstream refers to it by.
type Artifact struct {
	// ID is the stable identity. It is also the file's name, so the identity and
	// the location can never disagree and two files cannot claim one artifact.
	ID    string `yaml:"id" json:"id"`
	Kind  Kind   `yaml:"kind" json:"kind"`
	Title string `yaml:"title" json:"title"`
	// Supports names the artifacts upstream of this one: the goal a design
	// serves, the brief a goal serves. It is what makes the intent graph
	// traversable rather than inferred from prose that happens to mention
	// something. The brief supports nothing, being the root.
	Supports []string `yaml:"supports,omitempty" json:"supports,omitempty"`
	Status   Status   `yaml:"status" json:"status"`
	// Revisions is the append-only record of what changed and why. It carries at
	// least the creation, so an artifact always says where it came from.
	Revisions []Revision `yaml:"revisions" json:"revisions"`
	// Approvals is the append-only record of the operator approving this document,
	// each naming the revision it was given for. It is empty for a document nobody
	// has approved, which is not the same as one nothing requires approving: what
	// requires approval is the project's configuration, and see approval.go.
	Approvals []Approval `yaml:"approvals,omitempty" json:"approvals,omitempty"`
	// Path is the repository-relative file this was read from.
	Path string `yaml:"-" json:"path,omitempty"`
}

// InForce reports an artifact that states what the product currently intends. A
// draft is not in force yet and a superseded or retired one no longer is; all
// three are still recorded, because the record of what was intended is what
// makes a later change traceable.
func (a Artifact) InForce() bool {
	return a.Status == StatusActive
}

// Ended returns the revision that superseded or retired this artifact, and
// whether it has one. An artifact whose status says it ended without such a
// revision is refused by Validate, so this is how a reader gets the reason
// without re-deriving it from the prose.
func (a Artifact) Ended() (Revision, bool) {
	for index := len(a.Revisions) - 1; index >= 0; index-- {
		switch a.Revisions[index].Action {
		case ActionSuperseded, ActionRetired:
			return a.Revisions[index], true
		}
	}
	return Revision{}, false
}

// Validate reports every contract violation in the artifact at once, so a file
// that is wrong in three ways is corrected once rather than three times.
func (a Artifact) Validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("artifact id", a.ID); err != nil {
		problems = append(problems, err)
	}
	if !a.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q must be one of %s", a.Kind, renderKinds()))
	}
	switch title := strings.TrimSpace(a.Title); {
	case title == "":
		problems = append(problems, errors.New("title is required"))
	case len(title) > MaxTitleBytes:
		problems = append(problems, fmt.Errorf("title is %d bytes, limit is %d", len(title), MaxTitleBytes))
	}
	if !a.Status.Valid() {
		problems = append(problems, fmt.Errorf("status %q must be one of %s", a.Status, renderStatuses()))
	}
	problems = append(problems, a.supportsProblems()...)
	if len(a.Revisions) == 0 {
		problems = append(problems, errors.New("revisions must record at least the creation of this artifact"))
	}
	// Whether the role a revision names owns this kind is deliberately not checked
	// here: it is reported over the loaded set instead, because it must not cost
	// the document. See UnauthorizedRevisions.
	for index, revision := range a.Revisions {
		if err := revision.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("revisions[%d]: %w", index, err))
		}
	}
	problems = append(problems, a.approvalProblems()...)
	// The two halves of an ending have to agree, the same way an invariant's
	// retirement does: a file that says superseded without the revision that
	// superseded it records no decision, and an artifact still in force that
	// records one describes intent that was replaced and is somehow still
	// current.
	ended, hasEnding := a.Ended()
	switch {
	case (a.Status == StatusSuperseded || a.Status == StatusRetired) && !hasEnding:
		problems = append(problems, fmt.Errorf("a %s artifact must record the revision that %s it", a.Status, a.Status))
	case (a.Status == StatusDraft || a.Status == StatusActive) && hasEnding:
		problems = append(problems, fmt.Errorf("a %s artifact cannot record having been %s", a.Status, ended.Action))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid artifact %q: %w", a.ID, err)
	}
	return nil
}

// supportsProblems reports what makes a declared upstream reference unusable.
// Only the shape of a reference is checked here: an id that no artifact answers
// to is a broken relationship rather than a malformed file, and finding those
// needs the whole set rather than one document.
func (a Artifact) supportsProblems() []error {
	var problems []error
	if len(a.Supports) > MaxSupportsCount {
		problems = append(problems, fmt.Errorf("supports names %d artifacts, limit is %d", len(a.Supports), MaxSupportsCount))
	}
	for index, reference := range a.Supports {
		trimmed := strings.TrimSpace(reference)
		if err := domain.ValidateIdentifier(fmt.Sprintf("supports[%d]", index), trimmed); err != nil {
			problems = append(problems, err)
			continue
		}
		if trimmed == strings.TrimSpace(a.ID) {
			problems = append(problems, fmt.Errorf("supports[%d] names this artifact itself; nothing is upstream of itself", index))
		}
	}
	return problems
}

// Validate reports every contract violation in the revision at once.
func (r Revision) Validate() error {
	var problems []error
	if !r.Action.Valid() {
		problems = append(problems, fmt.Errorf("action %q must be %q, %q, %q, %q, or %q",
			r.Action, ActionCreated, ActionAmended, ActionSuperseded, ActionRetired, ActionIdentified))
	}
	switch {
	case r.Intent != "" && !r.Intent.Valid():
		problems = append(problems, fmt.Errorf("intent %q must be %q or %q, or left out", r.Intent, IntentConsistent, IntentFundamental))
	case r.Intent != "" && r.Action != ActionAmended && r.Action != ActionCreated:
		problems = append(problems, fmt.Errorf("intent is recorded on a creation or amendment, and this revision is %q", r.Action))
	}
	// Whether the role that made this revision owns the artifact needs the kind,
	// which one revision does not carry, so that half is checked by the artifact
	// itself against Authorize. What is required here is that a revision names a
	// role the harness knows at all, so the record says whose authority it was
	// made under rather than naming somebody nobody can hold to it.
	if !knownRole(r.By) {
		problems = append(problems, fmt.Errorf("by %q must name a role: %s", r.By, renderRoles()))
	}
	if r.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	switch reason := strings.TrimSpace(r.Reason); {
	case reason == "":
		problems = append(problems, errors.New("reason is required"))
	case len(reason) > MaxReasonBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), MaxReasonBytes))
	}
	return errors.Join(problems...)
}

// roles are the roles a revision may be recorded under. It is the harness's own
// set rather than a free-form string, so a typo is a refused artifact instead of
// an authority nobody can trace.
func roles() []domain.AgentRole {
	return []domain.AgentRole{
		domain.RoleProductManager,
		domain.RoleArchitect,
		domain.RoleDevelopmentManager,
		domain.RoleDeveloper,
		domain.RoleReviewer,
	}
}

func knownRole(role domain.AgentRole) bool {
	for _, known := range roles() {
		if role == known {
			return true
		}
	}
	return false
}

// Problem names one file in an artifact home that could not be read as an
// artifact, and says why. An artifact like this is refused rather than loaded
// as half of one: a document with no usable identity cannot be referred to, and
// admitting it under a guessed id would be worse than saying it is not there.
// It is reported wherever the set is used, so the gap is visible rather than
// silent.
type Problem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (p Problem) String() string {
	return p.Path + ": " + p.Reason
}

// Set is every artifact the repository records, the files in its artifact
// homes that could not be read as one, and what is wrong with the documents
// that did load.
type Set struct {
	// Homes are the repository-relative directories this was loaded from,
	// carried so what is reported can say where it looked.
	Homes     []string   `json:"homes,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Problems  []Problem  `json:"problems,omitempty"`
	// ReferenceProblems are what is wrong with the documents that did load: the
	// relationships between them that do not hold, and the changes recorded under
	// a role that does not own the document. They are kept apart from Problems
	// because they mean something different: a Problem is a file that is not in
	// the set, and one of these is a document that is. Nothing is dropped over
	// one.
	ReferenceProblems []ReferenceProblem `json:"reference_problems,omitempty"`
}

// Find returns the artifact with an id, whatever its lifecycle status.
func (s Set) Find(id string) (Artifact, bool) {
	for _, candidate := range s.Artifacts {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Artifact{}, false
}

// ReferenceProblemsFor returns the broken relationships recorded against one
// artifact, so a reader looking at a single document is told what is wrong with
// it without filtering the whole set themselves.
func (s Set) ReferenceProblemsFor(id string) []ReferenceProblem {
	matched := make([]ReferenceProblem, 0, len(s.ReferenceProblems))
	for _, problem := range s.ReferenceProblems {
		if problem.ID == id {
			matched = append(matched, problem)
		}
	}
	return matched
}

// InForce returns the artifacts that state what the product currently intends.
func (s Set) InForce() []Artifact {
	current := make([]Artifact, 0, len(s.Artifacts))
	for _, candidate := range s.Artifacts {
		if candidate.InForce() {
			current = append(current, candidate)
		}
	}
	return current
}

// OfKind returns the artifacts of one kind, in the same order the set holds
// them.
func (s Set) OfKind(kind Kind) []Artifact {
	matched := make([]Artifact, 0, len(s.Artifacts))
	for _, candidate := range s.Artifacts {
		if candidate.Kind == kind {
			matched = append(matched, candidate)
		}
	}
	return matched
}

func renderKinds() string {
	rendered := make([]string, 0, len(Kinds()))
	for _, kind := range Kinds() {
		rendered = append(rendered, string(kind))
	}
	return strings.Join(rendered, ", ")
}

func renderStatuses() string {
	rendered := make([]string, 0, len(Statuses()))
	for _, status := range Statuses() {
		rendered = append(rendered, string(status))
	}
	return strings.Join(rendered, ", ")
}

func renderRoles() string {
	rendered := make([]string, 0, len(roles()))
	for _, role := range roles() {
		rendered = append(rendered, string(role))
	}
	sort.Strings(rendered)
	return strings.Join(rendered, ", ")
}
