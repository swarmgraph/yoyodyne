package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MaxProposalBytes bounds the untrusted proposal payload one turn may carry.
// The reply it arrives in is bounded by the provider; this bounds the part the
// harness decodes and shows an operator as something to approve.
const MaxProposalBytes = 32 << 10

// MaxProposalsPerTurn bounds how many work items one reply may propose. An
// operator decides on every one of them, so a turn that proposes a whole
// backlog is refused rather than turned into a queue of prompts nobody reads.
// maxProposalsPerTurnText is the same bound as the contract states it; a test
// keeps the number the product manager is told equal to the one enforced here.
const (
	MaxProposalsPerTurn     = 10
	maxProposalsPerTurnText = "10"
)

const (
	maxProposalTitleBytes = 200
	maxProposalTextBytes  = 8 << 10
)

// proposalFence opens the one block a reply may carry proposals in. It is a
// distinct language tag rather than plain JSON so proposals can never be
// confused with a JSON example the conversation happens to be discussing.
const proposalFence = "```yoyodyne-proposal"

// Proposal is one Beads work item the product manager suggests. It carries no
// authority: it is a recommendation, and what becomes of it is the harness's to
// decide against the project's admission policy — either the operator is asked,
// or it is admitted because it serves a goal they already approved.
type Proposal struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// Rationale is why this work follows from the conversation. It is required
	// because an operator approving work needs the reasoning, not only the
	// wording, and it is what a created item is traced back to.
	Rationale string `json:"rationale"`
	// Goal is the goal this work serves, named from the specifications. It is
	// required, and requiring it is what makes traceability something the
	// harness holds rather than something a well-behaved model asserts: work
	// that serves no goal cannot be proposed at all, so it is raised as a
	// concern instead and the operator is asked.
	Goal          string   `json:"goal"`
	RelevantGoals []string `json:"relevant_goals,omitempty"`
	// Parent and Dependencies name Beads items that already exist. The product
	// manager may place proposed work in the tracker's structure; it may not
	// invent the items it is placed against.
	Parent       string   `json:"parent,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	// Class is the kind of work this is, where a project treats a kind of work
	// differently at admission. It is optional and usually absent: work that
	// claims no class is ordinary work, and that is what most work is.
	//
	// It is a claim the agent makes about its own proposal, which is exactly what
	// it looks like. What keeps it honest is that a class only ever matters where
	// the operator carved it out themselves, and what they carved out is a kind
	// of work that changes nothing: an item claiming to be diagnosis and then
	// doing something else is a work item whose description says what it does,
	// under a goal that had to resolve, in a queue the operator reads.
	Class domain.WorkItemClass `json:"class,omitempty"`
	// Kind is whether the work fixes a bug or adds a feature, and it is required:
	// the rework rate is read from it, and an item admitted without one is a merge
	// that rate cannot count. It is written into the tracker's type field.
	Kind domain.WorkItemKind `json:"kind"`
}

// PendingProposal is a recorded proposal awaiting the operator's decision,
// together with the conversation turn it came from, so an item created from it
// traces back to the intent that produced it.
type PendingProposal struct {
	// ID identifies the proposal within its conversation as turn.position, so
	// an operator can name one and the record says which turn it came from.
	ID             string   `json:"id"`
	ConversationID string   `json:"conversation_id"`
	Turn           int      `json:"turn"`
	Proposal       Proposal `json:"proposal"`
	// Asking is what about this proposal is being put to the operator: work
	// already in the tracker that this looks like, or the reason the harness would
	// not admit it itself in a project that admits work — the goal it names
	// resolved to nothing, or to a document nobody approved, or to one amended
	// since. It is empty where the answer is not about the proposal at all — a
	// project that asks about every work item, and a proposal that was admitted —
	// because a reason recorded there would be the policy repeated on every card
	// rather than anything about the work.
	//
	// A resemblance is the exception to that last part and is recorded whatever
	// the policy is, because it is about the work rather than about the policy: it
	// is the same duplicate either way, and the operator deciding needs the
	// identifier of the item it already is.
	//
	// It is recorded with the proposal rather than worked out again at the
	// prompt: the answer depends on the goals as they stood when the proposal was
	// made, and the goals move.
	Asking string `json:"asking,omitempty"`
	// Lane is the lane label a program manager's creation named this proposal in,
	// and is empty for every proposal the product manager makes. It is the
	// harness's rather than the proposal's own field, so no proposal block can name
	// a label to be applied: it is set only where a lane creation the admission
	// gate refused is put to the operator instead, and an approval creates the item
	// in that lane.
	Lane string `json:"lane,omitempty"`
	// Asker is who asked for the work, decided when the proposal is made rather
	// than when it is decided: an operator approving at the console a proposal a
	// sweep made is deciding the sweep's proposal, not asking for the work. It is
	// empty on a proposal recorded before origins were, and the item admitted from
	// one then records no origin rather than a guessed one.
	Asker domain.WorkItemAsker `json:"asker,omitempty"`
}

// recorded is the proposal as the durable conversation keeps it, so a later
// process can put it back on the table. Everything an operator decides from is
// carried: what was proposed, which turn proposed it, and why it was not simply
// admitted.
func (p PendingProposal) recorded() runstate.PendingProposal {
	return runstate.PendingProposal{
		ID:            p.ID,
		Turn:          p.Turn,
		Title:         p.Proposal.Title,
		Description:   p.Proposal.Description,
		Rationale:     p.Proposal.Rationale,
		Goal:          p.Proposal.Goal,
		RelevantGoals: p.Proposal.RelevantGoals,
		Parent:        p.Proposal.Parent,
		Dependencies:  p.Proposal.Dependencies,
		Class:         string(p.Proposal.Class),
		Kind:          string(p.Proposal.Kind),
		Asking:        p.Asking,
		Lane:          p.Lane,
		Asker:         string(p.Asker),
	}
}

// restoredProposal is one recorded proposal put back on the table, in the
// conversation it was proposed in. It is the exact inverse of recorded: a
// proposal an operator decides in a later process has to be the one they were
// shown in an earlier one, down to the reason they are being asked at all.
func restoredProposal(conversationID string, recorded runstate.PendingProposal) PendingProposal {
	return PendingProposal{
		ID:             recorded.ID,
		ConversationID: conversationID,
		Turn:           recorded.Turn,
		Proposal: Proposal{
			Title:         recorded.Title,
			Description:   recorded.Description,
			Rationale:     recorded.Rationale,
			Goal:          recorded.Goal,
			RelevantGoals: recorded.RelevantGoals,
			Parent:        recorded.Parent,
			Dependencies:  recorded.Dependencies,
			Class:         domain.WorkItemClass(recorded.Class),
			Kind:          domain.WorkItemKind(recorded.Kind),
		},
		Asking: recorded.Asking,
		Lane:   recorded.Lane,
		Asker:  domain.WorkItemAsker(recorded.Asker),
	}
}

// CreatedItem is one work item the harness created from an approved proposal.
type CreatedItem struct {
	ProposalID string `json:"proposal_id"`
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
}

// proposalDocument is the payload shape of the fenced block. The block always
// carries a list, so proposing one item and proposing three are the same
// protocol rather than two.
type proposalDocument struct {
	Items []Proposal `json:"items"`
}

// ProposalError reports that a turn carried a proposal block the harness could
// not read. It is a distinct type because it is not a broken conversation: the
// turn completed, the answer is real, and the provider session is recorded, so
// a caller can report the unusable block and carry on talking. What is lost is
// whatever that block was trying to propose, which is why it is never silently
// treated as a reply that proposed nothing.
type ProposalError struct {
	Err error
}

func (e *ProposalError) Error() string {
	return "the Lead Product Manager proposed work the harness cannot read: " + e.Err.Error()
}

func (e *ProposalError) Unwrap() error { return e.Err }

// extractProposals splits a reply into the prose the operator reads and the
// proposals the turn carried. Proposals come only from the fenced block: no
// amount of prose describing work is a proposal, and a block the contract does
// not accept is an error rather than a silently dropped suggestion.
func extractProposals(reply string) (string, []Proposal, error) {
	prose, payload, found, err := splitFencedBlock(reply, proposalFence, "proposal")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return strings.TrimSpace(reply), nil, nil
	}
	proposals, err := decodeProposals(payload)
	if err != nil {
		return "", nil, err
	}
	return prose, proposals, nil
}

// splitFencedBlock returns the reply's prose and the payload of its one block of
// the named kind. A second block of the same kind is refused: a reply an
// operator reads carries one list of that kind in it, not a visible list and
// another one further down. The kind is named in every failure, because a reply
// may carry a block of more than one kind and the operator has to be told which
// one could not be read.
func splitFencedBlock(reply, fence, kind string) (prose string, payload string, found bool, err error) {
	opensAt := indexFence(reply, fence)
	if opensAt < 0 {
		return "", "", false, nil
	}
	rest := reply[opensAt+len(fence):]
	if line := rest[:lineEnd(rest)]; strings.TrimSpace(line) != "" {
		return "", "", false, fmt.Errorf("%s block opens with trailing text %q", kind, strings.TrimSpace(line))
	}
	rest = rest[lineEnd(rest):]
	closesAt := strings.Index(rest, "\n```")
	if closesAt < 0 {
		return "", "", false, fmt.Errorf("%s block is not closed", kind)
	}
	// Whatever shares the closing fence's line belongs to the fence; prose
	// resumes on the line after it.
	after := rest[closesAt+len("\n```"):]
	after = after[lineEnd(after):]
	if indexFence(after, fence) >= 0 {
		return "", "", false, fmt.Errorf("a reply carries at most one %s block", kind)
	}
	prose = strings.TrimSpace(reply[:opensAt] + "\n" + after)
	return prose, rest[:closesAt], true, nil
}

