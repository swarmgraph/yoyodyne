package runstate

// Why a watch session left a developer slot empty while ready work waited, as
// the session itself recorded it.
//
// A session that leaves a slot empty is otherwise silent about it: its idle line
// says what the queue held, and nothing says which slot stood empty, since
// when, or what kept each ready item out of it. The scheduler writes one of
// these onto the watch log whenever its account of the empty slots changes,
// and a cleared one once no slot is empty while ready work waits, so a night of
// polls over one unchanging reason is one line. The read model reads the latest
// one back for the health check and `yoyo status` (readmodel.EmptySlots).

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// MaxEmptySlotItems is how many of the ready items passed over one account
// names. The rest are counted in Ready, not named.
const MaxEmptySlotItems = 5

// MaxEmptySlotWhyBytes bounds why one item was passed over, so an
// account naming every item it may still fits one watch log line.
const MaxEmptySlotWhyBytes = 512

// EmptySlotCause is ownership.EmptySlotCause, aliased here because the record
// carries it and the registry decides who moves each one.
type EmptySlotCause = ownership.EmptySlotCause

// EmptySlots is one account of the developer slots a poll left empty while
// ready work waited.
type EmptySlots struct {
	// Cause is which of the closed set of reasons this is, and Reason the same
	// in ordinary words, naming whatever the cause names: who placed the hold,
	// which item a rule kept out and why, which run a slot is still counted for.
	Cause  EmptySlotCause `json:"cause,omitempty"`
	Reason string         `json:"reason,omitempty"`
	// Slots is the numbers of the slots left empty, and empty where the poll
	// stopped before it worked out which slots were free; FreeSlots is how many
	// were free, and zero where it did not count them either.
	Slots     []int `json:"slots,omitempty"`
	FreeSlots int   `json:"free_slots,omitempty"`
	// Ready is how many ready items the poll left waiting, and ReadyUnread marks
	// a poll that stopped before it read the queue, so it could not count them.
	Ready       int  `json:"ready,omitempty"`
	ReadyUnread bool `json:"ready_unread,omitempty"`
	// Passed is the first ready items the poll passed over, in the order it
	// reached them, each with why.
	Passed []EmptySlotItem `json:"passed,omitempty"`
	// CountedFor is the items a slot still counted as taken is counted for,
	// where that is the cause, so a surface can name each by its title.
	CountedFor []string `json:"counted_for,omitempty"`
	// Since is when the session first recorded this reason, and At when it
	// recorded this entry: the same reason said again over a different set of
	// slots keeps its Since.
	Since time.Time `json:"since"`
	At    time.Time `json:"at"`
	// Cleared marks the account ending: the poll found no slot empty while
	// ready work waited. It carries nothing else.
	Cleared bool `json:"cleared,omitempty"`
}

// EmptySlotItem is one ready item a poll passed over, and why.
type EmptySlotItem struct {
	WorkItemID string `json:"work_item_id"`
	Why        string `json:"why"`
}

// SameReason reports two accounts giving the same reason over the same slots
// and the same items passed over, which is when a session writes nothing.
func (e EmptySlots) SameReason(other EmptySlots) bool {
	if e.Cleared != other.Cleared || e.Cause != other.Cause || e.Reason != other.Reason {
		return false
	}
	if len(e.Slots) != len(other.Slots) || len(e.Passed) != len(other.Passed) {
		return false
	}
	for index := range e.Slots {
		if e.Slots[index] != other.Slots[index] {
			return false
		}
	}
	for index := range e.Passed {
		if e.Passed[index] != other.Passed[index] {
			return false
		}
	}
	return true
}

// SlotsSaid is which slots stand empty, in words.
func (e EmptySlots) SlotsSaid() string {
	switch {
	case len(e.Slots) == 1:
		return fmt.Sprintf("developer slot %d", e.Slots[0])
	case len(e.Slots) > 1:
		numbers := make([]string, len(e.Slots))
		for index, slot := range e.Slots {
			numbers[index] = fmt.Sprint(slot)
		}
		return "developer slots " + strings.Join(numbers[:len(numbers)-1], ", ") + " and " + numbers[len(numbers)-1]
	case e.FreeSlots == 1:
		return "1 developer slot"
	case e.FreeSlots > 1:
		return fmt.Sprintf("%d developer slots", e.FreeSlots)
	}
	return "every free developer slot"
}

