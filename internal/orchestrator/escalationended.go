package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
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
		showCtx, cancel := trackerCallContext(ctx)
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
	// Re-read under the lease: another sweep may have told the item already. An
	// ending recorded before the escalation was decided was an earlier one's.
	if state.EscalationEnded != nil && !state.EscalationEnded.At.Before(ending.DecidedAt) {
		return EscalationSettlement{}, false
	}
	now := r.clock().Now()
	if ending.ReturnedTo != "" {
		if err := r.returnEscalation(state.RunID, settlement.Why, now); err != nil {
			settlement.Failure = err.Error()
			return settlement, true
		}
	}
	recordCtx, cancel := trackerCallContext(ctx)
	defer cancel()
	if _, err := r.Tracker.RecordOutcome(recordCtx, ending.WorkItemID, renderEscalationEnded(state.RunID, settlement.Why, ending.ReturnedTo)); err != nil {
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

// returnEscalation puts a stoppage whose escalation went back to the
// development manager on her docket again: each of the run's entries the
// escalation settled is answered by a closure that holds nothing, so the next
// build lists it as undecided. An entry already put back, or settled since by
// anything but the escalation, is left as it is, which is what makes a second
// sweep after a failed note harmless.
func (r Reconciler) returnEscalation(runID, why string, now time.Time) error {
	if r.Docket.Docket == nil {
		return fmt.Errorf("the escalation of run %s goes back to the development manager and no docket was wired to put it back on, so the next sweep will try again", runID)
	}
	entries, err := r.Docket.Docket.List()
	if err != nil {
		return fmt.Errorf("read the docket to put run %s back in front of the development manager: %w", runID, err)
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.RunID) != runID || entry.Closed == nil ||
			strings.TrimSpace(entry.Closed.Decision) != runstate.TriageDecisionEscalate || !entry.Closed.Holds(now) {
			continue
		}
		// Strictly after the escalation, so the docket reads this as the closure
		// standing over the entry rather than one made at the same moment.
		returnedAt := now
		if !returnedAt.After(entry.Closed.ClosedAt) {
			returnedAt = entry.Closed.ClosedAt.Add(time.Second)
		}
		if _, err := r.Docket.Docket.Close(triage.Closure{
			SchemaVersion: triage.ClosureSchemaVersion,
			Key:           entry.Key,
			ProductID:     entry.ProductID,
			RunID:         entry.RunID,
			WorkItemID:    entry.WorkItemID,
			Decision:      triage.EscalationReturnedDecision,
			Reason:        singleLine(fmt.Sprintf("%s escalated this to the operator at %s: %s; it goes back to her because %s", strings.TrimSpace(entry.Closed.DecidedBy), entry.Closed.ClosedAt.UTC().Format(time.RFC3339), strings.TrimSpace(entry.Closed.Reason), why), triage.MaxMessageBytes),
			DecidedBy:     "the harness, finding the escalation gave the operator no reason from his closed list",
			ClosedAt:      returnedAt,
		}); err != nil {
			return fmt.Errorf("put the %s entry of run %s back in front of the development manager: %w", entry.Class, runID, err)
		}
	}
	return nil
}

// renderEscalationEnded is what the item is told: which escalation ended, what
// ended it, that nothing of it waits on the operator any more, and, where it
// went back to a role, that the role decides it now.
func renderEscalationEnded(runID, why string, returnedTo readmodel.Mover) string {
	lines := []string{
		fmt.Sprintf("The development manager's escalation of run %s to the operator has ended: %s.", runID, strings.TrimSuffix(why, ".")),
		"Nothing about it waits on the operator any more, so `yoyo status`, the dashboard, and the channel no longer name it as needing the operator's hand. The escalation itself stays recorded on the triage record as the decision that was made.",
	}
	if returnedTo != "" {
		lines = append(lines, fmt.Sprintf("It is %s decision now: the stopped run is back on the %s's triage docket as one nobody has decided about, and she may decide it again — a repair, a re-run, a wait, an escalation to the Lead Product Manager or the architect, or one to the operator for a reason on his closed list.",
			returnedTo.Possessive(), domain.AgentRole(returnedTo).Title()))
	}
	return strings.Join(lines, "\n")
}
