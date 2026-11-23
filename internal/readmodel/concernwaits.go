package readmodel

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ConcernWait is one concern a role raised in its conversation that nobody has
// answered: work it would not propose because of what the goals admit, and the
// question it stopped to ask the operator about it. It is read off the
// conversation's saved record, which holds a concern only until it is answered,
// so an answer takes it off the line by itself.
type ConcernWait struct {
	ConcernID      string           `json:"concern_id"`
	ConversationID string           `json:"conversation_id"`
	Agent          string           `json:"agent"`
	Role           domain.AgentRole `json:"role"`
	// Kind is the concern contract's kind as the record keeps it: unplaceable,
	// conflict, or judgement (chat.ConcernKind owns the vocabulary).
	Kind     string   `json:"kind"`
	Subject  string   `json:"subject"`
	Goal     string   `json:"goal,omitempty"`
	Detail   string   `json:"detail"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Turn     int      `json:"turn"`
}

// concernWaitID is what identifies a waiting concern across every
// conversation: a concern's own identifier is unique only within the
// conversation that raised it.
func concernWaitID(conversationID, concernID string) string {
	return conversationID + "/" + concernID
}

func concernWait(conversation runstate.Conversation, agent string, concern runstate.PendingConcern) ConcernWait {
	return ConcernWait{
		ConcernID: concern.ID, ConversationID: conversation.ConversationID, Agent: agent, Role: conversation.Role,
		Kind: strings.TrimSpace(concern.Kind), Subject: strings.TrimSpace(concern.Subject), Goal: strings.TrimSpace(concern.Goal),
		Detail: strings.TrimSpace(concern.Detail), Question: strings.TrimSpace(concern.Question), Options: concern.Options,
		Turn: concern.Turn,
	}
}

// ConcernHeadline is what kind of concern a kind token is, in the words the
// conversation puts it to the operator in. The vocabulary is chat.ConcernKind's,
// which this package cannot import because chat reads the read model; a test
// in the chat package holds the two wordings equal.
func ConcernHeadline(kind string) string {
	switch kind {
	case "unplaceable":
		return "work it cannot place: it serves no goal it can find"
	case "conflict":
		return "work it says would cut against a goal"
	case "judgement":
		return "work that fits the goals, and that it judges to be against the product's intent"
	}
	return "a concern the harness does not recognize"
}

// headline is the concern's kind in words, and says so where the record names
// none.
func (c ConcernWait) headline() string {
	if c.Kind == "" {
		return "a concern whose record does not say which kind it is"
	}
	return ConcernHeadline(c.Kind)
}

// Says is the waiting concern in the words the needs-a-human line prints.
func (c ConcernWait) Says() string {
	subject := c.Subject
	if subject == "" {
		subject = "a subject its record does not hold"
	}
	return fmt.Sprintf("concern %s, %q, raised by the %s in conversation %s about %s, waits for the operator's answer",
		c.ConcernID, subject, c.Role.Title(), c.ConversationID, c.headline())
}

// reach is the command that reaches the conversation the concern waits in.
func (c ConcernWait) reach() string {
	if c.Role == domain.RoleProductManager && c.Agent == string(domain.RoleProductManager) {
		return "yoyo chat"
	}
	return "yoyo agent chat " + c.Agent
}

// answerCommand is what the operator types to answer it.
func (c ConcernWait) answerCommand() string {
	return fmt.Sprintf("%s --message \"answer %s <your answer>\"", c.reach(), c.ConcernID)
}

// whose is whose move the waiting concern is and what settles it.
func (c ConcernWait) whose(mover Mover) string {
	return mover.Possessive() + " — the " + c.Role.Title() + " stopped to ask rather than propose the work; `" + c.answerCommand() + "` answers it, and it leaves this line once he has"
}

// concernAttention is a waiting concern as the attention line carries it.
func concernAttention(waiting ConcernWait) Attention {
	return resolved(Attention{
		Kind:    AttentionConcern,
		ID:      concernWaitID(waiting.ConversationID, waiting.ConcernID),
		Concern: &waiting,
	})
}
