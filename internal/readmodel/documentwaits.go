package readmodel

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DocumentWaits is the confirmed documents waiting for a developer slot. It is
// an optional capability of Runs, satisfied by *runstate.Store; a reading over
// runs without it names no waiting document.
type DocumentWaits interface {
	DocumentWaits() ([]runstate.DocumentWait, error)
}

// DocumentSlotWait is one confirmed document whose reviewed run waits for a
// developer slot: which document, the role that owns it and the conversation
// it was written in, and since when it has waited.
type DocumentSlotWait struct {
	RunID          string           `json:"run_id"`
	WriteID        string           `json:"write_id"`
	Title          string           `json:"title"`
	Path           string           `json:"path"`
	Owner          domain.AgentRole `json:"owner"`
	ConversationID string           `json:"conversation_id"`
	Since          time.Time        `json:"since"`
	// Line is Says as the reading worded it, carried so the dashboard prints
	// the terminal's words rather than composing its own.
	Line string `json:"says"`
}

// documentWaitNext is what happens to a waiting document, and whose move it is:
// nobody's.
const documentWaitNext = "the harness starts it in the next developer slot that frees, ahead of any new development run, and nothing is asked of anybody"

// Says is the waiting document in the words the not-startable line prints.
func (w DocumentSlotWait) Says() string {
	return fmt.Sprintf("document %s (%s), the %s's, from conversation %s, waiting for a developer slot since %s",
		w.WriteID, w.Title, w.Owner.Title(), w.ConversationID, localMoment(w.Since))
}

// readDocumentWaits reads the documents waiting for a developer slot from the
// run store, longest waiting first.
func readDocumentWaits(sources Sources) ([]DocumentSlotWait, string) {
	waits, ok := sources.Runs.(DocumentWaits)
	if !ok {
		return nil, ""
	}
	recorded, err := waits.DocumentWaits()
	if err != nil {
		return nil, fmt.Sprintf("the documents waiting for a developer slot could not be read: %v", err)
	}
	var listed []DocumentSlotWait
	for _, wait := range recorded {
		document := wait.Document
		entry := DocumentSlotWait{
			RunID: wait.RunID(), WriteID: document.WriteID, Title: document.Candidate.Artifact.Title,
			Path: document.Candidate.Artifact.Path, Owner: document.Owner, ConversationID: document.ConversationID,
			Since: wait.Since,
		}
		entry.Line = entry.Says()
		listed = append(listed, entry)
	}
	return listed, ""
}
