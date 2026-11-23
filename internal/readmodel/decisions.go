package readmodel

// What waits on the operator's own approve-or-decline, read once for every
// surface that puts it to him.
//
// Who owns a thing waiting is the attention line's answer, which every surface
// projects, and never decided here: this list is the line's own entries whose
// mover is the operator, narrowed to the ones a verb approves or declines and
// opened into the records they carry, so the dashboard can put each to him one
// at a time and carry out the same verb. Today that is three kinds. A proposed
// change whose entry on the line names him — a change to the brief or the
// goals that changes their intent or does not say — decided by `yoyo amendment
// approve` or `decline`; an ordinary proposed change is the owning role's, as
// its entry on the line says, and is not here. And a document an owning role
// wrote in its conversation, which the line carries as a waiting document
// (documentapprovals.go) whose mover the ownership registry names, decided by
// "approve document-4.1" sent to that conversation; only the ones whose mover
// is the operator are listed.
//
// And a concern a role raised in its conversation, which the line carries as
// a waiting concern (concernwaits.go) and the ownership registry names him for:
// it is answered rather than approved, with "answer c5.1 <answer>" sent to
// that conversation, so the dashboard shows it whole and offers the question
// to its role, and gives the command that answers it.
//
// Every decision listed carries its account (decisionaccount.go): what
// changes, why, what approving and declining lead to, and why it is his.
//
// One more kind has a verb and is named below, so the dashboard can carry it
// out the day the line names the operator for one: work proposed in a
// conversation, decided by "approve 3.1". The line has no entry for it today,
// so none is listed.
//
// What is not here is everything on the line that no verb approves or
// declines: a hold is released, a directive resolved, a step a person took
// recorded. Those stay on the line, and the page offers no button for them.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// DecisionKind is which of the three records an operator decision is about.
type DecisionKind string

const (
	// DecisionAmendment is a change proposed to a document, from the amendment
	// log. Its ID is the proposal's id.
	DecisionAmendment DecisionKind = "amendment"
	// DecisionDocument is a document an owning role wrote in its conversation and
	// the operator has not decided. Its ID is the write's, document-turn.position.
	DecisionDocument DecisionKind = "document"
	// DecisionProposal is work an agent proposed in its conversation and nobody
	// has decided. Its ID is the proposal's, turn.position.
	DecisionProposal DecisionKind = "proposal"
	// DecisionConcern is a question a role stopped to ask in its conversation
	// that nobody has answered. Its ID is the concern's, c-turn.position. It is
	// answered in that conversation rather than approved or declined, so the
	// dashboard shows it whole and offers the question to its role, and the
	// answer is given with the conversation's command.
	DecisionConcern DecisionKind = "concern"
)

// DecisionKinds is the whole vocabulary.
func DecisionKinds() []DecisionKind {
	return []DecisionKind{DecisionAmendment, DecisionDocument, DecisionProposal, DecisionConcern}
}

// Answered reports a kind the operator answers in its conversation rather
// than approves or declines.
func (k DecisionKind) Answered() bool { return k == DecisionConcern }

// Valid reports whether a token is one of the kinds.
func (k DecisionKind) Valid() bool {
	for _, known := range DecisionKinds() {
		if k == known {
			return true
		}
	}
	return false
}

// Label is the plain words a person reads for the kind.
func (k DecisionKind) Label() string {
	switch k {
	case DecisionAmendment:
		return "proposed document change"
	case DecisionDocument:
		return "document to approve"
	case DecisionProposal:
		return "proposed work"
	case DecisionConcern:
		return "question to answer"
	}
	return string(k)
}

