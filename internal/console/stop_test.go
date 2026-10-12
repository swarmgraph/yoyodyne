package console

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// forever is a window no test outlasts.
const forever = time.Hour

// never is an escape wait that does not pass, so an escape byte is only ever
// the start of a sequence whatever the machine's load.
func never() <-chan time.Time { return nil }

// escapeFired is an escape wait the test ends itself: every wait started passes
// once fire is closed.
func escapeFired() (func() <-chan time.Time, func()) {
	fired := make(chan time.Time)
	return func() <-chan time.Time { return fired }, func() { close(fired) }
}

// keysWaiting reports whether anything the operator typed is still waiting to be
// read, under the lock the console reads it under.
func keysWaiting(console *terminal) bool {
	console.mu.Lock()
	defer console.mu.Unlock()
	return len(console.keys) > 0
}

// testClock is a clock a test moves itself.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
}

// TestAnEscapeOnItsOwnIsWaitedForAndThenRead is the escape key alone: its byte
// is the start of every sequence the other keys send, so it is held until the
// wait passes with nothing after it, and then read as the key rather than
// joined to whatever is typed next.
func TestAnEscapeOnItsOwnIsWaitedForAndThenRead(t *testing.T) {
	t.Parallel()

	if _, _, complete := decodeKey([]byte{0x1b}); complete {
		t.Fatal("an escape byte on its own was read before anything could follow it")
	}
	for _, test := range []struct {
		bytes string
		late  bool
	}{
		{bytes: "\x1b", late: true},
		{bytes: "\x1b\x1b", late: true},
		{bytes: "\x1b[", late: false},
		{bytes: "\x1b[1;", late: false},
		{bytes: "a", late: false},
	} {
		if got := lateEscape([]byte(test.bytes)); got != test.late {
			t.Errorf("lateEscape(%q) = %v, want %v", test.bytes, got, test.late)
		}
	}

	console, keys, out := terminalUnderTest(t, 40)
	wait, fire := escapeFired()
	console.escapeAfter = wait
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("ab"))
	out.await(t, "the typed text", func(s *screen) bool { return s.lastLine() == "you> ab" })
	keys.Write([]byte{0x1b})
	out.await(t, "the escape waiting", func(*screen) bool { return keysWaiting(console) })
	fire()
	out.await(t, "the escape read as a key", func(*screen) bool { return !keysWaiting(console) })
	// Joined to the escape, this b would be Alt-b, which moves back a word and
	// would put it at the start of the message.
	keys.Write([]byte("b\r"))
	if line := lines.line(t); line != "abb" {
		t.Fatalf("line = %q, want the escape read on its own and the b typed after it", line)
	}
}

// TestAnEscapeSequenceSplitAcrossReadsIsStillOneKey is the other side of the
// wait: an arrow whose bytes arrive in two reads is still the arrow, and not
// the escape key followed by two characters typed into the message.
func TestAnEscapeSequenceSplitAcrossReadsIsStillOneKey(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	console.escapeAfter = never
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("ab"))
	out.await(t, "the typed text", func(s *screen) bool { return s.lastLine() == "you> ab" })
	keys.Write([]byte{0x1b})
	keys.Write([]byte("[D"))
	keys.Write([]byte("X\r"))
	if line := lines.line(t); line != "aXb" {
		t.Fatalf("line = %q, want the split arrow to have moved the cursor", line)
	}
}

// TestCtrlCClearsWhatIsBeingTyped is Ctrl-C on a prompt with text in it: the
// text goes, the conversation does not, and Ctrl-Y puts back what was cleared.
func TestCtrlCClearsWhatIsBeingTyped(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("a draft"))
	out.await(t, "the typed text", func(s *screen) bool { return s.lastLine() == "you> a draft" })
	keys.Write([]byte{0x03})
	cleared := out.await(t, "the prompt cleared", func(s *screen) bool { return s.lastLine() == "you>" })
	if strings.Contains(cleared.text(), endingNotice) {
		t.Fatalf("clearing a message offered to end the conversation:\n%s", cleared.text())
	}
	keys.Write([]byte{0x19, '\r'})
	if line := lines.line(t); line != "a draft" {
		t.Fatalf("line = %q, want Ctrl-Y to put back what Ctrl-C cleared", line)
	}
}

// TestCtrlCTwiceOnAnEmptyPromptEndsTheConversation is the only way Ctrl-C ends
// it: once on an empty prompt says what a second press does, and the second
// ends it as the input ending does.
func TestCtrlCTwiceOnAnEmptyPromptEndsTheConversation(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	console.endAfter = forever
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte{0x03})
	out.await(t, "what a second Ctrl-C would do", func(s *screen) bool {
		return strings.Contains(s.text(), endingNotice)
	})
	keys.Write([]byte{0x03})
	if _, err := lines.result(t); !errors.Is(err, io.EOF) {
		t.Fatalf("Prompt() error = %v, want the conversation ended", err)
	}
}

