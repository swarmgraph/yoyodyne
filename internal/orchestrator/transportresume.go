package orchestrator

// The harness resuming, by itself, an approved change the tracker, the forge or
// the network stopped by not answering, or that the merge queue let go for
// something that says nothing about the change (runstate.CauseMergeQueueReleased,
// resumesFromKeptCommit in mergequeuerecovery.go). The rest of this comment is
// about the first; the second is resumed through the same path, and its
// resumed promotion admits the kept commit to the queue again.
//
// The operating rules say a dropped connection is waited out and that only a
// person or a bound on the work ends a run. An approved change whose promotion
// stopped because one of those did not answer is no verdict and no decision:
// the reviewer approved it, and what it needs is the same promotion once the
// service answers. So the carry-out of recorded decisions offers it as the
// harness's own task (DecisionResumeIntegration) at every pull, as it offers a
// first stall or a check stage the bound stopped, and fires it through the one
// integration resumer `yoyo triage resume` uses, which asks the remote and the
// tracker before it writes anything (IntegrationResumer.causeCleared and its
// read of the item). A try that finds them still not answering writes what it
// waits on and when it tried onto the stop (runstate.IntegrationStop.Waiting),
// which the run record, `yoyo status`, the docket and the factory health check
// read; a try that meets anything else stops the harness trying and leaves the
// stop to the development manager as every other integration stop is
// (NoteHarnessResumeRefused). A decision she records about the stop after it
// was recorded is hers to have carried out instead.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// DecisionResumeIntegration is the task a carry-out fires to resume an approved
// change a service stopped by not answering, or the merge queue let go. Like DecisionContinueStall it is
// not a word from the development manager's vocabulary and is never written
// onto an item's triage record.
const DecisionResumeIntegration = "resume-integration"

// CarryOutResumes resumes the approved changes the harness resumes by itself.
// It is satisfied by HarnessResumedIntegrations.
type CarryOutResumes interface {
	Due(runID string) (bool, error)
	Resume(ctx context.Context, runID string) (IntegrationResumeResult, error)
	Wait(ctx context.Context, runID, why string) error
	Refuse(ctx context.Context, runID, why string) error
}

// HarnessResumeRuns is the run store those tries read and write through. Each
// write is one of the store's narrow updates, so a try never overwrites what
// another writer changed on the run. It is satisfied by *runstate.Store.
type HarnessResumeRuns interface {
	Read(runID string) (runstate.State, error)
	Update(ctx context.Context, runID string, update runstate.RunUpdate) (runstate.State, error)
}

// HarnessResumedIntegrations resumes them through the integration resumer, and
// writes what each try found onto the run's record of its stop.
//
// Items is where a try that stops the harness trying is noted, so the item says
// why the stop is the development manager's now. Optional. A wait is not noted
// there, because the tracker is often what is not answering, and the run's own
// record is where every surface reads it.
type HarnessResumedIntegrations struct {
	Runs    HarnessResumeRuns
	Resumer IntegrationResumer
	Items   interface {
		RecordOutcome(context.Context, string, string) (beads.WorkItem, error)
	}
	Clock execution.Clock
}

func (h HarnessResumedIntegrations) now() time.Time {
	if h.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return h.Clock.Now().UTC()
}

// Due reports a run the harness resumes by itself now, from its record.
func (h HarnessResumedIntegrations) Due(runID string) (bool, error) {
	state, err := h.Runs.Read(runID)
	if err != nil {
		return false, err
	}
	return state.HarnessResumesIntegration(), nil
}

// Resume makes one try. The resumer asks everything before it writes anything,
// so a try that finds a service still not answering leaves the run exactly as
// it stopped.
func (h HarnessResumedIntegrations) Resume(ctx context.Context, runID string) (IntegrationResumeResult, error) {
	return h.Resumer.Resume(ctx, IntegrationResumeRequest{Run: runID, Harness: true})
}

// Wait writes onto the stop what the try found it waiting on and when.
func (h HarnessResumedIntegrations) Wait(ctx context.Context, runID, why string) error {
	if _, err := h.Runs.Update(ctx, runID, runstate.HarnessResumeWaited{Why: why, At: h.now()}); err != nil {
		return fmt.Errorf("record what the harness's resumption of run %s waits on: %w", runID, err)
	}
	return nil
}

// Refuse writes onto the stop why the harness stopped trying, and notes it on
// the item the first time.
func (h HarnessResumedIntegrations) Refuse(ctx context.Context, runID, why string) error {
	prior, err := h.Runs.Read(runID)
	if err != nil {
		return fmt.Errorf("record why the harness stopped resuming run %s: %w", runID, err)
	}
	if prior.IntegrationStop == nil || prior.IntegrationStop.HarnessRefused != "" {
		return nil
	}
	state, err := h.Runs.Update(ctx, runID, runstate.HarnessResumeRefused{Why: why, At: h.now()})
	if err != nil {
		return fmt.Errorf("record why the harness stopped resuming run %s: %w", runID, err)
	}
	if h.Items == nil || strings.TrimSpace(state.WorkItemID) == "" {
		return nil
	}
	note := fmt.Sprintf("The harness stopped trying to resume the approved change of run %s by itself, because its try met something it does not wait out: %s. The stop is the development manager's to decide now, and `yoyo triage resume %s` still resumes it at no cost once that is settled.",
		runID, strings.TrimSpace(why), runID)
	if _, err := h.Items.RecordOutcome(ctx, state.WorkItemID, singleLine(note, runstate.MaxBlockerBytes)); err != nil {
		return fmt.Errorf("note on %s why the harness stopped resuming run %s: %w", state.WorkItemID, runID, err)
	}
	return nil
}

