package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// noDeveloper is a provider that fails the test if a developer or reviewer is
// asked anything: a take-up that is refused gets no further than its checks.
func noDeveloper(t *testing.T) *orchestratortest.Backend {
	t.Helper()
	return orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("the %s was invoked by a take-up that should have been refused", request.Role)
		return nil
	}, approveVerdict)
}

// assertWaitingAgain fails unless the run is still in flight, waiting on its
// dependency again and holding no developer slot, with its branch and
// worktree, and the change in it, where they were.
func (f dependencyPhaseFixture) assertWaitingAgain(t *testing.T, before runstate.State) {
	t.Helper()
	after := f.load(t)
	if !after.Status.InFlight() || after.DependencyPause == nil || after.HoldsDeveloperSlot() {
		t.Fatalf("after the refused take-up: status %q, dependency pause %#v, holds a developer slot %t; want the run in flight, waiting again and holding none",
			after.Status, after.DependencyPause, after.HoldsDeveloperSlot())
	}
	if after.Branch != before.Branch || after.WorktreePath != before.WorktreePath || after.ProviderSessionID != before.ProviderSessionID {
		t.Fatalf("after the refused take-up the run's branch, worktree or session changed: before %q %q %q, after %q %q %q",
			before.Branch, before.WorktreePath, before.ProviderSessionID, after.Branch, after.WorktreePath, after.ProviderSessionID)
	}
	if _, err := os.Stat(filepath.Join(after.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the change the run made is gone from its worktree: %v", err)
	}
}

// A run paused on its item's dependency is taken up once the dependency
// closes, takes its developer slot back, and is then refused because its item
// reads blocked. The slot is given back at once: the run waits again, as it
// did before the take-up, and holds no slot.
func TestARefusedContinuationOfAPausedRunGivesItsSlotBack(t *testing.T) {
	t.Parallel()
	fixture := newDependencyPhaseFixture(t)
	before := fixture.load(t)
	fixture.release()
	fixture.tracker.Item.Status = "blocked"

	_, err := fixture.pipeline(t, noDeveloper(t)).Run(context.Background(), fixture.tracker.Item.ID)
	var refused ItemStatusRefusal
	if !errors.As(err, &refused) {
		t.Fatalf("Run() error = %v, want the take-up refused over the item reading blocked", err)
	}
	fixture.assertWaitingAgain(t, before)
}

// stuckWorktrees is a worktree manager whose readiness check does not return
// for a take-up under way: a check made with a deadline waits until it
// passes. Checks made with no deadline, before the slot is taken back, answer
// at once.
type stuckWorktrees struct {
	WorktreeManager
}

func (w stuckWorktrees) ValidateReady(ctx context.Context) error {
	if _, bounded := ctx.Deadline(); !bounded {
		return w.WorktreeManager.ValidateReady(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}

// A take-up that goes silent once it holds its developer slot — here the
// checkout's readiness check never answers — is ended at the take-up limit.
// The run keeps its branch, worktree, session and change, and gives the slot
// back.
func TestASilentTakeUpIsEndedAtItsLimitWithItsWorkKept(t *testing.T) {
	t.Parallel()
	fixture := newDependencyPhaseFixture(t)
	before := fixture.load(t)
	fixture.release()

	pipeline := fixture.pipeline(t, noDeveloper(t))
	pipeline.Worktrees = stuckWorktrees{WorktreeManager: pipeline.Worktrees}
	pipeline.TakeUpLimit = 50 * time.Millisecond
	started := time.Now()
	_, err := pipeline.Run(context.Background(), fixture.tracker.Item.ID)
	var ended TakeUpTimeoutError
	if !errors.As(err, &ended) || ended.Limit != pipeline.TakeUpLimit || ended.RunID != before.RunID {
		t.Fatalf("Run() error = %v, want the take-up of run %s ended at its limit", err, before.RunID)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want it to carry what the stuck step returned", err)
	}
	if waited := time.Since(started); waited > time.Minute {
		t.Fatalf("the take-up was ended after %s, want it ended near its limit", waited)
	}
	fixture.assertWaitingAgain(t, before)
}

// A take-up refused after it took its slot back gives the slot back whatever
// the run waited on: its epic, or GitHub's checks, including a wait an
// earlier take-up marked as holding the slot and then lost.
func TestGiveSlotBackPutsBackEveryKindOfWait(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 18, 26, 0, 0, time.UTC)
	ciWait := func(taken bool) *runstate.CIWait {
		return &runstate.CIWait{Commit: "9afda54ec758f261fd2f6dd6bc74645d6c9b98e8", Branch: "yoyodyne/yoyodyne-task/run", PushedAt: now, Required: []string{"test"}, SlotTaken: taken}
	}
	for _, test := range []struct {
		name string
		wait func(*runstate.State)
	}{
		{"epic", func(state *runstate.State) { state.EpicWait = &runstate.EpicWait{Since: now} }},
		{"github checks", func(state *runstate.State) { state.CIWait = ciWait(false) }},
		{"github checks a lost take-up held", func(state *runstate.State) { state.CIWait = ciWait(true) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newDependencyPhaseFixture(t)
			waiting := fixture.load(t)
			waiting.DependencyPause = nil
			test.wait(&waiting)
			// The run as a take-up leaves it once it has taken the slot back and
			// been refused: no wait it holds no slot for.
			taken := waiting
			taken.EpicWait = nil
			if taken.CIWait != nil {
				held := *taken.CIWait
				held.SlotTaken = true
				taken.CIWait = &held
			}
			if err := fixture.store.Save(taken); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			if !fixture.load(t).HoldsDeveloperSlot() {
				t.Fatal("the taken-up record holds no developer slot, so the test proves nothing")
			}
			pipeline := fixture.pipeline(t, noDeveloper(t))
			if err := pipeline.giveSlotBack(context.Background(), waiting); err != nil {
				t.Fatalf("giveSlotBack() error = %v", err)
			}
			after := fixture.load(t)
			if after.HoldsDeveloperSlot() || !after.Status.InFlight() {
				t.Fatalf("after giving the slot back: holds a slot %t, status %q; want a run in flight holding none", after.HoldsDeveloperSlot(), after.Status)
			}
			if (waiting.EpicWait != nil) != (after.EpicWait != nil) || (waiting.CIWait != nil) != (after.CIWait != nil) {
				t.Fatalf("after giving the slot back: epic wait %#v, checks wait %#v; want the wait the run was taken up from", after.EpicWait, after.CIWait)
			}
		})
	}
}
