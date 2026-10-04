package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// EscalationSettlement is what the reconcile sweep did about one escalation to
// the operator that has stopped being one: the item it told, and what ended it.
type EscalationSettlement struct {
	Finding        *readmodel.Attention `json:"finding,omitempty"`
	FindingProblem string               `json:"finding_problem,omitempty"`
	RunID          string               `json:"run_id"`
	WorkItemID     string               `json:"work_item_id"`
	Why            string               `json:"why"`
	// Failure is what stopped the item being told or the run's record saying
	// so; the next sweep tries again.
	Failure string `json:"failure,omitempty"`
}

// EndEscalations tells each work item whose escalation to the operator has
// ended what ended it, once, and records the ending on the escalated run.
//
// An escalation is a finding on the operator's line while it is the decision
// standing on the item's latest stopped run, and the read model ends it the
// moment the item is parked, retired, or closed, or the run's branch and
// worktree are both gone. That is what every surface reading the model sees.
// This is the other half: the item is where somebody reads what happened to
// it, and an escalation that simply stops being named says nothing there about
// why — on 2026-09-28 the Lead Product Manager parked yoyodyne-ifd.78 and the
// development manager's escalation of run-95b34031 went on naming the operator
// for two days with nothing on the item to say it had been settled. The run's
// record carries the ending too, so the item is told once, and so the channel,
// which asks neither the tracker nor the repository, reads it as over.
//
// Which escalations have ended is the read model's derivation, asked with the
// tracker's own answer about each escalated item and the repository's about
// each run's change; nothing here decides it a second way. A tracker read that
// fails leaves that item's escalation standing for the next sweep rather than
// ending it on a guess.
func (r Reconciler) EndEscalations(ctx context.Context) ([]EscalationSettlement, error) {
	if r.Docket == nil || r.Docket.Decisions == nil || r.Tracker == nil {
		return nil, nil
	}
	recorded, err := r.Store.Recorded()
	if err != nil {
		return nil, fmt.Errorf("read the recorded runs to end the escalations nothing waits on: %w", err)
	}
	itemProblems := make(map[string]string)
	items := func(workItemID string) readmodel.EscalatedItem {
		showCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		item, err := r.Tracker.Show(showCtx, workItemID)
		if err != nil {
			itemProblems[workItemID] = fmt.Sprintf("read %s to learn whether its escalation has ended: %v", workItemID, err)
			return readmodel.EscalatedItem{Admitted: true}
		}
		return readmodel.EscalatedItem{
			Admitted: strings.TrimSpace(item.Status) != "closed",
			Parked:   item.Parking.Reason(),
		}
	}
	look := readmodel.Looking(ctx, r.Docket.Remains, func() time.Time { return r.clock().Now() })
	actions, ended, problem := readmodel.Escalations(recorded, r.Docket.Decisions, items, look)
	var settled []EscalationSettlement
	for _, action := range actions {
		if failure := itemProblems[action.WorkItemID]; failure != "" {
			settled = append(settled, EscalationSettlement{RunID: action.RunID, WorkItemID: action.WorkItemID, Failure: failure})
		}
	}
	for _, ending := range ended {
		if ending.Recorded {
			continue
		}
		if settlement, acted := r.endEscalation(ctx, ending); acted {
			settled = append(settled, settlement)
		}
	}
	for index := range settled {
		result := &settled[index]
		result.Finding, result.FindingProblem = r.recordReconcileFinding(ctx, result.RunID, runstate.ReconcileEscalation, result.Failure)
	}
	if problem != "" {
		return settled, errors.New(problem)
	}
	return settled, ctx.Err()
}

// endEscalation tells one item its escalation has ended and records that on
// the run, under the run's lease. A run a live process holds is that process's,
// and is left for the next sweep.
func (r Reconciler) endEscalation(ctx context.Context, ending readmodel.EndedEscalation) (EscalationSettlement, bool) {
	settlement := EscalationSettlement{RunID: ending.RunID, WorkItemID: ending.WorkItemID, Why: runstate.BoundEscalationEnding(ending.Why)}
	state, lease, err := r.Store.AdoptRun(ctx, ending.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		return EscalationSettlement{}, false
	case err != nil:
		settlement.Failure = fmt.Sprintf("take the record of run %s to end its escalation: %v", ending.RunID, err)
		return settlement, true
	}
	defer lease.Release()
	// Re-read under the lease: another sweep may have told the item already.
	if state.EscalationEnded != nil {
		return EscalationSettlement{}, false
	}
	now := r.clock().Now()
	recordCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := r.Tracker.RecordOutcome(recordCtx, ending.WorkItemID, renderEscalationEnded(state.RunID, settlement.Why)); err != nil {
		settlement.Failure = fmt.Sprintf("the escalation of run %s to the operator has ended and %s could not be told so: %v", state.RunID, ending.WorkItemID, err)
		return settlement, true
	}
	state.EscalationEnded = &runstate.EscalationEnding{At: now, Why: settlement.Why}
	state.UpdatedAt = now
	if err := r.Store.Save(state); err != nil {
		settlement.Failure = fmt.Sprintf("%s was told its escalation ended, and run %s's record could not say so, so the next sweep will tell it again: %v", ending.WorkItemID, state.RunID, err)
	}
	return settlement, true
}

// renderEscalationEnded is what the item is told: which escalation ended, what
// ended it, and that nothing of it waits on the operator any more.
func renderEscalationEnded(runID, why string) string {
	return strings.Join([]string{
		fmt.Sprintf("The development manager's escalation of run %s to the operator has ended: %s.", runID, strings.TrimSuffix(why, ".")),
		"Nothing about it waits on the operator any more, so `yoyo status`, the dashboard, and the channel no longer name it as needing the operator's hand. The escalation itself stays recorded on the triage record as the decision that was made.",
	}, "\n")
}
