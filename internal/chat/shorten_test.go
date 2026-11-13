package chat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/console"
)

// expandPress is what a scripted prompt answers with to stand for the operator
// pressing the expand key.
const expandPress = "<Ctrl-O>"

// shorteningConsole is a terminal as far as the conversation can tell: it
// shortens the harness's long output, and its prompt reports the expand key as
// ErrExpand. Everything else is the plain stream it wraps.
type shorteningConsole struct {
	console.Console
	out     io.Writer
	prompts []string
}

func (c *shorteningConsole) ShortensOutput() bool { return true }

func (c *shorteningConsole) Prompt(_ context.Context, prompt string, _ <-chan struct{}) (string, error) {
	if len(c.prompts) == 0 {
		return "", io.EOF
	}
	line := c.prompts[0]
	c.prompts = c.prompts[1:]
	if line == expandPress {
		return "", console.ErrExpand
	}
	fmt.Fprintf(c.out, "%s%s\n", prompt, line)
	return line, nil
}

// longTrackerConversation is a conversation whose one answer reads the same
// item ten times, so what the harness prints after it is long.
func longTrackerConversation(t *testing.T) *Session {
	t.Helper()

	var reads []string
	for i := 0; i < 10; i++ {
		reads = append(reads, `{"action":"read","id":"yoyodyne-ifd.22"}`)
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Reading it.", reads...)},
		{SessionID: "session-1", FinalText: "That is what it says."},
	}}
	options := testOptions(t, provider)
	options.Tracker = &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.22": {ID: "yoyodyne-ifd.22", Title: "Readable conversations", Description: "Say who is speaking.", Status: "open"},
	}}
	return openTestSession(t, options)
}

// TestALongHarnessBlockIsShortenedOnATerminalAndShownWholeOnRequest covers the
// terminal: the tracker's long account is its first lines and a line saying how
// many more there are and which key shows them, the answer is whole, and the key
// shows the whole account.
func TestALongHarnessBlockIsShortenedOnATerminalAndShownWholeOnRequest(t *testing.T) {
	t.Parallel()

	// The same conversation over a stream is the account in full.
	var plain bytes.Buffer
	if err := longTrackerConversation(t).Converse(context.Background(), testConsole(strings.NewReader("What does ifd.22 say?\n/exit\n"), &plain)); err != nil {
		t.Fatalf("Converse() over a stream error = %v", err)
	}
	if strings.Contains(plain.String(), "more lines; press") {
		t.Fatalf("a stream was shown shortened output:\n%s", plain.String())
	}
	full := strings.Count(plain.String(), "yoyodyne-ifd.22")
	if full < 10 {
		t.Fatalf("the stream did not show every read (%d mentions):\n%s", full, plain.String())
	}

	var out bytes.Buffer
	screen := &shorteningConsole{
		Console: testConsole(strings.NewReader(""), &out),
		out:     &out,
		prompts: []string{"What does ifd.22 say?", expandPress, "/exit"},
	}
	if err := longTrackerConversation(t).Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	said := out.String()
	expansion := strings.Index(said, "In full, what was shortened after the last answer:")
	if expansion < 0 {
		t.Fatalf("the expand key showed nothing:\n%s", said)
	}
	before, after := said[:expansion], said[expansion:]
	if !strings.Contains(before, "That is what it says.") {
		t.Fatalf("the answer was not shown whole:\n%s", said)
	}
	if !strings.Contains(before, "more lines; press Ctrl-O to show them in full") {
		t.Fatalf("the long block was not shortened:\n%s", said)
	}
	if shortened := strings.Count(before, "yoyodyne-ifd.22"); shortened >= full {
		t.Fatalf("the shortened block mentions the item %d times, the whole one %d:\n%s", shortened, full, said)
	}
	if whole := strings.Count(after, "yoyodyne-ifd.22"); whole < 10 {
		t.Fatalf("the expansion did not show every read (%d mentions):\n%s", whole, said)
	}
}

// TestShortenBlock covers the rule itself: a short block is untouched, a long
// one keeps its first lines, says how many more there are, and keeps the blank
// line that separates it from the next block.
func TestShortenBlock(t *testing.T) {
	t.Parallel()

	short := "one\ntwo\nthree\n\n"
	if got, cut := shortenBlock(short); cut || got != short {
		t.Fatalf("shortenBlock(short) = %q, %v", got, cut)
	}
	atLimit := strings.Repeat("line\n", shortenAbove) + "\n"
	if got, cut := shortenBlock(atLimit); cut || got != atLimit {
		t.Fatalf("a block of %d lines was shortened: %q", shortenAbove, got)
	}
	var long strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&long, "line %d\n", i)
	}
	long.WriteString("\n")
	got, cut := shortenBlock(long.String())
	want := "line 1\nline 2\nline 3\nline 4\nline 5\n… 15 more lines; press Ctrl-O to show them in full\n\n"
	if !cut || got != want {
		t.Fatalf("shortenBlock(long) = %q, want %q", got, want)
	}
	if got, cut := shortenBlock(""); cut || got != "" {
		t.Fatalf("shortenBlock(\"\") = %q, %v", got, cut)
	}
}

// TestTheExpandKeyWithNothingShortenedSaysSo: pressing it when nothing was
// shortened answers rather than doing nothing.
func TestTheExpandKeyWithNothingShortenedSaysSo(t *testing.T) {
	t.Parallel()

	session := &Session{}
	var out bytes.Buffer
	session.showShortened(&out)
	if !strings.Contains(out.String(), "Nothing printed since the last answer was shortened") {
		t.Fatalf("showShortened() = %q", out.String())
	}
}
