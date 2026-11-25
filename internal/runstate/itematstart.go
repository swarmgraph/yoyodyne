package runstate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ItemAtStart is what the run's work item said when the run was started on it,
// kept as digests rather than text: it is compared with the item as it stands
// when a repair starts, to tell the developer what changed and which notes were
// added since. A run recorded before this was kept carries none.
type ItemAtStart struct {
	Title              string `json:"title"`
	Description        string `json:"description"`
	Design             string `json:"design"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	// Notes is the digest of the notes' first NotesBytes bytes, which were all of
	// them then. Notes are only ever added to, so the notes added since are what
	// follows, unless the digest says the earlier ones were rewritten.
	Notes      string `json:"notes"`
	NotesBytes int    `json:"notes_bytes"`
}

// ItemTextDigest is the digest ItemAtStart keeps of one field. Surrounding
// space is not part of what a field says.
func ItemTextDigest(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

// NewItemAtStart records an item's text.
func NewItemAtStart(title, description, design, acceptance, notes string) *ItemAtStart {
	return &ItemAtStart{
		Title:              ItemTextDigest(title),
		Description:        ItemTextDigest(description),
		Design:             ItemTextDigest(design),
		AcceptanceCriteria: ItemTextDigest(acceptance),
		Notes:              ItemTextDigest(notes),
		NotesBytes:         len(strings.TrimSpace(notes)),
	}
}

// NotesSince is what notes hold beyond the ones the run started with, and
// whether the earlier ones are still there unchanged. Where they are not, every
// note is returned, because nothing says which of them are new.
func (i ItemAtStart) NotesSince(notes string) (string, bool) {
	notes = strings.TrimSpace(notes)
	if len(notes) < i.NotesBytes || ItemTextDigest(notes[:i.NotesBytes]) != i.Notes {
		return notes, false
	}
	return strings.TrimSpace(notes[i.NotesBytes:]), true
}

// Validate reports every contract violation in the record at once.
func (i ItemAtStart) Validate() error {
	var problems []error
	for _, field := range []struct{ name, digest string }{
		{"title", i.Title}, {"description", i.Description}, {"design", i.Design},
		{"acceptance_criteria", i.AcceptanceCriteria}, {"notes", i.Notes},
	} {
		if decoded, err := hex.DecodeString(field.digest); err != nil || len(decoded) != sha256.Size {
			problems = append(problems, fmt.Errorf("%s must be a SHA-256 digest", field.name))
		}
	}
	if i.NotesBytes < 0 {
		problems = append(problems, errors.New("notes_bytes cannot be negative"))
	}
	return errors.Join(problems...)
}