// indexFence finds a fence that opens its own line, so a fence quoted inside
// prose is text rather than a block boundary.
func indexFence(text, fence string) int {
	if strings.HasPrefix(text, fence) {
		return 0
	}
	if at := strings.Index(text, "\n"+fence); at >= 0 {
		return at + 1
	}
	return -1
}

func lineEnd(text string) int {
	if at := strings.IndexByte(text, '\n'); at >= 0 {
		return at + 1
	}
	return len(text)
}

// decodeProposals strictly decodes the block payload. Unknown fields, trailing
// content, and oversized input are rejected rather than tolerated: what the
// operator is asked to approve has to be exactly what the product manager sent.
func decodeProposals(payload string) ([]Proposal, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode work item proposals: the proposal block is empty")
	}
	if len(trimmed) > MaxProposalBytes {
		return nil, fmt.Errorf("decode work item proposals: block is %d bytes, limit is %d", len(trimmed), MaxProposalBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var document proposalDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode work item proposals: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode work item proposals: unexpected trailing content after the proposals")
	}
	if len(document.Items) == 0 {
		return nil, errors.New("decode work item proposals: a proposal block must propose at least one work item")
	}
	if len(document.Items) > MaxProposalsPerTurn {
		return nil, fmt.Errorf("decode work item proposals: %d work items proposed in one reply, limit is %d", len(document.Items), MaxProposalsPerTurn)
	}
	var problems []error
	for i, proposal := range document.Items {
		if err := proposal.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("items[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid work item proposals: %w", errors.Join(problems...))
	}
	return document.Items, nil
}