// TestAnythingBetweenTwoCtrlCsKeepsTheConversation is the first press taken
// back: any other key in between, or the window passing, means the next Ctrl-C
// is a first one again.
func TestAnythingBetweenTwoCtrlCsKeepsTheConversation(t *testing.T) {
	t.Parallel()

	t.Run("another key", func(t *testing.T) {
		t.Parallel()

		console, keys, out := terminalUnderTest(t, 60)
		console.endAfter = forever
		lines := prompting(context.Background(), console, "you> ", nil)
		keys.Write([]byte{0x03})
		out.await(t, "the first press", func(s *screen) bool { return strings.Contains(s.text(), endingNotice) })
		keys.Write([]byte("x"))
		out.await(t, "the first press forgotten", func(s *screen) bool {
			return !strings.Contains(s.text(), endingNotice) && s.lastLine() == "you> x"
		})
		// Backspace leaves the prompt empty again, so this Ctrl-C is a first.
		keys.Write([]byte{0x7f, 0x03})
		out.await(t, "a first press again", func(s *screen) bool { return strings.Contains(s.text(), endingNotice) })
		keys.Write([]byte("kept\r"))
		if line := lines.line(t); line != "kept" {
			t.Fatalf("line = %q, want the conversation still open", line)
		}
	})

	t.Run("the window passing", func(t *testing.T) {
		t.Parallel()

		console, keys, out := terminalUnderTest(t, 60)
		clock := &testClock{now: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}
		console.now = clock.Now
		console.endAfter = forever
		lines := prompting(context.Background(), console, "you> ", nil)
		keys.Write([]byte{0x03})
		out.await(t, "the first press", func(s *screen) bool { return strings.Contains(s.text(), endingNotice) })
		clock.advance(forever)
		keys.Write([]byte{0x03})
		keys.Write([]byte("kept\r"))
		if line := lines.line(t); line != "kept" {
			t.Fatalf("line = %q, want a press after the window to be a first one", line)
		}
	})
}

// TestCtrlCTwiceOnAListEndsTheConversation is the same two presses on a question
// with answers to pick from, which has no text of its own to clear.
func TestCtrlCTwiceOnAListEndsTheConversation(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	console.endAfter = forever
	answers := choosing(context.Background(), console, "answer c1.1? ", testOptions, nil)
	out.await(t, "the answers on offer", func(s *screen) bool { return strings.Contains(s.text(), choiceKeys) })
	keys.Write([]byte{0x03})
	out.await(t, "the first press", func(s *screen) bool { return strings.Contains(s.text(), endingNotice) })
	keys.Write([]byte{0x03})
	if _, err := answers.result(t); !errors.Is(err, io.EOF) {
		t.Fatalf("Choose() error = %v, want the conversation ended", err)
	}
}

// TestCtrlCIsTheConsolesOnlyWhileItReads is the terminal setting behind all of
// this: Ctrl-C reaches the console as a key only while something is reading the
// keyboard, and is the terminal's interrupt again the rest of the time, so a
// command that is busy between prompts can still be interrupted.
func TestCtrlCIsTheConsolesOnlyWhileItReads(t *testing.T) {
	t.Parallel()

	console, keys, _ := terminalUnderTest(t, 40)
	var mu sync.Mutex
	var owned []bool
	console.interruptKey = func(own bool) {
		mu.Lock()
		defer mu.Unlock()
		owned = append(owned, own)
	}
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("hello\r"))
	lines.line(t)
	_, done := console.WatchForStop()
	done()
	answers := choosing(context.Background(), console, "answer c1.1? ", testOptions, nil)
	keys.Write([]byte("\r"))
	answers.line(t)

	mu.Lock()
	defer mu.Unlock()
	want := []bool{true, false, true, false, true, false}
	if len(owned) != len(want) {
		t.Fatalf("Ctrl-C was taken and given back %v, want %v", owned, want)
	}
	for index := range want {
		if owned[index] != want[index] {
			t.Fatalf("Ctrl-C was taken and given back %v, want %v", owned, want)
		}
	}
}

// TestEscStopsATurnAndTheProgressLineSaysSo is the keyboard read while a turn is
// being answered: the progress line names the key, Esc stops the turn, and the
// line goes back to saying nothing about it once the turn is over.
func TestEscStopsATurnAndTheProgressLineSaysSo(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 80)
	wait, fire := escapeFired()
	console.escapeAfter = wait
	activity := console.Working("thinking")
	stop, done := console.WatchForStop()
	out.await(t, "the key that stops the turn", func(s *screen) bool {
		return strings.Contains(s.text(), "thinking") && strings.Contains(s.text(), "esc to stop")
	})
	// The escape is waited for as it is at a prompt, and stops the turn once the
	// wait has passed with nothing after it.
	keys.Write([]byte{0x1b})
	fire()
	<-stop
	done()
	activity.Close()
	out.await(t, "the progress line gone", func(s *screen) bool { return !strings.Contains(s.text(), "esc to stop") })
}

// TestCtrlCStopsATurnAndTypingAheadIsKept is Ctrl-C while a turn is being
// answered, which stops it rather than ending the conversation, and everything
// else typed meanwhile — an arrow split across two reads among it — waits for
// the next prompt exactly as it always did.
func TestCtrlCStopsATurnAndTypingAheadIsKept(t *testing.T) {
	t.Parallel()

	console, keys, _ := terminalUnderTest(t, 80)
	console.escapeAfter = never
	stop, done := console.WatchForStop()
	keys.Write([]byte("hi"))
	keys.Write([]byte{0x1b})
	keys.Write([]byte("[D"))
	keys.Write([]byte{0x03})
	<-stop
	done()
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("!\r"))
	if line := lines.line(t); line != "h!i" {
		t.Fatalf("line = %q, want what was typed ahead kept, the arrow applied, and the Ctrl-C gone", line)
	}
}
