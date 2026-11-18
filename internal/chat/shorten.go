package chat

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/console"
)

// What the harness prints after an answer — tracker actions, reads, reports —
// can run to pages, and on a terminal it would push the answer it is about off
// the screen. So on a terminal a long block is shown as its first lines and a
// line saying how many more there are and which key shows them, as Claude Code
// shows a tool's output. The answer itself is never shortened, and a console
// that is not a terminal is shown everything, as it always was.
const (
	// shortenedLines is how many lines of a shortened block are shown.
	shortenedLines = 5
	// shortenAbove is the most lines a block may have and still be shown in
	// full. It is a little above shortenedLines so no block is shortened to
	// hide only a line or two.
	shortenAbove = 8
)

// harnessBlock writes one block of the harness's own output. On a console that
// shortens output, a block longer than shortenAbove lines is shown shortened
// and kept whole, so the expand key can show it (showShortened).
func (s *Session) harnessBlock(screen console.Console, write func(io.Writer)) {
	if !console.Shortens(screen) {
		write(screen)
		return
	}
	var block bytes.Buffer
	write(&block)
	shown, cut := shortenBlock(block.String())
	if cut {
		s.shortened = append(s.shortened, block.String())
	}
	io.WriteString(screen, shown)
}

// shortenBlock returns a block as its first shortenedLines lines and a line
// saying how many more there are, and whether it shortened anything. The blank
// lines that end a block are kept after the shortened block, because they are
// what separates it from the next one.
func shortenBlock(block string) (string, bool) {
	body := strings.TrimRight(block, "\n")
	lines := strings.Split(body, "\n")
	if body == "" || len(lines) <= shortenAbove {
		return block, false
	}
	ending := strings.TrimPrefix(block[len(body):], "\n")
	hidden := len(lines) - shortenedLines
	return strings.Join(lines[:shortenedLines], "\n") + "\n" +
		fmt.Sprintf("… %d more lines; press %s to show them in full\n", hidden, console.ExpandKey) +
		ending, true
}

// showShortened writes in full every block shortened since the last answer,
// which is what the operator asked for by pressing the expand key.
func (s *Session) showShortened(out io.Writer) {
	if len(s.shortened) == 0 {
		fmt.Fprintf(out, "Nothing printed since the last answer was shortened, so %s has nothing more to show.\n\n", console.ExpandKey)
		return
	}
	fmt.Fprintf(out, "In full, what was shortened after the last answer:\n\n")
	for _, block := range s.shortened {
		io.WriteString(out, block)
		if !strings.HasSuffix(block, "\n\n") {
			fmt.Fprintln(out)
		}
	}
}