// resumeTask reports a stop the harness resumes by itself now, and whether
// there is one. A decision the development manager recorded about the stoppage
// after it was recorded is hers to have carried out instead.
func (c CarryOut) resumeTask(entry triage.Entry, item outstandingItem) (CarryOutTask, bool, error) {
	if c.Resumes == nil || entry.IntegrationStop == nil {
		return CarryOutTask{}, false, nil
	}
	if decision, decided := item.counters.DecisionOf(entry.RunID); decided && !decision.InFlight() && !decision.DecidedAt.Before(entry.RecordedAt) {
		return CarryOutTask{}, false, nil
	}
	due, err := c.Resumes.Due(entry.RunID)
	if err != nil || !due {
		return CarryOutTask{}, false, err
	}
	reason := "the tracker, the forge, or the network did not answer while this approved change was being promoted, and the harness resumes it by itself once they answer"
	if entry.IntegrationStop.Cause == triage.CauseMergeQueueReleased {
		reason = "the merge queue let this approved change go for something that says nothing about the change, and " + triage.HarnessResumesWhen(entry.IntegrationStop.Cause)
	}
	return CarryOutTask{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
		Decision:   DecisionResumeIntegration,
		Reason:     reason,
		Cause:      entry.IntegrationStop.Cause,
	}, true, nil
}

// resumeIntegration makes one try. Nothing it meets is written onto the item's
// triage record, because nobody's decision is waiting on it: a wait is written
// onto the stop and asked again at the next pull, and anything else stops the
// harness trying and is written onto the stop and the item.
func (c CarryOut) resumeIntegration(ctx context.Context, task CarryOutTask, carried CarriedOut) (CarriedOut, Outcome, error) {
	waiting := func(gate, what string) (CarriedOut, Outcome, error) {
		carried.Gate = gate
		carried.Waiting = true
		carried.Problem = fmt.Sprintf("the harness's resumption of run %s's approved change is waiting on %s: %s; nothing was spent, and the next pull tries again", task.RunID, gate, what)
		if err := c.Resumes.Wait(ctx, task.RunID, what); err != nil {
			carried.RecordProblem = strings.TrimSpace(carried.RecordProblem + " " + err.Error())
		}
		return carried, Outcome{}, nil
	}
	if c.Resumes == nil {
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("nothing is wired to this harness to resume run %s's approved change", task.RunID)
		return carried, Outcome{}, nil
	}
	hold, held, err := c.paused()
	if err != nil {
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("the harness's resumption of run %s was not attempted: %v", task.RunID, err)
		return carried, Outcome{}, nil
	}
	if held {
		return waiting(runstate.TriageGateSpendingPause, fmt.Sprintf("the operator's pause on everything the harness spends, in place since %s", hold.HeldAt.Local().Format(time.RFC1123)))
	}
	result, runErr := c.Resumes.Resume(ctx, task.RunID)
	carried.RecordProblem = result.RecordProblem
	switch {
	case result.Resumed:
		carried.Carried = true
		carried.Reason = result.Reason
		return carried, result.Outcome, runErr
	case result.IntakeHeld != nil:
		return waiting(runstate.TriageGateIntakeHold, fmt.Sprintf("`yoyo release` to lift the hold on what the harness chooses, in place since %s", result.IntakeHeld.HeldAt.Local().Format(time.RFC1123)))
	case result.OutsideNamedEpics != nil:
		return waiting(runstate.TriageGateNamedEpics, result.OutsideNamedEpics.Error())
	case result.CapacityFull != nil:
		carried.WaitsForSlot = result.CapacityFull.Slot
		return waiting(runstate.TriageGateCapacity, capacityWaitSays(*result.CapacityFull))
	case serviceUnanswered(runErr):
		return waiting(runstate.TriageGateHarness, "the tracker, the forge, or the network to answer ("+singleLine(runErr.Error(), 600)+")")
	case errors.Is(runErr, ErrRunInFlight):
		return waiting(runstate.TriageGateWorkItem, "another run of the same item to finish ("+singleLine(runErr.Error(), 600)+")")
	}
	why := strings.TrimSpace(refusalText(runErr))
	if err := c.Resumes.Refuse(ctx, task.RunID, why); err != nil {
		carried.RecordProblem = strings.TrimSpace(carried.RecordProblem + " " + err.Error())
	}
	carried.Gate = runstate.TriageGateHarness
	carried.Problem = fmt.Sprintf("the harness stopped trying to resume run %s's approved change by itself: %s; the stop is the development manager's now", task.RunID, why)
	return carried, Outcome{}, nil
}

// serviceUnanswered reports a try refused only because the remote or the
// tracker still did not answer, which is the wait this resumption exists to
// sit out rather than a reason to stop trying.
func serviceUnanswered(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrCauseStands) || beads.Unanswered(err) || recovery.Recoverable(err)
}
