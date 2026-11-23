package chat

// A revision written after automatic publication of its document stopped over
// returned runs: held, released for one run, or declined. The rule and the
// record are runstate's documentreturns.go; this file is what the writer's
// conversation does with them and the development manager's typed action that
// decides one.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReturnedDocuments records the development manager's decision on a revision
// held in another role's conversation. It is satisfied by
// *runstate.ConversationStore.
type ReturnedDocuments interface {
	DecideReturnedDocument(ctx context.Context, conversationID, writeID string, decision runstate.DocumentReturnsDecision) (runstate.PendingWrite, error)
}

var returnedDocumentDecisions = []string{runstate.DocumentReturnsRelease, runstate.DocumentReturnsDecline}

// heldDocument is the conversation and the write a held_document action names.
func (a TrackerAction) heldDocument() (conversationID, writeID string) {
	conversationID, writeID, _ = strings.Cut(strings.TrimSpace(a.Document), "/")
	return conversationID, writeID
}

func (a TrackerAction) heldDocumentProblems() []error {
	var problems []error
	if conversation, write := a.heldDocument(); !strings.HasPrefix(conversation, "chat-") || !strings.HasPrefix(write, "document-") {
		problems = append(problems, fmt.Errorf("held_document requires \"document\", the held document exactly as your list names it, such as \"chat-.../document-12.1\"; %q is not one", a.Document))
	}
	if !slices.Contains(returnedDocumentDecisions, strings.TrimSpace(a.Decision)) {
		problems = append(problems, fmt.Errorf("held_document requires \"decision\", one of %s", strings.Join(returnedDocumentDecisions, ", ")))
	}
	if reason := strings.TrimSpace(a.Reason); len(reason) > runstate.MaxHandedBackReasonBytes {
		problems = append(problems, fmt.Errorf("held_document reason is %d bytes, limit is %d", len(reason), runstate.MaxHandedBackReasonBytes))
	}
	return problems
}

// decideHeldDocument records her decision on the held revision. The writer's
// conversation carries it out and tells the writer at its next message.
func (s *Session) decideHeldDocument(ctx context.Context, outcome *TrackerOutcome) {
	if s.options.ReturnedDocuments == nil {
		outcome.fail(errors.New("this conversation cannot reach held documents, so nothing was decided; the development manager's conversation is the one that can"))
		return
	}
	action := outcome.Action
	conversationID, writeID := action.heldDocument()
	write, err := s.options.ReturnedDocuments.DecideReturnedDocument(ctx, conversationID, writeID, runstate.DocumentReturnsDecision{
		Decision:  strings.TrimSpace(action.Decision),
		Reason:    strings.TrimSpace(action.Reason),
		DecidedBy: fmt.Sprintf("the %s in conversation %s at turn %d", RoleTitle(s.state.Role), s.state.ConversationID, s.state.Turns),
		DecidedAt: s.options.clock().Now().UTC(),
	})
	if err != nil {
		outcome.fail(err)
		return
	}
	if write.ReturnsHold.Decision.Decision == runstate.DocumentReturnsRelease {
		outcome.applied("released document %s (%s) for one reviewed run, which starts at its writer's next message; if that run is returned, the next revision is held for you again", writeID, write.Artifact)
		return
	}
	outcome.applied("declined document %s (%s); its writer is told your reason at their next turn", writeID, write.Artifact)
}

// releasedReturns reports a revision the development manager released from the
// stop for one run.
func (r *writeRecord) releasedReturns() bool {
	hold := r.pending.ReturnsHold
	return hold != nil && hold.Decision != nil && hold.Decision.Decision == runstate.DocumentReturnsRelease
}

// holdForReturns keeps a revision unsent for the development manager and tells
// its writer once.
func (s *Session) holdForReturns(record *writeRecord, why string) error {
	id := strings.TrimSpace(record.pending.Write.ID)
	record.pending.ReturnsHold = &runstate.DocumentReturnsHold{
		HeldAt: s.options.clock().Now().UTC(), Why: why, LastRun: s.state.DocumentLastReturn[id],
	}
	return s.carryResults(fmt.Sprintf("Document %s (%s) is kept and not sent: automatic publication stopped after %d returned runs for this document in this conversation, and %s. It is held for the development manager, who releases it for one reviewed run or declines it with a reason; you are told which, and it does not need writing again. Writing a new revision puts that one in its place.\n",
		record.pending.ID, id, runstate.MaxDocumentReturns, why))
}

// settleReturnsHold carries out what the development manager decided about a
// held revision, and reports whether the revision goes on to be sent.
func (s *Session) settleReturnsHold(record *writeRecord) (bool, error) {
	hold := record.pending.ReturnsHold
	if hold == nil || record.pending.Publication != nil {
		return true, nil
	}
	if hold.Decision == nil {
		return false, nil
	}
	id := strings.TrimSpace(record.pending.Write.ID)
	decided := hold.Decision
	if record.releasedReturns() {
		if hold.Told {
			return true, nil
		}
		hold.Told = true
		return true, s.carryResults(fmt.Sprintf("Document %s (%s) was released by %s for one reviewed run, because: %s. It is sent as you wrote it. If that run is returned, your next revision is held for the development manager again.\n",
			record.pending.ID, id, decided.DecidedBy, decided.Reason))
	}
	told := fmt.Sprintf("Document %s (%s) was declined by %s, because: %s. It is not sent and its waiting place is given back. A revision you write that answers this is held for the development manager in its turn.\n",
		record.pending.ID, id, decided.DecidedBy, decided.Reason)
	if err := s.emit(execution.EventDocumentDrafted, record.pending); err != nil {
		return false, err
	}
	record.decided = true
	s.keepHandedBack(record, runstate.DocumentDelivery{Detail: told})
	return false, s.carryResults(told)
}

// returnedFindings is what the last returned run said of a document whose
// publication has stopped, kept with the next revision so the development
// manager reads the findings beside it. It is read before the earlier revision
// is forgotten; a held revision being replaced passes on what it carried.
func (s *Session) returnedFindings(id string) string {
	if s.state.DocumentReturns[id] < runstate.MaxDocumentReturns {
		return ""
	}
	for _, back := range s.state.HandedBack {
		if back.Artifact == id {
			return back.Reason
		}
	}
	for i := len(s.writes) - 1; i >= 0; i-- {
		if pending := s.writes[i].pending; strings.TrimSpace(pending.Write.ID) == id && pending.Publication == nil {
			return pending.ReturnedFindings
		}
	}
	return ""
}

// handedBackBy names the run that handed a revision back, where one did.
func handedBackBy(runID string) string {
	if runID == "" {
		return ""
	}
	return " by run " + runID
}
