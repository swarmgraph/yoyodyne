package orchestrator

// Continuing a run at its checks after execution.check_stage_timeout stopped
// the stage.
//
// A stage the bound stopped judged nothing. No check failed and nothing was
// handed back to the developer; the change is on its branch exactly as the
// developer attempt left it, and what stopped the stage was the machine — the
// race suites of three runs beside each other, most often. Every verb that could
// pick such a run up spent something for it: a repair was refused for want of a
// failure to hand back, a resumption covers only approved changes, and a re-run
// started the item over from the target branch, redoing the development and
// spending the item's re-run budget, while the finished change sat on its
// branch. On 2026-09-26 that was the largest avoidable spend on the line.
//
// So the harness continues such a run itself, the way a stall at the checks is
// continued (yoyodyne-ifd.428.16) but without anybody deciding it: at its checks,
// on the same branch and in the same worktree, with no developer invoked and no
// review round, repair grant, or re-run spent. The scheduling pass fires it on a
// pull where a developer slot is free, ahead of fresh work at equal or lower
// priority. Like fresh work, it is not held on machine load. It does so at most
// runstate.MaxCheckStageContinuations times
// for one run; past that the stoppage is the development manager's, as it was
// before.
//
// It is held to what a repair is held to: the worktree has to be as the harness
// left it and still hold the change, because that change is what the checks
// judge. A missing checkout can be restored from the verified recorded branch,
// clearing verification credit before writing and preserving consumed budgets.
// A checkout that cannot be verified or restored is refused on the run and put
// back on the docket for the development manager; the harness does not ask again.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// MachineLoad reads the machine's one-minute load average and the number of
// cores it is read against, and whether the platform could report it. It is
// satisfied by gitworktree.MachineLoad.
type MachineLoad func() (load float64, cores int, ok bool)

// CheckStageRedocket puts a stoppage the harness declined to continue back on
// the docket, as the development manager's to decide. It is satisfied by
// Docketer.
type CheckStageRedocket interface {
	RecordStoppedRun(state runstate.State) (bool, error)
}

// CheckStageContinuer continues one run the check stage bound stopped, at its
// checks, on the change it already has. It decides nothing about the work and
// spends nothing: the harness stopped the stage, and this is the harness
// carrying it the rest of the way.
type CheckStageContinuer struct {
	Docket ResumeDocket
	// Redocket is what puts a stoppage whose continuation was refused back in
	// front of the development manager. Optional: without it the refusal is still
	// written onto the run and the item, and the entry keeps saying the harness is
	// the one to move.
	Redocket CheckStageRedocket
	Runs     RepairRuns
	Intake   IntakeHolds
	// Items is the work item the stopped run holds. Required: a closed item, or
	// one made to wait on other work, is not one a run may be continued on.
	Items RepairItems
	// Worktrees proves the worktree is as the harness left it and still holds
	// the change. Required: what the checks judge is whatever is in it.
	Worktrees RepairWorktrees
	// Load is retained for callers that supply a machine reading. Continuing
	// checks has no load gate, just as starting fresh work has none.
	Load MachineLoad
	// Capacity is execution.max_concurrent_developers. Required: the continued
	// run holds a slot for exactly as long as any run does.
	Capacity int
	Start    RepairContinueStarter
	Clock    execution.Clock
}

// CheckStageContinueRequest names the run the docket entry is about.
type CheckStageContinueRequest struct {
	Run string
	// AheadOf is a sentence the pass that fired this adds to the reason the run
	// and the item record, where it put the continuation ahead of higher-priority
	// ready work, naming that work. Empty for every other continuation.
	AheadOf string
}

