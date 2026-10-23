package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// terminal is the conversation on a terminal, with the line being composed kept
// in a region of its own at the bottom of the screen. Everything written goes
// above that region: the region is erased, the text is written into the
// scrollback where it stays, and the region is drawn again underneath. Nothing
// is written into the alternate screen and nothing is redrawn above the current
// line, so the terminal's own scrollback, selection, and copying keep working
// on the conversation exactly as they would on any other command's output.
//
// Everything below runs under mu. Output arrives from whichever goroutine is
// talking and keystrokes are applied by the one that is prompting, so the
// screen is only ever coherent if one of them holds it at a time.
type terminal struct {
	mu      sync.Mutex
	out     io.Writer
	input   <-chan chunk
	restore func() error
	// modes puts this conversation's own terminal settings on again after
	// something took them off, and hands back what restores the operator's. It
	// exists because Ctrl-Z hands the terminal to the shell and then takes it
	// back, so entering cbreak cannot be something that happens only once. It is
	// nil where there is no terminal to set — a console over pipes in a test —
	// and nothing there was handed over to begin with.
	modes func() (func() error, error)
	// raise delivers a signal the terminal used to raise for itself. It is a
	// field so a test can watch what a key was turned into without stopping the
	// process it is running in.
	raise func(signalKey)
	// width is asked for at every draw rather than remembered, so a window the
	// operator resized is drawn at the size it is now.
	width func() int
	// height is the same question about the other dimension, and it bounds the
	// region rather than wrapping it: a region taller than the window scrolls off
	// the top of it, and what has scrolled off cannot be climbed back over to
	// erase, so the erase would start inside the conversation instead.
	height func() int
	// theme is how much this terminal's environment permits it to be dressed.
	// It is fixed for the life of the console, because an operator who set
	// NO_COLOR set it before the conversation opened.
	theme Theme

	// pending is text written without a newline yet. It is held back rather than
	// drawn, because half a line above the composing region cannot be erased
	// again without taking the operator's own text with it.
	pending []byte

	// keys is input read but not yet applied: a rune split across two reads, or
	// a second line typed ahead while the first was being answered.
	keys []byte

	// keyboard is how much this terminal agreed to say about a keystroke, and
	// restoreKeyboard is what puts that agreement back as it was found.
	keyboard        keyboardMode
	restoreKeyboard string
	// keyboardWait is how long a negotiation waits for a terminal that answers
	// none of it, which is keyboardReplyTimeout. It is a field so a test whose
	// terminal does answer can wait for the answer, which ends the negotiation,
	// rather than bet that it lands inside a quarter of a second.
	keyboardWait time.Duration
	// escapeAfter starts the wait for an escape byte to be known as the escape
	// key, which is escapeWait; endAfter is endWindow; now is the clock the window
	// is measured on. They are fields for the same reason keyboardWait is: a test
	// decides when they pass rather than sleeping through them.
	escapeAfter func() <-chan time.Time
	endAfter    time.Duration
	now         func() time.Time

	// interruptKey takes Ctrl-C over from the terminal as a key, or gives it back
	// (setInterruptCharacter). It is nil where there is no terminal to set — a
	// console over pipes in a test, where Ctrl-C was only ever a byte. reading
	// counts what is reading the keyboard now — a prompt, a list, a turn being
	// watched — and Ctrl-C is the console's while anything is.
	interruptKey func(owned bool)
	reading      int

	// ending is when Ctrl-C was pressed on an empty prompt, and is zero when it
	// was not: a second press inside endAfter ends the conversation, and any other
	// key, or the window passing, forgets the first.
	ending time.Time

	// watching says a turn is being answered and the keyboard is being read for
	// the keys that stop it (WatchForStop), which the account of work in progress
	// names while it lasts.
	watching bool

	prompting  bool
	promptText string
	// besideTurn says the operator is typing while a turn is being answered
	// (BesideTurn, in waiting.go): a line sent is not written into the
	// transcript, because whoever asked for it shows it themselves, and the keys
	// that stop a turn stop it.
	besideTurn bool
	// line is the message being composed, which may be more than one line of it:
	// a newline in here is a newline the operator typed, and it reaches the
	// message they send exactly as it is drawn.
	line   []rune
	cursor int
	// killed is what Ctrl-K, Ctrl-U, Ctrl-W, or Option-backspace last deleted,
	// which Ctrl-Y puts back. It outlives the message it came from, so text cut
	// from one message can be put into the next. killing says the last key
	// applied was one of them, so the next one adds to it rather than replacing it.
	killed  []rune
	killing bool
	// pastes is the text of each long paste the line shows as a label, which a
	// marker rune in line, killed, or draft stands for (paste.go).
	pastes []string

	// recall is the messages sent earlier in the conversation, oldest first,
	// which up and down step through (recall.go). recalled is which of them is
	// in the line now, and len(recall) is none of them: the message being
	// written. draft and draftCursor are that message as it stood when up first
	// took it off the line, kept so coming back down puts it back intact.
	recall      []string
	recalled    int
	draft       []rune
	draftCursor int

	// choosing says the region is carrying a list of answers to pick from,
	// choices is what is on offer, and chosen is the one under the marker.
	// entering says they picked their own words and are typing them, which is a
	// line being composed like any other: it is remembered because a prompt
	// interrupted so the harness could write something has to resume as the
	// prompt it was rather than putting the list again.
	choosing bool
	choices  []string
	chosen   int
	entering bool

	// commands is what a slash on an empty line lists (commands.go). menuOpen
	// says that slash was typed and Esc has not put the list away, and
	// menuChosen is which of the commands still listed is under the marker.
	commands   []Command
	menuOpen   bool
	menuChosen int

	// status is the account of work in progress and resting is what is left on
	// that line between turns. They share one row of the region, and work in
	// progress covers what is merely true for as long as it lasts: what the
	// operator is waiting on is the more urgent of the two, and a second row
	// would take another line of their screen for good.
	status  string
	resting string

	// waiting is what the operator sent while an answer was being worked on,
	// oldest first, kept on screen until the conversation takes it (waiting.go).
	waiting []string

	// drawn says whether a region is on screen, drawnStatus is the status line it
	// was drawn with, drawnWaiting is the rows of waiting messages it was drawn
	// with, drawnChoices is the list of answers it was drawn with,
	// drawnComposed is the prompt and the message as they were drawn, and
	// drawnCursor is where the cursor was left in that text, counted in runes.
	// The rows the status and the list occupy and the row the cursor is on are
	// all worked out from that text and the width at the time rather than
	// remembered, so a window the operator resized between the drawing and the
	// erasing is erased at the size the terminal has rewrapped it to.
	drawn         bool
	drawnStatus   string
	drawnWaiting  []string
	drawnChoices  []string
	drawnComposed string
	drawnCursor   int
	closed        bool
}

