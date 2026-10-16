package console

import (
	"context"
	"io"
	"strings"
	"testing"
)

// composing is one prompt beside a turn being answered, running in a goroutine
// the way a conversation runs it. The turn ends before the answer is handed
// over, so a prompt the test opens once it has the answer is never still beside
// the turn.
func composing(ctx context.Context, console *terminal, interrupt <-chan struct{}) prompted {
	answers := make(prompted, 1)
	end := console.BesideTurn()
	go func() {
		line, err := console.Prompt(ctx, "you> ", interrupt)
		end()
		answers <- struct {
			line string
			err  error
		}{line, err}
	}()
	return answers
}

// TestAMessageSentDuringATurnWaitsOnScreen is the request: the prompt stays
// usable under the account of the turn, a message sent there is shown as
// waiting rather than written into the answer arriving above it, and it is
// handed over only when the conversation asks for it.
func TestAMessageSentDuringATurnWaitsOnScreen(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	ctx := context.Background()
	work := console.Working("the model is thinking")
	defer work.Close()

	sent := composing(ctx, console, nil)
	keys.Write([]byte("and the goals?\r"))
	if line := sent.line(t); line != "and the goals?" {
		t.Fatalf("line = %q", line)
	}
	console.Wait("and the goals?")

	// Composing again is what the conversation does next, and the region it
	// draws carries the account of the turn, the waiting message, and the empty
	// line under it, in that order.
	finished := make(chan struct{})
	again := composing(ctx, console, finished)
	io.WriteString(console, "product-manager> Two goals, then.\n")
	rendered := out.await(t, "the waiting message under the answer", func(s *screen) bool {
		lines := s.lines()
		return len(lines) >= 3 && strings.HasPrefix(lines[len(lines)-2], waitingLabel+"and the goals?") && lines[len(lines)-1] == "you>"
	})
	lines := rendered.lines()
	if !strings.Contains(lines[len(lines)-3], "the model is thinking") {
		t.Fatalf("the account of the turn is not above the waiting message; screen was:\n%s", rendered.text())
	}
	// It was never echoed into the transcript: only the answer is there.
	if strings.Contains(rendered.text(), "you> and the goals?") {
		t.Fatalf("the waiting message was written into the conversation; screen was:\n%s", rendered.text())
	}

	close(finished)
	if _, err := again.result(t); err != ErrInterrupted {
		t.Fatalf("Prompt() error = %v, want ErrInterrupted", err)
	}
	message, ok := console.NextWaiting()
	if !ok || message != "and the goals?" {
		t.Fatalf("NextWaiting() = %q, %v", message, ok)
	}
	if _, ok := console.NextWaiting(); ok {
		t.Fatal("a message was handed over twice")
	}
	if strings.Contains(out.screen().text(), waitingLabel) {
		t.Fatalf("a message handed over is still shown waiting; screen was:\n%s", out.screen().text())
	}
}

// TestUpTakesAWaitingMessageBackToChangeOrWithdraw covers the rest of it: up on
// an empty line puts the last waiting message back into the line, where it can
// be changed and sent again, or emptied, which withdraws it.
func TestUpTakesAWaitingMessageBackToChangeOrWithdraw(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	Remember(console, "an earlier message")
	ctx := context.Background()
	console.Wait("first")
	console.Wait("secnd")

	// Changed: the last one comes back, is corrected, and is sent again.
	sent := composing(ctx, console, nil)
	keys.Write([]byte(pressUp))
	out.await(t, "the last waiting message in the line", func(s *screen) bool { return s.lastLine() == "you> secnd" })
	keys.Write([]byte("\x7f\x7f\x7fcond" + pressEnter))
	if line := sent.line(t); line != "second" {
		t.Fatalf("line = %q", line)
	}
	console.Wait("second")

	// Withdrawn: the line is emptied and nothing replaces it.
	cleared := make(chan struct{})
	withdrawing := composing(ctx, console, cleared)
	keys.Write([]byte(pressUp))
	out.await(t, "the corrected message back in the line", func(s *screen) bool { return s.lastLine() == "you> second" })
	keys.Write([]byte("\x15")) // Ctrl-U
	out.await(t, "an empty line", func(s *screen) bool { return s.lastLine() == "you>" })
	close(cleared)
	if _, err := withdrawing.result(t); err != ErrInterrupted {
		t.Fatalf("Prompt() error = %v, want ErrInterrupted", err)
	}

	if message, ok := console.NextWaiting(); !ok || message != "first" {
		t.Fatalf("NextWaiting() = %q, %v, want the first message", message, ok)
	}
	if message, ok := console.NextWaiting(); ok {
		t.Fatalf("NextWaiting() = %q, want the withdrawn message gone", message)
	}

	// With nothing waiting, up is recall again.
	recalled := prompting(ctx, console, "you> ", nil)
	keys.Write([]byte(pressUp))
	out.await(t, "the earlier message recalled", func(s *screen) bool { return s.lastLine() == "you> an earlier message" })
	keys.Write([]byte(pressEnter))
	if line := recalled.line(t); line != "an earlier message" {
		t.Fatalf("line = %q", line)
	}
}

