package orchestrator

// A change the harness's merge queue hands back only because the target moved
// under it gets its run's one attempt to catch up, end to end through a real
// run, a real queue, and a real run store: the run is put back live rather
// than stopped, its developer reconciles the conflict on top of the target,
// the change is checked and reviewed again and rejoins the queue by itself,
// and no repair attempt or fresh run is spent. A second conflict, or a
// catch-up whose result fails its checks with no repair attempts left, stops
// the run for the development manager as before, saying why.

import (
	"context"
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

const conflictFree = "test -f feature.txt && ! grep -q '<<<<<<<' feature.txt"

// newCatchUpRun is a queue run whose developer writes feature.txt on its
// first attempt and, on every later one, does what resolve says to the
// worktree it is handed. Its checks refuse a feature.txt still holding
// conflict markers.
func newCatchUpRun(t *testing.T, resolve func(dir string) error) (*queueRun, *orchestratortest.Backend) {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Add the feature", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
		}
		return resolve(request.WorkingDirectory)
	}, approveVerdict)
	pipeline, runs := newAutomaticPipeline(t, repository, tracker, provider, []string{conflictFree})
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue
	pipeline.Config.Execution.MergeQueue = true
	return &queueRun{repository: repository, tracker: tracker, pipeline: pipeline, runs: runs, queue: queue}, provider
}

// catchUpDriver is the queue's driver with the hand-back wired as the product
// wires it: able to give a run its catch-up.
func (r *queueRun) catchUpDriver() MergeQueueDriver {
	driver := r.driver()
	driver.Promoter.Repair = MergeQueueRunHandback{
		Store: r.runs, Tracker: r.tracker,
		CatchUp: r.runs, Capacity: r.pipeline.Config.Execution.MaxConcurrentDevelopers,
	}
	return driver
}

func (r *queueRun) catchUpPass(t *testing.T, key runstate.MergeQueueKey) runstate.MergeQueuePass {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return r.catchUpDriver().Pass(ctx, key)
}

// conflictOnTarget lands on the target its own feature.txt, which the queued
// change also adds, so the queued head no longer merges onto it.
func conflictOnTarget(t *testing.T, repository, content string) {
	t.Helper()
	writeQueueFile(t, repository, "feature.txt", content)
	runPipelineGit(t, repository, "add", "feature.txt")
	runPipelineGit(t, repository, "commit", "-m", "the target's own feature.txt")
}

// resolveBothSides is a developer that keeps both answers.
func resolveBothSides(dir string) error {
	return os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("implemented\nsomething else\n"), 0o600)
}

func TestAChangeHandedBackOnlyBecauseTheTargetMovedCatchesUpAndRejoinsTheQueue(t *testing.T) {
	t.Parallel()

	r, provider := newCatchUpRun(t, resolveBothSides)
	admitted := r.run(t)
	if admitted.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", admitted)
	}
	key := admitted.MergeQueue.Key()
	conflictOnTarget(t, r.repository, "something else\n")

	pass := r.catchUpPass(t, key)
	if pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflicting change handed back", pass)
	}
	put, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	// Put back live for its catch-up, not stopped: nothing for the development
	// manager to decide, the item still in progress, and nothing charged.
	if put.Status != runstate.StatusRunning || put.Phase != runstate.PhaseDeveloping || strings.TrimSpace(put.Blocker) != "" {
		t.Fatalf("run = status %q, phase %q, blocker %q; want it live at its developer attempt", put.Status, put.Phase, put.Blocker)
	}
	if put.Reconciliation == nil || put.ReplayConflict == nil || !put.ReplayConflict.Reconciliation || put.RepairAttempts != 0 {
		t.Fatalf("run = reconciliation %#v, conflict %#v, repair attempts %d; want the conflict recorded as its one free attempt", put.Reconciliation, put.ReplayConflict, put.RepairAttempts)
	}
	if !catchUpNotTakenUp(put) || !owedUpdateContinuation(put) || !put.CatchingUpWithMergeQueueTarget() {
		t.Fatal("the run put back is not one the reconciling sweep hosts")
	}
	if record := r.tracker.Record(); record.Blocked || !strings.Contains(record.Notes, "gets its one attempt to bring the change up to date") || !strings.Contains(record.Notes, "not a repair attempt and not a fresh run") {
		t.Fatalf("blocked = %t, notes:\n%s\nwant the item told, not blocked", record.Blocked, record.Notes)
	}
	// Giving the same hand-back again (a later pass, after a note that failed)
	// changes nothing and writes the note once.
	landing, _, err := r.queue.Landing(key, admitted.MergeQueue.EntryID)
	if err != nil || landing.Handback == nil {
		t.Fatalf("landing = %#v, %v", landing, err)
	}
	entry, _, err := r.catchUpDriver().Promoter.entry(key, admitted.MergeQueue.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.catchUpDriver().Promoter.Repair.HandBack(context.Background(), entry, *landing.Handback); err != nil {
		t.Fatalf("HandBack() again error = %v", err)
	}
	if notes := r.tracker.Record().Notes; strings.Count(notes, "gets its one attempt to bring the change up to date") != 1 {
		t.Fatalf("notes:\n%s\nwant the catch-up said once", notes)
	}

	// What the sweep's ContinueUpdates does for it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rejoined, err := r.pipeline.Continue(ctx, "yoyodyne-task", admitted.RunID)
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if rejoined.Status != runstate.StatusSucceeded || rejoined.MergeQueue == nil || rejoined.MergeQueue.EntryID == admitted.MergeQueue.EntryID {
		t.Fatalf("Continue() = %#v, want the reconciled change admitted to the queue again", rejoined)
	}
	if provider.DeveloperAttempts != 2 || rejoined.RepairAttempts != 0 {
		t.Fatalf("developer attempts = %d, repair attempts = %d; want one catch-up attempt charged to nothing", provider.DeveloperAttempts, rejoined.RepairAttempts)
	}
	runs, err := r.runs.Recorded()
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %d, %v; want the same run, no fresh one", len(runs), err)
	}

	// And the queue lands it, on the target as it now stands.
	if pass := r.catchUpPass(t, key); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the reconciled change landed", pass)
	}
	if got := readQueueFile(t, r.repository, "feature.txt"); got != "implemented\nsomething else\n" {
		t.Fatalf("feature.txt on the target = %q, want the reconciled change", got)
	}
	if !r.tracker.Closed {
		t.Fatal("the item was not closed when the reconciled change landed")
	}
}