// chunk is one read from the operator's terminal.
type chunk struct {
	data []byte
	err  error
}

func openTerminal(in, out *os.File, env func(string) string) (*terminal, error) {
	restore, err := enterCBreak(in.Fd())
	if err != nil {
		return nil, err
	}
	width := func() int { return terminalWidth(out.Fd()) }
	height := func() int { return terminalHeight(out.Fd()) }
	terminal := newTerminal(in, out, width, height, restore)
	terminal.modes = func() (func() error, error) { return enterCBreak(in.Fd()) }
	// A terminal whose interrupt character cannot be read keeps Ctrl-C as the
	// interrupt it always was: the conversation still ends on it, which is worse
	// than clearing the prompt and better than a key that does nothing.
	if character, err := interruptCharacter(in.Fd()); err == nil {
		terminal.interruptKey = func(owned bool) {
			if owned {
				setInterruptCharacter(in.Fd(), noCharacter)
				return
			}
			setInterruptCharacter(in.Fd(), character)
		}
	}
	terminal.theme = NewTheme(env, width)
	// What the terminal will say about a keystroke is settled before the first
	// prompt is drawn, because it decides both what shift-return does and what
	// /help is allowed to claim it does.
	terminal.negotiateKeyboard(terminal.keyboardWait)
	// A paste is asked to arrive bracketed for the same span, so a block of
	// several lines is one message rather than the first of them and a spill.
	io.WriteString(out, pasteOn)
	return terminal, nil
}

// newTerminal builds the console over whatever the caller supplies, so the
// region and the editing can be exercised without a real terminal to drive.
func newTerminal(in io.Reader, out io.Writer, width, height func() int, restore func() error) *terminal {
	return &terminal{
		out:          out,
		input:        readInput(in),
		restore:      restore,
		raise:        raiseSignal,
		width:        width,
		height:       height,
		keyboardWait: keyboardReplyTimeout,
		escapeAfter:  func() <-chan time.Time { return time.After(escapeWait) },
		endAfter:     endWindow,
		now:          time.Now,
	}
}

// readInput reads the operator's terminal for as long as it lasts. It is a
// goroutine of its own because a read that is blocked in the terminal driver
// cannot be waited on beside anything else, and Prompt has to be able to give
// up on one to report a run that has finished.
func readInput(in io.Reader) <-chan chunk {
	stream := make(chan chunk, 4)
	go func() {
		defer close(stream)
		if in == nil {
			return
		}
		buffer := make([]byte, 256)
		for {
			count, err := in.Read(buffer)
			if count > 0 {
				data := make([]byte, count)
				copy(data, buffer[:count])
				stream <- chunk{data: data}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					stream <- chunk{err: err}
				}
				return
			}
		}
	}()
	return stream
}

// Write puts text above the composing region. Whole lines are written and stay
// written; a trailing part-line waits for its newline.
func (t *terminal) Write(text []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, errors.New("the console is closed")
	}
	var ready []string
	t.pending = append(t.pending, text...)
	for {
		index := indexNewline(t.pending)
		if index < 0 {
			break
		}
		ready = append(ready, string(t.pending[:index]))
		t.pending = t.pending[index+1:]
	}
	if len(ready) == 0 {
		return len(text), nil
	}
	if err := t.render(ready); err != nil {
		return 0, err
	}
	return len(text), nil
}

// render erases the region, writes the finished lines into the scrollback, and
// draws the region again. It is the only path that puts anything on screen, so
// the region can never be left half erased.
func (t *terminal) render(lines []string) error {
	var out strings.Builder
	out.WriteString(t.eraseRegion())
	for _, line := range lines {
		out.WriteString(line)
		// Output processing is left as the terminal had it, so a newline is
		// still a newline and the harness writes lines the way it always did.
		out.WriteString("\n")
	}
	out.WriteString(t.drawRegion())
	_, err := io.WriteString(t.out, out.String())
	return err
}

// eraseRegion returns the sequence that takes the composing region off the
// screen, leaving the cursor where the region began. It only ever moves within
// the region it drew itself, so the conversation above stays exactly where the
// terminal put it.
func (t *terminal) eraseRegion() string {
	if !t.drawn {
		return ""
	}
	var out strings.Builder
	if rows := t.statusRows() + t.waitingRows() + t.choiceRows() + cursorRow(t.drawnComposed, t.drawnCursor, t.columns()); rows > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", rows)
	}
	out.WriteString("\r\x1b[J")
	t.drawn = false
	t.drawnStatus = ""
	t.drawnWaiting = nil
	t.drawnChoices = nil
	t.drawnComposed = ""
	t.drawnCursor = 0
	return out.String()
}

// statusRows is how many rows the status line took at the width the terminal
// has now, counting the newline that ends it.
func (t *terminal) statusRows() int { return statusHeight(t.drawnStatus, t.columns()) }

// statusHeight is how many rows a status line occupies at this width. A line
// that exactly fills the width still occupies one row: the terminal defers the
// wrap, and the newline is what commits it. Drawing and erasing take this same
// measure, so what is climbed over is what was put there.
func statusHeight(text string, width int) int {
	if text == "" {
		return 0
	}
	return (visibleWidth(text)-1)/width + 1
}

// waitingRows is how many rows the waiting messages took at the width the
// terminal has now, measured as the status line is.
func (t *terminal) waitingRows() int {
	rows := 0
	for _, line := range t.drawnWaiting {
		rows += statusHeight(line, t.columns())
	}
	return rows
}

// choiceRows is how many rows the list of answers took at the width the
// terminal has now, counted the same way the status is: every row of it is
// above the cursor, so an erase that climbs one too few leaves half a list on
// screen and one too many takes a line of the conversation with it.
func (t *terminal) choiceRows() int {
	rows := 0
	for _, line := range t.drawnChoices {
		rows += (visibleWidth(line)-1)/t.columns() + 1
	}
	return rows
}

