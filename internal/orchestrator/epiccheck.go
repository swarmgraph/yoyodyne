package orchestrator

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ScheduleEpicChecks makes the check of the whole epic (internal/epiccheck) for
// each named epic whose members are all done. Owed reads the records and calls no
// provider. Check makes the checks that are owed, or finishes one begun, and
// answers what they cost.
type ScheduleEpicChecks interface {
	Owed(ctx context.Context) (bool, error)
	Check(ctx context.Context) (float64, error)
}

// epicChecksTask names the checking, and is the conversation it holds: the
// checking session belongs to none, so it keeps no role's pass waiting.
const epicChecksTask = "the check of the whole of a named epic"

// checkWholeEpics makes the checks that are owed, beside the pull, one round at a
// time. It stops nothing: one that could not be made is said and tried again.
func (s Scheduler) checkWholeEpics(ctx context.Context, schedule *Schedule, pull Pull, firings *recurringFirings, starting bool) {
	if pull.EpicChecks == nil || firings == nil || !starting || firings.taking(epicChecksTask) {
		return
	}
	owed, err := pull.EpicChecks.Owed(ctx)
	schedule.epicChecked(err)
	if err != nil || !owed {
		return
	}
	firings.launchTurn(StartedFiring{Task: epicChecksTask, Role: domain.RoleReviewer, Conversation: epicChecksTask},
		func(ctx context.Context) func(*Schedule) {
			cost, err := pull.EpicChecks.Check(ctx)
			return func(schedule *Schedule) {
				schedule.SpentUSD += cost
				schedule.epicChecked(err)
			}
		}, s.now)
}

func (s *Schedule) epicChecked(err error) {
	s.EpicCheckProblem = ""
	if err != nil {
		s.EpicCheckProblem = fmt.Sprintf("the check of the whole of a named epic could not be made, so the epic is not delivered yet: %v", err)
	}
}
