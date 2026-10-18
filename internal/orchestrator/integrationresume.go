package orchestrator

// Resuming the integration of an approved change the environment stopped.
//
// This is the fourth thing the harness does about a docketed stoppage, and it
// is unlike the other three in one way that decides everything about it: it
// carries out no decision, because there was nothing to decide. The reviewer
// approved the change, and what stopped it short of the target branch was the
// environment — a primary checkout carrying somebody's uncommitted edit, a
// tracker read that timed out under load, a forge or a network that went away.
// None of that is a verdict on the work, and none of it is a question a
// development manager answers.
//
// The cause is read off the error that ended the run and never off the run's
// prose afterwards: a dirty checkout, a killed replay, a target the harness
// would not catch up, and a remote that refused the harness's credential each
// by the sentinel declared for it, and a transport that did not answer by the
// recovery package's closed reading of the error — the same reading that
// decides what the harness waits out and asks again at the boundaries that have
// a window, applied here to a step that has none. The diverged target and the
// refused credential are cleared by a person, but what they clear is the
// branches or the key rather than the change, so they are resumed like the
// rest once cleared, and refused in words that say what clears them until then. A worktree the convergence
// sweep retires while the run stands stopped is not a cause at all but a
// condition met at resume, and it is put back from the branch.
//
// Every verb that could pick such a run up before this existed spent something
// for it. A repair needs a failure returned to the developer and there was none,
// so it was refused; a re-run starts the item over and buys a fresh run and a
// fresh review for a change nobody disputed. yoyodyne-ifd.309's approved change
// stopped this way twice, and four operator overrides were signed to get it onto
// the target, every one paying for the environment rather than a verdict. This
// is the verb that ends that: the run resumes at the promotion it stopped short
// of, with its approval standing, and the item's counters stay where the review
// left them.
//
// # What it asks, and in what order
//
// Everything that can refuse is asked before anything is written, for the reason
// the repair action asks first: a re-entry half made — an item put back and a
// run not made live, or the reverse — is a state nothing else here notices. The
// record has to say the run is one of these; the checkout has to be one a
// promotion can be made from, because the run was refused for exactly that once
// already and re-entering it into the same refusal would make the run live for
// nothing; the item has to be one a run may continue on; and the harness has to
// have room. Then the worktree: as the harness left it and still holding the
// change, because what is promoted is whatever is in it. A worktree the
// convergence sweep retired while the run stood stopped is put back first, from
// the branch at the commit the run recorded — the branch still holds every
// commit the reviewer approved, so a retired checkout is a directory to
// recreate rather than a change to re-derive. That restore is the one write
// made before the re-entry, and it is recorded on the run as it is made, so a
// refusal after it leaves a stopped run whose checkout is back rather than a
// record that says the checkout is gone. Then the item is put back, the run is
// made live at the integrating phase, and the pipeline is asked to continue
// exactly that run.
//
// # What it charges
//
// Nothing. The resumption is recorded on the run as a continuation — the record
// of a run that was put back at its promotion — and not as an attempt: the
// repair count, the review evidence, and every counter on the item's durable
// triage record are untouched. A replay that conflicts is the one outcome that
// leaves this path, and it leaves it exactly as any replay conflict does: the
// run stops, both sides are preserved, and the blocker names it for a person.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// ResumeWorktrees proves the stopped run's worktree is still the one the harness
// left and still holds the approved change, and that the primary checkout is one
// a promotion can be made from. The last is asked here and not by the repair
// action because it is this stoppage's own cause: a run refused for a dirty
// checkout and made live again into the same checkout would stop again before
// it promoted anything, with its record saying it was resumed.
//
// RestoreWorktree puts a retired worktree back from its branch at the commit the
// run recorded, which the repair action has no equivalent of: a repair hands a
// developer whatever the checkout holds, and a checkout that was retired holds
// nothing to hand; a resumption promotes the reviewed commit, and that commit is
// on the branch whatever became of the directory.
//
// It is satisfied by gitworktree.Manager.
//
// TargetDivergence and VerifyRemoteAccess are the same question asked of the two
// causes a person clears outside the checkout: whether the target branch can be
// fast-forwarded onto the remote's again, and whether the remotes take the
// harness's credential again. Both are reads, so asking them writes nothing.
type ResumeWorktrees interface {
	RepairWorktrees
	ValidateReady(ctx context.Context) error
	RestoreWorktree(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Worktree, error)
	TargetDivergence(ctx context.Context, targetBranch string) (gitworktree.Catchup, error)
	VerifyRemoteAccess(ctx context.Context, targetBranch string) error
}

