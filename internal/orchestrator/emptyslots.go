package orchestrator

// Why a poll left a developer slot empty while ready work waited, said by the
// scheduler as it decides it.
//
// Every way a pull can end without filling a free slot names one of the causes
// in ownership.EmptySlotCause, and the session writes it onto the watch log
// (runstate.EmptySlots) when it differs from what it last wrote. A pull that
// waits without naming one is written as not known rather than as nothing, so
// an empty slot is never left unexplained. The read model reads it back for
// the health check and `yoyo status` (readmodel.EmptySlots).

import (
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/developerslot"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// countedGrace is how long a start of this session may go without a run in
// flight on record before the slot it holds is said to be still counted for
// it. A dispatch takes some seconds to reserve its run, and saying so about
// every start would write a line for each.
const countedGrace = 2 * time.Minute

// slotAccount is what one pull says about its empty slots. free is how many
// slots it found free, or -1 before it counted them, and slots their numbers;
// account is the reason a site in the pull set, where one did.
type slotAccount struct {
	free    int
	slots   []int
	account *runstate.EmptySlots
	set     bool
}

func newSlotAccount() slotAccount { return slotAccount{free: -1} }

// counted records the free slots this pull found.
func (a *slotAccount) counted(free int, slots []int) {
	a.free = max(free, 0)
	a.slots = append([]int(nil), slots...)
	if len(a.slots) > a.free {
		a.slots = a.slots[:a.free]
	}
}

// say sets the pull's reason. A nil account is the pull having found no slot
// empty while ready work waited.
func (a *slotAccount) say(account *runstate.EmptySlots) {
	a.account, a.set = account, true
}

// orElse is the account the pull set, or, where none was, the one a wait in
// the given state means: nothing where every slot is taken, the hold where the
// session is braked, a rule where the machine or the provider refuses, and
// not known otherwise. said is what the session is saying about the wait.
func (a slotAccount) orElse(state runstate.WatchState, said string, provider bool) *runstate.EmptySlots {
	if a.set {
		return a.account
	}
	if a.free == 0 {
		return nil
	}
	cause := ownership.EmptySlotNotKnown
	reason := "the scheduler left the slot empty and could not name why"
	switch {
	case state == runstate.WatchBraked:
		cause, reason = ownership.EmptySlotHold, said
	case state == runstate.WatchBlocked || provider:
		cause, reason = ownership.EmptySlotRule, said
	case said != "":
		reason += "; the session said: " + said
	}
	return a.unread(cause, reason)
}

// unread is an account made before the queue was read, over the slots this
// pull counted free.
func (a slotAccount) unread(cause ownership.EmptySlotCause, reason string) *runstate.EmptySlots {
	account := &runstate.EmptySlots{Cause: cause, Reason: singleLine(reason, maxEmptySlotReasonBytes), ReadyUnread: true, Slots: a.slots}
	if a.free > 0 {
		account.FreeSlots = a.free
	}
	return account
}

// maxEmptySlotReasonBytes bounds the reason a pull writes, well inside what
// the log takes, so an account naming every item it may still fits one line.
const maxEmptySlotReasonBytes = 1024

// holdAccount is a hold on new work keeping the free slots empty.
func (a slotAccount) holdAccount(hold runstate.IntakeHold) *runstate.EmptySlots {
	return a.unread(ownership.EmptySlotHold, "new work is held, so nothing is started into a free slot: "+hold.Says())
}

// ruleAccount is a rule of the line's keeping the free slots empty before the
// queue was read: a restart onto a new build, the limit on pull requests
// waiting for a person.
func (a slotAccount) ruleAccount(reason string) *runstate.EmptySlots {
	return a.unread(ownership.EmptySlotRule, reason)
}

// countedAccount is a slot the pull found taken by a start of this session
// that has no run in flight on record, past the grace a dispatch is given to
// reserve one. It is nil where every slot is held by a run on record.
func countedAccount(occupied map[string]runstate.State, gone map[string]bool, mine map[string]int, started []Started, now time.Time) *runstate.EmptySlots {
	var ids []string
	for id, state := range occupied {
		index, ours := mine[id]
		if !ours || gone[id] || state.RunID != "" || index < 0 || index >= len(started) {
			continue
		}
		if began := started[index].began; began.IsZero() || now.Sub(began) < countedGrace {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	first := started[mine[ids[0]]]
	account := &runstate.EmptySlots{
		Cause: ownership.EmptySlotStillCounted,
		Reason: fmt.Sprintf("the slot is still counted as taken for %s, which this session started at %s and which has no run in flight on record",
			ids[0], first.began.Local().Format("15:04 MST")),
		ReadyUnread: true,
	}
	for _, id := range ids {
		if slot := started[mine[id]].Slot; slot > 0 {
			account.Slots = append(account.Slots, slot)
		}
		if len(account.CountedFor) < runstate.MaxEmptySlotItems {
			account.CountedFor = append(account.CountedFor, id)
		}
	}
	sort.Ints(account.Slots)
	if len(account.Slots) == 0 {
		account.FreeSlots = len(ids)
	}
	return account
}

// keptOutByRule is the classes a ready item is passed over in for a rule of
// the line's rather than for anything about the item: work overlapping a run
// in flight, an item already tried, the provider's limit, another slot, a
// directive pausing it.
func keptOutByRule(class runstate.PassedOverClass) bool {
	switch class {
	case runstate.PassedOverSequencedBehindWork, runstate.PassedOverAlreadyTried, runstate.PassedOverWaitingOnUsageWindow,
		runstate.PassedOverLeftForAnotherSlot, runstate.PassedOverPausedByDirective:
		return true
	}
	return false
}

// notWaiting is the classes of item that are not ready work waiting for a
// slot: one a run already carries, and one whose children carry it.
func notWaiting(class runstate.PassedOverClass) bool {
	return class == runstate.PassedOverAlreadyInFlight || class == runstate.PassedOverCoveredByChildren
}

// walkAccount is the account of a pull that walked the queue and started
// nothing into the slots left: the first ready item passed over decides the
// cause, a rule where a rule kept it out and no eligible item otherwise. why
// is the reason the pull kept for each item, where it kept one. It is nil
// where no ready item waited, and not known where the slots stand empty with
// ready work the walk never reached. Ready work a slot could take is the
// tracker's ready items that no run carries already.
func walkAccount(slots []int, queue backlog.Queue, passed []readmodel.PassedOverItem, why map[string]string, refused string, heldProvider domain.Backend) *runstate.EmptySlots {
	if len(slots) == 0 {
		return nil
	}
	account := &runstate.EmptySlots{Slots: append([]int(nil), slots...)}
	// Only ready work counts: an item the tracker does not offer — parked,
	// waiting on other work, held for a decision — is not work a slot could
	// have taken, and its own reason is kept on it elsewhere.
	ready := make(map[string]bool, len(queue.Entries))
	for _, entry := range queue.Entries {
		if entry.Ready {
			ready[entry.ID] = true
		}
	}
	for _, item := range passed {
		if notWaiting(item.Class) {
			delete(ready, item.ID)
		}
	}
	var first *readmodel.PassedOverItem
	for index := range passed {
		item := passed[index]
		if !ready[item.ID] {
			continue
		}
		account.Ready++
		if first == nil {
			first = &passed[index]
		}
		if len(account.Passed) < runstate.MaxEmptySlotItems {
			account.Passed = append(account.Passed, runstate.EmptySlotItem{WorkItemID: item.ID, Why: passedWhy(item, why)})
		}
	}
	switch {
	case refused != "":
		account.Cause, account.Reason = ownership.EmptySlotRule, "the machine refuses every start: "+refused
		if account.Ready == 0 {
			account.Ready = len(ready)
		}
		if account.Ready == 0 {
			return nil
		}
	case first == nil && len(ready) == 0:
		return nil
	case first == nil && heldProvider != "":
		account.Cause, account.Reason, account.Ready = ownership.EmptySlotRule,
			fmt.Sprintf("the free slots dispatch onto %s, whose launch settings are held until a check finds them active", heldProvider), len(ready)
	case first == nil:
		account.Cause, account.Reason, account.Ready = ownership.EmptySlotNotKnown,
			"the queue holds ready work and the scheduler passed none of it over, yet started nothing into the slot", len(ready)
	case keptOutByRule(first.Class):
		account.Cause, account.Reason = ownership.EmptySlotRule, fmt.Sprintf("a rule keeps %s out: %s", first.ID, passedWhy(*first, why))
	default:
		account.Cause, account.Reason = ownership.EmptySlotNothingEligible,
			fmt.Sprintf("no ready item could be started; the first, %s, was passed over as %s", first.ID, passedWhy(*first, why))
	}
	account.Reason = singleLine(account.Reason, maxEmptySlotReasonBytes)
	return account
}

// passedWhy is why one item was passed over: its class, and the reason the
// pull kept for it where it kept one.
func passedWhy(item readmodel.PassedOverItem, why map[string]string) string {
	reason := item.Reason
	if kept := why[item.ID]; kept != "" {
		reason = kept
	}
	said := string(item.Class)
	if reason != "" {
		said += ": " + reason
	}
	return singleLine(said, runstate.MaxEmptySlotWhyBytes)
}

// emptySlots writes the pull's account of its empty slots where it differs
// from the one the session last wrote, keeping the earlier one's Since where
// only the slots or the items changed under the same reason. A nil account
// ends a standing one with a cleared entry, written once.
func (w *watchSession) emptySlots(account *runstate.EmptySlots) {
	if w.to == nil {
		return
	}
	now := w.now()
	if account == nil {
		if w.slots == nil || w.slots.Cleared {
			return
		}
		account = &runstate.EmptySlots{Cleared: true}
	}
	next := *account
	if w.slots != nil && w.slots.SameReason(next) {
		return
	}
	next.At, next.Since = now, now
	if !next.Cleared && w.slots != nil && !w.slots.Cleared && w.slots.Cause == next.Cause && w.slots.Reason == next.Reason {
		next.Since = w.slots.Since
	}
	w.slots = &next
	recorded := next
	w.record(SessionState{State: runstate.WatchWatching, Reason: next.Says(), EmptySlots: &recorded})
}

// slotNumbers is the numbers of the slots given, in order.
func slotNumbers(slots []developerslot.Slot) []int {
	numbers := make([]int, 0, len(slots))
	for _, slot := range slots {
		numbers = append(numbers, slot.Number)
	}
	return numbers
}

// unfilledSlots is the numbers of the free slots a pull did not fill, in order.
func unfilledSlots(free []developerslot.Slot, filled map[int]bool) []int {
	var left []int
	for _, slot := range free {
		if !filled[slot.Number] {
			left = append(left, slot.Number)
		}
	}
	return left
}
