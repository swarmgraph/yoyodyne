package triage

// Which of the docket's entries the development manager is shown, in what
// order, and how the next pass resumes past what this one showed.
//
// The docket is a log, and on 2026-09-25 it held 187 entries, 125 of them on
// work items already closed and several of them repeats of one stopped run. The
// window a conversation carries was filled newest first by whatever was on the
// log, so it listed eleven entries nothing could act on while twelve stopped runs
// waited behind them for between seven and thirty-six days. A bounded window is
// not optional — a docket of hundreds delivered whole is the turn — so what is
// decided here is what the bound is spent on.
//
// Four things decide it. Only live entries are in it: the work item is not
// closed, and nobody has decided about the entry, or the decision has lapsed or
// the harness was stopped carrying it out. Each stopped run is in it once, however
// many entries it left. It is a walk rather than a listing, exactly as the
// report pile is: the oldest stoppage first, anything critical ahead of it, and a
// durable position that the next pass resumes past, so an entry one pass could not
// show is the first thing the next pass shows. And a stoppage nobody has decided
// comes before every one whose decision is recorded and waiting on the harness,
// however that decision is gated: on 2026-09-26 four decided entries, each read
// as critical for a gate that would not clear on its own, filled the window ahead
// of 29 stoppages nobody had decided
// (docs/diagnoses/yoyodyne-ifd-428-38-decided-entries-filled-the-docket-window.md).
// A stoppage she decided to wait on is listed after both, until the wait runs
// out: on 2026-10-01 eleven stopped runs she had waited on filled the top of
// every docket, because a wait closed only a publication and every other entry
// it was recorded on stood as though nobody had looked.

import (
	"sort"
	"strings"
	"time"
)

// WindowPosition is how far through the live docket, oldest stoppage first, the
// window has been carried. It is a point in that order rather than a count, for
// the reason the report pile's position is: the docket changes between passes, an
// index would name a different entry every time, and a moment plus the key that
// breaks its ties names the same one for as long as it is live.
type WindowPosition struct {
	Since time.Time `json:"since,omitempty"`
	Key   string    `json:"key,omitempty"`
}

// Started reports whether a position names anywhere at all. The zero position is
// the oldest end of the docket, which is where a window that has shown nothing
// stands.
func (p WindowPosition) Started() bool {
	return p.Key != "" || !p.Since.IsZero()
}

// passed reports whether a window at this position has already been carried past
// a stoppage, in the order Live puts the docket in.
func (p WindowPosition) passed(stoppage Stoppage) bool {
	if !p.Started() {
		return false
	}
	switch {
	case stoppage.Since.Before(p.Since):
		return true
	case stoppage.Since.After(p.Since):
		return false
	default:
		return stoppage.Entry.Key <= p.Key
	}
}

// Stoppage is one live stoppage as the window carries it: the entry that speaks
// for it, when the work first stopped, and how many further entries about the
// same run are folded beneath it (Entry.Earlier).
type Stoppage struct {
	Entry Entry
	// Since is the earliest moment any entry about this run was docketed, which is
	// how long the stoppage has waited for somebody. It is what the window orders
	// by rather than the entry's own moment, because the entry kept may be a later
	// one about the same run and the wait began at the first.
	Since  time.Time
	Folded int
	// Decided marks a stoppage every entry of which carries a decision still
	// standing: it is on the docket only because the harness was stopped carrying
	// that decision out. What it asks of the development manager is the gate, not
	// a decision, so it waits behind every stoppage that still needs one.
	Decided bool
	// Waiting marks a stoppage every entry of which carries a decision to wait
	// that has not run out. It asks nothing of anybody until it does, so it is
	// listed after every other stoppage, and once the wait runs out it is a
	// question again at the age it had when it first stopped.
	Waiting bool
}

// At is the position a window is left at by being carried past one stoppage.
func (s Stoppage) At() WindowPosition {
	return WindowPosition{Since: s.Since.UTC(), Key: s.Entry.Key}
}

