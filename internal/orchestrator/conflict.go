package orchestrator

// What the scheduler will not start beside what, and how it says so.
//
// The scheduler enforces almost nothing, and this is not an exception to that.
// Integration is already serialized by the promotion lease, a target branch that
// moved is already handled by the replay, and a replay that will not apply
// already stops its run with both sides preserved. Every one of those still
// holds whether or not anything here runs.
//
// What they cost is the point. Two runs started at once over the same files are
// one promotion plus, on the loser, a replay, a fresh set of checks, and an
// entirely fresh review — or a stopped run and an item waiting for a person. So
// the scheduler declines to buy that cost when it can see it coming, and
// sequences the two rather than racing them.
//
// Two things let it see one coming: an item and the epic a run is already over,
// which are one scope however the tracker files them, and two items that will
// change the same files. Sharing a parent is deliberately not a third, though
// it was until yoyodyne-ifd.261. Priority-0 work is not held back by the second
// behind a run over less urgent work (outranks).
//
// The intent this map carried was that two children of one epic race each other:
// work broken out of one piece is work over one part of the repository. That is
// true of a decomposition and false of a heading, and nothing structural tells
// one from the other — both are an item with children, and a heading's children
// are unrelated to each other by construction. This tracker files its whole
// backlog under headings ("Scheduler and pipeline", "CLI and operator
// surfaces"), so the rule held every child of a heading behind whichever of them
// started first: on the reading of 2026-09-22, 29 of the 109 unfinished items
// sat behind one epic and 15 behind another. That is the queue serialized rather
// than one race declined, and it was measured throughput loss rather than a
// hypothesis — one of the two causes named for the idle line of 2026-09-04.
//
// So the intent is amended rather than honored, and what replaces it is the
// relation that really is one piece of work: an item and the epic it was broken
// out of. That draws the container-versus-decomposed line where it can be drawn
// soundly rather than guessed at. An epic nobody is running is a container and
// holds nothing back, whatever hangs off it; an epic a run is over is one whose
// execution a child of it would be carrying, so that child waits. It is the
// yoyodyne-ifd.121 shape — the epic in flight, the decomposition creating its
// child underneath it, the two started as two runs of one scope — and it is the
// half yoyodyne-ifd.256 left open. Two children of a heading race nothing by
// having been filed together, and two children of a real decomposition that will
// touch the same code are caught by the surfaces below, which read what the
// items say rather than what their filing implies.
//
// It is choosing rather than enforcing, which is why it is here and not
// downstream. And it is only choosing: nothing here holds an item back past the
// pull it was held at. The conflicts are re-read at every pull from what is
// actually in flight, so an item held at one poll is pulled at the next one
// where the run it would have raced has ended.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/surface"
)

// maxSequencedItemsNamed bounds how many held-back items one recorded reason
// names before it falls back to counting them. The count stays exact either way,
// for the reason every listing in this package is bounded: a reason nobody
// finishes reading accounts for nothing.
const maxSequencedItemsNamed = 3

// holder is one run in flight as the guard names it: the item it is over, and
// the run itself where the pull can know it. A run this session started has no
// identifier until it reserves, which is several steps after it is started, so
// for that one pull it is named as the session's own rather than by a run.
//
// The run is named because a reader is going to check it. Until
// yoyodyne-ifd.379 the report named the item alone, and an item cannot be checked
// against `yoyo status`, which lists runs: on 2026-09-18 a report that said
// yoyodyne-ifd.272 was in flight, two days after its last run had failed, was
// read as the guard holding a developer slot on a dead run.
type holder struct {
	item string
	run  string
	// priority is the priority of the item the run is over, as the pull read it,
	// which decides whether top-priority work waits for it (outranks).
	priority int
}

// String is how the reason names the run.
func (h holder) String() string {
	if h.run == "" {
		return "the run this session started for " + h.item
	}
	return h.run + " (" + h.item + ")"
}

// conflict is one reason an item was not started now: the work already in
// flight it would race, and what the two of them share.
type conflict struct {
	// With is the run already going, and the item it is over.
	With holder
	// Over is what the two share, in words, because what an operator does about
	// a shared epic and about a shared file are different things.
	Over string
}

