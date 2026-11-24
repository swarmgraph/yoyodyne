package chat

// A hold an owning role puts on sending a document, and the notes a finished
// document leaves on the work items it answers.
//
// A role that has reason to wait before sending documents — a fix to
// publication itself not yet merged, say — still owes the design or ruling it
// was handed. Without a way to say "finished, send it later", the only hold a
// role could keep was one in its own memory, which held the work back with the
// sending, lifted only when somebody noticed, and let items it had not designed
// read as ruled. So the hold is put on one document: the document is written,
// kept in the conversation's record ready to send, and sent at the first
// message after the work item the hold names closes. Nothing else about it
// waits — in particular its text reaches the items it answers as soon as it is
// written, so a developer can start from it before it is published.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// heldForSending reports a document kept under its owner's hold on sending,
// whose hold this process has not seen lifted, and that is not yet confirmed.
func (r *writeRecord) heldForSending() bool {
	return r.pending.Write.Hold != nil && r.pending.Publication == nil && !r.holdLifted
}

// keptForRoom reports a document the harness kept, rather than refused, because
// the conversation already had runstate.MaxPendingWrites documents waiting, and
// that is not yet let in among them (PublishDocuments).
func (r *writeRecord) keptForRoom() bool {
	return r.pending.WaitsForRoom && r.pending.Publication == nil
}

// heldApart reports a document counted with the held documents rather than the
// waiting ones: held for sending, or kept until there is room. It counts as the
// record does (runstate.PendingWrite.Held).
func (r *writeRecord) heldApart() bool {
	return (r.pending.Write.Hold != nil || r.pending.WaitsForRoom) && r.pending.Publication == nil
}

// HeldDocument is one document a conversation keeps ready to send under a hold
// its owner put on sending it: which document, the items it answers, and what
// lifts the hold.
type HeldDocument struct {
	WriteID     string   `json:"write_id"`
	Artifact    string   `json:"artifact"`
	Answers     []string `json:"answers,omitempty"`
	UntilClosed string   `json:"until_closed"`
	Reason      string   `json:"reason"`
}

// HeldDocuments is every document this conversation keeps held for sending.
// It is what says an item's design or ruling is finished and waiting to be
// sent, rather than ruled or not yet done.
func (s *Session) HeldDocuments() []HeldDocument {
	var held []HeldDocument
	for _, record := range s.writes {
		if record.decided || !record.heldForSending() {
			continue
		}
		write := record.pending.Write
		held = append(held, HeldDocument{
			WriteID:     record.pending.ID,
			Artifact:    strings.TrimSpace(write.ID),
			Answers:     trimmedItems(write.Answers),
			UntilClosed: strings.TrimSpace(write.Hold.UntilClosed),
			Reason:      strings.TrimSpace(write.Hold.Reason),
		})
	}
	return held
}

// refuseWorkItemReferences refuses a write naming a work item the tracker does
// not hold, in what it answers or in its hold: a note nobody can write and a
// hold nothing can lift are both answers that will not change by asking again.
// A hold needs a tracker to be lifted at all, so a conversation without one
// refuses it.
func (s *Session) refuseWorkItemReferences(ctx context.Context, writes []artifact.Write) error {
	var problems []error
	for _, write := range writes {
		var named []string
		named = append(named, trimmedItems(write.Answers)...)
		if write.Hold != nil {
			named = append(named, strings.TrimSpace(write.Hold.UntilClosed))
		}
		if len(named) == 0 {
			continue
		}
		if s.options.Tracker == nil {
			return errors.New("no tracker is configured for this conversation, so nothing could note the document on the items it answers or see its hold lift")
		}
		for _, id := range named {
			if err := beads.ValidateIssueID(id); err != nil {
				problems = append(problems, fmt.Errorf("work item %q: %w", id, err))
				continue
			}
			if _, err := s.options.Tracker.Show(ctx, id); err != nil {
				problems = append(problems, fmt.Errorf("work item %s could not be read: %w", id, err))
			}
		}
	}
	return errors.Join(problems...)
}

