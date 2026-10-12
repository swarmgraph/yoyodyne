package console

import (
	"io"
	"strings"
	"testing"
	"time"
)

// TestWhatEachKeyboardCallsShiftReturn covers the whole reason this is
// negotiated at all: the two protocols spell the same keystroke differently,
// and a terminal that speaks neither sends a byte that cannot be told from
// return. Every one of these has to reach the same conclusion about what the
// operator pressed.
func TestWhatEachKeyboardCallsShiftReturn(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		bytes string
		want  keyCode
	}{
		{name: "return alone still sends", bytes: "\r", want: keyEnter},
		{name: "shift-return over the kitty protocol", bytes: "\x1b[13;2u", want: keyNewline},
		{name: "return alone over the kitty protocol", bytes: "\x1b[13u", want: keyEnter},
		{name: "return with an event type attached", bytes: "\x1b[13;2:1u", want: keyNewline},
		{name: "shift-return over modifyOtherKeys", bytes: "\x1b[27;2;13~", want: keyNewline},
		{name: "return over modifyOtherKeys", bytes: "\x1b[27;1;13~", want: keyEnter},
		{name: "alt-return, which needs nothing negotiated", bytes: "\x1b\r", want: keyNewline},
		{name: "alt-return where the terminal reports it", bytes: "\x1b[13;3u", want: keyNewline},
		// The editing keys a negotiated keyboard reports as escape codes rather
		// than the control bytes they used to arrive as still edit.
		{name: "Ctrl-U reported rather than sent", bytes: "\x1b[117;5u", want: keyKillLine},
		{name: "Ctrl-W reported rather than sent", bytes: "\x1b[119;5u", want: keyKillWord},
		{name: "backspace reported rather than sent", bytes: "\x1b[127u", want: keyBackspace},
		{name: "a letter is still a letter", bytes: "\x1b[97u", want: keyRune},
		// A terminal that reports modifiers stops raising the signals itself, so
		// the key has to be recognised as the signal it was.
		{name: "Ctrl-C reported rather than raised", bytes: "\x1b[99;5u", want: keySignal},
		{name: "Ctrl-C over modifyOtherKeys", bytes: "\x1b[27;5;99~", want: keySignal},
		{name: "the escape key is not a keystroke this understands", bytes: "\x1b[27u", want: keyIgnored},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pressed, size, complete := decodeKey([]byte(test.bytes))
			if !complete {
				t.Fatalf("decodeKey(%q) did not read a whole keystroke", test.bytes)
			}
			if size != len(test.bytes) {
				t.Fatalf("decodeKey(%q) consumed %d bytes, want %d", test.bytes, size, len(test.bytes))
			}
			if pressed.code != test.want {
				t.Fatalf("decodeKey(%q) = %v, want %v", test.bytes, pressed.code, test.want)
			}
		})
	}
}

func TestCtrlCReportedAsAKeyIsStillTheInterrupt(t *testing.T) {
	t.Parallel()

	pressed, _, _ := decodeKey([]byte("\x1b[99;5u"))
	if pressed.code != keySignal || pressed.signal != signalInterrupt {
		t.Fatalf("decodeKey() = %v/%v, want the interrupt", pressed.code, pressed.signal)
	}
}

