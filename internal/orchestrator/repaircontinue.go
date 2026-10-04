package orchestrator

// Re-entering a stopped run's repair loop, on the development manager's
// decision.
//
// This is the second triage decision the harness carries out, and it is the
// opposite of the first. A re-run is for a change whose ground moved: nothing
// about the work was wrong, so it starts over against the repository as it now
// stands. This is for a change that is nearly right and ran out of attempts —
// the reviewer's findings are the ones worth acting on, and the developer that
// wrote the change is the one to act on them. So nothing starts over: the same
// run continues, on the branch and in the worktree it stopped in, in the
// developer session that already holds the context, with the findings handed
// back exactly as the reviewer wrote them.
//
// It carries out one other stoppage, and the two differ in what they hand the
// developer rather than in what they do. A run the harness stopped on time
// before anything was returned to its developer — a stall — is continued at the
// attempt it was stopped in, in the same session, with nothing handed back
// because nothing judged anything. Everything below holds for it identically,
// with two exceptions named where they are made: the preserved worktree need
// not already hold a change, and the continuation counts no repair attempt. A
// stall at the checks or the review, after the attempt finished, keeps only the
// second: it is continued at that step on the change it has, with no developer
// invoked, so that change has to be there as it does for a repair.
//
// The decision is not this package's, and neither is the size of what it grants.
// The development manager records a repair, which spends the item's repair-grant
// budget as it is recorded and is truncated there to the review rounds the cap
// still had room for. What reaches here is the run the docket entry names and
// nothing else: the decision, the grant, and the reasoning it was made on are
// read from the item's durable triage record, where the development manager's
// own conversation wrote them, and everything here is the harness acting on
// that: proving the stoppage is over, proving the worktree is still the one the
// harness left, reading whether it may spend on a provider at all, superseding
// the blocker, and continuing the run.
//
// # Why the reasoning is read and not given
//
// This verb used to take the reasoning as a flag and record it on the run and
// the item as the account of why the run was going again — the same unchecked
// attribution yoyodyne-ifd.311 took out of the re-run. A repair decision has been
// durable since then too, written by the grant that spends its budget, so it is
// read here the same way: a stoppage with no repair recorded about it is refused
// naming the missing record, and the account the run and the item carry cites
// the decision it was built from. The decision names the run it is about, and it
// is read from the record of the item that run was made for, so a repair can only
// ever be carried out against the run it names: one recorded about some other
// item's run is not found here, and is refused rather than carried out against
// whatever this item has on the docket.
//
// # Why nothing is written until everything has been asked
//
// The grant is already spent when this runs, so what this action can waste is
// the other thing: a re-entry half made. A condition asked after the item has
// been put back, or after the run has been made live, leaves one of the two
// saying something the other contradicts — and a run recorded as running that
// nothing is running is a state no other reader here would notice. That is not
// hypothetical in shape: the first exercised triage re-run asked the item's
// status past the claim that bounded it, and the next attempt was then refused
// by the once-only guard for a run that had never happened. So everything that
// can refuse without a checkout is asked first. A missing checkout is restored
// only after the decision, hold and capacity pass, with verification credit
// cleared durably first. The writes that carry the grant out remain last.
//
// # Why the blocker is superseded rather than left standing
//
// A run that is going again has not stopped, and three readers take the blocker
// on its record as the fact that it has: `yoyo status`, reconciliation, and the
// docket, which re-dockets any terminal run whose blocker still stands. Leaving
// it would make the record lie to all three, and would make the work item —
// blocked by the run that stopped — one the pipeline refuses to resume. So both
// are superseded at the moment of re-entry: the item is put back and told why,
// and the run's blocker is cleared onto the continuation that supersedes it,
// which keeps the words it was recorded in.
//
// # Why the worktree has to be as the harness left it
//
// What a continued developer is handed back is whatever is in that worktree
// now. A worktree somebody has been operating on by hand — or one an agent
// committed in — is one where continuing would hand a developer work nothing
// recorded and then put it through a gate that assumes the harness owns every
// commit. That is a person's decision rather than this action's, so re-entry
// passes the same HEAD-state ownership check every read of a worktree's change
// already passes, and refuses to a person where it does not.
//
// # Why the change has to be in it
//
// That check says the checkout is untouched; it does not say anything is there.
// A handback seeded from the target branch rather than from the preserved one is
// a worktree the harness would call its own and that holds none of the work the
// findings are about — and a developer handed the findings and an empty
// directory delivers an empty repair or reinvents the change, with nothing in
// the run's record afterwards to tell either from a repair that went well. Four
// same-day rounds were lost that way before anything asked. So the change is
// asked for as well as the checkout, before the grant is spent, and the resumed
// run asks again where it would invoke the developer.
//
// # What the developer is given
//
// Nothing here assembles it. The repair input the run recorded — the reviewer's
// findings, the failing check, the refused paths, or the replay conflict — is
// what the resumed run hands back, rebuilt from durable state by the pipeline
// exactly as it is for a run an interrupted process left mid-repair. Guidance a development manager
// wants to add travels in the work item's notes, which every run's context
// bundle already carries, and which cannot grant a protected path.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// RepairRuns is the durable run state this action reads and writes. It is the
// re-run's set with one more read: what the harness has already carried out of
// an item's repair grant lives on the runs it was carried out on, so counting it
// takes every run the item has had rather than the one being continued.
//
// It is satisfied by runstate.Store.
type RepairRuns interface {
	RerunRuns
	Recorded() ([]runstate.State, error)
}

// RepairItems is the work item the stopped run is about. Unlike a re-run's, this
// is read and written: the item was blocked by the run stopping, and a re-entry
// that left it blocked would be a continuation the pipeline itself refuses to
// resume. What is written is the triage decision and the claim that supersedes
// the blocker, and nothing else — what becomes of the item is the continued
// run's to record, as it always was.
//
// It is satisfied by beads.Client.
type RepairItems interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
	RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error)
	Claim(ctx context.Context, id string) (beads.WorkItem, *beads.StaleBlockClear, error)
}

// RepairWorktrees proves the stopped run's worktree is still the one the harness
// left, and that it still holds the change the repair is about. The first is the
// architect's condition on re-entry and it is deliberately the existing gate
// rather than a second one: anything touched since the blocker refuses, and a
// refusal is a person's to decide about. The second is the same question asked
// of the change rather than of the checkout — a worktree that is exactly as the
// harness left it and empty passes the first and fails this one.
//
// It is satisfied by gitworktree.Manager.
type RepairWorktrees interface {
	VerifyOwnedHead(ctx context.Context, worktree gitworktree.Worktree) error
	PreservedChanges
}

