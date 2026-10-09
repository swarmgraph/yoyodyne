package orchestrator

// Starting a confirmed document that waited for a developer slot.
//
// A document a role wrote and the harness confirmed is landed by a short
// reviewed run, and that run takes a developer slot. Where every slot was taken
// when its conversation offered it, it was offered again only at that
// conversation's next message, so a design that unblocks other work waited for
// as long as development kept the slots full and the conversation stayed quiet.
// The publication is now written down as waiting (runstate.DocumentWait), and
// every pull starts the waiting documents into its free slots before anything
// else takes one: a document run is short, and the work it unblocks is usually
// work waiting behind it. A run already going is never stopped for one.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ScheduleDocuments is the confirmed documents waiting for a developer slot,
// and the way the scheduler starts one. It is optional, and a pull wired without
// it starts no document: each is then offered again only at its conversation's
// next message, which is what every document waited on before this existed.
type ScheduleDocuments interface {
	DocumentWaits() ([]runstate.DocumentWait, error)
	// Publish starts the document's reviewed run and waits it out, as its
	// conversation would; see Pipeline.PublishWaitingDocument.
	Publish(ctx context.Context, document runstate.DocumentPublication) (Outcome, error)
}

// refusedDocument is what a session remembers of a document start that failed,
// so a pull leaves it for continuationRetry rather than failing it every poll.
type refusedDocument struct {
	at  time.Time
	why string
}

// nextDocuments reads the documents waiting for a developer slot and returns
// the ones this pull starts, longest waiting first, as far as the free slots and
// the session's --limit allow. A document left waiting is passed over saying it
// takes the next slot that frees.
//
// A document whose run is already in flight is left to that run: it is never
// started a second time, and the run's own start clears the wait. The store's
// reservation is what finally decides either way, refusing a second run of one
// document and a run beyond the capacity.
func (s Scheduler) nextDocuments(pull Pull, occupied map[string]runstate.State, mine map[string]int, refused map[string]refusedDocument, free, started int, passOver func(id, reason string)) ([]runstate.DocumentWait, error) {
	if pull.Documents == nil {
		return nil, nil
	}
	waits, err := pull.Documents.DocumentWaits()
	if err != nil {
		return nil, fmt.Errorf("read the documents waiting for a developer slot: %w", err)
	}
	var starting []runstate.DocumentWait
	for _, wait := range waits {
		runID := wait.RunID()
		if _, dispatched := mine[runID]; dispatched {
			continue
		}
		if _, inFlight := occupied[runID]; inFlight {
			passOver(runID, fmt.Sprintf("%s is already being landed by run %s, so it is not started again", documentNamed(wait), runID))
			continue
		}
		if last, tried := refused[runID]; tried {
			again := last.at.Add(continuationRetry)
			if s.now().Before(again) {
				passOver(runID, fmt.Sprintf("starting %s failed at %s: %s; it is attempted again from %s",
					documentNamed(wait), last.at.Local().Format("2006-01-02 15:04 MST"), last.why, again.Local().Format("2006-01-02 15:04 MST")))
				continue
			}
		}
		if s.Limit > 0 && started+len(starting) >= s.Limit {
			break
		}
		if free < 1 {
			passOver(runID, fmt.Sprintf("%s has waited for a developer slot since %s and takes the next one that frees, ahead of any new development run",
				documentNamed(wait), wait.Since.Local().Format("2006-01-02 15:04 MST")))
			continue
		}
		free--
		starting = append(starting, wait)
	}
	return starting, nil
}

// documentNamed is a waiting document in the words a schedule line uses.
func documentNamed(wait runstate.DocumentWait) string {
	document := wait.Document
	return fmt.Sprintf("document %s (%s), written by the %s in conversation %s", document.WriteID, document.Candidate.Artifact.Title, document.Owner.Title(), document.ConversationID)
}

// documentReason is why a waiting document was started, as its schedule entry
// records it.
func documentReason(wait runstate.DocumentWait) string {
	return fmt.Sprintf("%s was confirmed and has waited for a developer slot since %s; the harness is landing it through its reviewed run in the slot that freed, ahead of any new development run",
		documentNamed(wait), wait.Since.Local().Format("2006-01-02 15:04 MST"))
}
