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
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// exportStalled is how the tracker's client reports a `bd export` killed at its
// bound, which is what stopped a run before its developer started on October 10.
var exportStalled = errors.New("bd export failed with status timed_out and exit code -1: ")

// exportStall is one run whose worktree's export stalls failures times before it
// answers, with what the run did about it.
type exportStall struct {
	tracker    *orchestratortest.Tracker
	pipeline   Pipeline
	store      *runstate.Store
	clock      *pausingClock
	prepared   int
	developers int
}

func newExportStall(t *testing.T, failures int) *exportStall {
	t.Helper()
	repository := pipelineRepository(t)
	stall := &exportStall{
		tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}},
		clock:   &pausingClock{now: baseTime},
	}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		stall.developers++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, stall.tracker, provider, []string{"exit 0"})
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          filepath.Join(t.TempDir(), "worktrees"),
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
		CurrentExports:        []string{".beads/issues.jsonl"},
		PrepareExports: func(context.Context) error {
			stall.prepared++
			if stall.prepared <= failures {
				return exportStalled
			}
			return nil
		},
		Timeout: testGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	pipeline.Worktrees = worktrees
	stall.pipeline = waiting(pipeline, stall.clock, 6*time.Hour, 6*time.Hour)
	stall.store = store
	return stall
}