// OperatorDecision is one thing waiting on the operator's approve or decline:
// what it is, who raised it, where it is decided, and whom to ask about it
// first.
type OperatorDecision struct {
	Kind DecisionKind `json:"kind"`
	ID   string       `json:"id"`
	// Label is Kind in the words the page prints.
	Label string `json:"label"`
	// Role is who raised it: the proposing role for a proposed change, and the
	// conversation's role for the other two. Agent is the configured agent whose
	// conversation a document or proposed work is decided in; a proposed change
	// is decided in no conversation and carries none.
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	// Owner is the role whose authority a proposed change is decided under,
	// which is the document's owner.
	Owner domain.AgentRole `json:"owner,omitempty"`
	// ConversationID is the conversation a document or proposed work is decided
	// in.
	ConversationID string `json:"conversation_id,omitempty"`
	// Subject is what it is about: the document a change is proposed to, the
	// document written, or the work proposed.
	Subject string `json:"subject"`
	// Detail is the thing itself: the change proposed, the document's body, or
	// the work's description. Why is the reason given for it.
	Detail string `json:"detail"`
	Why    string `json:"why,omitempty"`
	// Asking is why proposed work was put to the operator rather than admitted.
	Asking string `json:"asking,omitempty"`
	// WorkItemID is the item a proposed change's proposer was working on.
	WorkItemID string `json:"work_item_id,omitempty"`
	// RaisedAt is when a proposed change was raised. The conversation records
	// keep the turn rather than a time, so the other two carry none.
	RaisedAt *time.Time `json:"raised_at,omitempty"`
	// Recommendation is what the owning role argued on a recurring pass, where
	// it argued about a proposed change.
	Recommendation *RecommendedAmendment `json:"recommendation,omitempty"`
	// AskRole and AskAgent are whose conversation a question about it goes to:
	// the role that raised it. Where that agent cannot be reached in a
	// conversation, the question goes to the Lead Product Manager instead, and
	// the answer to the ask says so.
	AskRole  domain.AgentRole `json:"ask_role"`
	AskAgent string           `json:"ask_agent"`
	// DeclineNeedsReason says a decline is refused without a reason, which the
	// amendment log requires and the conversations do not.
	DeclineNeedsReason bool `json:"decline_needs_reason,omitempty"`
	// Says is the decision in one line, as a terminal would print it.
	Says string `json:"says"`
	// Account is what changes, why, what approving and declining each lead
	// to, and why it is the operator's, as every surface listing it says it
	// (DecisionAccount).
	Account DecisionAccount `json:"account"`
	// Answer is the command that answers a decision the operator answers
	// rather than approves or declines.
	Answer string `json:"answer,omitempty"`
}

// Key names one decision across its kind, the way the dashboard addresses it.
func (d OperatorDecision) Key() string {
	return string(d.Kind) + ":" + d.Agent + ":" + d.ID
}

// ReadOperatorDecisions lists what waits on the operator's approve or decline,
// and nothing the attention line does not already give him. Who owns an entry
// is never decided here: the list is the attention line's own entries for the
// proposed changes, whose mover is the operator, opened into the proposals they
// carry: a change to the brief or the goals that changes their intent or does
// not say, carrying what its owner argued about it on a recurring pass where it
// did. An ordinary proposal is the owning role's, as its entry says, and is not
// here. Then the line's waiting documents whose mover is the operator, opened
// into document decisions. Proposed work in a
// conversation has no entry on the line naming him, so none is listed.
// Proposals come oldest first, by the log's order. A source that could not be
// read is named in the problem rather than read as an empty list.
func ReadOperatorDecisions(sources Sources) ([]OperatorDecision, string) {
	documents, problem := readDocumentApprovals(sources)
	decisions, amendmentsProblem := readAmendments(sources, sources.now()).operatorDecisions(documents)
	return decisions, joinProblems(amendmentsProblem, problem)
}

