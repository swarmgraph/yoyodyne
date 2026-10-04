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
	// Another settlement can record the forge's final answer before the refresh
	// sweep revisits the request. That answer settles its earlier read refusal.
	if published := state.PullRequest; published != nil && (published.Merged || published.Superseded != "" || published.HandedBack != nil || strings.EqualFold(strings.TrimSpace(published.State), "CLOSED")) {
		attention, problem := r.clearReconcileFindings(ctx, &state, func(f runstate.ReconcileFinding) bool {
			return f.Step == runstate.ReconcileRefresh
		})
		if problem != "" {
			return attention, problem
		}
		if step == runstate.ReconcileRefresh {
			return nil, ""
		}
	}
	if (step == runstate.ReconcileRun || step == runstate.ReconcilePublication) && problem == "" && state.PullRequest != nil && state.PullRequest.Merged {
		problem = state.PublishFailure
	}
	// Both settlement paths retry the same merged publication obligation.
	if strings.HasPrefix(problem, "delete the merged remote branch") || strings.HasPrefix(problem, "confirm the queued merge reached") || strings.HasPrefix(problem, "confirm the merge reached") {
		step = runstate.ReconcilePublication
	}
	index := slices.IndexFunc(state.ReconcileFindings, func(f runstate.ReconcileFinding) bool { return f.Step == step })
	if problem == "" {
		return r.clearReconcileFindings(ctx, &state, func(f runstate.ReconcileFinding) bool {
			return f.Step == step || (step == runstate.ReconcilePublication && f.Step == runstate.ReconcileRun && !state.Outstanding())
		})
	}
	problem = runstate.RecordReconcileProblem(problem)
	if index < 0 {
		state.ReconcileFindings = append(state.ReconcileFindings, runstate.ReconcileFinding{Step: step, Problem: problem, Pending: true})
		index = len(state.ReconcileFindings) - 1
	} else if state.ReconcileFindings[index].Problem != problem || state.ReconcileFindings[index].Resolved {
		state.ReconcileFindings[index].Problem = problem
		state.ReconcileFindings[index].Pending = true
		state.ReconcileFindings[index].Resolved = false
	}
	if state.ReconcileFindings[index].Pending {
		if err := r.Store.Save(state); err != nil {
			view := state
			view.ReconcileFindings = []runstate.ReconcileFinding{state.ReconcileFindings[index]}
			attention := readmodel.ReconcileFindingAttention(view)
			return &attention, fmt.Sprintf("save settlement finding for run %s: %v", runID, err)
		}
	}
	return r.deliverReconcileFinding(ctx, &state, index)
}

// clearReconcileFindings requires the run lease and proof of resolution from
// its caller. A resolved operation can still owe delivery of its saved finding;
// finish that delivery before clearing its only pending marker.
func (r Reconciler) clearReconcileFindings(ctx context.Context, state *runstate.State, resolved func(runstate.ReconcileFinding) bool) (*readmodel.Attention, string) {
	// Persist resolution before attempting delivery. The operation can now fall
	// out of its sweep's candidates without losing the remaining obligation.
	candidate := *state
	candidate.ReconcileFindings = slices.Clone(state.ReconcileFindings)
	changed := false
	for index, finding := range candidate.ReconcileFindings {
		if resolved(finding) && !finding.Resolved {
			candidate.ReconcileFindings[index].Resolved = true
			changed = true
		}
	}
	if changed {
		if err := r.Store.Save(candidate); err != nil {
			attention := readmodel.ReconcileFindingAttention(*state)
			return &attention, fmt.Sprintf("record resolved settlement finding for run %s: %v", state.RunID, err)
		}
		*state = candidate
	}
	for index, finding := range state.ReconcileFindings {
		if resolved(finding) && finding.Pending {
			attention, problem := r.deliverReconcileFinding(ctx, state, index)
			if problem != "" {
				return attention, problem
			}
		}
	}
	candidate = *state
	candidate.ReconcileFindings = slices.DeleteFunc(slices.Clone(state.ReconcileFindings), resolved)
	if len(candidate.ReconcileFindings) != len(state.ReconcileFindings) {
		if err := r.Store.Save(candidate); err != nil {
			attention := readmodel.ReconcileFindingAttention(*state)
			return &attention, fmt.Sprintf("clear settlement finding for run %s: %v", state.RunID, err)
		}
		*state = candidate
	}
	return nil, ""
}

// deliverReconcileFinding requires the caller's run lease. It also runs after
// resolution, because a failed note is an obligation separate from the artifact
// that originally produced the finding.
func (r Reconciler) deliverReconcileFinding(ctx context.Context, state *runstate.State, index int) (*readmodel.Attention, string) {
	finding := &state.ReconcileFindings[index]
	view := *state
	view.ReconcileFindings = []runstate.ReconcileFinding{*finding}
	attention := readmodel.ReconcileFindingAttention(view)
	if !finding.Pending {
		return &attention, ""
	}
	// The historical note stays identical after resolution, including when a
	// delivery marker save failed. Attention describes only what is still owed.
	refusalView := *state
	refusalFinding := *finding
	refusalFinding.Resolved = false
	refusalView.ReconcileFindings = []runstate.ReconcileFinding{refusalFinding}
	refusal := readmodel.ReconcileFindingAttention(refusalView)
	note := "Settlement finding: " + refusal.CitedWhat() + "\nRun: " + state.RunID + "\nNext move: " + refusal.CitedWhose()
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
	if err := r.Store.Save(*state); err != nil {
		finding.Pending = true
		return &attention, fmt.Sprintf("record delivered settlement finding for run %s: %v", state.RunID, err)
	}
	return &attention, ""
}

