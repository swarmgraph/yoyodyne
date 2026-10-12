package readmodel

// What waits on the operator's own approve-or-decline, read once for every
// surface that puts it to him.
//
// Three records hold a decision only he makes, and each already has a verb that
// makes it: a change proposed to a document, decided by `yoyo amendment approve`
// or `decline`; a document an owning role wrote in its conversation, decided by
// "approve document-4.1" sent to that conversation; and work an agent proposed
// in its conversation and stopped to ask about, decided by "approve 3.1" sent
// the same way. The dashboard puts these to him one at a time and carries out
// the same verbs, so the list is derived here from the amendment log and the
// conversation records and nowhere else — the page reads it, and a second
// derivation in the page would be a second answer to "what is waiting on me".
//
// What is not here is everything on the attention line that no verb approves
// or declines: a hold is released, a directive resolved, a step a person took
// recorded. Those stay on the line, and the page offers no button for them.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
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
)

// DecisionKinds is the whole vocabulary.
func DecisionKinds() []DecisionKind {
	return []DecisionKind{DecisionAmendment, DecisionDocument, DecisionProposal}
}

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
}

// Key names one decision across its kind, the way the dashboard addresses it.
func (d OperatorDecision) Key() string {
	return string(d.Kind) + ":" + d.Agent + ":" + d.ID
}

// ReadOperatorDecisions lists what waits on the operator's approve or decline:
// every undecided proposed change, oldest first, and then every undecided
// document and proposed work in each recorded conversation, conversation by
// conversation in agent order. A source that could not be read is named in the
// problem and costs only its own part of the list.
func ReadOperatorDecisions(sources Sources) ([]OperatorDecision, string) {
	var decisions []OperatorDecision
	var problems []string
	if sources.Amendments != nil {
		records, err := sources.Amendments.List()
		if err != nil {
			problems = append(problems, fmt.Sprintf("the proposed changes could not be read: %v", err))
		} else {
			recommended := map[string]RecommendedAmendment{}
			if sources.Sweeps != nil {
				if sweeps, _, err := sources.Sweeps.List(); err == nil {
					for _, each := range RecommendedAmendments(sweeps, records) {
						recommended[each.Proposal] = each
					}
				}
			}
			for _, proposal := range amendment.Pending(records) {
				decision := amendmentDecision(proposal)
				if each, found := recommended[proposal.ID]; found {
					decision.Recommendation = &each
				}
				decisions = append(decisions, decision)
			}
		}
	}
	if sources.Conversations != nil {
		conversations, err := sources.Conversations.Recorded()
		if err != nil {
			problems = append(problems, fmt.Sprintf("the conversations could not be read: %v", err))
		} else {
			sort.SliceStable(conversations, func(i, j int) bool {
				return conversations[i].Identity().Agent < conversations[j].Identity().Agent
			})
			for _, conversation := range conversations {
				decisions = append(decisions, conversationDecisions(conversation)...)
			}
		}
	}
	return decisions, strings.Join(problems, "; ")
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

func conversationDecisions(conversation runstate.Conversation) []OperatorDecision {
	identity := conversation.Identity()
	var decisions []OperatorDecision
	for _, write := range conversation.PendingWrites {
		// A document confirmed for publication waits on its run, and one held
		// for sending is its owner's to release: neither is the operator's to
		// decide, which is what the conversation itself says of them.
		if write.Publication != nil || write.Held() {
			continue
		}
		subject := strings.TrimSpace(write.Title)
		if subject == "" {
			subject = write.Artifact
		}
		decision := OperatorDecision{
			Kind: DecisionDocument, ID: write.ID, Label: DecisionDocument.Label(),
			Role: identity.Role, Agent: identity.Agent, Owner: identity.Role, ConversationID: conversation.ConversationID,
			Subject: subject, Detail: write.Body, Why: write.Reason,
			AskRole: identity.Role, AskAgent: identity.Agent,
		}
		decision.Says = fmt.Sprintf("the %s wrote %s (%s) in conversation %s, and it waits on your approval", identity.Role.Title(), write.ID, subject, conversation.ConversationID)
		decisions = append(decisions, decision)
	}
	for _, proposal := range conversation.PendingProposals {
		decision := OperatorDecision{
			Kind: DecisionProposal, ID: proposal.ID, Label: DecisionProposal.Label(),
			Role: identity.Role, Agent: identity.Agent, ConversationID: conversation.ConversationID,
			Subject: proposal.Title, Detail: proposal.Description, Why: proposal.Rationale, Asking: proposal.Asking,
			AskRole: identity.Role, AskAgent: identity.Agent,
		}
		decision.Says = fmt.Sprintf("the %s proposed work %s (%s) in conversation %s, and it waits on your decision", identity.Role.Title(), proposal.ID, proposal.Title, conversation.ConversationID)
		decisions = append(decisions, decision)
	}
	return decisions
}
