package beads

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
)

// timedOutCall is bd killed at its bound, which is how a stalled tracker call
// reaches this client.
func timedOutCall() execution.ProcessResult {
	return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}
}

// verbs is the bd verb of each call a runner was asked for, in order.
func verbs(runner *fakeRunner) []string {
	var called []string
	for _, args := range runner.args {
		called = append(called, args[0])
	}
	return called
}

// unwaited is a client whose waits between attempts are recorded rather than
// slept.
func unwaited(runner *fakeRunner, waited *[]time.Duration) Client {
	return Client{Runner: runner, listingPause: func(_ context.Context, wait time.Duration) bool {
		*waited = append(*waited, wait)
		return true
	}}
}

// A note whose write bd was killed at its bound after the tracker took it is
// read back, found at the end of the notes, and answered with — and never
// appended a second time.
func TestAWriteThatStallsAfterLandingIsReadBackAndNotMadeAgain(t *testing.T) {
	t.Parallel()
	note := "Run run-1 succeeded."
	runner := &fakeRunner{
		results:   []execution.ProcessResult{timedOutCall()},
		responses: []string{"", workItemJSON("in_progress", "earlier note\n"+note)},
	}
	var waited []time.Duration
	client := unwaited(runner, &waited)

	item, err := client.RecordOutcome(context.Background(), "yoyodyne-1", note)
	if err != nil {
		t.Fatalf("RecordOutcome() error = %v, want the landed write answered", err)
	}
	if !NotesEndWith(item.Notes, note) {
		t.Fatalf("RecordOutcome() = %+v, want the item read back with the note", item)
	}
	if got := verbs(runner); !slices.Equal(got, []string{"update", "show"}) {
		t.Fatalf("bd was asked %v, want the write and one read-back and no second write", got)
	}
	if !slices.Equal(waited, writeWaits[:1]) {
		t.Fatalf("waited %v, want %v before the read-back", waited, writeWaits[:1])
	}
}

// A claim whose write bd was killed at its bound before the tracker took it is
// read back, found not to be claimed, and made again.
func TestAWriteThatStallsBeforeLandingIsReadBackThenMadeAgain(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		results:   []execution.ProcessResult{timedOutCall()},
		responses: []string{"", workItemJSON("open", ""), workItemJSON("in_progress", "")},
	}
	var waited []time.Duration
	client := unwaited(runner, &waited)

	item, _, err := client.Claim(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Claim() error = %v, want the claim made again and answered", err)
	}
	if item.Status != statusInProgress {
		t.Fatalf("Claim() = %+v, want the item claimed", item)
	}
	if got := verbs(runner); !slices.Equal(got, []string{"update", "show", "update"}) {
		t.Fatalf("bd was asked %v, want the claim, a read-back, then the claim again", got)
	}
	for _, index := range []int{0, 2} {
		if !slices.Contains(runner.args[index], "--claim") {
			t.Fatalf("call %d was %v, want the claim", index, runner.args[index])
		}
	}
}

// A read-back the bound kills as well is waited out and read again rather than
// written over: nothing is made a second time until a read has answered.
func TestAReadBackThatStallsIsReadAgainBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		results:   []execution.ProcessResult{timedOutCall(), timedOutCall()},
		responses: []string{"", "", workItemJSON("closed", "")},
	}
	var waited []time.Duration
	client := unwaited(runner, &waited)

	item, err := client.Complete(context.Background(), "yoyodyne-1", "landed")
	if err != nil {
		t.Fatalf("Complete() error = %v, want the landed close answered", err)
	}
	if item.Status != statusClosed {
		t.Fatalf("Complete() = %+v, want the item closed", item)
	}
	if got := verbs(runner); !slices.Equal(got, []string{"close", "show", "show"}) {
		t.Fatalf("bd was asked %v, want the close and two read-backs", got)
	}
	if !slices.Equal(waited, writeWaits) {
		t.Fatalf("waited %v, want %v", waited, writeWaits)
	}
}

