package orchestrator

// What the pipeline and the work session do with the development manager's
// run.recover requests (internal/runrecover).
//
// The tool runs in her conversation, and two of its actions are runs of the
// pipeline, which a conversation cannot host: continuing the same run, and
// finishing the steps after an approved change's stopped integration. So the
// tool writes the request on the run (runstate.RecoveryContinuation) and the
// work session carries it out through the path that would have taken the run up
// anyway. A continuation is taken up by the scheduler's re-adoption of runs in
// flight with nobody behind them, beside the runs a redeploy put down
// (schedule.go), and accepted by Pipeline.Run on the terms below; a finished
// merge is fired by the carry-out of recorded decisions through the integration
// resumer, exactly as `yoyo triage resume` fires it. Either is marked taken up by
// the step that takes it up (resumeRun, IntegrationResumer.supersedeOnRun), and
// a refusal the work session will not retry by itself is written onto the
// request, where run.inspect, run.verify and the next health observation read it.
// A finished merge's waits are written there too, and both are noted on the item.
//
// The rules a request is accepted on are here rather than in the tool, because
// they are the rules the pipeline applies when it takes the run up: a request
// the tool accepted is one the pipeline can serve.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// BlockingDependencies is the unfinished work an item waits on, by the rule the
// pipeline pauses a run by.
func BlockingDependencies(item beads.WorkItem) []string {
	return blockingDependencies(item)
}

// RecoveryContinuable says whether the pipeline can take this run up from its
// record when it is continued through run.recover, and why not where it cannot.
// It accepts every shape Pipeline.Run already resumes, and one more: a run in
// flight at its developer attempt, its checks or its review with no stop
// recorded at all, which is what a process that died outright leaves. That is
// the shape a redeploy stop is resumed from (stoppedForRedeployIsResumable), and
// resumeRun picks the step to resume at from the record whichever it is.
func RecoveryContinuable(state runstate.State) error {
	if !state.Status.InFlight() {
		return fmt.Errorf("it has ended %s", state.Status)
	}
	if state.Document != nil {
		return errors.New("it lands a document, which is continued by its own publication rather than by the pipeline")
	}
	if state.OutsideFile != nil {
		return errors.New("it brings an outside file in, which the scheduler continues at its next look at the record")
	}
	if pausedForUsageLimit(state) || pausedForDirective(state) || pausedForDependency(state) || pausedForTracker(state) ||
		pausedForOperatorHold(state) || stoppedProviderIsResumable(state) || stoppedForRedeployIsResumable(state) ||
		continuedAtCheckStage(state) || resumableRepair(state) {
		return nil
	}
	switch state.Phase {
	case runstate.PhaseDeveloping, runstate.PhaseChecking, runstate.PhaseReviewing:
	case runstate.PhaseIntegrating:
		return errors.New("it was interrupted inside its integration, which only the repository can say landed or not; `yoyo reconcile` settles it against the repository")
	default:
		return fmt.Errorf("it is at its %q phase, which nothing continues from a record", state.Phase)
	}
	var missing []string
	if state.ProviderSessionID == "" {
		missing = append(missing, "a developer session")
	}
	if state.WorktreePath == "" || state.Branch == "" || state.BaseCommit == "" {
		missing = append(missing, "a preserved branch and worktree")
	}
	if len(missing) > 0 {
		return fmt.Errorf("its record names no %s to continue in", strings.Join(missing, " and no "))
	}
	return nil
}

// integrationOwed reports a run in flight whose integration is begun or done:
// one interrupted inside it, or one whose promotion is on its record. Neither
// is put on a wait, by a dispatch or by run.recover; reconciliation settles
// the step against the repository, which is the one thing that can say whether
// the promotion landed.
func integrationOwed(state runstate.State) bool {
	return state.Status.InFlight() && (state.Phase == runstate.PhaseIntegrating || state.Integration != nil)
}

