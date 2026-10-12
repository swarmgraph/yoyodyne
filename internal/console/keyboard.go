package console

// Shift-return is the same byte as return on a terminal in its legacy mode:
// the driver hands over a carriage return and says nothing about what was held
// with it. A console that offered shift-return everywhere would therefore be
// offering a key that silently does nothing on half the terminals there are,
// which is why what a terminal will report is asked rather than assumed, and
// why the fallbacks that ask the terminal for nothing are offered whatever the
// answer turns out to be.

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// keyboardMode is how much this terminal agreed to say about a keystroke.
type keyboardMode int

const (
	// keyboardLegacy is a terminal that says nothing: return and shift-return
	// arrive as the same byte, and nothing here can tell them apart.
	keyboardLegacy keyboardMode = iota
	keyboardKitty
	keyboardModifyOther
)

// signalKey is a signal the terminal driver raises itself until a keyboard is
// negotiated, after which the terminal reports the key and raises nothing.
// Ctrl-C is not one of them: it is a key of the console's own (keyInterrupt).
type signalKey int

const (
	signalNone signalKey = iota
	signalSuspend
	signalQuit
)

// signalFor reports which signal a key held with Ctrl would have raised. Only
// the keys the driver acts on are named: everything else it passes through, and
// this passes it through too.
func signalFor(pressed rune) (signalKey, bool) {
	switch pressed {
	case 'z':
		return signalSuspend, true
	case '\\':
		return signalQuit, true
	}
	return signalNone, false
}

const (
	// kittyQuery asks whether the terminal speaks the kitty keyboard protocol.
	// One that does answers with the flags it has set; one that does not says
	// nothing at all.
	kittyQuery = "\x1b[?u"
	// kittyPush turns on the one flag this needs — the terminal disambiguating
	// the keys that share a byte with another — and leaves whatever the
	// application under it had set to be popped back on the way out.
	kittyPush = "\x1b[>1u"
	kittyPop  = "\x1b[<u"
	// modifyOtherQuery asks xterm and its imitators what modifyOtherKeys is set
	// to. The answer says both that the terminal has the setting and what it was
	// before this touched it, which is what it is put back to on the way out.
	modifyOtherQuery = "\x1b[?4m"
	modifyOtherSet   = "\x1b[>4;%dm"
	// modifyOtherAll is the level that reports the keys with a meaning of their
	// own, return among them. The level below it leaves return exactly as legacy
	// mode had it, which is the thing being negotiated away.
	modifyOtherAll = 2
	// identityQuery asks the terminal who it is. Every terminal answers it, and
	// answers it in the order it was asked, so its reply is what says the
	// questions in front of it have been answered or never will be.
	identityQuery = "\x1b[c"
)

// keyboardReplyTimeout bounds the wait for a terminal that answers none of it.
// It is short because it is spent before the first prompt is drawn, and it is
// only ever spent in full by a terminal that will not even say who it is.
const keyboardReplyTimeout = 250 * time.Millisecond

