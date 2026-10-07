package chat

// What the operator is asked about before work reaches the queue.
//
// The harness began by asking about every work item, because there was nothing
// else it could honestly do: the product manager proposed items directly, and
// the operator was the only thing standing between a proposal and the tracker.
// That is still what a project gets until it says otherwise, and it is the gate
// this can move: a project that sets `approvals.work_items` to automatic has the
// operator approve a goal instead, and work that traces to one they approved is
// admitted without a further prompt. Which of the two is in force is the
// project's to say, and this file holds to whichever it said.
//
// Approval moved up a level; it did not disappear, and three things still stop
// and ask. Work that traces to no goal and work that would cut against one are
// raised as concerns rather than proposed at all, so they never reach here. A
// change to the goals themselves is the operator's and reaches the queue
// through nothing: the product manager may argue for one in prose and cannot
// make one. What is left for this file is the fourth case — work that names a
// goal — and the question it answers is whether anybody agreed to that goal.
//
// A project may also narrow its own gate rather than move it, with
// `approvals.work_item_exemptions`. That is a second question this file answers
// and it is answered first, because it is the operator saying a kind of work is
// not what they are asking to be asked about. It exempts nothing until they
// write one down, it never exempts work from naming a goal the repository
// records, and the only class there is describes work that changes nothing —
// which is what makes it a narrowing rather than the gate coming down.
//
// What it narrows is the per-item question and only that. A project that has
// already moved that approval up to its goals has no per-item question left for
// an exemption to stand down, so an exempt class there clears the approved-goal
// requirement below exactly as every other item does. An exemption that reached
// past the per-item question would be admitting work under a goal nobody
// approved, which is the paragraph after this one being false.
//
// The whole arrangement rests on that question being answered honestly rather
// than asserted. Moving the gate up only moves it if work cannot reach the
// queue without demonstrably serving an approved goal, so an attribution that
// resolves is not enough: it has to resolve to a goal in a document the
// operator approved, as that document now stands. Anything short of that is put
// to them, which is the gate staying exactly where it was for that item.
//
// # What stops an agent approving its own goal
//
// An approval records `by: operator` on the strength of who ran the command,
// and nothing in the record distinguishes the operator from anything else with a
// shell. That was harmless while the record gated nothing; here it is what lets
// work past, so the boundary has to be somewhere. It is not in the record, and
// it is not asserted in a prompt — it is the two things the harness already
// enforces deterministically.
//
// Conversation roles cannot write files: their adapter either disables tools
// or enforces read-only execution without escalation. Developer roles can write
// inside a run's worktree, and a run's change is compared against the
// protected paths before any check runs and before any reviewer sees it — the
// goals live in one of those homes, so an approval a developer wrote is refused
// with the rest of the diff and never reaches the repository the goals are read
// from. The exception is a path the work item grants, and a grant is read from
// the fields the operator and the managers write rather than from the notes a
// run fills in, so nothing can grant itself one.
//
// So the boundary is that an agent cannot write into the goals at all, rather
// than that an approval says who gave it. If either of those two enforcements
// is ever loosened, this is what was resting on them.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Admission is the project's `approvals.work_items` policy, in the terms this
// package thinks in. It is passed in rather than read here for the reason the
// artifact package's approval policy is: what a project asks the operator about
// is the project's decision, and this package's job is only to hold to it.
//
// Its zero value asks about everything. That is deliberate rather than
// incidental: a conversation assembled without a policy is one nobody stated a
// policy for, and the safe reading of no policy is the gate the harness started
// with rather than the one it is allowed to run without.
type Admission struct {
	WorkItems domain.ApprovalMode
	// Exempt are the classes of work this project admits without asking, whatever
	// WorkItems says. It is the operator's own narrowing of their gate: an
	// operator who wants to be asked about every change to the product does not
	// necessarily want to be asked before something reads the repository and
	// writes down what it found, and until they could say so the policy they had
	// recorded was a sentence nothing enforced.
	//
	// Its zero value exempts nothing, which is the gate exactly as the project
	// set it. An exemption is a decision the operator hands over, so it is
	// configured rather than defaulted.
	Exempt []domain.WorkItemClass
}

