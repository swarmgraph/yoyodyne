package runstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func goalReviewStore(t *testing.T) *GoalReviewStore {
	t.Helper()
	store, err := NewGoalReviewStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewGoalReviewStore() error = %v", err)
	}
	return store
}

// Each review is recorded so the next can read it: a completed one records the
// goals it went through with their fingerprints and its start, and a second
// daily review for a day that has one is refused.
func TestGoalReviewsAreRecordedForTheNextReview(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := goalReviewStore(t)
	started := time.Date(2026, 11, 23, 17, 0, 0, 0, time.UTC)
	review := GoalReview{
		Trigger: GoalReviewDaily, Day: "2026-11-23", StartedAt: started,
		GoneThrough: []GoalReviewEntry{{Goal: "a", Fingerprint: "fa"}},
		Unchanged:   []GoalReviewEntry{{Goal: "b", Fingerprint: "fb", Since: started.Add(-24 * time.Hour)}},
		Raised:      []string{"yoyodyne-3"},
	}
	if _, err := store.Begin(ctx, review, []string{"a", "b"}); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := store.Begin(ctx, review, []string{"a", "b"}); !errors.Is(err, ErrGoalReviewRecorded) {
		t.Fatalf("second Begin() error = %v, want ErrGoalReviewRecorded", err)
	}
	if _, err := store.Finish(ctx, started, true, "", started.Add(time.Minute)); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	record, found, err := store.Load()
	if err != nil || !found {
		t.Fatalf("Load() = %v, %v", found, err)
	}
	if got := record.Goals["a"]; got.Fingerprint != "fa" || !got.ReviewedAt.Equal(started) {
		t.Errorf("goal a = %+v, want its fingerprint as gone through at the review's start", got)
	}
	if _, kept := record.Goals["b"]; kept {
		t.Error("an unchanged goal was recorded as gone through again")
	}
	daily, found := record.Daily("2026-11-23")
	if !found || daily.Outcome != GoalReviewCompleted || len(daily.Raised) != 1 || len(daily.Unchanged) != 1 {
		t.Errorf("Daily() = %+v, %v, want the completed review with what it read", daily, found)
	}
}

// A review with nothing to go through is recorded finished with no pass; one
// whose pass did not complete leaves the goals as they were, and a goal no
// longer read leaves the record.
func TestGoalReviewsThatNeedNoPassOrDidNotComplete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := goalReviewStore(t)
	first := time.Date(2026, 11, 22, 17, 0, 0, 0, time.UTC)
	if _, err := store.Begin(ctx, GoalReview{Trigger: GoalReviewDaily, Day: "2026-11-22", StartedAt: first,
		GoneThrough: []GoalReviewEntry{{Goal: "a", Fingerprint: "fa"}, {Goal: "gone", Fingerprint: "fg"}}}, []string{"a", "gone"}); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := store.Finish(ctx, first, true, "", first); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	second := first.Add(24 * time.Hour)
	record, err := store.Begin(ctx, GoalReview{Trigger: GoalReviewDaily, Day: "2026-11-23", StartedAt: second,
		Unchanged: []GoalReviewEntry{{Goal: "a", Fingerprint: "fa", Since: first}}}, []string{"a"})
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if last, _ := record.Last(); last.Outcome != GoalReviewNothingChanged || last.FinishedAt.IsZero() {
		t.Errorf("last = %+v, want a finished review in which nothing changed", last)
	}
	if _, kept := record.Goals["gone"]; kept {
		t.Error("a goal no longer read is still recorded")
	}

	third := second.Add(24 * time.Hour)
	if _, err := store.Begin(ctx, GoalReview{Trigger: GoalReviewDaily, Day: "2026-11-24", StartedAt: third,
		GoneThrough: []GoalReviewEntry{{Goal: "a", Fingerprint: "fa2"}}}, []string{"a"}); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	record, err = store.Finish(ctx, third, false, "the pass did not complete", third)
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if got := record.Goals["a"]; got.Fingerprint != "fa" || !got.ReviewedAt.Equal(first) {
		t.Errorf("goal a = %+v, want the last completed review's fingerprint kept", got)
	}
	if last, _ := record.Last(); last.Outcome != GoalReviewNotCompleted || last.Problem == "" {
		t.Errorf("last = %+v, want it recorded as not completed, with why", last)
	}
}