// LiveDocket is the docket as the window reads it: the live stoppages, oldest
// first, and how many entries were left out of them and why. The counts are
// kept rather than dropped because a window that silently shows a subset is one
// a reader takes for the whole.
type LiveDocket struct {
	Stoppages []Stoppage
	// Dead counts entries on a work item the tracker holds as closed. Nothing a
	// development manager decides about one changes anything, because the work it
	// stopped is finished or withdrawn.
	Dead int
	// Settled counts entries a decision still standing has settled. The docket's
	// own build already leaves these out; they are counted here as well so a
	// caller handing the window the whole log is shown the same thing. A wait is
	// not among them: a waited stoppage is listed, last (Stoppage.Waiting).
	Settled int
	// Folded counts entries this read folded beneath another about the same run;
	// a docket the build already folded has none left to fold.
	Folded int
}

// Live reads the docket for the window. closed answers whether the tracker holds
// a work item as closed; nil is a tracker that could not be asked, and then no
// entry is taken for dead, because hiding a stoppage on the strength of a
// listing nobody read is the one direction this must not fail in. An item the
// answer does not know is live for the same reason.
func Live(entries []Entry, closed func(workItemID string) bool, now time.Time) LiveDocket {
	var docket LiveDocket
	open := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if !entry.Undecided(now) && !entry.WaitStands(now) {
			docket.Settled++
			continue
		}
		if closed != nil && closed(strings.TrimSpace(entry.WorkItemID)) {
			docket.Dead++
			continue
		}
		open = append(open, entry)
	}
	// The docket's own build has already folded each run's repeats (Fold), so
	// this folds nothing there; it is asked again for a caller handing the window
	// the log as it stands, and folds the same way, with the evidence beneath.
	folded := Fold(open)
	docket.Folded = len(open) - len(folded)
	for _, entry := range folded {
		stoppage := Stoppage{Entry: entry, Since: entry.RecordedAt, Folded: len(entry.Earlier),
			Decided: entry.decisionStands(now), Waiting: entry.WaitStands(now)}
		for _, earlier := range entry.Earlier {
			if earlier.RecordedAt.Before(stoppage.Since) {
				stoppage.Since = earlier.RecordedAt
			}
			stoppage.Decided = stoppage.Decided && earlier.decisionStands(now)
			stoppage.Waiting = stoppage.Waiting && earlier.WaitStands(now)
		}
		// A wait is a decision that nothing is to be carried out yet, so a waited
		// stoppage is never one waiting on the harness.
		stoppage.Decided = stoppage.Decided && !stoppage.Waiting
		docket.Stoppages = append(docket.Stoppages, stoppage)
	}
	sort.SliceStable(docket.Stoppages, func(i, j int) bool {
		left, right := docket.Stoppages[i], docket.Stoppages[j]
		if !left.Since.Equal(right.Since) {
			return left.Since.Before(right.Since)
		}
		return left.Entry.Key < right.Entry.Key
	})
	return docket
}

// Critical reports a stoppage that must not wait its turn in the walk: the entry
// standing for its run is critical, or one folded beneath it is. The entry that
// stands is the run's latest docketing, and an escalation docketed before a later
// account of the same run is still the item waiting on a decision.
func (s Stoppage) Critical() bool {
	if s.Entry.Critical() {
		return true
	}
	for _, earlier := range s.Entry.Earlier {
		if earlier.Critical() {
			return true
		}
	}
	return false
}

// Undecided reports an entry that is still a question at a moment: nobody has
// decided about it, the decision has lapsed, or the harness tried to carry the
// decision out and a gate stopped it.
//
// A finding that the recovery no longer applies does not reopen a settled
// entry (CarryOutStopped), since it asks nobody to decide anything.
func (e Entry) Undecided(at time.Time) bool {
	return e.Closed == nil || !e.Closed.Holds(at) || e.CarryOutStopped()
}