// RestorableWorktrees is needed only when the repository verified a checkout
// missing. Restoration checks out the recorded branch; it creates no run.
type RestorableWorktrees interface {
	RestoreWorktree(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Worktree, error)
	Inspect(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Inspection, error)
}

// PreservedChanges reads what a preserved worktree holds. It is the whole of
// what proving a handback carries its change needs, and it is stated apart from
// the gate above so the pipeline's own worktree manager satisfies it too: what
// this action asks before it spends and what the resumed run enforces before it
// invokes a developer are one question rather than two.
//
// It is satisfied by gitworktree.Manager.
type PreservedChanges interface {
	ChangedPaths(ctx context.Context, worktree gitworktree.Worktree) ([]string, error)
}

// RepairContinueStarter re-enters the harness on one run of one work item. It
// names the run because that is the whole of what this action dispatches: the
// run recorded as running and resumable that it has just left behind, adopted
// and continued rather than a fresh one started. No selection is passed, because
// why this run exists was recorded when it was reserved and why it is going
// again is recorded on the run and the item by the action itself.
//
// It is satisfied by Pipeline.Continue, which re-enters the run named or refuses
// — every recorded loss of a repair round was a dispatch that started something
// fresh instead, so what this type asks for is deliberately not something a
// fresh run can satisfy.
type RepairContinueStarter func(ctx context.Context, workItemID, runID string) (Outcome, error)

// RepairContinuer re-enters one stopped run's repair loop under a grant of
// further repair attempts. It reads the work item and writes the triage decision
// to it, it has no forge access, and it decides nothing about the work: what it
// does is check that a decision somebody else made may be carried out, and then
// carry it out.
type RepairContinuer struct {
	Docket RerunDocket
	Runs   RepairRuns
	Intake IntakeHolds
	// Decisions is the item's durable triage record, which is what says the
	// development manager granted this repair and how much it is worth. Required:
	// a continuation carried out on nobody's grant would be a repair budget of
	// this action's own invention.
	Decisions RerunDecisions
	// Items is the work item the stopped run blocked. Required: the run cannot be
	// resumed while its item says it is waiting on a person.
	Items RepairItems
	// Worktrees proves the preserved worktree is still as the harness left it and
	// still holds the change. Required: the change handed back is whatever is in
	// it, and a handback carrying nothing is a repair of nothing.
	Worktrees RepairWorktrees
	// Remains asks the repository for the branch and checkout every continuation
	// needs, by the same look as the docket and the hold. An integration stop
	// names the resume while its branch is there and the re-run once it is gone.
	Remains readmodel.Remains
	// ConfiguredAttempts is `execution.repair_attempts_before_replan`, which is
	// the budget the continued run's loop adds its grants to. It is read here
	// only to report what the run may now spend in total: a carry-out that
	// reported the grant alone would say a run with four attempts had two.
	ConfiguredAttempts int
	// Capacity is execution.max_concurrent_developers as this carry-out read it.
	// Required: re-entry makes a terminal run live again, so a continuation the
	// harness has no room for would put one more developer to work than the
	// operator configured.
	Capacity int
	Start    RepairContinueStarter
	Clock    execution.Clock
}

// RepairContinueRequest is one decision to carry out: the run the docket entry
// names, and nothing else. The reasoning is not asked for and cannot be given —
// it is read from the decision the development manager recorded, because a
// continuation that took it from whoever typed the command would carry an
// attribution to a role that never wrote those words.
type RepairContinueRequest struct {
	Run string
}