// negotiateKeyboard asks the terminal whether it will report the modifiers held
// with a keystroke, and turns that reporting on where it will. It is asked
// again after anything that hands the terminal over and takes it back, because
// what a terminal agreed to before is not what it is doing now.
//
// It takes no lock, and both of its callers are why: one runs before the
// console is anybody's, and the other holds it. Nothing else may call it.
func (t *terminal) negotiateKeyboard(timeout time.Duration) {
	if _, err := io.WriteString(t.out, kittyQuery+modifyOtherQuery+identityQuery); err != nil {
		return
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var buffer []byte
	for {
		select {
		case piece, open := <-t.input:
			if !open || piece.err != nil {
				t.applyKeyboard(readKeyboardReplies(buffer))
				return
			}
			buffer = append(buffer, piece.data...)
			if replies := readKeyboardReplies(buffer); replies.finished {
				t.applyKeyboard(replies)
				return
			}
		case <-deadline.C:
			t.applyKeyboard(readKeyboardReplies(buffer))
			return
		}
	}
}

// applyKeyboard turns on what the terminal said it has, and remembers what puts
// it back. Like the negotiation it belongs to, it takes no lock.
func (t *terminal) applyKeyboard(replies keyboardReplies) {
	// Anything the terminal sent that was not an answer is the operator typing
	// ahead of the conversation, and it is theirs: it waits for the first prompt
	// rather than being swallowed with the negotiation.
	t.keys = append(t.keys, replies.typed...)
	t.keyboard = replies.mode
	switch replies.mode {
	case keyboardKitty:
		io.WriteString(t.out, kittyPush)
		t.restoreKeyboard = kittyPop
	case keyboardModifyOther:
		fmt.Fprintf(t.out, modifyOtherSet, modifyOtherAll)
		t.restoreKeyboard = fmt.Sprintf(modifyOtherSet, replies.previous)
	}
}

// keyboardReplies is what the terminal said about itself: which protocol it will
// report modifiers with, what modifyOtherKeys was set to before it was asked,
// whether it has finished answering, and everything it sent that was not an
// answer, which is the operator typing and is theirs.
type keyboardReplies struct {
	mode     keyboardMode
	previous int
	finished bool
	typed    []byte
}

// readKeyboardReplies reads the answers out of everything that has arrived. It
// is given the whole buffer each time rather than what is new, because a reply
// split across two reads is not an answer until the rest of it turns up.
func readKeyboardReplies(buffer []byte) keyboardReplies {
	var replies keyboardReplies
	for index := 0; index < len(buffer); {
		if buffer[index] != 0x1b {
			replies.typed = append(replies.typed, buffer[index])
			index++
			continue
		}
		size, complete := escapeSpan(buffer[index:])
		if !complete {
			replies.typed = append(replies.typed, buffer[index:]...)
			break
		}
		sequence := string(buffer[index : index+size])
		index += size
		switch {
		case strings.HasPrefix(sequence, "\x1b[?") && strings.HasSuffix(sequence, "u"):
			replies.mode = keyboardKitty
		case strings.HasPrefix(sequence, "\x1b[>4;") && strings.HasSuffix(sequence, "m"):
			// The kitty protocol is the better answer where both are given: it is
			// what the terminals that have both implement properly.
			if replies.mode != keyboardKitty {
				replies.mode = keyboardModifyOther
			}
			replies.previous = field(strings.TrimSuffix(sequence[len("\x1b[>"):], "m"), 1)
		case strings.HasPrefix(sequence, "\x1b[?") && strings.HasSuffix(sequence, "c"):
			replies.finished = true
		default:
			replies.typed = append(replies.typed, sequence...)
		}
	}
	return replies
}

// escapeSpan is how many bytes the escape sequence at the start of the buffer
// occupies, and whether the whole of it has arrived.
func escapeSpan(buffer []byte) (int, bool) {
	if len(buffer) < 2 {
		return 0, false
	}
	if buffer[1] != '[' && buffer[1] != 'O' {
		return 2, true
	}
	for index := 2; index < len(buffer); index++ {
		if buffer[index] >= 0x40 && buffer[index] <= 0x7e {
			return index + 1, true
		}
	}
	return 0, false
}

// composingHelp says how a message of more than one line is typed, given what
// the terminal turned out to report. It names shift-return only where the
// terminal has agreed to report it, because a help text that names a key that
// does nothing is worse than one that names a duller key that works.
func composingHelp(mode keyboardMode) string {
	switch mode {
	case keyboardKitty:
		return `Multi-line: shift-return inserts a newline — this terminal reports it, over the kitty keyboard protocol. Alt-return and a line ending in \ do the same. Return sends.`
	case keyboardModifyOther:
		return `Multi-line: shift-return inserts a newline — this terminal reports it, over modifyOtherKeys. Alt-return and a line ending in \ do the same. Return sends.`
	}
	return `Multi-line: this terminal does not report shift-return, so it sends like return; /terminal-setup sets the terminal up so that it adds a newline, where the terminal can be. Alt-return inserts a newline, and so does ending a line with \. Return sends.`
}
