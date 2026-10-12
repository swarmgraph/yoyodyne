package console

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// plain is the conversation as an ordinary stream of text. It is what a pipe, a
// file, a redirected terminal, and a terminal that says it is dumb all get:
// lines in, lines out, nothing written that a reader would have to interpret.
//
// It deliberately does not interrupt a prompt when a background run finishes.
// A stream has no moment where the operator is waiting with the screen to
// themselves, and reporting between two lines that are already buffered would
// make a redirected transcript depend on timing. The run is reported at the
// next line instead, which is where a stream can say it truthfully, and which
// is what a redirected conversation has always done.
type plain struct {
	// mu keeps one write whole against another. A conversation writes from the
	// goroutine that is talking, but a console is reachable from anywhere.
	mu      sync.Mutex
	out     io.Writer
	scanner *bufio.Scanner
}

func newPlain(in io.Reader, out io.Writer) *plain {
	console := &plain{out: out}
	if in != nil {
		console.scanner = bufio.NewScanner(in)
		console.scanner.Buffer(make([]byte, 4096), MaxLineBytes)
	}
	return console
}

func (p *plain) Write(text []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.Write(text)
}

func (p *plain) Prompt(ctx context.Context, prompt string, _ <-chan struct{}) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.mu.Lock()
	_, err := io.WriteString(p.out, prompt)
	p.mu.Unlock()
	if err != nil {
		return "", err
	}
	// A line ending in the continuation mark carries on to the next one, which is
	// how a message of more than one line is written to a stream that has no
	// keystrokes to report. The prompt is written once for the whole of it: a
	// redirected transcript says what was asked, not how many lines the answer
	// took.
	var composed strings.Builder
	for {
		if p.scanner == nil || !p.scanner.Scan() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.scanner != nil {
				if err := p.scanner.Err(); err != nil {
					return "", err
				}
			}
			// The newline closes the prompt line nobody answered, so a transcript
			// does not end mid-line.
			fmt.Fprintln(p.out)
			return "", io.EOF
		}
		carried, continues := carriedOn(p.scanner.Text())
		composed.WriteString(carried)
		if !continues {
			break
		}
		if composed.Len() > MaxLineBytes {
			return "", fmt.Errorf("the operator sent more than %d bytes without ending the message", MaxLineBytes)
		}
	}
	// Nothing is echoed here. Where the input is a terminal it echoed the line
	// itself, and where it is not there was never an echo to reproduce; writing
	// one would either double the line or change what a redirected transcript
	// has always contained.
	return composed.String(), nil
}

// choiceHint is how a stream says the list above it can be answered by number.
// A stream cannot move a marker, so the numbers are the whole affordance, and
// one that is never named is one the operator has to guess at.
const choiceHint = "  (type the number of a choice, or answer in your own words)"

// Choose puts the answers to the operator as a numbered list and reads the
// number they type. This is the same question a terminal offers a marker for,
// asked the only way a stream can ask it: every answer that was on offer, and a
// way to name one of them.
//
// Free entry costs nothing extra here. A line that is not one of the numbers is
// already the operator's own answer, so it is returned as one, and the numbered
// choice that says as much is there for the operator who reads the list before
// typing rather than after.
func (p *plain) Choose(ctx context.Context, prompt string, options []string, interrupt <-chan struct{}) (string, error) {
	choices := offered(options)
	var list strings.Builder
	for index, choice := range choices {
		fmt.Fprintf(&list, "  %d. %s\n", index+1, choice)
	}
	fmt.Fprintln(&list, choiceHint)
	p.mu.Lock()
	_, err := io.WriteString(p.out, list.String())
	p.mu.Unlock()
	if err != nil {
		return "", err
	}
	line, err := p.Prompt(ctx, prompt, interrupt)
	if err != nil {
		return "", err
	}
	index, numbered := chosenNumber(line, len(choices))
	switch {
	case numbered && index < len(options):
		return options[index], nil
	case numbered:
		// They named their own words, which are still to come: the prompt is put
		// again and what they say to it is the answer.
		return p.Prompt(ctx, prompt, interrupt)
	default:
		return line, nil
	}
}

// Working says each phase once, as a line like any other. A stream has nothing
// to animate and nothing to erase, so what an operator watching a redirected
// conversation gets is the phases themselves: fewer than a terminal shows, and
// every one of them a fact about what happened rather than about how long it
// took.
func (p *plain) Working(phase string) Activity {
	activity := &narration{out: p}
	activity.Doing(phase)
	return activity
}

// WatchForStop has nothing to watch: a stream hands over whole lines, and a
// key pressed while a turn is being answered is not one of them.
func (p *plain) WatchForStop() (<-chan struct{}, func()) { return nil, func() {} }

// Status has nothing to rest on. A stream is written once and never revisited,
// so a line whose whole purpose is to be replaced would be a line repeated for
// every turn of the conversation, and a redirected transcript would carry a
// running total nobody asked it to.
func (p *plain) Status(string) {}

// Theme dresses nothing. Colour and rules are for a terminal, and a stream that
// is not one gets the same lines a redirected conversation has always had.
func (p *plain) Theme() Theme { return Theme{} }

// Composing names only the continuation mark. A stream reports no keystrokes and
// has none to report: whatever key was pressed, what arrives here is a line.
func (p *plain) Composing() string {
	return `Multi-line: end a line with \ to carry it on to the next. A line that does not ends the message.`
}

// Close has nothing to restore: a stream was never put into a state anybody
// has to be got out of.
func (p *plain) Close() error { return nil }
