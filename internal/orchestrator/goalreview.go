package orchestrator

// The daily unmet-goal review (docs/designs/unmet-goal-review.md, sections 1
// and 5).
//
// Once a day, at the first pull from 09:00 on the operator's local clock,
// whether or not any developer is idle, the harness reads every goal and the
// work serving it and records a review. For each goal it works out a
// fingerprint of what it read (internal/goalreview) and compares it with the
// one recorded by the last review that went through that goal and completed.
//
// A goal whose fingerprint is unchanged is said in one line, with the time of
// the review that went through it, and no provider call is spent on it. Where
// at least one goal changed, the review is delivered as a pass of the Lead
// Product Manager's task, out of its cadence as a critical report is: the
// goals to go through, the unchanged ones in a line each, and the
// items the operator raised on purpose apart from both. Her ordinary pass's
// work comes with it, so the review takes the place of one of her passes rather
// than adding one. Where every goal is unchanged, nobody is woken: the review
// is still recorded, as the list of unchanged goals and the raised items.
//
// The review is recorded as it starts, and that record is what makes it once
// a day across every process. Only a pass that completes records the goals it
// went through as gone through; one that did not leaves them changed, so the
// next day's review goes through them again rather than calling them
// unchanged since a review nobody finished.
//
// The architect's pass going first, and what each role does with the goals,
// are piece 3 of the design and not built here.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goalreview"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// GoalReviewSource reads what the review goes through: the goals in force and
// the work items, each with the goal it serves. It is satisfied in the CLI
// over the goals documents and the tracker.
type GoalReviewSource interface {
	Read(ctx context.Context) (goalreview.Reading, error)
}

// GoalReviewRecord is where each review is recorded. It is satisfied by
// *runstate.GoalReviewStore.
type GoalReviewRecord interface {
	Load() (runstate.GoalReviews, bool, error)
	Begin(ctx context.Context, review runstate.GoalReview, goals []string) (runstate.GoalReviews, error)
	Finish(ctx context.Context, startedAt time.Time, completed bool, problem string, at time.Time) (runstate.GoalReviews, error)
}

// goalReviewSummons is what a review's pass records as having brought it out
// of its cadence.
const goalReviewSummons = "the daily review of unmet goals"

// goalReviewWired reports a trigger that runs the daily review, and the Lead
// Product Manager's task it is delivered through.
func (t Trigger) goalReviewWired() (string, config.RecurringTask, bool) {
	if t.GoalReview == nil || t.GoalReviews == nil {
		return "", config.RecurringTask{}, false
	}
	return t.productManagerTask()
}

// goalReviewOwed reports whether today's daily review is still to be recorded,
// and since when it has been due. It reads only the record, so a pull on which
// nothing is owed reads neither the goals nor the tracker.
func (t Trigger) goalReviewOwed(now time.Time) (time.Time, bool, error) {
	due := goalreview.DailyStart(now, t.GoalReviewZone)
	record, _, err := t.GoalReviews.Load()
	if err != nil {
		return due, false, fmt.Errorf("read the record of the daily goal reviews, so today's review waits until it can be read: %w", err)
	}
	_, done := record.Daily(goalreview.Day(due, t.GoalReviewZone))
	return due, !done, nil
}

