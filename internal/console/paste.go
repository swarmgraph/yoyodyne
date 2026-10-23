package console

// A paste arrives at the console exactly as typing does unless the terminal is
// told otherwise: byte by byte, with each newline in it the same carriage return
// that return sends. So a paste of three lines used to send the first as the
// message and spill the other two into the prompts that followed. Bracketed
// paste is the terminal's answer to that. Asked for it, the terminal wraps what
// is pasted in a pair of markers, and everything between them is one block the
// console takes whole, its newlines and all, rather than a run of keystrokes.
//
// It is asked for rather than negotiated, unlike the keyboard: there is no
// question a terminal answers about it, and one that does not have the mode
// ignores the request and goes on sending a paste as keystrokes, which is no
// worse than before. kitty, xterm, and Terminal.app all have it.

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// pasteOn asks the terminal to bracket what is pasted, and pasteOff stops it
	// doing so. The mode is turned on for the life of the conversation and off
	// whenever the terminal changes hands, exactly as the keyboard is, so the
	// shell that gets the terminal back is not left with markers it did not ask
	// for around its own pastes.
	pasteOn  = "\x1b[?2004h"
	pasteOff = "\x1b[?2004l"
	// pasteBegin and pasteEnd are the markers the terminal puts around a paste.
	pasteBegin = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// decodePaste reads a bracketed paste from the start of the buffer. It reports
// whether the buffer opened with one at all — anything else is left to the
// escape decoding that would otherwise have read it — and, where it did, how
// many bytes the whole of it took and whether the whole of it has arrived. A
// paste whose closing marker has not turned up yet waits for it rather than
// being applied in halves: the terminal writes the markers around the block it
// was given, so the end is coming. One that has not come by the time a whole
// message's worth has arrived is taken for a marker nobody will close, for the
// reason an escape that goes on too long is: the opening marker is dropped and
// what followed it is read as the keystrokes it would have been without it,
// rather than holding every keystroke after it for good.
func decodePaste(buffer []byte) (key, int, bool, bool) {
	if !bytes.HasPrefix(buffer, []byte(pasteBegin)) {
		return key{}, 0, false, false
	}
	body := buffer[len(pasteBegin):]
	end := bytes.Index(body, []byte(pasteEnd))
	if end < 0 {
		if len(body) > MaxLineBytes {
			return key{code: keyIgnored}, len(pasteBegin), true, true
		}
		return key{}, 0, false, true
	}
	size := len(pasteBegin) + end + len(pasteEnd)
	return key{code: keyPaste, text: pastedText(body[:end])}, size, true, true
}

// pastedText is what a pasted block becomes in the message. The line structure
// is kept exactly, whichever way the terminal spelled it: a terminal sends the
// newlines of a paste as carriage returns, as a line feed, or as the pair, and
// every one of those is the one newline the operator sees. Everything else that
// is not text is not kept, because it is not something the region can draw. A
// tab becomes a space rather than nothing, since dropping it would run two words
// together; the region measures a rune as one column and a tab is as many as
// the terminal decides, so kept as it was it would leave the erase a row short.
// Other control characters, and the escape that would start a sequence, are
// dropped as they are when typed, and a byte that is not text at all is dropped
// with them, as is a rune that would read as a held paste's marker.
func pastedText(pasted []byte) string {
	var text strings.Builder
	text.Grow(len(pasted))
	for index := 0; index < len(pasted); {
		character, size := utf8.DecodeRune(pasted[index:])
		index += size
		switch {
		case character == '\r':
			if index < len(pasted) && pasted[index] == '\n' {
				index++
			}
			text.WriteByte('\n')
		case character == '\n':
			text.WriteByte('\n')
		case character == '\t':
			text.WriteByte(' ')
		case character < 0x20, character == 0x7f:
		case character == utf8.RuneError && size == 1:
		case pasteMarker(character):
		default:
			text.WriteRune(character)
		}
	}
	return text.String()
}