// ResumeDocket is the docket the resumption reads its stoppage from and settles
// it on. It is written as well as read, unlike a re-run's, because a resumption
// is the harness's own act rather than a decision somebody recorded: nothing
// else closes the entry, and a stoppage that stopped being one has to leave the
// docket or it is put to the development manager as a question with no answer.
//
// It is satisfied by runstate.DocketStore.
type ResumeDocket interface {
	List() ([]triage.Entry, error)
	Close(closure triage.Closure) (bool, error)
}

// IntegrationResumer resumes the integration of one approved change the
// environment stopped. It reads the work item and writes the resumption to it,
// it has no forge access of its own — the run it continues has that — and it
// decides nothing about the work: the reviewer decided, and this carries the
// approved change the rest of the way.
type IntegrationResumer struct {
	Docket ResumeDocket
	Runs   RepairRuns
	Intake IntakeHolds
	// Items is the work item the stopped run holds. Required: the run cannot be
	// resumed on an item that has been closed or made to wait on other work.
	Items RepairItems
	// Worktrees proves the checkout and the preserved worktree are what a
	// promotion needs. Required: what is promoted is whatever is in the worktree.
	Worktrees ResumeWorktrees
	// Remains asks the same repository as the docket and the hold.
	Remains readmodel.Remains
	// Capacity is execution.max_concurrent_developers as this carry-out read it.
	// Required: re-entry makes a terminal run live again, and a promotion holds a
	// slot for exactly as long as any run does.
	Capacity int
	Start    RepairContinueStarter
	Clock    execution.Clock
}

// IntegrationResumeRequest is one resumption to make: the run the docket entry
// names, and reasoning to record beside the harness's own, where somebody gave
// any. The reasoning is optional, unlike a repair's, because a resumption is
// not the development manager's decision: what the run records as why it is
// going again is the stop it supersedes, in the harness's words.
type IntegrationResumeRequest struct {
	Run    string
	Reason string
	// Asked says the resumption carries out the development manager's run.recover
	// request to finish this merge. The request on the run is then what the
	// resumption is made on where the docket holds no entry of the stoppage at
	// all, as for a run whose docketing was never written: the request is hers,
	// and the run's own record is what RecoveryFinishable accepted it on.
	Asked bool
}

// IntegrationResumeResult is what the action did. It reports the resumption it
// made and what became of the run, and it reports just as carefully when it
// resumed nothing: an intake hold, a full harness, and a refusal are three
// different things for an operator to do something about.
type IntegrationResumeResult struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	// Cause is the environmental cause of the stop this resumption supersedes,
	// and Stopped is that stop as the run recorded it.
	Cause   runstate.EnvironmentalCause `json:"cause,omitempty"`
	Stopped string                      `json:"stopped,omitempty"`
	// Reason is what the run and the item record as why this resumption exists.
	Reason  string `json:"reason"`
	Resumed bool   `json:"resumed"`
	// WorktreeRestored says the worktree the convergence sweep had retired was
	// put back from the branch before the run was resumed, at the path named.
	WorktreeRestored bool `json:"worktree_restored,omitempty"`
	// SupersededFailure and SupersededBlocker are what the run ended on, in the
	// words they were recorded in.
	SupersededFailure string `json:"superseded_failure,omitempty"`
	SupersededBlocker string `json:"superseded_blocker,omitempty"`
	// IntakeHeld is the operator's hold, when one is what stopped this. Nothing
	// was resumed and nothing was superseded.
	IntakeHeld *runstate.IntakeHold `json:"intake_held,omitempty"`
	// CapacityFull is every developer slot being occupied, when that is what this
	// is waiting on. It is not a refusal: nothing was written, and asking again
	// once a slot frees resumes the same run.
	CapacityFull *runstate.CapacityError `json:"capacity_full,omitempty"`
	Outcome      Outcome                 `json:"outcome"`
	// RecordProblem names a durable record this action could not write once it had
	// begun. It is reported beside the result rather than in place of it: the
	// docket entry a resumption could not close is a stoppage put to the
	// development manager as a question the run has already answered.
	RecordProblem string `json:"record_problem,omitempty"`
}