// RepairContinueResult is what the action did. It reports the continuation it
// made and what became of the run, and it reports just as carefully when it
// continued nothing: an intake hold, a full harness, and a refusal are three
// different things for an operator to do something about.
type RepairContinueResult struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	// Reason is what the run and the item record as why this continuation exists:
	// the development manager's recorded decision, cited to the record it was read
	// from, and the reasoning it was recorded with.
	Reason string `json:"reason"`
	// Granted is what this re-entry added to the run's repair budget, out of the
	// Decided rounds the development manager's grant is worth. Truncated says the
	// item's record has a grant the round cap cut, which is what says the item is
	// at the end of what it will be given.
	Granted   int  `json:"granted,omitempty"`
	Decided   int  `json:"decided,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
	// RepairBudget is what the continued run may now spend in total, and
	// RepairAttempts what it had already spent when it stopped.
	RepairBudget     int  `json:"repair_budget,omitempty"`
	RepairAttempts   int  `json:"repair_attempts,omitempty"`
	Continued        bool `json:"continued"`
	WorktreeRestored bool `json:"worktree_restored,omitempty"`
	// Stall says what was carried out was a stalled attempt being carried on
	// rather than a change being repaired: the harness stopped this run's
	// provider before anything was returned to its developer, so the
	// continuation resumes that session at the point it stalled and counts no
	// repair attempt. It is reported because the two cost the item different
	// things, and a reader told only that a repair was carried out would read
	// the attempt counters below as a run that had spent one.
	Stall bool `json:"stall,omitempty"`
	// Checks says a stage stopped at its bound was continued at the checks.
	Checks bool `json:"checks,omitempty"`
	// ResumesAt is the step the continued run was put back at. A stage bound
	// continues its checks, and a stall at the checks or
	// the review is continued at that step, on the change the attempt left, with
	// no developer invoked.
	ResumesAt runstate.Phase `json:"resumes_at,omitempty"`
	// SupersededBlocker is the durable blocker the re-entry cleared, in the words
	// it was recorded in.
	SupersededBlocker string `json:"superseded_blocker,omitempty"`
	// IntakeHeld is the operator's hold, when one is what stopped this. Nothing
	// was carried out and nothing was superseded.
	IntakeHeld *runstate.IntakeHold `json:"intake_held,omitempty"`
	// CapacityFull is every developer slot being occupied, when that is what this
	// is waiting on. It is not a refusal and not a failure: nothing was spent, the
	// decision stands, and asking again once a slot frees carries out the same one.
	CapacityFull *runstate.CapacityError `json:"capacity_full,omitempty"`
	Outcome      Outcome                 `json:"outcome"`
	// RecordProblem names a durable record this action could not write once it had
	// begun carrying the grant out. It is reported beside the result rather than
	// in place of it, and never left unsaid: half a re-entry is exactly the state
	// nothing else here will notice.
	RecordProblem string `json:"record_problem,omitempty"`
}

// ErrWorktreeNotAsLeft is what a re-entry refused for the state of the preserved
// worktree unwraps to, so a caller can tell "somebody has been in here" from a
// repository that could not be read without matching on the words of either.
var ErrWorktreeNotAsLeft = errors.New("the preserved worktree is not as the harness left it")

// WorktreeSurgeryError refuses a continuation of a worktree something has
// touched since the run stopped, and says what it is being escalated to a person
// for. Nothing was spent and nothing was superseded: the item is still blocked,
// which is the durable state an escalation would have made anyway.
type WorktreeSurgeryError struct {
	RunID        string
	WorktreePath string
	Cause        error
}

func (e WorktreeSurgeryError) Error() string {
	return fmt.Sprintf(
		"the worktree run %s preserved at %s is not as the harness left it, so its repair loop was not re-entered and nothing was spent: %v; what is in that worktree is what a continued developer would be handed back, so this is a person's to look at — the item stays blocked, and re-entry is refused until somebody says what became of the change",
		e.RunID, e.WorktreePath, e.Cause)
}

func (e WorktreeSurgeryError) Unwrap() []error { return []error{ErrWorktreeNotAsLeft, e.Cause} }

// ErrPreservedChangeMissing is what a re-entry refused for a worktree holding
// none of the change it was picked up to continue unwraps to, so a caller can
// tell that apart from a worktree somebody has been operating in without
// matching on the words of either.
var ErrPreservedChangeMissing = errors.New("the preserved worktree holds none of the change it was picked up to continue")

// MissingPreservedChangeError refuses a continuation whose preserved worktree no
// longer holds the change the reviewer's findings are about. Nothing was spent
// and nothing was superseded: the item is still blocked, which is the durable
// state an escalation would have made anyway.
type MissingPreservedChangeError struct {
	RunID        string
	WorktreePath string
	Cause        error
}

func (e MissingPreservedChangeError) Error() string {
	return fmt.Sprintf(
		"the worktree run %s preserved at %s holds none of the change it was handed back to repair, so its repair loop was not re-entered and nothing was spent: %v; a repair continues a change that already exists, and re-entering here would hand a developer the findings about that change and an empty worktree — which is delivered as an empty repair or as the same change reinvented, and neither is something the run says afterwards. This is a person's to look at: the item stays blocked, and re-entry is refused until somebody says what became of the change",
		e.RunID, e.WorktreePath, e.Cause)
}

func (e MissingPreservedChangeError) Unwrap() error { return ErrPreservedChangeMissing }

// preservedChangeHeld proves the worktree a handback would re-enter still holds
// the change that handback is about, and says what it found where it does not.
//
// It is the one question this whole item turns on. What a continued developer is
// handed is whatever is in that worktree, and the prompt handed with it is a
// failure about a change the run already made; the two agree only while the
// change is there. A handback that arrives on a clean worktree reads to a
// developer as work to derive from the reviewer's findings, and it has been
// delivered as exactly that — an empty repair, or ten files reconstructed by
// hand against a base that had moved — with nothing in the run's own record
// afterwards to tell it from a repair that went well.
//
// A worktree that cannot be read at all is the same answer rather than a
// different one: the change is not there to hand anybody, and which of the two
// happened is a person's to find out.
func preservedChangeHeld(ctx context.Context, worktrees PreservedChanges, state runstate.State) error {
	changed, err := worktrees.ChangedPaths(ctx, worktreeOf(state))
	if err != nil {
		return fmt.Errorf("what %s holds could not be read: %w", state.WorktreePath, err)
	}
	if len(changed) == 0 {
		return fmt.Errorf("%s holds no change at all against the base commit %s the run recorded", state.WorktreePath, state.BaseCommit)
	}
	return nil
}

// Continue carries out one repair-continue decision.
//
// The order is the order the guarantees need. Everything that can refuse is
// asked before anything is written, so a refused continuation leaves the item's
// grant exactly where it was and asking again once the refusal no longer applies
// carries out the same decision; and the item is put back before the run is made
// live again, because a run recorded as running that nothing is running is the
// one half-finished state no other reader here would notice.
func (c RepairContinuer) Continue(ctx context.Context, request RepairContinueRequest) (RepairContinueResult, error) {
	if err := c.validate(); err != nil {
		return RepairContinueResult{}, err
	}
	runID := strings.TrimSpace(request.Run)
	if !runstate.ValidRunID(runID) {
		return RepairContinueResult{}, fmt.Errorf("%q is not a run identifier; a triage decision names the run the docket entry is about", request.Run)
	}

	entry, err := docketedStoppage(c.Docket, runID, "repair")
	if err != nil {
		var missing NoDocketedStoppageError
		if errors.As(err, &missing) {
			if prior, readErr := c.Runs.Load(runID); readErr == nil {
				if alternative := oldForgeRepairAlternative(prior); alternative != nil {
					return RepairContinueResult{}, alternative
				}
			}
		}
		return RepairContinueResult{}, err
	}
	result := RepairContinueResult{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
	}
	// The run is adopted before anything is asked of it, and held until the
	// re-entry is written. A stopped run that still owes something is not in
	// flight and would be found by nothing else, so this is the same lease
	// reconciliation takes to act on one: what is read here is what is written,
	// and a sweep settling the same run beside this cannot lose either half.
	prior, lease, err := c.Runs.AdoptRun(ctx, entry.RunID)
	if err != nil {
		return result, fmt.Errorf("take the stopped run to re-enter its repair loop: %w", err)
	}
	defer lease.Release()

	found := readmodel.LookFor(ctx, c.Remains, prior)
	if err := stoppageIsOver(prior, found); err != nil {
		return result, err
	}
	// The run continued is the run the decision names, and it is continued on the
	// item it was made for. A record that put this run under some other item would
	// have the dispatch below continue one item's run as another's work, so it is
	// refused naming both rather than retargeted.
	if owner := strings.TrimSpace(prior.WorkItemID); owner != entry.WorkItemID {
		return result, fmt.Errorf(
			"run %s is recorded as made for %q while its docket entry names %s, so a repair of it would continue one item's run as another's work; nothing was spent, and which item this stoppage belongs to is a person's to settle",
			prior.RunID, owner, entry.WorkItemID)
	}
	if err := continuableRepair(prior, found); err != nil {
		return result, err
	}
	recovery := checkoutRecovery{Runs: c.Runs, Worktrees: c.Worktrees, Clock: c.Clock}
	result.RepairAttempts = prior.RepairAttempts
	result.SupersededBlocker = prior.Blocker
	// Whether this is a stall being carried on rather than a change being
	// repaired decides two things below, and both of them before anything is
	// written: whether the worktree has to hold a change already, and whether the
	// continuation counts an attempt.
	result.Checks = prior.StoppedAtStageBound()
	result.Stall = !result.Checks && continuableStall(prior)
	result.ResumesAt = continuedPhase(prior, result.Stall)
	// The architect's condition, asked before anything is written: the change a
	// continued developer is handed back is whatever is in that worktree.
	if found.WorktreeThere {
		if err := c.Worktrees.VerifyOwnedHead(ctx, worktreeOf(prior)); err != nil {
			return result, WorktreeSurgeryError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}
		}
		if prior.CheckoutRestorePending {
			if err := recovery.verifyRestoredCheckout(ctx, prior); err != nil {
				return result, err
			}
		}
		// And that the change is in it, which the gate above does not ask: a worktree
		// exactly as the harness left it and holding nothing passes that one. The
		// resumed run asks this again where it would invoke a developer, which is the
		// enforcement; asking it here is what keeps a handback that cannot work from
		// spending the item's grant to find out.
		//
		// A stall mid-attempt is the exception, and it is the same exception the
		// resumed run makes: nothing was handed back, so there is no change this
		// continuation is about, and an empty worktree is exactly what the attempt it
		// is owed starts from. Asking here for a change a stalled first attempt may
		// never have written would refuse the decision this action exists to carry
		// out. A stall at the checks or the review is not excepted: those steps judge
		// the change the attempt left, so it has to be there exactly as a repair's
		// does.
		if !result.Stall || resumesAnExistingChange(prior) {
			if err := preservedChangeHeld(ctx, c.Worktrees, prior); err != nil {
				return result, MissingPreservedChangeError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}
			}
		}
	}
	if err := noRunInFlight(c.Runs, entry.WorkItemID); err != nil {
		return result, err
	}
	// That the development manager granted this repair is read rather than taken
	// on trust, and so is how much of that grant is left to carry out and what it
	// was decided on. The reason the run records names that role, so the decision
	// and the words attributed to it both come from the record the role wrote.
	granted, err := c.granted(entry.WorkItemID, entry.RunID)
	if err != nil {
		return result, err
	}
	result.Granted = granted.attempts
	result.Decided = granted.decided
	result.Truncated = granted.truncated
	item, err := c.Items.Show(ctx, entry.WorkItemID)
	if err != nil {
		return result, fmt.Errorf("read the work item the stoppage is about: %w", err)
	}
	if err := continuableItem(item, entry.WorkItemID); err != nil {
		return result, err
	}
	// The hold is read before anything is written, so a held harness leaves the
	// grant uncarried rather than half re-entering a run it would not continue.
	hold, held, err := c.Intake.Held()
	if err != nil {
		return result, fmt.Errorf("read whether intake is held: %w", err)
	}
	if held {
		result.IntakeHeld = &hold
		return result, nil
	}
	// Capacity is read last of everything asked, because it is the condition most
	// likely to have changed while the rest were being asked and the one a
	// moment's wait settles. A full harness is reported rather than refused: the
	// grant is untouched and the decision stands.
	full, free, err := slotIsFree(c.Runs, c.Capacity)
	if err != nil {
		return result, err
	}
	if !free {
		result.CapacityFull = &full
		return result, nil
	}
	if !found.WorktreeThere {
		prior, err = recovery.restoreCheckout(ctx, prior)
		if err != nil {
			return result, err
		}
		result.WorktreeRestored = true
		result.ResumesAt = continuedPhase(prior, result.Stall)
		if err := preservedChangeHeld(ctx, c.Worktrees, prior); err != nil {
			return result, MissingPreservedChangeError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}
		}
	} else if prior.CheckoutRestorePending {
		prior, err = recovery.recordRestoredCheckout(ctx, prior)
		if err != nil {
			return result, err
		}
		result.WorktreeRestored = true
	}

	result.Reason = continueReason(entry, granted, result.Stall, result.Checks, result.ResumesAt)
	if result.WorktreeRestored {
		result.Reason += fmt.Sprintf("\nThe missing checkout was restored at %s from the harness's recorded commit %s, in the same run and developer session; previous check approval was cleared before restoration.", prior.WorktreePath, prior.HarnessCommit)
	}

	// The item is put back first, because a run made live behind an item that
	// still says it is blocked is a run nothing can resume and nothing will
	// notice. Recording why comes before the claim, so the item never reads as
	// work somebody quietly restarted.
	if err := c.supersedeOnItem(ctx, entry.WorkItemID, itemRecord(result.Reason, item, prior)); err != nil {
		return result, err
	}
	continued, err := c.supersedeOnRun(prior, granted, result.Reason, result.Stall)
	if err != nil {
		return result, fmt.Errorf("record the re-entry on run %s, whose item has already been put back and told why: %w", prior.RunID, err)
	}
	result.RepairBudget = continued.RepairBudget(c.ConfiguredAttempts)
	result.RepairAttempts = continued.RepairAttempts
	result.Continued = true
	// The finding a refused carry-out left is taken back here, once the re-entry is
	// recorded and before the run goes: a finding that stood for the length of the
	// run would have the docket say the decision is not happening while it runs.
	if problem := clearCarryOutFinding(ctx, c.Decisions, entry.WorkItemID, prior.RunID, c.now()); problem != "" {
		result.RecordProblem = problem
	}
	// The lease is given up before the run is continued, because continuing it is
	// the pipeline adopting the same run: holding it here would refuse the very
	// process this action exists to start.
	lease.Release()

	// The run is named rather than left to be discovered. What is dispatched is
	// this run's repair loop and nothing else, so a dispatch that found anything
	// else in flight for the item refuses instead of continuing it.
	outcome, runErr := c.Start(ctx, entry.WorkItemID, prior.RunID)
	result.Outcome = outcome
	return result, runErr
}

// repairGrant is the development manager's grant as this carry-out found it:
// the decision it was recorded as, how many of its rounds are left to carry out,
// how many it was worth in total, and whether the round cap cut it when it was
// recorded.
type repairGrant struct {
	decision  runstate.TriageDecision
	attempts  int
	decided   int
	truncated bool
}

// granted reports a repair grant of the development manager's that this
// re-entry may carry out, and refuses where there is none. It is two questions
// and the second is what makes the first mean anything.
//
// The item's repair-grant counter is the decision's own footprint: the
// development manager spends it as the decision is recorded and before anything
// acts on it, so an item carrying none is an item nobody decided this about, and
// an item whose grant the round cap refused carries none either. Its size is
// what the project configured a grant to be worth, truncated to the rounds the
// cap had room for — which is why the size is read from the record rather than
// from the configuration a second time: a carry-out working from its own reading
// would hand a run more attempts than the cap ever let the item have.
//
// But the counter is a total and is never cleared, so on its own it cannot tell
// a grant waiting to be carried out from one carried out last month. So what has
// already been carried out is read back against it, from the continuations the
// item's runs record, and a grant with nothing left is refused: a further
// stoppage needs a further decision, which past the cap is an escalation rather
// than a larger budget.
//
// The decision standing about this stoppage is the first thing asked, as a
// re-run asks it, and it is what the run's attribution is built from. A spent
// counter says somebody granted this item a repair; the decision says it was
// this stoppage, this role, this conversation and these words. A stoppage with
// no decision recorded about it is refused naming the missing record — which is
// also what a repair recorded before decisions were durable meets, and the one
// the development manager has to record again. One decision stands per stopped
// run, and a re-run, a wait, or an escalation recorded in place of the repair
// released the rounds the repair reserved — so a repair carried out on that run
// afterwards would spend attempts the cap no longer holds room for, on a
// decision nobody holds any more.
//
// The record read is the one of the item the run was made for, and the decision
// found there names the run itself. So a repair recorded against some other
// item's run is never what this finds: it is refused here as missing from this
// item's record rather than carried out against a run it does not name.
func (c RepairContinuer) granted(workItemID, runID string) (repairGrant, error) {
	counters, err := c.Decisions.Counters(workItemID)
	if err != nil {
		return repairGrant{}, fmt.Errorf("read what triage has recorded about %s: %w", workItemID, err)
	}
	standing, found := counters.DecisionOf(runID)
	if !found {
		return repairGrant{}, permanentCarryOut(triage.CarryOutDecisionMissing, fmt.Errorf(
			"the development manager has recorded no triage decision about the stoppage of run %s on %s's triage record, so there is nothing here to carry out: a repair carries the decision the record holds rather than words given to this command, and the decision is recorded where it is made, in the development manager's own conversation, against the item the run was made for. Recording it there spends a further repair grant of %s, which the cap may refuse — `yoyo triage override` is what permits that",
			runID, workItemID, workItemID))
	}
	if standing.Decision != runstate.TriageDecisionRepair {
		return repairGrant{}, permanentCarryOut(triage.CarryOutDecisionSuperseded, fmt.Errorf(
			"the decision standing about the stoppage of run %s is %q rather than a repair, %s: a repair recorded earlier about it was superseded by that decision and the rounds it reserved were released with it, so carrying a repair out here would spend attempts the item's record no longer holds",
			runID, standing.Decision, standing.Cite()))
	}
	if counters.RepairGrants < 1 {
		return repairGrant{}, fmt.Errorf(
			"a repair of the stoppage of run %s is recorded as decided, %s, and %s's repair budget shows none spent, so its durable record disagrees with itself and nothing here is safe to carry out: the decision spends the budget as it is recorded, in one write",
			runID, standing.Cite(), workItemID)
	}
	carried, err := c.carriedOut(workItemID)
	if err != nil {
		return repairGrant{}, err
	}
	remaining := counters.GrantedRounds - carried
	if remaining < 1 {
		return repairGrant{}, fmt.Errorf(
			"triage has granted %s %d repair attempt(s) and the harness has carried out %d, so there is nothing of that grant left to re-enter on: a further stoppage of an item that has already been handed back needs a further decision, which past the cap is an escalation rather than a larger budget",
			workItemID, counters.GrantedRounds, carried)
	}
	return repairGrant{decision: standing, attempts: remaining, decided: counters.GrantedRounds, truncated: counters.TruncatedGrants > 0}, nil
}

// carriedOut is how much of one item's repair grant the harness has already
// handed to a run. It is counted from the continuations recorded on the item's
// runs rather than from a counter of its own, because those are what the grant
// actually bought: a continuation is written before the developer is invoked, so
// an attempt nobody made still counts, which is the direction that keeps one
// decision from starting two continuations.
//
// A continuation whose round the environment refused is the one exception, and
// it is not an exception to that rule so much as the rule read properly: the
// attempt was made and bought nothing, because the round was handed an empty
// worktree rather than the change it was granted to repair. Counting it spends a
// grant on the harness's own failure and refuses the next handback of an item
// that has had nothing — which is how three items advanced toward escalation in
// one night. The run's settle marks those, and this is what reads the mark.
func (c RepairContinuer) carriedOut(workItemID string) (int, error) {
	recorded, err := c.Runs.Recorded()
	if err != nil {
		return 0, fmt.Errorf("read the runs %s has had, to count what its repair grant has already bought: %w", workItemID, err)
	}
	carried := 0
	for _, state := range recorded {
		if state.WorkItemID == workItemID {
			carried += state.CarriedOutRepairAttempts()
		}
	}
	return carried, nil
}

// continuableRepair reports a stopped run whose repair loop there is something to
// re-enter. Every condition is one the pipeline would otherwise meet past the
// grant: it resumes from durable state alone, so a run missing any of what that
// state has to supply would be refused after the item's budget had been spent on
// it.
//
// An approved change the environment stopped is asked about first, ahead of
// everything a repair would otherwise need, because the answer is the same
// whatever else the record holds: the reviewer approved it, and what it needs
// is its integration resumed rather than a developer handed anything. It is
// asked ahead of the repair input in particular because an approving verdict can
// carry minor findings, which read here as a failure returned to the developer
// — and a repair loop re-entered on those would spend a grant to have an
// approved change repaired. The refusal says which verb the run needs and why,
// in the docket's own sentence, because on yoyodyne-ifd.309 the refusal it
// replaced said only that there was nothing to repair — true, and what sent the
// development manager to a re-run of an undisputed change.
//
// The recorded repair input is the last of them and the one that makes this the
// action it is. A replay that conflicted is one of them (yoyodyne-ifd.132): the
// run records the conflict before it stops, so a continuation hands the same
// developer the same disagreement to reconcile on top of the target. A run that
// stopped with no failure returned to it — a provider that kept refusing — has
// no repair loop to continue: what it needs is a re-run or a person, and handing it another repair budget
// would buy attempts at a failure nobody ever showed the developer.
//
// A stall or a check-stage timeout is an exception to the input rather than
// to the reasoning. The harness is what stopped that run, before anything was
// returned to its developer, so what it is owed is the attempt it was making —
// which is a continuation of the same session rather than another answer to a
// complaint, and is charged accordingly. A timed-out check stage continues its
// checks on the change the developer already finished.
//
// found is what the repository holds of the run's change, asked for every
// continuation. An integration stop names the resume while the branch is there
// and, once it is gone, says so and names the re-run — the same answer the
// docket gives on the same stoppage, by the same rule.
func continuableRepair(prior runstate.State, found triage.Found) error {
	if alternative := oldForgeRepairAlternative(prior); alternative != nil {
		return alternative
	}
	if prior.IntegrationStop != nil {
		if !triage.IntegrationResumable(&found, false) {
			err := errors.New(triage.IntegrationGoneSays(prior.RunID, found.Describe()) +
				", and a repair has no approved change to hand back either")
			if found.Unknown {
				return err
			}
			return permanentCarryOut(triage.CarryOutBranchGone, err)
		}
		return errors.New(prior.IntegrationStop.ResumeSays(prior.RunID))
	}
	if prior.WorktreePath == "" || prior.Branch == "" || prior.BaseCommit == "" || prior.TargetBranch == "" {
		return permanentCarryOut(triage.CarryOutWorktreeGone, fmt.Errorf("run %s recorded no preserved worktree to continue in, so there is no change to repair; a fresh run of the item is what it needs", prior.RunID))
	}
	if found.Unknown || !found.BranchThere || (!found.WorktreeThere && !found.Looked()) {
		err := fmt.Errorf("there is no verified branch and checkout of run %s to continue: %s", prior.RunID, found.Describe())
		if found.Unknown {
			return err
		}
		if !found.BranchThere {
			return permanentCarryOut(triage.CarryOutBranchGone, err)
		}
		return permanentCarryOut(triage.CarryOutWorktreeGone, err)
	}
	if !found.WorktreeThere {
		if prior.ArtifactsRetiredBy != "" {
			return permanentCarryOut(triage.CarryOutWorktreeGone, fmt.Errorf("run %s's artifacts were retired by run %s; the development manager must decide recovery of the superseded work before its checkout can be restored", prior.RunID, prior.ArtifactsRetiredBy))
		}
		if prior.HarnessCommit == "" || prior.PreservedWorkRef != "" || prior.Phase == runstate.PhaseDeveloping {
			return permanentCarryOut(triage.CarryOutWorktreeGone, fmt.Errorf("run %s's missing checkout cannot be recovered from its branch: the run has no recorded completed commit, captured work remains at %q, or an interrupted developer may have left uncommitted work; missing uncommitted work is not recovered by checking out a branch, so the development manager must decide what follows", prior.RunID, prior.PreservedWorkRef))
		}
	}
	if prior.ProviderSessionID == "" {
		return fmt.Errorf("run %s recorded no developer session, so a continuation could not be the same developer carrying on with the change it made", prior.RunID)
	}
	// A stall or a check-stage timeout owes the run the step the harness stopped,
	// rather than a repair of a change nobody complained about.
	if !handedBackRepair(prior) && !continuableStall(prior) && !prior.StoppedAtStageBound() {
		return fmt.Errorf("run %s recorded no reviewer findings, failing check, refused paths, or replay conflict, and is not a provider the harness stopped with its session preserved, so no failure was ever returned to its developer and there is no attempt to carry on with: %s stopped for something a repair budget does not answer",
			prior.RunID, prior.RunID)
	}
	return nil
}

// Older handbacks kept the forge reading on the publication alone, sometimes
// without enough evidence to attribute it to this change. They cannot invent
// repair input, but must name the recovery the existing record supports.
func oldForgeRepairAlternative(prior runstate.State) error {
	if prior.CheckFailure == nil && prior.MergeDrop != nil && prior.PullRequest != nil && prior.PullRequest.Checks != nil && prior.PullRequest.Checks.Red() {
		return fmt.Errorf("run %s's merge was withdrawn with failing forge checks, but this older record kept no failing check as repair input; the supported alternative is a re-run decided by the development manager and carried out with `yoyo triage rerun %s`, which starts fresh from the target branch",
			prior.RunID, prior.RunID)
	}
	return nil
}

// continuableItem reports a work item a stopped run may be continued on. It is
// deliberately narrower than the condition a fresh run is held to: an item
// blocked by its own run stopping is exactly what this action expects to find,
// and requiring it to have been put back first is the remembered reopen this
// action exists to remove. What it refuses is an item nothing may be run on at
// all — one somebody closed, or one waiting on other work.
func continuableItem(item beads.WorkItem, workItemID string) error {
	if item.ID != workItemID {
		return fmt.Errorf("Beads returned work item %q for requested id %q", item.ID, workItemID)
	}
	switch item.Status {
	case "open", "in_progress", "blocked":
	default:
		return fmt.Errorf("%w: work item %s status is %q, so it is not one a stopped run may be continued on; nothing was spent, so the same decision is carried out by asking again once it is",
			ErrItemNotStartable, item.ID, item.Status)
	}
	blockers := blockingDependencies(item)
	if len(blockers) > 0 {
		return fmt.Errorf("%w: work item %s is blocked by: %s; nothing was spent, so the same decision is carried out by asking again once they are closed",
			ErrItemNotStartable, item.ID, strings.Join(blockers, ", "))
	}
	return nil
}

// itemRecord is what the item is told about this re-entry: the decision, and,
// where the item still read in_progress, that the claim the stopped run left on
// it is the one being superseded and why that is safe.
//
// That is the in-progress twin of the stale blocked status the claim clears
// with a note of its own. By the time this is written the run has been proved
// terminal and nothing of the item is in flight, so the claim has nothing
// working behind it; the continuation takes it over, and the item's notes say
// what moved it rather than leaving a claim that silently changed hands.
func itemRecord(reason string, item beads.WorkItem, prior runstate.State) string {
	if item.Status != claimedItemStatus {
		return reason
	}
	return reason + fmt.Sprintf(
		"\nThe item still read in_progress from run %s, which ended as %s with no run of this item in flight, so that claim was left over from the stopped run rather than held by anything working on the item; the continuation of the same run supersedes it.",
		prior.RunID, prior.Status)
}

// supersedeOnItem records the decision on the work item and puts it back to work
// the harness may continue. Both halves are the supersession: the note is what
// the next reader of the item finds instead of deciding the stoppage a second
// time, and the claim is what stops the item saying it is waiting on a person
// while a developer is working on it.
func (c RepairContinuer) supersedeOnItem(ctx context.Context, workItemID, reason string) error {
	if _, err := c.Items.RecordOutcome(ctx, workItemID, reason); err != nil {
		return fmt.Errorf("record the repair decision on %s: %w", workItemID, err)
	}
	// The blocker this claim clears is the one the stopped run wrote, so the
	// clear's account is read off the error where the read-back never confirmed
	// it; a confirmed one is the item back at work, which the check below and the
	// continuation record are the account of.
	item, _, err := c.Items.Claim(ctx, workItemID)
	if err != nil {
		return fmt.Errorf("put %s back to work for the repair it was granted: %w", workItemID, err)
	}
	// What bd reports back is checked rather than assumed, for the reason every
	// other write here is: an item that still says it is blocked is one the
	// pipeline refuses to resume, and finding that out from the refusal would
	// cost the grant that has already been spent.
	if err := validateClaimedItem(item, workItemID); err != nil {
		return fmt.Errorf("validate the work item put back for its repair: %w", err)
	}
	return nil
}

// supersedeOnRun makes the stopped run live again under its grant, and reports
// the record it wrote.
//
// The attempt this re-entry is about is counted here rather than left to the
// resumed run, and that is what makes the grant mean what it says. The pipeline
// re-runs an attempt that was in flight when a process stopped rather than
// counting it a second time, which is right for an interruption and wrong here:
// this attempt was never made, so a continuation that did not count it would
// hand the item one more developer invocation than the operator configured a
// grant to be worth. It is recorded before the developer is invoked, exactly as
// the repair loop's own attempts are.
//
// A stall or a check-stage timeout counts no attempt, and for the reason
// the paragraph above counts every other one: what a repair attempt buys is
// another answer to a failure somebody returned, and neither returned one. The
// run is owed the attempt the harness stopped it in, so charging one here would
// take an attempt off a budget that has bought nothing — and would hand the
// developer a prompt saying which attempt of how many this is for an attempt it
// has not made yet. What is still spent is the item's grant: the continuation
// records what it was worth, so the decision that authorized it is carried out
// once and a second is a second decision.
func (c RepairContinuer) supersedeOnRun(prior runstate.State, granted repairGrant, reason string, stalled bool) (runstate.State, error) {
	continued := prior
	// Also correct stale flags when a process restored and verified the checkout
	// but died before its final save. Captured work still keeps its ref and sweep
	// record; restoration from that ref is deliberately outside this action.
	if prior.PreservedWorkRef == "" && prior.ArtifactsRetiredBy == "" {
		continued.WorktreeRemoved = false
		continued.WorktreeSweptAt = nil
	}
	if prior.ArtifactsRetiredBy == "" {
		continued.BranchRemoved = false
		continued.BranchSweptAt = nil
	}
	continued.RepairContinuations = append(append([]runstate.RepairContinuation{}, prior.RepairContinuations...),
		runstate.RepairContinuation{
			GrantedAttempts:   granted.attempts,
			Reason:            reason,
			ContinuedAt:       c.now(),
			SupersededBlocker: prior.Blocker,
			Stall:             stalled,
			CheckStage:        prior.StoppedAtStageBound(),
		})
	if !stalled && !prior.StoppedAtStageBound() {
		continued.RepairAttempts = prior.RepairAttempts + 1
	}
	// The blocker is cleared onto the continuation that supersedes it. A terminal
	// run whose blocker still stands is what the docket re-dockets and what
	// `yoyo status` reports as stopped work, and this run has not stopped.
	continued.Blocker = ""
	// The failure the run ended on describes the stoppage this supersedes, and a
	// run that is going again has not failed.
	continued.Failure = ""
	// Which gate stopped it goes with the failure, for the same reason.
	continued.StopClass = ""
	// The environmental refusal on the record belongs to the round this
	// supersedes, and that round has already settled and been paid back. Leaving
	// it would make the next round inherit a classification it has not earned, and
	// the settle would find the class already decided and give back nothing.
	continued.Environmental = nil
	if prior.CheckFailure != nil && prior.CheckFailure.ForgeHeadCommit != "" {
		// The old head's forge reading remains in the repair input and the
		// superseded blocker above. It must not describe the publication this
		// attempt will make, or leave that publication looking dropped again.
		continued.PublishFailure = ""
		continued.MergeDrop = nil
		if prior.PullRequest != nil {
			published := *prior.PullRequest
			published.Checks = nil
			continued.PullRequest = &published
		}
	}
	continued.Status = runstate.StatusRunning
	continued.Phase = continuedPhase(prior, stalled)
	continued.CompletedAt = nil
	continued.SettledQuietSince = nil
	continued.UpdatedAt = c.now()
	if err := c.Runs.Save(continued); err != nil {
		return runstate.State{}, err
	}
	return continued, nil
}

// continuedPhase is the step a continuation puts the run back at. A repair is
// a developer attempt unless the harness stopped a step already underway. A
// check-stage timeout or a stall at checks or review has the attempt behind it, and
// putting the run back at developing would hand the developer a second attempt
// nobody asked for — a prompt with nothing returned in it, against a change it
// already finished — before the step it actually stalled in was asked again.
// Left at its own phase, the resumed pipeline goes straight to that step on the
// change the worktree holds, exactly as it does for a run a process died in
// there.
func continuedPhase(prior runstate.State, stalled bool) runstate.Phase {
	if prior.StoppedAtStageBound() || (stalled && stallResumesPastTheAttempt(prior)) {
		return prior.Phase
	}
	return runstate.PhaseDeveloping
}

// continueReason is what the run and the item record as why this run is going
// again: the decision the harness read, where that decision is recorded, the
// grant it carries, and the reasoning the decision was recorded with.
//
// All of it is read from the durable record, exactly as a re-run's reason is,
// which is what makes the whole sentence evidence rather than a claim. It cites
// the record it came from — whose decision, which conversation, which turn — so
// a reader who doubts the attribution can go and find the turn it was written
// on, and nothing in it can be supplied by whoever asked for the carry-out.
//
// A stall says what it is rather than borrowing the repair's sentence. What the
// item's notes carry is what the next reader of this run finds instead of
// deciding the stoppage again, and "re-entered on the change it already has"
// would describe a change nobody complained about and an attempt that was
// never judged. A stall at the checks or the review says which step it was put
// back at, because no developer is invoked there and a reader told the session
// was carried on would look for an attempt that never happens.
func continueReason(entry triage.Entry, granted repairGrant, stalled, checks bool, resumesAt runstate.Phase) string {
	grant := fmt.Sprintf("%d further repair attempt(s)", granted.attempts)
	if granted.truncated {
		grant = fmt.Sprintf("%d further repair attempt(s), from a grant the review-round cap had already cut to %d",
			granted.attempts, granted.decided)
	}
	decided := granted.decision.Cite()
	reason := fmt.Sprintf(
		"Triaged: the development manager's triage decided a repair of the stopped work of run %s, %s, and the harness re-entered that run's repair loop on the change it already has, under a grant of %s recorded against %s's durable triage budget. The durable blocker that run stopped on is superseded by this re-entry. The reasoning that decision was recorded with: ",
		entry.RunID, decided, grant, entry.WorkItemID)
	if stalled {
		reason = fmt.Sprintf(
			"Triaged: the development manager's triage decided a repair of the stopped work of run %s, %s, and the run was continued in the developer session it stalled in, at the attempt the harness stopped it in, under a grant of %s recorded against %s's durable triage budget. Nothing had judged the work, so the continuation counts no review round and no repair attempt. The durable blocker that run stopped on is superseded by this re-entry. The reasoning that decision was recorded with: ",
			entry.RunID, decided, grant, entry.WorkItemID)
		if resumesAt != runstate.PhaseDeveloping && resumesAt != "" {
			reason = fmt.Sprintf(
				"Triaged: the development manager's triage decided a repair of the stopped work of run %s, %s, and the run was continued at the %s phase, the step it stalled in, on the change its completed developer attempt left in the preserved worktree and branch, with no developer attempt, under a grant of %s recorded against %s's durable triage budget. Nothing had judged the work, so the continuation itself counts no review round and no repair attempt; what the step it asks again decides is charged as that step always is. The durable blocker that run stopped on is superseded by this re-entry. The reasoning that decision was recorded with: ",
				entry.RunID, decided, resumesAt, grant, entry.WorkItemID)
		}
	}
	if checks {
		reason = fmt.Sprintf("Triaged: the development manager's triage decided a repair of the stopped work of run %s, %s, and the harness continued the same run at its checks after the check-stage bound stopped it, on the change its developer already finished, under a grant of %s recorded against %s's durable triage budget. No developer attempt or review round is counted by this continuation. The reasoning that decision was recorded with: ", entry.RunID, decided, grant, entry.WorkItemID)
	}

	// The reasoning is folded to what the run's record will hold rather than
	// refused: losing the end of a long argument is better than refusing to carry
	// out a decision because of its length. The room is never negative, and the
	// assembled sentence is folded to the same bound, for the reason a re-run's
	// is: the prefix carries a citation built from recorded identifiers, so
	// measuring room is not the same as enforcing the bound.
	room := runstate.MaxSelectionReasonBytes - len(reason)
	if room < 0 {
		room = 0
	}
	return singleLine(reason+singleLine(strings.TrimSpace(granted.decision.Reason), room), runstate.MaxSelectionReasonBytes)
}

func (c RepairContinuer) validate() error {
	var problems []error
	if c.Docket == nil {
		problems = append(problems, errors.New("a repair requires the triage docket the decision was made against"))
	}
	if c.Runs == nil {
		problems = append(problems, errors.New("a repair requires the durable run state"))
	}
	if c.Intake == nil {
		problems = append(problems, errors.New("a repair requires the intake hold, because continuing a run spends on a provider"))
	}
	if c.Decisions == nil {
		problems = append(problems, errors.New("a repair requires the item's triage budget, which is what says the development manager granted one and what bounds it"))
	}
	if c.Items == nil {
		problems = append(problems, errors.New("a repair requires the work item, because the run that stopped blocked it and a blocked item is not one a run may be resumed on"))
	}
	if c.Worktrees == nil {
		problems = append(problems, errors.New("a repair requires the worktree, because what a continued developer is handed back is whatever is in it"))
	}
	if c.Start == nil {
		problems = append(problems, errors.New("a repair requires a way to continue the run"))
	}
	if c.ConfiguredAttempts < 0 {
		problems = append(problems, fmt.Errorf("the configured repair budget is %d, which is not a budget; execution.repair_attempts_before_replan is what states it", c.ConfiguredAttempts))
	}
	if c.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which runs nothing; a repair reads the same limit the reservation enforces, so that a full harness costs the item nothing", c.Capacity))
	}
	return errors.Join(problems...)
}

func (c RepairContinuer) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}

// Render describes what the action did, for whoever asked for it.
func (result RepairContinueResult) Render() string {
	var rendered strings.Builder
	if result.IntakeHeld != nil {
		// Who is holding it comes off the record rather than out of this sentence:
		// the same switch is placed by the operator and by the harness's own
		// failure-storm brake, and they are different things to do something about.
		fmt.Fprintf(&rendered, "INTAKE HELD since %s: %s\n",
			result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339), result.IntakeHeld.Says())
		fmt.Fprintf(&rendered, "nothing was continued for %s, and it keeps its repair grant; `yoyo release` lifts the hold, and asking again carries out the same decision\n",
			result.WorkItemID)
		return rendered.String()
	}
	if result.CapacityFull != nil {
		fmt.Fprintf(&rendered, "WAITING FOR A DEVELOPER: nothing was continued for %s, %d active run(s), limit %d\n",
			result.WorkItemID, result.CapacityFull.Active, result.CapacityFull.Limit)
		fmt.Fprintf(&rendered, "%s keeps its repair grant and the decision still stands, so asking again once a slot frees carries out the same one\n", result.WorkItemID)
		return rendered.String()
	}
	if result.WorktreeRestored {
		fmt.Fprintln(&rendered, "restored the recorded checkout from its branch; the run and developer session are unchanged, and checks must pass again")
	}
	switch {
	case result.Checks:
		fmt.Fprintf(&rendered, "continued run %s at its checks after the check-stage bound stopped it, on the change it already has, with no developer attempt\n", result.RunID)
	case result.Stall && result.ResumesAt != "" && result.ResumesAt != runstate.PhaseDeveloping:
		fmt.Fprintf(&rendered, "continued run %s at the %s phase, the step it stalled in, on the change it already has, with no developer attempt\n", result.RunID, result.ResumesAt)
	case result.Stall:
		fmt.Fprintf(&rendered, "continued run %s in the developer session it stalled in, at the attempt the harness stopped it in\n", result.RunID)
	default:
		fmt.Fprintf(&rendered, "re-entered the repair loop of run %s on the change it already has\n", result.RunID)
	}
	fmt.Fprintf(&rendered, "carried out %d further repair attempt(s)", result.Granted)
	if result.Truncated {
		fmt.Fprintf(&rendered, ", from a grant the review-round cap had already cut to %d", result.Decided)
	}
	fmt.Fprintf(&rendered, "; %d of %d attempt(s) now spent\n", result.RepairAttempts, result.RepairBudget)
	if result.Stall || result.Checks {
		fmt.Fprintln(&rendered, "nothing had judged the work, so this continuation counts no review round and no repair attempt")
	}
	fmt.Fprintf(&rendered, "continued because %s\n", result.Reason)
	if result.SupersededBlocker != "" {
		fmt.Fprintf(&rendered, "superseded blocker: %s\n", singleLine(result.SupersededBlocker, 240))
	}
	if result.RecordProblem != "" {
		fmt.Fprintln(&rendered, result.RecordProblem)
	}
	return rendered.String()
}
