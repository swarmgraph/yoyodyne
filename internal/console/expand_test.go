package console

import (
	"context"
	"io"
	"testing"
)

// TestCtrlOAsksForShortenedOutputAndKeepsTheLine covers the key that shows the
// harness's shortened output in full: the prompt returns ErrExpand at once, and
// what the operator was typing is still there for the next prompt.
func TestCtrlOAsksForShortenedOutputAndKeepsTheLine(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	ctx := context.Background()
	expanded := prompting(ctx, console, "you> ", nil)

	keys.Write([]byte("what about"))
	out.await(t, "the typed text", func(s *screen) bool { return s.lastLine() == "you> what about" })
	keys.Write([]byte{0x0f})
	if _, err := expanded.result(t); err != ErrExpand {
		t.Fatalf("Prompt() error = %v, want ErrExpand", err)
	}
	io.WriteString(console, "the whole block\n")

	lines := prompting(ctx, console, "you> ", nil)
	out.await(t, "the resumed line", func(s *screen) bool { return s.lastLine() == "you> what about" })
	keys.Write([]byte(" the goals?\r"))
	if line := lines.line(t); line != "what about the goals?" {
		t.Fatalf("line = %q", line)
	}
}

// TestCtrlOIsTheExpandKeyHoweverItIsReported covers both ways a terminal sends
// Ctrl-O: the control byte, and the key reported with its modifier where the
// keyboard was negotiated.
func TestCtrlOIsTheExpandKeyHoweverItIsReported(t *testing.T) {
	t.Parallel()

	if pressed, _, _ := decodeKey([]byte{0x0f}); pressed.code != keyExpand {
		t.Fatalf("decodeKey(Ctrl-O) = %v, want keyExpand", pressed.code)
	}
	if pressed := reportedKey('o', 1+modifierCtrl); pressed.code != keyExpand {
		t.Fatalf("reportedKey(Ctrl-O) = %v, want keyExpand", pressed.code)
	}
}

// TestOnlyATerminalShortensOutput: a stream shows everything in full, so it
// must not say it shortens.
func TestOnlyATerminalShortensOutput(t *testing.T) {
	t.Parallel()

	console, _, _ := terminalUnderTest(t, 40)
	if !Shortens(console) {
		t.Fatal("a terminal does not shorten output")
	}
	if Shortens(newPlain(nil, io.Discard)) {
		t.Fatal("a stream shortens output")
	}
}