// ReconcileFindingMaintenance reports obligations revisited independently of
// the operation that created them. It uses the same saved findings and shared
// attention projection as the operation's own result.
type ReconcileFindingMaintenance struct {
	RunID          string                   `json:"run_id"`
	WorkItemID     string                   `json:"work_item_id"`
	WorkItemTitle  string                   `json:"work_item_title,omitempty"`
	Cleared        []runstate.ReconcileStep `json:"cleared"`
	Finding        *readmodel.Attention     `json:"finding,omitempty"`
	FindingProblem string                   `json:"finding_problem,omitempty"`
}

// maintainReconcileFindings visits every saved finding, including runs no
// operation sweep selects any more. Discovery failure prevents the pass from
// reading its state; one run's delivery or save refusal never stops the rest.
func (r Reconciler) maintainReconcileFindings(ctx context.Context) ([]ReconcileFindingMaintenance, error) {
	recorded, err := r.Store.Recorded()
	if err != nil {
		return nil, fmt.Errorf("discover saved settlement findings: %w", err)
	}
	results := make([]ReconcileFindingMaintenance, 0)
	for _, state := range recorded {
		if len(state.ReconcileFindings) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result, maintained := r.maintainRunFindings(ctx, state)
		if maintained {
			results = append(results, result)
		}
	}
	return results, nil
}

func (r Reconciler) maintainRunFindings(ctx context.Context, recorded runstate.State) (ReconcileFindingMaintenance, bool) {
	result := ReconcileFindingMaintenance{RunID: recorded.RunID, WorkItemID: recorded.WorkItemID, WorkItemTitle: recorded.WorkItemTitle, Cleared: make([]runstate.ReconcileStep, 0)}
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	if errors.Is(err, runstate.ErrRunHeld) {
		return result, false
	}
	if err != nil {
		result.FindingProblem = fmt.Sprintf("adopt run %s to maintain settlement findings: %v", recorded.RunID, err)
		return result, true
	}
	defer lease.Release()
	result.WorkItemID = state.WorkItemID
	result.WorkItemTitle = state.WorkItemTitle
	before := slices.Clone(state.ReconcileFindings)
	result.Finding, result.FindingProblem = r.clearReconcileFindings(ctx, &state, func(f runstate.ReconcileFinding) bool {
		return f.Resolved || reconcileFindingResolved(state, f.Step)
	})
	for _, finding := range before {
		if !hasReconcileFinding(state, finding.Step) {
			result.Cleared = append(result.Cleared, finding.Step)
		}
	}
	if result.FindingProblem != "" {
		return result, true
	}
	maintained := len(result.Cleared) > 0
	for index, finding := range state.ReconcileFindings {
		if finding.Pending {
			maintained = true
			result.Finding, result.FindingProblem = r.deliverReconcileFinding(ctx, &state, index)
			if result.FindingProblem != "" {
				break
			}
		}
	}
	return result, maintained
}

// Durable completion also covers records written before the resolution marker
// existed, and a resolution-marker save refused after the operation succeeded.
// Absence from a sweep's candidate list alone is never proof of completion.
func reconcileFindingResolved(state runstate.State, step runstate.ReconcileStep) bool {
	published := state.PullRequest
	switch step {
	case runstate.ReconcileRun:
		return state.Status.Terminal() && !state.Outstanding() && state.CleanupFailure == "" && state.PublishFailure == "" && (state.Phase == runstate.PhaseComplete || state.SettledQuietSince != nil)
	case runstate.ReconcileRefresh:
		return published != nil && (published.Merged || published.Superseded != "" || published.HandedBack != nil || strings.EqualFold(strings.TrimSpace(published.State), "CLOSED"))
	case runstate.ReconcilePublication:
		return published != nil && published.Merged && !state.Outstanding() && state.PublishFailure == ""
	case runstate.ReconcileRecovery:
		return published != nil && state.PublishFailure == "" && (published.Merged || published.MergeQueued)
	case runstate.ReconcileSuperseded:
		return published != nil && published.Superseded != ""
	case runstate.ReconcileWorktree:
		return state.WorktreeRemoved && (state.PreservedWorkRef == "" || state.PreservedWorkNotedAt != nil)
	case runstate.ReconcileEscalation:
		return state.EscalationEnded != nil
	case runstate.ReconcileRedTarget:
		return published != nil && published.TargetRed != nil && (published.Merged || published.MergeQueued || published.HandedBack != nil)
	default:
		return false
	}
}

func hasReconcileFinding(state runstate.State, step runstate.ReconcileStep) bool {
	return slices.ContainsFunc(state.ReconcileFindings, func(f runstate.ReconcileFinding) bool { return f.Step == step })
}

func nonEmptyProblems(problems ...string) []string {
	return slices.DeleteFunc(problems, func(problem string) bool { return problem == "" })
}
