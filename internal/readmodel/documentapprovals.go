package readmodel

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DocumentApproval is one document an owning role wrote in a conversation that
// waits for the operator to approve or decline it: which document, the role and
// agent that wrote it and the conversation it was written in, what the write
// does, and since when it has waited. It is read off the conversation's saved
// record, which holds a document only until it is decided, so an approval or a
// decline takes it off the line by itself.
type DocumentApproval struct {
	WriteID        string           `json:"write_id"`
	ConversationID string           `json:"conversation_id"`
	Agent          string           `json:"agent"`
	Owner          domain.AgentRole `json:"owner"`
	// Action is "create" or "revise", Document the document's identifier, and
	// Kind, Title, and Directory what the write names of it, empty where a
	// revision leaves them alone.
	Action    string `json:"action"`
	Document  string `json:"document"`
	Kind      string `json:"kind,omitempty"`
	Title     string `json:"title,omitempty"`
	Directory string `json:"directory,omitempty"`
	Reason    string `json:"reason"`
	// ProductIntent is a document stating the product's intent: the brief, the
	// goals and non-goals, and the operating rules.
	ProductIntent bool `json:"product_intent,omitempty"`
	Turn          int  `json:"turn"`
	// WrittenAt is when the role wrote it, absent on a record kept before the
	// moment was.
	WrittenAt time.Time `json:"written_at,omitzero"`
}

// documentApprovalID is what identifies a waiting document across every
// conversation: a write's own identifier is unique only within the
// conversation that wrote it.
func documentApprovalID(conversationID, writeID string) string {
	return conversationID + "/" + writeID
}

// Says is the waiting document in the words the needs-a-human line prints.
func (d DocumentApproval) Says() string {
	what := "a revision of " + d.Document
	if d.Action == string(artifact.WriteCreate) {
		what = "a new document " + d.Document
		if d.Kind != "" {
			what = fmt.Sprintf("a new %s document %s", d.Kind, d.Document)
		}
	}
	if d.Title != "" {
		what += fmt.Sprintf(" (%q)", d.Title)
	}
	since := "since a time its record does not hold"
	if !d.WrittenAt.IsZero() {
		since = "since " + localMoment(d.WrittenAt)
	}
	return fmt.Sprintf("document %s, %s written by the %s in conversation %s, waits for the operator's approval, %s",
		d.WriteID, what, d.Owner.Title(), d.ConversationID, since)
}

// command is what the operator types to approve or decline it, as the
// conversation it waits in is reached.
func (d DocumentApproval) command() string {
	reach := "yoyo agent chat " + d.Agent
	if d.Owner == domain.RoleProductManager && d.Agent == string(domain.RoleProductManager) {
		reach = "yoyo chat"
	}
	return fmt.Sprintf("`%s --message \"approve %s\"` approves it and `%s --message \"decline %s <reason>\"` declines it", reach, d.WriteID, reach, d.WriteID)
}

// whose is whose move the waiting document is and what settles it.
func (d DocumentApproval) whose(mover Mover) string {
	if mover.IsOperator() {
		return mover.Possessive() + " — it states the product's intent, and a change to it is his to approve; " + d.command() + ", and it leaves this line once he has"
	}
	return mover.Possessive() + " — the project's approval setting for this kind of document asks for a person's approval, which no role may give, so it waits for the operator: " + d.command() + ", and it leaves this line once he has"
}

// documentApprovalAttention is a waiting document as the attention line carries
// it.
func documentApprovalAttention(waiting DocumentApproval) Attention {
	return resolved(Attention{
		Kind:             AttentionDocumentApproval,
		ID:               documentApprovalID(waiting.ConversationID, waiting.WriteID),
		DocumentApproval: &waiting,
	})
}

// readDocumentApprovals is every document waiting for the operator's approval,
// across every recorded conversation, longest waiting first. A document the
// conversation keeps is waiting on him unless a reviewed run is carrying it,
// which is the harness's, or its owner has held it for sending, which is the
// owner's until the item its hold names closes (chat.Session.Writes reads the
// same two the same way).
func readDocumentApprovals(sources Sources) ([]Attention, string) {
	if sources.Conversations == nil {
		return nil, ""
	}
	recorded, err := sources.Conversations.Recorded()
	if err != nil {
		return nil, fmt.Sprintf("the documents waiting for the operator's approval could not be read: %v", err)
	}
	var waiting []DocumentApproval
	for _, conversation := range recorded {
		agent := conversation.Agent
		if agent == "" {
			agent = string(conversation.Role)
		}
		for _, write := range conversation.PendingWrites {
			if write.Publication != nil || write.Held() {
				continue
			}
			waiting = append(waiting, DocumentApproval{
				WriteID: write.ID, ConversationID: conversation.ConversationID, Agent: agent, Owner: conversation.Role,
				Action: write.Action, Document: strings.TrimSpace(write.Artifact), Kind: write.Kind,
				Title: strings.TrimSpace(write.Title), Directory: strings.TrimSpace(write.Directory),
				Reason: strings.TrimSpace(write.Reason), ProductIntent: productIntent(conversation.Role, write),
				Turn: write.Turn, WrittenAt: write.WrittenAt,
			})
		}
	}
	// A record holding no moment sorts last rather than first: it is not known
	// to be the oldest.
	sort.SliceStable(waiting, func(i, j int) bool {
		first, second := waiting[i].WrittenAt, waiting[j].WrittenAt
		if first.IsZero() != second.IsZero() {
			return second.IsZero()
		}
		if !first.Equal(second) {
			return first.Before(second)
		}
		return documentApprovalID(waiting[i].ConversationID, waiting[i].WriteID) < documentApprovalID(waiting[j].ConversationID, waiting[j].WriteID)
	})
	attention := make([]Attention, 0, len(waiting))
	for _, each := range waiting {
		attention = append(attention, documentApprovalAttention(each))
	}
	return attention, ""
}

// productIntent reports a write of the product's intent. A role files a
// document only in its kind's home, so the kind decides; a revision may name
// none, and then it is the product's intent where the role that wrote it is
// the one that owns the brief, since that role owns no other kind.
func productIntent(role domain.AgentRole, write runstate.PendingWrite) bool {
	if kind := strings.TrimSpace(write.Kind); kind != "" {
		return artifact.IntentKind(artifact.Kind(kind))
	}
	owner, known := artifact.Owner(artifact.KindBrief)
	return known && owner == role
}