// RecoveryWaitable says whether a dependency wait may be recorded on this run
// through run.recover, and why not where it may not.
func RecoveryWaitable(state runstate.State) error {
	if integrationOwed(state) {
		return errors.New("its integration is begun or done, and what it is owed is the settlement of that step against the repository, which `yoyo reconcile` makes and a watching session's reconciliation pass makes at every pull; a wait would hide it from that settlement")
	}
	return nil
}

// recoveryContinuationDue reports a run whose record asks to be continued and
// that the pipeline can take up.
func recoveryContinuationDue(state runstate.State) bool {
	return state.RecoveryContinuationOutstanding(runstate.RecoverContinue) && RecoveryContinuable(state) == nil
}

// RecoveryFinishable says whether the steps after this run's integration can be
// finished from its record, and why not where they cannot. It is the record
// half of what the integration resumer asks (resumableStop); the docket entry,
// the branch, the checkout and the item are asked again when the work session
// carries the request out, and refuse it there.
func RecoveryFinishable(state runstate.State) error {
	if !state.Status.Terminal() {
		if state.Phase == runstate.PhaseIntegrating {
			return errors.New("it is still in flight inside its integration, which `yoyo reconcile` settles against the repository")
		}
		return fmt.Errorf("it is %s at %q, and only an approved change whose integration stopped has its merge finished", state.Status, state.Phase)
	}
	switch {
	case state.Integration != nil:
		return errors.New("its change is promoted already, and what is outstanding is its publication, which `yoyo reconcile` settles")
	case state.ReplayConflict != nil:
		return fmt.Errorf("%s", state.ReplayConflict.Says(state.RunID))
	case state.IntegrationStop == nil || !state.ApprovedAwaitingIntegration():
		return errors.New("it is not an approved change the environment stopped short of its promotion")
	case state.WorktreePath == "" || state.Branch == "" || state.BaseCommit == "" || state.TargetBranch == "":
		return errors.New("it recorded no preserved branch and worktree")
	case state.ProviderSessionID == "":
		return errors.New("it recorded no developer session, so its approval cannot be shown to come from a second invocation")
	case state.ResumptionsLeft() == 0:
		return fmt.Errorf("its integration has been resumed %d times, which is the bound on one run", len(state.IntegrationResumptions))
	}
	return nil
}

// DecisionFinishMerge is the task a carry-out fires for a run.recover request to
// finish a merge. It is the development manager's decision, recorded on the run
// rather than on the item's triage record, so it is never written there.
const DecisionFinishMerge = "finish-merge"

// CarryOutIntegrations finishes the merges the development manager asked for
// through run.recover. It is satisfied by RecoveredIntegrations.
//
// Asked is the request the run's record carries, where one is outstanding.
// Wait writes what an attempt found it waiting on onto that request, so the run
// says what it waits on whichever session reads it next.
type CarryOutIntegrations interface {
	Asked(runID string) (runstate.RecoveryContinuation, bool, error)
	Finish(ctx context.Context, runID string) (IntegrationResumeResult, error)
	Wait(ctx context.Context, runID, why string) error
}

