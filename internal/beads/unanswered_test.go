package beads

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A call the store never answered reads as unanswered, wrapped however a caller
// wraps it; a call bd answered, even with a refusal, does not.
func TestOnlyACallTheStoreNeverAnsweredIsUnanswered(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"killed at its bound", fmt.Errorf("read the item: %w", processFailure{verb: "show", status: execution.ProcessTimedOut, exitCode: -1}), true},
		{"a write behind another write", busyWrite{fmt.Errorf("another write held it: %w", context.DeadlineExceeded)}, true},
		{"an export behind another export", fmt.Errorf("refresh: %w", ErrExportBusy), true},
		{"bd refused", processFailure{verb: "show", status: execution.ProcessFailed, exitCode: 1, message: "refused"}, false},
		{"no such item", fmt.Errorf("%w: gone", ErrNoSuchWorkItem), false},
		{"anything else", errors.New("bd show timed out"), false},
		{"nothing", nil, false},
	} {
		if got := Unanswered(test.err); got != test.want {
			t.Errorf("%s: Unanswered(%v) = %v, want %v", test.name, test.err, got, test.want)
		}
	}
}

// Every attempt Show makes timing out leaves an error that still reads as
// unanswered, which is what lets a caller leave its work standing.
func TestAReadThatTimedOutOnEveryAttemptIsUnanswered(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: []execution.ProcessResult{timedOutCall(), timedOutCall(), timedOutCall()}}
	client := Client{Runner: runner, listingPause: func(context.Context, time.Duration) bool { return true }}
	_, err := client.Show(context.Background(), "yoyodyne-ifd.174")
	if err == nil || !Unanswered(err) {
		t.Fatalf("Show() error = %v, want one that reads as unanswered", err)
	}
}