// ErrNotResumable is what a resumption refused for the shape of the stopped run
// unwraps to: it is not an approved change the environment stopped short of its
// promotion, so what it needs is one of the other triage verbs or a person.
var ErrNotResumable = errors.New("the stopped run is not an approved change the environment stopped short of its promotion")

// ErrWorktreeNotRestored is what a resumption refused because a retired worktree
// could not be put back unwraps to. Nothing was written: the run is still
// stopped, and the branch still holds the approved change.
var ErrWorktreeNotRestored = errors.New("the retired worktree could not be restored from its branch")

// ErrCheckoutNotReady is what a resumption refused for the primary checkout
// unwraps to. Nothing was written: the run is still stopped, the checkout is
// what has to change, and asking again once it has resumes the same run.
var ErrCheckoutNotReady = errors.New("the primary checkout is not one a promotion can be made from")

// resumedDocketDecision is the word the closure a resumption makes carries, so
// a reader of a closed entry can tell a stoppage the harness resumed from one
// the development manager decided about.
const resumedDocketDecision = triage.ResumedDecision

// Resume resumes one approved change's integration.
//
// The order is the order the guarantees need. Everything that can refuse is
// asked before anything is written, so a refused resumption leaves the run and
// the item exactly as they were and asking again once the refusal no longer
// applies resumes the same run; the item is put back before the run is made
// live, because a run recorded as running that nothing is running is the one
// half-finished state no other reader here would notice; and the docket entry is
// closed last, because a closure that could not be written is a fact to report
// beside a resumption that happened rather than a reason to refuse it.
func (r IntegrationResumer) Resume(ctx context.Context, request IntegrationResumeRequest) (IntegrationResumeResult, error) {
	if err := r.validate(); err != nil {
		return IntegrationResumeResult{}, err
	}
	runID := strings.TrimSpace(request.Run)
	if !runstate.ValidRunID(runID) {
		return IntegrationResumeResult{}, fmt.Errorf("%q is not a run identifier; a resumption names the run the docket entry is about", request.Run)
	}

	entry, err := docketedStoppage(r.Docket, runID, "resume")
	if errors.As(err, &NoDocketedStoppageError{}) && request.Asked {
		entry, err = r.askedStoppage(runID, err)
	}
	if err != nil {
		return IntegrationResumeResult{}, err
	}
	result := IntegrationResumeResult{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
	}
	// The run is adopted before anything is asked of it, and held until the
	// re-entry is written, for the reason the repair action holds it: a stopped
	// run is in flight to nothing else, and a sweep settling the same run beside
	// this must not lose either half.
	prior, lease, err := r.Runs.AdoptRun(ctx, entry.RunID)
	if err != nil {
		return result, fmt.Errorf("take the stopped run to resume its integration: %w", err)
	}
	defer lease.Release()

	found := readmodel.LookFor(ctx, r.Remains, prior)
	if err := stoppageIsOver(prior, found); err != nil {
		return result, err
	}
	if err := resumableStop(prior, found); err != nil {
		return result, err
	}
	result.Cause = prior.IntegrationStop.Cause
	result.Stopped = prior.IntegrationStop.Describe()
	result.SupersededFailure = prior.Failure
	result.SupersededBlocker = prior.Blocker
	// The checkout first, because it is this stoppage's own cause and the
	// cheapest thing here to ask: a promotion is made from the primary checkout,
	// and one the harness does not own is what refused this run once already.
	if err := r.Worktrees.ValidateReady(ctx); err != nil {
		return result, CheckoutNotReadyError{RunID: prior.RunID, Cause: err}
	}
	// Then the stop's own cause, where it is one a person clears somewhere other
	// than the checkout: a run made live into a target that still will not catch
	// up, or a remote that still refuses the key, stops again before it promotes
	// anything, with its record saying it was resumed.
	if err := r.causeCleared(ctx, prior); err != nil {
		return result, err
	}
	if err := noRunInFlight(r.Runs, entry.WorkItemID); err != nil {
		return result, err
	}
	item, err := r.Items.Show(ctx, entry.WorkItemID)
	if err != nil {
		return result, fmt.Errorf("read the work item the stoppage is about: %w", err)
	}
	if err := continuableItem(item, entry.WorkItemID); err != nil {
		return result, err
	}
	// The hold is read before anything is written. A promotion spends no provider,
	// but it is still the harness choosing to carry work on, and a held intake is
	// the operator saying not to choose anything more.
	hold, held, err := r.Intake.Held()
	if err != nil {
		return result, fmt.Errorf("read whether intake is held: %w", err)
	}
	if held {
		result.IntakeHeld = &hold
		return result, nil
	}
	// Capacity last, because it is the condition most likely to have changed
	// while the rest were asked and the one a moment's wait settles.
	full, free, err := slotIsFree(r.Runs, r.Capacity)
	if err != nil {
		return result, err
	}
	if !free {
		result.CapacityFull = &full
		return result, nil
	}
	// A worktree the sweep retired is put back before it is asked anything. It is
	// asked after the waits above rather than before them, because it is the one
	// thing here that writes before the re-entry: a held intake or a full harness
	// must leave the run exactly as it stopped, and a restore made and then waited
	// on would not.
	if !found.WorktreeThere {
		restored, err := r.restoreWorktree(ctx, prior)
		if err != nil {
			return result, err
		}
		prior = restored
		result.WorktreeRestored = true
	}
	// Then the worktree, on both of the repair action's conditions: as the harness
	// left it, and still holding the change. What is promoted is whatever is in
	// it, so both are a person's to decide about where they do not hold.
	if err := r.Worktrees.VerifyOwnedHead(ctx, worktreeOf(prior)); err != nil {
		return result, WorktreeSurgeryError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}
	}
	if err := preservedChangeHeld(ctx, r.Worktrees, prior); err != nil {
		return result, MissingPreservedChangeError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}
	}

	result.Reason = resumeReason(prior, strings.TrimSpace(request.Reason))

	// The item is put back first, for the reason the repair action puts it back
	// first: a run made live behind an item that still says it is stopped is one
	// nothing can resume and nothing will notice.
	if err := r.supersedeOnItem(ctx, entry.WorkItemID, result.Reason); err != nil {
		return result, err
	}
	if _, err := r.supersedeOnRun(prior, result.Reason); err != nil {
		return result, fmt.Errorf("record the resumption on run %s, whose item has already been put back and told why: %w", prior.RunID, err)
	}
	result.Resumed = true
	// The stoppage has stopped being one. Its entry is closed in the harness's own
	// name rather than left for the development manager to decide about a run
	// that is going again; a closure that could not be written is reported, and
	// the resumption stands.
	if err := r.closeEntry(entry, result.Reason); err != nil {
		result.RecordProblem = fmt.Sprintf("the docket entry for this stoppage could not be closed, so it is still put to the development manager although the run is going again: %v", err)
	}
	// The lease is given up before the run is continued, because continuing it is
	// the pipeline adopting the same run.
	lease.Release()

	outcome, runErr := r.Start(ctx, entry.WorkItemID, prior.RunID)
	result.Outcome = outcome
	return result, runErr
}

