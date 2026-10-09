package runstate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// DocumentPublication binds a candidate to its owning conversation and author.
// The conversation keeps this handoff until the run has settled; the run keeps
// it afterwards, independently of the provider's session.
type DocumentPublication struct {
	Candidate            artifact.ConfirmedDocument `json:"candidate"`
	ConversationID       string                     `json:"conversation_id"`
	WriteID              string                     `json:"write_id"`
	Turn                 int                        `json:"turn"`
	Owner                domain.AgentRole           `json:"owner"`
	AuthorSession        string                     `json:"author_session,omitempty"`
	AuthorBackend        string                     `json:"author_backend"`
	AuthorModel          string                     `json:"author_model"`
	AuthorAccount        string                     `json:"author_account,omitempty"`
	AuthorConfigRevision string                     `json:"author_config_revision,omitempty"`
}

func (d DocumentPublication) RunID() string {
	sum := sha256.Sum256([]byte(d.ConversationID + "\x00" + d.WriteID))
	return fmt.Sprintf("run-%x", sum[:16])
}

const MaxDocumentReturns = 3

// ErrDocumentNotPublishable marks a publication refused before any run was
// opened for it, because the confirmation it carries no longer holds: the
// approval policy or the integration setting changed since, or the saved
// candidate is not one the harness can publish. Trying it again unchanged is
// refused the same way, so its conversation stops trying rather than keeping it.
var ErrDocumentNotPublishable = errors.New("the document's confirmation no longer allows a reviewed publication")

// DocumentDelivery is the durable run result returned to its owning conversation.
type DocumentDelivery struct {
	RunID   string `json:"run_id"`
	Settled bool   `json:"settled"`
	Landed  bool   `json:"landed"`
	Detail  string `json:"detail"`
	// WaitingForSlot is the developer capacity that refused the run, where every
	// slot was taken and the document is waiting for one (see DocumentWait), and
	// nil otherwise.
	WaitingForSlot *CapacityError `json:"waiting_for_slot,omitempty"`
}

func (d DocumentPublication) Validate() error {
	if err := d.Candidate.Validate(); err != nil {
		return err
	}
	if err := artifact.Authorize(d.Owner, d.Candidate.Artifact.Kind); err != nil {
		return err
	}
	if !strings.HasPrefix(d.ConversationID, "chat-") || !strings.HasPrefix(d.WriteID, "document-") || d.Turn < 1 || !domain.Backend(d.AuthorBackend).Valid() || strings.TrimSpace(d.AuthorModel) == "" {
		return errors.New("document publication must name its owning conversation, write, turn, and author provider")
	}
	revision := d.Candidate.Artifact.Revisions[len(d.Candidate.Artifact.Revisions)-1]
	if revision.By != d.Owner {
		return errors.New("document publication names a different role from its last author")
	}
	// The setting the harness confirmed under decides, as it did at confirmation;
	// the candidate's own validation has already refused product intent recorded
	// under any other setting.
	approval, _ := d.Candidate.Artifact.LatestApproval()
	if artifact.IntentPolicy(approval.Policy) && !artifact.ConsistentIntentClaim(revision) {
		return errors.New("automatic publication cannot confirm a change of fundamental intent")
	}
	return nil
}