// columns is how wide the terminal is now. It divides the arithmetic that
// positions the cursor, so a terminal that answers with nothing usable is
// floored rather than allowed to take the conversation down with it.
func (t *terminal) columns() int {
	if width := t.width(); width > 0 {
		return width
	}
	return 1
}

// rows is how tall the terminal is now. It bounds the region, so a terminal
// that answers with nothing usable is floored at the one row a region needs
// rather than left bounding it to nothing at all.
func (t *terminal) rows() int {
	if height := t.height(); height > 0 {
		return height
	}
	return 1
}

// drawRegion returns the sequence that puts the region back: the account of
// work in progress if there is one, the line being composed under it, and the
// cursor where the operator left it. The whole of it is drawn within the window
// the operator has, because a region that scrolled off the top of the window
// cannot be erased again from where it began.
func (t *terminal) drawRegion() string {
	status := t.statusLine()
	if !t.prompting && !t.choosing && status == "" && len(t.waiting) == 0 {
		return ""
	}
	width := t.columns()
	rows := t.rows()
	var out strings.Builder
	// Where the window has no room for both, the status gives up its row: the
	// region has to fit, and what the operator is typing is the part of it they
	// cannot do without.
	if statusHeight(status, width) >= rows {
		status = ""
	}
	if status != "" {
		// The status is written and left behind: the region is redrawn as a
		// whole, so it is put back on every draw rather than moved.
		out.WriteString(status)
		out.WriteString("\n")
		rows -= statusHeight(status, width)
	}
	t.drawnStatus = status
	// What the operator sent while an answer was being worked on sits between
	// what the harness is doing and what they are typing, because it is theirs
	// and is not said yet: it goes out once the answer above it has arrived.
	waiting := t.waitingRegion(width, rows)
	for _, line := range waiting {
		out.WriteString(line)
		out.WriteString("\n")
		rows -= statusHeight(line, width)
	}
	t.drawnWaiting = waiting
	// The answers on offer sit under the status, and are redrawn under whatever
	// the harness writes exactly as the composing line is: the operator is
	// picking from a list rather than typing into one, so the list has to stay
	// under their cursor keys while the conversation carries on above it.
	choices := t.choiceRegion()
	for _, line := range choices {
		out.WriteString(line)
		out.WriteString("\n")
		rows -= (visibleWidth(line)-1)/width + 1
	}
	t.drawnChoices = choices
	if !t.prompting {
		// Nothing is being composed, so the cursor rests at the start of the row
		// below whatever the region drew — the status, the answers on offer, or
		// both — which is where the next thing written will go.
		t.drawn = true
		t.drawnComposed = ""
		t.drawnCursor = 0
		return out.String()
	}
	composed := t.promptText + t.shown(t.line)
	// Where the cursor is in the composed text, counted in runes, because that is
	// what `place` steps through. It is not the same as the columns the prompt
	// occupies: a prompt with colour in it carries runes that take no room at
	// all, and counting those as columns would put the cursor past the end of
	// what the operator can see.
	cursor := utf8.RuneCountInString(t.promptText) + utf8.RuneCountInString(t.shown(t.line[:t.cursor]))
	// A message with more lines than the window has rows is drawn as the part of
	// it that fits. Everything after this point measures the text that is drawn
	// rather than the whole message, so the erase climbs back over rows the
	// region really put on the screen.
	composed, cursor = boundComposed(composed, cursor, width, rows)
	// The list of commands goes under the line being composed, in whatever rows
	// the window has left below it. It is below the cursor, so the erase, which
	// clears from where the region begins to the end of the screen, takes it
	// without climbing over it.
	menu := t.menuRegion(width, rows-drawnRows(composed, width))
	// A newline the operator typed is written as one: the message occupies as
	// many rows as it has lines, and the rest of the region is measured from the
	// same text, so what is erased is what was drawn.
	out.WriteString(composed)
	endRow, endColumn := place(composed, utf8.RuneCountInString(composed), width)
	// A row that exactly fills the width leaves the cursor in the terminal's
	// deferred-wrap state, where it is neither on this row nor the next. One
	// space commits the wrap and the carriage return puts it at the start of the
	// row it is really on, so the arithmetic below describes the screen.
	if endColumn >= width {
		out.WriteString(" \r")
		endRow++
		endColumn = 0
	}
	for _, line := range menu {
		out.WriteString("\n")
		out.WriteString(line)
		endRow++
	}
	targetRow, targetColumn := place(composed, cursor, width)
	if targetColumn >= width {
		targetRow++
		targetColumn = 0
	}
	if up := endRow - targetRow; up > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", up)
	}
	out.WriteString("\r")
	if targetColumn > 0 {
		fmt.Fprintf(&out, "\x1b[%dC", targetColumn)
	}
	t.drawn = true
	t.drawnComposed = composed
	t.drawnCursor = cursor
	return out.String()
}

// place is where the cursor sits once the first index runes of text have been
// written: the row, counted from the row the text began on, and the column
// across it. Runes go in and columns come out, and the two differ — an escape
// sequence is several runes and no columns at all, because it dresses what
// follows it rather than taking room on the screen — so a caller counts what it
// passes in with RuneCountInString and reads what it gets back as the screen.
//
// A row that is exactly full is reported as the column past its end, which is
// the terminal's deferred-wrap state — neither on that row nor the next until
// something else is written — and is what its callers resolve.
func place(text string, index, width int) (int, int) {
	runes := []rune(text)
	row, column := 0, 0
	for position := 0; position < index && position < len(runes); {
		if runes[position] == 0x1b {
			position += escapeRunes(runes[position:])
			continue
		}
		character := runes[position]
		position++
		if character == '\n' {
			row++
			column = 0
			continue
		}
		if column >= width {
			row++
			column = 0
		}
		column++
	}
	return row, column
}

// escapeRunes is how many runes the escape sequence at the start of the runes
// occupies. What it names is what the region steps over rather than counts: the
// dressing a theme wrote is not somewhere the cursor can be.
func escapeRunes(runes []rune) int {
	if len(runes) < 2 || (runes[1] != '[' && runes[1] != 'O') {
		return 1
	}
	for position := 2; position < len(runes); position++ {
		if runes[position] >= 0x40 && runes[position] <= 0x7e {
			return position + 1
		}
	}
	return len(runes)
}