// maxRulingNoteBodyBytes is how much of a document its note on an answered item
// carries. A design runs longer than a note should, so a longer one is cut and
// the note says where the whole of it is kept.
const maxRulingNoteBodyBytes = 16 << 10

// noteAnsweredItems writes each recorded document into the notes of the work
// items it answers, as it is written: what it is, where it is kept, whether
// sending it is held and until what, and its text. A note that could not be
// written is told to the owning role and stops nothing. Only a failure to
// record the conversation is returned.
func (s *Session) noteAnsweredItems(ctx context.Context, written []PendingWrite) error {
	var failures strings.Builder
	for _, pending := range written {
		answers := trimmedItems(pending.Write.Answers)
		if len(answers) == 0 || s.options.Tracker == nil {
			continue
		}
		note := s.rulingNote(pending)
		for _, id := range answers {
			if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
				fmt.Fprintf(&failures, "Document %s (%s) could not be noted on %s, which it answers: %v. The document itself is kept.\n", pending.ID, pending.Write.Title, id, err)
			}
		}
	}
	if failures.Len() == 0 {
		return nil
	}
	return s.carryResults(failures.String())
}

// rulingNote is what an answered item carries about the document written for
// it. It is read by a developer about to start the item, so it says first
// whether the document is published yet and then gives its text.
func (s *Session) rulingNote(pending PendingWrite) string {
	write := pending.Write
	title := strings.TrimSpace(write.Title)
	if title == "" {
		title = strings.TrimSpace(write.ID)
	}
	var note strings.Builder
	fmt.Fprintf(&note, "%s\n\n", s.trackerProvenance(fmt.Sprintf("%s%s (%s, document %s)", runstate.RulingNoteOpening, strings.TrimSpace(write.ID), title, pending.ID), write.Reason))
	if hold := write.Hold; hold != nil {
		fmt.Fprintf(&note, "It is finished and kept ready to send. Sending it for publication waits until %s closes: %s\n\n", strings.TrimSpace(hold.UntilClosed), strings.TrimSpace(hold.Reason))
	} else if pending.WaitsForRoom {
		note.WriteString("It is finished and kept until there is room among the documents waiting for publication, and is sent then. Until it is published, the text below is what it says.\n\n")
	} else {
		note.WriteString("It is finished and sent for publication. Until it is published, the text below is what it says.\n\n")
	}
	body := strings.TrimSpace(write.Body)
	if len(body) > maxRulingNoteBodyBytes {
		cut := maxRulingNoteBodyBytes
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		body = strings.TrimSpace(body[:cut]) + fmt.Sprintf("\n\n[The rest is cut here; the whole document is kept as %s in conversation %s.]", pending.ID, pending.ConversationID)
	}
	note.WriteString(body)
	return note.String()
}

// liftedHold reads whether a held document's hold has lifted, and tells the
// owning role once where it has not: that the document is kept ready to send,
// and what it waits for. A tracker that would not answer keeps the hold and is
// told likewise; the hold is read again at the next message.
func (s *Session) liftedHold(ctx context.Context, record *writeRecord) (bool, error) {
	hold := record.pending.Write.Hold
	until := strings.TrimSpace(hold.UntilClosed)
	title := strings.TrimSpace(record.pending.Write.Title)
	if title == "" {
		title = strings.TrimSpace(record.pending.Write.ID)
	}
	var waiting string
	if s.options.Tracker == nil {
		waiting = fmt.Sprintf("Document %s (%s) is finished and kept, ready to send. It is held until %s closes (%s), and no tracker is configured to see it close; it does not need writing again.\n", record.pending.ID, title, until, strings.TrimSpace(hold.Reason))
	} else if item, err := s.options.Tracker.Show(ctx, until); err != nil {
		waiting = fmt.Sprintf("Document %s (%s) is finished and kept, ready to send. It is held until %s closes, and the tracker could not say whether it has: %v. It is read again at the next message; the document does not need writing again.\n", record.pending.ID, title, until, err)
	} else if strings.TrimSpace(item.Status) == "closed" {
		record.holdLifted = true
		return true, s.carryResults(fmt.Sprintf("Document %s (%s) was held until %s closed. %s has closed, so the hold is lifted and the document goes on from the text the harness kept, as any other document does; it does not need writing again.\n", record.pending.ID, title, until, until))
	} else {
		waiting = fmt.Sprintf("Document %s (%s) is finished and kept, ready to send. It is held until %s closes, because: %s. The harness sends it at the first message after that; it does not need writing again, and the items it answers are held for sending, not ruled.\n", record.pending.ID, title, until, strings.TrimSpace(hold.Reason))
	}
	if waiting == record.failure {
		return false, nil
	}
	record.failure = waiting
	return false, s.carryResults(waiting)
}

