package chat

// A role's own waiting documents: one waiting revision per document, at most
// runstate.MaxPendingWrites waiting documents, and the role's own way to see,
// replace and withdraw them. This is rule 5 of the ruling on the route
// documents land by (docs/designs/document-landing-route.md).
//
// Where a new revision goes depends on where the document's earlier one stands:
//
//   - its run has not started — it is waiting to be confirmed, for the
//     operator, for a document landing, or under a hold — so the new revision
//     replaces it;
//   - its run is going, so the new revision waits behind that run and is sent
//     once it ends;
//   - its run ended without landing it, so it is settled as handed back and the
//     new revision opens a fresh run at once.
//
// Every write is answered as it is recorded, in the role's next turn: where it
// stands and why. One the harness recorded and never answered — a process that
// died between the two — is reported as a harness defect when it is found.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// documentStandings is the part of the publisher that reads where a document's
// runs stand without starting anything, and takes one off the wait for a
// document landing. It is satisfied by orchestrator.Pipeline.
type documentStandings interface {
	DocumentStanding(runstate.DocumentPublication) (runstate.DocumentStanding, error)
	WithdrawDocument(runstate.DocumentPublication) error
}

// standingOf is where one waiting document's run stands. A document with no
// confirmed handoff has not started. Where the publisher cannot say, or the run
// store would not answer, a confirmed one is taken to be running: a new revision
// then waits behind it rather than being sent beside a run that may be going.
func (s *Session) standingOf(record *writeRecord) runstate.DocumentStanding {
	publication := record.pending.Publication
	if publication == nil {
		return runstate.DocumentStanding{}
	}
	running := runstate.DocumentStanding{RunID: publication.RunID(), Started: true}
	reader, ok := s.options.DocumentPublisher.(documentStandings)
	if !ok {
		return running
	}
	standing, err := reader.DocumentStanding(*publication)
	if err != nil {
		return running
	}
	return standing
}

// revisionPlace is where one new revision goes among the waiting revisions of
// its document.
type revisionPlace struct {
	// replaces are the waiting revisions whose run has not started.
	replaces []*writeRecord
	// finished are the waiting revisions whose run has ended, settled before the
	// new one is recorded.
	finished []finishedRevision
	// behind is the revision whose run is going, which the new one waits for.
	behind    *writeRecord
	behindRun string
}

type finishedRevision struct {
	record   *writeRecord
	standing runstate.DocumentStanding
}

// placeRevision finds where a new revision of a document goes.
func (s *Session) placeRevision(write artifact.Write) revisionPlace {
	id := strings.TrimSpace(write.ID)
	var place revisionPlace
	for _, record := range s.writes {
		if record.decided || strings.TrimSpace(record.pending.Write.ID) != id {
			continue
		}
		standing := s.standingOf(record)
		switch {
		case !standing.Started:
			place.replaces = append(place.replaces, record)
		case standing.Running():
			place.behind, place.behindRun = record, standing.RunID
		default:
			place.finished = append(place.finished, finishedRevision{record: record, standing: standing})
		}
	}
	return place
}

// frees reports whether a waiting revision gives its place up to the new one.
func (p revisionPlace) frees(record *writeRecord) bool {
	for _, replaced := range p.replaces {
		if replaced == record {
			return true
		}
	}
	for _, finished := range p.finished {
		if finished.record == record {
			return true
		}
	}
	return false
}

// takePlace settles what a new revision frees, before the revision is
// recorded: a finished run's revision is settled as its run ended, and an
// unstarted one is replaced. An unstarted one whose run the publisher finds
// started after all is left running, and the new revision waits behind it.
// What it settles is said in the answer to the new revision.
func (s *Session) takePlace(place *revisionPlace, newID string) ([]string, error) {
	var said []string
	for _, finished := range place.finished {
		said = append(said, fmt.Sprintf("%s's run %s ended without landing it, so that revision no longer waits and this one opens a fresh run", finished.record.pending.ID, finished.standing.RunID))
		if err := s.settleDelivery(finished.record, finished.standing.Delivery); err != nil {
			return said, err
		}
	}
	for _, replaced := range place.replaces {
		if publication := replaced.pending.Publication; publication != nil {
			if withdrawer, ok := s.options.DocumentPublisher.(documentStandings); ok {
				if err := withdrawer.WithdrawDocument(*publication); err != nil {
					place.behind, place.behindRun = replaced, publication.RunID()
					continue
				}
			}
		}
		summary := replaced.pending.recordedSummary()
		summary["replaced_by"] = newID
		if err := s.emit(execution.EventDocumentReplaced, summary); err != nil {
			return said, fmt.Errorf("record replaced document %s: %w", replaced.pending.ID, err)
		}
		replaced.decided = true
		said = append(said, fmt.Sprintf("it replaces %s, whose run had not started; that revision is no longer sent", replaced.pending.ID))
	}
	return said, nil
}