// inFlight is what the runs already going have taken, as one pull sees it: the
// items they are over, the epics those items were broken out of, and the
// surfaces they will change.
//
// It is built per pull and grows as that pull starts things. An item started
// three entries ago is in flight as surely as one another process is running,
// and the durable run state does not know about it yet — a run does not appear
// there until it reserves, which is several steps after it is started.
//
// What it is built from is the runs in flight now and nothing else, in the one
// sense runstate.Status.InFlight gives that: pending or running, whatever phase.
// A run that failed — at integration, on a replay conflict, with its branch and
// pull request preserved for a person — is a record, and a record holds no
// epic. See occupiedItems, which is where that reading is made.
type inFlight struct {
	// running maps the item each in-flight run is over to that run. A candidate
	// broken out of one of these would be carrying part of that run's own scope,
	// which is the race the header describes.
	running map[string]holder
	// carrying maps the epic an in-flight run's item was broken out of to that
	// run, so an epic is not started beside a run already carrying a piece of its
	// execution. The queue-side coverage check answers the same question from the
	// tracker's own reading of the backlog; this answers it for a child this pull
	// started itself, and for a run over an item the reading no longer lists.
	//
	// It is keyed by the epic rather than by the child, and read only against a
	// candidate's own identifier. A candidate is never compared against it by
	// parent, which is what would put two children of one heading back into a
	// queue behind each other.
	carrying map[string]holder
	// taken is the surfaces each in-flight run holds, in the order the runs
	// were taken, so which conflict is reported for a candidate is stable rather
	// than an artifact of map ordering.
	taken []takenSurfaces
}

type takenSurfaces struct {
	by    holder
	paths []string
}

func newInFlight() *inFlight {
	return &inFlight{running: map[string]holder{}, carrying: map[string]holder{}}
}

// take records a run over an item as work in flight, so nothing that would race
// it is started beside it. The run is the identifier the durable state gave it,
// and empty for a run this pull started itself, which has none yet. An item the
// tracker no longer lists arrives here empty and takes nothing: a run over work
// that has left the queue cannot be compared with anything, and guessing at what
// it touches would hold real work back on no evidence.
func (f *inFlight) take(item beads.WorkItem, run string) {
	id := strings.TrimSpace(item.ID)
	if id == "" {
		return
	}
	by := holder{item: id, run: strings.TrimSpace(run), priority: item.Priority}
	claim(f.running, id, by)
	// Parentage is read whichever way the tracker states it, as the queue-side
	// coverage check reads it. bd states it as a field beside the item and as a
	// parent-child edge, a store may use either, and this project's own export
	// uses only the edge — so a reading of the field alone sees such a store as a
	// backlog nothing was ever broken out of, and this guard as an empty map.
	// beads.WorkItem.DecomposedFrom is where both readings live.
	if parent := item.DecomposedFrom(); parent != "" {
		claim(f.carrying, parent, by)
	}
	if paths := surface.Of(item); len(paths) > 0 {
		f.taken = append(f.taken, takenSurfaces{by: by, paths: paths})
	}
}

// claim records one identifier against the first in-flight run to hold it. The
// first rather than the last, so a candidate held back at one pull is told about
// the same run at the next one for as long as that run lasts.
func claim(held map[string]holder, id string, by holder) {
	if _, taken := held[id]; !taken {
		held[id] = by
	}
}