// A write the tracker never answers is refused once the waits are spent, saying
// whether it landed is not known and still reading as a timeout, so a caller
// that retries it further knows to read the item back first. It is never made
// more often than a read-back found it missing.
func TestAWriteTheTrackerNeverAnswersIsRefusedAsUnknownAfterTheWaits(t *testing.T) {
	t.Parallel()
	note := "Run run-1 succeeded."
	runner := &fakeRunner{results: []execution.ProcessResult{
		timedOutCall(), timedOutCall(), timedOutCall(), timedOutCall(), timedOutCall(),
	}}
	var waited []time.Duration
	client := unwaited(runner, &waited)

	_, err := client.RecordOutcome(context.Background(), "yoyodyne-1", note)
	if err == nil {
		t.Fatal("RecordOutcome() error = nil, want the write refused")
	}
	for _, want := range []string{"whether the last write landed is not known", "1 write(s) and 2 read-back(s)", "status timed_out"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("RecordOutcome() error = %q, want it to say %q", err, want)
		}
	}
	if !timedOut(err) || !recovery.Recoverable(err) {
		t.Fatalf("RecordOutcome() error = %v, want it still read as a timeout a caller may wait out", err)
	}
	if got := verbs(runner); !slices.Equal(got, []string{"update", "show", "show"}) {
		t.Fatalf("bd was asked %v, want no write made again without a read-back that answered", got)
	}
}

// A write whose read-back bd refuses outright cannot be settled, so it is not
// made again: the refusal is returned beside the timeout.
func TestAWriteWhoseReadBackIsRefusedIsNotMadeAgain(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		results:  []execution.ProcessResult{timedOutCall()},
		failures: map[int]execution.ProcessResult{1: {Status: execution.ProcessFailed, ExitCode: 1, Stderr: "database is locked"}},
	}
	var waited []time.Duration
	client := unwaited(runner, &waited)

	_, err := client.RecordOutcome(context.Background(), "yoyodyne-1", "a note")
	if err == nil || !strings.Contains(err.Error(), "so it was not made again") || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("RecordOutcome() error = %v, want the unsettled write refused with the read-back's failure", err)
	}
	if got := verbs(runner); !slices.Equal(got, []string{"update", "show"}) {
		t.Fatalf("bd was asked %v, want no second write", got)
	}
}

// A write bd refuses for any reason other than its bound is answered as it
// always was, with no read-back and no second attempt.
func TestAWriteBdRefusesIsNotRetried(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "issue yoyodyne-1 not found"}}}
	var waited []time.Duration
	client := unwaited(runner, &waited)

	if _, err := client.Complete(context.Background(), "yoyodyne-1", "landed"); err == nil {
		t.Fatal("Complete() error = nil, want the refusal")
	}
	if len(runner.args) != 1 || len(waited) != 0 {
		t.Fatalf("bd was asked %v after waiting %v, want the one refused call", runner.args, waited)
	}
}

// A read of one item the bound kills once is asked again and answers: the
// tracker reads that stopped a settlement on 2026-10-05 were this.
func TestAReadThatTimesOutOnceAnswersOnTheNextAttempt(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		results:   []execution.ProcessResult{timedOutCall()},
		responses: []string{"", workItemJSON("open", "")},
	}
	var waited []time.Duration
	client := unwaited(runner, &waited)

	item, err := client.Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v, want the second attempt's answer", err)
	}
	if item.ID != "yoyodyne-1" || len(runner.args) != 2 || !slices.Equal(waited, listingWaits[:1]) {
		t.Fatalf("Show() = %+v after %v and waits %v", item, runner.args, waited)
	}
}

// A read the bound kills on every attempt is refused naming the attempts, and
// still reads as a timeout to whatever waits it out further.
func TestAReadTheBoundKillsOnEveryAttemptIsRefusedByName(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: []execution.ProcessResult{timedOutCall(), timedOutCall(), timedOutCall()}}
	var waited []time.Duration
	client := unwaited(runner, &waited)

	_, err := client.Show(context.Background(), "yoyodyne-1")
	if err == nil || !strings.Contains(err.Error(), "on any of 3 attempts") || !timedOut(err) {
		t.Fatalf("Show() error = %v, want the read refused by name and still a timeout", err)
	}
	if errors.Is(err, ErrNoSuchWorkItem) {
		t.Fatalf("Show() error = %v, want a tracker that did not answer, not a missing item", err)
	}
}