// askedStoppage is the stoppage a run.recover request to finish the merge stands
// for when the docket holds no entry of it. It is the entry the docket would
// hold, keyed as the docket keys it; with nothing on the docket to close, the
// closure the resumption then attempts is reported beside it rather than
// refusing it. A run whose record carries no such request is refused as it
// would have been.
func (r IntegrationResumer) askedStoppage(runID string, refused error) (triage.Entry, error) {
	state, err := r.Runs.Load(runID)
	if err != nil {
		return triage.Entry{}, fmt.Errorf("read run %s, whose stoppage is not on the docket, for the request to finish its merge: %w", runID, err)
	}
	if !state.RecoveryContinuationOutstanding(runstate.RecoverFinishMerge) {
		return triage.Entry{}, refused
	}
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, runID),
		Class:         triage.ClassStoppedRun,
		ProductID:     state.ProductID,
		RunID:         runID,
		WorkItemID:    state.WorkItemID,
		RecordedAt:    state.UpdatedAt,
	}, nil
}

// restoreWorktree puts a retired worktree back from its branch and records on
// the run that it is there again. The record is written at once, under the
// run's lease, rather than with the re-entry: a refusal past this point then
// leaves a stopped run whose checkout is back, which is true, instead of a run
// whose record says the checkout is gone while the directory stands.
func (r IntegrationResumer) restoreWorktree(ctx context.Context, prior runstate.State) (runstate.State, error) {
	if _, err := r.Worktrees.RestoreWorktree(ctx, worktreeOf(prior)); err != nil {
		return runstate.State{}, WorktreeRestoreError{RunID: prior.RunID, Branch: prior.Branch, Cause: err}
	}
	restored := prior
	restored.WorktreeRemoved = false
	restored.WorktreeSweptAt = nil
	restored.UpdatedAt = r.now()
	if err := r.Runs.Save(restored); err != nil {
		return runstate.State{}, fmt.Errorf("record that the worktree of run %s was restored at %s: %w", prior.RunID, prior.WorktreePath, err)
	}
	return restored, nil
}

