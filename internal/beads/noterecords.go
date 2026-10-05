package beads

import (
	"fmt"
	"strconv"
	"strings"
)

const noteBoundary = "<!-- yoyodyne-note-bytes: "

// FrameNote records the boundary of one append without changing its payload.
// The length makes a quoted boundary, stop, or decision inside the payload data
// rather than another note. There is no footer: confirmation and retry readers
// can still compare the appended prose at the end of the notes.
func FrameNote(note string) string {
	return fmt.Sprintf("%s%d -->\n%s", noteBoundary, len(note), note)
}

// NoteRecord is a payload from an actual framed append, or a stretch of older
// notes whose append boundaries were not recorded. Unframed prose cannot prove
// which matching sentences were separate writes rather than quoted content.
type NoteRecord struct {
	Text   string
	Framed bool
}

// NoteRecords reads the length-delimited appends, skipping over their complete
// payloads. It never searches inside a framed payload for another boundary.
// Malformed or incomplete boundaries remain ordinary, unframed text.
func NoteRecords(notes string) []NoteRecord {
	var records []NoteRecord
	start := 0
	search := 0
	for search < len(notes) {
		relative := strings.Index(notes[search:], noteBoundary)
		if relative < 0 {
			break
		}
		at := search + relative
		search = at + len(noteBoundary)
		if at > 0 && notes[at-1] != '\n' {
			continue
		}
		end := strings.IndexByte(notes[search:], '\n')
		if end < 0 {
			break
		}
		header := notes[search : search+end]
		length, err := strconv.Atoi(strings.TrimSuffix(header, " -->"))
		payload := search + end + 1
		if err != nil || !strings.HasSuffix(header, " -->") || length < 0 || length > len(notes)-payload {
			continue
		}
		if at > start {
			records = append(records, NoteRecord{Text: notes[start:at]})
		}
		records = append(records, NoteRecord{Text: notes[payload : payload+length], Framed: true})
		start = payload + length
		search = start
	}
	if start < len(notes) {
		records = append(records, NoteRecord{Text: notes[start:]})
	}
	return records
}

func frameNoteArguments(args []string) []string {
	framed := append([]string(nil), args...)
	for index, arg := range framed {
		if note, appending := strings.CutPrefix(arg, "--append-notes="); appending {
			// Confirmation already compares trimmed prose. Trimming before measuring
			// also keeps the boundary valid if the tracker trims an append's edges.
			framed[index] = "--append-notes=" + FrameNote(strings.TrimSpace(note))
		}
	}
	return framed
}
