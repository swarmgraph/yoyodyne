package runstate

import (
	"fmt"
	"time"
)

// TeamMergeAwaited records that a run's landed change waits for the team's
// merge, where a sweep or the merge queue settled the item after the run had
// ended. It is a narrow update, so it keeps whatever else is on the run as
// stored. A run that already records a wait keeps the earlier one, so recording
// it twice records it once.
type TeamMergeAwaited struct {
	Wait TeamMerge
	At   time.Time
}

func (u TeamMergeAwaited) apply(state *State) (bool, error) {
	if state.TeamMerge != nil {
		return false, nil
	}
	if err := u.Wait.Validate(); err != nil {
		return false, fmt.Errorf("run %s cannot record waiting for the team's merge: %w", state.RunID, err)
	}
	wait := u.Wait
	state.TeamMerge = &wait
	state.UpdatedAt = u.At
	return true, nil
}
