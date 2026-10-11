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
	fmt.Fprintf(&note, "%s\n\n", s.trackerProvenance(fmt.Sprintf("Design or ruling for this item written as %s (%s, document %s)", strings.TrimSpace(write.ID), title, pending.ID), write.Reason))
	if hold := write.Hold; hold != nil {
		fmt.Fprintf(&note, "It is finished and kept ready to send. Sending it for publication waits until %s closes: %s\n\n", strings.TrimSpace(hold.UntilClosed), strings.TrimSpace(hold.Reason))
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

// waitingAndHeld counts this conversation's undecided documents apart from the
// ones held for sending, which runstate.MaxHeldWrites bounds separately. It
// counts as the record does (runstate.PendingWrite.Held), so a hold seen lifted
// in this process still counts as held until its document is confirmed.
func (s *Session) waitingAndHeld() (waiting, held int) {
	for _, record := range s.writes {
		switch {
		case record.decided:
		case record.pending.Write.Hold != nil && record.pending.Publication == nil:
			held++
		default:
			waiting++
		}
	}
	return waiting, held
}

// refuseWriteCount refuses documents beyond what the conversation may keep:
// runstate.MaxPendingWrites waiting for a decision or publication, and
// runstate.MaxHeldWrites held for sending.
func (s *Session) refuseWriteCount(writes []artifact.Write) error {
	waiting, held := s.waitingAndHeld()
	for _, write := range writes {
		if write.Hold != nil {
			held++
		} else {
			waiting++
		}
	}
	if waiting > runstate.MaxPendingWrites {
		return fmt.Errorf("%d document(s) would be waiting for confirmation or publication and the limit is %d; finish those before writing another",
			waiting, runstate.MaxPendingWrites)
	}
	if held > runstate.MaxHeldWrites {
		return fmt.Errorf("%d document(s) would be held for sending and the limit is %d; a document that answers several items names them all in one, and the held ones are sent as their holds lift",
			held, runstate.MaxHeldWrites)
	}
	return nil
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
