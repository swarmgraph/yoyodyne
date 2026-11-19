package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// trackerStalled is the tracker client giving up on a write bd never answered:
// its own read-backs could not say whether the write landed, so the run's
// retry has to ask before it makes it again.
var trackerStalled = errors.New("bd update on yoyodyne-task did not answer within its 30s bound: 3 write(s) and 2 read-back(s) over 2m40s, and whether the last write landed is not known: bd update failed with status timed_out and exit code -1: ")

func calls(tracker *orchestratortest.Tracker, call string) int {
	count := 0
	for _, made := range tracker.Calls {
		if made == call {
			count++
		}
	}
	return count
}

func TestRecordedRetriesAreValidatedAndBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", runstate.MaxRetryFailureBytes+64)
	if bounded := boundedFailureDetail(long); len(bounded) > runstate.MaxRetryFailureBytes {
		t.Errorf("bounded detail is %d bytes, which exceeds the %d byte bound", len(bounded), runstate.MaxRetryFailureBytes)
	}
	valid := runstate.Retry{Boundary: runstate.RetryMerge, Attempt: 1, DelaySeconds: 1, At: baseTime}
	if err := valid.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want a recorded retry to be valid", err)
	}
	for _, invalid := range []runstate.Retry{
		{Attempt: 1, DelaySeconds: 1, At: baseTime},
		{Boundary: runstate.RetryMerge, Attempt: 0, DelaySeconds: 1, At: baseTime},
		{Boundary: runstate.RetryMerge, Attempt: 1, DelaySeconds: -1, At: baseTime},
		{Boundary: runstate.RetryMerge, Attempt: 1, DelaySeconds: 1},
		{Boundary: runstate.RetryMerge, Attempt: 1, DelaySeconds: 1, At: baseTime, Failure: long},
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("Validate() error = nil for %#v, want the contract violation reported", invalid)
		}
	}
}

// parkedTrackerRun runs one item through a tracker that stalls the claim and
// the closure far past the two-hour window before answering each, and records
// what the run's record said at every wait it took.
func parkedTrackerRun(t *testing.T) (*orchestratortest.Tracker, Outcome, runstate.State, []runstate.State, int) {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{
		Item:                 beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"},
		ClaimFailures:        stallsPastTheWindow,
		TransientClaimErr:    trackerStalled,
		CompleteFailures:     stallsPastTheWindow,
		TransientCompleteErr: trackerStalled,
	}
	developers := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		developers++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &pausingClock{now: baseTime}
	pipeline = waiting(pipeline, clock, 6*time.Hour, 6*time.Hour)
	saved := &lastSaved{StateStore: pipeline.Store}
	pipeline.Store = saved
	var atWaits []runstate.State
	clock.onSleep = func() { atWaits = append(atWaits, saved.last) }

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run to wait on the tracker and carry on", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return tracker, outcome, state, atWaits, developers
}

// stallsPastTheWindow is more refusals than the two-hour window has waits for,
// so the last few are met by the run parked on the tracker.
const stallsPastTheWindow = 20

// A claim and a closure the tracker leaves unanswered past the whole window do
// not end the run. Each parks it on the tracker, with the boundary and the
// tracker's last words on its record, and the run goes on from the step that
// was waiting once the tracker answers: one claim, one developer, one closure,
// and nothing left parked.
func TestATrackerWriteUnansweredPastItsWindowParksTheRunUntilTheTrackerAnswers(t *testing.T) {
	t.Parallel()
	tracker, outcome, state, atWaits, developers := parkedTrackerRun(t)

	if outcome.Status != runstate.StatusSucceeded || !tracker.Closed {
		t.Fatalf("status = %q, closed = %t, want the run finished once the tracker answered", outcome.Status, tracker.Closed)
	}
	if developers != 1 || calls(tracker, "claim") != 1 {
		t.Fatalf("developers = %d, claims = %d, want one of each: %v", developers, calls(tracker, "claim"), tracker.Calls)
	}
	if state.TrackerPause != nil {
		t.Fatalf("tracker pause = %#v, want it lifted once the tracker answered", state.TrackerPause)
	}
	parkedAtClaim, parkedAtClose := false, false
	for _, recorded := range atWaits {
		pause := recorded.TrackerPause
		if pause == nil {
			continue
		}
		if pause.Boundary != runstate.RetryTrackerWrite || !strings.Contains(pause.Failure, "did not answer") {
			t.Fatalf("park = %#v, want the tracker write and its last failure", pause)
		}
		if pausedForTracker(recorded) {
			t.Fatalf("a park on a tracker write was offered to a later process to resume: %#v", pause)
		}
		switch {
		case recorded.WorktreePath == "":
			parkedAtClaim = true
		case recorded.Phase == runstate.PhaseCompleting:
			parkedAtClose = true
		}
	}
	if !parkedAtClaim || !parkedAtClose {
		t.Fatalf("parked at the claim %t, at the closure %t, want both recorded before their waits", parkedAtClaim, parkedAtClose)
	}
}