// tellHeldForRoom tells the owning role once that a held document stays held
// while the conversation already has runstate.MaxPendingWrites documents
// waiting for publication, and is let go once one of them is settled.
func (s *Session) tellHeldForRoom(record *writeRecord) error {
	title := strings.TrimSpace(record.pending.Write.Title)
	if title == "" {
		title = strings.TrimSpace(record.pending.Write.ID)
	}
	waiting := fmt.Sprintf("Document %s (%s) is finished and kept, ready to send, and held until %s closes. This conversation already has %d documents waiting for publication, so it stays held until one of them is settled, and is sent at the first message after that once its hold has lifted; it does not need writing again.\n", record.pending.ID, title, strings.TrimSpace(record.pending.Write.Hold.UntilClosed), runstate.MaxPendingWrites)
	if waiting == record.failure {
		return nil
	}
	record.failure = waiting
	return s.carryResults(waiting)
}

// waitingAndHeld counts this conversation's undecided documents apart from the
// ones held for sending or kept until there is room, which
// runstate.MaxHeldWrites bounds separately. It
// counts as the record does (runstate.PendingWrite.Held), so a hold seen lifted
// in this process still counts as held until its document is confirmed.
//
// Waiting documents are counted as documents rather than revisions: a document
// whose run is going and a newer revision waiting behind it hold one place
// (runstate.WaitingDocuments).
func (s *Session) waitingAndHeld() (waiting, held int) {
	return s.waitingAndHeldWithout(nil)
}

// waitingAndHeldWithout counts as waitingAndHeld does, leaving out the waiting
// revisions a new revision is about to free.
func (s *Session) waitingAndHeldWithout(frees func(*writeRecord) bool) (waiting, held int) {
	documents := map[string]bool{}
	for _, record := range s.writes {
		switch {
		case record.decided || (frees != nil && frees(record)):
		case record.heldApart():
			held++
		default:
			documents[strings.TrimSpace(record.pending.Write.ID)] = true
		}
	}
	return len(documents), held
}

