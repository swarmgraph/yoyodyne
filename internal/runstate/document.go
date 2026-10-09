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

// RunID is the run that makes the first attempt to publish this document.
func (d DocumentPublication) RunID() string { return d.RunIDFor(0) }

// RunIDFor is the run that makes one attempt to publish this document, counted
// from zero. Every attempt is its own run, so a run that stopped keeps its
// record whole while the next one starts, and each is derived from the same
// handoff, so the conversation keeps one stored text however many attempts it
// takes. The first keeps the identifier a document run has always had.
func (d DocumentPublication) RunIDFor(attempt int) string {
	key := d.ConversationID + "\x00" + d.WriteID
	if attempt > 0 {
		key += fmt.Sprintf("\x00attempt-%d", attempt)
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("run-%x", sum[:16])
}

// MaxDocumentReturns is how many runs that judged a document may hand it back
// to its owner, in one conversation, before automatic publication of it stops.
const MaxDocumentReturns = 3

// MaxDocumentAttempts is how many runs one confirmed document is given when
// each stops for a cause that judged nothing about it. The harness starts the
// next with the same stored text; once this many have stopped, the last is
// handed to the development manager as a stopped run, with the text kept on
// its record, rather than back to the owner to write again.
const MaxDocumentAttempts = 3

// DocumentRetry is what a document run that stopped without judging its
// document records about the run after it.
type DocumentRetry struct {
	// DevelopersAtStop is how many runs held a developer slot, this one
	// included, when this run's checks were stopped by a time limit. Zero where
	// the stop was not a time limit or the count could not be taken. The next
	// attempt waits until fewer are running, because the same suite under the
	// same load would be stopped the same way.
	DevelopersAtStop int `json:"developers_at_stop,omitempty"`
	// WaitedFor says what the next attempt waited for before it started, in
	// the words the owning conversation was told.
	WaitedFor string `json:"waited_for,omitempty"`
	// Next is the run the next attempt was started as.
	Next string `json:"next,omitempty"`
	// HandedOver says this was the last attempt, and the run was put to the
	// development manager as a stopped run.
	HandedOver bool `json:"handed_over,omitempty"`
}

// DocumentAttempt is which attempt at its document this run is, counted from
// zero, and -1 for a run that is not a document run or is none of them.
func (s State) DocumentAttempt() int {
	if s.Document == nil {
		return -1
	}
	for attempt := 0; attempt < MaxDocumentAttempts; attempt++ {
		if s.Document.RunIDFor(attempt) == s.RunID {
			return attempt
		}
	}
	return -1
}

// DocumentJudged reports whether a document run that ended without landing
// stopped on a judgement of the document: a check that ran and failed, the
// independent reviewer refusing it, a path the change may not touch, or a
// target document that changed after it was confirmed. Those are what its
// owner has to answer by revising it. Every other stop — a check stopped by a
// time limit, the forge or the network failing, the machine or the harness
// stopping, a harness step failing — said nothing about the document, so the
// same text is tried again and nothing is counted against it.
func (s State) DocumentJudged() bool {
	if s.ReplayConflict != nil || s.PathRefusal != nil {
		return true
	}
	switch s.RecordedStopClass() {
	case StopReview:
		return true
	case StopChecks:
		return s.CheckFailure != nil
	}
	return false
}

// DocumentStoppedOnTime reports a document run whose checks were stopped by a
// time limit: a check's own, the stage's, or the deadline of the work that
// started it.
func (s State) DocumentStoppedOnTime() bool {
	class := s.RecordedStopClass()
	return class == StopCheckTimeout || class == CauseCheckStageBound.StopClass()
}

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
	// Judged says a settled run that did not land stopped on a judgement of the
	// document, which is the only return its owner revises for and the only one
	// counted toward MaxDocumentReturns. HandedOver says the attempts were spent
	// on stops that judged nothing and the last run went to the development
	// manager instead.
	Judged     bool `json:"judged,omitempty"`
	HandedOver bool `json:"handed_over,omitempty"`
	// Retrying is what an unsettled delivery tells the owning role: that a run
	// stopped without judging the document and the next attempt is waiting or
	// will start at the next message. Empty for every other unsettled delivery.
	Retrying string `json:"retrying,omitempty"`
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
