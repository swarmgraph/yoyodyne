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
// A preference reorders work across every priority, priority 0 included: a slot
// that prefers labels takes ready work carrying any of them ahead of priority 0
// work carrying none, whether or not it is marked dedicated, and nothing — no
// setting, timer or wait — lets that priority 0 work go ahead of it (the
// operator's rule of 2026-11-21 in docs/product/operating-rules.md). A priority
// 0 item that no slot's labels cover starts when some slot has none of its own
// labelled work ready: a slot with no labels, which keeps the ordinary order,
// priority first, or a preferring slot falling back. Until then the report names
// it as waiting for such a slot (waitingForAnotherSlotReason), and status says
// how long it has waited. Work on a change a run already wrote goes ahead of all
// of this, on every slot, and among that written work the one carrying a free
// slot's labels goes first (Scheduler.Schedule's precedence and
// takeContinuations).
//
// What a preference changes is which item a slot pulls first, and nothing else.
// The item a preferring slot starts is claimed, developed, checked, reviewed, and
// promoted exactly as it would be from any slot, under the same contract and the
// same authority table: configuration selects what a slot pulls and never widens
// what it may do.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/developerslot"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
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
	// keepWait is passOver for an entry the tracker calls ready: it names the
	// entry on the schedule and also keeps the reason on the item, with the
	// class the poll counts it under. See Schedule's keepWait.
	keepWait func(workItemID string, class runstate.PassedOverClass, reason string)
	// windows is what the recorded usage windows hold at this pull, where they
	// close some developer models and not every one; see usageWindow.
	windows readmodel.CapacityWindows
}

// keepWaitOn keeps a ready entry's reason through keepWait, and names it
// through passOver alone where the reading carries no keepWait.
func (r eligibilityReading) keepWaitOn(workItemID string, class runstate.PassedOverClass, reason string) {
	if r.keepWait != nil {
		r.keepWait(workItemID, class, reason)
		return
	}
	r.passOver(workItemID, reason)
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
		reading.keepWaitOn(entry.ID, runstate.PassedOverCoveredByChildren, backlog.CoveredReason(covering))
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
		reading.keepWaitOn(entry.ID, runstate.PassedOverPausedByDirective, "an unresolved directive pauses it: "+pausing[0].Summary())
		poll.pass(entry.ID, runstate.PassedOverPausedByDirective, "")
		return passedOver, nil
	}
	// A recorded usage window that closes the models this item's run would ask
	// for — until a reset the provider named, or until the next paced probe of a
	// refusal that named none — is a refusal the dispatch would meet. The item is
	// passed over rather than started into it, and nothing about it changes: it
	// keeps its priority and its place, and the first pull after the reset or at
	// the probe reads it as startable again. It is re-read from the durable
	// records at every pull, so a restarted session holds it too, and the reason
	// is kept on the item so it is shown waiting with when it is next tried.
	//
	// A pull with a slot that dispatches onto an endpoint pair of its own asks
	// this per slot instead, as the slot takes the item (windowFor), because the
	// answer depends on which slot that is. A probe that is due is also decided
	// there, since only the one item actually started is the probe.
	if window, models, verdict := s.windowFor(pull, reading.windows, read.items[entry.ID], 0); verdict == readmodel.CapacityClosed && !pull.routesSlots() {
		reason := windowReasonFor(window, models)
		reading.keepWaitOn(entry.ID, runstate.PassedOverWaitingOnUsageWindow, reason)
		poll.passWindow(entry.ID, reason)
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
	unmet, problem, unchecked := pull.unready(read.items[entry.ID])
	if problem != "" && schedule.ReadinessProblem == "" {
		schedule.ReadinessProblem = problem
	}
	if unchecked != "" && schedule.ReadinessUnchecked == "" {
		schedule.ReadinessUnchecked = unchecked
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
		reading.keepWaitOn(entry.ID, runstate.PassedOverPrerequisiteUnmet, unreadyReason(unmet))
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
}

// coveredBySlot reports whether some configured developer slot prefers one of
// these labels. Only the first capacity slots are read, because a preference
// beyond the capacity names no slot (developerslot.Assign).
func coveredBySlot(capacity int, preferences []domain.DeveloperSlot, labels []string) bool {
	for index, preference := range preferences {
		if index >= capacity {
			break
		}
		if preference.Preferring() && preference.Prefers(labels) {
			return true
		}
	}
	return false
}

// labelledForFreeSlot reports whether some free slot prefers one of these
// labels, which is what puts written work carrying them ahead of other written
// work: a freed slot takes written work on its own labels first, then the rest
// by priority and longest wait (writtenWorkOrder).
func labelledForFreeSlot(free []developerslot.Slot, labels []string) bool {
	for _, slot := range free {
		if slot.Preferring() && slot.Prefers(labels) {
			return true
		}
	}
	return false
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
	case p.labelled && p.slot.Dedicated:
		return fmt.Sprintf(" It was pulled into developer slot %d, which is kept for %s this item carries.", p.slot.Number, p.slot.Preference())
	case p.slot.Dedicated:
		return fmt.Sprintf(" It was pulled into developer slot %d, which is kept for %s and found none of that work ready, so it took the ordinary order.", p.slot.Number, p.slot.Preference())
	case p.labelled:
		return fmt.Sprintf(" It was pulled into developer slot %d, which prefers %s this item carries.", p.slot.Number, p.slot.Preference())
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
	if slot.Dedicated {
		return fmt.Sprintf("%s: developer slot %d is kept for %s and took %s ahead of it; a slot with no preference takes this item in the Lead Product Manager's order, and slot %d takes it once none of %s's work is ready",
			runstate.PassedOverLeftForAnotherSlot, slot.Number, slot.Preference(), took, slot.Number, slot.Preference())
	}
	return fmt.Sprintf("%s: developer slot %d prefers %s and pulled %s ahead of it; a slot with no preference takes this item in the Lead Product Manager's order, and slot %d falls back to it once none of %s's work is ready",
		runstate.PassedOverLeftForAnotherSlot, slot.Number, slot.Preference(), took, slot.Number, slot.Preference())
}

// waitingForAnotherSlotReason is what the report says of ready priority 0 work
// that no slot's labels cover, walked past by a slot taking its own labelled
// work, where no other slot was free to take it. Both items are named by title
// with the identifier after it, because this is the line an operator reads to
// see why top-priority work is not running. The sentence stays the same while
// the item waits, so the kept reason's first date is when the wait began, and
// status says how long that has been (readmodel's markSchedulingWait).
func waitingForAnotherSlotReason(slot developerslot.Slot, waiting, took string) string {
	kept := "prefers"
	if slot.Dedicated {
		kept = "is kept for"
	}
	return fmt.Sprintf("%s: %s is at priority 0, and no developer slot's labels cover it, so it is waiting for a developer slot with none of its own labelled work ready; developer slot %d %s %s and took %s",
		runstate.PassedOverLeftForAnotherSlot, waiting, slot.Number, kept, slot.Preference(), took)
}

// titledItem names a work item the way a person reads it: its title with its
// identifier after it, or the identifier alone where the item has no title.
func titledItem(item beads.WorkItem, id string) string {
	if title := strings.TrimSpace(item.Title); title != "" {
		return title + " (" + id + ")"
	}
	return id
}
