package chat

// What the development manager decides about a stopped document publication.
//
// A document a role wrote is landed by a run of its own with no work item
// behind it, so when that run stops and is put on her docket, none of the
// decisions recorded on an item's triage record can be taken on it: there is no
// item to note, block, or budget. Two decisions answer it instead, and both are
// recorded on the run. "retry" lands the same confirmed text again through a
// fresh run, which the harness starts as a document landing frees, and closes
// the stopped run's pull request; "retire" closes that pull request and records
// why. Each names the run the docket entry names, and no item.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// StoppedPublications carries out a decision about a stopped document
// publication. It is satisfied by orchestrator.DocumentDecider. Each operation
// reports what it did in a sentence for the role that decided, and refuses a
// run that is not a stopped document publication.
type StoppedPublications interface {
	// DocumentRun reports whether the named run is a document publication, so a
	// decision only an item can take is refused on one with the two that apply.
	DocumentRun(ctx context.Context, runID string) (bool, error)
	RetryDocument(ctx context.Context, runID string, decision runstate.DocumentDecision) (string, error)
	RetireDocument(ctx context.Context, runID string, decision runstate.DocumentDecision) (string, error)
}

// decidesDocument reports a triage action deciding a stopped document
// publication.
func (a TrackerAction) decidesDocument() bool {
	if a.Action != actionTriage {
		return false
	}
	decision := strings.TrimSpace(a.Decision)
	return decision == decisionRetryDocument || decision == decisionRetireDocument
}

// carryOutDocumentDecision records a retry or a retirement on the stopped
// document run it names, and closes the run's entry on the docket.
func (s *Session) carryOutDocumentDecision(ctx context.Context, outcome *TrackerOutcome, decision, runID string) {
	action := outcome.Action
	if s.options.Publications == nil {
		outcome.fail(errors.New("nothing that carries out a decision about a document publication is wired to this conversation, so nothing was recorded"))
		return
	}
	recorded := runstate.DocumentDecision{
		Reason: strings.TrimSpace(action.Reason),
		// Who decided it and where are the conversation's own facts, as on every
		// other decision, rather than anything the reply asserted.
		DecidedBy: fmt.Sprintf("the %s in conversation %s at turn %d", RoleTitle(s.state.Role), s.state.ConversationID, s.state.Turns),
	}
	carry := s.options.Publications.RetireDocument
	if decision == decisionRetryDocument {
		carry = s.options.Publications.RetryDocument
	}
	said, err := carry(ctx, runID, recorded)
	if err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("triaged the document publication of run %s as %q: %s%s", runID, decision, said, s.closeDocketEntry(ctx, decision, runID, action.Reason))
}

// refuseDocumentRun refuses a decision that needs a work item when the run it
// names is a document publication, which has none, naming the two decisions
// that answer one. A run the records cannot answer for is left to the checks
// every other decision already goes through.
func (s *Session) refuseDocumentRun(ctx context.Context, decision, runID string) error {
	if runID == "" || s.options.Publications == nil {
		return nil
	}
	document, err := s.options.Publications.DocumentRun(ctx, runID)
	if err != nil || !document {
		return nil
	}
	return fmt.Errorf("run %s is a document publication, which has no work item, so %q cannot be recorded on it; the two decisions that answer it are %q, which lands the same confirmed text through a fresh run and closes its pull request, and %q, which closes its pull request and records why",
		runID, decision, decisionRetryDocument, decisionRetireDocument)
}