// Validate reports every contract violation in the proposal at once.
func (p Proposal) Validate() error {
	var problems []error
	switch title := strings.TrimSpace(p.Title); {
	case title == "":
		problems = append(problems, errors.New("title is required"))
	case len(title) > maxProposalTitleBytes:
		problems = append(problems, fmt.Errorf("title is %d bytes, limit is %d", len(title), maxProposalTitleBytes))
	case strings.ContainsAny(title, "\r\n"):
		// A title is one line in what the operator is shown before approving,
		// so a title that spans lines cannot dress itself up as more than a
		// title.
		problems = append(problems, errors.New("title cannot span lines"))
	}
	problems = append(problems, goal.ValidateRelevant(p.RelevantGoals))
	problems = append(problems, validateProposalText("description", p.Description))
	problems = append(problems, validateProposalText("rationale", p.Rationale))
	switch named := strings.TrimSpace(p.Goal); {
	case named == "":
		problems = append(problems, errors.New("goal is required; work that serves no goal is raised as a concern rather than proposed"))
	case len(named) > goal.MaxStatementBytes:
		problems = append(problems, fmt.Errorf("goal is %d bytes, limit is %d", len(named), goal.MaxStatementBytes))
	case strings.ContainsAny(named, "\r\n"):
		problems = append(problems, errors.New("goal cannot span lines"))
	}
	if parent := strings.TrimSpace(p.Parent); parent != "" {
		if err := beads.ValidateIssueID(parent); err != nil {
			problems = append(problems, fmt.Errorf("parent: %w", err))
		}
	}
	// A class the harness does not recognize claims an exemption that does not
	// exist, so the proposal is refused rather than admitted as ordinary work
	// under a word nothing reads. The empty class is not a claim at all.
	if p.Class != "" && !p.Class.Valid() {
		problems = append(problems, fmt.Errorf("class %q is not one the harness recognizes; the classes there are: %s", p.Class, namedWorkItemClasses()))
	}
	problems = append(problems, kindProblem(p.Kind))
	// A grant naming a path the provider refuses is caught here rather than by the
	// run it would be admitted for. The harness's grant lifts the harness's own
	// refusal and never a provider's, and the difference between the two is
	// measured in repair rounds.
	//
	// The two fields read are the two a created item carries. The rationale is
	// excluded because it goes into the item's notes, which is not a field a grant
	// is read from; the design guidance and the acceptance criteria are absent
	// rather than excluded, because a proposal cannot set either and nothing in
	// the harness ever does. A grant written into one of those with the tracker's
	// own command is caught by the run instead, which reads all four before it
	// claims the item — see orchestrator.refuseProviderGrant, which asks this same
	// predicate.
	problems = append(problems, protectedpath.GrantProblems(p.Title, p.Description)...)
	seen := make(map[string]struct{}, len(p.Dependencies))
	for i, dependency := range p.Dependencies {
		trimmed := strings.TrimSpace(dependency)
		if err := beads.ValidateIssueID(trimmed); err != nil {
			problems = append(problems, fmt.Errorf("dependencies[%d]: %w", i, err))
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			problems = append(problems, fmt.Errorf("dependencies[%d]: %s is listed twice", i, trimmed))
			continue
		}
		seen[trimmed] = struct{}{}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid work item proposal: %w", err)
	}
	return nil
}

