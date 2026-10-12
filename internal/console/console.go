// Package console is where an interactive conversation meets the operator.
//
// On a terminal the line being composed gets a region of its own at the bottom
// of the screen: everything the harness writes is written above it, so a reply,
// a proposal, or a run that finishes never lands in the middle of a half-typed
// sentence. Nothing is drawn into the alternate screen and nothing already
// written is redrawn, so the terminal's own scrollback, selection, copying, and
// resizing keep working on the conversation exactly as they would on any other
// command's output. The same region carries the account of work in progress
// while the operator is waiting for an answer, and the answers to a question
// that enumerates them while they are picking one, and the theme dresses what is
// written above it so one kind of thing can be told from another.
//
// Anything that is not a terminal gets the same conversation as an ordinary
// stream of text: no cursor control, no colour, no rules, and the same lines in
// the same order a redirected conversation has always had. None of this reaches
// the recorded reply, the event stream, or --json: what the product manager
// said is what was said, and this is only how it is shown.
//
// The Theme is where every surface inherits one discipline rather than arguing
// it again: the words carry the meaning and the dressing only makes it findable,
// emphasis is spent rather than spread, the state and severity vocabularies are
// named here so one of them looks the same everywhere it is shown, and NO_COLOR,
// a terminal that says it is dumb, and a stream that is not one suppress every
// escape there is together. It is recorded in docs/developing-yoyo.md, under
// "What a surface may do with emphasis", with the legibility goal it serves. A
// surface that finds itself writing an escape of its own has found something
// this package should be taught.
package console

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

// MaxLineBytes bounds one line the operator types. It is the same order as the
// largest message a conversation accepts, so an over-long line is refused
// rather than silently cut in half.
const MaxLineBytes = 64 << 10