// cursorRow is how many rows below the start of the text the cursor is, which
// is how far the region has to climb to erase itself.
func cursorRow(text string, index, width int) int {
	row, column := place(text, index, width)
	if column >= width {
		row++
	}
	return row
}

// drawnRows is how many rows text occupies once it has been drawn. It is one
// more than the row the end of the text lands on, and a text that ends on an
// exactly full row lands on the row after it, because drawing commits that
// deferred wrap rather than leaving the cursor between two rows.
func drawnRows(text string, width int) int {
	return cursorRow(text, utf8.RuneCountInString(text), width) + 1
}

// rowStarts is the rune index each row of the text begins at when it is written
// at this width. The first row begins at nothing, a newline the operator typed
// begins the next row after itself, and a row the width filled begins the next
// at the rune that would not fit on it.
func rowStarts(text string, width int) []int {
	runes := []rune(text)
	starts := []int{0}
	column := 0
	for position := 0; position < len(runes); {
		if runes[position] == 0x1b {
			position += escapeRunes(runes[position:])
			continue
		}
		character := runes[position]
		position++
		if character == '\n' {
			starts = append(starts, position)
			column = 0
			continue
		}
		if column >= width {
			starts = append(starts, position-1)
			column = 0
		}
		column++
	}
	return starts
}

// rowIndex is which of those rows a rune index falls on.
func rowIndex(starts []int, index int) int {
	row := 0
	for row+1 < len(starts) && starts[row+1] <= index {
		row++
	}
	return row
}

// escapesIn is the escape sequences in the runes and nothing else. Replaying
// them leaves the terminal dressed exactly as the text before them left it, and
// in no columns at all, so a region that begins part way down a message is
// coloured as the whole of it would have been.
func escapesIn(runes []rune) []rune {
	var kept []rune
	for position := 0; position < len(runes); {
		if runes[position] != 0x1b {
			position++
			continue
		}
		size := escapeRunes(runes[position:])
		kept = append(kept, runes[position:position+size]...)
		position += size
	}
	return kept
}

// boundComposed is the part of the composed text a window this many rows tall
// has room for, and where the cursor sits in it.
//
// A region taller than the window scrolls off the top of it as it is drawn, and
// what has scrolled off is no longer somewhere the cursor can be moved: the
// erase would climb until it hit the top of the window and clear from there,
// which is inside the conversation. So a region that will not fit is cut to
// what does. What is kept is the end of the message, which is where the
// operator is typing, moved up far enough that the cursor is still on screen.
// Only what is drawn is cut — the message they send is the whole of it.
func boundComposed(text string, cursor, width, rows int) (string, int) {
	if rows < 1 {
		rows = 1
	}
	if drawnRows(text, width) <= rows {
		return text, cursor
	}
	runes := []rune(text)
	starts := rowStarts(text, width)
	cursorAt := rowIndex(starts, cursor)
	last := len(starts) - 1
	if cursorAt+rows-1 < last {
		last = cursorAt + rows - 1
	}
	// What is kept ends at the end of the message, or one rune short of the row
	// after it. That rune is the operator's own newline where the row ended in
	// one, and the last column of a row the width filled exactly otherwise, whose
	// wrap the drawing would commit onto a row this region does not have.
	end := len(runes)
	if last < len(starts)-1 {
		end = starts[last+1] - 1
	}
	first := last
	for first > 0 && drawnRows(string(runes[starts[first-1]:end]), width) <= rows {
		first--
	}
	if first > cursorAt {
		first = cursorAt
	}
	visible := runes[starts[first]:end]
	// The row the cursor is on is kept whatever else goes, so where keeping it
	// leaves one row too many it is the end that gives way.
	for len(visible) > 0 && drawnRows(string(visible), width) > rows {
		visible = visible[:len(visible)-1]
	}
	prefix := escapesIn(runes[:starts[first]])
	moved := len(prefix) + cursor - starts[first]
	if moved < 0 {
		moved = 0
	}
	if moved > len(prefix)+len(visible) {
		moved = len(prefix) + len(visible)
	}
	return string(prefix) + string(visible), moved
}

// redraw puts the region back where it belongs after the state behind it
// changed. It writes nothing into the scrollback.
func (t *terminal) redraw() error {
	return t.render(nil)
}

// Working draws the account of work in progress on a line of its own below the
// conversation, where it is erased and drawn again exactly as the composing
// line is. It is a region rather than ordinary output for the same reason the
// composing line is: an indicator that scrolled away with the transcript would
// be a log of itself, and one written into the operator's own line would be
// worse than none at all.
func (t *terminal) Working(phase string) Activity {
	return newSpinner(phase, t.setStatus, time.Now, spinnerInterval)
}

// Theme reports how much this terminal may be dressed.
func (t *terminal) Theme() Theme { return t.theme }

// Composing says how a message of more than one line is typed on this terminal,
// which is what it turned out to report rather than what terminals usually do.
func (t *terminal) Composing() string { return composingHelp(t.keyboard) }

// setStatus replaces what the activity line says. A console that has been
// closed keeps the screen the operator's shell left it with, and an unchanged
// line is not redrawn, so a display that has nothing new to say costs nothing.
func (t *terminal) setStatus(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.status == text {
		return
	}
	t.status = text
	t.redraw()
}

// Status replaces what rests on that line between turns. It takes effect at
// once when nothing is being waited for, and otherwise when the account of work
// in progress ends: the two would fight over one row, and the work is what the
// operator is waiting to hear about.
func (t *terminal) Status(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.resting == text {
		return
	}
	t.resting = text
	if t.status != "" {
		return
	}
	t.redraw()
}

// The lines the status row carries for the keys this console gives a meaning of
// their own: what stops the turn being answered, and what a first Ctrl-C on an
// empty prompt is waiting for.
const (
	stopHint     = " · esc to stop"
	endingNotice = "press ctrl-c again to end the conversation"
)

// statusLine is what the region's top row says now. A first Ctrl-C on an empty
// prompt says what a second would do, over anything else on the row, because it
// is the one line here that is about to matter; work in progress that the
// keyboard is being watched for says which key stops it.
func (t *terminal) statusLine() string {
	if !t.ending.IsZero() {
		return endingNotice
	}
	if t.status != "" {
		if t.watching {
			return t.status + stopHint
		}
		return t.status
	}
	return t.resting
}

