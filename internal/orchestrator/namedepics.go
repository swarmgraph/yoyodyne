package orchestrator

// Asking the one decision on what may start (internal/maystart) beside the
// checks each path already makes. It replaces none of them: a yes lifts no hold.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// StartDecision asks maystart.Decide for one product. Every command wires it
// (cli's components.mayStart). Nil is a caller that names no epic, such as a
// test, and refuses nothing.
type StartDecision func(maystart.Start) maystart.Decision

// refuseRun is why a run of the item may not start by this path, and nil where
// it may.
func (d StartDecision) refuseRun(path maystart.Path, workItemID string) *OutsideNamedEpicsError {
	if d == nil {
		return nil
	}
	decision := d(maystart.Run(path, workItemID))
	if decision.MayStart() {
		return nil
	}
	return &OutsideNamedEpicsError{WorkItemID: workItemID, Decision: decision}
}

// OutsideNamedEpicsError is a start declined because the factory works only on
// the epics the operator named and the item is in none of them, or because the
// record of them could not be read. Nothing was reserved, claimed or spent.
type OutsideNamedEpicsError struct {
	WorkItemID string            `json:"work_item_id"`
	Decision   maystart.Decision `json:"decision"`
}

func (e *OutsideNamedEpicsError) Error() string {
	return fmt.Sprintf("%s was not started: %s; it starts after %s", e.WorkItemID, e.Decision.Why, e.Clears())
}

// Clears is what lets the same start go ahead, with the commands that do it.
func (e *OutsideNamedEpicsError) Clears() string {
	if e.Decision.Answer != maystart.MayNot {
		return "the record of the named epics can be read again"
	}
	var ways []string
	for _, epic := range e.Decision.Open {
		if epic != e.WorkItemID { // an epic is not one of its own members
			ways = append(ways, fmt.Sprintf("`yoyo epic add %s %s`", epic, e.WorkItemID))
		}
	}
	if len(ways) == 0 {
		return "the operator names an epic holding it with `yoyo epic start`, or runs `yoyo epic off`"
	}
	return "the operator adds it with " + strings.Join(ways, " or ") + ", or runs `yoyo epic off`"
}

// startPath is the path this pipeline's own start is on, read from who chose
// the work. Every path that begins a run is judged by the same rule, so this
// says which path it is and never what the answer is.
func (p Pipeline) startPath() maystart.Path {
	switch strings.TrimSpace(p.Selection.By) {
	case runstate.SelectedByOperator:
		return maystart.NamedRun
	case runstate.SelectedByBrake:
		return maystart.HoldTest
	case runstate.SelectedByDevelopmentManager:
		if runstate.ChosenForFeedbackRepair(p.Selection.Reason) {
			return maystart.FeedbackRepair
		}
		return maystart.DecidedRerun
	default:
		return maystart.ReadyWork
	}
}
