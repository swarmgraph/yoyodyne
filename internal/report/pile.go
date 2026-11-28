package report

// How a pile with more in it than one turn can carry is worked through, and how
// it is described to somebody deciding whether it is draining.
//
// Bounding what one turn is shown is not optional: a pile of five hundred
// reports delivered whole is the turn, and nothing else in it would get read.
// What the bound must not do is deliver the same slice every time. Ordering the
// unhandled pile worst-first and taking the first ten is exactly that failure —
// the ten it takes are the same ten until somebody decides about one of them,
// and everything filed behind them is invisible however long it waits. That is
// how this project came to hold five hundred and sixty-four unhandled reports
// with the oldest three weeks old while every turn reported a full listing.
//
// So delivery is a walk rather than a listing. A reader carries a position
// through the pile in the order it was filed, oldest first, and each turn is
// shown what the position has not passed yet. The position advances over what
// was actually shown, so the next turn resumes rather than restarts, and the
// oldest report in the pile is the first thing offered rather than the last —
// which is what makes the oldest unhandled report's age fall instead of climb.
//
// Three things sit on top of the walk. A critical report jumps it, because
// something already costing somebody has to be read today rather than when the
// walk reaches it; the walk starts again from the beginning of the pile once it
// has reached the end and the reader's own record of what it has been shown has
// moved on; and the start of a recurring pass offers again everything the reader
// was shown and left undecided (PendingAgain). An ordinary turn re-offers nothing
// the reader still remembers being shown, which is what stops a settled pile
// being re-read every turn. The record is bounded and forgets only after hundreds
// of reports, so a reader with one long-lived conversation would otherwise never
// see again a report it was shown once and left; the pass is where it is.

import (
	"fmt"
	"sort"
	"time"
)

// Position is how far through the pile, in the order the pile was filed, a
// reader has been carried.
//
// It is a point in that order rather than a count, because the pile grows under
// a reader between turns: an index would name a different report every time
// something was filed, and a moment plus the identifier that breaks its ties
// names the same one forever.
type Position struct {
	RecordedAt time.Time `json:"recorded_at,omitempty"`
	ID         string    `json:"id,omitempty"`
}

// At is the position a reader is left at by being carried past one report.
func At(reported Report) Position {
	return Position{RecordedAt: reported.RecordedAt, ID: reported.ID}
}

// Started reports whether a position names anywhere at all. The zero position is
// the beginning of the pile, which is where a reader that has been shown nothing
// stands.
func (p Position) Started() bool {
	return p.ID != "" || !p.RecordedAt.IsZero()
}

// Passed reports whether a reader at this position has already been carried past
// a report. It is the order ByFiling puts the pile in, so the two can never
// disagree about which report comes first.
func (p Position) Passed(reported Report) bool {
	if !p.Started() {
		return false
	}
	switch {
	case reported.RecordedAt.Before(p.RecordedAt):
		return true
	case reported.RecordedAt.After(p.RecordedAt):
		return false
	default:
		return reported.ID <= p.ID
	}
}