// WorktreeRestoreError refuses a resumption whose retired worktree could not be
// put back from its branch. Nothing was written: the branch still holds the
// approved change, and what refused is named for a person.
type WorktreeRestoreError struct {
	RunID  string
	Branch string
	Cause  error
}

func (e WorktreeRestoreError) Error() string {
	return fmt.Sprintf(
		"the integration of run %s was not resumed, because the worktree the sweep retired could not be put back from %s: %v; nothing was written, and the branch still holds the approved change",
		e.RunID, e.Branch, e.Cause)
}

func (e WorktreeRestoreError) Unwrap() error { return ErrWorktreeNotRestored }

// CheckoutNotReadyError refuses a resumption into a primary checkout a promotion
// cannot be made from, which is what stopped the run in the first place. Nothing
// was written.
type CheckoutNotReadyError struct {
	RunID string
	Cause error
}

func (e CheckoutNotReadyError) Error() string {
	return fmt.Sprintf(
		"the integration of run %s was not resumed, because the primary checkout is still not one a promotion can be made from: %v; nothing was written, so asking again once the checkout is as the harness left it resumes the same run",
		e.RunID, e.Cause)
}

func (e CheckoutNotReadyError) Unwrap() error { return ErrCheckoutNotReady }

// ErrCauseStands is what a resumption refused because the cause of the stop is
// still there unwraps to. Nothing was written: the run is still stopped, and
// asking again once the cause has cleared resumes the same run.
var ErrCauseStands = errors.New("the cause of the integration stop still stands")

// CauseStandsError refuses a resumption whose stop a person has to clear and
// has not yet: a target branch that still cannot be fast-forwarded onto the
// remote's, or a remote that still refuses the harness's credential. It names
// what clears it, in the words the cause gives, so the refusal is an
// instruction rather than a second report of the stop.
type CauseStandsError struct {
	RunID string
	Cause runstate.EnvironmentalCause
	Found string
}

func (e CauseStandsError) Error() string {
	return fmt.Sprintf(
		"the integration of run %s was not resumed, because what stopped it still stands — %s (%s): %s; nothing was written, so asking again once you %s resumes the same run",
		e.RunID, e.Cause, e.Cause.Title(), e.Found, e.Cause.ClearedBy())
}

func (e CauseStandsError) Unwrap() error { return ErrCauseStands }

