package orchestrator

// How one pull fills the developer slots it found free, once a slot can prefer a
// label.
//
// Before slots could prefer anything a pull walked the queue once, in the product
// manager's order, and spent each free slot on the next item it could start. A
// slot that prefers a label breaks that walk in two: it pulls the ready work
// carrying its label first, wherever that sits in the order, and the rest of the
// backlog only when none of its label's work is ready. So the questions a pull
// asks of an item are separated from the walk that asks them — each item is
// judged once however many slots reach it — and the walk is made per slot, with
// the preferring slots first so that labelled work goes to the slot configured
// for it before a slot with no preference reaches it.
//
// A preference reorders work below the top priority and never across it: ready
// priority-0 work is taken by whichever slot reaches it first, preferring or not,
// before any labelled work of a lower priority (outranksPreference).
//
// What a preference changes is which item a slot pulls first, and nothing else.
// The item a preferring slot starts is claimed, developed, checked, reviewed, and
// promoted exactly as it would be from any slot, under the same contract and the
// same authority table: configuration selects what a slot pulls and never widens
// what it may do.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/developerslot"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// eligibility is one pull's answer about one entry: it may be started, it was
// passed over with its account recorded, or a reading of the harness failed
// before an answer was reached.
type eligibility int

const (
	passedOver eligibility = iota
	startable
	unanswered
)

// walk is what one slot's walk of the queue came to.
type walk int

const (
	// walkNothing is a slot that reached the end of the queue with nothing it
	// could take, which for a preferring slot asking for its label is the
	// moment it falls back.
	walkNothing walk = iota
	walkStarted
	// walkStopped is the probe's record refusing the item, which ends the
	// choosing for this pull.
	walkStopped
	// walkUnreadable is a reading of the harness that failed mid-walk, carried
	// back with its error for the pull to ride out or stop on.
	walkUnreadable
)

// eligibilityReading is what the questions about one entry are asked against:
// the pull, the queue as it was read, what this session has tried, what is in
// flight, and where a passover is recorded.
type eligibilityReading struct {
	pull     Pull
	read     pulled
	tried    map[string]attempt
	occupied map[string]runstate.State
	// waiting is the runs in flight paused on work their items wait on, which
	// hold no developer slot and so are not in occupied, and whose items are
	// still not ones to start a second run of.
	waiting  map[string]runstate.State
	schedule *Schedule
	poll     *idlePoll
	passOver func(workItemID, reason string)
}

// eligibility asks every question about one entry but the conflict one, and
// records what passed the entry over where something did. The conflict is asked
// by the walk, because it depends on what the pull has already started; these
// hold for the whole pull.
//
// The order is the order the questions cost, cheapest first, and the last of
// them is the only one that reads the repository. A reading that fails is
// returned as unanswered with its error, and nothing is recorded about the
// entry, so the pull that reads it again asks afresh.
func (s Scheduler) eligibility(entry backlog.Entry, reading eligibilityReading) (eligibility, error) {
	pull, read, schedule, poll := reading.pull, reading.read, reading.schedule, reading.poll
	if !entry.Ready {
		// Unready entries are counted rather than listed, with two exceptions:
		// an item no developer run can carry, and an item somebody parked.
		// Neither is waiting for anything, so the count they would otherwise
		// disappear into — work that will become pullable — is a count neither
		// of them will ever join. Each is named once, like every other
		// deferral, and what it names is what somebody does about it.
		if reason, named := passedOverReason(entry); named {
			reading.passOver(entry.ID, reason)
		}
		poll.pass(entry.ID, unreadyClass(entry), entry.Executor.Role())
		return passedOver, nil
	}
	if s.cooling(reading.tried, read.items[entry.ID]) {
		if recorded := reading.tried[entry.ID]; !recorded.until.IsZero() {
			poll.passWindow(entry.ID, recorded.reason)
		} else {
			poll.passTried(entry.ID, recorded.reason)
		}
		return passedOver, nil
	}
	_, busy := reading.occupied[entry.ID]
	if _, paused := reading.waiting[entry.ID]; busy || paused {
		poll.pass(entry.ID, runstate.PassedOverAlreadyInFlight, "")
		return passedOver, nil
	}
	// A decomposed item is not itself a run. Its children are where the work
	// went, and starting the parent beside them buys the same change a second
	// time — two developers rewriting one file, the second of them guaranteed
	// a conflict at integration. Nothing downstream would catch it: the
	// reservation sees two different items, and the tracker reports the parent
	// as ready because nothing blocks it.
	//
	// A child covers whether it is queued or already claimed, because both are
	// work that has not been done yet. This is re-read at every pull like
	// everything else, so an item stops being covered when its last child
	// closes, and one that is decomposed while the session watches stops being
	// pullable at the next selection. The derivation and the words are the
	// backlog's, which is what the standing status refuses the same item with.
	if covering := read.coverage.Covering(entry.ID); len(covering) > 0 {
		reading.passOver(entry.ID, backlog.CoveredReason(covering))
		poll.pass(entry.ID, runstate.PassedOverCoveredByChildren, "")
		return passedOver, nil
	}
	// An unresolved directive stops the work whether it is read here or in
	// the pipeline, so this is not the enforcement — it is the scheduler
	// declining to spend a slot on a start it can see will not proceed, and
	// saying which directive it was.
	pausing, err := pull.Directives.Pausing(entry.ID)
	if err != nil {
		return unanswered, fmt.Errorf("read the directives that pause %s: %w", entry.ID, err)
	}
	if len(pausing) > 0 {
		// The directive is re-read at every pull rather than remembered,
		// because what clears it is a person and the whole point of a pass
		// that outlives them answering is that it notices. What is
		// remembered is only that this was said: an item paused all night
		// is one line in the report rather than one per poll.
		reading.passOver(entry.ID, "an unresolved directive pauses it: "+pausing[0].Summary())
		poll.pass(entry.ID, runstate.PassedOverPausedByDirective, "")
		return passedOver, nil
	}
	// The last question asked before a slot is spent, and the only one that
	// reads the repository: does the tree hold what this item says it needs?
	// It is asked last because everything above it is cheaper and because
	// everything above it clears on its own, where this needs a person; and
	// it is asked at all because the four times it was not, the answer cost
	// a full run each to discover. See readiness for the incidents and for
	// what the two readings are.
	//
	// A reading that failed is not a refusal. The tree is unreadable, which
	// says nothing about the item, so the item is dispatched exactly as it
	// would have been and the failed reading is reported beside the pass.
	unmet, problem := pull.unready(read.items[entry.ID])
	if problem != "" && schedule.ReadinessProblem == "" {
		schedule.ReadinessProblem = problem
	}
	if len(unmet) > 0 {
		// Routed to the development manager rather than only reported, because
		// a refusal that lives in one pass's output is one nobody reads: the
		// docket is where work nothing will pick up already goes. The write is
		// keyed to the item and what was found, so a session polling every
		// fifteen seconds dockets this once rather than once a poll.
		if err := pull.route(read.items[entry.ID], unmet); err != nil && schedule.ReadinessProblem == "" {
			schedule.ReadinessProblem = err.Error()
		}
		reading.passOver(entry.ID, unreadyReason(unmet))
		poll.pass(entry.ID, runstate.PassedOverPrerequisiteUnmet, "")
		return passedOver, nil
	}
	return startable, nil
}

