// Package developerslot is the one derivation of which developer slot each run
// in flight occupies, and which slots are free to pull.
//
// A developer slot is one unit of execution.max_concurrent_developers: the
// capacity one developer run takes. The slots are numbered from 1, and a project
// may say what each one prefers — a label whose work that slot pulls first, and
// the rest of the backlog only when none of it is ready. A run whose record
// names its slot (runstate.RunRouting) is in that slot and no other: its number
// is never re-derived from labels, start order, or a lowered capacity. For a
// run whose record names none, which slot it is "in" is a reading of what is in
// flight against what the slots prefer, made the same way every time. A run over
// labelled work sits in a slot that prefers its label while one is unassigned;
// everything else sits in a slot with no preference first, and in a preferring
// slot only once those are full, which is that slot having fallen back.
//
// It is derived here, once, because two things read it and must not disagree:
// the scheduler, which fills the free slots, and the standing status, which
// names the preferred label beside each slot. A slot the scheduler called free
// that the status called taken is exactly the disagreement an operator would
// then have to adjudicate, and reading both from one function is what makes it
// impossible.
package developerslot

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Run is one developer run in flight, as the assignment reads it: which run,
// over which item, carrying which labels, started when. The labels are the
// caller's to supply — the run's own record where it carries them, the tracker
// where it does not — because this package reads neither.
type Run struct {
	RunID      string
	WorkItemID string
	Labels     []string
	StartedAt  time.Time
	// Slot is the slot the run's record names, and zero where it names none.
	Slot int
}

// Slot is one developer slot as assigned: its number, what it prefers, and the
// run in it where one is.
type Slot struct {
	// Number is the slot's place in the configuration, counted from 1, which is
	// how an operator names it and how the configuration's list is read.
	Number int `json:"number"`
	// Preferred is the labels this slot pulls first, and empty for a slot with no
	// preference.
	Preferred []string `json:"preferred,omitempty"`
	// RunID and WorkItemID are the run in flight in this slot, and empty where
	// the slot is free.
	RunID      string `json:"run_id,omitempty"`
	WorkItemID string `json:"work_item_id,omitempty"`
}

// Free reports a slot with no run in it.
func (s Slot) Free() bool {
	return s.RunID == "" && s.WorkItemID == ""
}

// Preferring reports a slot that pulls labelled work first.
func (s Slot) Preferring() bool {
	return len(s.Preferred) > 0
}

// Prefers reports whether an item carrying these labels is what the slot pulls
// first. It is the configured preference's own answer.
func (s Slot) Prefers(labels []string) bool {
	return domain.DeveloperSlot{Prefer: s.Preferred}.Prefers(labels)
}

// Says names the slot the way a line reads it: "developer slot 2", with what it
// prefers where it prefers anything.
func (s Slot) Says() string {
	if !s.Preferring() {
		return fmt.Sprintf("developer slot %d", s.Number)
	}
	return fmt.Sprintf("developer slot %d (prefers %s)", s.Number, s.Preference())
}

// Preference is the labels the slot prefers, said as a phrase: "the dashboard
// label", or "the dashboard or reliability labels".
func (s Slot) Preference() string {
	switch len(s.Preferred) {
	case 0:
		return "no label"
	case 1:
		return "the " + s.Preferred[0] + " label"
	default:
		return "the " + strings.Join(s.Preferred[:len(s.Preferred)-1], ", ") + " or " + s.Preferred[len(s.Preferred)-1] + " labels"
	}
}

// Assignment is every configured slot with the run in it, and the runs in flight
// that no slot could hold.
type Assignment struct {
	Slots []Slot
	// Overflow is the runs in flight beyond the configured capacity: the
	// operator lowered it under runs already going, or two processes raced for
	// the last slot and both reserved. A run whose recorded slot lies beyond the
	// capacity, or is recorded by an earlier run too, is here as well, keeping
	// its number, rather than moved into a slot it was never given. They are
	// named rather than dropped, so a count of what is in flight and a count of
	// what the slots hold can be reconciled by whoever reads both.
	Overflow []Run
}