func (t *terminal) Prompt(ctx context.Context, prompt string, interrupt <-chan struct{}) (string, error) {
	t.claimInterrupt()
	defer t.releaseInterrupt()
	line, submitted, err := t.beginPrompt(prompt)
	for {
		switch {
		case errors.Is(err, errEnded):
			// Ctrl-C twice on an empty prompt is the operator ending the
			// conversation, which is what the input ending has always meant.
			return "", t.endPrompt(io.EOF)
		case err != nil:
			return "", err
		case submitted:
			return line, nil
		}
		escape, expiry := t.timers()
		select {
		case piece, open := <-t.input:
			if !open {
				return "", t.endPrompt(io.EOF)
			}
			if piece.err != nil {
				return "", t.endPrompt(fmt.Errorf("read what the operator typed: %w", piece.err))
			}
			line, submitted, err = t.feed(piece.data, false)
		case <-escape:
			line, submitted, err = t.feedLate(t.feed)
		case <-expiry:
			t.forgetEnding()
		case <-interrupt:
			// Something else needs the screen. What has been typed stays exactly
			// as it is, and the next prompt picks it up mid-word.
			return "", ErrInterrupted
		case <-ctx.Done():
			return "", t.endPrompt(ctx.Err())
		}
	}
}

// errEnded is Ctrl-C pressed the second time on an empty prompt, which ends the
// conversation. Prompt and Choose report it as io.EOF.
var errEnded = errors.New("the operator ended the conversation")

// endWindow is how soon a second Ctrl-C has to follow the first on an empty
// prompt to end the conversation. A first one on its own only says what a
// second would do, so a Ctrl-C pressed out of habit costs nothing.
const endWindow = 2 * time.Second

// claimInterrupt and releaseInterrupt bracket anything that reads the keyboard:
// while one is, Ctrl-C is a key this console reads rather than an interrupt the
// terminal raises.
func (t *terminal) claimInterrupt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reading++
	if t.reading == 1 && t.interruptKey != nil && !t.closed {
		t.interruptKey(true)
	}
}

func (t *terminal) releaseInterrupt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reading--
	if t.reading == 0 && t.interruptKey != nil && !t.closed {
		t.interruptKey(false)
	}
}

// timers are the two waits a reader of the keyboard has besides the keyboard: an
// escape byte waiting to be known as the escape key, and a first Ctrl-C waiting
// for a second. Either is nil when nothing is waiting for it.
func (t *terminal) timers() (<-chan time.Time, <-chan time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var escape, expiry <-chan time.Time
	if lateEscape(t.keys) {
		escape = t.escapeAfter()
	}
	if !t.ending.IsZero() {
		expiry = time.After(t.endAfter - t.now().Sub(t.ending))
	}
	return escape, expiry
}

// feedLate is what a reader does when an escape byte has waited its time. Input
// that arrived as the wait ran out is read first, because it may be the rest of
// the sequence the escape began; only an escape still waiting after that is the
// escape key.
func (t *terminal) feedLate(feed func([]byte, bool) (string, bool, error)) (string, bool, error) {
	select {
	case piece, open := <-t.input:
		if open && piece.err == nil {
			return feed(piece.data, false)
		}
		// The input ended or failed just as the escape ran out. What it said is
		// lost to this read, and the next one finds the input closed and ends there.
	default:
	}
	return feed(nil, true)
}

// forgetEnding takes back a first Ctrl-C whose window has passed.
func (t *terminal) forgetEnding() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ending.IsZero() || t.now().Sub(t.ending) < t.endAfter {
		return
	}
	t.ending = time.Time{}
	t.redraw()
}

// interrupted is Ctrl-C at a prompt or a list. Text being composed is cleared,
// and kept for Ctrl-Y to put back, so a message cleared by mistake is not lost;
// on an empty prompt the first press says what a second does and the second,
// inside endAfter, ends the conversation.
func (t *terminal) interrupted() error {
	if len(t.line) > 0 {
		t.kill(0, len(t.line), false)
		t.killing = false
		t.forget()
		t.ending = time.Time{}
		return nil
	}
	if !t.ending.IsZero() && t.now().Sub(t.ending) < t.endAfter {
		t.ending = time.Time{}
		return errEnded
	}
	t.ending = t.now()
	return nil
}

// beginPrompt shows the prompt and applies anything the operator typed ahead
// while the last turn was being answered, so type-ahead is honoured in the
// order it was typed rather than discarded.
func (t *terminal) beginPrompt(prompt string) (string, bool, error) {
	t.mu.Lock()
	t.prompting = true
	t.promptText = prompt
	t.mu.Unlock()
	return t.feed(nil, false)
}

// endPrompt takes the composing region down because there will be no line: the
// input ended, or the conversation was cancelled.
func (t *terminal) endPrompt(cause error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	// The prompt and anything composed under it are written into the scrollback
	// rather than erased with the region: the operator typed it, and the screen
	// is the only place it exists. A long paste is written in full for the same
	// reason.
	composed := t.promptText + t.sent(t.line)
	t.prompting = false
	t.entering = false
	t.line = nil
	t.cursor = 0
	t.ending = time.Time{}
	t.forget()
	t.releasePastes()
	t.render([]string{composed})
	return cause
}

// feed applies input to the message being composed and returns it once the
// operator sends one. Anything typed after that stays buffered: two messages
// that arrive in one read are two messages, in order.
//
// late says an escape byte at the front of what is waiting has waited its time
// and is the escape key (lateEscape).
func (t *terminal) feed(data []byte, late bool) (string, bool, error) {
	line, submitted, raised, err := t.consume(data, late)
	t.raiseAll(raised)
	return line, submitted, err
}

// raiseAll raises the signals a negotiated keyboard stopped the terminal
// raising, with the screen let go of: one of them stops this process, and doing
// that while holding the console would stop it mid-draw. It is the same for a
// line being composed and a list being chosen from, because the keys the
// terminal reports instead of acting on are the same whatever is on screen.
func (t *terminal) raiseAll(raised []signalKey) {
	for _, pressed := range raised {
		if pressed == signalSuspend {
			// Stopping is the one that has to hand the terminal over first and take
			// it back afterwards, so it is not simply raised.
			t.suspend()
			continue
		}
		t.raise(pressed)
	}
}

