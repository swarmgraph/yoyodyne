package console

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// keyCode is what one keystroke means to the message being composed. The set is
// deliberately small: this edits prose, and anything it does not understand is
// ignored rather than inserted, because a stray escape sequence in the middle of
// a message to the product manager is worse than a keystroke that did nothing.
type keyCode int

const (
	keyIgnored keyCode = iota
	keyRune
	keyEnter
	keyNewline
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyWordLeft
	keyWordRight
	keyKillLine
	keyKillWord
	// keyKillToEnd deletes from the cursor to the end of the line it is on, and
	// keyYank puts back what the kill keys last deleted (terminal.go).
	keyKillToEnd
	keyYank
	// keySignal is a key the terminal driver would have turned into a signal and
	// which a negotiated keyboard reports as a key instead. It is not an edit:
	// the console raises what the terminal stopped raising.
	keySignal
	// keyPaste is a block the terminal bracketed rather than a key at all: what
	// the operator pasted, whole, with its newlines still in it. It carries text
	// rather than a rune, and a newline in it is a newline in the message rather
	// than the return that would have sent it.
	keyPaste
	// keyEscape is the escape key pressed on its own. It shares its byte with the
	// start of every sequence the other keys send, so it is only known to be the
	// key once escapeWait has passed with nothing after it, or where a negotiated
	// keyboard reports it as a key of its own.
	keyEscape
	// keyInterrupt is Ctrl-C read as a key rather than raised as a signal. At the
	// prompt it clears what is being typed, and twice on an empty prompt ends the
	// conversation; while a turn is being answered it stops the turn.
	keyInterrupt
	// keyTab completes a command's name where the list of commands is showing
	// (commands.go), and is ignored everywhere else.
	keyTab
)

type key struct {
	code   keyCode
	value  rune
	signal signalKey
	text   string
}

// maxEscapeBytes bounds how long an escape sequence may be before it is taken
// for rubbish. Without it, a terminal that sends an escape and stops would hold
// every keystroke after it.
const maxEscapeBytes = 16

// escapeWait is how long an escape byte with nothing after it waits before it
// is taken for the escape key itself. A terminal sends every sequence a key
// makes in one write, so what follows the escape of a sequence arrives within
// a read or two; a person cannot press another key that quickly, so anything
// slower than this is the escape key. Over a slow connection a sequence split
// for longer than this reads as escape followed by what came after it.
const escapeWait = 100 * time.Millisecond

// decodeKey reads the first keystroke out of the buffer. It reports how many
// bytes it consumed, and whether the buffer held a whole keystroke at all: a
// rune or an escape sequence split across two reads is not one, and waits for
// the rest rather than being applied in halves. An escape byte on its own is
// one of those too, until escapeWait says otherwise (lateEscape).
//
// The terminal is left in cbreak rather than raw mode, so the signal keys are
// still the terminal's, with one exception: while the operator is being read
// from, Ctrl-C is turned into a byte rather than an interrupt (tty_unix.go),
// and arrives here as keyInterrupt.
func decodeKey(buffer []byte) (key, int, bool) {
	if len(buffer) == 0 {
		return key{}, 0, false
	}
	switch buffer[0] {
	case '\r', '\n':
		return key{code: keyEnter}, 1, true
	case 0x03:
		return key{code: keyInterrupt}, 1, true
	case '\t':
		return key{code: keyTab}, 1, true
	case 0x7f, 0x08:
		return key{code: keyBackspace}, 1, true
	case 0x01:
		return key{code: keyHome}, 1, true
	case 0x05:
		return key{code: keyEnd}, 1, true
	case 0x0b:
		return key{code: keyKillToEnd}, 1, true
	case 0x19:
		// Ctrl-Y is the delayed suspend on the BSDs, macOS among them, but only
		// with IEXTEN set, which cbreak clears (tty_unix.go), so it arrives here.
		return key{code: keyYank}, 1, true
	case 0x15:
		return key{code: keyKillLine}, 1, true
	case 0x17:
		return key{code: keyKillWord}, 1, true
	case 0x1b:
		return decodeEscape(buffer)
	}
	if buffer[0] < 0x20 {
		return key{code: keyIgnored}, 1, true
	}
	if !utf8.FullRune(buffer) {
		if len(buffer) < utf8.UTFMax {
			return key{}, 0, false
		}
		return key{code: keyIgnored}, 1, true
	}
	value, size := utf8.DecodeRune(buffer)
	if value == utf8.RuneError && size == 1 {
		return key{code: keyIgnored}, 1, true
	}
	return key{code: keyRune, value: value}, size, true
}

