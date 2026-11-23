package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goalreview"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// reviewZone is the operator's zone the tests read 09:00 in.
var reviewZone = time.FixedZone("PST", -8*60*60)

// readGoals is the goals and work a test hands the review.
type readGoals struct {
	reading goalreview.Reading
	reads   int
}

func (r *readGoals) Read(context.Context) (goalreview.Reading, error) {
	r.reads++
	return r.reading, nil
}

// dailyReview is the trigger with the Lead Product Manager's task and the
// daily review wired, over one state root.
type dailyReview struct {
	trigger Trigger
	sweeps  *runstate.SweepStore
	reviews *runstate.GoalReviewStore
	source  *readGoals
	role    *wokenRole
	clock   *movingRecurringClock
}

func newDailyReview(t *testing.T) *dailyReview {
	t.Helper()
	root := t.TempDir()
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	reviews, err := runstate.NewGoalReviewStore(root, "example")
	if err != nil {
		t.Fatalf("NewGoalReviewStore() error = %v", err)
	}
	// Her own task fired a moment before the first pull and is not due again in
	// any test, so whatever wakes her is the review.
	const every = 30 * 24 * time.Hour
	if _, err := sweeps.Claim(context.Background(), "lead-pm", every, novemberDay(20, 8, 0)); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	r := &dailyReview{sweeps: sweeps, reviews: reviews, role: &wokenRole{}, clock: &movingRecurringClock{}}
	r.source = &readGoals{reading: goalreview.Reading{
		Goals: []goalreview.Goal{
			{Reference: "nearly-autonomous", Statement: "[nearly-autonomous] Run development nearly autonomously."},
			{Reference: "plain-language", Statement: "[plain-language] The harness's surfaces read clearly."},
		},
		Items: []goalreview.Item{
			{ID: "yoyodyne-1", Title: "Start the review", Status: "open", Priority: 1, Serves: "nearly-autonomous"},
			{ID: "yoyodyne-2", Title: "Reword the status line", Status: "open", Priority: 2, Serves: "plain-language"},
			{ID: "yoyodyne-3", Title: "Speed up the MCP pieces", Status: "open", Priority: 1, Labels: []string{domain.RaisedByOperatorLabel}},
		},
	}}
	r.trigger = Trigger{
		Tasks: map[string]config.RecurringTask{
			"lead-pm": {Role: domain.RoleProductManager, Every: config.Duration(every), Enabled: true, Prompt: "work your queue", MaxTurns: 1},
		},
		Claims:         sweeps,
		Reports:        sweeps,
		Roles:          r.role,
		GoalReview:     r.source,
		GoalReviews:    reviews,
		GoalReviewZone: reviewZone,
		Clock:          r.clock,
	}
	return r
}

// novemberDay is a moment on November dayOfMonth, 2026, on the operator's clock.
func novemberDay(dayOfMonth, hour, minute int) time.Time {
	return time.Date(2026, time.November, dayOfMonth, hour, minute, 0, 0, reviewZone)
}

// pull takes one pull at the moment given, with the Lead Product Manager
// answering each turn with a complete account.
func (r *dailyReview) pull(t *testing.T, at time.Time) RecurringSweep {
	t.Helper()
	r.clock.now = at.UTC()
	r.role.answers = append(r.role.answers, scriptedTurn{result: complete("went through the goals")})
	fired, err := r.trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() at %s error = %v", at.Format(time.RFC3339), err)
	}
	r.role.answers = nil
	return fired
}

func (r *dailyReview) last(t *testing.T) runstate.GoalReview {
	t.Helper()
	record, _, err := r.reviews.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	review, found := record.Last()
	if !found {
		t.Fatal("no review is recorded")
	}
	return review
}

// The review runs once a day from 09:00 on the operator's clock: nothing
// before it, one pass of the Lead Product Manager's at the first pull after
// it with every goal never gone through listed in full, and nothing more that
// day. The trigger is told nothing about idle developers, so it runs whether or
// not any are.
func TestTheDailyReviewRunsOnceADayFromNineLocalTime(t *testing.T) {
	t.Parallel()

	r := newDailyReview(t)
	// The 19th's review is already recorded, so before 09:00 on the 20th
	// nothing is owed.
	seed := runstate.GoalReview{Trigger: runstate.GoalReviewDaily, Day: "2026-11-19", StartedAt: novemberDay(19, 9, 0)}
	if _, err := r.reviews.Begin(context.Background(), seed, nil); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if fired := r.pull(t, novemberDay(20, 8, 59)); len(fired.Fired) != 0 || r.source.reads != 0 {
		t.Fatalf("fired at 08:59 = %+v, reads = %d, want nothing and no reading before 09:00", fired.Fired, r.source.reads)
	}

	fired := r.pull(t, novemberDay(20, 9, 5))
	if len(fired.Fired) != 1 || fired.Fired[0].Task != "lead-pm" || fired.Fired[0].Summoned != goalReviewSummons {
		t.Fatalf("fired at 09:05 = %+v, want one pass of the Lead Product Manager's task for the review", fired.Fired)
	}
	message := r.role.messages[len(r.role.messages)-1]
	for _, want := range []string{"# Daily review of unmet goals", "Goals to go through (2)", "- [nearly-autonomous] Run development nearly autonomously.", "  yoyodyne-1 (P1, open): Start the review", "Items the operator raised on purpose (1)", "- yoyodyne-3 (P1, open): Speed up the MCP pieces", "work your queue"} {
		if !strings.Contains(message, want) {
			t.Errorf("message lacks %q:\n%s", want, message)
		}
	}
	review := r.last(t)
	if review.Day != "2026-11-20" || review.Outcome != runstate.GoalReviewCompleted || len(review.GoneThrough) != 2 || len(review.Raised) != 1 {
		t.Fatalf("review = %+v, want the 20th's review completed over both goals with the raised item", review)
	}

	turns := len(r.role.messages)
	if fired := r.pull(t, novemberDay(20, 15, 0)); len(fired.Fired) != 0 || len(r.role.messages) != turns {
		t.Fatalf("fired again on the same day = %+v, want nothing", fired.Fired)
	}
}

