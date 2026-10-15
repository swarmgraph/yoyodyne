package chat

// Refusing a link or a reparent that would make an item wait on its own work.
//
// The tracker holds every item under a waiting parent back with it, so a parent
// linked to wait on something broken out of it — a design an implementation
// needs, created under that implementation, or a closing measure linked from the
// item that coordinates it — waits on work that is held back until it stops
// waiting. Neither is ever offered, and every sibling the held work waits on is
// held the same way. The tracker accepts both writes, and nothing on any one
// item's own links says why the work stopped, so the arrangement is refused here,
// before it is written, by the one walk beads.FindCircularWait makes.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// circularWaitRefusal is why the write would leave id waiting on its own work,
// and empty where it would not. first is what the write gives id: a link
// waiting on an item, or a new parent. A tracker that cannot be read well
// enough to settle it refuses the write as well, saying so, because a loop
// written on a guess is the outcome this exists to prevent and the role can ask
// again.
func (s *Session) circularWaitRefusal(ctx context.Context, id string, first beads.WaitStep) string {
	wait, err := beads.FindCircularWait(id, first, func(other string) (beads.WorkItem, bool, error) {
		item, err := s.options.Tracker.Show(ctx, other)
		if err != nil {
			return beads.WorkItem{}, false, err
		}
		return item, true, nil
	})
	if err != nil {
		return fmt.Sprintf("whether this would leave %s waiting on its own work could not be read, so nothing was changed: %s",
			id, singleLine(err.Error(), maxTrackerFailureBytes))
	}
	if wait == nil {
		return ""
	}
	return fmt.Sprintf("refused, and nothing was changed: %s. A prerequisite an item waits on goes beside it, under the same parent, "+
		"rather than under it; place it there and link that, or link the work that actually needs it",
		strings.TrimSuffix(wait.Describe(), "."))
}
