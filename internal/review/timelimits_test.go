package review

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

func TestTheReviewDefaultIsTheConfigurationsDefault(t *testing.T) {
	t.Parallel()
	if defaultReviewTimeout != config.DefaultReviewTimeLimit {
		t.Fatalf("defaultReviewTimeout = %s, want config.DefaultReviewTimeLimit %s", defaultReviewTimeout, config.DefaultReviewTimeLimit)
	}
}

// A reviewer's configured time_limits.review is its invocation's total time.
func TestAConfiguredReviewLimitIsTheInvocationsTimeout(t *testing.T) {
	t.Parallel()

	limits := config.TimeLimits{Review: config.Duration(40 * time.Minute)}
	provider := &fakeBackend{finalText: `{"decision":"approve","approves":"implementation","summary":"fine"}`}
	reviewer := Reviewer{Backend: provider, Clock: reviewClock{}, Model: "review-model", Timeout: limits.ReviewLimit()}
	if _, err := reviewer.Review(context.Background(), newRequest(nil)); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if provider.request.Timeout != 40*time.Minute {
		t.Fatalf("review timeout = %s, want the configured 40m", provider.request.Timeout)
	}
}
