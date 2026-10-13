package domain

// The order heavy checks are let start in.
//
// A heavy check is a configured check the project has said only so many of may
// run at once on one machine (execution.heavy_checks). A check waiting for a
// place is let start in the order these name, and in the order it asked among
// equals. It is defined here rather than beside the permits because two
// packages hold it — the check runner, which asks for a place, and the record
// of places, which grants them — and neither may import the other.

// HeavyCheckPriority is which of the three orders a check waiting for a place
// is let start in.
type HeavyCheckPriority string

const (
	// HeavyCheckForLanding is a check run for a landing or a promotion: the
	// landing checks over an integrated commit, a merge queue candidate checked
	// locally, or a replay onto a target branch that moved. Every run after it
	// is cut from what it vouches for, so it goes first.
	HeavyCheckForLanding HeavyCheckPriority = "landing"
	// HeavyCheckForPreservedWork is a check run for a decided repair or a re-run
	// holding work an earlier attempt preserved.
	HeavyCheckForPreservedWork HeavyCheckPriority = "preserved"
	// HeavyCheckOrdinary is everything else.
	HeavyCheckOrdinary HeavyCheckPriority = "ordinary"
)

// HeavyCheckPriorities is every priority, first-served first.
func HeavyCheckPriorities() []HeavyCheckPriority {
	return []HeavyCheckPriority{HeavyCheckForLanding, HeavyCheckForPreservedWork, HeavyCheckOrdinary}
}

// Valid reports one of the three priorities.
func (p HeavyCheckPriority) Valid() bool {
	return p.Rank() >= 0
}

// Rank is the priority's place in the order, from 0 for the first served, and
// -1 for anything that is not a priority.
func (p HeavyCheckPriority) Rank() int {
	for rank, priority := range HeavyCheckPriorities() {
		if p == priority {
			return rank
		}
	}
	return -1
}