// operatorDecisions is the list from one reading of the proposed changes and
// the attention line's entries, which the standing shares with the line it is
// derived from: of the entries, only the waiting documents whose mover is the
// operator are taken, and every other entry is passed over.
func (reading amendmentReading) operatorDecisions(line []Attention) ([]OperatorDecision, string) {
	var decisions []OperatorDecision
	for _, entry := range line {
		if !entry.Mover.IsOperator() {
			continue
		}
		var decision OperatorDecision
		switch {
		case entry.Kind == AttentionDocumentApproval && entry.DocumentApproval != nil:
			decision = documentDecision(*entry.DocumentApproval)
		case entry.Kind == AttentionConcern && entry.Concern != nil:
			decision = concernDecision(*entry.Concern)
		default:
			continue
		}
		decision.Account, _ = entry.Account()
		decisions = append(decisions, decision)
	}
	if reading.problem != "" {
		return decisions, reading.problem
	}
	recommended := map[string]RecommendedAmendment{}
	for _, each := range reading.recommended {
		recommended[each.Proposal] = each
	}
	theirs := map[string]Attention{}
	for _, entry := range reading.attention() {
		if entry.Mover.IsOperator() && entry.Amendment != nil {
			theirs[entry.ID] = entry
		}
	}
	var changes []OperatorDecision
	for _, proposal := range amendment.Pending(reading.records) {
		entry, found := theirs[proposal.ID]
		if !found {
			continue
		}
		decision := amendmentDecision(proposal)
		decision.Account, _ = entry.Account()
		if each, found := recommended[proposal.ID]; found {
			decision.Recommendation = &each
		}
		changes = append(changes, decision)
	}
	return append(changes, decisions...), reading.sweepProblem
}

// documentDecision is a waiting document as the dashboard puts it to the
// operator: decided in its conversation by the write's own id, and asked about
// in the same one.
func documentDecision(document DocumentApproval) OperatorDecision {
	subject := document.Title
	if subject == "" {
		subject = document.Document
	}
	return OperatorDecision{
		Kind: DecisionDocument, ID: document.WriteID, Label: DecisionDocument.Label(),
		Role: document.Owner, Agent: document.Agent, Owner: document.Owner, ConversationID: document.ConversationID,
		Subject: subject, Detail: document.Body, Why: document.Reason,
		AskRole: document.Owner, AskAgent: document.Agent,
		Says: document.Says(),
	}
}

// concernDecision is a waiting concern as the dashboard puts it to the
// operator: shown whole, answered in its conversation, and asked about in the
// same one.
func concernDecision(concern ConcernWait) OperatorDecision {
	detail := concern.Detail
	if concern.Question != "" {
		detail = strings.TrimSpace(detail + "\n\nIt asks: " + concern.Question)
	}
	for index, option := range concern.Options {
		detail += fmt.Sprintf("\n%d. %s", index+1, strings.TrimSpace(option))
	}
	return OperatorDecision{
		Kind: DecisionConcern, ID: concern.ConcernID, Label: DecisionConcern.Label(),
		Role: concern.Role, Agent: concern.Agent, ConversationID: concern.ConversationID,
		Subject: concern.Subject, Detail: detail, Why: concern.headline(),
		AskRole: concern.Role, AskAgent: concern.Agent,
		Says:   concern.Says(),
		Answer: concern.answerCommand(),
	}
}

// FindOperatorDecision is one decision by its kind, the agent whose
// conversation holds it (empty for a proposed change), and its id, read fresh.
// It is what an act is checked against before it is carried out, so a decision
// made since the page last read is refused rather than decided twice.
func FindOperatorDecision(sources Sources, kind DecisionKind, agent, id string) (OperatorDecision, bool, string) {
	decisions, problem := ReadOperatorDecisions(sources)
	for _, decision := range decisions {
		if decision.Kind == kind && decision.ID == id && (kind == DecisionAmendment || decision.Agent == agent) {
			return decision, true, problem
		}
	}
	return OperatorDecision{}, false, problem
}

func amendmentDecision(proposal amendment.Proposal) OperatorDecision {
	agent := proposal.Agent
	if agent == "" {
		agent = string(proposal.Role)
	}
	raised := proposal.RaisedAt
	decision := OperatorDecision{
		Kind:               DecisionAmendment,
		ID:                 proposal.ID,
		Label:              DecisionAmendment.Label(),
		Role:               proposal.Role,
		Owner:              proposal.Owner,
		Subject:            proposal.Artifact,
		Detail:             proposal.Change,
		Why:                proposal.Why,
		WorkItemID:         proposal.WorkItemID,
		RaisedAt:           &raised,
		AskRole:            proposal.Role,
		AskAgent:           agent,
		DeclineNeedsReason: true,
	}
	decision.Says = fmt.Sprintf("the %s proposes a change to %s, decided under the %s's authority", proposal.Role.Title(), proposal.Artifact, proposal.Owner.Title())
	return decision
}