// kindProblem is the refusal of an admission that does not say whether its work
// is a bug fix or a feature, or says it in a word that is neither. It names the
// kind as what is missing, because that is the one thing the admission has to
// add to be accepted.
func kindProblem(kind domain.WorkItemKind) error {
	switch {
	case kind == "":
		return fmt.Errorf("kind is missing: say whether this work is a bug fix or a feature, as %s", namedWorkItemKinds())
	case !kind.Valid():
		return fmt.Errorf("kind %q is not one the harness recognizes; say %s", kind, namedWorkItemKinds())
	}
	return nil
}

// namedWorkItemKinds is every kind there is, quoted, in the words a refusal
// uses.
func namedWorkItemKinds() string {
	quoted := make([]string, 0, len(domain.WorkItemKinds))
	for _, kind := range domain.WorkItemKinds {
		quoted = append(quoted, fmt.Sprintf("%q", kind))
	}
	return strings.Join(quoted, " or ")
}

// kindLabel is how an operator deciding a proposal is told its kind, and says
// so in words where a proposal recorded before kinds were required has none.
func kindLabel(kind domain.WorkItemKind) string {
	if kind == "" {
		return "not recorded"
	}
	return string(kind)
}

// issueType is the tracker type the item admitted from this proposal is
// created with: its kind, or the untyped "task" for a proposal recorded before
// kinds were required, which the survey then names as having none.
func (p Proposal) issueType() string {
	if p.Kind.Valid() {
		return string(p.Kind)
	}
	return untypedIssueType
}

func validateProposalText(field, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(trimmed) > maxProposalTextBytes {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), maxProposalTextBytes)
	}
	return nil
}

// ProposalPlacementError reports that a turn proposed work placed against items
// the tracker does not hold. Like ProposalError it is not a broken
// conversation: the turn completed, the answer is real, and nothing was
// created. It is a separate failure because it is a different thing to be told —
// the block was perfectly readable, and what it named does not exist.
type ProposalPlacementError struct {
	Err error
}

func (e *ProposalPlacementError) Error() string {
	return "the Lead Product Manager proposed work placed against items that do not exist: " + e.Err.Error()
}