// suspend is Ctrl-Z where the terminal reports the key instead of stopping the
// process itself. The terminal is handed back exactly as closing hands it
// back — the region taken down, the negotiated keyboard popped, the operator's
// own modes restored — so the shell that takes the foreground finds none of this
// conversation's settings on it. Coming back is the same in reverse, and the
// keyboard is negotiated again rather than assumed: what was agreed before a
// stop is not still agreed after one, and shift-return that silently stopped
// working for the rest of a session is exactly what this whole negotiation
// exists to prevent.
func (t *terminal) suspend() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	io.WriteString(t.out, t.eraseRegion()+pasteOff+t.restoreKeyboard)
	t.restoreKeyboard = ""
	if t.restore != nil {
		t.restore()
	}
	// The process stops inside this and comes back when the operator resumes it.
	t.raise(signalSuspend)
	if t.modes != nil {
		if restore, err := t.modes(); err == nil {
			t.restore = restore
		}
		// A terminal that will not be put back into cbreak leaves the conversation
		// readable a line at a time rather than a keystroke at a time. It is worse
		// than it was and better than a conversation that ends because the operator
		// stopped it, so it carries on and the region is drawn on what there is.
	}
	// The operator's own settings went back on the terminal for the stop, Ctrl-C
	// among them, and whatever is reading the keyboard still wants it as a key.
	if t.reading > 0 && t.interruptKey != nil {
		t.interruptKey(true)
	}
	t.negotiateKeyboard(t.keyboardWait)
	// Bracketing is asked for again for the reason the keyboard is: the shell
	// that had the terminal in between may have turned it off.
	io.WriteString(t.out, pasteOn)
	t.redraw()
}

// consume is feed under the lock: everything that changes what is on screen,
// and the signal keys it met on the way, which are raised once it is let go of.
func (t *terminal) consume(data []byte, late bool) (string, bool, []signalKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var raised []signalKey
	t.keys = append(t.keys, data...)
	if len(t.keys) > MaxLineBytes {
		t.keys = nil
		t.line = nil
		t.cursor = 0
		t.forget()
		t.releasePastes()
		t.redraw()
		return "", false, raised, fmt.Errorf("the operator sent more than %d bytes without ending the message", MaxLineBytes)
	}
	for len(t.keys) > 0 {
		pressed, size, complete := t.nextKey(late)
		if !complete {
			break
		}
		t.keys = t.keys[size:]
		if pressed.code == keySignal {
			raised = append(raised, pressed.signal)
			continue
		}
		// The list of commands takes the keys it moves and completes with while it
		// is showing, Esc among them: putting the list away is what Esc is pressed
		// for there, even beside a turn, and a second press stops the turn.
		if t.steerMenu(pressed) {
			t.ending = time.Time{}
			t.killing = false
			continue
		}
		if t.stopsTurn(pressed) {
			t.ending = time.Time{}
			if err := t.redraw(); err != nil {
				return "", false, raised, err
			}
			return "", false, raised, ErrStopTurn
		}
		if pressed.code == keyInterrupt {
			if err := t.interrupted(); err != nil {
				return "", false, raised, err
			}
			continue
		}
		t.ending = time.Time{}
		if pressed.code != keyEnter {
			t.openMenu(pressed)
			t.apply(pressed)
			t.pointMenu()
			continue
		}
		t.killing = false
		t.menuOpen = false
		// The line is read with any long paste still a marker, so a backslash at
		// the end of one is not taken for the operator carrying on.
		line := string(t.line)
		// A message whose last character is a backslash is one the operator is
		// carrying on: the newline that asks the terminal for nothing at all, and
		// so the one that works where shift-return cannot be reported and
		// alt-return is taken by something else. It is composed in the region like
		// any other newline rather than sent and joined up afterwards.
		if carried, ok := carriedOn(line); ok {
			t.line = []rune(carried)
			t.cursor = len(t.line)
			continue
		}
		// What is sent and echoed carries every long paste in full: the label is
		// how the line is drawn while it is composed, not what was said.
		line = t.sent(t.line)
		t.line = nil
		t.cursor = 0
		// A recalled message that is sent is sent as it now reads, and the next
		// prompt starts from a message of its own rather than from where up had
		// got to in the earlier ones.
		t.forget()
		t.releasePastes()
		t.prompting = false
		// Whatever the message was answering is answered. A choice the operator
		// left to say it in their own words has been said.
		t.entering = false
		// The finished message joins the transcript above, so who said what is
		// still legible after it scrolls: the terminal echoed nothing, because
		// the region is drawn rather than typed into.
		echo := []string{t.promptText + line}
		if t.besideTurn {
			echo = nil
		}
		if err := t.render(echo); err != nil {
			return "", false, raised, err
		}
		return line, true, raised, nil
	}
	if err := t.redraw(); err != nil {
		return "", false, raised, err
	}
	return "", false, raised, nil
}

// nextKey decodes the keystroke at the front of what is waiting, taking an escape
// byte that has waited its time for the escape key itself.
func (t *terminal) nextKey(late bool) (key, int, bool) {
	pressed, size, complete := decodeKey(t.keys)
	if !complete && late && lateEscape(t.keys) {
		return key{code: keyEscape}, 1, true
	}
	return pressed, size, complete
}