// settleDelivery settles a waiting document whose run ended, as the run says:
// only a run that judged the document counts toward the returns that stop
// automatic publication, and one that ended without landing or passing is kept
// among the role's handed-back documents with what the run said. The owning
// role is told what the run came to.
func (s *Session) settleDelivery(record *writeRecord, delivery runstate.DocumentDelivery) error {
	record.decided = true
	if !delivery.Landed && delivery.Judged {
		if s.state.DocumentReturns == nil {
			s.state.DocumentReturns = map[string]int{}
		}
		s.state.DocumentReturns[record.pending.Write.ID]++
		if s.state.DocumentReturns[record.pending.Write.ID] == runstate.MaxDocumentReturns {
			delivery.Detail += fmt.Sprintf("The %s must revise its plan: automatic publication stopped after %d returned runs for this document in this conversation.\n", s.state.Role.Title(), runstate.MaxDocumentReturns)
		}
	}
	if !delivery.Landed && !delivery.Passed {
		s.keepHandedBack(record, delivery)
	}
	return s.carryResults(delivery.Detail)
}

// keepHandedBack adds one handed-back revision to what the role can see, in
// place of any earlier one of the same document, keeping the latest.
func (s *Session) keepHandedBack(record *writeRecord, delivery runstate.DocumentDelivery) {
	artifactID := strings.TrimSpace(record.pending.Write.ID)
	reason := oneline.Fold(strings.TrimSpace(delivery.Detail), runstate.MaxHandedBackReasonBytes)
	if reason == "" {
		reason = "its run ended without landing it and recorded no reason"
	}
	kept := s.forgetHandedBack(artifactID)
	kept = append(kept, runstate.HandedBackDocument{
		ID: record.pending.ID, Artifact: artifactID, Turn: record.pending.Turn, Written: record.pending.WrittenAt,
		RunID: delivery.RunID, Reason: reason, At: s.options.clock().Now().UTC(),
	})
	for len(kept) > runstate.MaxHandedBackDocuments {
		kept = slices.Delete(kept, 0, 1)
	}
	s.state.HandedBack = kept
}

// forgetHandedBack is the handed-back revisions without any of one document's.
func (s *Session) forgetHandedBack(artifactID string) []runstate.HandedBackDocument {
	var kept []runstate.HandedBackDocument
	for _, back := range s.state.HandedBack {
		if back.Artifact != artifactID {
			kept = append(kept, back)
		}
	}
	return kept
}

// answerWrite is what the owning role is told about one write as it is
// recorded: that it is recorded, what it took the place of, and where it now
// waits and why. What it is confirmed, published, or refused as is told later,
// as each happens.
func (s *Session) answerWrite(record *writeRecord, settled []string, place revisionPlace) string {
	var said strings.Builder
	fmt.Fprintf(&said, "Document %s (%s, %s) is recorded", record.pending.ID, strings.TrimSpace(record.pending.Write.ID), record.pending.Write.Describe())
	for _, line := range settled {
		said.WriteString("; " + line)
	}
	said.WriteString(". It is waiting: " + s.waitingReason(record, place.behind, place.behindRun) + "\n")
	return said.String()
}

// waitingReason says why a document that has not started its run is waiting.
func (s *Session) waitingReason(record *writeRecord, behind *writeRecord, behindRun string) string {
	switch {
	case behind != nil:
		return fmt.Sprintf("its earlier revision %s is being published in run %s, and this one is sent once that run ends", behind.pending.ID, behindRun)
	case record.heldForSending():
		hold := record.pending.Write.Hold
		return fmt.Sprintf("you held sending it until %s closes (%s)", strings.TrimSpace(hold.UntilClosed), strings.TrimSpace(hold.Reason))
	case record.waitsFor != "":
		return record.waitsFor
	case record.pending.Publication != nil && record.failure != "":
		return "it is confirmed and its run has not started: " + strings.TrimSpace(record.failure)
	case record.pending.Publication != nil:
		return "it is confirmed and its run has not started yet"
	default:
		return "the harness confirms it under its kind's approval setting, or puts it to the operator where that setting asks him, at the start of the next message, and tells you which"
	}
}

// waitingBehind is the waiting revision of the same document whose run is
// going, ahead of this one, if there is one.
func (s *Session) waitingBehind(record *writeRecord) (*writeRecord, string) {
	id := strings.TrimSpace(record.pending.Write.ID)
	for _, other := range s.writes {
		if other == record {
			return nil, ""
		}
		if other.decided || other.pending.Publication == nil || strings.TrimSpace(other.pending.Write.ID) != id {
			continue
		}
		return other, other.pending.Publication.RunID()
	}
	return nil, ""
}