// refuseWriteCount refuses documents beyond what the conversation may keep:
// runstate.MaxPendingWrites waiting for a decision or publication, and
// runstate.MaxHeldWrites held for sending or kept until there is room.
//
// A document written while the waiting places are full is not refused: it is
// kept until one of them is settled (recordWritesWithoutToolAudit marks it, and
// PublishDocuments lets it in), and counted with the held documents meanwhile,
// so the role never has to write a finished document again because the limit
// was full. It is refused only where that allowance is full too.
//
// A new revision of a document already waiting takes no new place, and what it
// frees — a revision whose run has not started or has ended — is counted as
// given back.
func (s *Session) refuseWriteCount(writes []artifact.Write, places []revisionPlace) error {
	waitingDocuments := map[string]bool{}
	frees := func(record *writeRecord) bool {
		for _, place := range places {
			if place.frees(record) {
				return true
			}
		}
		return false
	}
	_, held := s.waitingAndHeldWithout(frees)
	for _, record := range s.writes {
		if !record.decided && !frees(record) && !record.heldApart() {
			waitingDocuments[strings.TrimSpace(record.pending.Write.ID)] = true
		}
	}
	keptForRoom := 0
	for _, write := range writes {
		id := strings.TrimSpace(write.ID)
		switch {
		case write.Hold != nil:
			held++
		case waitingDocuments[id]:
		case len(waitingDocuments) < runstate.MaxPendingWrites:
			waitingDocuments[id] = true
		default:
			held++
			keptForRoom++
		}
	}
	if keptForRoom > 0 && held > runstate.MaxHeldWrites {
		return fmt.Errorf("%d documents are already waiting for confirmation or publication, which is the limit of %d per role, and the %d places for documents kept until there is room or held for sending are full too. Wait for one to land or be handed back, write a new revision of one already waiting (it replaces the waiting revision if its run has not started), or withdraw one whose run has not started with \"withdraw\"; \"list\" shows each and where it stands",
			len(waitingDocuments), runstate.MaxPendingWrites, runstate.MaxHeldWrites)
	}
	// A document confirmed for publication is stored twice, so a new one is
	// counted at twice its length; held documents are stored once.
	stored := 0
	for _, record := range s.writes {
		if !record.decided && !frees(record) {
			stored += runstate.PendingWriteStoredBytes([]runstate.PendingWrite{record.pending.recorded()})
		}
	}
	for _, write := range writes {
		if write.Hold != nil {
			stored += len(write.Body)
		} else {
			stored += 2 * len(write.Body)
		}
	}
	if stored > runstate.MaxPendingWriteStoredBytes {
		return fmt.Errorf("the documents waiting would take %d bytes of the %d bytes one conversation may keep, counting each confirmed document twice. Wait for one to land or be handed back, or withdraw one whose run has not started with \"withdraw\"; \"list\" shows each and where it stands",
			stored, runstate.MaxPendingWriteStoredBytes)
	}
	if held > runstate.MaxHeldWrites {
		return fmt.Errorf("%d document(s) would be held for sending or kept until there is room, and the limit is %d; a document that answers several items names them all in one, and the held ones are sent as their holds lift",
			held, runstate.MaxHeldWrites)
	}
	return nil
}

// tellKeptForRoom tells the owning role once that a document kept because the
// waiting places were full is still kept, and is sent once one of them is
// settled.
func (s *Session) tellKeptForRoom(record *writeRecord) error {
	waiting := fmt.Sprintf("Document %s (%s) is finished and kept. This conversation already has %d documents waiting for publication, so it is sent at the first message after one of them is settled; it does not need writing again.\n", record.pending.ID, writeTitle(record), runstate.MaxPendingWrites)
	if waiting == record.failure {
		return nil
	}
	record.failure = waiting
	return s.carryResults(waiting)
}

// letInForRoom lets a document kept for room in among the waiting documents
// once there is room, records that, and tells the owning role. From here it is
// confirmed and published as any other document is.
func (s *Session) letInForRoom(record *writeRecord) error {
	record.pending.WaitsForRoom = false
	record.failure = ""
	if err := s.record(); err != nil {
		return err
	}
	return s.carryResults(fmt.Sprintf("Document %s (%s) was kept because the documents waiting for publication were at their limit. One of them is settled, so it now takes that place and goes on from the text the harness kept; it does not need writing again.\n", record.pending.ID, writeTitle(record)))
}

// writeTitle is a document's title, or its identifier where it has none.
func writeTitle(record *writeRecord) string {
	if title := strings.TrimSpace(record.pending.Write.Title); title != "" {
		return title
	}
	return strings.TrimSpace(record.pending.Write.ID)
}

func trimmedItems(items []string) []string {
	var trimmed []string
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			trimmed = append(trimmed, item)
		}
	}
	return trimmed
}