// PerItemApproval reports the operator being asked about every work item before
// it is admitted.
func (a Admission) PerItemApproval() bool { return a.WorkItems != domain.ApprovalAutomatic }

// Exempts reports work of this class being admissible without the operator
// being asked about the item. An unclassified item is never exempt: work that
// claims no class is ordinary work, and this is the one question where silence
// has to mean the gate rather than the exception.
func (a Admission) Exempts(class domain.WorkItemClass) bool {
	if class == "" {
		return false
	}
	for _, exempt := range a.Exempt {
		if exempt == class {
			return true
		}
	}
	return false
}

// ExemptClasses reports the classes this project exempts, in the order the
// harness names them, so what is said to an agent about its own project is the
// configured list rather than whatever order a file wrote it in.
func (a Admission) ExemptClasses() []domain.WorkItemClass {
	var exempt []domain.WorkItemClass
	for _, class := range domain.WorkItemClasses {
		if a.Exempts(class) {
			exempt = append(exempt, class)
		}
	}
	return exempt
}

// namedWorkItemClasses lists the classes a proposal or an action may claim, so
// a refusal names what was available rather than only what was wrong.
func namedWorkItemClasses() string {
	named := make([]string, 0, len(domain.WorkItemClasses))
	for _, class := range domain.WorkItemClasses {
		named = append(named, fmt.Sprintf("%q", class))
	}
	return strings.Join(named, ", ")
}

// namedWorkItemExecutors lists the executors an action may claim, so a refusal
// names what was available rather than only what was wrong.
func namedWorkItemExecutors() string {
	named := make([]string, 0, len(domain.WorkItemExecutors))
	for _, executor := range domain.WorkItemExecutors {
		named = append(named, fmt.Sprintf("%q", executor))
	}
	return strings.Join(named, ", ")
}

// perItemApprovalReason is why the operator is being asked about work that
// would otherwise have been admitted. It names the setting, because an operator
// who wants the other behavior has to be able to find the thing to change.
const perItemApprovalReason = `this project asks about every work item before it is admitted, as approvals.work_items is "human"`

// exemptsFromPerItemApproval reports the class the work claims standing the
// per-item question down. It is the one predicate both doors into the queue ask,
// rather than each deciding for itself: the product manager reaches the proposal
// path and the direct "create", and a carve-out that opened one of them wider
// than the other would be work arriving through whichever asked less — which is
// the failure the exemption was written not to be.
//
// It is false wherever there is no per-item question to stand down. A project
// that has moved that approval up to its goals admits on the approved goal, and
// an exemption reaching past that would admit work under a goal nobody approved.
func (s *Session) exemptsFromPerItemApproval(class domain.WorkItemClass) bool {
	return s.options.Admission.Exempts(class) && s.options.Admission.PerItemApproval()
}

// admissionGap says why work serving the named goal is not admitted to the
// queue without the operator being asked, and is empty exactly when it is. It
// is the one place that decision is made, so the proposal path and the tracker
// path can never come to answer it differently.
//
// The class the work claims is judged first, because an exemption is the
// operator saying this kind of work is not what their gate is for. What an
// exemption does not move is the goal: work admitted under one still names a
// goal the repository records, since an exemption is about who is asked and
// never about whether the work is for anything.
//
// What it narrows is the per-item gate and only that. Where a project asks about
// every item, an exemption is what stands the question down, and the goal has
// only to resolve. Where a project has already moved that approval up to its
// goals, the approved goal is the whole of what admits work, so the exemption
// has nothing left to narrow and takes nothing away: an exempt class under
// `work_items: automatic` clears exactly the gate every other item clears. An
// exemption that reached past the per-item question would be the carve-out
// admitting work under a goal nobody approved, which is the one thing this
// file's header says the arrangement rests on not happening.
//
// The goal an exemption does ask for is a resolved one, and nothing weaker. A
// goal the repository has nothing to check against is neither confirmed nor
// denied, so admitting on it would be admitting on a claim nobody could check —
// which is what the operator is asked about instead, whatever class the work
// claims. Every state that is not a resolved goal says why in its own words, so
// the refusal is the attribution's own reason rather than a sentence about the
// exemption.
func (s *Session) admissionGap(named string, class domain.WorkItemClass) string {
	attribution := s.options.Goals.Attribute(named)
	if s.exemptsFromPerItemApproval(class) {
		if !attribution.Resolved() {
			return attribution.Reason
		}
		return ""
	}
	if s.options.Admission.PerItemApproval() {
		return perItemApprovalReason
	}
	return attribution.ApprovalGap()
}

