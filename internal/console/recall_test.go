package console

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The keys these tests press, as a terminal sends them.
const (
	pressUp    = "\x1b[A"
	pressDown  = "\x1b[B"
	pressLeft  = "\x1b[D"
	pressAlt   = "\x1b\r" // alt-return, the newline every terminal sends
	pressEnter = "\r"
)

// TestUpRecallsEarlierMessagesAndDownBringsBackTheOneBeingWritten is the
// request: up on the first line replaces it with the message sent before, each
// further press goes further back, and down comes forward again to the message
// that was being written, exactly as it was left — cursor and all.
func TestUpRecallsEarlierMessagesAndDownBringsBackTheOneBeingWritten(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	Remember(console, "what is the brief?", "and the goals?")
	lines := prompting(context.Background(), console, "you> ", nil)

	keys.Write([]byte("half typed" + pressLeft + pressLeft))
	out.await(t, "the half-typed message", func(s *screen) bool { return s.lastLine() == "you> half typed" })

	keys.Write([]byte(pressUp))
	out.await(t, "the last message sent", func(s *screen) bool { return s.lastLine() == "you> and the goals?" })
	keys.Write([]byte(pressUp))
	out.await(t, "the one before it", func(s *screen) bool { return s.lastLine() == "you> what is the brief?" })
	// There is nothing earlier, so a further press leaves the earliest there.
	keys.Write([]byte(pressUp))
	keys.Write([]byte(pressDown))
	out.await(t, "forward again", func(s *screen) bool { return s.lastLine() == "you> and the goals?" })
	keys.Write([]byte(pressDown))
	out.await(t, "the half-typed message back", func(s *screen) bool { return s.lastLine() == "you> half typed" })
	// The cursor is where it was left, two from the end, so typing carries on
	// from there rather than from wherever the recalled messages put it.
	keys.Write([]byte("!" + pressEnter))
	if line := lines.line(t); line != "half typ!ed" {
		t.Fatalf("line = %q, want the half-typed message intact with the cursor where it was", line)
	}
}

// TestARecalledMessageIsSentAsItReads checks that what up recalls is a message
// to send and edit like any other, and that the next prompt starts from a
// message of its own rather than from where up had got to.
func TestARecalledMessageIsSentAsItReads(t *testing.T) {
	t.Parallel()

	console, keys, _ := terminalUnderTest(t, 40)
	Remember(console, "first", "second")
	ctx := context.Background()

	lines := prompting(ctx, console, "you> ", nil)
	keys.Write([]byte(pressUp + pressUp + " again" + pressEnter))
	if line := lines.line(t); line != "first again" {
		t.Fatalf("line = %q, want the earlier message as edited", line)
	}
	Remember(console, "first again")

	// One press is the message just sent, and the message being written is
	// empty again rather than the recalled one it was sent from.
	lines = prompting(ctx, console, "you> ", nil)
	keys.Write([]byte(pressUp + pressEnter))
	if line := lines.line(t); line != "first again" {
		t.Fatalf("line = %q, want the message sent last", line)
	}
	lines = prompting(ctx, console, "you> ", nil)
	keys.Write([]byte(pressUp + pressDown + pressEnter))
	if line := lines.line(t); line != "" {
		t.Fatalf("line = %q, want the empty message being written", line)
	}
}

// TestUpAndDownMoveBetweenTheLinesOfAMessage checks that inside a message of
// several lines the keys move the cursor rather than recall anything, keeping
// the column where the line is long enough and stopping at its end where not.
func TestUpAndDownMoveBetweenTheLinesOfAMessage(t *testing.T) {
	t.Parallel()

	console, keys, _ := terminalUnderTest(t, 40)
	Remember(console, "never recalled")
	lines := prompting(context.Background(), console, "you> ", nil)

	keys.Write([]byte("first line" + pressAlt + "mid" + pressAlt + "the last line"))
	// From the end of the last line, up lands at the end of the shorter line
	// above, and up again keeps that column on the first line.
	keys.Write([]byte(pressUp + "1" + pressUp + "2"))
	// Down twice from there is the last line at the same column, and nothing
	// down there recalls anything either.
	keys.Write([]byte(pressDown + pressDown + "3" + pressEnter))
	want := "firs2t line\nmid1\nthe 3last line"
	if line := lines.line(t); line != want {
		t.Fatalf("line = %q, want %q", line, want)
	}
}

