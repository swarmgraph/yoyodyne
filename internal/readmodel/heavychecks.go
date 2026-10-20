package readmodel

// A heavy check's place kept for processes nobody can confirm have gone.
//
// A place among the machine's heavy checks (execution.heavy_checks) is given
// back only once every process its check started is gone (runstate's
// heavycheck.go). A check that leaves one behind keeps its place, and every
// other run's check of the same name on the machine waits behind it, for as
// long as that process lasts. The record says so on the permit; this puts it on
// the attention line, naming the check, the run, the item, and why, read from
// the one record every product on the machine shares.

import (
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// HeavyCheckPlaces is the record of places for the machine's heavy checks. It
// is satisfied by *runstate.HeavyCheckStore.
type HeavyCheckPlaces interface {
	Record() (runstate.HeavyCheckRecord, error)
}

// KeptHeavyCheck is one place kept for a check's leftover processes.
type KeptHeavyCheck struct {
	Permit runstate.HeavyCheckPermit `json:"permit"`
}

// Since is when the check's main process ended, or, for a check whose harness
// stopped before writing that down, when it was given its place.
func (k KeptHeavyCheck) Since() time.Time {
	switch {
	case k.Permit.ExitedAt != nil:
		return *k.Permit.ExitedAt
	case k.Permit.GrantedAt != nil:
		return *k.Permit.GrantedAt
	}
	return k.Permit.AskedAt
}

// Says is the entry as a sentence: the check, whose run it is, since when, and
// why its place is kept.
func (k KeptHeavyCheck) Says() string {
	permit := k.Permit
	whose := "run " + permit.RunID
	if permit.WorkItemID != "" {
		whose += " over " + permit.WorkItemID
	}
	return fmt.Sprintf("the heavy check %s of %s (product %s) has kept its place since %s, because %s; every other run's %s on this machine waits behind it",
		permit.Check, whose, permit.Product, localMoment(k.Since()), singleLine(permit.Problem, maxRefusalBytes), permit.Check)
}

// ReadKeptHeavyChecks reads every place kept for leftover processes, oldest
// first. A reading with no record wired says nothing; a record that cannot be
// read says so rather than reporting every place free.
func ReadKeptHeavyChecks(sources Sources) ([]KeptHeavyCheck, string) {
	if sources.HeavyChecks == nil {
		return nil, ""
	}
	record, err := sources.HeavyChecks.Record()
	if err != nil {
		return nil, fmt.Sprintf("the record of places for the machine's heavy checks could not be read: %v", err)
	}
	var kept []KeptHeavyCheck
	for _, permit := range record.Stuck() {
		kept = append(kept, KeptHeavyCheck{Permit: permit})
	}
	sort.SliceStable(kept, func(first, second int) bool { return kept[first].Since().Before(kept[second].Since()) })
	return kept, ""
}

// keptHeavyCheckAttention is a kept place, as the attention line carries it.
// It is the development manager's: whether the leftover process is a hang to
// clear by hand or something to let finish is a judgement about run
// reliability, which is hers, and releasing the place is a command she has.
// Nothing about it is on the closed list of what only a person can do. The
// permit is the ID.
func keptHeavyCheckAttention(kept KeptHeavyCheck) Attention {
	return resolved(Attention{
		Kind:           AttentionKeptHeavyCheck,
		ID:             kept.Permit.ID,
		WorkItemID:     kept.Permit.WorkItemID,
		KeptHeavyCheck: &kept,
	})
}