// continuationMark ends a line the operator is carrying on to the next. It is
// the newline that asks the terminal for nothing: where shift-return cannot be
// reported and alt-return is taken by the window manager, and on a stream that
// is not a terminal at all, this is still a newline in the message.
const continuationMark = `\`

// carriedOn reports whether a line the operator finished carries on to the next,
// and what it holds without the mark. A message that is to end in a backslash
// therefore cannot be typed, which is the price of a rule with nothing to
// remember: the two keys above put one anywhere in a message.
func carriedOn(line string) (string, bool) {
	if !strings.HasSuffix(line, continuationMark) {
		return line, false
	}
	return strings.TrimSuffix(line, continuationMark) + "\n", true
}

// ErrInterrupted reports that Prompt returned because something else needed to
// be shown, not because the operator finished a line. Whatever they had typed
// is kept, and the next Prompt resumes it, so an interruption costs them
// nothing.
var ErrInterrupted = errors.New("prompt interrupted")

// Console is the operator's end of a conversation: everything the harness
// writes, and the line they are typing, which output never writes into.
type Console interface {
	// Write writes ordinary prose above whatever is being composed. Text is
	// written a whole line at a time: a write that does not end in a newline is
	// held until one arrives, so nothing is ever left half drawn above the
	// composing line.
	io.Writer
	// Prompt reads one line the operator types. It returns ErrInterrupted,
	// without losing what has been typed, when interrupt fires, so the caller
	// can write above the composing line and prompt again. A closed input
	// returns io.EOF, and so does Ctrl-C pressed twice on an empty prompt where
	// the console reads keystrokes: both are the operator ending the
	// conversation.
	Prompt(ctx context.Context, prompt string, interrupt <-chan struct{}) (string, error)
	// Choose puts a question whose answers can be counted to the operator and
	// returns the one they gave: one of the options, exactly as it was handed
	// over, or whatever they said instead. Their own words are always on offer
	// as the last choice, so a list nobody's answer is on never traps them.
	// What differs between the consoles is only how the choice is made — a
	// marker moved with the cursor keys where the screen can be addressed, and
	// a numbered list to type the number of where it cannot. It reports
	// ErrInterrupted and io.EOF exactly as Prompt does.
	Choose(ctx context.Context, prompt string, options []string, interrupt <-chan struct{}) (string, error)
	// Working starts an account of something the operator is waiting for,
	// beginning with the phase it is in now. It is closed by the caller once
	// there is an answer to read.
	Working(phase string) Activity
	// WatchForStop reads the keyboard while the operator waits on a turn rather
	// than typing, and closes the channel it returns the first time they press a
	// key that stops the turn: Esc, or Ctrl-C. Anything else they type is kept for
	// the next prompt. done ends the watching and is safe to call more than once.
	// A console with no keystrokes to read returns a channel that never closes.
	WatchForStop() (stop <-chan struct{}, done func())
	// Status leaves one line resting under the conversation, above whatever is
	// being composed, until it is replaced or emptied. It carries what is true
	// between turns — what the last turn cost — rather than what happened, which
	// is written into the conversation. An account of work in progress covers it
	// while there is one and it comes back underneath when that ends. A stream
	// has no line to rest anything on and ignores it.
	Status(text string)
	// Theme reports how much this console may dress what is written to it.
	Theme() Theme
	// Composing says, in one line, how a message of more than one line is typed
	// here. What it says is what this console turned out to support rather than
	// what a terminal usually does: shift-return is named only where the terminal
	// has agreed to report it, and the fallback that asks the terminal for
	// nothing is named whatever the answer was.
	Composing() string
	// Close restores the terminal and flushes anything held back. It is safe to
	// call more than once.
	Close() error
}

// Options describes the streams a conversation is held over and the environment
// that decides how much of a terminal they are.
type Options struct {
	In  io.Reader
	Out io.Writer
	// Env reads one environment variable. A nil Env reads the process's own.
	Env func(string) string
}

// Open returns the console these streams can support. A terminal on both ends
// gets the composing region; everything else gets the plain stream, because
// cursor control written into a file is corruption rather than presentation.
func Open(options Options) Console {
	env := options.Env
	if env == nil {
		env = os.Getenv
	}
	if !addressable(options.In, options.Out, env) {
		return newPlain(options.In, options.Out)
	}
	terminal, err := openTerminal(options.In.(*os.File), options.Out.(*os.File), env)
	if err != nil {
		// The streams look like a terminal but will not behave as one. Degrading
		// to the plain stream keeps the conversation working; pretending the
		// cursor control took effect would leave the screen lying about where the
		// operator's own text is.
		return newPlain(options.In, options.Out)
	}
	return terminal
}

// ThemeFor returns the theme a command that is not a conversation may dress its
// output with. A conversation asks the console it opened, which knows what its
// streams turned out to be; a command has only the writer it was handed, so this
// asks that writer the same two questions — a terminal on the other end, and an
// environment that permits dressing at all. Anything else, a redirected stream
// or a buffer in a test among them, is the undressed theme, which is the same
// answer `Open` gives for the same streams.
func ThemeFor(out io.Writer, env func(string) string) Theme {
	if env == nil {
		env = os.Getenv
	}
	file, ok := out.(*os.File)
	if !ok || !isTerminalStream(out) {
		return Theme{}
	}
	return NewTheme(env, func() int { return terminalWidth(file.Fd()) })
}

// addressable reports whether these streams are a terminal this may draw a
// composing region on.
func addressable(in io.Reader, out io.Writer, env func(string) string) bool {
	return permitted(env("TERM"), isTerminalStream(out), isTerminalStream(in))
}

// permitted decides whether the composing region may be drawn. A terminal that
// says it is dumb, or will not say what it is, is taken at its word, and both
// ends have to be a terminal: the region is only meaningful where output can be
// positioned and input can be read a keystroke at a time.
func permitted(term string, outTTY, inTTY bool) bool {
	return term != "" && term != "dumb" && outTTY && inTTY
}

// isTerminalStream reports whether a stream is a terminal this process can
// address. Anything that is not one of the process's own files is not, which is
// what makes a conversation held over buffers in a test the plain stream it
// ought to be.
func isTerminalStream(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return isTerminal(file.Fd())
}
