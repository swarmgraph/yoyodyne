package orchestrator

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ScheduleEpicJoins judges the pieces that may join an epic the operator named
// (internal/epicjoin). Owed reads the records and calls no provider. Judge makes
// the judgments that are owed and answers what they cost.
type ScheduleEpicJoins interface {
	Owed(ctx context.Context) (bool, error)
	Judge(ctx context.Context) (float64, error)
}

// epicJoinsTask names the judging, and is the conversation it holds: the judging
// session belongs to none, so it keeps no role's pass waiting.
const epicJoinsTask = "the judgment of pieces that may join a named epic"

// judgeEpicPieces makes the judgments that are owed, beside the pull, one round at
// a time. It stops nothing: one that could not be made is said and tried again.
func (s Scheduler) judgeEpicPieces(ctx context.Context, schedule *Schedule, pull Pull, firings *recurringFirings, starting bool) {
	if pull.EpicJoins == nil || firings == nil || !starting || firings.taking(epicJoinsTask) {
		return
	}
	owed, err := pull.EpicJoins.Owed(ctx)
	schedule.epicJoined(err)
	if err != nil || !owed {
		return
	}
	firings.launchTurn(StartedFiring{Task: epicJoinsTask, Role: domain.RoleReviewer, Conversation: epicJoinsTask},
		func(ctx context.Context) func(*Schedule) {
			cost, err := pull.EpicJoins.Judge(ctx)
			return func(schedule *Schedule) {
				schedule.SpentUSD += cost
				schedule.epicJoined(err)
			}
		}, s.now)
}

func (s *Schedule) epicJoined(err error) {
	s.EpicJoinProblem = ""
	if err != nil {
		s.EpicJoinProblem = fmt.Sprintf("a piece that may join a named epic could not be judged, so it has not started: %v", err)
	}
}