func (s *exportStall) state(t *testing.T, runID string) runstate.State {
	t.Helper()
	state, err := s.store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

// An export that times out twice and then answers costs the run two recorded
// waits and nothing else: the worktree it already made is adopted rather than
// made again, the item is claimed once, the developer is started once, and the
// run finishes as if the export had answered first time.
func TestAWorktreeExportThatTimesOutAndThenAnswersIsWaitedOut(t *testing.T) {
	t.Parallel()
	stall := newExportStall(t, 2)

	outcome, err := stall.pipeline.Run(context.Background(), stall.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the stalled export waited out", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !stall.tracker.Closed {
		t.Fatalf("status = %q, closed = %t, want the run finished and its item closed", outcome.Status, stall.tracker.Closed)
	}
	if stall.prepared != 3 {
		t.Errorf("exports asked for = %d, want the two that timed out and the one that answered", stall.prepared)
	}
	if stall.developers != 1 {
		t.Errorf("developers started = %d, want one", stall.developers)
	}
	if claims := calls(stall.tracker, "claim"); claims != 1 {
		t.Errorf("claims = %d, want the one the run started with: %v", claims, stall.tracker.Calls)
	}
	state := stall.state(t, outcome.RunID)
	if attempts := state.RetryAttempts(runstate.RetryTrackerExport); attempts != 2 {
		t.Fatalf("waits recorded at the export = %d, want two: %#v", attempts, state.Retries)
	}
	if !strings.Contains(state.Retries[0].Failure, "exit code -1") {
		t.Errorf("recorded failure = %q, want the tracker client's own words", state.Retries[0].Failure)
	}
	if state.RepairAttempts != 0 || state.Environmental != nil {
		t.Errorf("repair attempts = %d, environmental = %#v, want nothing spent or excused for repeating preparation", state.RepairAttempts, state.Environmental)
	}
}

// An export that never answers through the whole window stops the run before
// any developer starts, saying it stopped while preparing its worktree, what it
// left, and what gives the item back. The round is the environment's, so
// nothing is charged for it, and the worktree it made is left unchanged.
func TestAWorktreeExportThatNeverAnswersStopsTheRunUnchargedBeforeAnyDeveloper(t *testing.T) {
	t.Parallel()
	stall := newExportStall(t, 1<<20)

	outcome, err := stall.pipeline.Run(context.Background(), stall.tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the run stopped on an export that never answered")
	}
	if stall.developers != 0 {
		t.Fatalf("developers started = %d, want none on an unconfirmed export", stall.developers)
	}
	if outcome.Status != runstate.StatusFailed {
		t.Fatalf("status = %q, want failed", outcome.Status)
	}
	state := stall.state(t, outcome.RunID)
	for _, says := range []string{"while preparing its worktree", "before any developer started", "holds nothing of the work", "audit of claims", "exit code -1"} {
		if !strings.Contains(state.Failure, says) {
			t.Errorf("failure = %q, want it to say %q", state.Failure, says)
		}
	}
	refusal := state.Environmental
	if refusal == nil || refusal.Cause != runstate.CauseTransportFailure || !refusal.NothingRan || !refusal.Refused {
		t.Fatalf("environmental = %#v, want a transport failure refused before anything ran", refusal)
	}
	if state.RetryAttempts(runstate.RetryTrackerExport) == 0 || state.RetryWaited(runstate.RetryTrackerExport) > time.Duration(2)*time.Hour {
		t.Errorf("export waits = %d over %s, want the window spent and no more", state.RetryAttempts(runstate.RetryTrackerExport), state.RetryWaited(runstate.RetryTrackerExport))
	}
	if claims := calls(stall.tracker, "claim"); claims != 1 {
		t.Errorf("claims = %d, want one: %v", claims, stall.tracker.Calls)
	}
	if state.WorktreePath == "" {
		t.Fatal("the run recorded no worktree, want the one it made kept on its record")
	}
	if _, err := os.Stat(state.WorktreePath); err != nil {
		t.Fatalf("worktree %s: %v, want it left where it was made", state.WorktreePath, err)
	}
	if _, err := os.Stat(filepath.Join(state.WorktreePath, "feature.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("feature.txt in the worktree: %v, want nothing of the work in it", err)
	}
}

// A run ended while it waits out the export stops there: the wait it was taking
// and the worktree it made were on its record before it slept, no developer is
// started, and nothing is charged for a round that never reached the work.
func TestAWorktreeExportWaitInterruptedLeavesTheWaitAndTheWorktreeOnRecord(t *testing.T) {
	t.Parallel()
	stall := newExportStall(t, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var atWait runstate.State
	saved := &lastSaved{StateStore: stall.pipeline.Store}
	stall.clock.onSleep = func() {
		atWait = saved.last
		cancel()
	}
	interrupted := stall.pipeline
	interrupted.Store = saved
	interrupted.Sleep = func(ctx context.Context, duration time.Duration) error {
		_ = stall.clock.sleep(ctx, duration)
		return ctx.Err()
	}

	outcome, err := interrupted.Run(ctx, stall.tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the run ended by its interruption")
	}
	if stall.developers != 0 || stall.prepared != 1 {
		t.Fatalf("developers = %d, exports = %d, want none started and no export asked again after the interruption", stall.developers, stall.prepared)
	}
	if atWait.RetryAttempts(runstate.RetryTrackerExport) != 1 || atWait.WorktreePath == "" {
		t.Fatalf("record at the wait: export waits = %d, worktree = %q, want both written before the wait", atWait.RetryAttempts(runstate.RetryTrackerExport), atWait.WorktreePath)
	}
	state := stall.state(t, outcome.RunID)
	if state.Status == runstate.StatusRunning || state.Status == runstate.StatusPending {
		t.Fatalf("status = %q, want the interrupted run ended", state.Status)
	}
	if refusal := state.Environmental; refusal == nil || !refusal.NothingRan || !refusal.Refused {
		t.Errorf("environmental = %#v, want the round refused before anything ran", refusal)
	}
	if _, err := os.Stat(atWait.WorktreePath); err != nil {
		t.Errorf("worktree %s: %v, want it kept", atWait.WorktreePath, err)
	}
}

// lastSaved remembers the last state a run saved, which is what a process that
// died at that moment leaves on disk.
type lastSaved struct {
	StateStore
	last runstate.State
}

func (s *lastSaved) Save(state runstate.State) error {
	if err := s.StateStore.Save(state); err != nil {
		return err
	}
	s.last = state
	return nil
}
