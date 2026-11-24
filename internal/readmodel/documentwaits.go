package readmodel

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DocumentWaits is the confirmed documents waiting for a document landing to
// free. It is
// an optional capability of Runs, satisfied by *runstate.Store; a reading over
// runs without it names no waiting document.
type DocumentWaits interface {
	DocumentWaits() ([]runstate.DocumentWait, error)
}

// DocumentSlotWait is one confirmed document whose reviewed run waits for a
// document landing to free: which document, the role that owns it and the conversation
// it was written in, and since when it has waited.
type DocumentSlotWait struct {
	RunID          string           `json:"run_id"`
	WriteID        string           `json:"write_id"`
	DocumentID     string           `json:"document_id"`
	Title          string           `json:"title"`
	Path           string           `json:"path"`
	Owner          domain.AgentRole `json:"owner"`
	ConversationID string           `json:"conversation_id"`
	Since          time.Time        `json:"since"`
	// Behind is what the document waits behind where another copy of it is
	// still going or waiting in a merge queue (runstate.PublishingAhead), in
	// the words its owning role is told, and empty where it waits only for a
	// document landing to free.
	Behind string `json:"behind,omitempty"`
	// Outside is why the named epics refused its run, and empty where they did
	// not. RenderEpics prints it with the problems, so the not-startable line
	// leaves it out.
	Outside string `json:"outside,omitempty"`
	// Line is Says as the reading worded it, carried so the dashboard prints
	// the terminal's words rather than composing its own.
	Line string `json:"says"`
}

// documentWaitNext is what happens to a waiting document, and whose move it is:
// nobody's.
const documentWaitNext = "the harness starts it as soon as a document landing frees, without taking a developer slot, and nothing is asked of anybody"

// documentBehindNext is what happens to a document waiting behind another copy
// of itself, and whose move it is: nobody's.
const documentBehindNext = "the harness starts it once that copy lands or is handed back, without taking a developer slot, and nothing is asked of anybody"

// Next is what happens to the waiting document.
func (w DocumentSlotWait) Next() string {
	if w.Behind != "" {
		return documentBehindNext
	}
	return documentWaitNext
}

// Says is the waiting document in the words the not-startable line prints.
func (w DocumentSlotWait) Says() string {
	if w.Outside != "" {
		return fmt.Sprintf("document %s (%s), the %s's, from conversation %s, waiting since %s: %s",
			w.WriteID, w.Title, w.Owner.Title(), w.ConversationID, localMoment(w.Since), w.Outside)
	}
	if w.Behind != "" {
		return fmt.Sprintf("document %s (%s), the %s's, from conversation %s, waiting since %s: %s",
			w.WriteID, w.Title, w.Owner.Title(), w.ConversationID, localMoment(w.Since), w.Behind)
	}
	return fmt.Sprintf("document %s (%s), the %s's, from conversation %s, waiting for a document landing since %s",
		w.WriteID, w.Title, w.Owner.Title(), w.ConversationID, localMoment(w.Since))
}

// markOutside puts the named epics' refusal on each waiting document it is about.
func markOutside(waits []DocumentSlotWait, epics *Epics) {
	if epics == nil {
		return
	}
	for _, refused := range epics.RefusedDocuments {
		for i := range waits {
			if waits[i].WriteID == refused.WriteID && waits[i].DocumentID == refused.DocumentID {
				waits[i].Outside = refused.Why
				waits[i].Line = waits[i].Says()
			}
		}
	}
}

// documentRuns is the run store's listing of one document's runs, an optional
// capability of Runs satisfied by *runstate.Store; a reading over runs without
// it says no waiting document waits behind another copy.
type documentRuns interface {
	DocumentRuns(id string) ([]runstate.State, error)
}

// readDocumentWaits reads the documents waiting for a document landing from the
// run store, longest waiting first.
func readDocumentWaits(sources Sources) ([]DocumentSlotWait, string) {
	waits, ok := sources.Runs.(DocumentWaits)
	if !ok {
		return nil, ""
	}
	recorded, err := waits.DocumentWaits()
	if err != nil {
		return nil, fmt.Sprintf("the documents waiting for a document landing could not be read: %v", err)
	}
	var listed []DocumentSlotWait
	for _, wait := range recorded {
		document := wait.Document
		entry := DocumentSlotWait{
			RunID: wait.RunID(), WriteID: document.WriteID, DocumentID: document.Candidate.Artifact.ID, Title: document.Candidate.Artifact.Title,
			Path: document.Candidate.Artifact.Path, Owner: document.Owner, ConversationID: document.ConversationID,
			Since: wait.Since,
		}
		if lister, ok := sources.Runs.(documentRuns); ok {
			// A listing that cannot be read says nothing it does not know: the
			// document is shown waiting, as the run store records it.
			if runs, err := lister.DocumentRuns(document.Candidate.Artifact.ID); err == nil {
				if ahead, found := runstate.PublishingAhead(document, runs); found {
					entry.Behind = ahead.Says()
				}
			}
		}
		entry.Line = entry.Says()
		listed = append(listed, entry)
	}
	return listed, ""
}