// causeCleared asks whether the cause of the stop is still there, for the two
// causes a person clears outside the primary checkout. The checkout's own cause
// is asked by ValidateReady for every stop, and the rest — a transport that did
// not answer, a replay the harness killed — are not standing conditions anybody
// can look at: the resumed promotion is the only way to ask them.
//
// A question that could not be answered refuses too. The run stays stopped with
// nothing written, which costs a second ask; making it live on an answer nobody
// got would cost a second stop recorded as a resumption.
func (r IntegrationResumer) causeCleared(ctx context.Context, prior runstate.State) error {
	cause := prior.IntegrationStop.Cause
	switch cause {
	case runstate.CauseDivergedTarget:
		catchup, err := r.Worktrees.TargetDivergence(ctx, prior.TargetBranch)
		if err != nil {
			return CauseStandsError{RunID: prior.RunID, Cause: cause, Found: fmt.Sprintf("whether %s can be caught up could not be asked: %v", prior.TargetBranch, err)}
		}
		if catchup.Held != "" {
			return CauseStandsError{RunID: prior.RunID, Cause: cause, Found: catchup.Held}
		}
	case runstate.CauseRemoteAuthRefused:
		if err := r.Worktrees.VerifyRemoteAccess(ctx, prior.TargetBranch); err != nil {
			found := fmt.Sprintf("whether the remotes take the credential could not be asked: %v", err)
			if errors.Is(err, gitworktree.ErrRemoteAuthRefused) {
				found = err.Error()
			}
			return CauseStandsError{RunID: prior.RunID, Cause: cause, Found: found}
		}
	}
	return nil
}

// resumableStop reports a stopped run whose integration there is something to
// resume, in the run's own record: an approved change, not promoted, that the
// environment stopped, on a branch and in a worktree that are still there. It
// says what is missing where the record does not say that, because each of the
// things it can be missing sends a reader somewhere different.
func resumableStop(prior runstate.State, found triage.Found) error {
	if prior.WorktreePath == "" || prior.Branch == "" || prior.BaseCommit == "" || prior.TargetBranch == "" {
		return fmt.Errorf("%w: run %s recorded no preserved worktree, so there is no approved change to promote", ErrNotResumable, prior.RunID)
	}
	// A retired worktree is put back from the branch, so only the branch going is
	// the end of it. A sweep that captured uncommitted work off the directory took
	// something the branch does not hold, and a restore that left it on the ref
	// would promote a checkout missing what the developer left: that is a
	// person's to look at rather than something to resume past.
	if found.Unknown || !found.BranchThere {
		return fmt.Errorf("%w: the branch run %s preserved is not verified as present, so there is no approved branch to promote: %s", ErrNotResumable, prior.RunID, found.Describe())
	}
	if !found.WorktreeThere && strings.TrimSpace(prior.PreservedWorkRef) != "" {
		return fmt.Errorf("%w: the sweep that retired run %s's worktree captured uncommitted work on %s, which the branch does not hold, so what a restored checkout would promote is not what the developer left; a person decides what becomes of that work", ErrNotResumable, prior.RunID, prior.PreservedWorkRef)
	}
	if prior.ProviderSessionID == "" {
		return fmt.Errorf("%w: run %s recorded no developer session, so its approval cannot be shown to have come from a second invocation", ErrNotResumable, prior.RunID)
	}
	if prior.Integration != nil {
		return fmt.Errorf("%w: run %s promoted its change already, so what is outstanding is its publication, which `yoyo reconcile` settles", ErrNotResumable, prior.RunID)
	}
	if prior.ReviewDecision != runstate.ReviewApprove || strings.TrimSpace(prior.ReviewSessionID) == "" {
		return fmt.Errorf("%w: run %s has no approving verdict standing, so there is nothing here authorized to promote; `yoyo triage repair` and `yoyo triage rerun` are what a change that was not approved needs", ErrNotResumable, prior.RunID)
	}
	// The bound on resumptions is asked here, before anything is written, rather
	// than met at the save that would refuse it: a refusal there would come after
	// the item had been put back, which is the half-made state the ordering above
	// exists to prevent. Sixteen environmental stops of one promotion is not a
	// promotion the environment is going to let through, and what it needs is a
	// person looking at the machine.
	if prior.ResumptionsLeft() == 0 {
		return fmt.Errorf("%w: run %s has been resumed %d times, which is the bound on one run's resumptions; an environment that has stopped the same promotion that often is a machine somebody has to look at, and `yoyo triage rerun` is what starts the item over once it is right", ErrNotResumable, prior.RunID, len(prior.IntegrationResumptions))
	}
	// A replay that conflicted is refused in the docket's own sentence for it
	// rather than in the general one below, because it is the stop a reader most
	// reaches for this verb on — the change is approved and stood at its
	// promotion — and the verb would replay onto the same target and meet it
	// again.
	if prior.ReplayConflict != nil {
		return fmt.Errorf("%w: %s", ErrNotResumable, prior.ReplayConflict.Says(prior.RunID))
	}
	if prior.IntegrationStop == nil {
		return fmt.Errorf("%w: run %s stopped after its approval for something the environment does not answer for — %s — so it is a person's to decide about", ErrNotResumable, prior.RunID, singleLine(nonEmpty(prior.Failure, prior.Blocker, "the record names no failure"), 240))
	}
	// Presence was checked above. The record-only predicate also reads the
	// removal flag, which cannot decide whether the branch still exists.
	if !prior.ApprovedAwaitingIntegration() {
		return fmt.Errorf("%w: run %s is not one this can resume", ErrNotResumable, prior.RunID)
	}
	return nil
}

