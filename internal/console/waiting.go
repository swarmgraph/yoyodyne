package console

import (
	"errors"
	"strings"
)

// Waiting is a console the operator can go on typing into while an answer is
// being worked on. What they send then is not said to anybody yet: it waits, on
// screen, until the conversation is ready for it, and until then it is still
// theirs to change or take back. A conversation asks for this by asserting it,
// so a console that cannot offer it — a stream, which has no screen to keep a
// message waiting on — is the conversation it always was, read a line at a time
// once each answer has been written.
type Waiting interface {
	// BesideTurn puts the console in the state a turn being answered calls for,
	// and returns what ends it. Until then a line Prompt reads is not written
	// into the conversation, because it is about to be shown waiting or answered
	// as a command and the caller writes it where it belongs; and Esc, or Ctrl-C
	// on an empty line, makes Prompt return ErrStopTurn, keeping whatever was
	// typed, because those are the keys that stop a turn (WatchForStop).
	BesideTurn() (end func())
	// Wait shows a message as waiting to be sent, after any already waiting.
	Wait(message string)
	// NextWaiting takes the oldest message still waiting off the screen and
	// returns it. It reports false when nothing is waiting, which includes a
	// message the operator took back to change and has not sent again.
	NextWaiting() (string, bool)
}

// waitingLabel opens each row that shows a message waiting, and waitingHint
// ends the last of them, saying the one thing about a waiting message that
// cannot be guessed: where it goes to be changed.
const (
	waitingLabel = "waiting to send: "
	waitingHint  = "  (↑ to change or withdraw)"
)

// ErrStopTurn reports that the operator pressed a key that stops the turn being
// answered while they were typing beside it. What they had typed is kept, and
// the next prompt picks it up.
var ErrStopTurn = errors.New("the operator asked to stop the turn")

// BesideTurn holds Ctrl-C as the console's own key for the whole turn, as
// WatchForStop does, rather than only while a prompt is open: between two
// prompts the conversation may be carrying out a command, and a Ctrl-C pressed
// then has to stop the turn rather than the process. The status row says which
// key stops it for as long as this lasts.
func (t *terminal) BesideTurn() func() {
	t.claimInterrupt()
	t.mu.Lock()
	t.besideTurn = true
	t.watching = true
	t.redraw()
	t.mu.Unlock()
	var ended bool
	return func() {
		t.mu.Lock()
		if ended {
			t.mu.Unlock()
			return
		}
		ended = true
		t.besideTurn = false
		t.watching = false
		t.redraw()
		t.mu.Unlock()
		t.releaseInterrupt()
	}
}

// stopsTurn reports whether a key pressed beside a turn stops it: Esc always,
// and Ctrl-C where there is no text for it to clear, which is what it does at
// any other prompt.
func (t *terminal) stopsTurn(pressed key) bool {
	if !t.besideTurn {
		return false
	}
	return pressed.code == keyEscape || (pressed.code == keyInterrupt && len(t.line) == 0)
}

// Wait puts a message in the region, above the line being composed.
func (t *terminal) Wait(message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || message == "" {
		return
	}
	t.waiting = append(t.waiting, message)
	t.redraw()
}

// NextWaiting takes the oldest waiting message out of the region.
func (t *terminal) NextWaiting() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.waiting) == 0 {
		return "", false
	}
	next := t.waiting[0]
	t.waiting = t.waiting[1:]
	if !t.closed {
		t.redraw()
	}
	return next, true
}

// takeBackWaiting is up pressed on an empty line while something is waiting: the
// message sent last comes back into the line to be changed, and is no longer
// waiting. Sending it again puts it back; emptying the line withdraws it. It is
// only an empty line that does this, so a message being written is never put
// aside for one that was already sent, and up goes on recalling earlier
// messages once nothing is waiting.
func (t *terminal) takeBackWaiting() bool {
	if len(t.line) != 0 || t.recalled != len(t.recall) || len(t.waiting) == 0 {
		return false
	}
	last := len(t.waiting) - 1
	t.line = []rune(t.waiting[last])
	t.cursor = len(t.line)
	t.waiting = t.waiting[:last]
	return true
}

// waitingRegion is the rows that show what is waiting, one row each, newest
// last, and no more of them than leave the line being composed a row of its
// own. A message is shown by the start of its first line, cut to the width,
// because the row is there to say what is waiting rather than to be read in
// full: up puts the whole of it back.
func (t *terminal) waitingRegion(width, rows int) []string {
	if len(t.waiting) == 0 || rows < 2 {
		return nil
	}
	shown := t.waiting
	if len(shown) > rows-1 {
		shown = shown[len(shown)-(rows-1):]
	}
	lines := make([]string, 0, len(shown))
	for index, message := range shown {
		hint := ""
		if index == len(shown)-1 {
			hint = waitingHint
		}
		lines = append(lines, t.theme.Detail(waitingRow(message, hint, width)))
	}
	return lines
}

// waitingRow is one waiting message as a single row of this width. What does
// not fit is cut and marked as cut, and so is a message of more than one line,
// so a row never claims to be the whole of what was sent.
func waitingRow(message, hint string, width int) string {
	first, rest, more := strings.Cut(message, "\n")
	text := []rune(waitingLabel + first)
	cut := more && strings.TrimSpace(rest) != ""
	room := width - len([]rune(hint))
	if room < len([]rune(waitingLabel))+2 {
		// A window too narrow for the hint keeps the message instead: it is what
		// the row is for.
		hint = ""
		room = width
	}
	if len(text) > room-1 || (cut && len(text) > room-2) {
		text = text[:max(room-2, 0)]
		cut = true
	}
	row := string(text)
	if cut {
		row += " …"
	}
	return row + hint
}