// against reports the in-flight work an item would race, and over what. Most
// items race nothing, which is the answer that keeps unrelated work running
// concurrently: this is a reason to sequence two items, not a reason to
// serialize the queue.
func (f *inFlight) against(item beads.WorkItem) (conflict, bool) {
	id := strings.TrimSpace(item.ID)
	// The epic this item was broken out of, with a run already over it: the
	// child carries part of that run's own scope, so it waits rather than
	// racing it. Only a run over the epic itself holds a child back — a run over
	// another child of the same epic is not this item's work, whatever the two
	// share by being filed together.
	if parent := item.DecomposedFrom(); parent != "" {
		if by, held := f.running[parent]; held && by.item != id {
			return conflict{With: by, Over: "the epic " + parent + " this item was broken out of"}, true
		}
	}
	// The other direction: this item is the epic, and a run is already over a
	// child carrying part of its execution. The queue-side coverage check
	// ordinarily catches that from the tracker's own reading; this catches the
	// child started later in this same pull, which that reading predates.
	if by, held := f.carrying[id]; held && by.item != id {
		return conflict{With: by, Over: "the epic " + id + " that run was broken out of"}, true
	}
	mine := surface.Of(item)
	if len(mine) == 0 {
		return conflict{}, false
	}
	for _, taken := range f.taken {
		if taken.by.item == id || outranks(item, taken.by) {
			continue
		}
		if shared, races := surface.Shared(mine, taken.paths); races {
			return conflict{With: taken.by, Over: "the surface " + shared}, true
		}
	}
	return conflict{}, false
}

// outranks is whether a candidate is started over a surface it shares with a
// run rather than sequenced behind it: the candidate is at priority 0 and the run
// is over less urgent work. A wait behind a run lasts as long as the run does,
// which for a run whose record says it is going when nothing is carrying it is
// indefinitely, and in the meantime every slot goes to work below the top of the
// order. Racing it costs a replay, and the less urgent run is the one that pays:
// whichever change promotes second replays, and top-priority work started now is
// usually first. Only the shared surface gives way. A child of an epic a run is
// over still waits whatever either's priority, because that is the same change
// made twice rather than two changes that touch one file.
func outranks(candidate beads.WorkItem, run holder) bool {
	return candidate.Priority == 0 && run.priority > 0
}

// reason is what the schedule says about an item held back for this conflict. It
// says what would have been bought as well as what was avoided, because an
// operator reading that the scheduler passed over ready work needs the trade
// rather than the rule.
//
// It is dated to the pull rather than said in the present tense, because the
// schedule that carries it is rendered when the session ends, which can be days
// after the pull that last held the item. A line that said a run "is already in
// flight" was read, on 2026-09-18, as the guard's current reading rather than as
// a session's record of one.
func (c conflict) reason() string {
	return fmt.Sprintf(
		"it would race %s, which was in flight over %s at the last pull that held it back. Sequencing them costs a wait; racing them costs the loser a replay, a fresh set of checks, and a fresh review. It is pulled once that run ends",
		c.With, c.Over)
}

// waited is the same fact in the past tense, for the recorded reason of the
// selection that eventually pulls the item.
func (c conflict) waited() string {
	return fmt.Sprintf("%s was in flight over %s", c.With, c.Over)
}

// sequencing is how conflict-avoidance shaped one selection. Both halves are
// ordinarily empty, and a selection records nothing about either then: most
// items race nothing, and a reason that said so on every run would be a sentence
// nobody reads on the runs where it matters.
type sequencing struct {
	// after is the hold this item itself came out of, where this pass held it
	// back earlier and the run it would have raced has since ended.
	after conflict
	// ahead names the items earlier in the product manager's order that this
	// pull held back for a conflict, which is why this item was pulled before
	// them. The order is chosen rather than departed from: the queue is still
	// read top to bottom, and what moved is only what was startable now.
	ahead []string
}

// reason is what the selection records about having been sequenced. It is
// appended to the ordinary account of why the item was chosen rather than
// replacing it: where the item sat in the order is still the first thing that
// picked it, and this is what happened to that order on the way.
func (s sequencing) reason() string {
	var said strings.Builder
	if s.after.With.item != "" {
		fmt.Fprintf(&said, " It was held back earlier in this session because %s, and was pulled once that cleared.", s.after.waited())
	}
	if len(s.ahead) > 0 {
		named := s.ahead
		if len(named) > maxSequencedItemsNamed {
			named = named[:maxSequencedItemsNamed]
		}
		fmt.Fprintf(&said, " %d item(s) ahead of it in the order (%s", len(s.ahead), strings.Join(named, ", "))
		if further := len(s.ahead) - len(named); further > 0 {
			fmt.Fprintf(&said, ", and %d further", further)
		}
		said.WriteString(") were held back at this pull to keep them from racing work already in flight, so this was pulled in their place.")
	}
	return said.String()
}
