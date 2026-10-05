package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// checkoutRecovery restores a recorded checkout for either a repair decision or
// an automatic check continuation. Its caller holds the run's lease and checks
// intake and capacity before restoration writes anything.
type checkoutRecovery struct {
	Runs      RepairRuns
	Worktrees RepairWorktrees
	Clock     execution.Clock
}

func (c checkoutRecovery) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}

// Clear verification credit durably before touching the filesystem. If the
// process dies after Git restores the directory, a later carry-out sees the
// same stopped run and decision, with no old checks it can accidentally reuse.
func (c checkoutRecovery) restoreCheckout(ctx context.Context, prior runstate.State) (runstate.State, error) {
	worktrees, ok := c.Worktrees.(RestorableWorktrees)
	if !ok {
		return prior, fmt.Errorf("run %s's checkout is missing and no harness restoration is wired; outstanding recovery and surviving artifacts are kept", prior.RunID)
	}
	prior.ChecksPassed = nil
	// A repaired change must earn promotion again. Clear the recorded
	// integration with its approval so the stopped record remains valid.
	prior.Integration = nil
	prior.CheckoutRestorePending = true
	if prior.ReviewDecision == runstate.ReviewApprove {
		prior.ReviewDecision = ""
		prior.ReviewApproves = ""
		prior.ReviewSummary = ""
	}
	if prior.Phase == runstate.PhaseReviewing {
		prior.Phase = runstate.PhaseChecking
	}
	prior.UpdatedAt = c.now()
	if err := c.Runs.Save(prior); err != nil {
		return prior, fmt.Errorf("invalidate verification before restoring run %s's checkout: %w", prior.RunID, err)
	}
	if _, err := worktrees.RestoreWorktree(ctx, worktreeOf(prior)); err != nil {
		return prior, fmt.Errorf("restore the checkout of run %s from its recorded branch %s: %w; available artifacts are kept and no continuation was spent", prior.RunID, prior.Branch, err)
	}
	return c.recordRestoredCheckout(ctx, prior)
}

func (c checkoutRecovery) verifyRestoredCheckout(ctx context.Context, prior runstate.State) error {
	worktrees, ok := c.Worktrees.(RestorableWorktrees)
	if !ok {
		return fmt.Errorf("run %s has an unfinished checkout restoration and no harness restoration is wired", prior.RunID)
	}
	if err := c.Worktrees.VerifyOwnedHead(ctx, worktreeOf(prior)); err != nil {
		return WorktreeSurgeryError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}
	}
	inspection, err := worktrees.Inspect(ctx, worktreeOf(prior))
	if err != nil {
		return fmt.Errorf("verify the complete restored checkout of run %s: %w", prior.RunID, err)
	}
	if !inspection.Registered || inspection.Branch != prior.Branch || inspection.Dirty {
		return fmt.Errorf("run %s's checkout restoration is unfinished or has uncommitted changes; available artifacts are kept, and missing work is not claimed recovered", prior.RunID)
	}
	return nil
}

func (c checkoutRecovery) recordRestoredCheckout(ctx context.Context, prior runstate.State) (runstate.State, error) {
	if err := c.verifyRestoredCheckout(ctx, prior); err != nil {
		return prior, err
	}
	prior.CheckoutRestorePending = false
	prior.WorktreeRemoved = false
	prior.WorktreeSweptAt = nil
	prior.BranchRemoved = false
	prior.BranchSweptAt = nil
	prior.UpdatedAt = c.now()
	if err := c.Runs.Save(prior); err != nil {
		return prior, fmt.Errorf("record the restored checkout of run %s: %w", prior.RunID, err)
	}
	return prior, nil
}
