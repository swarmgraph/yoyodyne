package runstate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Holding a document whose automatic publication stopped, and the development
// manager's decision about it. This is point 7 of the document landing route
// (docs/designs/document-landing-route.md): after MaxDocumentReturns returned
// runs of one document in one conversation, the writer's next revision is kept
// on the conversation's record, unsent, until she releases it for one run or
// declines it. The decision is written on the revision itself, and the writer's
// conversation carries it out at its next message (chat's documentreturns.go).

// DocumentReturnsHold is why one revision is held after automatic publication
// of its document stopped, and what was decided about it.
type DocumentReturnsHold struct {
	HeldAt time.Time `json:"held_at"`
	// Why is which way out of the stop the revision did not meet.
	Why string `json:"why"`
	// LastRun is the run that last returned the document, where it is known.
	LastRun string `json:"last_run,omitempty"`
	// Decision is empty while the development manager has not decided, and Told
	// says the writer has been told of a release.
	Decision *DocumentReturnsDecision `json:"decision,omitempty"`
	Told     bool                     `json:"told,omitempty"`
}

// The two things the development manager decides about a held revision.
const (
	DocumentReturnsRelease = "release"
	DocumentReturnsDecline = "decline"
)

// DocumentReturnsDecision is what was decided about a held revision: release,
// which starts one reviewed run of it, or decline.
type DocumentReturnsDecision struct {
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason"`
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
}

// Validate refuses a decision that is not one of the two, or that does not say
// why, by whom, and when.
func (d DocumentReturnsDecision) Validate() error {
	if d.Decision != DocumentReturnsRelease && d.Decision != DocumentReturnsDecline {
		return fmt.Errorf("%q does not decide a held document; the decisions are %q and %q", d.Decision, DocumentReturnsRelease, DocumentReturnsDecline)
	}
	if strings.TrimSpace(d.Reason) == "" || strings.TrimSpace(d.DecidedBy) == "" || d.DecidedAt.IsZero() {
		return errors.New("a decision about a held document records why, by whom, and when it was decided")
	}
	if len(d.Reason) > MaxHandedBackReasonBytes {
		return fmt.Errorf("the reason is %d bytes, limit is %d", len(d.Reason), MaxHandedBackReasonBytes)
	}
	return nil
}

// Validate refuses a hold with no reason or an unusable decision.
func (h DocumentReturnsHold) Validate() error {
	if h.HeldAt.IsZero() || strings.TrimSpace(h.Why) == "" {
		return errors.New("a document held after returned runs records why and since when")
	}
	if h.Decision != nil {
		return h.Decision.Validate()
	}
	return nil
}

// AwaitsDevelopmentManager reports a revision held after returned runs that
// nobody has decided yet.
func (w PendingWrite) AwaitsDevelopmentManager() bool {
	return w.Publication == nil && w.ReturnsHold != nil && w.ReturnsHold.Decision == nil
}

// decideDocumentWait bounds how long a decision waits for the writer's
// conversation when somebody is mid-turn in it.
const decideDocumentWait = time.Minute

// DecideReturnedDocument records the development manager's decision on a held
// revision of one conversation and counts it as something that conversation is
// told, so its next pass is not skipped. It waits for a turn in progress there
// up to a bound, and then records nothing and says so.
func (s *ConversationStore) DecideReturnedDocument(ctx context.Context, conversationID, writeID string, decision DocumentReturnsDecision) (PendingWrite, error) {
	if err := decision.Validate(); err != nil {
		return PendingWrite{}, err
	}
	conversations, err := s.Recorded()
	if err != nil {
		return PendingWrite{}, err
	}
	var found *Conversation
	for i := range conversations {
		if conversations[i].ConversationID == conversationID {
			found = &conversations[i]
		}
	}
	if found == nil {
		return PendingWrite{}, fmt.Errorf("no recorded conversation %s holds a document to decide; nothing was recorded", conversationID)
	}
	waiting, cancel := context.WithTimeout(ctx, decideDocumentWait)
	defer cancel()
	hold, err := s.Claim(waiting, found.Identity())
	if errors.Is(err, ErrConversationHeld) {
		return PendingWrite{}, fmt.Errorf("the writer's conversation is in the middle of a turn, so nothing was recorded and the document stays on your list; decide it again later: %w", err)
	}
	if err != nil {
		return PendingWrite{}, err
	}
	defer hold.Release()
	current, err := s.Load(found.Identity())
	if err != nil {
		return PendingWrite{}, err
	}
	if current.ConversationID != conversationID {
		return PendingWrite{}, fmt.Errorf("conversation %s has been replaced by %s, so nothing was recorded", conversationID, current.ConversationID)
	}
	for i := range current.PendingWrites {
		write := &current.PendingWrites[i]
		if write.ID != writeID {
			continue
		}
		switch {
		case write.ReturnsHold == nil || write.Publication != nil:
			return PendingWrite{}, fmt.Errorf("document %s is not held for your decision; nothing was recorded", writeID)
		case write.ReturnsHold.Decision != nil:
			return PendingWrite{}, fmt.Errorf("document %s was already decided %q at %s, which stands; nothing was recorded", writeID, write.ReturnsHold.Decision.Decision, write.ReturnsHold.Decision.DecidedAt.Local().Format("2006-01-02 15:04 MST"))
		}
		write.ReturnsHold.Decision = &decision
		current.Told++
		if err := s.Save(current); err != nil {
			return PendingWrite{}, err
		}
		return *write, nil
	}
	return PendingWrite{}, fmt.Errorf("document %s is not waiting in conversation %s; its writer may have replaced or withdrawn it, so nothing was recorded", writeID, conversationID)
}
