package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordReconcileFinding re-reads the run under its lease after the settlement
// has released it. Saving before delivery preserves a refused note for retry;
// saving after delivery prevents the same refusal being announced each pass.
func (r Reconciler) recordReconcileFinding(ctx context.Context, runID string, step runstate.ReconcileStep, problem string) (*readmodel.Attention, string) {
	state, lease, err := r.Store.AdoptRun(ctx, runID)
	if errors.Is(err, runstate.ErrRunHeld) {
		return nil, ""
	}
	if err != nil {
		return nil, fmt.Sprintf("record settlement finding for run %s: %v", runID, err)
	}
	defer lease.Release()
	if (step == runstate.ReconcileRun || step == runstate.ReconcilePublication) && problem == "" && state.PullRequest != nil && state.PullRequest.Merged {
		problem = state.PublishFailure
	}
	// Both settlement paths retry the same merged publication obligation.
	if strings.HasPrefix(problem, "delete the merged remote branch") || strings.HasPrefix(problem, "confirm the queued merge reached") || strings.HasPrefix(problem, "confirm the merge reached") {
		step = runstate.ReconcilePublication
	}
	index := slices.IndexFunc(state.ReconcileFindings, func(f runstate.ReconcileFinding) bool { return f.Step == step })
	if problem == "" {
		before := len(state.ReconcileFindings)
		state.ReconcileFindings = slices.DeleteFunc(state.ReconcileFindings, func(f runstate.ReconcileFinding) bool {
			return f.Step == step || (step == runstate.ReconcilePublication && f.Step == runstate.ReconcileRun && !state.Outstanding())
		})
		if len(state.ReconcileFindings) != before {
			if err := r.Store.Save(state); err != nil {
				return nil, fmt.Sprintf("clear settlement finding for run %s: %v", runID, err)
			}
		}
		return nil, ""
	}
	problem = runstate.RecordReconcileProblem(problem)
	if index < 0 {
		state.ReconcileFindings = append(state.ReconcileFindings, runstate.ReconcileFinding{Step: step, Problem: problem, Pending: true})
		index = len(state.ReconcileFindings) - 1
	} else if state.ReconcileFindings[index].Problem != problem {
		state.ReconcileFindings[index].Problem = problem
		state.ReconcileFindings[index].Pending = true
	}
	finding := &state.ReconcileFindings[index]
	view := state
	view.ReconcileFindings = []runstate.ReconcileFinding{*finding}
	attention := readmodel.ReconcileFindingAttention(view)
	if !finding.Pending {
		return &attention, ""
	}
	if err := r.Store.Save(state); err != nil {
		return &attention, fmt.Sprintf("save settlement finding for run %s: %v", runID, err)
	}
	note := "Settlement finding: " + attention.CitedWhat() + "\nRun: " + state.RunID + "\nNext move: " + attention.CitedWhose()
	// A previous delivery may have succeeded just before its marker save failed.
	// The item's existing notes are the other durable copy of that delivery.
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	item, readErr := r.Tracker.Show(readCtx, state.WorkItemID)
	cancel()
	if readErr != nil {
		return &attention, fmt.Sprintf("read prior settlement notes of %s: %v", state.WorkItemID, readErr)
	}
	if !strings.Contains(item.Notes, note) {
		if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, note); err != nil {
			return &attention, fmt.Sprintf("deliver settlement finding to %s: %v", state.WorkItemID, err)
		}
	}
	finding.Pending = false
	if err := r.Store.Save(state); err != nil {
		return &attention, fmt.Sprintf("record delivered settlement finding for run %s: %v", runID, err)
	}
	return &attention, ""
}

func nonEmptyProblems(problems ...string) []string {
	return slices.DeleteFunc(problems, func(problem string) bool { return problem == "" })
}
