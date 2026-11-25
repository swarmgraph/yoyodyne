package runstate

import (
	"fmt"
	"strings"
)

// CancelledHoldingItem reports a run that ended cancelled while it held its
// work item and that the development manager has to decide about. The docket
// and the hold the pull and the health check read both ask it, so the two name
// the same runs. kept is whether the run's branch or worktree may still be
// there.
//
// It leaves out a run that never claimed its item, one whose item another run's
// merge completed, one whose own change landed, one that carries a blocker,
// which is a stoppage already, and one the development manager stopped herself,
// which is docketed as decided. It also leaves out a run the claim audit ended
// with nothing kept: the audit gave that item back to be started again.
func (s State) CancelledHoldingItem(kept bool) bool {
	return s.Status == StatusCancelled && s.holdsItemIfCancelled(s.StopClass, kept)
}

// holdsItemIfCancelled is CancelledHoldingItem without the status, for a run
// cancelled under the stop class given.
func (s State) holdsItemIfCancelled(class StopClass, kept bool) bool {
	return s.WorkItemID != "" && s.WorkItemClaimedAt != nil && !s.HarnessAuthored() &&
		s.Retirement == nil && s.Integration == nil &&
		strings.TrimSpace(s.Blocker) == "" &&
		class != StopManager &&
		(kept || class != StopDeadClaim)
}

// HoldsItemOnceDeadClaim reports a run the claim audit has just ended being one
// CancelledHoldingItem names, asked of the record as it read before the ending.
func (s State) HoldsItemOnceDeadClaim(kept bool) bool {
	return s.holdsItemIfCancelled(StopDeadClaim, kept)
}

// CancelledSays is how a cancelled run ended and who or what cancelled it.
// requestedBy is who the recorded stop request names, and empty where no
// request is recorded.
func (s State) CancelledSays(requestedBy string) string {
	step := strings.TrimSpace(string(s.Phase))
	if step == "" {
		step = "unrecorded"
	}
	ended := fmt.Sprintf("the run ended cancelled at its %s step", step)
	if s.CompletedAt != nil {
		ended += " at " + s.CompletedAt.Local().Format("2006-01-02 15:04:05 MST")
	}
	return ended + "; it was cancelled by " + s.cancelledBy(strings.TrimSpace(requestedBy))
}

func (s State) cancelledBy(requestedBy string) string {
	switch s.StopClass {
	case StopOperator:
		if requestedBy == "" {
			return "the operator"
		}
		return requestedBy
	case StopRedeploy:
		return "the harness, restarting into a new build"
	case StopDeadClaim:
		return "the harness, because the process carrying the run was gone and did not come back"
	case CauseUsageWindow.StopClass():
		return "the harness, because the AI provider's usage limit resets later than the harness will wait"
	case StopCancelled:
		return "the process it ran in being told to stop; nothing recorded who asked"
	case "", StopUnknown:
		return "something its record does not name"
	}
	return fmt.Sprintf("the harness (recorded cause: %s)", StopCauseShown(s.StopClass))
}
