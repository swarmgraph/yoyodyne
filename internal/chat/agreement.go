package chat

// The architect recording that a revision of a document agrees with a work item.
//
// A design not yet published is checked against the admitted work it touches
// (docs/designs/unpublished-designs-and-admitted-work.md). One way the architect
// answers an overlap is to say she checked the document against the item and
// found they agree. The architect reads the tracker and writes none of it, so
// the note is written by the harness, from her "agree" action, and says only
// what the harness itself verified: that the item exists, that the document
// exists, that the revision is one the document has, and when the item was read.
// Notes are only added to, so the item's earlier notes are never touched.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// DocumentRevisions is the part of a document store that says how many
// revisions a landed document has. The store the CLI hands a conversation has it;
// one without it cannot confirm a revision exists, so an agreement is refused
// rather than recorded on the architect's word alone.
type DocumentRevisions interface {
	RevisionCount(id string) (count int, found bool, err error)
}

// agreementClause is what the architect is told about the "agree" action.
const agreementClause = `When a document of yours that is not yet published overlaps an admitted work item and you have read the item's decisions against what the document now says and they agree, record it with "agree": {"action":"agree","id":"beads-id","document":"document-id","revision":2,"reason":"why they agree"}. "document" is the document's identifier and "revision" its number in its own revision log, counting from zero. The harness refuses the action for an item or a document that does not exist and for a revision the document does not have; otherwise it adds one note to the item, naming the document, the revision, and the time it read the item, and changes nothing else on the item. A newer note on the item or a newer revision of the document makes the overlap stand again, so record agreement for the revision you actually checked. If they do not agree, do not record it: revise the document, or tell the Lead Product Manager which part conflicts.`

// agreementProblems checks an "agree" action names a document and a revision.
// Whether either exists needs the stores, so that is asked as it is carried out.
func (a TrackerAction) agreementProblems() []error {
	var problems []error
	if strings.TrimSpace(a.Document) == "" {
		problems = append(problems, errors.New("agree requires \"document\", the identifier of the document that agrees with the item"))
	}
	switch {
	case a.Revision == nil:
		problems = append(problems, errors.New("agree requires \"revision\", the document's revision number, counting from zero"))
	case *a.Revision < 0:
		problems = append(problems, errors.New("agree \"revision\" is a revision number and cannot be negative"))
	}
	return problems
}

// documentRevisionCount is how many revisions the named document has, counting
// the ones landed in the repository and the ones this conversation has waiting
// to land, and whether the document is known at all.
func (s *Session) documentRevisionCount(id string) (int, bool, error) {
	count, found := 0, false
	if lookup, ok := s.options.Documents.(DocumentRevisions); ok {
		landed, known, err := lookup.RevisionCount(id)
		if err != nil {
			return 0, false, err
		}
		if known {
			count, found = landed, true
		}
	} else if s.options.Documents != nil {
		return 0, false, errors.New("this conversation's document store cannot say how many revisions a document has")
	}
	for _, waiting := range s.state.PendingWrites {
		if waiting.Artifact == id {
			count++
			found = true
		}
	}
	return count, found, nil
}

// agreementNote is the note the harness adds. It names the document, its
// revision, and the time the item was read, in the operator's local zone and in
// UTC so a later reader can compare it with another entry exactly.
func (s *Session) agreementNote(item beads.WorkItem, document string, revision int, readAt time.Time, reason string) string {
	note := fmt.Sprintf("Agreement recorded by the harness from the %s's action in conversation %s, after turn %d: the %s checked revision %d of the document %s against this item as it stood at %s (%s) and found that they agree.",
		RoleTitle(s.state.Role), s.state.ConversationID, s.state.Turns, RoleTitle(s.state.Role), revision, document,
		readAt.Local().Format("2006-01-02 15:04 MST"), readAt.UTC().Format(time.RFC3339))
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		note += "\n\nReason: " + trimmed
	}
	return note
}

// carryOutAgreement writes the one note. Everything it records was checked
// first, and a failure writes nothing.
func (s *Session) carryOutAgreement(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	document := strings.TrimSpace(action.Document)
	revision := *action.Revision
	if s.options.Documents == nil {
		outcome.Failure = "this conversation was opened without the documents' store, so it cannot confirm a document exists and nothing was recorded"
		return
	}
	// The item is read for this action by applyTrackerAction; an item the tracker
	// would not describe is not one that is known to exist.
	if outcome.target == nil {
		reason := outcome.TargetUnread
		if reason == "" {
			reason = "the tracker did not describe it"
		}
		outcome.Failure = fmt.Sprintf("%s could not be read, so nothing was recorded: %s", outcome.WorkItemID, reason)
		return
	}
	count, found, err := s.documentRevisionCount(document)
	if err != nil {
		outcome.fail(err)
		return
	}
	if !found {
		outcome.Failure = fmt.Sprintf("there is no document %q, so nothing was recorded; name a document by its identifier", singleLine(document, maxTrackerFailureBytes))
		return
	}
	if revision >= count {
		outcome.Failure = fmt.Sprintf("%s has %d revision(s), numbered from zero, so there is no revision %d to record agreement for", document, count, revision)
		return
	}
	readAt := s.options.clock().Now()
	change := beads.WorkItemChange{AppendNotes: s.agreementNote(*outcome.target, document, revision, readAt, action.Reason)}
	if _, err := s.options.Tracker.Update(ctx, outcome.WorkItemID, change); err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("recorded on %s that revision %d of %s agrees with it, as it stood at %s", outcome.WorkItemID, revision, document, readAt.Local().Format("15:04 MST"))
}
