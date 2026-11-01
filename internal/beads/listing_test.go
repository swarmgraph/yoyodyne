package beads

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// listingRecord is a ListingRecorder that keeps what it was told.
type listingRecord struct {
	mu       sync.Mutex
	failed   []string
	answered int
}

func (r *listingRecord) Failed(_ time.Time, failure string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, failure)
	return nil
}

func (r *listingRecord) Answered(time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answered++
	return nil
}

func timedOutListing() execution.ProcessResult {
	return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}
}

// The listing that stopped every sweep's docket on 2026-09-29: bd killed at its
// bound on every attempt. It is asked again after each wait, and refused once
// the attempts are spent with how many there were and over how long, around
// bd's own failure — so the caller names the tracker not answering, and a
// reader of the refusal can still see it was a timeout. The recorder is told
// once, for the listing, rather than once per attempt.
func TestAListingTheBoundKillsOnEveryAttemptIsAskedAgainThenRefusedByName(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: []execution.ProcessResult{timedOutListing(), timedOutListing(), timedOutListing()}}
	record := &listingRecord{}
	var waited []time.Duration
	client := Client{Runner: runner, Listings: record, listingPause: func(_ context.Context, wait time.Duration) bool {
		waited = append(waited, wait)
		return true
	}}

	_, err := client.List(context.Background(), "closed")
	if err == nil {
		t.Fatal("List() error = nil, want the listing refused")
	}
	for _, want := range []string{"did not answer within its " + DefaultTimeout.String() + " bound on any of 3 attempts", "bd list failed with status timed_out"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("List() error = %q, want it to say %q", err, want)
		}
	}
	if len(runner.args) != 3 {
		t.Fatalf("bd was asked %d times, want 3: %v", len(runner.args), runner.args)
	}
	if !slices.Equal(waited, listingWaits) {
		t.Fatalf("waited %v between attempts, want %v", waited, listingWaits)
	}
	if len(record.failed) != 1 || record.answered != 0 || !strings.Contains(record.failed[0], "3 attempts") {
		t.Fatalf("recorded %+v, want one failure naming the attempts", record)
	}
}

// A listing killed once and answered on the next attempt is an answer, and is
// recorded as one: that is contention waited out, which is the case the
// retry is for.
func TestAListingKilledOnceAnswersOnTheNextAttempt(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		results:   []execution.ProcessResult{timedOutListing()},
		responses: []string{"", workItemJSON("closed", "")},
	}
	record := &listingRecord{}
	client := Client{Runner: runner, Listings: record, listingPause: func(context.Context, time.Duration) bool { return true }}

	items, err := client.List(context.Background(), "closed")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "yoyodyne-1" {
		t.Fatalf("List() = %+v", items)
	}
	if len(record.failed) != 0 || record.answered != 1 {
		t.Fatalf("recorded %+v, want one answer", record)
	}
}

// Only a listing the bound killed is asked again. bd refusing is the same
// answer on the next attempt, so it is returned as it was, and still recorded
// as the tracker not answering a listing.
func TestAListingBdRefusesIsNotAskedAgain(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "failed to open database"}}}
	record := &listingRecord{}
	client := Client{Runner: runner, Listings: record, listingPause: func(context.Context, time.Duration) bool {
		t.Fatal("a refusal was waited on")
		return true
	}}

	if _, err := client.List(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "failed to open database") {
		t.Fatalf("List() error = %v, want bd's refusal", err)
	}
	if len(runner.args) != 1 || len(record.failed) != 1 {
		t.Fatalf("asked %d times and recorded %+v, want one of each", len(runner.args), record)
	}
}

// A caller that gives up during the wait gets the failure it had, and nothing
// is recorded: a listing nobody waited for says nothing about the tracker.
func TestAListingWhoseCallerGivesUpDuringTheWaitRecordsNothing(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: []execution.ProcessResult{timedOutListing()}}
	record := &listingRecord{}
	ctx, cancel := context.WithCancel(context.Background())
	client := Client{Runner: runner, Listings: record, listingPause: func(context.Context, time.Duration) bool {
		cancel()
		return false
	}}

	if _, err := client.List(ctx, ""); err == nil {
		t.Fatal("List() error = nil")
	}
	if len(runner.args) != 1 || len(record.failed) != 0 || record.answered != 0 {
		t.Fatalf("asked %d times and recorded %+v, want one attempt and nothing recorded", len(runner.args), record)
	}
}

// The store held under a competing writer, against bd itself. A second process
// creates items in a loop — every write takes bd's exclusive lock on the store
// and rewrites its export — while the client lists the same store under a
// bound short enough that a listing queued behind a write can be killed. What
// is asserted is the item's own sentence: every listing either completes with
// the whole store in it, or fails naming the tracker not answering within its
// bound. What it must never do is answer with part of the store or fail with
// something nobody could act on.
func TestAListingUnderACompetingWriterCompletesOrNamesTheFailure(t *testing.T) {
	t.Parallel()
	project := newTracker(t)
	ctx := context.Background()
	seed := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	for index := range 3 {
		if _, err := seed.Create(ctx, NewWorkItem{Title: fmt.Sprintf("seed %d", index), Description: "seeded before the competing writer starts", Type: "task"}); err != nil {
			t.Fatalf("seed Create() error = %v", err)
		}
	}

	// The competing writer carries its own end, as every background load here
	// has to: it stops after its deadline however the test ends.
	writerCtx, stopWriter := context.WithTimeout(ctx, 90*time.Second)
	defer stopWriter()
	written := make(chan int, 1)
	go func() {
		count := 0
		for writerCtx.Err() == nil {
			command := exec.CommandContext(writerCtx, "bd", "create", fmt.Sprintf("competing write %d", count), "--description=a write beside the listings", "--type=task", "--json")
			command.Dir = project
			if command.Run() == nil {
				count++
			}
		}
		written <- count
	}()

	record := &listingRecord{}
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: 15 * time.Second, Listings: record}
	deadline := time.Now().Add(45 * time.Second)
	listings := 0
	for time.Now().Before(deadline) {
		items, err := client.List(ctx, "")
		listings++
		if err != nil {
			if !strings.Contains(err.Error(), "did not answer within its 15s bound") && !strings.Contains(err.Error(), "bd list failed") {
				t.Fatalf("listing %d failed without naming the tracker: %v", listings, err)
			}
			continue
		}
		if len(items) < 3 {
			t.Fatalf("listing %d answered with %d item(s), fewer than the 3 seeded before it began", listings, len(items))
		}
	}
	stopWriter()
	writes := <-written
	if listings == 0 {
		t.Fatal("no listing was made")
	}
	if len(record.failed)+record.answered != listings {
		t.Fatalf("recorded %d failure(s) and %d answer(s) over %d listing(s), want one record per listing", len(record.failed), record.answered, listings)
	}
	t.Logf("%d listing(s) beside %d competing write(s): %d answered, %d named the tracker not answering", listings, writes, record.answered, len(record.failed))
}