// RecoveryRuns is the run store the work session reads and settles requests
// through. *runstate.Store is one.
type RecoveryRuns interface {
	Read(runID string) (runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
}

// RecoveredIntegrations carries out run.recover's finish-the-merge requests
// through the integration resumer.
//
// Items is the run's work item, where a refusal and each new wait are noted as
// well as on the request, so the item says what became of the request without
// anybody opening the run. Optional: wired without it, the run alone says so.
type RecoveredIntegrations struct {
	Runs    RecoveryRuns
	Resumer IntegrationResumer
	Items   interface {
		RecordOutcome(context.Context, string, string) (beads.WorkItem, error)
	}
	Clock execution.Clock
}

func (r RecoveredIntegrations) now() execution.Clock {
	if r.Clock == nil {
		return execution.RealClock{}
	}
	return r.Clock
}

// Asked reports the request to finish its merge that a run's record carries,
// where one is outstanding.
func (r RecoveredIntegrations) Asked(runID string) (runstate.RecoveryContinuation, bool, error) {
	state, err := r.Runs.Read(runID)
	if err != nil {
		return runstate.RecoveryContinuation{}, false, err
	}
	if !state.RecoveryContinuationOutstanding(runstate.RecoverFinishMerge) {
		return runstate.RecoveryContinuation{}, false, nil
	}
	return *state.RecoveryContinuation, true, nil
}

// Wait writes onto the request what the attempt found it waiting on, and notes
// it on the item the first time it is found waiting on that. A wait already
// written in the same words is left alone, so a pull every minute through a
// long hold writes once.
func (r RecoveredIntegrations) Wait(ctx context.Context, runID, why string) error {
	state, lease, err := r.Runs.AdoptRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("record what finishing run %s's merge waits on: %w", runID, err)
	}
	defer lease.Release()
	if !state.WaitRecoveryContinuation(runstate.RecoverFinishMerge, why, r.now().Now()) {
		return nil
	}
	if err := r.Runs.Save(state); err != nil {
		return fmt.Errorf("record what finishing run %s's merge waits on: %w", runID, err)
	}
	return r.note(ctx, state.WorkItemID, fmt.Sprintf("Finishing the merge of run %s, which the development manager asked for through run.recover (request %s), is waiting: %s. Nothing is refused; the next pull that can take it tries again.",
		runID, state.RecoveryContinuation.RequestID, strings.TrimSpace(why)))
}

func (r RecoveredIntegrations) note(ctx context.Context, workItemID, note string) error {
	if r.Items == nil || strings.TrimSpace(workItemID) == "" {
		return nil
	}
	if _, err := r.Items.RecordOutcome(ctx, workItemID, singleLine(note, runstate.MaxBlockerBytes)); err != nil {
		return fmt.Errorf("note on %s what became of the request to finish its merge: %w", workItemID, err)
	}
	return nil
}

// Finish resumes the run's integration on the request its record carries. A
// refusal the resumer gives for the run itself is written onto the request, so
// the work session does not ask again each pull and the development manager
// reads why; a wait for the intake hold or a free slot is not, because the next
// pull asks again.
func (r RecoveredIntegrations) Finish(ctx context.Context, runID string) (IntegrationResumeResult, error) {
	state, err := r.Runs.Read(runID)
	if err != nil {
		return IntegrationResumeResult{RunID: runID}, err
	}
	asked := state.RecoveryContinuation
	if !asked.Outstanding() || asked.Action != runstate.RecoverFinishMerge {
		return IntegrationResumeResult{RunID: runID}, fmt.Errorf("run %s carries no request to finish its merge", runID)
	}
	reason := fmt.Sprintf("the development manager asked through run.recover (request %s) for the integration of run %s to be finished: %s", asked.RequestID, runID, asked.Reason)
	result, err := r.Resumer.Resume(ctx, IntegrationResumeRequest{Run: runID, Reason: reason, Asked: true})
	if result.Resumed || result.IntakeHeld != nil || result.CapacityFull != nil || err == nil {
		return result, err
	}
	if recordErr := r.refuse(ctx, runID, asked.RequestID, refusalText(err)); recordErr != nil {
		result.RecordProblem = strings.TrimSpace(result.RecordProblem + " " + recordErr.Error())
	}
	return result, err
}

func (r RecoveredIntegrations) refuse(ctx context.Context, runID, requestID, why string) error {
	state, lease, err := r.Runs.AdoptRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("record why run %s's merge was not finished: %w", runID, err)
	}
	defer lease.Release()
	if state.RecoveryContinuation == nil || state.RecoveryContinuation.RequestID != requestID || !state.RecoveryContinuation.Outstanding() {
		return nil
	}
	refused := *state.RecoveryContinuation
	at := r.now().Now().UTC()
	refused.Refused, refused.RefusedAt = strings.TrimSpace(why), &at
	if refused.Refused == "" {
		refused.Refused = "the integration resumer refused it and said nothing more"
	}
	refused.Waiting, refused.WaitingSince = "", nil
	state.RecoveryContinuation = &refused
	if err := r.Runs.Save(state); err != nil {
		return err
	}
	return r.note(ctx, state.WorkItemID, fmt.Sprintf("The harness did not finish the merge of run %s that the development manager asked for through run.recover (request %s), and will not try again unless she asks again: %s",
		runID, requestID, refused.Refused))
}

