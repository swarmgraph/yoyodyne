package domain

// The note written beside RaisedByOperatorLabel, in one fixed form, so the
// review of unmet goals and every view of raised items read the same record
// (docs/designs/unmet-goal-review.md, section 6). The label says the item is
// raised; the latest note in this form says by which of his words, when, and
// what it was put ahead of.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Heading lines of the two notes. Each opens its note, and the parser finds a
// note by them.
const (
	operatorRaisedHeading = "Raised by the operator on purpose."
	operatorEndedHeading  = "The operator ended his raising of this item."
)

// MaxRaisedAboveListed bounds how many items a note names as being behind the
// raised item. The rest are counted, so the note stays readable on a long
// backlog and still says how much the raising passed.
const MaxRaisedAboveListed = 20

// OperatorRaising is one note: the operator raising an item, or ending that.
type OperatorRaising struct {
	// Ended is true for the note that ends a raising.
	Ended bool
	// Words are his own, quoted from the message named below.
	Words string
	// Conversation and AfterTurn name the message: the one he sent in that
	// conversation after its turn AfterTurn, the way the harness's other notes
	// name a turn.
	Conversation string
	AfterTurn    int
	// At is when he sent it.
	At time.Time
	// Above names the open and blocked items the raised item stood ahead of in the order when
	// the note was written, and AboveMore counts those left unnamed.
	Above     []string
	AboveMore int
}

// Render writes the note in its fixed form, with the time in zone and in UTC.
func (r OperatorRaising) Render(zone *time.Location) string {
	if zone == nil {
		zone = time.Local
	}
	heading, aboveLabel := operatorRaisedHeading, "Raised above: "
	if r.Ended {
		heading, aboveLabel = operatorEndedHeading, "It was above: "
	}
	var note strings.Builder
	note.WriteString(heading + "\n")
	note.WriteString("His words: \"" + oneLineWords(r.Words) + "\"\n")
	fmt.Fprintf(&note, "From: conversation %s, his message after turn %d\n", r.Conversation, r.AfterTurn)
	fmt.Fprintf(&note, "When: %s (%s)\n", r.At.In(zone).Format("15:04 MST on January 2, 2006"), r.At.UTC().Format(time.RFC3339))
	note.WriteString(aboveLabel)
	switch {
	case len(r.Above) == 0 && r.AboveMore == 0:
		note.WriteString("nothing")
	default:
		note.WriteString(strings.Join(r.Above, ", "))
		if r.AboveMore > 0 {
			fmt.Fprintf(&note, " and %d more", r.AboveMore)
		}
	}
	return note.String()
}

// oneLineWords folds his words onto one line, which is what lets the note be
// read back line by line.
func oneLineWords(words string) string {
	return strings.Join(strings.Fields(words), " ")
}

// LatestOperatorRaising reads the last note in the fixed form out of an item's
// notes. A note that does not read back whole is skipped rather than half-read.
func LatestOperatorRaising(notes string) (OperatorRaising, bool) {
	lines := strings.Split(notes, "\n")
	for start := len(lines) - 1; start >= 0; start-- {
		heading := strings.TrimSpace(lines[start])
		if heading != operatorRaisedHeading && heading != operatorEndedHeading {
			continue
		}
		if raising, ok := parseOperatorRaising(heading == operatorEndedHeading, lines[start+1:]); ok {
			return raising, true
		}
	}
	return OperatorRaising{}, false
}

func parseOperatorRaising(ended bool, lines []string) (OperatorRaising, bool) {
	if len(lines) < 4 {
		return OperatorRaising{}, false
	}
	raising := OperatorRaising{Ended: ended}
	words, ok := strings.CutPrefix(strings.TrimSpace(lines[0]), "His words: \"")
	if !ok || !strings.HasSuffix(words, "\"") {
		return OperatorRaising{}, false
	}
	raising.Words = strings.TrimSuffix(words, "\"")
	from, ok := strings.CutPrefix(strings.TrimSpace(lines[1]), "From: conversation ")
	if !ok {
		return OperatorRaising{}, false
	}
	conversation, turn, ok := strings.Cut(from, ", his message after turn ")
	if !ok {
		return OperatorRaising{}, false
	}
	afterTurn, err := strconv.Atoi(turn)
	if err != nil {
		return OperatorRaising{}, false
	}
	raising.Conversation, raising.AfterTurn = conversation, afterTurn
	when := strings.TrimSpace(lines[2])
	open, close := strings.LastIndex(when, "("), strings.LastIndex(when, ")")
	if !strings.HasPrefix(when, "When: ") || open < 0 || close < open {
		return OperatorRaising{}, false
	}
	if raising.At, err = time.Parse(time.RFC3339, when[open+1:close]); err != nil {
		return OperatorRaising{}, false
	}
	aboveLabel := "Raised above: "
	if ended {
		aboveLabel = "It was above: "
	}
	above, ok := strings.CutPrefix(strings.TrimSpace(lines[3]), aboveLabel)
	if !ok {
		return OperatorRaising{}, false
	}
	if above != "nothing" {
		if listed, more, counted := strings.Cut(above, " and "); counted {
			above = listed
			number, err := strconv.Atoi(strings.TrimSuffix(more, " more"))
			if err != nil {
				return OperatorRaising{}, false
			}
			raising.AboveMore = number
		}
		for _, id := range strings.Split(above, ", ") {
			if id = strings.TrimSpace(id); id != "" {
				raising.Above = append(raising.Above, id)
			}
		}
	}
	return raising, true
}
