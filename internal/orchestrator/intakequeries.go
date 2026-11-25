package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/maystart"
)

// ScheduleIntake is the project's scheduled intake queries, which a watching
// session runs beside its pulls (docs/designs/scheduled-message-intake.md,
// section 1). Due names every query whose run for the current window of its
// schedule has not started, and Run runs one; both are the intake package's
// (internal/intake), adapted where the session is wired. A run records itself
// before it spends anything, so a run killed part-way is still the one run of
// its window.
type ScheduleIntake interface {
	Due(now time.Time) ([]string, error)
	Run(ctx context.Context, query string) error
}

// intakeRuns is the intake query runs a session has in flight, each in a
// goroutine of its own, collected at the top of every pull and waited out when
// the session ends. A query with a run in flight is not started again: a run
// that falls due while the previous one still works is skipped, and the next
// window's run carries on from where the working one stops.
type intakeRuns struct {
	inFlight map[string]bool
	done     chan intakeRunDone
	ctx      context.Context
	cancel   context.CancelFunc
	// mayStart is the decision on what may start, as the last pull wired it.
	mayStart StartDecision
}

type intakeRunDone struct {
	query string
	err   error
}

func newIntakeRuns(ctx context.Context) *intakeRuns {
	runCtx, cancel := context.WithCancel(ctx)
	return &intakeRuns{inFlight: map[string]bool{}, done: make(chan intakeRunDone, 16), ctx: runCtx, cancel: cancel}
}

// startIntakeRuns starts a run of every query that is due and has none in
// flight.
func (s Scheduler) startIntakeRuns(schedule *Schedule, intake ScheduleIntake, runs *intakeRuns) {
	due, err := intake.Due(s.now())
	if err != nil {
		schedule.IntakeProblem = fmt.Sprintf("the intake record could not be read, so no query runs until it can: %v", err)
		return
	}
	for _, query := range due {
		if runs.inFlight[query] {
			continue
		}
		// Asked before the reader runs, so a refusal reads no message and moves
		// no query's time. It is the same answer for every query.
		if !runs.mayStart.allows(maystart.MessageReading()) {
			return
		}
		runs.inFlight[query] = true
		schedule.IntakeRuns++
		go func(query string) {
			runs.done <- intakeRunDone{query: query, err: intake.Run(runs.ctx, query)}
		}(query)
	}
}

// collectIntakeRuns takes every run that has ended, and with block waits for
// every one still in flight.
func collectIntakeRuns(schedule *Schedule, runs *intakeRuns, block bool) {
	for len(runs.inFlight) > 0 {
		var done intakeRunDone
		if block {
			done = <-runs.done
		} else {
			select {
			case done = <-runs.done:
			default:
				return
			}
		}
		delete(runs.inFlight, done.query)
		schedule.IntakeProblem = ""
		if done.err != nil {
			schedule.IntakeProblem = fmt.Sprintf("intake query %s: %v", done.query, done.err)
		}
	}
}
