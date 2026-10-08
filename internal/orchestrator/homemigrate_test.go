package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run a redeploy preserved in a home laid out the earlier way is moved by
// `yoyo home migrate` with everything it holds — its record, its worktree and
// the change in it, its developer session, its counters — and a watching
// session on the machine home re-adopts it: the same run, from the worktree at
// its new path, which Git has registered there, re-earning the gate from its
// checks with no developer attempt spent and the item claimed once across the
// stop and the move. This is what the migration is for on a machine whose
// harness was running when the new build was deployed over it.
func TestAWatchingSessionReadoptsARunTheMigrationMoved(t *testing.T) {
	t.Parallel()

	earlier := t.TempDir()
	if err := os.MkdirAll(filepath.Join(earlier, "products", "yoyodyne"), 0o700); err != nil {
		t.Fatal(err)
	}
	machineHome := filepath.Join(t.TempDir(), ".yoyodyne")
	repository := pipelineRepository(t)
	store, err := runstate.NewStore(earlier, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	earlierWorktrees := home.WorktreeDirectory(earlier, "yoyodyne", "yoyodyne")
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}

	// The session before the deploy stops the run at its checks for its redeploy,
	// as TestRunLeavesARunStoppedForARedeployResumableAndReadoptsIt does.
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	running := filepath.Join(t.TempDir(), "checking")
	firstPipeline := automatic(newSharedPipeline(t, repository, earlierWorktrees, store, tracker, first,
		[]string{"touch '" + running + "' && sleep 60"}), first)
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	drained := RedeployDrain{At: time.Date(2026, 10, 7, 7, 50, 0, 0, time.UTC), Bound: 15 * time.Minute, SessionID: "watch-before"}
	go func() {
		for {
			if _, err := os.Stat(running); err == nil {
				stop(drained)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	paused, err := firstPipeline.Run(ctx, tracker.Item.ID)
	if err != nil || !paused.Paused || paused.RedeployStop == nil {
		t.Fatalf("Run() = %#v, %v; want the run preserved for the redeploy", paused, err)
	}
	before, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing holds the preserved run, so the migration moves it.
	if named, err := runstate.HomeInFlight(earlier); err != nil || len(named) != 0 {
		t.Fatalf("HomeInFlight() = %v, %v; want a preserved run with nothing behind it not counted as in flight", named, err)
	}
	now := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	migration, err := home.MoveHome(home.MigrateOptions{From: earlier, To: machineHome,
		Checkouts: map[string]string{"yoyodyne": repository}, Now: func() time.Time { return now }, BoundBy: "the test"})
	if err != nil || migration.Failed() {
		t.Fatalf("MoveHome() = %+v, %v", migration, err)
	}
	if _, left, err := runstate.RewriteMigratedWorktrees(earlier, machineHome, migration.Products, now, "the test"); err != nil || len(left) != 0 {
		t.Fatalf("RewriteMigratedWorktrees() = %+v, %v", left, err)
	}
	migrated, err := runstate.NewStore(machineHome, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := migrated.Load(paused.RunID)
	if err != nil {
		t.Fatalf("the run's record is not in the machine home: %v", err)
	}
	worktrees := home.WorktreeDirectory(machineHome, "yoyodyne", "yoyodyne")
	resolvedWorktrees, err := filepath.EvalSymlinks(worktrees)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(moved.WorktreePath) != resolvedWorktrees || moved.HomeMigration == nil || moved.HomeMigration.FromWorktreePath != before.WorktreePath {
		t.Fatalf("migrated record = %+v (migration %+v), want its worktree under %s with the earlier path kept", moved, moved.HomeMigration, worktrees)
	}
	if moved.RedeployStop == nil || moved.ProviderSessionID != before.ProviderSessionID || moved.RepairAttempts != before.RepairAttempts ||
		moved.ReviewRounds != before.ReviewRounds || moved.TransientRelaunches != before.TransientRelaunches || moved.Phase != before.Phase || moved.Branch != before.Branch {
		t.Fatalf("migrated record = %+v, want every counter, the session, the branch, and the stop as before: %+v", moved, before)
	}

	// The watching session on the machine home re-adopts it.
	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	secondPipeline := automatic(newSharedPipeline(t, repository, worktrees, migrated, tracker, second, []string{"exit 0"}), second)
	harness := newScheduleHarness()
	incomplete, err := migrated.Incomplete()
	if err != nil || len(incomplete) != 1 {
		t.Fatalf("Incomplete() = %v, %v; want the preserved run", incomplete, err)
	}
	harness.inFlight[incomplete[0].WorkItemID] = incomplete[0]
	var outcome Outcome
	attempts := 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// A re-adoption the pipeline did not take is tried again at every pull,
		// so a second attempt ends the session here rather than spinning until
		// the test's own time limit.
		if attempts++; attempts > 1 {
			t.Errorf("the migrated run was picked up %d times; the first attempt came to %#v", attempts, outcome)
			return h.complete(id), nil
		}
		result, err := secondPipeline.Run(context.Background(), id)
		outcome = result
		return result, err
	}
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, SessionID: "watch-" + strings.Repeat("b", 32)}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Readopted != paused.RunID {
		t.Fatalf("started = %#v, want the migrated run re-adopted", schedule.Started)
	}
	if outcome.RunID != paused.RunID || outcome.WorktreePath != moved.WorktreePath || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("outcome = %#v, want the migrated run finished from its new worktree", outcome)
	}
	if developers := second.RequestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("developer invocations after the move = %d, want none for a run stopped at its checks", len(developers))
	}
	if outcome.RepairAttempts != before.RepairAttempts {
		t.Fatalf("repair attempts = %d, want %d carried across the move", outcome.RepairAttempts, before.RepairAttempts)
	}
	if claims := countCalls(tracker.Calls, "claim"); claims != 1 {
		t.Fatalf("claims = %d, want the item claimed once across the stop and the move", claims)
	}
}