// A goal whose fingerprint is unchanged since the last review is one line with
// the time of that review and is not gone through again; a day on which every
// goal is unchanged still records a review, of the unchanged goals and the
// raised items, and wakes nobody.
func TestAnUnchangedGoalIsOneLineAndAnUnchangedDayWakesNobody(t *testing.T) {
	t.Parallel()

	r := newDailyReview(t)
	r.pull(t, novemberDay(20, 9, 5))
	turns := len(r.role.messages)

	// Nothing changed: recorded, nobody woken.
	if fired := r.pull(t, novemberDay(21, 9, 1)); len(fired.Fired) != 0 || len(r.role.messages) != turns {
		t.Fatalf("fired on an unchanged day = %+v, want no pass and no provider call", fired.Fired)
	}
	review := r.last(t)
	if review.Day != "2026-11-21" || review.Outcome != runstate.GoalReviewNothingChanged || len(review.GoneThrough) != 0 || len(review.Unchanged) != 2 {
		t.Fatalf("review = %+v, want the 21st recorded with both goals unchanged", review)
	}
	if len(review.Raised) != 1 || review.Raised[0] != "yoyodyne-3" {
		t.Errorf("raised = %v, want the operator's item listed", review.Raised)
	}
	for _, want := range []string{"- [plain-language] The harness's surfaces read clearly.: unchanged since the review at 09:05 PST on Nov 20, 2026", "Items the operator raised on purpose (1)"} {
		if !strings.Contains(review.Report, want) {
			t.Errorf("report lacks %q:\n%s", want, review.Report)
		}
	}

	// One goal's work moves: only that goal is gone through, the other is one
	// line naming the review that went through it.
	r.source.reading.Items[0].Priority = 0
	fired := r.pull(t, novemberDay(22, 9, 0))
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the review's pass for the changed goal", fired.Fired)
	}
	message := r.role.messages[len(r.role.messages)-1]
	if !strings.Contains(message, "Goals to go through (1)") || !strings.Contains(message, "yoyodyne-1 (P0, open)") {
		t.Errorf("message does not go through the changed goal alone:\n%s", message)
	}
	if !strings.Contains(message, "[plain-language] The harness's surfaces read clearly.: unchanged since the review at 09:05 PST on Nov 20, 2026") {
		t.Errorf("message does not give the unchanged goal one line:\n%s", message)
	}
	if strings.Contains(message, "yoyodyne-2") {
		t.Errorf("message goes through the unchanged goal's work:\n%s", message)
	}
}

// A review whose pass did not complete leaves its goals as they were, so the
// next day's review goes through them again rather than calling them
// unchanged.
func TestAReviewThatDidNotCompleteIsGoneThroughAgain(t *testing.T) {
	t.Parallel()

	r := newDailyReview(t)
	r.role.failure = errors.New("the provider refused the turn")
	r.pull(t, novemberDay(20, 9, 5))
	if review := r.last(t); review.Outcome != runstate.GoalReviewNotCompleted || review.Problem == "" {
		t.Fatalf("review = %+v, want it recorded as not completed, with why", review)
	}
	r.role.failure = nil

	fired := r.pull(t, novemberDay(21, 9, 5))
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the goals gone through again", fired.Fired)
	}
	if review := r.last(t); len(review.GoneThrough) != 2 || review.Outcome != runstate.GoalReviewCompleted {
		t.Fatalf("review = %+v, want both goals gone through and completed", review)
	}
}

// The review's pass takes the place of an ordinary one, so the missing
// capabilities an ordinary pass reports are reported before it too.
func TestTheReviewsPassReportsMissingCapabilities(t *testing.T) {
	t.Parallel()

	r := newDailyReview(t)
	pile := &capabilityPile{}
	r.trigger.Attribution = report.Attribution{ProductID: "example", RepositoryID: "example"}
	r.trigger.HandEntries = func(context.Context) ([]readmodel.Attention, string) {
		return []readmodel.Attention{handEntry("run-open", ownership.HandCapCrossing, "")}, ""
	}
	r.trigger.CapabilityReports = pile

	if fired := r.pull(t, novemberDay(20, 9, 5)); len(fired.Fired) != 1 || fired.Fired[0].Summoned != goalReviewSummons {
		t.Fatalf("fired = %+v, want the review's pass", fired.Fired)
	}
	if filed := pile.missingCapabilityReports(); len(filed) != 1 {
		t.Fatalf("reports = %+v, want the missing capability reported", filed)
	}
}