// TestRecallHappensOnlyFromTheFirstOrLastLine checks the edges: up recalls
// only once the cursor is on the first line, down comes forward only from the
// last, and a recalled message of several lines is climbed through rather than
// skipped.
func TestRecallHappensOnlyFromTheFirstOrLastLine(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	Remember(console, "oldest", "one\ntwo")
	lines := prompting(context.Background(), console, "you> ", nil)

	keys.Write([]byte("draft" + pressAlt + "more"))
	out.await(t, "the draft", func(s *screen) bool { return s.lastLine() == "more" })
	// The first press only climbs to the draft's first line; the second recalls.
	keys.Write([]byte(pressUp))
	keys.Write([]byte(pressUp))
	out.await(t, "the recalled message", func(s *screen) bool { return s.lastLine() == "two" })
	// The recalled message has two lines and the cursor is on its last, so one
	// press climbs within it and the next reaches the message before.
	keys.Write([]byte(pressUp))
	keys.Write([]byte(pressUp))
	out.await(t, "the oldest message", func(s *screen) bool { return s.lastLine() == "you> oldest" })
	// Coming forward lands on the first line of the two-line message, so one
	// press walks down it and the next brings the draft back whole.
	keys.Write([]byte(pressDown + pressDown + pressDown + pressEnter))
	if line := lines.line(t); line != "draft\nmore" {
		t.Fatalf("line = %q, want the draft intact", line)
	}
}

// TestRecallSurvivesAnInterruption checks that a message brought back with up
// is still the one on screen after a run's report needed the screen, and that
// the draft kept aside still comes back under it.
func TestRecallSurvivesAnInterruption(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	Remember(console, "earlier")
	ctx := context.Background()
	finished := make(chan struct{})
	interrupted := prompting(ctx, console, "you> ", finished)

	keys.Write([]byte("mine" + pressUp))
	out.await(t, "the recalled message", func(s *screen) bool { return s.lastLine() == "you> earlier" })
	close(finished)
	if _, err := interrupted.result(t); err != ErrInterrupted {
		t.Fatalf("Prompt() error = %v, want ErrInterrupted", err)
	}

	lines := prompting(ctx, console, "you> ", nil)
	out.await(t, "the recalled message resumed", func(s *screen) bool { return s.lastLine() == "you> earlier" })
	keys.Write([]byte(pressDown + pressEnter))
	if line := lines.line(t); line != "mine" {
		t.Fatalf("line = %q, want the draft kept across the interruption", line)
	}
}

// TestRememberKeepsWhatIsWorthRecalling checks what goes into the history: a
// message repeated straight after itself and an empty one are not two presses
// of up apart, and the oldest go once there are more than are kept.
func TestRememberKeepsWhatIsWorthRecalling(t *testing.T) {
	t.Parallel()

	console, _, _ := terminalUnderTest(t, 40)
	Remember(console, "a", "a", "", "b", "a")
	if got := strings.Join(console.recall, ","); got != "a,b,a" {
		t.Fatalf("recall = %q, want a,b,a", got)
	}
	for index := 0; index < maxRecalled; index++ {
		Remember(console, strings.Repeat("x", index%2+1))
	}
	if len(console.recall) != maxRecalled || console.recalled != maxRecalled {
		t.Fatalf("recall holds %d with %d recalled, want %d and none recalled", len(console.recall), console.recalled, maxRecalled)
	}
}

// TestRememberOnAStreamDoesNothing: a stream has no keys to recall with, so
// remembering for it is harmless rather than an error.
func TestRememberOnAStreamDoesNothing(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	stream := Open(Options{In: strings.NewReader("typed\n"), Out: &out, Env: func(string) string { return "" }})
	Remember(stream, "earlier")
	if line, err := stream.Prompt(context.Background(), "you> ", nil); err != nil || line != "typed" {
		t.Fatalf("Prompt() = %q, %v", line, err)
	}
}