// decisionStands reports an entry a decision still holds over at a moment. Such
// an entry is on the docket at all only because its carry-out was stopped, and
// it is the one kind of live entry that asks nobody to decide anything.
func (e Entry) decisionStands(at time.Time) bool {
	return e.Closed != nil && e.Closed.Holds(at)
}

// WaitStands reports an entry a decision to wait still holds over at a moment:
// a decision that names when it is to be looked at again, which has not come,
// or names the work item it waits on, which is not closed yet. Such an entry is
// closed and asks nobody anything, and the docket still lists it, after
// everything else, so a reader can see what is being waited on and till when
// rather than taking it for settled.
func (e Entry) WaitStands(at time.Time) bool {
	return e.Closed != nil && (!e.Closed.RevisitAfter.IsZero() || e.Closed.WaitsOnWork()) &&
		e.Closed.Holds(at) && !e.CarryOutStopped()
}

// CarryOutStopped reports a settled entry whose decision the harness has tried to
// carry out since it was decided, and been stopped. The finding has to be about
// this decision — made after it — because a finding about an earlier decision on
// the same stoppage is one this decision has since superseded.
func (e Entry) CarryOutStopped() bool {
	return e.Closed != nil && e.CarryOut != nil && !e.CarryOut.NoLongerApplies() && e.CarryOut.RefusedAt.After(e.Closed.ClosedAt)
}

// Critical reports an entry that must not wait its turn in the walk: a role
// having said the item cannot be met as it stands, which parks the item until the
// development manager decides, and a decision of hers the harness was stopped
// carrying out by a gate that will not clear on its own. A product decision about
// a run in flight is the third: the run spends for as long as the question waits,
// so it is not left behind older stoppages that spend nothing while they wait.
//
// A stopped carry-out is critical among the decided entries only: it leads
// them, and it never goes ahead of a stoppage nobody has decided (Walk).
func (e Entry) Critical() bool {
	if e.Class == ClassEscalation || e.Class == ClassProductDecision {
		return true
	}
	// A legacy grant with no decision record has no closure, but its permanent
	// refusal still needs the manager's attention at once.
	if e.CarryOut != nil && e.CarryOut.Cause != "" && !e.CarryOut.NoLongerApplies() {
		return true
	}
	return e.CarryOutStopped() && !e.CarryOut.Waiting
}

// Window is what one pass is offered of the live docket, in the three parts a
// caller has to keep apart and in the order it lists them. Urgent jumps the walk
// and Next is the walk: the position advances over what was shown from Next and
// never over what jumped, because a critical at the far end of the docket would
// otherwise carry the position past everything between. Decided comes after
// both: the stoppages whose decision is recorded and waiting on the harness,
// critical ones first, listed only where the window has room once every
// stoppage nobody has decided is listed. Waiting comes last of all: the
// stoppages she decided to wait on, oldest first, until each wait runs out.
type Window struct {
	Urgent  []Stoppage
	Next    []Stoppage
	Decided []Stoppage
	Waiting []Stoppage
}

// Walk is what a window at one position is offered. Next begins past the
// position and carries on from the oldest end once it reaches the newest, so the
// walk is a cycle through everything live: nothing is buried behind the
// position, and a pass is never offered less than the docket holds because the
// last pass happened to end near the newest end.
func Walk(stoppages []Stoppage, position WindowPosition) Window {
	var window Window
	var behind, gated []Stoppage
	for _, one := range stoppages {
		switch {
		case one.Waiting:
			window.Waiting = append(window.Waiting, one)
		case one.Decided && one.Critical():
			window.Decided = append(window.Decided, one)
		case one.Decided:
			gated = append(gated, one)
		case one.Critical():
			window.Urgent = append(window.Urgent, one)
		case position.passed(one):
			behind = append(behind, one)
		default:
			window.Next = append(window.Next, one)
		}
	}
	window.Next = append(window.Next, behind...)
	window.Decided = append(window.Decided, gated...)
	return window
}
