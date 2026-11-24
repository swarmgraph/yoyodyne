package domain

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// The note reads back as it was written, and the latest note in the form is
// the one read, so an ending written after a raising is what an item says.
func TestAnOperatorRaisingReadsBackFromTheNotesItWasWrittenInto(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 10, 15, 57, 0, 0, time.UTC)
	raised := OperatorRaising{
		Words:        "put the MCP pieces\nahead of everything",
		Conversation: "chat-91253e0e",
		AfterTurn:    5464,
		At:           at,
		Above:        []string{"yoyodyne-a", "yoyodyne-b"},
		AboveMore:    3,
	}
	pacific, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("no zone data: %v", err)
	}
	rendered := raised.Render(pacific)
	for _, required := range []string{
		"Raised by the operator on purpose.",
		`His words: "put the MCP pieces ahead of everything"`,
		"From: conversation chat-91253e0e, his message after turn 5464",
		"When: 07:57 PST on November 10, 2026 (2026-11-10T15:57:00Z)",
		"Raised above: yoyodyne-a, yoyodyne-b and 3 more",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered = %q, want %q", rendered, required)
		}
	}
	read, found := LatestOperatorRaising("Admitted earlier.\n\n" + rendered + "\n\nRecorded by the Lead Product Manager.")
	want := raised
	want.Words = "put the MCP pieces ahead of everything"
	if !found || !reflect.DeepEqual(read, want) {
		t.Fatalf("read back = %#v, %v; want %#v", read, found, want)
	}

	ended := OperatorRaising{Ended: true, Words: "that can go back now", Conversation: "chat-91253e0e", AfterTurn: 5500, At: at.Add(time.Hour)}
	read, found = LatestOperatorRaising(rendered + "\n\n" + ended.Render(time.UTC))
	if !found || !read.Ended || read.Words != "that can go back now" || len(read.Above) != 0 {
		t.Fatalf("latest = %#v, %v; want the ending, above nothing", read, found)
	}
	if _, found := LatestOperatorRaising("Raised by the operator on purpose.\nnothing else"); found {
		t.Fatal("a heading with no note under it read as a raising")
	}
}

func TestRaisedByOperatorReadsTheLabelExactly(t *testing.T) {
	t.Parallel()

	if !RaisedByOperator([]string{"product", " raised-by-operator "}) || RaisedByOperator([]string{"Raised-By-Operator"}) {
		t.Fatal("RaisedByOperator does not compare the label as the tracker stores it")
	}
}