// Assign reads which slot each run in flight occupies. capacity is
// execution.max_concurrent_developers and preferences is execution.developer_slots
// as the caller read them; a preference list shorter than the capacity leaves the
// remaining slots preferring nothing, and one longer is cut to the capacity here
// because the configuration has already refused it.
//
// The reading is deterministic: runs are taken oldest first and by run id
// among equals. A run naming its slot is put in that slot first. Each of the
// rest is put in the first slot in configured order that holds it — a slot
// preferring one of its labels, then a slot preferring nothing, then any slot
// at all. Labelled runs are placed before unlabelled
// ones so that an unlabelled run started earlier never takes the preferring
// slot from the labelled run that slot was configured for.
func Assign(capacity int, preferences []domain.DeveloperSlot, inFlight []Run) Assignment {
	if capacity < 0 {
		capacity = 0
	}
	assignment := Assignment{Slots: make([]Slot, capacity)}
	for index := range assignment.Slots {
		assignment.Slots[index].Number = index + 1
		if index < len(preferences) {
			assignment.Slots[index].Preferred = append([]string(nil), preferences[index].Prefer...)
		}
	}
	runs := append([]Run(nil), inFlight...)
	sort.SliceStable(runs, func(first, second int) bool {
		if !runs[first].StartedAt.Equal(runs[second].StartedAt) {
			return runs[first].StartedAt.Before(runs[second].StartedAt)
		}
		return runs[first].RunID < runs[second].RunID
	})
	placed := make([]bool, len(runs))
	// Recorded slots first: they are identities, not a reading.
	for index, run := range runs {
		if run.Slot == 0 {
			continue
		}
		placed[index] = true
		if run.Slot <= capacity && assignment.Slots[run.Slot-1].Free() {
			assignment.Slots[run.Slot-1].take(run)
			continue
		}
		assignment.Overflow = append(assignment.Overflow, run)
	}
	// Labelled runs next, into the slots configured for their labels.
	for index, run := range runs {
		if placed[index] {
			continue
		}
		if slot := assignment.freeSlot(func(slot Slot) bool { return slot.Preferring() && slot.Prefers(run.Labels) }); slot != nil {
			slot.take(run)
			placed[index] = true
		}
	}
	// Then everything else: a slot with no preference first, and a preferring
	// slot only once those are full, which is that slot having fallen back.
	for index, run := range runs {
		if placed[index] {
			continue
		}
		slot := assignment.freeSlot(func(slot Slot) bool { return !slot.Preferring() })
		if slot == nil {
			slot = assignment.freeSlot(func(Slot) bool { return true })
		}
		if slot == nil {
			assignment.Overflow = append(assignment.Overflow, run)
			continue
		}
		slot.take(run)
	}
	return assignment
}

func (a *Assignment) freeSlot(suits func(Slot) bool) *Slot {
	for index := range a.Slots {
		if a.Slots[index].Free() && suits(a.Slots[index]) {
			return &a.Slots[index]
		}
	}
	return nil
}

func (s *Slot) take(run Run) {
	s.RunID, s.WorkItemID = run.RunID, run.WorkItemID
}

// Free is the slots with no run in them, in the order a pull fills them:
// the preferring slots first, in configured order, so that labelled work is
// pulled into the slot configured for it before a slot with no preference
// reaches it; then the rest, in configured order.
func (a Assignment) Free() []Slot {
	var free []Slot
	for _, slot := range a.Slots {
		if slot.Free() && slot.Preferring() {
			free = append(free, slot)
		}
	}
	for _, slot := range a.Slots {
		if slot.Free() && !slot.Preferring() {
			free = append(free, slot)
		}
	}
	return free
}

// Preferring reports whether any configured slot prefers a label, which is
// what decides whether a surface says anything about slots at all: a project
// that configured no preference reads exactly as it did before slots could
// prefer anything.
func Preferring(preferences []domain.DeveloperSlot) bool {
	for _, preference := range preferences {
		if preference.Preferring() {
			return true
		}
	}
	return false
}