func TestASecondConflictInTheQueueStopsTheRunSayingItsCatchUpWasUsed(t *testing.T) {
	t.Parallel()

	r, _ := newCatchUpRun(t, resolveBothSides)
	admitted := r.run(t)
	key := admitted.MergeQueue.Key()
	conflictOnTarget(t, r.repository, "something else\n")
	if pass := r.catchUpPass(t, key); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the first conflict handed back", pass)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rejoined, err := r.pipeline.Continue(ctx, "yoyodyne-task", admitted.RunID)
	if err != nil || rejoined.MergeQueue == nil {
		t.Fatalf("Continue() = %#v, %v; want the change back in the queue", rejoined, err)
	}

	// The target moves under it again.
	conflictOnTarget(t, r.repository, "a third answer\n")
	if pass := r.catchUpPass(t, key); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the second conflict handed back", pass)
	}
	stopped, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != runstate.StatusFailed || strings.TrimSpace(stopped.Blocker) == "" || stopped.ReplayConflict == nil {
		t.Fatalf("run = status %q, blocker %q; want it stopped for the development manager with the conflict kept", stopped.Status, stopped.Blocker)
	}
	if !strings.Contains(stopped.Blocker, "already used its one attempt to bring its change up to date") {
		t.Fatalf("blocker = %q, want it to say the catch-up was already used", stopped.Blocker)
	}
	if record := r.tracker.Record(); !record.Blocked {
		t.Fatal("the item was not blocked on the second conflict")
	}
}

func TestACatchUpThatFailsItsChecksStopsForTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	// The developer leaves the conflict markers where they are, every time.
	r, _ := newCatchUpRun(t, func(string) error { return nil })
	r.pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1
	admitted := r.run(t)
	key := admitted.MergeQueue.Key()
	conflictOnTarget(t, r.repository, "something else\n")
	if pass := r.catchUpPass(t, key); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflict handed back", pass)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	outcome, _ := r.pipeline.Continue(ctx, "yoyodyne-task", admitted.RunID)
	stopped, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != runstate.StatusFailed || strings.TrimSpace(stopped.Blocker) == "" || stopped.CheckFailure == nil {
		t.Fatalf("run = status %q, blocker %q, check failure %#v (outcome %#v); want it stopped on its failing check", stopped.Status, stopped.Blocker, stopped.CheckFailure, outcome)
	}
	if stopped.Reconciliation == nil || stopped.MergeQueue == nil || stopped.MergeQueue.EntryID != admitted.MergeQueue.EntryID {
		t.Fatalf("reconciliation = %#v, queue admission = %#v; want the catch-up recorded and nothing admitted again", stopped.Reconciliation, stopped.MergeQueue)
	}
	if record := r.tracker.Record(); !record.Blocked {
		t.Fatal("the item was not blocked when the catch-up failed")
	}
}