func (e *ProposalPlacementError) Unwrap() error { return e.Err }

// verifyProposalReferences checks that every item the proposals are placed
// against is one the tracker actually holds. Validation alone only says an
// identifier is well formed, which is how a proposal naming an item nobody ever
// created reached the operator looking exactly like one that did. The check runs
// before the operator is asked rather than at approval, because an approval that
// then fails has already spent the decision it was asking for.
func (s *Session) verifyProposalReferences(ctx context.Context, proposals []Proposal) error {
	referenced := referencedItems(proposals)
	if len(referenced) == 0 {
		return nil
	}
	if s.options.Tracker == nil {
		return fmt.Errorf("no work tracker is configured, so %s could not be checked", strings.Join(referenced, ", "))
	}
	var problems []error
	for _, id := range referenced {
		if _, err := s.options.Tracker.Show(ctx, id); err != nil {
			// A tracker that refused the lookup is reported the same way as one
			// that answered "no such item": either way the reference is unverified,
			// and unverified is not what the operator is asked to approve.
			problems = append(problems, fmt.Errorf("%s could not be confirmed: %w", id, err))
		}
	}
	return errors.Join(problems...)
}

// ProposalGoalError reports that a turn proposed work under a goal the
// repository does not record. Like the errors beside it the conversation is
// intact and nothing was created; what is wrong is the one thing the operator
// would have been relying on, which is that approving the work approves
// something that serves an agreed goal.
type ProposalGoalError struct {
	Err error
}

func (e *ProposalGoalError) Error() string {
	return "the Lead Product Manager proposed work under goals the repository does not record: " + e.Err.Error()
}

func (e *ProposalGoalError) Unwrap() error { return e.Err }

// verifyProposalGoals checks that every proposal names a goal the repository
// actually records. Validation alone only says a goal was named, which is how a
// proposal serving a goal nobody agreed reaches the operator looking exactly
// like one that serves an approved one. It runs before the operator is asked
// rather than at approval, for the same reason the placement check does: an
// approval that then fails has already spent the decision it was asking for.
//
// A repository with no goals to check against is not a proposal problem. The
// creation that follows says the goal went unchecked, and refusing to propose
// anything until the goals exist would leave a new project unable to plan its
// way to writing them.
func (s *Session) verifyProposalGoals(proposals []Proposal) error {
	if _, uncheckable := s.options.Goals.Uncheckable(); uncheckable {
		return nil
	}
	var problems []error
	for _, proposal := range proposals {
		if _, err := s.options.Goals.ResolveRelevant(proposal.RelevantGoals); err != nil {
			problems = append(problems, fmt.Errorf("%q: %w", proposal.Title, err))
		}
		attribution := s.options.Goals.Attribute(proposal.Goal)
		if attribution.State != goal.StateUnresolved {
			continue
		}
		problems = append(problems, fmt.Errorf("%q serves %q, and %s",
			strings.TrimSpace(proposal.Title), attribution.Named, attribution.Reason))
	}
	return errors.Join(problems...)
}

// referencedItems lists every existing item the proposals name, once each and in
// a stable order, so a turn placing three items under one parent asks the
// tracker about it once and a failure always reads the same way.
func referencedItems(proposals []Proposal) []string {
	seen := make(map[string]struct{})
	var referenced []string
	for _, proposal := range proposals {
		for _, id := range append([]string{strings.TrimSpace(proposal.Parent)}, proposal.dependencies()...) {
			if id == "" {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			referenced = append(referenced, id)
		}
	}
	sort.Strings(referenced)
	return referenced
}

// dependencies returns the proposal's dependencies as validated identifiers.
func (p Proposal) dependencies() []string {
	trimmed := make([]string, 0, len(p.Dependencies))
	for _, dependency := range p.Dependencies {
		trimmed = append(trimmed, strings.TrimSpace(dependency))
	}
	return trimmed
}

// Render describes one proposal for an operator who is about to decide on it.
// Everything in it came from the provider, so every line is indented under the
// proposal's own identifier and nothing is printed at the margin where the
// harness speaks.
func (p PendingProposal) Render() string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "[%s] %s\n", p.ID, strings.TrimSpace(p.Proposal.Title))
	for _, line := range p.body() {
		rendered.WriteString(indent(line))
	}
	return rendered.String()
}

