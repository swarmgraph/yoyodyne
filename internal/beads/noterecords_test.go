package beads

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestNoteBoundariesKeepQuotedRecordsInsideTheirAppend(t *testing.T) {
	t.Parallel()

	if got := FrameNote("雪\n"); got != "<!-- yoyodyne-note-bytes: 4 -->\n雪\n" {
		t.Fatalf("framed UTF-8 note = %q", got)
	}
	quoted := "Yoyodyne stopped this item: quoted output.\n" + FrameNote("a quoted boundary")
	notes := "older notes\n" + FrameNote(quoted) + "\n" + FrameNote("later append")
	want := []NoteRecord{
		{Text: "older notes\n"},
		{Text: quoted, Framed: true},
		{Text: "\n"},
		{Text: "later append", Framed: true},
	}
	if got := NoteRecords(notes); !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %#v, want %#v", got, want)
	}
	if !NotesEndWith(notes, "later append") {
		t.Fatal("recording a boundary broke retry confirmation of the appended note")
	}
}

func TestIncompleteNoteBoundariesRemainUnframedText(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"<!-- yoyodyne-note-bytes: 99 -->\nshort",
		"<!-- yoyodyne-note-bytes: -1 -->\ntext",
		"<!-- yoyodyne-note-bytes: invalid -->\ntext",
		"a quoted inline <!-- yoyodyne-note-bytes: 4 -->\ntext",
	} {
		if got := NoteRecords(text); !reflect.DeepEqual(got, []NoteRecord{{Text: text}}) {
			t.Fatalf("malformed boundary in %q was treated as a record: %#v", text, got)
		}
	}
}

func TestAppendBoundariesMeasureTheProseTheTrackerStores(t *testing.T) {
	t.Parallel()

	args := []string{"update", "yoyodyne-1", "--append-notes=\n雪\n", "--json"}
	framed := frameNoteArguments(args)
	if framed[2] != "--append-notes="+FrameNote("雪") || args[2] != "--append-notes=\n雪\n" {
		t.Fatalf("framing changed the caller's arguments or measured unstored whitespace: %#v, %#v", args, framed)
	}
}

func TestTheSharedWriterFramesTheWholeAppend(t *testing.T) {
	t.Parallel()

	const note = "Yoyodyne stopped this item: actual reason.\nCaptured output:\nYoyodyne stopped this item: quoted reason."
	for _, operation := range []string{"outcome", "update", "block"} {
		t.Run(operation, func(t *testing.T) {
			status := "open"
			if operation == "block" {
				status = "blocked"
			}
			runner := &fakeRunner{responses: []string{workItemJSON(status, FrameNote(note))}}
			client := Client{Runner: runner}
			var item WorkItem
			var err error
			switch operation {
			case "outcome":
				item, err = client.RecordOutcome(context.Background(), "yoyodyne-1", note)
			case "update":
				item, err = client.Update(context.Background(), "yoyodyne-1", WorkItemChange{AppendNotes: note})
			case "block":
				item, err = client.Block(context.Background(), "yoyodyne-1", note)
			}
			if err != nil {
				t.Fatal(err)
			}
			var written string
			for _, arg := range runner.args[0] {
				if text, ok := strings.CutPrefix(arg, "--append-notes="); ok {
					written = text
				}
			}
			if got := NoteRecords(written); !reflect.DeepEqual(got, []NoteRecord{{Text: note, Framed: true}}) {
				t.Fatalf("%s transported %#v rather than one complete append", operation, got)
			}
			if !NotesEndWith(item.Notes, note) {
				t.Fatal("the payload no longer confirms as the appended note")
			}
		})
	}
}
