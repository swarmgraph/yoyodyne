package chat

// The architect recording that a revision of a document agrees with a work item.
//
// A design not yet published is checked against the admitted work it touches
// (docs/designs/unpublished-designs-and-admitted-work.md). One way the architect
// answers an overlap is to say she checked the document against the item and
// found they agree. The architect reads the tracker and writes none of it, so
// the note is written by the harness, from her "agree" action, and says only
// what the harness itself verified: that the item exists, that the document
// exists, that the revision is one the document has, and that the item is still
// as it stood at the time she was shown when she checked it. Notes are only added to, so the item's
// earlier notes are never touched.
//
// The note is written in the one wording the overlap list reads back
// (internal/designagreement), so recording agreement is what takes the
// overlap off the list.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/designagreement"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DocumentRevisions is the part of a document store that says how many
// revisions a landed document has. The store the CLI hands a conversation has it;
// one without it cannot confirm a revision exists, so an agreement is refused
// rather than recorded on the architect's word alone.
type DocumentRevisions interface {
	RevisionCount(id string) (count int, found bool, err error)
}

// AgreedRevision is the revision an "agree" action names: either its number in
// the document's own revision log, counting from zero as an approval counts, or
// the identifier of a revision still waiting to be published, such as
// "document-3211.1", which is how the overlap list names one. In JSON it is a
// number or a string.
type AgreedRevision struct {
	Number *int
	ID     string
}

// UnmarshalJSON reads a number as a revision number and a string as a waiting
// revision's identifier.
func (r *AgreedRevision) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var id string
		if err := json.Unmarshal(data, &id); err != nil {
			return err
		}
		*r = AgreedRevision{ID: strings.TrimSpace(id)}
		return nil
	}
	var number int
	if err := json.Unmarshal(data, &number); err != nil {
		return errors.New(`"revision" is a revision number or the identifier of a revision waiting to be published`)
	}
	*r = AgreedRevision{Number: &number}
	return nil
}

// MarshalJSON writes the revision back the way it was given.
func (r AgreedRevision) MarshalJSON() ([]byte, error) {
	if r.Number != nil {
		return json.Marshal(*r.Number)
	}
	return json.Marshal(r.ID)
}

// agreementClause is what the architect is told about the "agree" action.
const agreementClause = `When a document not yet published, whichever role wrote it, overlaps an admitted work item and you have read the item's decisions against what the document now says and they agree, record it with "agree": {"action":"agree","id":"beads-id","document":"document-id","revision":"document-3211.1","item_changed":"2026-11-20T11:30:37-08:00","reason":"why they agree"}. "document" is the document's identifier. "revision" is the revision you checked: the identifier the list of overlaps names it by, such as "document-3211.1", or, for one already landed, its number in the document's own revision log, counting from zero. "item_changed" is when the item last changed as you checked it: copy it from the list of overlaps or from your read of the item, whichever you checked the document against. The harness refuses the action for an item or a document that does not exist, for a revision the document does not have, for an item whose last change the tracker does not date, and for an item that has changed since that time, other than by agreement notes; then read the item again and check it as it is now. Otherwise it adds one note to the item, naming the document, the revision, and that time, and changes nothing else on the item. A newer entry on the item or a newer revision of the document makes the overlap stand again. If they do not agree, do not record it: revise the document where it is yours, or tell the Lead Product Manager which part conflicts.`

// agreementProblems checks an "agree" action names a document and a revision.
// Whether either exists needs the stores, so that is asked as it is carried out.
func (a TrackerAction) agreementProblems() []error {
	var problems []error
	if strings.TrimSpace(a.Document) == "" {
		problems = append(problems, errors.New("agree requires \"document\", the identifier of the document that agrees with the item"))
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(a.ItemChanged)); err != nil {
		problems = append(problems, errors.New("agree requires \"item_changed\", the time the item last changed, exactly as the list of overlaps or your read of the item printed it"))
	}
	switch {
	case a.Revision == nil || (a.Revision.Number == nil && a.Revision.ID == ""):
		problems = append(problems, errors.New("agree requires \"revision\", the revision's number counting from zero, or the identifier the list of overlaps names it by"))
	case a.Revision.Number != nil && *a.Revision.Number < 0:
		problems = append(problems, errors.New("agree \"revision\" is a revision number and cannot be negative"))
	}
	return problems
}