// supersedeOnItem records the resumption on the work item and puts it back to
// work the harness may continue. Both halves are the supersession, for the
// reason the repair action's are: the note is what the next reader finds, and
// the claim is what stops the item saying it is stopped while its change is
// being promoted.
func (r IntegrationResumer) supersedeOnItem(ctx context.Context, workItemID, reason string) error {
	if _, err := r.Items.RecordOutcome(ctx, workItemID, reason); err != nil {
		return fmt.Errorf("record the resumption on %s: %w", workItemID, err)
	}
	item, _, err := r.Items.Claim(ctx, workItemID)
	if err != nil {
		return fmt.Errorf("put %s back to work for the promotion it was approved for: %w", workItemID, err)
	}
	if err := validateClaimedItem(item, workItemID); err != nil {
		return fmt.Errorf("validate the work item put back for its promotion: %w", err)
	}
	return nil
}

// supersedeOnRun makes the stopped run live again at its promotion, and reports
// the record it wrote.
//
// Nothing is counted. The repair count is what the repair action increments,
// because that action buys a developer invocation; this buys none, and a
// resumption that counted an attempt would hand the next prompt this run ever
// writes a number that describes an invocation nobody made. The review evidence
// is what the resumed promotion is authorized by and is left exactly as the
// reviewer left it.
func (r IntegrationResumer) supersedeOnRun(prior runstate.State, reason string) (runstate.State, error) {
	resumed := prior
	resumed.IntegrationResumptions = append(append([]runstate.IntegrationResumption{}, prior.IntegrationResumptions...),
		runstate.IntegrationResumption{
			Cause:             prior.IntegrationStop.Cause,
			Reason:            reason,
			ResumedAt:         r.now(),
			SupersededFailure: prior.Failure,
			SupersededBlocker: prior.Blocker,
			SupersededRefusal: prior.Environmental,
		})
	// The stop is superseded by the resumption that carries it, and the run that
	// is going again has neither failed nor stopped: a terminal run whose blocker
	// still stands is what the docket re-dockets and what `yoyo status` reports as
	// stopped work.
	resumed.IntegrationStop = nil
	resumed.Blocker = ""
	resumed.Failure = ""
	resumed.StopClass = ""
	// The environmental refusal on the record belongs to the round that ended;
	// that round settled and was paid back, and the promotion this resumes is not
	// a round at all. It is carried onto the resumption above rather than lost.
	resumed.Environmental = nil
	resumed.Status = runstate.StatusRunning
	resumed.Phase = runstate.PhaseIntegrating
	resumed.CompletedAt = nil
	resumed.SettledQuietSince = nil
	resumed.UpdatedAt = r.now()
	// A development manager's run.recover request to finish this merge is the
	// reason this resumption was made, wherever it was fired from, and it is
	// answered by it.
	resumed.TakeUpRecoveryContinuation(runstate.RecoverFinishMerge, r.now())
	resumed.RecoveryHold = nil
	if err := r.Runs.Save(resumed); err != nil {
		return runstate.State{}, err
	}
	return resumed, nil
}

