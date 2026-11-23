// Package designagreement is the wording of the note the harness adds to a work
// item when the architect records that a revision of a document agrees with it
// (docs/designs/unpublished-designs-and-admitted-work.md, section 5).
//
// The architect's "agree" action writes the note (internal/chat) and the list
// of unpublished documents that overlap open work reads it back
// (internal/orchestrator). Both use this one wording, so the note the action
// writes is always one the list takes as answering the overlap.
package designagreement

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// Prefix opens the note. Only a note that opens with it and has exactly the
// shape Note writes is read as an agreement.
const Prefix = "Design agreement: "

var shape = regexp.MustCompile(`^Design agreement: the architect checked revision (\S+) of (\S+) against this item as it stood at (\S+), and found that they agree\. Recorded (\S+)\.$`)

// Note is the note recording that the architect checked the revision of the
// document against the item as it stood at stood, the time the tracker last
// changed the item when she checked it, and found that they agree, written at
// recorded.
func Note(document, revision string, stood, recorded time.Time) string {
	return fmt.Sprintf("%sthe architect checked revision %s of %s against this item as it stood at %s, and found that they agree. Recorded %s.",
		Prefix, revision, document, stood.UTC().Format(time.RFC3339), recorded.UTC().Format(time.RFC3339))
}

// Agreement is one agreement note read back from an item.
type Agreement struct {
	Revision string
	Document string
	Stood    time.Time
	Recorded time.Time
}

// Read reads text as an agreement note, and reports whether it is one.
func Read(text string) (Agreement, bool) {
	found := shape.FindStringSubmatch(strings.TrimSpace(text))
	if found == nil {
		return Agreement{}, false
	}
	stood, err := time.Parse(time.RFC3339, found[3])
	if err != nil {
		return Agreement{}, false
	}
	recorded, err := time.Parse(time.RFC3339, found[4])
	if err != nil {
		return Agreement{}, false
	}
	return Agreement{Revision: found[1], Document: found[2], Stood: stood, Recorded: recorded}, true
}

// settle is how long after an agreement note's own time the tracker's
// last-changed time for the item may stand and still be the note's own write
// rather than a later change: the note is stamped before the tracker records it.
const settle = time.Minute

// ItemChanged is an item's last-changed time as a role is shown it and gives it
// back, in the operator's local zone.
func ItemChanged(at time.Time) string { return at.Local().Format(time.RFC3339) }

// newest is the agreement notes nothing has followed on an item last changed at
// updated, newest first. Each was written while the item stood as the one before
// it left it, so walking back from the item as it is ends at the first note that
// is not an agreement, or the first agreement something else changed the item
// after.
func newest(notes string, updated time.Time) []Agreement {
	records := beads.NoteRecords(notes)
	var found []Agreement
	for i := len(records) - 1; i >= 0; i-- {
		if strings.TrimSpace(records[i].Text) == "" {
			continue
		}
		note, ok := Read(records[i].Text)
		if !ok || updated.After(note.Recorded.Add(settle)) {
			break
		}
		found = append(found, note)
		updated = note.Stood
	}
	return found
}

// Unchanged reports whether an item with these recorded notes, last changed at
// updated, is as it stood at seen but for agreement notes added since, so two
// documents can be agreed with one item from one reading of it.
func Unchanged(notes string, updated, seen time.Time) bool {
	seen = seen.Truncate(time.Second)
	if updated.Truncate(time.Second).Equal(seen) {
		return true
	}
	for _, note := range newest(notes, updated) {
		if note.Stood.Equal(seen) {
			return true
		}
	}
	return false
}

// Stands reports whether the item carries an agreement naming the revision of
// the document that nothing but later agreements has followed. An item the
// tracker gave no last-changed time for cannot be placed, and carries none.
func Stands(notes string, updated time.Time, document, revision string) bool {
	if updated.IsZero() {
		return false
	}
	for _, note := range newest(notes, updated) {
		if note.Document == document && note.Revision == revision {
			return true
		}
	}
	return false
}