// classNote is what a directly created item records about the class it claimed,
// and is nothing where it claimed none or where the class changed nothing. It is
// written onto the item for the reason the goal is: the queue has to say why
// each thing in it was allowed in, and an item that reached it on a carve-out
// nobody can see afterwards is the one an operator cannot audit.
//
// A class that changed nothing includes one claimed where there is no per-item
// question for it to have stood down. The exemption is not what let that item in
// — the approved goal is, exactly as for every other item in a project that
// admits on its goals — and a note claiming the carve-out would name an
// authority that was not the one exercised. It is the same predicate the gate
// itself asks, so what an item records can never disagree with what admitted it.
func (s *Session) classNote(class domain.WorkItemClass) string {
	if !s.exemptsFromPerItemApproval(class) {
		return ""
	}
	return "\n\n" + exemptClassNote(class)
}

// exemptClassNote is the sentence a work item admitted under an exemption
// records about where its authority came from. It is deliberately neither of
// the other two: nobody approved this item, and no approved goal let it
// through — what let it through is the class of work the operator carved out,
// and an item that said otherwise would misdescribe the only decision anybody
// made about it.
func exemptClassNote(class domain.WorkItemClass) string {
	return fmt.Sprintf("admitted by the harness without asking the operator, because it is %s-class work, which this project exempts from per-item approval in approvals.work_item_exemptions",
		class)
}

// admissionAsker is who is asking for work this session admits, where nothing
// more particular — a cited report — says otherwise. A session a recurring pass
// opened is the role's own pass, with nobody speaking in it; any other session is
// a conversation the operator is speaking in, so what it admits the operator
// asked for.
func (s *Session) admissionAsker() domain.WorkItemAsker {
	if s.pass != "" {
		return domain.AskerSweep
	}
	return domain.AskerOperator
}

// creationOrigin is what a directly created item records about where it came
// from, and is the zero origin for a creation that admits nothing. The most
// particular record the admission cites is who asked, whatever session admitted
// it: a cited report first, because the report is the record the work answers;
// then a directive, which is the operator asking in their own words; and only
// where it cites neither, the session itself. A directive is also whose behalf
// the work is on, which is what it adds beside a report.
//
// It records and never decides: every input has already been checked by the
// time it is asked, and nothing here admits or refuses work.
func (s *Session) creationOrigin(verb creation, cited report.Report, prompting directive.Directive) domain.WorkItemOrigin {
	if !verb.admits {
		return domain.WorkItemOrigin{}
	}
	return admittedOrigin(s.admissionAsker(), s.state.Role, cited.ID, cited.Role, prompting.ID)
}

// admittedOrigin is the one rule for which record is who asked, shared by an
// admission as it is made and by the backfill reading one back out of an item's
// notes, so the two can never record the same admission differently.
func admittedOrigin(session domain.WorkItemAsker, admittedBy domain.AgentRole, reportID string, reportedBy domain.AgentRole, directiveID string) domain.WorkItemOrigin {
	origin := domain.WorkItemOrigin{Asker: session, AdmittedBy: admittedBy, Directive: strings.TrimSpace(directiveID)}
	switch {
	case strings.TrimSpace(reportID) != "":
		origin.Asker = domain.AskerReport
		origin.Report = strings.TrimSpace(reportID)
		origin.ReportedBy = reportedBy
	case origin.Directive != "":
		origin.Asker = domain.AskerOperator
	}
	return origin
}

