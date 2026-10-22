package readmodel

// Documents an owning role wrote in its conversation, waiting for approval
// before they are written, as the attention line carries the ones whose
// decision is the operator's.
//
// A document whose kind's approval policy is not automatic waits in its
// conversation's record until somebody approves or declines it there, and
// nothing outside the conversation said it was waiting: the operator learned
// of a goals revision by being in the conversation that wrote it. Who decides
// one is the ownership resolver's answer (ownership.ResolvePendingDocument),
// never this file's: it names the operator for a document in the product home
// and nobody for any other, and only the ones it names him for are put on the
// line. A document confirmed for publication waits on its reviewed run, and
// one its owner holds for sending is the owner's to release, so neither is
// here.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// PendingDocument is one document waiting for approval in a conversation: the
// conversation and the agent and role it is with, and the write whole — what
// it does to which document, the text, and why.
type PendingDocument struct {
	ConversationID string           `json:"conversation_id"`
	Agent          string           `json:"agent"`
	Role           domain.AgentRole `json:"role"`
	Turn           int              `json:"turn"`
	Action         string           `json:"action"`
	Artifact       string           `json:"artifact"`
	Kind           string           `json:"kind,omitempty"`
	Title          string           `json:"title,omitempty"`
	Body           string           `json:"body"`
	Reason         string           `json:"reason"`
}

// Subject is the document named for a person: its title where it has one, and
// its id otherwise.
func (d PendingDocument) Subject() string {
	if title := strings.TrimSpace(d.Title); title != "" {
		return title
	}
	return d.Artifact
}

// Says is the waiting document in one sentence, given the write's id.
func (d PendingDocument) Says(id string) string {
	return fmt.Sprintf("the %s wrote %s, which would %s %s (%s), in conversation %s, and it waits for approval",
		d.Role.Title(), id, d.verb(), d.Artifact, d.Subject(), d.ConversationID)
}

func (d PendingDocument) verb() string {
	if d.Action == "create" {
		return "create"
	}
	return "revise"
}

// ApproveCommand is the command that approves it at a terminal: the message
// the conversation's own verb reads, sent to that conversation.
func (d PendingDocument) ApproveCommand(id string) string {
	if d.Role == domain.RoleProductManager && (d.Agent == "" || d.Agent == string(d.Role)) {
		return fmt.Sprintf("`yoyo chat --message \"approve %s\"`", id)
	}
	return fmt.Sprintf("`yoyo agent chat %s --message \"approve %s\"`", d.Agent, id)
}

// readPendingDocuments reads every conversation's waiting documents and puts
// on the line the ones the ownership resolver names an owner for, conversation
// by conversation in agent order and in the order each was written.
func readPendingDocuments(sources Sources) ([]Attention, string) {
	if sources.Conversations == nil {
		return nil, ""
	}
	conversations, err := sources.Conversations.Recorded()
	if err != nil {
		return nil, fmt.Sprintf("the documents waiting for approval could not be read: %v", err)
	}
	sort.SliceStable(conversations, func(i, j int) bool {
		return conversations[i].Identity().Agent < conversations[j].Identity().Agent
	})
	var waiting []Attention
	for _, conversation := range conversations {
		identity := conversation.Identity()
		mover, named := ownership.ResolvePendingDocument(identity.Role)
		if !named {
			continue
		}
		for _, write := range conversation.PendingWrites {
			if write.Publication != nil || write.Held() {
				continue
			}
			waiting = append(waiting, pendingDocumentAttention(conversation.ConversationID, identity, write, mover))
		}
	}
	return waiting, ""
}

func pendingDocumentAttention(conversationID string, identity runstate.ConversationIdentity, write runstate.PendingWrite, mover Mover) Attention {
	return Attention{
		Kind:  AttentionPendingDocument,
		ID:    write.ID,
		Mover: mover,
		PendingDocument: &PendingDocument{
			ConversationID: conversationID, Agent: identity.Agent, Role: identity.Role, Turn: write.Turn,
			Action: write.Action, Artifact: write.Artifact, Kind: write.Kind, Title: write.Title, Body: write.Body, Reason: write.Reason,
		},
	}
}