// body is what the operator reads under a proposal's heading, whichever shape
// it is shown in. It is one list rather than a rendering, so a proposal put in a
// card and a proposal printed as plain text can never come to say different
// things about the same work.
func (p PendingProposal) body() []string {
	lines := strings.Split(strings.TrimSpace(p.Proposal.Description), "\n")
	lines = append(lines, "why: "+strings.TrimSpace(p.Proposal.Rationale))
	// The goal is shown beside the reasoning, because what the operator is
	// deciding is whether this work serves the product rather than whether the
	// sentence describing it reads well.
	lines = append(lines, "goal: "+strings.TrimSpace(p.Proposal.Goal))
	lines = append(lines, "relevant goals: "+(goal.Set{}).DescribeRelevant(p.Proposal.RelevantGoals))
	lines = append(lines, "kind: "+kindLabel(p.Proposal.Kind))
	if parent := strings.TrimSpace(p.Proposal.Parent); parent != "" {
		lines = append(lines, "parent: "+parent)
	}
	if dependencies := p.Proposal.dependencies(); len(dependencies) > 0 {
		lines = append(lines, "depends on: "+strings.Join(dependencies, ", "))
	}
	// A proposal made in a program manager's lane names the lane, because the
	// item it creates is admitted into it and the operator is deciding that too.
	if lane := strings.TrimSpace(p.Lane); lane != "" {
		lines = append(lines, "lane: "+lane)
	}
	// Why this one is being decided rather than admitted, in a project that
	// admits work that traces to an approved goal. The operator is being asked
	// about it precisely because something did not hold, and being asked without
	// being told what would leave them deciding blind.
	if asking := strings.TrimSpace(p.Asking); asking != "" {
		lines = append(lines, "asking you because "+asking)
	}
	return lines
}

// provenanceNotes is what a created item records about where it came from. The
// conversation, the turn, and the proposal are named so the item traces back to
// the intent that produced it, and the rationale travels with it because the
// reasoning is not in the description. So does the goal: an item in the queue
// that does not say what it is for is exactly the work nobody can later decide
// to stop doing.
//
// authority says what put it in the queue, and it is a parameter because there
// are now two answers and they must never be written down as one. An item the
// operator approved says so; an item admitted on the strength of an approved
// goal says that instead, and says which document. An item claiming an approval
// nobody gave would be the record of the one thing this arrangement has to be
// able to prove it did not do.
//
// The goals are a parameter for a duller reason: the attribution is written down
// as the goal's identity where the goal it names carries one, and only the goals
// the repository records say what that identity is. A proposal names the goal in
// the words the product manager used, and those words are what the operator
// approved; what the item records is the goal they picked out.
//
// proposer is who proposed it, because a program manager's lane creation reaches
// the operator as a proposal too, and agent is the instance whose lane it is in.
// A lane proposal's item records the lane and the instance as a lane admission's
// does.
// origin is what an item admitted from this proposal records about where it
// came from: who asked, as the proposal recorded it, and the role that proposed
// it as the one that admitted it, whoever then decided it. A proposal that
// recorded no asker gives no origin.
func (p PendingProposal) origin(proposer domain.AgentRole) domain.WorkItemOrigin {
	if p.Asker == "" {
		return domain.WorkItemOrigin{}
	}
	return domain.WorkItemOrigin{Asker: p.Asker, AdmittedBy: proposer}
}

func (p PendingProposal) provenanceNotes(authority string, goals goal.Set, proposer domain.AgentRole, agent string) string {
	notes := fmt.Sprintf(
		"Proposed by the %s in conversation %s, turn %d, proposal %s, and %s.\n\n%s\n\nRationale: %s",
		RoleTitle(proposer), p.ConversationID, p.Turn, p.ID, authority, goals.NoteFor(p.Proposal.Goal), strings.TrimSpace(p.Proposal.Rationale),
	)
	if lane := strings.TrimSpace(p.Lane); lane != "" {
		notes += fmt.Sprintf("\n\nLane: %s, admitted by the program manager instance %s.", lane, strings.TrimSpace(agent))
	}
	return notes
}

func indent(text string) string {
	var indented strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		indented.WriteString("    " + strings.TrimSpace(line) + "\n")
	}
	return indented.String()
}