// documentRevisions is what the records say of the named document's revisions:
// how many have landed in the repository, the ones waiting to be published in
// any role's conversation, and whether the document is known at all. The waiting
// ones are what the list of overlaps reads (runstate.UnpublishedWrites), with
// this conversation's own as it stands now rather than as last saved.
func (s *Session) documentRevisions(id string) (landed int, waiting []runstate.PendingWrite, found bool, err error) {
	lookup, ok := s.options.Documents.(DocumentRevisions)
	if !ok {
		return 0, nil, false, errors.New("this conversation's document store cannot say how many revisions a document has")
	}
	if landed, found, err = lookup.RevisionCount(id); err != nil {
		return 0, nil, false, err
	}
	recorded, err := s.options.Store.Recorded()
	if err != nil {
		return 0, nil, false, fmt.Errorf("the conversations' records could not be read, so no waiting revision could be found: %w", err)
	}
	conversations := []runstate.Conversation{{PendingWrites: s.undecidedWrites(), HandedBack: s.state.HandedBack}}
	for _, conversation := range recorded {
		if conversation.ConversationID != s.state.ConversationID {
			conversations = append(conversations, conversation)
		}
	}
	for _, write := range runstate.UnpublishedWrites(conversations) {
		if strings.TrimSpace(write.Artifact) == id {
			waiting = append(waiting, write)
		}
	}
	return landed, waiting, found || len(waiting) > 0, nil
}

// itemChangedLine tells a role that may record agreement the "item_changed" time
// of the item it is reading. It is empty where the tracker gave no time.
func itemChangedLine(item beads.WorkItem) string {
	if item.UpdatedAt.IsZero() {
		return ""
	}
	return fmt.Sprintf("item_changed: %s (when the tracker last changed this item, for an \"agree\" action)\n", designagreement.ItemChanged(item.UpdatedAt))
}

// agreedRevisionName is what the note calls the revision: the identifier of a
// revision waiting to be published, which is how the list of overlaps names it,
// or the number of one already landed, which no overlap lists. It is empty, with
// the reason, where the document has no such revision.
func agreedRevisionName(document string, revision AgreedRevision, landed int, waiting []runstate.PendingWrite) (string, string) {
	if revision.Number == nil {
		for _, write := range waiting {
			if write.ID == revision.ID {
				return write.ID, ""
			}
		}
		return "", fmt.Sprintf("%s has no revision waiting to be published named %q, so nothing was recorded; name the revision as the list of overlaps names it, or by its number", document, singleLine(revision.ID, maxTrackerFailureBytes))
	}
	if number := *revision.Number; number >= landed {
		return "", fmt.Sprintf("%s has %d landed revision(s), numbered from zero, so there is no revision %d to record agreement for; a revision waiting to be published is named by its identifier", document, landed, number)
	}
	return strconv.Itoa(*revision.Number), ""
}

// carryOutAgreement writes the one note. Everything it records was checked
// first, and a failure writes nothing.
func (s *Session) carryOutAgreement(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	document := strings.TrimSpace(action.Document)
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
	landed, waiting, found, err := s.documentRevisions(document)
	if err != nil {
		outcome.fail(err)
		return
	}
	if !found {
		outcome.Failure = fmt.Sprintf("there is no document %q, so nothing was recorded; name a document by its identifier", singleLine(document, maxTrackerFailureBytes))
		return
	}
	revision, refused := agreedRevisionName(document, *action.Revision, landed, waiting)
	if refused != "" {
		outcome.Failure = refused
		return
	}
	if outcome.target.UpdatedAt.IsZero() {
		outcome.Failure = fmt.Sprintf("the tracker does not say when %s last changed, so the note could not name the item as it was checked, and nothing was recorded", outcome.WorkItemID)
		return
	}
	// The note vouches for the item she read, so an item that has changed since,
	// other than by agreement notes like this one, is refused. The refusal does
	// not give the current time: she gets it by reading the item.
	stood, err := time.Parse(time.RFC3339, strings.TrimSpace(action.ItemChanged))
	if err != nil {
		outcome.fail(err)
		return
	}
	if !designagreement.Unchanged(outcome.target.RecordedNotes(), outcome.target.UpdatedAt, stood) {
		outcome.Failure = fmt.Sprintf("%s is not as it stood at the \"item_changed\" time given, %s, so nothing was recorded; read the item again, check the document against it as it is now, and give the time that read prints", outcome.WorkItemID, stood.Local().Format("2006-01-02 15:04 MST"))
		return
	}
	note := designagreement.Note(document, revision, stood, s.options.clock().Now())
	if _, err := s.options.Tracker.Update(ctx, outcome.WorkItemID, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("recorded on %s that revision %s of %s agrees with it as it stood when it last changed, at %s", outcome.WorkItemID, revision, document, stood.Local().Format("2006-01-02 15:04 MST"))
}