// CheckStageContinueResult is what the action did, and just as carefully what
// it did not: an intake hold, a full harness, and a refusal are different
// things for somebody to know about.
type CheckStageContinueResult struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	// Command is the check the bound stopped the stage during.
	Command          string `json:"command,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Continued        bool   `json:"continued"`
	WorktreeRestored bool   `json:"worktree_restored,omitempty"`
	// SupersededFailure is what the stopped run ended on.
	SupersededFailure string `json:"superseded_failure,omitempty"`
	// IntakeHeld and CapacityFull are waits, and the next pull asks again.
	IntakeHeld   *runstate.IntakeHold    `json:"intake_held,omitempty"`
	CapacityFull *runstate.CapacityError `json:"capacity_full,omitempty"`
	// Refused is why the harness will not continue this run, written onto it,
	// where what refused is something only a person settles.
	Refused string  `json:"refused,omitempty"`
	Outcome Outcome `json:"outcome"`
	// RecordProblem names a durable record this action could not write once it
	// had begun, reported beside the result rather than in place of it.
	RecordProblem string `json:"record_problem,omitempty"`
}

// ErrNotContinuableAtChecks is what a continuation refused for the shape of the
// stopped run unwraps to: it is not a run the stage bound stopped that the
// harness still continues.
var ErrNotContinuableAtChecks = errors.New("the stopped run is not one the harness continues at its checks")

// continuedChecksDocketDecision is the word the closure a continuation makes
// carries, so a reader of a closed entry can tell a stoppage the harness
// continued from one the development manager decided about.
const continuedChecksDocketDecision = "continued at its checks"

// Due reports a run the harness would continue at its checks now, as far as its
// record can say: it is one the stage bound stopped with continuations left.
// It writes nothing. Machine load does not withhold it from selection.
// What the moment also has to allow — a free slot, the operator's switches —
// is asked by Continue, where a refusal is reported.
func (c CheckStageContinuer) Due(runID string) (bool, error) {
	if c.Runs == nil {
		return false, errors.New("continuing a check stage requires the durable run state")
	}
	state, err := c.Runs.Load(runID)
	if err != nil {
		return false, fmt.Errorf("read run %s to see whether its check stage is to be continued: %w", runID, err)
	}
	if !state.Status.Terminal() || !state.HarnessContinuesCheckStage() {
		return false, nil
	}
	return true, nil
}

// Continue continues one run the stage bound stopped, at its checks.
//
// The order is the one the resumption of an approved change keeps, for the same
// reasons: intake and capacity are asked before restoring a missing checkout,
// the item is put back before the run is made live, and the docket entry is
// closed last.
func (c CheckStageContinuer) Continue(ctx context.Context, request CheckStageContinueRequest) (CheckStageContinueResult, error) {
	if err := c.validate(); err != nil {
		return CheckStageContinueResult{}, err
	}
	runID := strings.TrimSpace(request.Run)
	if !runstate.ValidRunID(runID) {
		return CheckStageContinueResult{}, fmt.Errorf("%q is not a run identifier; a continuation names the run the docket entry is about", request.Run)
	}
	entry, err := docketedStoppage(c.Docket, runID, "continue at its checks")
	if err != nil {
		return CheckStageContinueResult{}, err
	}
	result := CheckStageContinueResult{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
	}
	prior, lease, err := c.Runs.AdoptRun(ctx, entry.RunID)
	if err != nil {
		return result, fmt.Errorf("take the stopped run to continue its checks: %w", err)
	}
	defer lease.Release()

	if !prior.Status.Terminal() {
		return result, fmt.Errorf("%w: run %s is recorded as %s rather than ended", ErrNotContinuableAtChecks, prior.RunID, prior.Status)
	}
	if !prior.HarnessContinuesCheckStage() {
		return result, fmt.Errorf("%w: %s", ErrNotContinuableAtChecks, nonEmpty(prior.CheckStageStopSays(), fmt.Sprintf("run %s did not end at its check stage bound", prior.RunID)))
	}
	if prior.CheckStage != nil {
		result.Command = prior.CheckStage.Command
	}
	result.SupersededFailure = prior.Failure
	if err := noRunInFlight(c.Runs, entry.WorkItemID); err != nil {
		return result, err
	}
	item, err := c.Items.Show(ctx, entry.WorkItemID)
	if err != nil {
		return result, fmt.Errorf("read the work item the stoppage is about: %w", err)
	}
	if err := continuableItem(item, entry.WorkItemID); err != nil {
		return result, err
	}
	hold, held, err := c.Intake.Held()
	if err != nil {
		return result, fmt.Errorf("read whether intake is held: %w", err)
	}
	if held {
		result.IntakeHeld = &hold
		return result, nil
	}
	full, free, err := slotIsFree(c.Runs, c.Capacity)
	if err != nil {
		return result, err
	}
	if !free {
		result.CapacityFull = &full
		return result, nil
	}
	// Repository facts, rather than removal flags, decide whether the recorded
	// checkout needs restoring. Intake and capacity waits leave it untouched.
	remains, _ := c.Worktrees.(readmodel.Remains)
	found := readmodel.LookFor(ctx, remains, prior)
	recovery := checkoutRecovery{Runs: c.Runs, Worktrees: c.Worktrees, Clock: c.Clock}
	if found.Unknown || !found.BranchThere || (!found.WorktreeThere && !found.Looked()) {
		return c.refuse(ctx, result, prior, fmt.Errorf("run %s's recorded branch and checkout could not be verified: %s; available artifacts are kept", prior.RunID, found.Describe()))
	}
	if !found.WorktreeThere {
		if prior.HarnessCommit == "" || prior.PreservedWorkRef != "" {
			return c.refuse(ctx, result, prior, fmt.Errorf("run %s's missing checkout has no verified completed commit or has separately captured uncommitted work; branch-only restoration cannot recover it, and missing uncommitted work is not claimed recovered", prior.RunID))
		}
		prior, err = recovery.restoreCheckout(ctx, prior)
		if err != nil {
			return c.refuse(ctx, result, prior, err)
		}
		result.WorktreeRestored = true
	} else if prior.CheckoutRestorePending {
		prior, err = recovery.recordRestoredCheckout(ctx, prior)
		if err != nil {
			return c.refuse(ctx, result, prior, err)
		}
		result.WorktreeRestored = true
	}
	// Verify ownership and the preserved change before spending a continuation.
	// A refusal is recorded for the development manager, with artifacts kept.
	if err := c.Worktrees.VerifyOwnedHead(ctx, worktreeOf(prior)); err != nil {
		return c.refuse(ctx, result, prior, WorktreeSurgeryError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err})
	}
	if err := preservedChangeHeld(ctx, c.Worktrees, prior); err != nil {
		return c.refuse(ctx, result, prior, MissingPreservedChangeError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err})
	}

	result.Reason = checkStageContinueReason(prior)
	if result.WorktreeRestored {
		result.Reason += fmt.Sprintf("\nThe missing checkout was restored at %s from the harness's recorded commit %s, in the same run and developer session; previous check approval was cleared before restoration.", prior.WorktreePath, prior.HarnessCommit)
	}
	if strings.TrimSpace(request.AheadOf) != "" {
		result.Reason = withAheadOf(result.Reason, request.AheadOf)
	}
	if _, err := c.Items.RecordOutcome(ctx, entry.WorkItemID, result.Reason); err != nil {
		return result, fmt.Errorf("record the continuation on %s: %w", entry.WorkItemID, err)
	}
	claimed, _, err := c.Items.Claim(ctx, entry.WorkItemID)
	if err != nil {
		return result, fmt.Errorf("put %s back to work for the checks its change is owed: %w", entry.WorkItemID, err)
	}
	if err := validateClaimedItem(claimed, entry.WorkItemID); err != nil {
		return result, fmt.Errorf("validate the work item put back for its checks: %w", err)
	}
	if err := c.Runs.Save(continuedAtChecks(prior, result.Reason, c.now())); err != nil {
		return result, fmt.Errorf("record the continuation on run %s, whose item has already been put back and told why: %w", prior.RunID, err)
	}
	result.Continued = true
	if err := c.closeEntry(entry, result.Reason); err != nil {
		result.RecordProblem = fmt.Sprintf("the docket entry for this stoppage could not be closed, so it still reads as a stoppage although the run is at its checks again: %v", err)
	}
	// Given up before the run is continued, because continuing it is the
	// pipeline adopting the same run.
	lease.Release()

	outcome, runErr := c.Start(ctx, entry.WorkItemID, prior.RunID)
	result.Outcome = outcome
	return result, runErr
}

// continuedAtChecks is the stopped run made live again at its checks. Nothing is
// counted: no attempt, no round, no grant. The stage on the record is left as
// the bound ended it until the continued stage replaces it, so a record read in
// between still says where the last one stopped.
func continuedAtChecks(prior runstate.State, reason string, now time.Time) runstate.State {
	continued := prior
	command := ""
	if prior.CheckStage != nil {
		command = prior.CheckStage.Command
	}
	continued.CheckStageContinuations = append(append([]runstate.CheckStageContinuation{}, prior.CheckStageContinuations...),
		runstate.CheckStageContinuation{
			Command:           command,
			ContinuedAt:       now,
			Reason:            reason,
			SupersededFailure: prior.Failure,
		})
	continued.Failure = ""
	continued.Status = runstate.StatusRunning
	continued.Phase = runstate.PhaseChecking
	continued.CompletedAt = nil
	continued.SettledQuietSince = nil
	continued.CheckStageContinuationWaitNoted = ""
	continued.WorktreeRemoved = false
	continued.WorktreeSweptAt = nil
	continued.BranchRemoved = false
	continued.BranchSweptAt = nil
	continued.UpdatedAt = now
	return continued
}

// refuse writes down that the harness will not continue this run, puts the
// stoppage back on the docket for the development manager, and tells the item.
// The refusal is the result's; a record that could not be written is reported
// beside it.
func (c CheckStageContinuer) refuse(ctx context.Context, result CheckStageContinueResult, prior runstate.State, cause error) (CheckStageContinueResult, error) {
	result.Refused = runstate.RecordFailure(cause.Error())
	refused := prior
	refused.CheckStageContinuationRefused = result.Refused
	refused.UpdatedAt = c.now()
	var problems []string
	if err := c.Runs.Save(refused); err != nil {
		problems = append(problems, fmt.Sprintf("the refusal could not be written onto run %s, so the next pull asks again: %v", prior.RunID, err))
	} else if c.Redocket != nil {
		entry, err := docketedStoppage(c.Docket, prior.RunID, "hand to the development manager")
		if err == nil {
			err = c.closeEntry(entry, "the harness could not continue this stoppage at its checks and has put it back on the docket for the development manager: "+result.Refused)
		}
		if err == nil {
			_, err = c.Redocket.RecordStoppedRun(refused)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("the stoppage could not be put back in front of the development manager: %v", err))
		}
	}
	note := "The harness did not continue the check stage the bound stopped on run " + prior.RunID + ", and will not ask again: " + result.Refused + " What happens to it next is the development manager's decision."
	if _, err := c.Items.RecordOutcome(ctx, prior.WorkItemID, note); err != nil {
		problems = append(problems, fmt.Sprintf("the refusal could not be noted on %s: %v", prior.WorkItemID, err))
	}
	result.RecordProblem = strings.Join(problems, "; ")
	return result, cause
}

// NoteWaiting records a remaining gate after thirty minutes of durable
// eligibility. The completed run supplies the starting time, so neither a
// watcher restart nor a note changes the deadline. The last note is held on
// the run to avoid repeating it on every poll, including after a restart.
func (c CheckStageContinuer) NoteWaiting(ctx context.Context, runID, why, clears string) error {
	prior, lease, err := c.Runs.AdoptRun(ctx, runID)
	if err != nil {
		return err
	}
	defer lease.Release()
	if !prior.HarnessContinuesCheckStage() || prior.CompletedAt == nil || c.now().Before(prior.CompletedAt.Add(runstate.CheckStageContinuationWait)) {
		return nil
	}
	note := singleLine(fmt.Sprintf("The check-stage continuation of run %s has waited at least 30 minutes since it became eligible. Remaining gate: %s. What clears it: %s. No check has been counted as passed by waiting.", runID, why, clears), runstate.MaxBlockerBytes)
	if prior.CheckStageContinuationWaitNoted == note {
		return nil
	}
	if _, err := c.Items.RecordOutcome(ctx, prior.WorkItemID, note); err != nil {
		return fmt.Errorf("note why the check-stage continuation still waits on %s: %w", prior.WorkItemID, err)
	}
	prior.CheckStageContinuationWaitNoted = note
	prior.UpdatedAt = c.now()
	return c.Runs.Save(prior)
}

// closeEntry takes the stoppage off the docket in the harness's own name, for a
// run that is going again or one handed back to the development manager.
func (c CheckStageContinuer) closeEntry(entry triage.Entry, reason string) error {
	_, err := c.Docket.Close(triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           entry.Key,
		ProductID:     entry.ProductID,
		RunID:         entry.RunID,
		WorkItemID:    entry.WorkItemID,
		Decision:      continuedChecksDocketDecision,
		Reason:        singleLine(reason, triage.MaxMessageBytes),
		DecidedBy:     "the harness, continuing a check stage its bound stopped under load",
		ClosedAt:      c.now(),
	})
	return err
}

// checkStageContinueReason is what the run and the item record as why the run
// is going again.
func checkStageContinueReason(prior runstate.State) string {
	during := ""
	if prior.CheckStage != nil && prior.CheckStage.Command != "" {
		during = " during " + prior.CheckStage.Command
	}
	return fmt.Sprintf(
		"Continued at its checks: the check stage of run %s was stopped by load at its execution.check_stage_timeout bound%s, which judged nothing, so the harness continued the run itself at its checks on the change it already has, on the same branch and in the same worktree, with no developer attempt (continuation %d of %d). No review round, repair grant, or re-run was spent on it.",
		prior.RunID, during, len(prior.CheckStageContinuations)+1, runstate.MaxCheckStageContinuations)
}

func (c CheckStageContinuer) validate() error {
	var problems []error
	if c.Docket == nil {
		problems = append(problems, errors.New("continuing a check stage requires the triage docket the stoppage is on"))
	}
	if c.Runs == nil {
		problems = append(problems, errors.New("continuing a check stage requires the durable run state"))
	}
	if c.Intake == nil {
		problems = append(problems, errors.New("continuing a check stage requires the intake hold, because carrying work on is the harness choosing to"))
	}
	if c.Items == nil {
		problems = append(problems, errors.New("continuing a check stage requires the work item the stopped run holds"))
	}
	if c.Worktrees == nil {
		problems = append(problems, errors.New("continuing a check stage requires the worktree, because what the checks judge is whatever is in it"))
	}
	if c.Start == nil {
		problems = append(problems, errors.New("continuing a check stage requires a way to continue the run"))
	}
	if c.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which runs nothing; a continuation reads the same limit the reservation enforces", c.Capacity))
	}
	return errors.Join(problems...)
}

func (c CheckStageContinuer) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}
