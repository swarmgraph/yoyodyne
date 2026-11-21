package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// refusingSpends is a shared record of spends whose remote refuses the first
// refusals tries and then takes the spend.
type refusingSpends struct {
	refusals int
	asked    int
}

func (r *refusingSpends) Spend(_ context.Context, workItemID string, spend runstate.SharedSpend, within func(runstate.SharedSpendCounts) error) error {
	r.asked++
	if r.asked <= r.refusals {
		return &runstate.SpendNotSharedError{WorkItemID: workItemID, Budget: spend.Budget, Problem: "the remote refused the push"}
	}
	return within(runstate.SharedSpendCounts{})
}

type spendWaitClock struct{ now time.Time }

func (c *spendWaitClock) Now() time.Time { return c.now }

// A review round the shared remote will not take is shown on the run's record as
// a wait, with when it began and how many tries it has had, while the run asks
// again; and the wait is gone once the remote takes the round.
func TestARefusedReviewRoundIsShownAsWaiting(t *testing.T) {
	t.Parallel()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	spends := &refusingSpends{refusals: 3}
	store.UseSharedSpends(spends)
	clock := &spendWaitClock{now: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)}
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: "yoyodyne", RepositoryID: "yoyodyne",
		WorkItemID: "yoyodyne-ifd.1", Backend: domain.BackendClaudeCode, Status: runstate.StatusRunning,
		StartedAt: started, UpdatedAt: started,
	}
	var seen []runstate.SpendWait
	run := &activeRun{
		pipeline: Pipeline{Store: store, Worktrees: orchestratortest.PartialWorktreeManager{}, Clock: clock},
		state:    state,
	}
	// A change is present, so the verdict that sent it back is a round.
	run.pipeline.Worktrees = changedWorktrees{}
	run.pipeline.Sleep = func(_ context.Context, d time.Duration) error {
		if d != teamSpendRetryInterval {
			t.Errorf("slept %s, want %s", d, teamSpendRetryInterval)
		}
		saved, err := store.Load(runID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.SpendWait == nil {
			t.Fatalf("the run's record names no wait while it sleeps")
		}
		seen = append(seen, *saved.SpendWait)
		if says := runinspect.WaitOf(saved); says == nil || !strings.Contains(says.Kind, "shared remote") || says.Since == nil {
			t.Errorf("the inspector's wait = %+v, want one on the shared remote with its start", says)
		}
		clock.now = clock.now.Add(d)
		return nil
	}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	if err := run.recordReviewVerdict(context.Background(), review.DecisionRepair); err != nil {
		t.Fatalf("recordReviewVerdict() error = %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d waits, want one per refused try", len(seen))
	}
	for i, wait := range seen {
		if wait.Tries != i+1 {
			t.Errorf("try %d recorded %d tries", i+1, wait.Tries)
		}
		if !wait.Since.Equal(seen[0].Since) {
			t.Errorf("the wait's start moved from %s to %s", seen[0].Since, wait.Since)
		}
	}
	if says := seen[2].Summary(); !strings.Contains(says, "shared remote has not taken") || !strings.Contains(says, "3 tries") {
		t.Errorf("summary = %q, want the remote, and the number of tries", says)
	}
	saved, err := store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.SpendWait != nil {
		t.Errorf("the wait is still recorded after the remote took the round: %+v", saved.SpendWait)
	}
}

// changedWorktrees reports a change in the worktree, which is what makes a
// verdict that sent the work back a round.
type changedWorktrees struct {
	orchestratortest.PartialWorktreeManager
}

func (changedWorktrees) ChangedPaths(context.Context, gitworktree.Worktree) ([]string, error) {
	return []string{"a.go"}, nil
}