// decodeEscape reads the sequences a terminal sends for the keys that have no
// character of their own. Anything else it sends is consumed and ignored, which
// is what keeps a function key from being typed into the message.
func decodeEscape(buffer []byte) (key, int, bool) {
	if len(buffer) == 1 {
		// The escape key itself, or the first byte of a sequence the rest of which
		// has not arrived. Which of the two is decided by time (lateEscape).
		return key{}, 0, false
	}
	if buffer[1] == '\r' || buffer[1] == '\n' {
		// Alt-return, which is the newline that needs nothing negotiated: a
		// terminal that will not say whether shift was held still sends the escape
		// in front of the return, so this is the key that works everywhere.
		return key{code: keyNewline}, 2, true
	}
	// Option or Alt held with a key, from a terminal that says so by putting an
	// escape in front of what the key would have sent. An escape pressed on its
	// own and then one of these letters reads the same, and is taken the same way.
	switch buffer[1] {
	case 'b':
		return key{code: keyWordLeft}, 2, true
	case 'f':
		return key{code: keyWordRight}, 2, true
	case 0x7f, 0x08:
		return key{code: keyKillWord}, 2, true
	case 0x1b:
		// Option or Alt with an arrow, from a terminal that sends the escape in
		// front of the arrow's own sequence rather than reporting it held.
		pressed, size, complete := decodeEscape(buffer[1:])
		if !complete {
			return key{}, 0, false
		}
		switch pressed.code {
		case keyLeft:
			pressed = key{code: keyWordLeft}
		case keyRight:
			pressed = key{code: keyWordRight}
		}
		return pressed, size + 1, true
	}
	if buffer[1] != '[' && buffer[1] != 'O' {
		// Escape followed by something else is not a sequence this understands.
		return key{code: keyIgnored}, 1, true
	}
	if pasted, size, complete, ok := decodePaste(buffer); ok {
		// A paste is read whole rather than as the sequence that opens it, so
		// the block between the brackets is one thing to the message rather than
		// a stream of keystrokes with a return in it.
		return pasted, size, complete
	}
	for index := 2; index < len(buffer); index++ {
		if buffer[index] < 0x40 || buffer[index] > 0x7e {
			continue
		}
		return escapeKey(string(buffer[2:index]), buffer[index]), index + 1, true
	}
	if len(buffer) >= maxEscapeBytes {
		return key{code: keyIgnored}, 1, true
	}
	return key{}, 0, false
}

// lateEscape reports whether the buffer starts with an escape byte that, once
// escapeWait has passed with nothing else arriving, can only be the escape key:
// the escape alone, or the escape followed by another. It is read as one byte, so
// whatever follows it is read on its own.
func lateEscape(buffer []byte) bool {
	if len(buffer) == 0 || buffer[0] != 0x1b {
		return false
	}
	if _, _, complete := decodeKey(buffer); complete {
		return false
	}
	return len(buffer) == 1 || buffer[1] == 0x1b
}

