package orchestrator

// Starting a confirmed document that waited for a document landing to free.
//
// A document a role wrote and the harness confirmed is landed by a short
// reviewed run, counted against execution.document_landings and never against
// the developer slots (runstate.State.HoldsDocumentLanding). Where every landing
// was taken when its conversation offered it, the publication is written down as
// waiting (runstate.DocumentWait), and every pull starts the waiting documents as
// landings free, whatever the developer slots hold: a document run is short, and
// the work it unblocks is usually work waiting behind it. A run already going is
// never stopped for one.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ScheduleDocuments is the confirmed documents waiting for a document landing
// to free, and the way the scheduler starts one. It is optional, and a pull
// wired without it starts no document: each is then offered again only at its
// conversation's next message.
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

// documentLandingLimit is how many documents may be landing at once, as this
// pull read it.
func (p Pull) documentLandingLimit() int {
	return config.Execution{DocumentLandings: p.DocumentLandings}.DocumentLandingLimit()
}

// nextDocuments reads the documents waiting for a document landing to free and
// returns the ones this pull starts, longest waiting first, as far as the free
// landings and the session's --limit allow. The landings in flight are landing,
// and no developer slot enters into it. A document left waiting is passed over
// saying it takes the next landing that frees.
//
// A document whose run is already in flight is left to that run: it is never
// started a second time, and the run's own start clears the wait. The store's
// reservation is what finally decides either way, refusing a second run of one
// document and a landing beyond the limit.
func (s Scheduler) nextDocuments(pull Pull, landing map[string]runstate.State, mine map[string]int, refused map[string]refusedDocument, started int, passOver func(id, reason string)) ([]runstate.DocumentWait, error) {
	if pull.Documents == nil {
		return nil, nil
	}
	waits, err := pull.Documents.DocumentWaits()
	if err != nil {
		return nil, fmt.Errorf("read the documents waiting for a document landing: %w", err)
	}
	free := pull.documentLandingLimit() - len(landing)
	var starting []runstate.DocumentWait
	for _, wait := range waits {
		runID := wait.RunID()
		if _, dispatched := mine[runID]; dispatched {
			continue
		}
		if _, inFlight := landing[runID]; inFlight {
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
			passOver(runID, fmt.Sprintf("%s has waited since %s for one of the %d document landings to free, and takes the next one that does",
				documentNamed(wait), wait.Since.Local().Format("2006-01-02 15:04 MST"), pull.documentLandingLimit()))
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
	return fmt.Sprintf("%s was confirmed and has waited for a document landing to free since %s; the harness is landing it through its reviewed run now that one has, without taking a developer slot",
		documentNamed(wait), wait.Since.Local().Format("2006-01-02 15:04 MST"))
}
