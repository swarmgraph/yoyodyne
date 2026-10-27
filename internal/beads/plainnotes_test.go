package beads

import (
	"context"
	"strings"
	"testing"
)

// The boundary an append is stored under is storage rather than prose, so an
// item read from the tracker carries its notes without it, and keeps the
// stored form only for the reader that tells one append from another.
func TestAnItemReadFromTheTrackerCarriesItsNotesWithoutTheirBoundaries(t *testing.T) {
	t.Parallel()

	stored := "Admitted by hand.\n" + FrameNote("Reprioritized from priority 2 to 1.") + "\n" + FrameNote("Yoyodyne stopped this item.\nRun: run-1")
	runner := &fakeRunner{responses: []string{workItemJSON("open", stored)}}
	item, err := (Client{Runner: runner}).Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	want := "Admitted by hand.\nReprioritized from priority 2 to 1.\nYoyodyne stopped this item.\nRun: run-1"
	if item.Notes != want {
		t.Fatalf("notes = %q, want %q", item.Notes, want)
	}
	if strings.Contains(item.Notes, noteBoundary) {
		t.Fatalf("the notes still carry a stored boundary: %q", item.Notes)
	}
	if item.RecordedNotes() != stored {
		t.Fatalf("recorded notes = %q, want the stored form %q", item.RecordedNotes(), stored)
	}
	framed := 0
	for _, record := range NoteRecords(item.RecordedNotes()) {
		if record.Framed {
			framed++
		}
	}
	if framed != 2 {
		t.Fatalf("the stored form lost its append boundaries: %d framed appends", framed)
	}

	plain := &fakeRunner{responses: []string{workItemJSON("open", "Older notes, written before boundaries were recorded.")}}
	older, err := (Client{Runner: plain}).Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if older.FramedNotes != "" || older.RecordedNotes() != older.Notes {
		t.Fatalf("notes with no boundaries were given a second copy: %+v", older)
	}
}