// TestTheKeyboardIsNegotiatedAndHandedBack is what makes shift-return honest:
// the terminal is asked what it will report, told to report it, and put back
// exactly as it was found.
func TestTheKeyboardIsNegotiatedAndHandedBack(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		reply    string
		mode     keyboardMode
		enables  string
		restores string
	}{
		{
			name:     "a terminal that speaks the kitty protocol",
			reply:    "\x1b[?0u\x1b[?62;c",
			mode:     keyboardKitty,
			enables:  kittyPush,
			restores: kittyPop,
		},
		{
			name:     "a terminal that only has modifyOtherKeys, set to nothing",
			reply:    "\x1b[>4;0m\x1b[?1;2c",
			mode:     keyboardModifyOther,
			enables:  "\x1b[>4;2m",
			restores: "\x1b[>4;0m",
		},
		{
			name:     "a terminal whose modifyOtherKeys the operator had already set",
			reply:    "\x1b[>4;1m\x1b[?1;2c",
			mode:     keyboardModifyOther,
			enables:  "\x1b[>4;2m",
			restores: "\x1b[>4;1m",
		},
		{
			name:  "a terminal that answers only who it is",
			reply: "\x1b[?1;2c",
			mode:  keyboardLegacy,
		},
		{
			name: "a terminal that answers nothing at all",
			mode: keyboardLegacy,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			reader, keys := io.Pipe()
			out := newRecorder(40)
			console := newTerminal(reader, out, func() int { return 40 }, func() int { return windowRows }, nil)
			go func() {
				if test.reply != "" {
					keys.Write([]byte(test.reply))
				}
			}()
			// A terminal that answers ends the negotiation with its identity
			// reply, so it is waited for; only the one that answers nothing is
			// given a bound, and running out is what that case asserts.
			wait := answeredNegotiation
			if test.reply == "" {
				wait = 200 * time.Millisecond
			}
			console.negotiateKeyboard(wait)

			if console.keyboard != test.mode {
				t.Fatalf("keyboard = %v, want %v", console.keyboard, test.mode)
			}
			written := out.raw()
			if !strings.HasPrefix(written, kittyQuery+modifyOtherQuery+identityQuery) {
				t.Fatalf("the terminal was not asked what it reports: %q", written)
			}
			if test.enables != "" && !strings.Contains(written, test.enables) {
				t.Fatalf("reporting was not turned on: %q", written)
			}
			if console.restoreKeyboard != test.restores {
				t.Fatalf("restoreKeyboard = %q, want %q", console.restoreKeyboard, test.restores)
			}
			// Closing hands the terminal back before anything else gets it.
			console.Close()
			if test.restores != "" && !strings.HasSuffix(out.raw(), test.restores) {
				t.Fatalf("the keyboard was not handed back: %q", out.raw())
			}
			keys.Close()
		})
	}
}

// TestTypingAheadOfTheNegotiationIsKept is the operator who started typing
// before the terminal finished answering. What they typed is theirs: it is not
// swallowed with the replies, and it is not read as one.
func TestTypingAheadOfTheNegotiationIsKept(t *testing.T) {
	t.Parallel()

	reader, keys := io.Pipe()
	out := newRecorder(40)
	console := newTerminal(reader, out, func() int { return 40 }, func() int { return windowRows }, nil)
	t.Cleanup(func() {
		console.Close()
		keys.Close()
	})
	go keys.Write([]byte("\x1b[?0uthe brief\x1b[?62;c"))
	console.negotiateKeyboard(answeredNegotiation)

	if console.keyboard != keyboardKitty {
		t.Fatalf("keyboard = %v, want the kitty protocol", console.keyboard)
	}
	if got := string(console.keys); got != "the brief" {
		t.Fatalf("what was typed ahead = %q, want %q", got, "the brief")
	}
}

// answeredNegotiation is the bound given to a negotiation whose terminal does
// answer. Its identity reply is what ends the negotiation, so the bound is never
// what does: it is past the binary's own -timeout, which is what reports a
// negotiation that never finishes, naming where it waited. A figure short enough
// to finish on was one a loaded machine could reach before the reply was read.
const answeredNegotiation = time.Hour

// TestHelpNamesOnlyTheKeysThisTerminalHas is the honesty the whole negotiation
// is for: a terminal that will not report shift-return is never told it does.
func TestHelpNamesOnlyTheKeysThisTerminalHas(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		mode   keyboardMode
		shift  bool
		naming string
	}{
		{mode: keyboardKitty, shift: true, naming: "kitty"},
		{mode: keyboardModifyOther, shift: true, naming: "modifyOtherKeys"},
		{mode: keyboardLegacy},
	} {
		help := composingHelp(test.mode)
		says := strings.Contains(help, "shift-return inserts a newline")
		if says != test.shift {
			t.Fatalf("composingHelp(%v) = %q; shift-return offered = %v", test.mode, help, says)
		}
		if test.naming != "" && !strings.Contains(help, test.naming) {
			t.Fatalf("composingHelp(%v) does not say how: %q", test.mode, help)
		}
		// Whatever the terminal turned out to be, the keys that work anyway are
		// named, because they are what the operator falls back to.
		if fallbacks := strings.ToLower(help); !strings.Contains(fallbacks, "alt-return") || !strings.Contains(fallbacks, `\`) {
			t.Fatalf("composingHelp(%v) leaves the operator nothing that always works: %q", test.mode, help)
		}
		// Where shift-return is not reported yet, the command that sets the
		// terminal up to report it is named, and only there.
		if names := strings.Contains(help, "/terminal-setup"); names == test.shift {
			t.Fatalf("composingHelp(%v) = %q; /terminal-setup named = %v", test.mode, help, names)
		}
	}
}