// A long paste is shown in the line being composed as one label, such as
// [Pasted text #1 +42 lines], rather than drawn in full, which would fill the
// screen with text the operator already has and push what they are typing
// around it out of sight. The text is held aside and the line carries a single
// marker rune where it goes, so every edit treats the paste as one character:
// the cursor steps over it, backspace removes it whole, and a kill and Ctrl-Y
// carry it like any other rune. The message sent is the line with each marker
// replaced by the text it holds, so what the conversation receives and records
// is the whole of what was pasted, in place.
//
// A short paste is not worth hiding, and is inserted as it was typed.
const (
	// shownPasteLines and shownPasteRunes are the most a paste can be and still
	// be drawn as it is: a few lines, and no more text than a few lines of a
	// wide window hold.
	shownPasteLines = 3
	shownPasteRunes = 1000
	// firstPasteMarker and lastPasteMarker bound the runes that stand for a held
	// paste. They are the last private-use plane, which no keyboard sends and no
	// text the operator could mean carries, and a rune from there that arrives
	// typed or pasted is dropped (pastedText, apply) so it can never be mistaken
	// for one.
	firstPasteMarker rune = 0x100000
	lastPasteMarker  rune = 0x10FFFD
)

// pasteMarker reports whether a rune is one that stands for a held paste.
func pasteMarker(value rune) bool {
	return value >= firstPasteMarker && value <= lastPasteMarker
}

// pastedLines is how many lines a paste holds. A newline at its very end ends
// its last line rather than starting one more, which is how an editor counts.
func pastedLines(text string) int {
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
}

// longPaste reports whether a paste is shown as a label rather than as itself.
func longPaste(text string) bool {
	return pastedLines(text) > shownPasteLines || utf8.RuneCountInString(text) > shownPasteRunes
}

// pasteLabel is what a held paste is drawn as: which paste it is, counted from
// one, and how much it holds, in lines where it has more than one and in
// characters where it is one long line.
func pasteLabel(marker rune, text string) string {
	number := int(marker-firstPasteMarker) + 1
	if lines := pastedLines(text); lines > 1 {
		return fmt.Sprintf("[Pasted text #%d +%d lines]", number, lines)
	}
	return fmt.Sprintf("[Pasted text #%d +%d characters]", number, utf8.RuneCountInString(text))
}

// holdPaste keeps a long paste aside and returns the marker that stands for it
// in the line. It reports false for a paste short enough to insert as it is,
// and for one arriving when every marker is taken, which is inserted as it is
// too rather than lost.
func (t *terminal) holdPaste(text string) (rune, bool) {
	if !longPaste(text) {
		return 0, false
	}
	marker := firstPasteMarker + rune(len(t.pastes))
	if marker > lastPasteMarker {
		return 0, false
	}
	t.pastes = append(t.pastes, text)
	return marker, true
}

// shown is the line as it is drawn, each held paste as its label.
func (t *terminal) shown(line []rune) string {
	return t.expand(line, pasteLabel)
}

// sent is the line as it is sent, each held paste as the whole of its text.
func (t *terminal) sent(line []rune) string {
	return t.expand(line, func(_ rune, text string) string { return text })
}

func (t *terminal) expand(line []rune, held func(rune, string) string) string {
	var text strings.Builder
	for _, value := range line {
		if !pasteMarker(value) {
			text.WriteRune(value)
			continue
		}
		if index := int(value - firstPasteMarker); index < len(t.pastes) {
			text.WriteString(held(value, t.pastes[index]))
		}
	}
	return text.String()
}

// releasePastes lets go of the held pastes once nothing can bring one back:
// the line is empty, and neither what Ctrl-Y puts back nor the message up
// would return to carries a marker. Numbering starts again from one then. Until
// then they are kept, so a paste cut out of one message can be put into the
// next.
func (t *terminal) releasePastes() {
	for _, held := range [][]rune{t.line, t.killed, t.draft} {
		for _, value := range held {
			if pasteMarker(value) {
				return
			}
		}
	}
	t.pastes = nil
}
