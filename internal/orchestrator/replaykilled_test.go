package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// timingOutReplays stands in for a machine too busy to let a replay finish: the
// first kills replays are stopped on their budget, reported the way the
// worktree manager reports one — the worktree back on its branch at the commit
// it was at — and every replay after them is the real one. It keeps which
// retry each replay was asked as.
type timingOutReplays struct {
	WorktreeManager
	kills int

	mu      sync.Mutex
	retries []int
}

func (w *timingOutReplays) RebaseOntoTarget(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Rebase, error) {
	w.mu.Lock()
	w.retries = append(w.retries, gitworktree.ReplayRetry(ctx))
	killed := len(w.retries) <= w.kills
	w.mu.Unlock()
	if !killed {
		return w.WorktreeManager.RebaseOntoTarget(ctx, worktree, message)
	}
	return gitworktree.Rebase{
			Branch:             worktree.Branch,
			TargetBranch:       worktree.TargetBranch,
			BaseCommit:         worktree.BaseCommit,
			HeadCommit:         worktree.HarnessCommit,
			PreviousBaseCommit: worktree.BaseCommit,
			PreviousHeadCommit: worktree.HarnessCommit,
		}, fmt.Errorf("%w: replay %s onto %s at 0123abcd timed out; its budget was 1m0s, bought for replaying 1 commit(s) that write 2 file(s)",
			gitworktree.ErrReplayKilled, worktree.Branch, worktree.TargetBranch)
}

func (w *timingOutReplays) asked() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.retries)
}

// recordedWaits is a pipeline's Sleep that waits for nothing and keeps what it
// was asked to wait.
type recordedWaits struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (r *recordedWaits) sleep(_ context.Context, wait time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waits = append(r.waits, wait)
	return nil
}

func (r *recordedWaits) of(waits ...time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, wait := range waits {
		if !slices.Contains(r.waits, wait) {
			return false
		}
	}
	return true
}

// An approved change whose replay onto the moved main the harness stops on its
// time limit once is asked again by the harness on its own, after a wait and
// with the limit doubled, and lands: no person, no developer, and no repair
// attempt is spent on it, and the run's record says the replay was retried.
func TestAReplayStoppedOnItsTimeLimitOnceIsRetriedAndLands(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
	scriptedRaces(t, repository, provider, 1)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	replays := &timingOutReplays{WorktreeManager: pipeline.Worktrees, kills: 1}
	pipeline.Worktrees = replays
	waits := &recordedWaits{}
	pipeline.Sleep = waits.sleep

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed || tracker.Blocked {
		t.Fatalf("Run() outcome = %#v, want the change landed and the item closed", outcome)
	}
	if asked := replays.asked(); !slices.Equal(asked, []int{0, 1}) {
		t.Fatalf("replays asked as retries %v, want the first and one retry with its limit doubled", asked)
	}
	if !waits.of(replayKilledWaits[0]) {
		t.Fatalf("waits = %v, want the %s before the retry", waits.waits, replayKilledWaits[0])
	}
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer invocations = %d, want 1", len(developers))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.RepairAttempts != 0 || state.ChargedReplays != 0 || state.IntegrationRetries != 1 {
		t.Fatalf("durable record: repairs %d, charged replays %d, races %d; want nothing spent beyond the one race lost",
			state.RepairAttempts, state.ChargedReplays, state.IntegrationRetries)
	}
	if attempts := state.RetryAttempts(runstate.RetryReplay); attempts != 1 {
		t.Fatalf("replay retries recorded = %d, want 1", attempts)
	}
}

// A replay the harness stops on its time limit every time it is asked is asked
// replayKilledRetries times more, each after its wait and on a doubled limit,
// and then the run stops: as an integration stop outside the work, with no
// repair attempt spent, the change kept on its branch, and the run on the
// development manager's list with the retries named in its reason.
func TestAReplayStoppedOnItsTimeLimitEveryTimeIsHeldForTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
	scriptedRaces(t, repository, provider, 1)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	replays := &timingOutReplays{WorktreeManager: pipeline.Worktrees, kills: replayKilledRetries + 1}
	pipeline.Worktrees = replays
	waits := &recordedWaits{}
	pipeline.Sleep = waits.sleep
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	reason := fmt.Sprintf("stopped the replay on its time limit %d times", replayKilledRetries+1)
	if err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("Run() error = %v, want the run stopped naming the %d replays stopped on time", err, replayKilledRetries+1)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("Run() outcome = %#v, want nothing promoted", outcome)
	}
	assertSavedStopClass(t, store, outcome.RunID, runstate.CauseReplayKilled.StopClass())
	if asked := replays.asked(); !slices.Equal(asked, []int{0, 1, 2, 3}) {
		t.Fatalf("replays asked as retries %v, want the first and %d retries", asked, replayKilledRetries)
	}
	if !waits.of(replayKilledWaits[:]...) {
		t.Fatalf("waits = %v, want %v before the retries", waits.waits, replayKilledWaits)
	}
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer invocations = %d, want nothing handed back", len(developers))
	}

	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationStop == nil || state.IntegrationStop.Cause != runstate.CauseReplayKilled {
		t.Fatalf("integration stop = %#v, want %q", state.IntegrationStop, runstate.CauseReplayKilled)
	}
	if state.RepairAttempts != 0 || state.ChargedReplays != 0 {
		t.Fatalf("durable record: repairs %d, charged replays %d; want nothing charged to the change", state.RepairAttempts, state.ChargedReplays)
	}
	if attempts := state.RetryAttempts(runstate.RetryReplay); attempts != replayKilledRetries {
		t.Fatalf("replay retries recorded = %d, want %d", attempts, replayKilledRetries)
	}
	if state.WorktreeRemoved || state.BranchRemoved {
		t.Fatalf("worktree removed %t, branch removed %t; want both kept", state.WorktreeRemoved, state.BranchRemoved)
	}
	if shown := gitOutput(t, repository, "show", state.Branch+":feature.txt"); !strings.Contains(shown, "implemented") {
		t.Fatalf("branch %s does not carry the change: %q", state.Branch, shown)
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != outcome.RunID {
		t.Fatalf("docket = %#v, want the run listed once", entries)
	}
	if !strings.Contains(entries[0].Failure, reason) {
		t.Fatalf("listed failure = %q, want it to name the retries", entries[0].Failure)
	}
}
