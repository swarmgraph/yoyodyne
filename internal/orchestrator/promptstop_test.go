package orchestrator

// A stop recorded while a run works takes effect at the run's next step, and a
// step in progress is cut short once the stop has waited its bound; a stop that
// reaches the run only after its checks passed keeps the change for its review.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stopWaitedItsBound is a stop recorded long enough ago that it has waited
// runstate.StopTakesEffectWithin, so a watched step it finds is cut short.
func stopWaitedItsBound(runID string) runstate.StopRequest {
	return runstate.StopRequest{
		SchemaVersion: runstate.StopSchemaVersion,
		ProductID:     "yoyodyne",
		RunID:         runID,
		WorkItemID:    "yoyodyne-task",
		RequestedAt:   time.Now().Add(-runstate.StopTakesEffectWithin - time.Minute),
		RequestedBy:   "the development manager",
		Reason:        "superseded by another fix",
	}
}

// longDeveloper is a provider whose developer attempt runs until its context is
// cancelled, as a real attempt of an hour would, after doing what develop does.
// It never runs longer than a minute, so a test that fails to cut it ends.
type longDeveloper struct {
	*orchestratortest.Backend
}

func (b longDeveloper) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	result, err := b.Backend.Run(ctx, request)
	if request.Role != domain.RoleDeveloper || err != nil {
		return result, err
	}
	select {
	case <-ctx.Done():
		result.Process.Status = execution.ProcessCancelled
		return result, ctx.Err()
	case <-time.After(time.Minute):
		return result, nil
	}
}

// longChecks runs the real checks and then, standing in for a suite of an
// hour, waits until its context is cancelled. before is called first with the
// run's id.
type longChecks struct {
	inner  CheckRunner
	before func(runID string) error
	block  bool
}

func (c longChecks) Run(ctx context.Context, request checks.Request, sink func(execution.Event) error) ([]checks.Result, uint64, error) {
	if err := c.before(request.RunID); err != nil {
		return nil, request.LastSequence, err
	}
	if !c.block {
		return c.inner.Run(ctx, request, sink)
	}
	select {
	case <-ctx.Done():
		return nil, request.LastSequence, ctx.Err()
	case <-time.After(time.Minute):
		return c.inner.Run(ctx, request, sink)
	}
}

// The case reported twice on Nov 11: a stop recorded while the developer is in
// the middle of a long attempt. It cuts the attempt short once it has waited
// its bound, rather than waiting for the attempt and the checks to finish, and
// the ending says when it took effect and the bound it was held to. What the
// attempt wrote is kept on the run's branch.
func TestAStopCutsALongDeveloperAttemptShortAtItsBound(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	inner := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := store.RecordStop(stopWaitedItsBound(request.RunID)); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("half done\n"), 0o600)
	}, approveVerdict)
	provider := longDeveloper{Backend: inner}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.StopWatchInterval = 10 * time.Millisecond

	started := time.Now()
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("the run took %s, want the stop to have cut the attempt short", elapsed)
	}
	if err == nil {
		t.Fatal("Run() error = nil, want the stop reported as what ended the run")
	}
	assertSavedStopClass(t, store, outcome.RunID, runstate.StopOperator)
	if outcome.Status != runstate.StatusCancelled {
		t.Fatalf("status = %q, want a stopped run recorded as cancelled", outcome.Status)
	}
	for _, want := range []string{"the development manager stopped this run", "cutting short the developer's attempt", runstate.StopTakesEffectWithin.String()} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Run() error = %v, want it to say %q", err, want)
		}
	}
	if len(outcome.Checks) != 0 || countRoleRequests(inner.Requests, "reviewer") != 0 {
		t.Fatalf("checks = %d, reviews = %d, want neither spent on a stopped run", len(outcome.Checks), countRoleRequests(inner.Requests, "reviewer"))
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a stopped run promoted its work: %#v (closed=%t)", outcome.Integration, tracker.Closed)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	show, err := exec.Command("git", "-C", repository, "show", stopped.Branch+":feature.txt").CombinedOutput()
	if err != nil || string(show) != "half done\n" {
		t.Fatalf("the cut attempt's work is not on %s: %s (%v)", stopped.Branch, show, err)
	}
	if !strings.Contains(tracker.Notes, "cutting short the developer's attempt") {
		t.Fatalf("the work item was not told how the stop took effect: %q", tracker.Notes)
	}
}

// A stop recorded while the checks run cuts them short at the same bound.
func TestAStopCutsLongChecksShortAtItsBound(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.StopWatchInterval = 10 * time.Millisecond
	pipeline.Checks = longChecks{inner: pipeline.Checks, block: true, before: func(runID string) error {
		return store.RecordStop(stopWaitedItsBound(runID))
	}}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "cutting short its checks") {
		t.Fatalf("Run() error = %v, want the stop to have cut the checks short", err)
	}
	assertSavedStopClass(t, store, outcome.RunID, runstate.StopOperator)
	if outcome.Status != runstate.StatusCancelled || countRoleRequests(provider.Requests, "reviewer") != 0 {
		t.Fatalf("status = %q, reviews = %d, want a cancelled run that bought no review", outcome.Status, countRoleRequests(provider.Requests, "reviewer"))
	}
}

// A stop that reaches the run only once its checks have passed does not cancel
// the change: it is kept and goes on to its review, and the run records and
// says that the stop arrived late.
func TestAStopArrivingAfterTheChecksPassKeepsTheChangeForReview(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.StopWatchInterval = 10 * time.Millisecond
	// Recorded as the checks begin and well inside its bound, so the checks
	// finish and pass before the run reaches the stop.
	pipeline.Checks = longChecks{inner: pipeline.Checks, before: func(runID string) error {
		request := stopWaitedItsBound(runID)
		request.RequestedAt = time.Now()
		return store.RecordStop(request)
	}}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the change kept and reviewed", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the reviewed change integrated rather than cancelled", outcome)
	}
	if reviews := countRoleRequests(provider.Requests, "reviewer"); reviews != 1 {
		t.Fatalf("reviewer invocations = %d, want the kept change reviewed once", reviews)
	}
	saved, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if saved.LateStop == nil || saved.LateStop.RequestedBy != "the development manager" || saved.LateStop.ChecksPassedAt.IsZero() {
		t.Fatalf("late stop = %#v, want the stop recorded as arriving after the checks passed", saved.LateStop)
	}
	if !strings.Contains(tracker.Notes, "Stop arrived late: the development manager asked this run to stop") ||
		!strings.Contains(tracker.Notes, "went on to its review rather than being cancelled") {
		t.Fatalf("the work item was not told the stop arrived late: %q", tracker.Notes)
	}
	history, err := store.History(runstate.RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 || history.Runs[0].LateStop == nil || history.Runs[0].PendingStop != nil {
		t.Fatalf("summary = %#v, want the late stop on the ended run and nothing pending", history.Runs)
	}
}