// apply is one keystroke's effect on the message being composed.
func (t *terminal) apply(pressed key) {
	killing := t.killing
	t.killing = false
	switch pressed.code {
	case keyRune:
		if !pasteMarker(pressed.value) {
			t.insert(pressed.value)
		}
	case keyNewline:
		t.insert('\n')
	case keyPaste:
		// A paste lands where the cursor is, whole, its newlines composed in the
		// region exactly as typed ones are. Nothing in it sends: return after it
		// is what sends, as it always was. A long one goes in as one marker,
		// drawn as a label naming it (paste.go).
		if marker, held := t.holdPaste(pressed.text); held {
			t.insert(marker)
			break
		}
		for _, value := range pressed.text {
			t.insert(value)
		}
	case keyBackspace:
		if t.cursor > 0 {
			t.line = append(t.line[:t.cursor-1], t.line[t.cursor:]...)
			t.cursor--
		}
	case keyDelete:
		if t.cursor < len(t.line) {
			t.line = append(t.line[:t.cursor], t.line[t.cursor+1:]...)
		}
	case keyLeft:
		if t.cursor > 0 {
			t.cursor--
		}
	case keyRight:
		if t.cursor < len(t.line) {
			t.cursor++
		}
	case keyUp:
		if !t.takeBackWaiting() {
			t.up()
		}
	case keyDown:
		t.down()
	case keyHome:
		t.cursor = 0
	case keyEnd:
		t.cursor = len(t.line)
	case keyWordLeft:
		t.cursor = t.wordStart()
	case keyWordRight:
		end := t.cursor
		for end < len(t.line) && separates(t.line[end]) {
			end++
		}
		for end < len(t.line) && !separates(t.line[end]) {
			end++
		}
		t.cursor = end
	case keyKillLine:
		t.kill(0, t.cursor, killing)
	case keyKillWord:
		t.kill(t.wordStart(), t.cursor, killing)
	case keyKillToEnd:
		// To the end of the line the cursor is on rather than of the whole
		// message, and at the end of a line it takes the newline, joining the
		// next line to this one.
		end := t.cursor
		for end < len(t.line) && t.line[end] != '\n' {
			end++
		}
		if end == t.cursor && end < len(t.line) {
			end++
		}
		t.kill(t.cursor, end, killing)
	case keyYank:
		for _, value := range t.killed {
			t.insert(value)
		}
	}
}

// wordStart is where the word before the cursor begins, past any separators
// between it and the cursor.
func (t *terminal) wordStart() int {
	start := t.cursor
	for start > 0 && separates(t.line[start-1]) {
		start--
	}
	for start > 0 && !separates(t.line[start-1]) {
		start--
	}
	return start
}

// kill deletes the runes between from and to and keeps them for Ctrl-Y to put
// back. Kills pressed one straight after another are kept together, in the
// order they stood in the message, so three Ctrl-Ws put back three words
// rather than the last of them.
func (t *terminal) kill(from, to int, following bool) {
	t.killing = true
	if from == to {
		return
	}
	removed := append([]rune(nil), t.line[from:to]...)
	switch {
	case !following:
		t.killed = removed
	case from < t.cursor:
		t.killed = append(removed, t.killed...)
	default:
		t.killed = append(t.killed, removed...)
	}
	t.line = append(t.line[:from], t.line[to:]...)
	t.cursor = from
}

// insert puts one rune where the cursor is. A newline is a rune like any other
// here: what it changes is how the region is drawn, not how it is edited.
func (t *terminal) insert(value rune) {
	t.line = append(t.line, 0)
	copy(t.line[t.cursor+1:], t.line[t.cursor:])
	t.line[t.cursor] = value
	t.cursor++
}

// separates reports what a word ends at. A newline ends one as a space does, so
// deleting the last word of a line stops at the line rather than running back
// into the one above it.
func separates(character rune) bool { return character == ' ' || character == '\n' }

// The marker that says which answer would be taken, and the line that says how
// to move it. Both are drawn rather than coloured: the marker is what carries
// the selection, so a terminal that may not be dressed shows it exactly as one
// that may.
const (
	chosenMarker   = "❯ "
	unchosenMarker = "  "
	choiceKeys     = "  ↑ ↓ to move, enter to choose"
)

// Choose puts a list of answers in the region and moves a marker through it.
// The list is redrawn under whatever the harness writes, exactly as a composing
// line is, so a run that finishes while the operator is deciding is reported
// above the question rather than into it.
func (t *terminal) Choose(ctx context.Context, prompt string, options []string, interrupt <-chan struct{}) (string, error) {
	// An operator part way through their own words is typing a line, not
	// choosing: what was interrupted was the prompt, so it is put again and the
	// list is not.
	if t.entered() {
		return t.Prompt(ctx, prompt, interrupt)
	}
	t.claimInterrupt()
	defer t.releaseInterrupt()
	index, chosen, err := t.beginChoice(prompt, offered(options))
	for {
		switch {
		case errors.Is(err, errEnded):
			return "", t.endChoice(io.EOF)
		case err != nil:
			return "", err
		case chosen:
			if index < len(options) {
				return options[index], nil
			}
			// They chose to say it themselves, so the answer is a line like any
			// other and is read as one.
			return t.Prompt(ctx, prompt, interrupt)
		}
		escape, expiry := t.timers()
		select {
		case piece, open := <-t.input:
			if !open {
				return "", t.endChoice(io.EOF)
			}
			if piece.err != nil {
				return "", t.endChoice(fmt.Errorf("read what the operator chose: %w", piece.err))
			}
			index, chosen, err = t.feedChoice(piece.data, false)
		case <-escape:
			_, _, err = t.feedLate(func(data []byte, late bool) (string, bool, error) {
				var fed error
				index, chosen, fed = t.feedChoice(data, late)
				return "", chosen, fed
			})
		case <-expiry:
			t.forgetEnding()
		case <-interrupt:
			// Something else needs the screen. The marker stays where they left
			// it, and the next Choose puts the same list back under it.
			return "", ErrInterrupted
		case <-ctx.Done():
			return "", t.endChoice(ctx.Err())
		}
	}
}

// entered reports that the operator has already left the list to say it in
// their own words.
func (t *terminal) entered() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.entering
}

// beginChoice puts the list on screen and applies anything typed ahead. A
// question the operator was already part way through keeps the answer they had
// moved to; a different question starts at the top. What is still on screen is
// only ever an interrupted question, because a choice that was made takes its
// list with it, so a question that reads the same as one already answered is
// asked from the top too.
func (t *terminal) beginChoice(prompt string, choices []string) (int, bool, error) {
	t.mu.Lock()
	if !sameQuestion(t.promptText, t.choices, prompt, choices) {
		t.chosen = 0
	}
	t.choices = choices
	t.choosing = true
	// A list takes the place of a prompt that was interrupted rather than
	// answered, which is a prompt left open while a turn was answered: what was
	// typed under it stays in the line for the next prompt, and only the list is
	// drawn meanwhile.
	t.prompting = false
	t.promptText = prompt
	t.mu.Unlock()
	return t.feedChoice(nil, false)
}

