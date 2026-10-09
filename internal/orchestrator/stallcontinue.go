package orchestrator

// Continuing a first silent-stream stall, by the harness itself.
//
// The harness stops a provider invocation whose stream has gone silent for
// longer than it allows, or that is still working when its total budget runs
// out, and leaves the run in flight to be continued; both are continued here
// (runstate.State.SettledSilentStreamStall). Nothing
// continues it, so half an hour later the reconciling sweep settles it and
// dockets it. Until yoyodyne-a0s that entry waited on the development manager
// recording a repair, which the carry-out then made as a continuation of the
// same session at the point it stalled, spending no review round and no repair
// attempt — a decision about a stop the harness made itself, that judged
// nothing. Two runs in two days waited on exactly that.
//
// So the harness makes that continuation itself, the way it continues a check
// stage its bound stopped: at the next pull with a developer slot free, in the
// same worktree and developer session, at the phase the run stalled in, with no
// decision recorded and nothing spent. It does so at most
// runstate.MaxHarnessStallContinuations times for one run; a run that stalls
// again is settled and docketed for the development manager, as every stall was
// before.
//
// It is held to what the development manager's own repair of a stall is held
// to: the worktree has to be as the harness left it, and a stall at the checks
// or the review has to still hold the change the attempt made, because that is
// what the step judges. A worktree that fails either is a person's to look at,
// so the refusal is written onto the run, the stoppage is put back on the docket
// for the development manager, and the harness does not ask again.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// StallContinuer continues one run the harness stopped for a silent provider
// stream, in the session and at the phase it stalled in. It decides nothing
// about the work and spends nothing: the harness stopped the provider, and this
// is the harness carrying the run the rest of the way.
type StallContinuer struct {
	Docket ResumeDocket
	// Redocket is what puts a stoppage whose continuation was refused back in
	// front of the development manager. Optional: without it the refusal is still
	// written onto the run and the item.
	Redocket CheckStageRedocket
	Runs     RepairRuns
	Intake   IntakeHolds
	// Items is the work item the stopped run holds. Required: a closed item, or
	// one made to wait on other work, is not one a run may be continued on.
	Items RepairItems
	// Worktrees proves the worktree is as the harness left it, and that a stall
	// past the attempt still holds the change. Required.
	Worktrees RepairWorktrees
	// Capacity is execution.max_concurrent_developers. Required: the continued
	// run holds a slot for exactly as long as any run does.
	Capacity int
	// Backends and Events are the repair continuer's, for the same reasons: a
	// stall carried on in its developer attempt is carried on in the session the
	// run recorded, on the backend that opened it.
	Backends DeveloperBackends
	Events   RunEvents
	Start    RepairContinueStarter
	Clock    execution.Clock
}

// StallContinueRequest names the run the docket entry is about.
type StallContinueRequest struct {
	Run string
}