// AdmittedItem is one work item the harness put in the queue without asking,
// because it serves a goal the operator approved or because it is of a class
// they carved out. It is reported for exactly that reason: autonomy the operator
// cannot see afterwards is indistinguishable from work happening behind their
// back, so what was admitted without a prompt is said out loud where a decision
// would have been, with which of the two let it in.
type AdmittedItem struct {
	ProposalID string `json:"proposal_id"`
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	// Goal is the goal the work traces to. It is recorded on every admitted item
	// whether or not the goal is what let it through, because an item in the queue
	// that does not say what it is for is exactly the work nobody can later decide
	// to stop doing.
	Goal          string   `json:"goal"`
	RelevantGoals []string `json:"relevant_goals,omitempty"`
	// Basis is why nobody was asked, in the same words the record and the item's
	// own notes use. It is carried rather than assumed, because there is more than
	// one answer: the operator approved the goal this serves, or they carved out
	// this class of work. An account that named the wrong one would be describing
	// a decision they did not make.
	Basis string `json:"basis,omitempty"`
}

// Render describes one admitted item for an operator reading what went into
// the queue without them. Everything in it but the identifiers came from the
// provider, so the goal and the title are indented under the harness's own
// line.
func (a AdmittedItem) Render() string {
	rendered := fmt.Sprintf("[%s] %s\n", a.WorkItemID, a.ProposalID) +
		indent(a.Title) + indent("goal: "+a.Goal)
	if len(a.RelevantGoals) > 0 {
		rendered += indent("relevant goals: " + strings.Join(a.RelevantGoals, "; "))
	}
	if a.Basis != "" {
		rendered += indent("admitted because " + a.Basis)
	}
	return rendered
}

// admitted is what the record keeps about work the harness admitted itself: the
// proposal it came from and what let it through, which is the approved goal it
// serves or the class the operator exempted. The reason is written down rather
// than left to be inferred from the policy in force at the time, because the
// policy is a file that changes and the record is not.
type admitted struct {
	PendingProposal
	Reason string `json:"reason"`
}

// admissionReason is what an admitted item's record says about why nobody was
// asked. The basis is a clause rather than a sentence, so the same words read
// the same way wherever they are said: in the record, and in the account of the
// admission the operator and the agent are both given.
func admissionReason(basis string) string {
	return "admitted without asking the operator, because " + basis
}

// approvedGoalBasis and exemptClassBasis are the two answers there are. They are
// never interchangeable: one says the operator approved what this work is for,
// and the other says they carved out this kind of work whatever it is for.
func approvedGoalBasis(named string) string {
	return fmt.Sprintf("it serves the approved goal %q", named)
}

func exemptClassBasis(class domain.WorkItemClass) string {
	return fmt.Sprintf("it is %s-class work this project exempts from per-item approval", class)
}

// admissionAuthority says what actually let one item into the queue with nobody
// asked: the clause every account of it uses, and the sentence written onto the
// item. Which of the two answers is true depends on the project rather than on
// the item, so it is decided here, once, rather than chosen at each of the
// places that write it down.
//
// The goal answers only where the project admits on that basis and the goal
// itself clears the gate. Anywhere else what let the item in is the class the
// operator carved out, and an item claiming an approved goal instead would be
// naming an authority that was not what authorized it.
func (s *Session) admissionAuthority(named string, class domain.WorkItemClass) (basis, note string) {
	attribution := s.options.Goals.Attribute(named)
	if !s.options.Admission.PerItemApproval() && attribution.ApprovalGap() == "" {
		return approvedGoalBasis(strings.TrimSpace(named)), approvedGoalNote(attribution)
	}
	return exemptClassBasis(class), exemptClassNote(class)
}

// approvedGoalNote is the sentence a work item admitted without a prompt
// records about where its authority came from. It is deliberately not the
// sentence an approved proposal records: an item that said the operator
// approved it when they were never asked would be the one lie this whole
// arrangement cannot afford.
func approvedGoalNote(attribution goal.Attribution) string {
	return fmt.Sprintf("admitted by the harness without asking the operator, because it serves a goal the operator approved in %s",
		attribution.Goal.ArtifactID)
}