// answerUnanswered reports every waiting document the harness recorded and
// never told its owning role about, as a harness defect, and answers it now.
// It runs before each message's documents are published, which is before the
// role's next turn.
func (s *Session) answerUnanswered() error {
	var told strings.Builder
	for _, record := range s.writes {
		if record.decided || !record.unanswered {
			continue
		}
		s.fileUnansweredDefect(record)
		behind, run := s.waitingBehind(record)
		fmt.Fprintf(&told, "Document %s (%s) was recorded at turn %d and you were not told so; that was the harness's fault, and it is reported. It is waiting: %s\n",
			record.pending.ID, strings.TrimSpace(record.pending.Write.ID), record.pending.Turn, s.waitingReason(record, behind, run))
		record.unanswered = false
	}
	if told.Len() == 0 {
		return nil
	}
	return s.carryResults(told.String())
}

// fileUnansweredDefect files the report a write left unanswered is owed. A
// conversation without the reports pile still answers the role, and records
// that it could not report it in the role's next turn.
func (s *Session) fileUnansweredDefect(record *writeRecord) {
	message := fmt.Sprintf("Harness defect: document %s (%s), written by the %s at turn %d of conversation %s, was recorded and its author was never told where it stands, so the write went unanswered past the author's next turn. It has been answered now; the cause is in that conversation's event log around its recording.",
		record.pending.ID, strings.TrimSpace(record.pending.Write.ID), RoleTitle(s.state.Role), record.pending.Turn, s.state.ConversationID)
	if s.options.Reports == nil {
		s.notice("the harness could not report that document %s went unanswered, because this conversation has no reports pile", record.pending.ID)
		return
	}
	collected, err := report.Collect([]report.Entry{{Severity: report.SeverityWarning, Message: message}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        s.state.ConversationID,
		Build:        s.options.Build,
		ProductID:    s.options.ProductID,
		RepositoryID: s.options.RepositoryID,
	}, s.options.clock().Now())
	if err == nil {
		for _, reported := range collected {
			if err = s.options.Reports.Append(reported); err != nil {
				break
			}
		}
	}
	if err != nil {
		s.notice("the harness could not report that document %s went unanswered: %v", record.pending.ID, err)
	}
}

// withdrawWrites carries out a role's withdrawals of its own waiting documents,
// under its own authority, and says what became of each. A document whose run
// is going or has passed is refused, and so is anything that is not one of this
// role's documents waiting in this conversation.
func (s *Session) withdrawWrites(withdrawals []artifact.Withdrawal) (string, error) {
	if len(withdrawals) == 0 {
		return "", nil
	}
	var out strings.Builder
	out.WriteString("# Withdrawn documents\n\n")
	owns := len(artifact.Owned(s.state.Role)) > 0
	for _, withdrawal := range withdrawals {
		id, reason := strings.TrimSpace(withdrawal.Write), strings.TrimSpace(withdrawal.Reason)
		if !owns {
			fmt.Fprintf(&out, "- %s: refused. The %s owns no documents, so it has none to withdraw.\n", id, RoleTitle(s.state.Role))
			continue
		}
		said, err := s.withdrawWrite(id, reason)
		if err != nil {
			return out.String(), err
		}
		fmt.Fprintf(&out, "- %s: %s\n", id, said)
	}
	return out.String() + "\n", s.record()
}

// withdrawWrite withdraws one write and says what became of it; only a failure
// to record is returned as an error.
func (s *Session) withdrawWrite(id, reason string) (string, error) {
	var record *writeRecord
	for _, candidate := range s.writes {
		if candidate.pending.ID == id && !candidate.decided {
			record = candidate
		}
	}
	if record == nil {
		for _, back := range s.state.HandedBack {
			if back.ID == id {
				s.state.HandedBack = s.forgetHandedBack(back.Artifact)
				return fmt.Sprintf("withdrawn. It had already been handed back by run %s and was no longer waiting; it is no longer listed among your documents.", back.RunID), nil
			}
		}
		return "refused. No document of yours with that name is waiting in this conversation; a document another role wrote, or one already settled, is not yours to withdraw.", nil
	}
	if err := record.pending.Write.Authorize(s.state.Role); err != nil {
		return "refused. " + err.Error(), nil
	}
	standing := s.standingOf(record)
	switch {
	case standing.Running():
		return fmt.Sprintf("refused. Its run %s has started and has not ended, so it is published or handed back as that run decides; write a new revision and it waits behind that run.", standing.RunID), nil
	case standing.Finished() && !standing.HandedBack():
		return fmt.Sprintf("refused. Its run %s has already passed it, and the harness tells you how it lands.", standing.RunID), nil
	}
	if publication := record.pending.Publication; publication != nil {
		if withdrawer, ok := s.options.DocumentPublisher.(documentStandings); ok {
			if err := withdrawer.WithdrawDocument(*publication); err != nil {
				return "refused. " + err.Error() + ".", nil
			}
		}
	}
	summary := record.pending.recordedSummary()
	summary["reason"] = reason
	summary["by"] = string(s.state.Role)
	if err := s.emit(execution.EventDocumentWithdrawn, summary); err != nil {
		return "", fmt.Errorf("record withdrawn document %s: %w", record.pending.ID, err)
	}
	record.decided = true
	s.state.HandedBack = s.forgetHandedBack(strings.TrimSpace(record.pending.Write.ID))
	if standing.HandedBack() {
		return fmt.Sprintf("withdrawn. Its run %s had ended without landing it; it is not sent again and its place is given back.", standing.RunID), nil
	}
	return "withdrawn. Its run had not started; it is not sent and its place is given back.", nil
}