// goalReview records today's review and, where any goal changed since a
// review last went through it, claims the Lead Product Manager's pass that
// goes through them.
func (t Trigger) goalReview(ctx context.Context, name string, task config.RecurringTask, due time.Time) (claimedFiring, bool, error) {
	reading, err := t.GoalReview.Read(ctx)
	if err != nil {
		return claimedFiring{}, false, fmt.Errorf("read the goals and the work serving them for the daily review, so it waits for a pull that can: %w", err)
	}
	record, _, err := t.GoalReviews.Load()
	if err != nil {
		return claimedFiring{}, false, fmt.Errorf("read the record of the daily goal reviews: %w", err)
	}
	now := t.now()
	prior := make(map[string]goalreview.Prior, len(record.Goals))
	for goal, reviewed := range record.Goals {
		prior[goal] = goalreview.Prior{Fingerprint: reviewed.Fingerprint, ReviewedAt: reviewed.ReviewedAt}
	}
	outcomes := goalreview.Compare(reading, prior, now)
	raised := goalreview.Raised(reading.Items)
	report := goalreview.Report(outcomes, raised, t.GoalReviewZone)

	review := runstate.GoalReview{
		Trigger:   runstate.GoalReviewDaily,
		Day:       goalreview.Day(due, t.GoalReviewZone),
		StartedAt: now,
		Report:    report,
	}
	goals := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		goals = append(goals, outcome.Goal.Reference)
		entry := runstate.GoalReviewEntry{Goal: outcome.Goal.Reference, Fingerprint: outcome.Fingerprint}
		if outcome.Unchanged {
			entry.Since = outcome.Since
			review.Unchanged = append(review.Unchanged, entry)
			continue
		}
		review.GoneThrough = append(review.GoneThrough, entry)
	}
	for _, item := range raised {
		review.Raised = append(review.Raised, item.ID)
	}
	write, stopWriting := recordContext(ctx)
	_, err = t.GoalReviews.Begin(write, review, goals)
	stopWriting()
	if errors.Is(err, runstate.ErrGoalReviewRecorded) {
		return claimedFiring{}, false, nil
	}
	if err != nil {
		return claimedFiring{}, false, fmt.Errorf("record the daily review of unmet goals, so it is not started until it can be: %w", err)
	}
	// Every goal unchanged: the review is recorded and nobody is woken.
	if len(review.GoneThrough) == 0 {
		return claimedFiring{}, false, nil
	}

	claimed, err := t.Claims.Summon(ctx, name, now)
	if err != nil {
		problem := fmt.Sprintf("the Lead Product Manager's pass for it could not be claimed: %v", err)
		write, stopWriting := recordContext(ctx)
		_, finishErr := t.GoalReviews.Finish(write, now, false, problem, now)
		stopWriting()
		return claimedFiring{}, false, errors.Join(fmt.Errorf("claim the firing of the recurring task %s for the daily review of unmet goals: %w", name, err), finishErr)
	}
	return claimedFiring{take: func(ctx context.Context) Fired {
		// What her ordinary pass is put comes with the review: see claimTask.
		batch := t.amendmentBatch(task)
		missing := t.reportMissingCapabilities(ctx, task.Role)
		addressed := t.addressedTo(task)
		return t.run(ctx, firing{
			name:      name,
			pass:      passName(claimed),
			task:      task,
			trigger:   runstate.PassTriggerSchedule,
			summoned:  goalReviewSummons,
			message:   goalReviewMessage(review, len(outcomes)) + "\n\n" + wakeMessage(name, task, "", t.overdueFor(task), addressed, batch) + missingCapabilitySection(missing),
			batch:     batch,
			addressed: addressed,
			due:       due,
			finish: func(answered bool) string {
				problem := ""
				if !answered {
					problem = "the pass did not complete"
				}
				write, stopWriting := recordContext(ctx)
				defer stopWriting()
				if _, err := t.GoalReviews.Finish(write, now, answered, problem, t.now()); err != nil {
					return fmt.Sprintf("what became of the daily review of unmet goals could not be recorded, so its goals are gone through again at the next review: %v", err)
				}
				if !answered {
					return fmt.Sprintf("the daily review's pass did not complete, so the %d goal(s) it was to go through are gone through again at the next review", len(review.GoneThrough))
				}
				return ""
			},
		})
	}}, true, nil
}

// goalReviewMessage is what the review puts in front of the Lead Product
// Manager's pass, ahead of her ordinary wake.
func goalReviewMessage(review runstate.GoalReview, goals int) string {
	lines := []string{
		"# Daily review of unmet goals",
		"",
		fmt.Sprintf("This pass is also today's review of the goals (docs/designs/unmet-goal-review.md). Of %d goal(s), %d changed since a review last went through them, or were never gone through, and are listed first; the rest are unchanged since the review named and are not to be gone through again.", goals, len(review.GoneThrough)),
		"For each goal to go through, say in a sentence how far it is from met, counting work that is switched off, running in a dry run, or landed only on a branch other than the target as not met. Name the admitted work that would move it most, and whether it can start now. Leave the items the operator raised on purpose where he put them.",
		"",
		strings.TrimSpace(review.Report),
	}
	return strings.Join(lines, "\n")
}
