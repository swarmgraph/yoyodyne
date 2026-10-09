package chat

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
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

// documentResumer is the part of the publisher that reads a conversation's
// document runs back, for resumeStoppedDocuments. A publisher without it
// resumes nothing.
type documentResumer interface {
	ResumableDocuments(conversationID string) ([]runstate.DocumentPublication, map[string]int, error)
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
	if err := s.resumeStoppedDocuments(publisher); err != nil {
		return err
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
			// A run that stopped without judging the document is followed by
			// another of the same text; the owning role is told once, and what
			// it is told is that nothing needs writing again.
			if retrying := delivery.Retrying; retrying != "" && retrying != record.failure {
				record.failure = retrying
				if err := s.carryResults(retrying); err != nil {
					return err
				}
			}
			continue
		}
		record.decided = true
		// Only a run that judged the document hands it back to its owner, and
		// only that is counted toward the returns that stop automatic
		// publication. A document handed to the development manager after
		// stops that judged nothing is not its owner's to revise.
		if !delivery.Landed && delivery.Judged {
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

// resumeStoppedDocuments puts back a document whose automatic publication in
// this conversation stopped over returns that judged nothing about it. Before
// the harness told those apart, a check stopped by a time limit or a forge
// error handed a document back to its owner like a failing check, and three of
// them stopped its publication with nothing found wrong in it.
//
// Where the run store's latest handoff for a stopped document ended on a stop
// that judged nothing, the handoff is kept again from the run store's copy of
// its text, so the next publication continues it rather than its owner writing
// it out again, and the document's count of returns is set to the returns that
// judged it. A stopped document the conversation has no room to hold yet keeps
// its count unchanged, so it still reads as stopped and is put back once a
// waiting document is settled; its owner is told once that it is waiting.
// Counts for documents that are not stopped are set to their judged returns.
//
// Until nothing is left waiting for room it looks again at each message; after
// that, once per process. A failure to read the run store is told to the owning
// role and stops nothing.
func (s *Session) resumeStoppedDocuments(publisher DocumentPublisher) error {
	resumer, ok := publisher.(documentResumer)
	if !ok || s.documentsResumed {
		return nil
	}
	if len(s.state.DocumentReturns) == 0 {
		s.documentsResumed = true
		return nil
	}
	resumable, judged, err := resumer.ResumableDocuments(s.state.ConversationID)
	if err != nil {
		s.documentsResumed = true
		return s.carryResults(fmt.Sprintf("The harness could not read this conversation's document runs to see whether any document stopped publishing over runs that judged nothing: %v. It looks again when this conversation is next opened.\n", err))
	}
	changed, waiting := false, false
	keep := map[string]bool{}
	var told strings.Builder
	for _, publication := range resumable {
		id := publication.Candidate.Artifact.ID
		recorded := s.state.DocumentReturns[id]
		if recorded < runstate.MaxDocumentReturns || judged[id] >= runstate.MaxDocumentReturns || s.holdsDocument(id) {
			continue
		}
		if s.waitingWrites() >= runstate.MaxPendingWrites {
			keep[id], waiting = true, true
			if !s.resumeWaitTold[id] {
				if s.resumeWaitTold == nil {
					s.resumeWaitTold = map[string]bool{}
				}
				s.resumeWaitTold[id] = true
				fmt.Fprintf(&told, "Document %s (%s) stopped publishing after %d returned runs, but only %d of them judged anything about the document, so the harness will publish it again from the confirmed text it kept. This conversation already holds %d documents waiting, so it is put back once one of them is settled; the %s does not need to write it again.\n", publication.WriteID, publication.Candidate.Artifact.Title, recorded, judged[id], runstate.MaxPendingWrites, s.state.Role.Title())
			}
			continue
		}
		publication := publication
		s.writes = append(s.writes, &writeRecord{pending: PendingWrite{ID: publication.WriteID, ConversationID: publication.ConversationID, Turn: publication.Turn, Write: writeOf(publication.Candidate), Publication: &publication}})
		changed = true
		fmt.Fprintf(&told, "Document %s (%s) is published again. Its automatic publication had stopped after %d returned runs, but only %d of them judged anything about the document; the rest were stopped by causes such as a check time limit or a forge error, which no longer count. The harness continues from the confirmed text it kept, and the %s does not need to write it again.\n", publication.WriteID, publication.Candidate.Artifact.Title, recorded, judged[id], s.state.Role.Title())
	}
	for id, returns := range s.state.DocumentReturns {
		if keep[id] || judged[id] >= returns {
			continue
		}
		changed = true
		if judged[id] == 0 {
			delete(s.state.DocumentReturns, id)
		} else {
			s.state.DocumentReturns[id] = judged[id]
		}
	}
	s.documentsResumed = !waiting
	if told.Len() > 0 {
		if err := s.carryResults(told.String()); err != nil {
			return err
		}
	}
	if !changed {
		return nil
	}
	return s.record()
}

// holdsDocument reports a document this conversation is still waiting on a
// decision or a publication for.
func (s *Session) holdsDocument(id string) bool {
	for _, record := range s.writes {
		if !record.decided && record.pending.Write.ID == id {
			return true
		}
	}
	return false
}

func (s *Session) waitingWrites() int {
	waiting := 0
	for _, record := range s.writes {
		if !record.decided {
			waiting++
		}
	}
	return waiting
}

// writeOf is the write a confirmed document answers, rebuilt from the text the
// run store kept, for a handoff put back by resumeStoppedDocuments. The
// publication carries the confirmed text itself, so this is what the
// conversation names and shows rather than anything that is written again.
func writeOf(document artifact.ConfirmedDocument) artifact.Write {
	a := document.Artifact
	write := artifact.Write{Action: artifact.WriteRevise, ID: a.ID, Title: a.Title, Supports: a.Supports, Body: documentBody(document.Content)}
	if len(a.Revisions) > 0 {
		last := a.Revisions[len(a.Revisions)-1]
		write.Reason, write.Intent = last.Reason, last.Intent
	}
	if document.Before == "" {
		write.Action, write.Intent = artifact.WriteCreate, ""
		write.Kind, write.Directory = a.Kind, path.Dir(a.Path)
	}
	return write
}

// documentBody is a document's text below its frontmatter.
func documentBody(content string) string {
	if rest, ok := strings.CutPrefix(content, "---\n"); ok {
		if _, body, found := strings.Cut(rest, "\n---\n"); found {
			return strings.TrimLeft(body, "\n")
		}
	}
	return content
}
