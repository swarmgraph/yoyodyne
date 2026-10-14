package beads

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
)

// deadlineRunner answers the way the operating-system runner does when a
// caller's deadline ends a call: it waits for the context and reports the call
// timed out, with nothing written.
type deadlineRunner struct{ args [][]string }

func (r *deadlineRunner) Run(ctx context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.args = append(r.args, command.Args)
	<-ctx.Done()
	return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}, nil
}

// A read stopped at the client's own limit says which item it was reading, for
// whom, and that the limit was the tracker's, and is still read as a stalled
// tracker call by everything that retries one.
func TestAReadStoppedAtItsLimitNamesTheItemAndWhoAsked(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: []execution.ProcessResult{timedOutCall(), timedOutCall(), timedOutCall()}}
	var waited []time.Duration
	client := unwaited(runner, &waited)
	ctx := OnBehalfOf(context.Background(), "the development manager's recurring pass development-manager-sweep#804")

	_, err := client.Show(ctx, "yoyodyne-7my")
	if err == nil {
		t.Fatal("Show() error = nil, want the timeout")
	}
	for _, want := range []string{
		"bd show failed with status timed_out and exit code -1: ",
		"reading work item yoyodyne-7my for the development manager's recurring pass development-manager-sweep#804",
		"at the 30s limit each tracker call is given",
		"on any of 3 attempts",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Show() error = %q, want it to say %q", err, want)
		}
	}
	if !timedOut(err) || !recovery.Recoverable(err) {
		t.Fatalf("Show() error = %q, want it still read as a stalled tracker call", err)
	}
}

// A write its caller gave less time than the tracker's limit says that it was
// the caller's deadline and how long that was, which is the difference between
// a slow tracker and a caller that did not wait for one.
func TestAWriteStoppedByItsCallersDeadlineSaysSo(t *testing.T) {
	t.Parallel()
	runner := &deadlineRunner{}
	client := Client{Runner: runner}
	ctx, cancel := context.WithTimeout(OnBehalfOf(context.Background(), "the architect's recurring pass architect-pass#313"), 2*time.Second)
	defer cancel()

	_, err := client.RecordOutcome(ctx, "yoyodyne-ifd.429.43", "Passed over.")
	if err == nil {
		t.Fatal("RecordOutcome() error = nil, want the caller's deadline")
	}
	for _, want := range []string{
		"bd update failed with status timed_out and exit code -1: ",
		"changing work item yoyodyne-ifd.429.43 (bd update) for the architect's recurring pass architect-pass#313",
		"when its caller's own deadline of 2s ran out before the 30s limit each tracker call is given",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("RecordOutcome() error = %q, want it to say %q", err, want)
		}
	}
}

// A call whose caller's deadline had passed before bd was started says it never
// started, rather than reading as a tracker that did not answer.
func TestACallWhoseDeadlineHadPassedSaysItNeverStarted(t *testing.T) {
	t.Parallel()
	client := Client{Runner: &deadlineRunner{}}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := client.List(ctx, "open")
	var failure processFailure
	if !errors.As(err, &failure) {
		t.Fatalf("List() error = %v, want bd's failure", err)
	}
	if want := "listing the work items never started: its caller's own deadline had already passed"; !strings.Contains(err.Error(), want) {
		t.Fatalf("List() error = %q, want it to say %q", err, want)
	}
}

// A call nobody named says what it was asked and nothing about whom.
func TestDescribeCall(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"show", "yoyodyne-7my", "--json"}, "reading work item yoyodyne-7my"},
		{[]string{"update", "yoyodyne-9j7", "--claim", "--json"}, "changing work item yoyodyne-9j7 (bd update)"},
		{[]string{"list", "--all", "--json"}, "listing the work items"},
		{[]string{"dep", "add", "yoyodyne-1", "yoyodyne-2"}, "bd dep"},
		{[]string{"export"}, "exporting every work item"},
	} {
		if got := describeCall(tc.args); got != tc.want {
			t.Errorf("describeCall(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
