package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DocumentPublisher is the harness's existing delivery pipeline. The role
// supplies prose, never a command, branch, grant, or choice of reviewer.
type DocumentPublisher interface {
	// PublishesDocuments reports whether a document confirmed now could land
	// through a reviewed run, which needs the project to integrate
	// automatically. Where it cannot, nothing is confirmed by policy and the
	// operator is asked, as for any kind whose policy is not automatic.
	PublishesDocuments() bool
	PublishDocument(context.Context, runstate.DocumentPublication) (runstate.DocumentDelivery, error)
}

type confirmationPreparer interface {
	PrepareConfirmation(domain.AgentRole, artifact.Write, artifact.Policy, string, time.Time) (artifact.ConfirmedDocument, bool, error)
}

// PublishDocuments also handles documents saved by an older build. Confirmation
// and its complete candidate are saved before any delivery work can start.
//
// It never stands between the operator and the role. A publication that could
// not start or finish is kept and told to the owning role, and the message that
// called this goes on; only a failure to record the conversation itself is
// returned.
func (s *Session) PublishDocuments(ctx context.Context) error {
	publisher := s.options.DocumentPublisher
	if publisher == nil {
		return nil
	}
	for _, record := range s.writes {
		if record.decided {
			continue
		}
		if record.pending.Publication == nil {
			confirmed, err := s.confirmDocument(record, publisher.PublishesDocuments())
			if err != nil {
				return err
			}
			if !confirmed {
				continue
			}
		}
		delivery, err := publisher.PublishDocument(ctx, *record.pending.Publication)
		if errors.Is(err, runstate.ErrDocumentNotPublishable) {
			// No run was opened for it, and the confirmation it carries no
			// longer holds — the policy or the integration setting changed since.
			// The document goes back to the path it would take now: the operator
			// is asked, or a submission the store refuses goes back to its owner.
			lapse := fmt.Sprintf("Document %s (%s) was not published and no run was opened: %v.", record.pending.ID, record.pending.Write.ID, err)
			record.pending.Publication = nil
			if _, err := s.confirmDocument(record, false); err != nil {
				return err
			}
			record.lapsed = true
			if !record.decided {
				lapse += " It is put to the operator to confirm instead."
			}
			if err := s.carryResults(lapse + "\n"); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			// The confirmation and its candidate stay saved, and the next message
			// tries again; the owning role is told what is holding it.
			if failure := err.Error(); failure != record.failure {
				record.failure = failure
				if err := s.carryResults(fmt.Sprintf("Document %s (%s) is confirmed, and its reviewed run could not start or finish: %s. It is kept and tried again at the next message; it does not need writing again.\n", record.pending.ID, record.pending.Publication.Candidate.Artifact.Title, failure)); err != nil {
					return err
				}
			}
			continue
		}
		if !delivery.Settled {
			continue
		}
		record.decided = true
		if !delivery.Landed {
			if s.state.DocumentReturns == nil {
				s.state.DocumentReturns = map[string]int{}
			}
			s.state.DocumentReturns[record.pending.Write.ID]++
			if s.state.DocumentReturns[record.pending.Write.ID] == runstate.MaxDocumentReturns {
				delivery.Detail += fmt.Sprintf("The %s must revise its plan: automatic publication stopped after %d returned runs for this document in this conversation.\n", s.state.Role.Title(), runstate.MaxDocumentReturns)
			}
		}
		if err := s.carryResults(delivery.Detail); err != nil {
			return err
		}
	}
	return s.record()
}

// confirmDocument confirms one waiting document under the automatic policy and
// saves the publication handoff, reporting whether it did. A document the
// policy does not confirm, or one that could not land through a reviewed run,
// is left for the operator; one the store refuses goes back to its owner. Only
// a failure to record the conversation is returned.
func (s *Session) confirmDocument(record *writeRecord, publishes bool) (bool, error) {
	preparer, ok := s.options.Documents.(confirmationPreparer)
	if !ok || record.lapsed {
		return false, nil
	}
	reason := fmt.Sprintf("confirmed by the harness in conversation %s, turn %d, for %s under the automatic approval policy", record.pending.ConversationID, record.pending.Turn, record.pending.ID)
	candidate, automatic, err := preparer.PrepareConfirmation(s.state.Role, record.pending.Write, s.options.DocumentPolicy, reason, s.options.clock().Now())
	if err != nil {
		// Keep the complete old draft in the durable event account before
		// releasing its pending slot and returning the refusal to its owner.
		if saved := s.emit(execution.EventDocumentDrafted, record.pending); saved != nil {
			return false, saved
		}
		record.decided = true
		return false, s.carryResults(fmt.Sprintf("Document %s (%s) could not be confirmed: %v. The %s must correct this saved submission in this conversation.\n", record.pending.ID, record.pending.Write.ID, err, s.state.Role.Title()))
	}
	// A confirmation saved where nothing can land it would hold the document
	// out of the operator's reach with no run to settle it, so the integration
	// setting is decided before anything is confirmed.
	if !automatic || !publishes {
		return false, nil
	}
	if s.state.DocumentReturns[candidate.Artifact.ID] >= runstate.MaxDocumentReturns {
		record.decided = true
		return false, s.carryResults(fmt.Sprintf("Document %s (%s) was not published. The %s must revise its plan: automatic publication stopped after %d returned runs for this document in this conversation. No operator decision or developer run is requested.\n", record.pending.ID, candidate.Artifact.Title, s.state.Role.Title(), runstate.MaxDocumentReturns))
	}
	record.pending.Publication = &runstate.DocumentPublication{Candidate: candidate, ConversationID: record.pending.ConversationID, WriteID: record.pending.ID, Turn: record.pending.Turn, Owner: s.state.Role, AuthorSession: s.state.ProviderSessionID, AuthorBackend: string(s.state.Backend), AuthorModel: s.requestedModel(), AuthorAccount: s.options.AccountAlias, AuthorConfigRevision: s.options.ConfigRevision}
	if err := s.record(); err != nil {
		return false, err
	}
	return true, nil
}
