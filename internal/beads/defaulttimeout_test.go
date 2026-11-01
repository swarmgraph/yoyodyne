package beads

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// slowTracker is a bd that takes a set time to answer and, like the process
// runner, is stopped where the call's limit is shorter than that. The time is
// pretended rather than slept, so a two-minute call costs the test nothing.
type slowTracker struct {
	takes    time.Duration
	response string
	limits   []time.Duration
}

func (s *slowTracker) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	s.limits = append(s.limits, command.Timeout)
	if command.Timeout > 0 && s.takes > command.Timeout {
		return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: s.response}, nil
}

// A listing of the oversized store ran 33 seconds with nothing else holding it,
// and was stopped at the thirty-second limit every tracker call then had. A
// Client that sets no limit of its own now waits the default for it, so a
// listing, and a read of one item, that take longer than thirty seconds answer.
func TestATrackerCallLongerThanThirtySecondsCompletesAtTheDefaultLimit(t *testing.T) {
	t.Parallel()
	if DefaultTimeout != 120*time.Second {
		t.Fatalf("DefaultTimeout = %s, want 2m0s", DefaultTimeout)
	}
	for _, takes := range []time.Duration{33 * time.Second, 90 * time.Second, 119 * time.Second} {
		listing := &slowTracker{takes: takes, response: workItemJSON("open", "")}
		items, err := (Client{Runner: listing, listingPause: func(context.Context, time.Duration) bool { return true }}).List(context.Background(), "open")
		if err != nil {
			t.Fatalf("a listing taking %s: List() error = %v", takes, err)
		}
		if len(items) != 1 || len(listing.limits) != 1 || listing.limits[0] != DefaultTimeout {
			t.Fatalf("a listing taking %s: List() = %+v asked with limits %v, want one item in one call at %s", takes, items, listing.limits, DefaultTimeout)
		}

		read := &slowTracker{takes: takes, response: workItemJSON("open", "")}
		item, err := (Client{Runner: read}).Show(context.Background(), "yoyodyne-1")
		if err != nil || item.ID != "yoyodyne-1" {
			t.Fatalf("a read taking %s: Show() = %+v, %v", takes, item, err)
		}
	}
}

// The fake stops what the limit says to stop, so the test above passes because
// of the limit rather than because the fake never times out: the same 33-second
// listing under the old thirty seconds is stopped on every attempt.
func TestTheSlowTrackerStopsACallPastItsLimit(t *testing.T) {
	t.Parallel()
	listing := &slowTracker{takes: 33 * time.Second, response: workItemJSON("open", "")}
	_, err := (Client{Runner: listing, Timeout: 30 * time.Second, listingPause: func(context.Context, time.Duration) bool { return true }}).List(context.Background(), "open")
	if err == nil {
		t.Fatal("List() error = nil, want a 33-second listing stopped at a 30-second limit")
	}
	if len(listing.limits) != 3 {
		t.Fatalf("bd was asked %d times, want 3", len(listing.limits))
	}
}