func escapeKey(parameters string, final byte) key {
	switch final {
	case 'A':
		return key{code: keyUp}
	case 'B':
		// Up and down move through a list of answers where there is one. In a
		// message being composed they move between its lines, and from its first
		// or last line through the messages sent before it (recall.go).
		return key{code: keyDown}
	case 'C':
		if movesByWord(parameters) {
			return key{code: keyWordRight}
		}
		return key{code: keyRight}
	case 'D':
		if movesByWord(parameters) {
			return key{code: keyWordLeft}
		}
		return key{code: keyLeft}
	case 'H':
		return key{code: keyHome}
	case 'F':
		return key{code: keyEnd}
	case 'u':
		// The kitty keyboard protocol: the key's own code point, then what was
		// held with it.
		return reportedKey(field(parameters, 0), field(parameters, 1))
	case '~':
		switch parameters {
		case "1", "7":
			return key{code: keyHome}
		case "3":
			return key{code: keyDelete}
		case "4", "8":
			return key{code: keyEnd}
		}
		// xterm's modifyOtherKeys says the same thing the other way round: the
		// literal 27, what was held, and then the key's code point.
		if field(parameters, 0) == 27 {
			return reportedKey(field(parameters, 2), field(parameters, 1))
		}
	}
	return key{code: keyIgnored}
}

// The modifiers both protocols report, as they encode them: one more than the
// bits that were held, so an unmodified key is 1 rather than 0.
const (
	modifierShift = 1
	modifierAlt   = 2
	modifierCtrl  = 4
)

// reportedKey is what a key reported with its modifiers means to the message
// being composed. The two protocols spell it differently and are saying the
// same thing, so both arrive here.
//
// A terminal that reports modifiers reports the signal keys too, and stops
// raising the signals itself. They are named here rather than ignored, because
// a Ctrl-C that did nothing is worse than one this console has to raise.
func reportedKey(code, modifiers int) key {
	held := modifiers - 1
	if held < 0 {
		held = 0
	}
	switch code {
	case 0x1b:
		// The kitty protocol reports the escape key as a key of its own, which is
		// the one way of telling it from the start of a sequence without waiting.
		if held == 0 {
			return key{code: keyEscape}
		}
		return key{code: keyIgnored}
	case '\r':
		// Return with anything held is the newline this exists for. Return alone
		// still sends, which is what keeps the common case the one it always was.
		if held&(modifierShift|modifierAlt) != 0 {
			return key{code: keyNewline}
		}
		return key{code: keyEnter}
	case '\t':
		if held == 0 {
			return key{code: keyTab}
		}
		return key{code: keyIgnored}
	case 0x7f, '\b':
		if held&(modifierAlt|modifierCtrl) != 0 {
			return key{code: keyKillWord}
		}
		return key{code: keyBackspace}
	}
	if held&modifierAlt != 0 && held&modifierCtrl == 0 {
		switch code {
		case 'b':
			return key{code: keyWordLeft}
		case 'f':
			return key{code: keyWordRight}
		}
	}
	if held&modifierCtrl != 0 {
		// Ctrl-C is a key of this console's own while it is reading, as it is
		// where nothing was negotiated (decodeKey).
		if code == 'c' {
			return key{code: keyInterrupt}
		}
		if signal, raised := signalFor(rune(code)); raised {
			return key{code: keySignal, signal: signal}
		}
		if code < 'a' || code > 'z' {
			return key{code: keyIgnored}
		}
		// Everything else the terminal used to send as a control byte means what
		// it always meant, so Ctrl-U, Ctrl-W, Ctrl-K and Ctrl-Y still edit.
		pressed, _, _ := decodeKey([]byte{byte(code - 'a' + 1)})
		return pressed
	}
	if held&modifierAlt != 0 || code < 0x20 || code > utf8.MaxRune {
		return key{code: keyIgnored}
	}
	return key{code: keyRune, value: rune(code)}
}

// movesByWord reports an arrow held with Option, Alt, or Ctrl, which every
// terminal that reports it spells as a second parameter: "1;3" for Alt.
func movesByWord(parameters string) bool {
	held := field(parameters, 1) - 1
	return held > 0 && held&(modifierAlt|modifierCtrl) != 0
}

// field reads one semicolon-separated parameter as a number. A parameter that
// is absent, empty, or carries a sub-parameter this does not read — the event
// type a terminal may attach to a key — is the number in front of the colon, or
// nothing at all.
func field(parameters string, index int) int {
	parts := strings.Split(parameters, ";")
	if index >= len(parts) {
		return 0
	}
	number, _, _ := strings.Cut(parts[index], ":")
	value, err := strconv.Atoi(number)
	if err != nil {
		return 0
	}
	return value
}