// closeEntry takes the stoppage off the docket, in the harness's own name. It is
// keyed to the entry the resumption was decided against, so a run that stops
// again afterwards is docketed afresh: the stoppage's own moment is what the
// scan compares against the closure.
func (r IntegrationResumer) closeEntry(entry triage.Entry, reason string) error {
	_, err := r.Docket.Close(triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           entry.Key,
		ProductID:     entry.ProductID,
		RunID:         entry.RunID,
		WorkItemID:    entry.WorkItemID,
		Decision:      resumedDocketDecision,
		Reason:        singleLine(reason, triage.MaxMessageBytes),
		DecidedBy:     "the harness, resuming the integration of an approved change the environment stopped",
		ClosedAt:      r.now(),
	})
	return err
}

// resumeReason is what the run and the item record as why the run is going
// again: the stop it supersedes, in the harness's words, and any reasoning
// given to the command that resumed it, attributed to that rather than quoted
// as anybody's decision.
func resumeReason(prior runstate.State, reasoning string) string {
	reason := fmt.Sprintf(
		"Resumed: the integration of run %s was resumed at the %s phase with its approval standing, after the environment stopped it there — %s. No review round, repair grant, or re-run was spent on it; the item's counters stand where the review left them.",
		prior.RunID, prior.IntegrationStop.Phase, prior.IntegrationStop.Cause.Title())
	if reasoning == "" {
		return reason
	}
	reason += " The reasoning given to the harness when it was asked to: "
	// Folded to what the run's record will hold rather than refused, exactly as a
	// repair's reasoning is.
	return reason + singleLine(reasoning, runstate.MaxSelectionReasonBytes-len(reason))
}

func (r IntegrationResumer) validate() error {
	var problems []error
	if r.Docket == nil {
		problems = append(problems, errors.New("a resumption requires the triage docket the stoppage is on"))
	}
	if r.Runs == nil {
		problems = append(problems, errors.New("a resumption requires the durable run state"))
	}
	if r.Intake == nil {
		problems = append(problems, errors.New("a resumption requires the intake hold, because carrying work on is the harness choosing to"))
	}
	if r.Items == nil {
		problems = append(problems, errors.New("a resumption requires the work item, because the run that stopped holds it and a closed item is not one a run may be resumed on"))
	}
	if r.Worktrees == nil {
		problems = append(problems, errors.New("a resumption requires the worktree, because what is promoted is whatever is in it"))
	}
	if r.Start == nil {
		problems = append(problems, errors.New("a resumption requires a way to continue the run"))
	}
	if r.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which runs nothing; a resumption reads the same limit the reservation enforces", r.Capacity))
	}
	return errors.Join(problems...)
}

func (r IntegrationResumer) now() time.Time {
	if r.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return r.Clock.Now().UTC()
}

// Render describes what the action did, for whoever asked for it.
func (result IntegrationResumeResult) Render() string {
	var rendered strings.Builder
	if result.IntakeHeld != nil {
		fmt.Fprintf(&rendered, "INTAKE HELD since %s: %s\n",
			result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339), result.IntakeHeld.Says())
		fmt.Fprintf(&rendered, "nothing was resumed for %s and nothing was spent; `yoyo release` lifts the hold, and asking again resumes the same run\n",
			result.WorkItemID)
		return rendered.String()
	}
	if result.CapacityFull != nil {
		fmt.Fprintf(&rendered, "WAITING FOR A DEVELOPER SLOT: nothing was resumed for %s, %d active run(s), limit %d\n",
			result.WorkItemID, result.CapacityFull.Active, result.CapacityFull.Limit)
		fmt.Fprintf(&rendered, "nothing was spent, so asking again once a slot frees resumes the same run\n")
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "resumed the integration of run %s with its approval standing\n", result.RunID)
	fmt.Fprintf(&rendered, "the stop it supersedes: %s\n", result.Stopped)
	if result.WorktreeRestored {
		fmt.Fprintln(&rendered, "the worktree the sweep had retired was put back from the branch at the reviewed commit")
	}
	fmt.Fprintln(&rendered, "charged nothing: no review round, no repair grant, no re-run")
	if result.SupersededBlocker != "" {
		fmt.Fprintf(&rendered, "superseded blocker: %s\n", singleLine(result.SupersededBlocker, 240))
	}
	if result.RecordProblem != "" {
		fmt.Fprintln(&rendered, result.RecordProblem)
	}
	return rendered.String()
}