// walkedPast is one entry a preferring slot walked past for want of its label,
// the slot that did, and the item the slot took instead.
type walkedPast struct {
	entry backlog.Entry
	slot  developerslot.Slot
	took  string
}

// pulledInto is which slot an item was started in and on what footing: because
// it carries the label the slot prefers, because the slot prefers a label and
// found none of its work ready, or because the slot prefers nothing.
type pulledInto struct {
	slot     developerslot.Slot
	labelled bool
	// outranked is a preferring slot that took top-priority work without its
	// label while walking for its label's work.
	outranked bool
}

// outranksPreference is whether an entry is taken by a preferring slot walking
// for its label even though it does not carry that label. Priority 0 is the
// top of the Lead Product Manager's order, where the work that holds everything
// else up is put — main's failing build, a fault that stops runs — and a slot
// that walked past it for labelled work at priority 1 or below would start the
// less urgent work first whenever it was the only slot free. Below the top, a
// preference still reorders freely, which is what it is configured for.
func outranksPreference(entry backlog.Entry) bool {
	return entry.Priority == 0
}

// reason is the sentence the run's recorded selection carries about its slot.
// It is empty for a project whose slots prefer nothing, so a run in such a
// project records exactly the reason it always did: which slot a run took is
// only worth saying where the slots differ.
func (p pulledInto) reason(preferences []domain.DeveloperSlot) string {
	if !developerslot.Preferring(preferences) {
		return ""
	}
	switch {
	case p.labelled:
		return fmt.Sprintf(" It was pulled into developer slot %d, which prefers %s this item carries.", p.slot.Number, p.slot.Preference())
	case p.outranked:
		return fmt.Sprintf(" It was pulled into developer slot %d, which prefers %s; this item does not carry it, and is taken ahead of that work because it is at priority 0.", p.slot.Number, p.slot.Preference())
	case p.slot.Preferring():
		return fmt.Sprintf(" It was pulled into developer slot %d, which prefers %s and found none of that work ready, so it fell back to the rest of the backlog.", p.slot.Number, p.slot.Preference())
	default:
		return fmt.Sprintf(" It was pulled into developer slot %d, which prefers no label.", p.slot.Number)
	}
}

// leftForAnotherSlotReason is what the report says of a ready item a preferring
// slot walked past and no other slot reached. It is named as what it is rather
// than as a deferral: the item waits on nothing about itself, and what pulls it
// is the next slot with no preference to come free, or the preferring slot once
// its label's work is exhausted.
func leftForAnotherSlotReason(slot developerslot.Slot, took string) string {
	return fmt.Sprintf("%s: developer slot %d prefers %s and pulled %s ahead of it; a slot with no preference takes this item in the Lead Product Manager's order, and slot %d falls back to it once none of %s's work is ready",
		runstate.PassedOverLeftForAnotherSlot, slot.Number, slot.Preference(), took, slot.Number, slot.Preference())
}