// TestUpKeepsAMessageBeingWritten checks that a waiting message never displaces
// one the operator is in the middle of: up there moves within it or recalls, as
// it always did.
func TestUpKeepsAMessageBeingWritten(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	console.Wait("waiting one")
	sent := composing(context.Background(), console, nil)
	keys.Write([]byte("half" + pressUp))
	out.await(t, "the half-typed message", func(s *screen) bool { return s.lastLine() == "you> half" })
	keys.Write([]byte(pressEnter))
	if line := sent.line(t); line != "half" {
		t.Fatalf("line = %q", line)
	}
	if message, ok := console.NextWaiting(); !ok || message != "waiting one" {
		t.Fatalf("NextWaiting() = %q, %v", message, ok)
	}
}

// TestAWaitingRowIsOneRowWhateverItHolds checks the measure the region erases
// by: a long message and one of several lines are each one row, marked as cut.
func TestAWaitingRowIsOneRowWhateverItHolds(t *testing.T) {
	t.Parallel()

	for _, message := range []string{strings.Repeat("long ", 30), "one line\nand another"} {
		row := waitingRow(message, waitingHint, 40)
		if visibleWidth(row) > 40 {
			t.Fatalf("row %q is %d columns, wider than 40", row, visibleWidth(row))
		}
		if !strings.Contains(row, "…") {
			t.Fatalf("row %q does not say it is cut", row)
		}
	}
	if row := waitingRow("short", waitingHint, 60); row != waitingLabel+"short"+waitingHint {
		t.Fatalf("row = %q", row)
	}
	// A window too narrow for the hint keeps the message rather than the hint.
	if row := waitingRow("short", waitingHint, 30); row != waitingLabel+"short" {
		t.Fatalf("row = %q", row)
	}
}

// TestAMessageStillWaitingWhenTheConsoleClosesIsLeftOnScreen keeps what the
// operator typed and never sent, as a closing console keeps everything else
// that exists only on the screen.
func TestAMessageStillWaitingWhenTheConsoleClosesIsLeftOnScreen(t *testing.T) {
	t.Parallel()

	console, _, out := terminalUnderTest(t, 60)
	console.Wait("never sent")
	console.Close()
	if text := out.screen().text(); !strings.Contains(text, "not sent: never sent") {
		t.Fatalf("screen =\n%s", text)
	}
}

// TestTheKeysThatStopATurnStopItFromThePromptBesideIt covers the prompt kept
// open while a turn is answered, which is what reads the keyboard then: Esc
// stops the turn whatever has been typed, Ctrl-C clears a line that has text
// and stops the turn from an empty one, and what was typed is kept either way.
func TestTheKeysThatStopATurnStopItFromThePromptBesideIt(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	ctx := context.Background()
	work := console.Working("the model is thinking")
	defer work.Close()

	escaped := composing(ctx, console, nil)
	keys.Write([]byte("half typed"))
	out.await(t, "the typed text", func(s *screen) bool { return s.lastLine() == "you> half typed" })
	if !strings.Contains(out.screen().text(), stopHint) {
		t.Fatalf("the status row does not say which key stops the turn; screen was:\n%s", out.screen().text())
	}
	keys.Write([]byte("\x1b[27u")) // Esc, as a terminal that reports it sends it
	if _, err := escaped.result(t); err != ErrStopTurn {
		t.Fatalf("Prompt() error = %v, want ErrStopTurn", err)
	}

	// Ctrl-C with text clears it, and does not stop anything.
	cleared := composing(ctx, console, nil)
	out.await(t, "the typed text kept", func(s *screen) bool { return s.lastLine() == "you> half typed" })
	keys.Write([]byte("\x03"))
	out.await(t, "an empty line", func(s *screen) bool { return s.lastLine() == "you>" })
	// From the empty line it stops the turn.
	keys.Write([]byte("\x03"))
	if _, err := cleared.result(t); err != ErrStopTurn {
		t.Fatalf("Prompt() error = %v, want ErrStopTurn", err)
	}

	// Once the turn is over the same keys are what they are at any prompt: Esc
	// does nothing and the line is sent as typed.
	after := prompting(ctx, console, "you> ", nil)
	keys.Write([]byte("\x1b[27u" + "next" + pressEnter))
	if line := after.line(t); line != "next" {
		t.Fatalf("line = %q", line)
	}
}