// ByFiling orders reports the way the pile was written, oldest first, with the
// identifier breaking the ties two reports filed in one reply always have. It
// copies rather than sorting in place, for the reason BySeverity does: the
// caller's slice is the pile's own order and nothing that reads it may disturb
// that.
func ByFiling(reports []Report) []Report {
	ordered := make([]Report, len(reports))
	copy(ordered, reports)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].RecordedAt.Equal(ordered[j].RecordedAt) {
			return ordered[i].RecordedAt.Before(ordered[j].RecordedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered
}

// Waiting is what one turn is offered of the pile, in the two parts a caller has
// to keep apart.
//
// Urgent jumps the walk, Again is what the reader was shown before and left
// undecided, offered again at the start of a recurring pass, and Next is the
// walk. The difference matters after the turn rather than during it: the
// position advances over what was shown from Next and never over the other two,
// because a critical filed this morning is at the far end of the pile and a
// report offered again sits wherever it was filed, and advancing to either would
// skip everything the walk had not reached.
type Waiting struct {
	Urgent []Report
	Again  []Report
	Next   []Report
}

// Empty reports a reader with nothing waiting for it, which is the answer on a
// pile somebody has worked through.
func (w Waiting) Empty() bool { return len(w.Urgent) == 0 && len(w.Again) == 0 && len(w.Next) == 0 }

// Pending is what a reader at one position is offered of the unhandled pile,
// given its own record of what it has already been shown.
//
// The walk starts again from the beginning where it has reached the end and
// there is still something the reader's record no longer says it has seen. That
// is what stops a report offered once and left from being buried forever, and it
// costs nothing on a pile that is being worked through: everything still in the
// record is skipped, so a reader whose record covers the whole unhandled pile is
// offered nothing at all.
func Pending(unhandled []Report, position Position, shown map[string]bool) Waiting {
	ordered := ByFiling(unhandled)
	if waiting := pendingFrom(ordered, position, shown); !waiting.Empty() {
		return waiting
	}
	return pendingFrom(ordered, Position{}, shown)
}

// PendingAgain is Pending at the start of a recurring pass: it offers, as well,
// every unhandled report the reader's record says it was already shown. Being
// shown a report is not deciding about it, and a report left undecided has to
// come back somewhere; an ordinary turn is the wrong place, because a pile the
// reader is working through would be re-read every turn, and the next pass is
// the right one, because a pass exists to work the pile. Criticals shown before
// join Urgent, and everything else shown before is Again, oldest first, ahead of
// the walk.
func PendingAgain(unhandled []Report, position Position, shown map[string]bool) Waiting {
	waiting := Pending(unhandled, position, shown)
	var urgent []Report
	for _, reported := range ByFiling(unhandled) {
		if !shown[reported.ID] {
			continue
		}
		if reported.Severity == SeverityCritical {
			urgent = append(urgent, reported)
			continue
		}
		waiting.Again = append(waiting.Again, reported)
	}
	if len(urgent) > 0 {
		waiting.Urgent = ByFiling(append(urgent, waiting.Urgent...))
	}
	return waiting
}

// pendingFrom is one pass over the pile from one position. A critical is offered
// wherever it sits, because the position is about draining the pile in order and
// a critical is the one thing that must not wait its turn.
func pendingFrom(ordered []Report, position Position, shown map[string]bool) Waiting {
	var waiting Waiting
	for _, reported := range ordered {
		if shown[reported.ID] {
			continue
		}
		if reported.Severity == SeverityCritical {
			waiting.Urgent = append(waiting.Urgent, reported)
			continue
		}
		if position.Passed(reported) {
			continue
		}
		waiting.Next = append(waiting.Next, reported)
	}
	return waiting
}

// Pile is how the collected reports stand: how many there are, how many nobody
// has decided about, how old the oldest of those is, and the worst severity
// among them.
//
// It is derived here rather than at each surface for the reason every shared
// derivation in this harness is: an operator told one unhandled count by the
// terminal and another by the channel has a disagreement to adjudicate rather
// than a pile to work through. What each surface decides for itself is only how
// to word it.
type Pile struct {
	Collected int `json:"collected"`
	Unhandled int `json:"unhandled"`
	// Oldest is when the oldest unhandled report was filed, and OldestAge how long
	// ago that is as of the reading. Both are zero on a pile with nothing
	// unhandled in it, which is the state this whole mechanism is aiming at.
	Oldest    time.Time     `json:"oldest,omitempty"`
	OldestAge time.Duration `json:"oldest_age,omitempty"`
	// Worst is the severity of the most attention-seeking unhandled report, and is
	// empty where there are none. It is what says whether a deep pile is deep with
	// notes or deep with things already costing somebody.
	Worst Severity `json:"worst,omitempty"`
	// Critical is how many of the unhandled reports were filed at critical. It is
	// a count rather than folded into Worst because "worst critical" says one
	// thing is costing somebody when it may be five, and a critical is the
	// severity that means somebody has to act — each one of them.
	Critical int `json:"critical,omitempty"`
	// Addressees is the unhandled reports counted by the role they were
	// addressed to, most first, and summing to Unhandled (addressees.go).
	Addressees []AddresseeCount `json:"addressees,omitempty"`
}

// Draining reports whether there is anything left to work through.
func (p Pile) Draining() bool { return p.Unhandled > 0 }

// Describe says how the pile stands in one line, for whichever surface is
// saying it. There is one wording rather than one per surface for the reason
// there is one derivation: an operator told the pile is 564 deep in the terminal
// and "564 reports outstanding" in a channel has to work out whether those are
// the same number before they can do anything about either.
//
// The age is coarse on purpose. What this answers is whether the oldest thing
// nobody has decided about has been waiting an afternoon or a month, and a
// duration printed to the second is a number a reader has to parse first.
func (p Pile) Describe() string {
	if p.Unhandled == 0 {
		return fmt.Sprintf("nobody is waiting on any of the %d collected report(s)", p.Collected)
	}
	described := fmt.Sprintf("%d of %d collected report(s) are unhandled, the oldest filed %s ago",
		p.Unhandled, p.Collected, pileAge(p.OldestAge))
	switch {
	case p.Critical > 0:
		described += fmt.Sprintf(", %d of them critical", p.Critical)
	case p.Worst != "":
		described += ", worst " + string(p.Worst)
	}
	return described
}

// pileAge is an elapsed time as somebody says one.
func pileAge(elapsed time.Duration) string {
	switch {
	case elapsed < 0:
		// A report stamped ahead of this reading. Said as itself rather than as a
		// negative duration, which reads as a bug in the arithmetic.
		return "no time at all; it is stamped ahead of this reading"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	}
}

// Summarize reads the pile as it stands. It is given the whole of both logs
// because that is what the stores hold: what is expensive about a pile this size
// is delivering it, not counting it.
func Summarize(reports []Report, handlings []Handling, now time.Time) Pile {
	return SummarizeHandled(reports, Handled(handlings), now)
}

// SummarizeHandled is Summarize for a caller that has already indexed what
// became of the pile, which is what a listing that prints each decision under
// its report is holding. A nil map is a pile nothing has been decided about
// rather than one whose decisions could not be read; a caller that cannot tell
// those apart must not call this.
func SummarizeHandled(reports []Report, handled map[string]Handling, now time.Time) Pile {
	pile := Pile{Collected: len(reports)}
	for _, reported := range reports {
		if _, done := handled[reported.ID]; done {
			continue
		}
		pile.Unhandled++
		if pile.Oldest.IsZero() || reported.RecordedAt.Before(pile.Oldest) {
			pile.Oldest = reported.RecordedAt
		}
		if reported.Severity == SeverityCritical {
			pile.Critical++
		}
		if pile.Worst == "" || reported.Severity.rank() < pile.Worst.rank() {
			pile.Worst = reported.Severity
		}
		pile.Addressees = countAddressee(pile.Addressees, reported)
	}
	orderAddressees(pile.Addressees)
	if !pile.Oldest.IsZero() {
		pile.OldestAge = now.UTC().Sub(pile.Oldest.UTC())
	}
	return pile
}
