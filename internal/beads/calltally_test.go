package beads

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Every bd invocation a client makes is told to its tally once it ends, the
// retries of a listing included, and one stopped at its limit is said at once,
// naming the read, so a command stopped as a whole has already said which read
// held it up.
func TestATallySaysEachTrackerReadAndTheOnesStoppedAtTheirLimit(t *testing.T) {
	t.Parallel()

	timedOut := execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}
	runner := &fakeRunner{
		responses: []string{workItemJSON("open", "")},
		failures:  map[int]execution.ProcessResult{1: timedOut, 2: timedOut, 3: timedOut},
	}
	var said bytes.Buffer
	tally := &CallTally{Slow: &said, SlowAfter: time.Hour}
	client := Client{Runner: runner, Dir: t.TempDir(), Timeout: 2 * time.Minute, Calls: tally,
		listingPause: func(context.Context, time.Duration) bool { return true }}

	if _, err := client.Show(context.Background(), "yoyodyne-1"); err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if _, err := client.List(context.Background(), "closed"); !Unanswered(err) {
		t.Fatalf("List() error = %v, want the tracker not answering", err)
	}

	lines := strings.Split(strings.TrimSpace(said.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("said %q, want one line for each of the listing's three attempts and none for the quick read", said.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "the tracker did not answer listing the work items with status closed: it was stopped after ") ||
			!strings.HasSuffix(line, "at its limit of 2m0s") {
			t.Errorf("said %q, want the read named with how long it ran and its limit", line)
		}
	}
	described := tally.Describe()
	for _, want := range []string{
		"asked the tracker 4 time(s), which took ",
		"3 of them stopped at their limit without an answer",
		"the longest: ",
	} {
		if !strings.Contains(described, want) {
			t.Errorf("Describe() = %q, want it to say %q", described, want)
		}
	}
}

// A call that answered, but only after the tally's threshold, is said as it
// ends with how long it took; the longest calls are named in the tally's line,
// longest first.
func TestATallySaysASlowAnswerAndNamesTheLongest(t *testing.T) {
	t.Parallel()

	var said bytes.Buffer
	tally := &CallTally{Slow: &said, SlowAfter: 30 * time.Second}
	for _, call := range []TrackerCall{
		{Call: "reading work item yoyodyne-1", Took: time.Second, Limit: 2 * time.Minute},
		{Call: "reading work item yoyodyne-2", Took: 47 * time.Second, Limit: 2 * time.Minute},
		{Call: "listing the work items with status closed", Took: 2 * time.Second, Limit: 2 * time.Minute},
		{Call: "reading work item yoyodyne-3", Took: 300 * time.Millisecond, Limit: 2 * time.Minute},
	} {
		tally.ObserveCall(call)
	}
	if got, want := strings.TrimSpace(said.String()), "the tracker took 47s to answer reading work item yoyodyne-2"; got != want {
		t.Fatalf("said %q, want %q", got, want)
	}
	if got, want := tally.Describe(), "asked the tracker 4 time(s), which took 50s in all; the longest: reading work item yoyodyne-2 (47s), listing the work items with status closed (2s), reading work item yoyodyne-1 (1s)"; got != want {
		t.Fatalf("Describe() = %q, want %q", got, want)
	}
	if got := (&CallTally{}).Describe(); got != "asked the tracker nothing" {
		t.Fatalf("an empty tally says %q", got)
	}
}