// finishMergeTask reports a run.recover request to finish this entry's run's
// merge, and whether there is one.
//
// The request and the item's triage record are both the development manager's,
// so the later of the two is what she decided. A decision about the run recorded
// after the request is carried out instead, except a wait: a wait does nothing,
// and the request it was recorded beside is what she is waiting on. A decision
// recorded before the request is one the request has decided past, whatever the
// stoppage's docket entry says.
func (c CarryOut) finishMergeTask(entry triage.Entry, item outstandingItem) (CarryOutTask, bool, error) {
	if c.Integrations == nil {
		return CarryOutTask{}, false, nil
	}
	asked, due, err := c.Integrations.Asked(entry.RunID)
	if err != nil || !due {
		return CarryOutTask{}, false, err
	}
	if decision, decided := item.counters.DecisionOf(entry.RunID); decided && supersedesFinishMerge(decision, asked) {
		return CarryOutTask{}, false, nil
	}
	return CarryOutTask{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
		Decision:   DecisionFinishMerge,
		Reason:     "the development manager asked through run.recover for this approved change's integration to be finished",
	}, true, nil
}

// supersedesFinishMerge reports a triage decision about a run that replaces the
// request to finish its merge: one about the stopped run, other than a wait,
// recorded at or after the request.
func supersedesFinishMerge(decision runstate.TriageDecision, asked runstate.RecoveryContinuation) bool {
	return !decision.InFlight() && decision.Decision != runstate.TriageDecisionWait && !decision.DecidedAt.Before(asked.RequestedAt)
}

// finishMerge carries one such request out. Like the harness's own
// continuations, nothing it meets is written onto the item's triage record,
// because the request is on the run rather than there: a wait is written onto
// the request and asked again at the next pull, and a refusal is written onto
// the request by RecoveredIntegrations, each noted on the item as well.
func (c CarryOut) finishMerge(ctx context.Context, task CarryOutTask, carried CarriedOut) (CarriedOut, Outcome, error) {
	waiting := func(gate, what string) (CarriedOut, Outcome, error) {
		carried.Gate = gate
		carried.Waiting = true
		carried.Problem = fmt.Sprintf("finishing run %s's merge, as the development manager asked, is waiting on %s: %s; the next pull asks again", task.RunID, gate, what)
		if err := c.Integrations.Wait(ctx, task.RunID, what); err != nil {
			carried.RecordProblem = strings.TrimSpace(carried.RecordProblem + " " + err.Error())
		}
		return carried, Outcome{}, nil
	}
	hold, held, err := c.paused()
	if err != nil {
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("finishing run %s's merge was not attempted: %v", task.RunID, err)
		return carried, Outcome{}, nil
	}
	if held {
		return waiting(runstate.TriageGateSpendingPause, fmt.Sprintf("the operator has paused everything the harness spends, since %s", hold.HeldAt.UTC().Format(time.RFC3339)))
	}
	result, runErr := c.Integrations.Finish(ctx, task.RunID)
	carried.RecordProblem = result.RecordProblem
	switch {
	case result.IntakeHeld != nil:
		return waiting(runstate.TriageGateIntakeHold, fmt.Sprintf("the operator has held what the harness chooses, since %s", result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339)))
	case result.CapacityFull != nil:
		return waiting(runstate.TriageGateCapacity, fmt.Sprintf("every developer slot is occupied: %d active, limit %d", result.CapacityFull.Active, result.CapacityFull.Limit))
	case !result.Resumed:
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("the harness did not finish run %s's merge: %s; the refusal is recorded on the request", task.RunID, strings.TrimSpace(refusalText(runErr)))
		return carried, Outcome{}, nil
	}
	carried.Carried = true
	carried.Reason = result.Reason
	return carried, result.Outcome, runErr
}