// StallContinueResult is what the action did, and just as carefully what it
// did not: an intake hold, a full harness, and a refusal are three different
// things for somebody to know about.
type StallContinueResult struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	// ResumesAt is the phase the run was put back at: the developer attempt it
	// stalled in, or the checks or review after it.
	ResumesAt runstate.Phase `json:"resumes_at,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Continued bool           `json:"continued"`
	// SupersededBlocker is the blocker the settled stall left on the item and the
	// run, in the words it was recorded in.
	SupersededBlocker string `json:"superseded_blocker,omitempty"`
	// IntakeHeld and CapacityFull are the two waits. Nothing was written for
	// either, and the next pull asks again.
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

// ErrNotHarnessContinuedStall is what a continuation refused for the shape of
// the stopped run unwraps to: it is not a first silent-stream stall the harness
// still continues.
var ErrNotHarnessContinuedStall = errors.New("the stopped run is not a stall the harness continues itself")

// continuedStallDocketDecision is the word the closure a continuation makes
// carries, so a reader of a closed entry can tell a stall the harness continued
// from one the development manager decided about.
const continuedStallDocketDecision = "continued after a stall"

// Due reports a run the harness would continue now, as far as its record can
// say: a settled first silent-stream stall whose continuation is not spent. It
// writes nothing. What the moment also has to allow — a free slot, the
// operator's switches — is asked by Continue, where a refusal is reported.
func (c StallContinuer) Due(runID string) (bool, error) {
	if c.Runs == nil {
		return false, errors.New("continuing a stall requires the durable run state")
	}
	state, err := c.Runs.Load(runID)
	if err != nil {
		return false, fmt.Errorf("read run %s to see whether its stall is to be continued: %w", runID, err)
	}
	return state.HarnessContinuesStall(), nil
}

// Continue continues one first silent-stream stall, at the phase it stalled in.
//
// The order is the one the repair of a stall keeps, for the same reasons:
// everything that can refuse is asked before anything is written, the item is
// put back before the run is made live, and the docket entry is closed last.
func (c StallContinuer) Continue(ctx context.Context, request StallContinueRequest) (StallContinueResult, error) {
	if err := c.validate(); err != nil {
		return StallContinueResult{}, err
	}
	runID := strings.TrimSpace(request.Run)
	if !runstate.ValidRunID(runID) {
		return StallContinueResult{}, fmt.Errorf("%q is not a run identifier; a continuation names the run the docket entry is about", request.Run)
	}
	entry, err := docketedStoppage(c.Docket, runID, "continue after its stall")
	if err != nil {
		return StallContinueResult{}, err
	}
	result := StallContinueResult{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
	}
	prior, lease, err := c.Runs.AdoptRun(ctx, entry.RunID)
	if err != nil {
		return result, fmt.Errorf("take the stalled run to continue it: %w", err)
	}
	defer lease.Release()

	// Restored before the record is asked whether it holds a session, and
	// written with the continuation, as a repair's is (RepairContinuer.Continue).
	restored := c.Backends.restoreSession(c.Events, &prior)
	if !prior.HarnessContinuesStall() {
		return result, fmt.Errorf("%w: %s", ErrNotHarnessContinuedStall, nonEmpty(prior.StallStopSays(), fmt.Sprintf("run %s did not end on a silent-stream stall the sweep settled", prior.RunID)))
	}
	if owner := strings.TrimSpace(prior.WorkItemID); owner != entry.WorkItemID {
		return result, fmt.Errorf("run %s is recorded as made for %q while its docket entry names %s, so continuing it would carry one item's run on as another's work; nothing was spent", prior.RunID, owner, entry.WorkItemID)
	}
	result.SupersededBlocker = prior.Blocker
	result.ResumesAt = prior.Phase
	if err := noRunInFlight(c.Runs, entry.WorkItemID); err != nil {
		return result, err
	}
	if result.ResumesAt == runstate.PhaseDeveloping {
		if err := c.Backends.refuse(prior); err != nil {
			return c.refuse(ctx, result, prior, err)
		}
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
	// The worktree last, on the repair's conditions. Either failing is a person's
	// to look at, so it is written down and the harness stops asking. A stall in
	// the attempt need not hold a change yet: an empty worktree is exactly what
	// the attempt it is owed starts from.
	if err := c.Worktrees.VerifyOwnedHead(ctx, worktreeOf(prior)); err != nil {
		return c.refuse(ctx, result, prior, WorktreeSurgeryError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err})
	}
	if resumesAnExistingChange(prior) {
		if err := preservedChangeHeld(ctx, c.Worktrees, prior); err != nil {
			return c.refuse(ctx, result, prior, MissingPreservedChangeError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err})
		}
	}

	result.Reason = stallContinueReason(prior, result.ResumesAt)
	if restored != "" {
		result.Reason = restoredSessionSays(restored) + " " + result.Reason
	}
	if _, err := c.Items.RecordOutcome(ctx, entry.WorkItemID, itemRecord(result.Reason, item, prior)); err != nil {
		return result, fmt.Errorf("record the continuation on %s: %w", entry.WorkItemID, err)
	}
	claimed, _, err := c.Items.Claim(ctx, entry.WorkItemID)
	if err != nil {
		return result, fmt.Errorf("put %s back to work for the stall it is owed: %w", entry.WorkItemID, err)
	}
	if err := validateClaimedItem(claimed, entry.WorkItemID); err != nil {
		return result, fmt.Errorf("validate the work item put back after its stall: %w", err)
	}
	if err := c.Runs.Save(continuedAfterStall(prior, result.Reason, c.now())); err != nil {
		return result, fmt.Errorf("record the continuation on run %s, whose item has already been put back and told why: %w", prior.RunID, err)
	}
	result.Continued = true
	if err := c.closeEntry(entry, result.Reason); err != nil {
		result.RecordProblem = fmt.Sprintf("the docket entry for this stoppage could not be closed, so it still reads as a stoppage although the run is going again: %v", err)
	}
	// Given up before the run is continued, because continuing it is the
	// pipeline adopting the same run.
	lease.Release()

	outcome, runErr := c.Start(ctx, entry.WorkItemID, prior.RunID)
	result.Outcome = outcome
	return result, runErr
}

// continuedAfterStall is the settled stall made live again at the phase it
// stalled in. The continuation it records is the harness's own and grants
// nothing, so no attempt, round, or grant is counted; it is what the pipeline
// adopts the run on, as it adopts a stall the development manager decided a
// repair of, and what bounds the harness to one continuation for the run.
func continuedAfterStall(prior runstate.State, reason string, now time.Time) runstate.State {
	continued := prior
	continued.RepairContinuations = append(append([]runstate.RepairContinuation{}, prior.RepairContinuations...),
		runstate.RepairContinuation{
			Reason:            reason,
			ContinuedAt:       now,
			SupersededBlocker: prior.Blocker,
			Stall:             true,
			ByHarness:         true,
		})
	continued.Blocker = ""
	continued.Failure = ""
	continued.Environmental = nil
	continued.Status = runstate.StatusRunning
	continued.Phase = prior.Phase
	continued.CompletedAt = nil
	continued.SettledQuietSince = nil
	continued.UpdatedAt = now
	return continued
}

// refuse writes down that the harness will not continue this run, puts the
// stoppage back on the docket for the development manager, and tells the item.
func (c StallContinuer) refuse(ctx context.Context, result StallContinueResult, prior runstate.State, cause error) (StallContinueResult, error) {
	result.Refused = runstate.RecordFailure(cause.Error())
	refused := prior
	refused.StallContinuationRefused = result.Refused
	refused.UpdatedAt = c.now()
	var problems []string
	if err := c.Runs.Save(refused); err != nil {
		problems = append(problems, fmt.Sprintf("the refusal could not be written onto run %s, so the next pull asks again: %v", prior.RunID, err))
	} else if c.Redocket != nil {
		entry, err := docketedStoppage(c.Docket, prior.RunID, "hand to the development manager")
		if err == nil {
			err = c.closeEntry(entry, "the harness could not continue this stall itself and has put it back on the docket for the development manager: "+result.Refused)
		}
		if err == nil {
			_, err = c.Redocket.RecordStoppedRun(refused)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("the stoppage could not be put back in front of the development manager: %v", err))
		}
	}
	note := "The harness did not continue run " + prior.RunID + " after its stall, and will not ask again: " + result.Refused + " What happens to it next is the development manager's decision."
	if _, err := c.Items.RecordOutcome(ctx, prior.WorkItemID, note); err != nil {
		problems = append(problems, fmt.Sprintf("the refusal could not be noted on %s: %v", prior.WorkItemID, err))
	}
	result.RecordProblem = strings.Join(problems, "; ")
	return result, cause
}

// closeEntry takes the stoppage off the docket in the harness's own name, for a
// run that is going again or one handed back to the development manager.
func (c StallContinuer) closeEntry(entry triage.Entry, reason string) error {
	_, err := c.Docket.Close(triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           entry.Key,
		ProductID:     entry.ProductID,
		RunID:         entry.RunID,
		WorkItemID:    entry.WorkItemID,
		Decision:      continuedStallDocketDecision,
		Reason:        singleLine(reason, triage.MaxMessageBytes),
		DecidedBy:     "the harness, continuing a run whose AI session it stopped for going silent or running out of its total budget",
		ClosedAt:      c.now(),
	})
	return err
}

// stallContinueReason is what the run and the item record as why the run is
// going again.
func stallContinueReason(prior runstate.State, resumesAt runstate.Phase) string {
	where := "in the developer session it stalled in, at the attempt the harness stopped it in"
	if resumesAt != runstate.PhaseDeveloping {
		where = fmt.Sprintf("at the %s phase it stalled in, on the change its completed developer attempt left, with no developer attempt", resumesAt)
	}
	readopted := ""
	if says := prior.ReadoptedSays(); says != "" {
		readopted = " Earlier, " + says + "."
	}
	return singleLine(fmt.Sprintf(
		"Continued after a stall: the AI session running run %s %s, so the harness stopped it and the sweep settled it; the cause was outside the work and nothing was judged, so the harness continued the run itself %s, in the same worktree, with no decision asked of anybody (continuation %d of %d). No review round, repair attempt, repair grant, or re-run was spent on it; if it is stopped this way again, it is docketed for the development manager.%s",
		prior.RunID, prior.HarnessStopSays(), where, prior.HarnessStallContinuations()+1, runstate.MaxHarnessStallContinuations, readopted), runstate.MaxSelectionReasonBytes)
}

func (c StallContinuer) validate() error {
	var problems []error
	if c.Docket == nil {
		problems = append(problems, errors.New("continuing a stall requires the triage docket the stoppage is on"))
	}
	if c.Runs == nil {
		problems = append(problems, errors.New("continuing a stall requires the durable run state"))
	}
	if c.Intake == nil {
		problems = append(problems, errors.New("continuing a stall requires the intake hold, because carrying work on is the harness choosing to"))
	}
	if c.Items == nil {
		problems = append(problems, errors.New("continuing a stall requires the work item the stopped run holds"))
	}
	if c.Worktrees == nil {
		problems = append(problems, errors.New("continuing a stall requires the worktree, because what the continued run works in is whatever is in it"))
	}
	if c.Start == nil {
		problems = append(problems, errors.New("continuing a stall requires a way to continue the run"))
	}
	if c.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which runs nothing; a continuation reads the same limit the reservation enforces", c.Capacity))
	}
	return errors.Join(problems...)
}

func (c StallContinuer) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}