// endChoice takes the list down because there will be no answer: the input
// ended, or the conversation was cancelled. What was asked is written into the
// scrollback rather than erased with the region, so a question nobody answered
// is still on the screen that was left behind.
func (t *terminal) endChoice(cause error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	asked := strings.TrimRight(t.promptText, " ")
	t.choosing = false
	t.choices = nil
	t.chosen = 0
	t.ending = time.Time{}
	t.render([]string{asked})
	return cause
}

// feedChoice applies input to the list and returns an answer once the operator
// presses enter. Anything typed after that stays buffered, exactly as it does
// for a line: what follows an answer belongs to whatever is asked next. The
// signal keys it met on the way are raised once the console is let go of, as
// feed raises them for a line: a list on screen is no reason Ctrl-C should stop
// interrupting.
func (t *terminal) feedChoice(data []byte, late bool) (int, bool, error) {
	index, chosen, raised, err := t.consumeChoice(data, late)
	t.raiseAll(raised)
	return index, chosen, err
}

// consumeChoice is feedChoice under the lock: everything that changes what is
// on screen, and the signal keys it met on the way.
func (t *terminal) consumeChoice(data []byte, late bool) (int, bool, []signalKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var raised []signalKey
	t.keys = append(t.keys, data...)
	for len(t.keys) > 0 {
		pressed, size, complete := t.nextKey(late)
		if !complete {
			break
		}
		t.keys = t.keys[size:]
		if pressed.code == keySignal {
			raised = append(raised, pressed.signal)
			continue
		}
		// A list has no text of its own to clear, so Ctrl-C there is what it is on
		// an empty prompt.
		if pressed.code == keyInterrupt {
			if err := t.interrupted(); err != nil {
				return 0, false, raised, err
			}
			continue
		}
		t.ending = time.Time{}
		if pressed.code != keyEnter {
			t.move(pressed)
			continue
		}
		chosen := t.chosen
		answer := t.choices[chosen]
		t.choosing = false
		// The last answer is their own words, and the prompt that reads them is
		// the same prompt any other line is read under.
		t.entering = chosen == len(t.choices)-1
		// The list is done with: nothing about a question that has been answered
		// should decide where the marker starts on the next one.
		t.choices = nil
		t.chosen = 0
		// What was asked and what was picked join the transcript above, so the
		// answer is still legible once the list it came from is gone.
		if err := t.render([]string{t.promptText + answer}); err != nil {
			return 0, false, raised, err
		}
		return chosen, true, raised, nil
	}
	if err := t.redraw(); err != nil {
		return 0, false, raised, err
	}
	return 0, false, raised, nil
}

// move is one keystroke's effect on which answer the marker is against.
// Anything the list has no use for is ignored rather than applied, for the
// reason a composing line ignores what it has no use for: a stray escape that
// moved the marker would answer a question the operator was still reading.
func (t *terminal) move(pressed key) {
	switch pressed.code {
	case keyUp:
		if t.chosen > 0 {
			t.chosen--
		}
	case keyDown:
		if t.chosen < len(t.choices)-1 {
			t.chosen++
		}
	case keyHome:
		t.chosen = 0
	case keyEnd:
		t.chosen = len(t.choices) - 1
	case keyRune:
		// The numbers are on screen beside the answers, so typing one moves to
		// it. It moves rather than answers: enter is what commits, wherever the
		// operator got to, so a mistyped digit is corrected rather than sent.
		if index := int(pressed.value - '1'); pressed.value >= '1' && index < len(t.choices) {
			t.chosen = index
		}
	}
}

// choiceRegion is the rows the list occupies: what is being answered, the
// answers with the marker against one of them, and how to move it. It is empty
// when nothing is being chosen, which is every other prompt there is.
func (t *terminal) choiceRegion() []string {
	if !t.choosing {
		return nil
	}
	lines := make([]string, 0, len(t.choices)+2)
	lines = append(lines, strings.TrimRight(t.promptText, " "))
	for index, choice := range t.choices {
		marker := unchosenMarker
		if index == t.chosen {
			marker = chosenMarker
		}
		// The number is written beside every answer whether or not it can be
		// typed, because it is what the same question looks like on a stream:
		// one list, read the same way, wherever the conversation is held.
		lines = append(lines, fmt.Sprintf("%s%d. %s", marker, index+1, choice))
	}
	return append(lines, choiceKeys)
}

func (t *terminal) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.prompting = false
	t.choosing = false
	t.status = ""
	t.resting = ""
	var out strings.Builder
	out.WriteString(t.eraseRegion())
	// Whatever was negotiated about the keyboard is handed back before the modes
	// are, so a shell that gets the terminal back is not left with a protocol
	// this conversation turned on for itself. The bracketing asked for around a
	// paste goes the same way, for the same reason.
	out.WriteString(pasteOff)
	out.WriteString(t.restoreKeyboard)
	t.restoreKeyboard = ""
	// A part-line held back is written rather than dropped. It is something the
	// harness said, and the screen is the only place it exists.
	if len(t.pending) > 0 {
		out.Write(t.pending)
		out.WriteString("\n")
		t.pending = nil
	}
	// A message still waiting when the conversation ends was never sent, and is
	// written into the scrollback as exactly that rather than erased with the
	// region: the operator typed it, and the screen is the only place it exists.
	for _, message := range t.waiting {
		out.WriteString("not sent: " + message + "\n")
	}
	t.waiting = nil
	_, err := io.WriteString(t.out, out.String())
	t.mu.Unlock()
	if t.restore != nil {
		if restoreErr := t.restore(); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore the terminal: %w", restoreErr))
		}
	}
	return err
}

func indexNewline(text []byte) int {
	for index, character := range text {
		if character == '\n' {
			return index
		}
	}
	return -1
}

// visibleWidth counts the columns text occupies on the row it is written on.
// Every rune is counted as one column and an escape sequence as none, which is
// the same measure `place` takes, so the region has one idea of what a column
// is rather than two that agree until a themed line arrives. A double-width
// rune in a typed line will be one column out until somebody needs it to be
// otherwise.
func visibleWidth(text string) int {
	runes := []rune(text)
	columns := 0
	for position := 0; position < len(runes); {
		if runes[position] == 0x1b {
			position += escapeRunes(runes[position:])
			continue
		}
		position++
		columns++
	}
	return columns
}