// renderWaitingDocuments lists the role's own documents waiting in this
// conversation, and the ones runs handed back, as the role asked to see them.
func (s *Session) renderWaitingDocuments() string {
	var out strings.Builder
	out.WriteString("# Your waiting documents\n\n")
	waiting, _ := s.waitingAndHeld()
	fmt.Fprintf(&out, "%d of %d places for waiting documents are taken; documents held for sending are counted apart, at most %d.\n", waiting, runstate.MaxPendingWrites, runstate.MaxHeldWrites)
	listed := 0
	for _, record := range s.writes {
		if record.decided {
			continue
		}
		listed++
		fmt.Fprintf(&out, "- %s: %s, %s. %s\n", record.pending.ID, record.pending.Write.Describe(), writtenWhen(record.pending.WrittenAt, record.pending.Turn), s.whereItStands(record))
	}
	if listed == 0 {
		out.WriteString("None is waiting.\n")
	}
	if len(s.state.HandedBack) > 0 {
		out.WriteString("\nHanded back to you, and no longer waiting; your next revision of each opens a fresh run:\n")
		for _, back := range s.state.HandedBack {
			fmt.Fprintf(&out, "- %s: %s, %s. Handed back by run %s: %s\n", back.ID, back.Artifact, writtenWhen(back.Written, back.Turn), back.RunID, back.Reason)
		}
	}
	return out.String() + "\n"
}

// whereItStands is one waiting document's state in the words the role reads.
func (s *Session) whereItStands(record *writeRecord) string {
	standing := s.standingOf(record)
	switch {
	case standing.Running():
		return fmt.Sprintf("Running: run %s is publishing it.", standing.RunID)
	case standing.HandedBack():
		return fmt.Sprintf("Handed back: run %s ended without landing it: %s", standing.RunID, oneline.Fold(strings.TrimSpace(standing.Delivery.Detail), runstate.MaxHandedBackReasonBytes))
	case standing.Finished():
		return fmt.Sprintf("Passed in run %s; the harness tells you how it lands.", standing.RunID)
	}
	behind, run := s.waitingBehind(record)
	return "Waiting to start: " + s.waitingReason(record, behind, run) + "."
}

// writtenWhen says when a document was written, in local time with its zone.
func writtenWhen(at time.Time, turn int) string {
	if at.IsZero() {
		return fmt.Sprintf("written at turn %d", turn)
	}
	return fmt.Sprintf("written at turn %d, %s", turn, at.Local().Format("Jan 2 15:04 MST"))
}

// answerDocumentRequests carries out the block's withdrawals and its request
// for a list, through the same audit and grant as the role's document writes:
// a role that may not write a document is refused both, and the refusal is
// handed back to it rather than ending the turn.
func (s *Session) answerDocumentRequests(parsed parsedReply) (string, error) {
	count := len(parsed.Withdraw)
	if parsed.ListDocuments {
		count++
	}
	if count == 0 {
		return "", nil
	}
	answer, err := auditTool(context.Background(), s, s.writeTool(), map[string]any{"withdraw": len(parsed.Withdraw), "list": parsed.ListDocuments}, count, func() (string, error) {
		answer, err := s.withdrawWrites(parsed.Withdraw)
		if err != nil {
			return answer, err
		}
		if parsed.ListDocuments {
			answer += s.renderWaitingDocuments()
		}
		return answer, nil
	}, nil)
	if err != nil && strings.Contains(err.Error(), "audit failed") {
		return "", err
	}
	if err != nil {
		return fmt.Sprintf("# Document requests refused\n\nNothing was withdrawn or listed: %v.\n\n", err), nil
	}
	return answer, nil
}