// Says is the account as one sentence.
func (e EmptySlots) Says() string {
	if e.Cleared {
		return "no developer slot is left empty while ready work waits"
	}
	waiting := "with ready work waiting"
	switch {
	case e.ReadyUnread:
		waiting = "without reading the queue"
	case e.Ready == 1:
		waiting = "with 1 ready item waiting"
	case e.Ready > 1:
		waiting = fmt.Sprintf("with %d ready items waiting", e.Ready)
	}
	because := "because of " + string(e.Cause)
	if e.Cause == ownership.EmptySlotNotKnown {
		because = "for a reason the scheduler could not name"
	}
	return fmt.Sprintf("%s left empty %s, %s: %s", e.SlotsSaid(), waiting, because, e.Reason)
}

func (e EmptySlots) validate() error {
	var problems []error
	if e.At.IsZero() {
		problems = append(problems, errors.New("an empty slot account records when it was taken"))
	}
	if e.Cleared {
		if e.Cause != "" || e.Reason != "" || len(e.Slots) > 0 || len(e.Passed) > 0 || len(e.CountedFor) > 0 {
			problems = append(problems, errors.New("a cleared empty slot account carries no reason, slot, or item"))
		}
		return errors.Join(problems...)
	}
	if !e.Cause.Valid() {
		problems = append(problems, fmt.Errorf("an empty slot account's cause %q is not one the scheduler names", e.Cause))
	}
	if strings.TrimSpace(e.Reason) == "" {
		problems = append(problems, errors.New("an empty slot account says its reason in words, and one it cannot name is said as not known"))
	}
	if len(e.Reason) > MaxWatchReasonBytes {
		problems = append(problems, fmt.Errorf("an empty slot account's reason is %d bytes, which exceeds the %d byte bound", len(e.Reason), MaxWatchReasonBytes))
	}
	if e.Since.IsZero() || e.Since.After(e.At) {
		problems = append(problems, errors.New("an empty slot account records since when its reason has stood, no later than the account"))
	}
	if e.Ready < 0 || e.FreeSlots < 0 {
		problems = append(problems, errors.New("an empty slot account's counts are not negative"))
	}
	for _, slot := range e.Slots {
		if slot < 1 {
			problems = append(problems, fmt.Errorf("an empty slot account names slot %d, which is not a slot", slot))
		}
	}
	if len(e.Passed) > MaxEmptySlotItems {
		problems = append(problems, fmt.Errorf("an empty slot account names %d items, past the %d it names", len(e.Passed), MaxEmptySlotItems))
	}
	if len(e.CountedFor) > MaxEmptySlotItems {
		problems = append(problems, fmt.Errorf("an empty slot account names %d items a slot is counted for, past the %d it names", len(e.CountedFor), MaxEmptySlotItems))
	}
	for _, id := range e.CountedFor {
		if err := domain.ValidateIdentifier("empty slot item counted for", id); err != nil {
			problems = append(problems, err)
		}
	}
	for _, item := range e.Passed {
		if err := domain.ValidateIdentifier("empty slot item passed over", item.WorkItemID); err != nil {
			problems = append(problems, err)
		}
		if strings.TrimSpace(item.Why) == "" {
			problems = append(problems, fmt.Errorf("an empty slot account names %s without saying why it was passed over", item.WorkItemID))
		}
		if len(item.Why) > MaxEmptySlotWhyBytes {
			problems = append(problems, fmt.Errorf("why %s was passed over is %d bytes, past the %d byte bound", item.WorkItemID, len(item.Why), MaxEmptySlotWhyBytes))
		}
	}
	return errors.Join(problems...)
}
