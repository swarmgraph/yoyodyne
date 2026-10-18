package testharness

import (
	"strings"
)

// LastLine is the line a reader of a note sees last.
func LastLine(notes string) string {
	notes = strings.TrimRight(notes, "\n")
	return notes[strings.LastIndexByte(notes, '\n')+1:]
}
