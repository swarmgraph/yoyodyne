package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReconcileWorktrees is the repository access reconciliation needs: reading
// what a run's artifacts actually look like now, finishing the removal an
// integrated run already earned, and — for a merge the forge performed after
// the run that asked for it had finished — reading what that merge left on the
// remote target, removing the branch it consumed, and bringing local state onto
// what it produced. It is deliberately narrower than the manager the pipeline
// uses, because reconciliation must never create a worktree and must never
// promote a change: the two writes here that move a ref only ever fast-forward
// a target branch onto a commit that already contains it, or delete a branch
// the target already carries.
type ReconcileWorktrees interface {
	Observe(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Observation, error)
	CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error)
	ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration, mergeCommit string) (string, error)
	DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error
	// The two writes convergence needs, and the only ones here that move a ref.
	// Both are fast-forward-or-nothing and both refuse on the evidence rather
	// than on a record: a target branch is only ever advanced onto a remote
	// commit that already contains it, and a run branch is only ever deleted
	// once the target is proven to carry its work.
	CatchUpTarget(ctx context.Context, targetBranch string) (gitworktree.Catchup, error)
	RemoveMergedBranch(ctx context.Context, branch, targetBranch string) (gitworktree.Removal, error)
	// The two removals that keep the worktree registrations from accumulating
	// without bound, which is what eventually stops a command spawning at all in
	// a machine's next worktree. Neither can lose anything: retiring a preserved
	// checkout records whatever uncommitted work it holds on a run-scoped ref
	// before removing the directory and never touches its branch, and pruning
	// only unregisters checkouts that are no longer on disk.
	RemovePreservedWorktree(ctx context.Context, worktree gitworktree.Worktree, uncommitted gitworktree.UncommittedWork) (gitworktree.WorktreeRemoval, error)
	PruneRegistrations(ctx context.Context) (gitworktree.Prune, error)
	// PushRemote names the remote run branches are published to, which is the
	// one fact a recovered publication record needs that the forge cannot
	// answer: the forge knows the request and the branch, and the record says
	// which remote carries that branch.
	PushRemote() string
	// VerifyRemoteTarget is the pre-merge check on the remote target that the
	// run's own merge made, made again by the sweep that arms the merge a run
	// recorded no request for. It reads and moves nothing.
	VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error
	// RemoveLandingCheckout removes the checkout a run's landing checks were
	// running in when the process running them died, and nothing where there is
	// none. It is nobody's change — a landing checkout is a detached copy of an
	// integrated commit — so it is the one removal here that preserves nothing.
	RemoveLandingCheckout(ctx context.Context, runID string) error
}

// ReconcilePullRequests is the forge access reconciliation needs: what the
// forge now says about a pull request whose merge it queued, the one merge
// request a sweep may make, and the closing of one whose work landed by another
// vehicle. It never repeats a merge the forge dropped — a drop means a
// requirement went unmet, and satisfying it is a person's work rather than
// something a sweep should force, which is why the re-arm is a triage decision
// and not a sweep. What it may ask for is the merge a promoted run's approving
// verdict authorized and the run never asked for, because its record held no
// request to ask with: that is the run's own merge made late, on the run's own
// evidence, and not a decision about a refusal.
//
// Closing is the opposite of forcing anything: it retires a request that will
// never merge, which is a fact the harness's own promotion record already
// settles. See supersession.go for what earns it.
type ReconcilePullRequests interface {
	State(ctx context.Context, head string) (publish.PullRequest, error)
	Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error)
	SupersededPublications
}

// ReconcileStore is the durable run state reconciliation reads and settles.
// AdoptRun is what keeps a reconciled run singular: a run a live process still
// holds is left to that process rather than decided about from outside.
type ReconcileStore interface {
	// Triage reads the decisions whose recovery still needs a stopped checkout.
	Triage() *runstate.TriageStore
	Outstanding() ([]runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
	// Recorded is every run the harness holds, whatever became of it. Settling a
	// run reads only the outstanding ones; converging local state reads all of
	// them, because the branches and targets a finished run left behind are
	// exactly what it has to sweep.
	Recorded() ([]runstate.State, error)
	// LeasePromotion admits this sweep to move one target branch, waiting its
	// turn behind whatever is promoting into it now. Catching a branch up reads
	// where it is and then moves it, which is the same race a promotion is, so
	// it queues in the same place rather than beside it.
	LeasePromotion(ctx context.Context, targetBranch string) (*runstate.Lease, error)
	// LoadWorkflowInstance reads the instance a run on the declarative path was
	// observed through. A settlement never steps one: it reads whether the
	// observation reached a terminal of its own, because a run this sweep is
	// making terminal with an instance still standing mid-graph is one that would
	// otherwise read as having agreed with the definition throughout.
	LoadWorkflowInstance(instanceID string) (runstate.WorkflowInstance, error)
	// StopRequested reads a stop somebody asked for. A run a live process holds
	// honours it at its next boundary; a run whose process is gone reaches no
	// boundary, so the sweep honours it in the process's place.
	StopRequested(runID string) (runstate.StopRequest, bool, error)
}

// ReconcileReleases is the record of claims the audit gave back, read. It is
// satisfied by runstate.ClaimStore.
type ReconcileReleases interface {
	List() ([]runstate.ReleasedClaim, error)
}

// Reconciler settles the runs an interrupted process left behind. It compares
// durable run state against what the repository and the work tracker actually
// show, and then either finishes the run's own remaining step or records a
// durable blocker naming what a person has to decide.
//
// It has no backend, and settling never invokes a provider. A lost process
// handle says nothing about what a developer did, so recovering from one is a
// question about recorded evidence and observable artifacts — never a reason to
// start a second developer for an item. A run the pipeline can still continue
// on its own is therefore left exactly as it is by the settle. The
// continuations the sweep makes itself are ContinueWaits and ContinueUpdates,
// and both keep that rule: what they continue is the run's own attempt or the
// run's own promotion, through the Continue the sweep verb wires, and never a
// second developer.
type Reconciler struct {
	Tracker   WorkTracker
	Worktrees ReconcileWorktrees
	Store     ReconcileStore
	// These readers compare a confirmed forge landing with running builds,
	// including keys added only to shipped templates. They are optional for
	// callers that have no running-service records to compare.
	Repository    string
	ConfigFiles   ConfigComparisonFiles
	ConfigReaders ConfigReaders
	// Publisher answers what became of a merge the forge queued. It is required
	// only to settle a run that has one, which is a run a publishing project
	// produced; a purely local project never records one.
	Publisher ReconcilePullRequests
	// Docket is where a run this sweep stops is put in front of the development
	// manager. It is optional: a sweep wired without one settles runs exactly as
	// it would have, and what it settled is still on the work item.
	Docket *Docketer
	// Releases is the claim audit's record of the claims it gave back, which the
	// convergence sweep reads to correct a release that did not say its run's
	// change was still on a branch. Optional: a sweep wired without it corrects
	// nothing and converges exactly as it did.
	Releases ReconcileReleases
	// Divergences is the product's record of the target branches the harness
	// would not catch up to the remote's. The convergence sweep lifts the record
	// on every target it finds converged. Optional: a sweep wired without it
	// converges exactly as it did and lifts nothing.
	Divergences ReconcileDivergences
	Clock       execution.Clock
	// Sleep is the wait between two attempts at a boundary that failed on
	// something a later attempt may survive. It is here for the reason the
	// pipeline's is: a test must be able to take the backoff without taking the
	// time, and a sweep given none waits on a timer.
	Sleep func(ctx context.Context, duration time.Duration) error
	// VanishedGrace is how long a run parked with no process behind it — its
	// provider stopped on time, or paused on anything nothing but a process
	// continues — may sit in flight before the sweep settles it rather than
	// reporting it resumable. Zero takes DefaultVanishedGrace.
	VanishedGrace time.Duration
	// Holds is the operator's pause over all harness activity, read before a run
	// parked on it is settled: while the pause stands, the park is the operator's
	// and is left exactly as it is. Optional: a sweep wired without it never
	// settles a run parked on the operator's pause.
	Holds OperatorHolds
	// Continue is how ContinueWaits continues a run that exited on its
	// in-process usage-limit bound, and how ContinueUpdates carries a queued
	// head through its update: the run's own pipeline re-entering the run named,
	// in the worktree and developer session it already has, which is
	// Pipeline.Continue. It is the provider invocation the sweep makes, and
	// it is optional for the reason the docket is: a reconciler wired without
	// one settles and reports exactly as it would have and continues nothing.
	// Only the sweep verb wires it, because whatever wires it hosts the
	// continued run for as long as the run takes — a conversation's settle must
	// not, and does not.
	Continue func(ctx context.Context, workItemID, runID string) (Outcome, error)
	// Checks reads the checks of a merge the forge still holds queued, and
	// withdraws one that is red before it is handed back or its head rewritten.
	// Optional: a reconciler wired without it reads a queued merge as queued and
	// nothing more.
	Checks ReconcileChecks
	// Filer files the item a queued merge's check red on the target itself is,
	// one per target branch and check, as a red landing files its own; the merge
	// then waits on that item rather than being handed to a person. Optional: a
	// reconciler wired without it hands such a merge back as it did before
	// yoyodyne-m5p.
	Filer WorkFiler
	// JobLogs reads the tail of a failing job's log, which the filed item
	// carries. Optional: without it the item says no log was read.
	JobLogs ReconcileJobLogs
	// TargetChecks reads how each check ended on the target branch's own head,
	// which is what confirms a check red on a level head is the target's before
	// it is filed as the target's. Optional: without it the failing job's log is
	// read for the change's files instead (redTargetOwner).
	TargetChecks ReconcileTargetChecks
	// Intake and Capacity are read before a queued head is put back at its
	// promotion, because that makes a finished run live again: a held intake and
	// a full harness each leave the merge queued for the next sweep. A capacity
	// of zero has no room.
	Intake   IntakeHolds
	Capacity int
	// HostsRuns says this sweep's last steps host the runs it makes live —
	// ContinueWaits and ContinueUpdates with a Continue wired — which only the
	// sweep verb does. A queued head is put back at its promotion only by a sweep
	// that will host it; any other pass leaves the merge queued for one that will.
	HostsRuns bool
	// Forge tallies what this sweep asked the forge about its publications and
	// how long the forge took, so a pass can say it. Optional: a sweep wired
	// without it asks exactly the same questions and tallies nothing.
	Forge *ForgeQuestions
}

// DefaultVanishedGrace is how long a run the harness stopped on time is left
// for something to continue it before the sweep settles it as a run whose
// process vanished.
//
// It is the claim audit's threshold, and for the same reason it is that long
// rather than shorter: the run's own record says it may be continued, so the
// grace is the room a `yoyo run` somebody typed is given to adopt it before the
// sweep decides nobody is going to. Acting early costs a continuation the
// development manager then decides instead; acting late is the failure this
// exists to end — two runs read as running for a day and a half on 2026-09-20,
// each holding a developer slot, with the sweep reporting both resumable on
// every pass and nothing resuming either.
const DefaultVanishedGrace = readmodel.DefaultDeadClaimThreshold

// ReconcileAction names what reconciliation did with one run.
type ReconcileAction string

const (
	// ActionHeld reports a run a live process owns, which was left untouched.
	ActionHeld ReconcileAction = "held"
	// ActionResumable reports a run whose own pipeline can continue it from
	// durable state, so it was left exactly as the interrupted process left it.
	ActionResumable ReconcileAction = "resumable"
	// ActionCompleted reports a run whose integrated work was carried to its
	// terminal state: the item settled by its landing — closed, or back in the
	// backlog — and the run's artifacts removed.
	ActionCompleted ReconcileAction = "completed"
	// ActionBlocked reports a run nothing could finish, whose work item now
	// carries a durable blocker.
	ActionBlocked ReconcileAction = "blocked"
	// ActionFailed reports a run recorded terminal without a blocker, because
	// it left nothing behind for anyone to act on.
	ActionFailed ReconcileAction = "failed"
	// ActionUnsettled reports a run reconciliation could not decide. It stays
	// outstanding, so the next sweep takes it up again.
	ActionUnsettled ReconcileAction = "unsettled"
	// ActionQueued reports a run whose merge the forge has accepted and not yet
	// performed. Nothing about it can be decided until the forge merges the
	// request or drops the queued merge, so the run is left outstanding and the
	// next sweep asks again.
	ActionQueued ReconcileAction = "queued"
	// ActionCancelled reports a run somebody asked to stop whose process was gone,
	// so nothing would ever reach the boundary that honours a stop: the sweep
	// ended it cancelled in that process's place, exactly as the process would
	// have.
	ActionCancelled ReconcileAction = "cancelled"
)

// Reconciliation is what happened to one run. Failure records that
// reconciliation itself could not finish, which leaves the run outstanding for
// the next attempt rather than silently settled.
type Reconciliation struct {
	RunID      string          `json:"run_id"`
	WorkItemID string          `json:"work_item_id"`
	Action     ReconcileAction `json:"action"`
	Status     runstate.Status `json:"status,omitempty"`
	// Outcome is what became of the run in the read model's fixed vocabulary,
	// carried beside the status rather than instead of it for the reason the run
	// history and the price breakdown carry theirs: the status is the durable
	// value and stays what it always was, and this is the reading of it. Without
	// it a sweep reported "failed" over a run it had just handed to a person with
	// its branch and worktree intact, which is the one word that says nothing
	// about whether the work survived.
	Outcome     runstate.RunOutcome   `json:"outcome,omitempty"`
	Phase       runstate.Phase        `json:"phase,omitempty"`
	Detail      string                `json:"detail,omitempty"`
	Integration *runstate.Integration `json:"integration,omitempty"`
	// Branch and WorktreePath are what the run made, beside the two flags saying
	// which of them the harness removed. They are named rather than only counted
	// because a sweep that says a branch was removed without saying which one has
	// told an operator something they cannot check, and because the three phrases
	// below cannot be read off two booleans: a record naming no artifact at all
	// and one whose artifacts were removed are opposite answers to "is my work
	// gone", and the flags alone are false for both.
	Branch          string `json:"branch,omitempty"`
	WorktreePath    string `json:"worktree_path,omitempty"`
	WorktreeRemoved bool   `json:"worktree_removed"`
	BranchRemoved   bool   `json:"branch_removed"`
	CleanupFailure  string `json:"cleanup_failure,omitempty"`
	Failure         string `json:"failure,omitempty"`
	// DocketProblem names a stoppage this sweep settled and could not put on the
	// triage docket. It is deliberately not a settlement failure: the run is
	// settled and its blocker is on the work item either way, and the blocker is
	// durable on the run itself, so the next docket build finds the same stoppage
	// from the same record. Reporting it as a failed settlement would describe a
	// settled run as outstanding, which no later sweep would correct.
	DocketProblem string `json:"docket_problem,omitempty"`
	// Catchup is the local target branch brought onto the merge commit the forge
	// made, present only on a run whose queued merge this sweep settled. It is
	// reported rather than made durable for the reason the run's own catch-up is:
	// it is idempotent and owned by no run, so a held one is a fact to read
	// rather than a debt to carry.
	Catchup *gitworktree.Catchup `json:"catchup,omitempty"`
	// The same active and prospective findings an immediate landing reports.
	ConfigMismatches         []runstate.ConfigMismatch         `json:"config_mismatches,omitempty"`
	TemplateConfigMismatches []runstate.ConfigTemplateMismatch `json:"template_config_mismatches,omitempty"`
}

// Artifacts is what this sweep says survives of the run's change, assembled
// from the record it settled. A surface asks it for the phrase rather than
// reading the four fields itself, so the recovery view says what remains in the
// same words `yoyo status` and the channel do.
func (r Reconciliation) Artifacts() runstate.Artifacts {
	return runstate.Artifacts{
		Branch:          r.Branch,
		BranchRemoved:   r.BranchRemoved,
		WorktreePath:    r.WorktreePath,
		WorktreeRemoved: r.WorktreeRemoved,
	}
}

// Settled reports a run this sweep left in a terminal state that is not
// success, which is where the outcome word and what remains of the change are
// worth saying. A run still owed a step has neither yet, and a run whose work
// landed removed its artifacts on purpose.
func (r Reconciliation) Settled() bool {
	return r.Status.Terminal() && r.Outcome != runstate.OutcomeSucceeded && r.Outcome != ""
}

// Reconcile settles every run that still owes a step. One run that cannot be
// settled is reported as such and never stops the sweep: an unreconcilable run
// must not hide the others from the operator reading the report.
func (r Reconciler) Reconcile(ctx context.Context) ([]Reconciliation, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	outstanding, err := r.Store.Outstanding()
	if err != nil {
		return nil, fmt.Errorf("discover outstanding runs: %w", err)
	}
	// The runs whose settlement asks the forge nothing — a run whose process
	// died, one a redeploy drain preserved, one stopped on time — are settled
	// before the ones that wait on the forge's answer about a merge, so a slow or
	// unanswering forge never holds the slots those runs keep. The results are
	// reported in the store's order either way.
	results := make([]Reconciliation, len(outstanding))
	var askingForge []int
	for index, recorded := range outstanding {
		if asksForge(recorded) {
			askingForge = append(askingForge, index)
			continue
		}
		results[index] = r.reconcileRun(ctx, recorded)
	}
	for _, index := range askingForge {
		results[index] = r.reconcileRun(ctx, outstanding[index])
	}
	return results, nil
}

// asksForge reports a run whose settlement may wait on the forge: a queued
// merge, or a landing through a pull request nobody confirmed. It is read off
// the listing only to order the settlements; each is still decided under its
// own lease from the record as it then stands.
func asksForge(state runstate.State) bool {
	return queuedMerge(state) || unconfirmedLanding(state)
}

// reconcileRun takes the run's lease and settles what it finds under it. The
// state is re-read by AdoptRun, so nothing is decided from the listing snapshot
// that another process may have moved on from in the meantime.
func (r Reconciler) reconcileRun(ctx context.Context, recorded runstate.State) Reconciliation {
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		result := reconciliationOf(recorded, ActionHeld)
		result.Detail = "a live process holds this run"
		return result
	case err != nil:
		result := reconciliationOf(recorded, ActionUnsettled)
		result.Failure = fmt.Errorf("adopt run %s: %w", recorded.RunID, err).Error()
		return result
	}
	defer lease.Release()

	result, err := r.settle(ctx, state)
	if err != nil {
		// A step that failed partway is never described as settled, however
		// much of it succeeded. What it did achieve is still reported, because
		// the next sweep and an operator both act on that.
		result.Failure = err.Error()
		result.Action = ActionUnsettled
	}
	return result
}

// settle decides one run from its durable state and what the repository shows.
func (r Reconciler) settle(ctx context.Context, state runstate.State) (Reconciliation, error) {
	// A run that is over and whose landing checks the record says are still
	// running is a run whose process died inside them: a live process would hold
	// the lease this settlement took. The landing is settled as unverified —
	// nothing judged the commit — and the checkout the checks ran in is
	// removed, because it is nobody's change and nothing else will ever remove
	// it. The run itself is left exactly as it recorded itself.
	if state.Status.Terminal() && state.LandingChecks != nil && !state.LandingChecks.Finished() {
		return r.settleInterruptedLandingChecks(ctx, state)
	}
	// Delivery after cleanup needs only the saved comparison, not the removed
	// worktree or the schemas of services that may have restarted since then.
	if state.Status.Terminal() && state.Phase == runstate.PhaseComplete && state.ConfigComparison != nil && state.ConfigComparison.Pending {
		comparison, err := r.nameConfigReaders(ctx, &state, "")
		result := reconciliationOf(state, ActionCompleted)
		result.ConfigMismatches = comparison.active
		result.TemplateConfigMismatches = comparison.templates
		result.Detail = "the saved configuration comparison was delivered to the work item"
		return result, err
	}
	// A run whose provider the harness stopped on time is owed the rest of the
	// attempt it was making, for the length of the grace and no longer. Nothing
	// in the harness continues such a run on its own — the scheduler chooses from
	// what the tracker calls ready and a claimed item is not — so a run nobody
	// typed `yoyo run` for stays "running" for good: filling a developer slot,
	// refusing every item beside it as a race, and reported resumable by every
	// sweep, whether as a stopped provider or, where the stop fell inside its
	// repair loop, as a repair that can continue. Past the grace it is settled as
	// what it is, a run whose process is gone, so the stoppage reaches the
	// development manager and her decision has something to be carried out
	// against. It is asked ahead of every other reading of the record because
	// each of those reads the same record and says "resumable" of it, and that
	// word is what let two of these stand for a day and a half. The lease this
	// sweep holds is what says the process is gone: a continuation somebody did
	// start holds it, and this is never reached.
	//
	// The same holds for every other park a run returns from and lets its process
	// exit — waiting on an unresolved directive, on a tracker that would not
	// answer, on a provider nobody could reach, or on the operator's pause once it
	// is lifted. Nothing in the harness continues any of those either, so each is
	// settled the same way once its record has sat still for the grace. On
	// 2026-09-26 a run parked on a dependency held developer slot 1 for twenty
	// hours, read as resumable by every sweep, because only a provider stop was
	// settled here (docs/diagnoses/yoyodyne-ifd-428-49-dead-run-held-its-slot.md).
	// A park on work its item depends on was settled here too until a watching
	// session's pull came to continue it once that work closes
	// (yoyodyne-ifd.428.51); it is a wait on that work now, and left to the pull.
	//
	// A stop somebody asked for comes first and does not wait for the grace: the
	// run was going to end at its next boundary, no process will reach one, and
	// the lease this sweep holds is the evidence that none is there to.
	if honoursStop(state) {
		request, requested, err := r.Store.StopRequested(state.RunID)
		if err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("read whether run %s was asked to stop: %w", state.RunID, err)
		}
		if requested {
			return r.settleStopRequest(ctx, state, request)
		}
	}
	if park, parked := r.parkNothingServes(state); parked && r.vanished(park) {
		return r.settleVanished(ctx, state, park)
	}
	// A run waiting out a provider that refused it is not an interrupted run at
	// all: it recorded a deadline and is owed the attempt it was refused.
	// Settling it here would throw away a claimed item and a preserved worktree
	// over a wait that has not finished yet. It is read ahead of the repair loop
	// because a limit refuses a repair attempt as readily as a first one, and
	// the deadline is the more specific fact about such a run: what it is owed
	// next is the wait being served, and only then the attempt.
	//
	// The wait has two halves, and the reading says which the run is in. Inside
	// the deadline it is the wait it is, whether a process is asleep on it or
	// exited on the in-process bound and left it recorded. Past the deadline with
	// no process holding it — which the lease this sweep holds is the evidence
	// of — it is a wait nothing is serving, and `yoyo reconcile` continues it
	// itself (ContinueWaits) rather than leaving it to hold a developer slot
	// until somebody types `yoyo run`.
	if pausedForUsageLimit(state) {
		result := reconciliationOf(state, ActionResumable)
		waited := runstate.DescribePause(state.PauseCause, state.UsageLimitKind)
		deadline := state.UsageLimitResetsAt.UTC().Format(time.RFC3339)
		switch {
		case exitedWait(state, r.clock().Now()):
			result.Detail = fmt.Sprintf("the run is paused for %s and its recorded deadline %s has passed with no process serving the wait; `yoyo reconcile` continues it in its own worktree and developer session, as `yoyo run %s` would",
				waited, deadline, state.WorkItemID)
		case exitsOnInProcessBound(state):
			result.Detail = fmt.Sprintf("the run is paused for %s and can continue once it asks again, by %s at the latest; a sweep after that deadline with no process serving the wait continues it",
				waited, deadline)
		default:
			// An outage wait, or a park the operator placed: the sweep continues
			// neither, so neither is promised a continuation it will not get.
			result.Detail = fmt.Sprintf("the run is paused for %s and can continue once it asks again, by %s at the latest",
				waited, deadline)
		}
		return result, nil
	}
	// A run its own pipeline can still continue is left alone. Ending it here
	// would discard a change that can still be finished, and finishing it here
	// would mean starting the developer reconciliation must never start.
	if resumableRepair(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the repair loop can continue from durable state at attempt %d", state.RepairAttempts)
		if state.ProviderStop != "" {
			// The stop fell inside the repair loop, so this reading is what a
			// vanished run inside its grace looks like, and it says so.
			result.Detail += fmt.Sprintf("; its provider was stopped because %s, and a sweep after %s of nothing continuing it settles it as a stopped run",
				describeProviderStop(state.ProviderStop), r.vanishedGrace())
		}
		return result, nil
	}
	// A run held up by an unresolved user directive is not an interrupted run
	// either. It recorded the directive it stopped short for and is owed the rest
	// of the gate once somebody settles it, so settling it here would cancel work
	// the operator only paused — which is the one thing pausing for a directive
	// must never turn into.
	if pausedForDirective(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is paused for unresolved directive %s and can continue once it is settled: %s; with no process holding it, a sweep after %s of its record not moving settles it as a stopped run",
			state.DirectivePause.DirectiveID, state.DirectivePause.Unresolved, r.vanishedGrace())
		return result, nil
	}
	// A run waiting on work its item depends on is not an interrupted run either.
	// It recorded what it waits for and is owed the rest of the gate once that
	// work is closed or unlinked, so settling it here would cancel work somebody
	// only made wait. Nor is it a park nothing continues: a watching session's
	// pull continues it once that work closes, however long the wait.
	if pausedForDependency(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is paused because %s waits on unfinished work: %s; a watching `yoyo work` session continues it at the first pull after that work closes",
			state.WorkItemID, state.DependencyPause.Summary())
		return result, nil
	}
	// A run parked because the tracker would not answer the read a gate boundary
	// makes is not an interrupted run either. It recorded what went unanswered and
	// is owed the rest of the gate once the store answers, so settling it here
	// would turn a busy store into a cancelled run — which is the whole failure
	// parking exists to replace.
	if pausedForTracker(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is parked because %s, and can continue once the tracker answers; with no process holding it, a sweep after %s of its record not moving settles it as a stopped run",
			state.TrackerPause.Summary(), r.vanishedGrace())
		return result, nil
	}
	// A run parked because the operator paused all harness activity is not an
	// interrupted run either, and it is the one where settling it would be
	// worst: the operator stopped it deliberately and expects to find it where
	// they left it, so ending it here would turn their pause into the cancelled
	// run that pausing exists to avoid.
	if pausedForOperatorHold(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is parked because the operator paused all harness activity at %s, and can continue once `yoyo resume` lifts it",
			state.OperatorHeldSince.UTC().Format(time.RFC3339))
		return result, nil
	}
	// A run whose provider the harness stopped on time is not an interrupted run
	// either, inside the grace read at the top: it recorded what stopped it and is
	// owed the rest of the attempt it was making, in the worktree and session that
	// attempt already established. The reading says how long that lasts, because
	// "resumable" on its own is the word that let two of these stand for a day
	// and a half.
	// A run this sweep put back at its promotion to bring a queued head up to
	// date is owed that continuation, which ContinueUpdates hosts. Read as
	// anything else — an integrating run with no promotion on its record — it
	// would be abandoned over the update that was meant to land it.
	if updatingQueuedHead(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = "the run was put back at its promotion to bring its queued head up to date onto its target, and the sweep's last step continues it: " +
			state.IntegrationResumptions[len(state.IntegrationResumptions)-1].Reason
		return result, nil
	}
	if stoppedProviderIsResumable(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run's provider was stopped because %s and it can continue from durable state; `yoyo run %s` continues it, and a sweep after %s of nothing continuing it settles it as a stopped run",
			describeProviderStop(state.ProviderStop), state.WorkItemID, r.vanishedGrace())
		return result, nil
	}
	// A run its hosting watch session stopped for a redeploy is not an
	// interrupted run either: it recorded the phase it was at and is owed the
	// rest of the gate from there, by the session that comes back or by `yoyo
	// run`. Settling it here would cancel work the session only put down.
	if stoppedForRedeployIsResumable(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("%s; the session that comes back re-adopts it, and `yoyo run %s` continues it otherwise",
			describeRedeployStop(*state.RedeployStop), state.WorkItemID)
		return result, nil
	}
	// A run whose merge the forge queued is not an interrupted run: it finished,
	// and what it still owes is the forge's answer about a merge that lands
	// minutes after the run was over. Asking for that answer is the whole of
	// reconciliation's part in it, and it is asked before the repository is
	// consulted at all: the local promotion such a run recorded is not a claim
	// about anything a later sweep can observe, and a merge nobody has answered
	// for yet must never be settled as a disagreement.
	if queuedMerge(state) {
		return r.settleQueuedMerge(ctx, state)
	}
	// A landing through the pull request whose merge nobody has confirmed is the
	// forge's to answer for, not the repository's: its local target was never
	// moved, so what the repository shows says nothing about whether it landed.
	if unconfirmedLanding(state) {
		return r.settleInterruptedLanding(ctx, state)
	}
	observation := gitworktree.Observation{}
	if state.WorktreePath != "" {
		var err error
		observation, err = r.Worktrees.Observe(ctx, worktreeOf(state))
		if err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("observe run %s artifacts: %w", state.RunID, err)
		}
	}
	// Recorded integration means the work is already promoted, whatever else
	// was interrupted afterwards. It is still only a claim a process wrote down,
	// and reconciliation exists for state a process that died wrote, so it is
	// reconciled against what the repository shows rather than trusted.
	if state.Integration != nil {
		if disagreement := contradictedIntegration(state, observation); disagreement != "" {
			return r.blockContradictedIntegration(ctx, state, observation, disagreement)
		}
		return r.completeIntegrated(ctx, state, false)
	}
	// An interruption inside the integration step is the one boundary durable
	// state cannot describe: the promotion either landed or it did not, and
	// only the repository knows which. Containment of the branch's commit in
	// the recorded target is that answer.
	if state.Phase == runstate.PhaseIntegrating && observation.BranchIntegrated {
		return r.recoverIntegration(ctx, state, observation)
	}
	return r.abandon(ctx, state, observation)
}

// settleInterruptedLandingChecks ends landing checks whose process died with
// its checks running: the record says the landing is unverified and why, and
// the checkout is removed. The action is the run's own outcome rather than a new one — the
// run was settled long before, and this settles only what it left running.
func (r Reconciler) settleInterruptedLandingChecks(ctx context.Context, state runstate.State) (Reconciliation, error) {
	state.LandingChecks.CloseInterrupted(r.clock().Now())
	if err := r.Worktrees.RemoveLandingCheckout(ctx, state.RunID); err != nil {
		state.LandingChecks.Problem += fmt.Sprintf("; the landing checkout could not be removed and is left for somebody to remove by hand: %v", err)
	}
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("save the unverified landing of %s: %w", state.RunID, err)
	}
	result := reconciliationOf(state, ActionCompleted)
	result.Detail = "settled the landing checks the run's process died inside as unverified: " + state.LandingChecks.Describe()
	return result, nil
}

// queuedMerge reports a run waiting on a merge the forge accepted and has not
// performed yet. It is the one thing a finished run can still owe, and it is
// only ever owed by a run that recorded the promotion the merge carries.
func queuedMerge(state runstate.State) bool {
	return state.Integration != nil && state.PullRequest != nil && state.PullRequest.MergeQueued
}

// contradictedIntegration reports what the repository says that a recorded
// integration does not, and nothing at all when the two agree or when the
// repository cannot answer. A record is only ever contradicted on positive
// evidence: the artifacts of a run that got far enough are legitimately gone,
// and their absence must never read as a promotion that never happened.
func contradictedIntegration(state runstate.State, observation gitworktree.Observation) string {
	integration := *state.Integration
	// Nothing was observed of the target, so there is nothing to reconcile the
	// record against.
	if state.WorktreePath == "" || state.TargetBranch == "" {
		return ""
	}
	// Removing either artifact required proving this exact commit had reached
	// this exact target, so a run that got that far is corroborated by a proof
	// the harness already made rather than by the target as it stands now.
	if state.WorktreeRemoved || state.BranchRemoved {
		return ""
	}
	// A merge the forge performed carried the same promotion onto the remote
	// target, which is corroboration from outside this record. A merge the forge
	// has not answered for never reaches here at all: settle asks the forge about
	// one before it observes anything.
	if state.PullRequest != nil && state.PullRequest.Merged {
		return ""
	}
	if !observation.TargetExists {
		return fmt.Sprintf("the run recorded commit %s as integrated into %s, but that branch does not exist",
			integration.SourceCommit, integration.TargetBranch)
	}
	// Integration only ever fast-forwards the target onto the promoted commit,
	// so a target still standing where the promotion left it carries it.
	if observation.TargetCommit == integration.TargetCommit {
		return ""
	}
	// Past that, only the branch that carried the commit answers containment,
	// and only while it still exists and still points at the recorded commit.
	// The base commit is in the target by construction and proves nothing.
	answered := observation.BranchExists &&
		observation.BranchCommit == integration.SourceCommit &&
		observation.BranchCommit != state.BaseCommit
	if !answered || observation.BranchIntegrated {
		return ""
	}
	return fmt.Sprintf("the run recorded commit %s as integrated into %s, but %s does not contain it",
		integration.SourceCommit, integration.TargetBranch, integration.TargetBranch)
}

// blockContradictedIntegration hands a promotion the repository does not carry
// to a person rather than completing the run on it, and clears the record that
// claimed it: a run that keeps a promotion nothing can prove owes a cleanup that
// can never prove itself, so every later sweep would decide it again. The commit
// and the target it named survive in the blocker on the item.
//
// It writes that record itself rather than through recordTerminalFailure,
// because clearing the promotion has to reach disk even for a run that was
// already terminal, and a run that is already terminal keeps the status it
// recorded for itself.
func (r Reconciler) blockContradictedIntegration(ctx context.Context, state runstate.State, observation gitworktree.Observation, reason string) (Reconciliation, error) {
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	// The blocker reaches the item before the record is disturbed, so an
	// interruption here leaves the run outstanding with the blocker already
	// recorded rather than a settled run nobody was told about.
	notes, err := r.recordBlocker(ctx, state, itemStatus, observation, reason)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	state.Integration = nil
	state.Blocker = runstate.RecordBlocker(notes)
	settled, saveErr := r.saveTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionBlocked)
	result.Detail = reason
	result.DocketProblem = r.docketStoppedRun(settled)
	return result, saveErr
}

// settleQueuedMerge asks the forge what became of a merge it queued, and
// settles the run on the answer. There are three answers, and the third is the
// one worth stating plainly:
//
//   - The forge merged. The publication finishes exactly as it would have
//     inside the run: the remote target is confirmed to carry the promotion, the
//     merge commit the forge made of it is recorded, the local target branch is
//     caught up onto the merge commit, and the item is settled on that
//     confirmation — closed where its landing discharged it, put back where
//     it did not. The catch-up is done here rather than left to the
//     convergence sweep so that settling a merge is complete on its own: a
//     caller that settles runs without sweeping afterwards must not be the
//     difference between a converged checkout and one silently left behind. The
//     branch the merge consumed is deleted after the closure and cannot affect
//     it, which is what stops a reset connection at the last step of the
//     hygiene from leaving a finished item open.
//   - The forge is still holding the merge. Its checks are read and written
//     onto the publication, and a red one is decided on (settleStillQueued):
//     updated onto the target where it fell behind and failed on files its
//     change does not touch, handed back as a dropped merge otherwise. A merge
//     whose checks are not red stays outstanding, and a later sweep asks again.
//   - The forge dropped it: the request is closed, or open with no merge queued
//     for it any more. Something the base branch requires went unmet, and the
//     harness does not merge past a requirement — not with administrator
//     privileges, not by asking again. The publication is recorded as
//     outstanding and the item is handed to a person with a durable blocker
//     rather than closed as integrated: the run that promoted the change left
//     that closure to the forge's answer, and this is the answer. The change
//     itself is not at risk — the local target branch it was integrated into is
//     the authoritative one and moved before any of this — so what the blocker
//     asks for is the publication, which is where triage's bounded re-arm of a
//     dropped merge acts. An item some earlier run already closed keeps the
//     closure it has; the outstanding publication is still recorded on it.
func (r Reconciler) settleQueuedMerge(ctx context.Context, state runstate.State) (Reconciliation, error) {
	published := *state.PullRequest
	if r.Publisher == nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf(
			"run %s is waiting on the queued merge of pull request %d, and reconciliation has no forge access to ask about it",
			state.RunID, published.Number)
	}
	observed, err := r.askState(ctx, published.Branch)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("ask the forge about the queued merge for run %s: %w", state.RunID, err)
	}
	if !observed.Merged && observed.AutoMerge {
		// Still queued is not the whole answer: a merge held for checks that will
		// never pass is not going to happen, so the checks are read beside it and
		// decided on (queuedchecks.go).
		return r.settleStillQueued(ctx, state)
	}
	published.State = observed.State
	published.Merged = observed.Merged
	state.PullRequest = &published

	if !observed.Merged {
		// A landing the forge stopped holding is a change on its kept branch and
		// on no target, and a head that fell behind is the lost race a replay
		// answers — so that is asked before the drop is handed to anybody.
		// Keep the queued disposition until the checks decide what the drop
		// means. A failed read records an unread state that the next sweep retries.
		if replayed, decided, err := r.replayDroppedLanding(ctx, &state, observed); decided {
			return replayed, err
		}
		published = *state.PullRequest
		published.MergeQueued = false
		state.PullRequest = &published
		state.PublishFailure = droppedMerge(published, strings.ToLower(nonEmpty(observed.State, "in an unreported state")), state.Integration.TargetBranch)
		// The moment the drop was found out, written down rather than left to be
		// worked out again by whoever next reads the record. It is the one thing a
		// channel can be told, and it is stamped here — before the settlement below
		// takes the run in either of its two directions — so both of them carry it.
		state.MergeDrop = &runstate.MergeDrop{At: r.clock().Now(), Reason: state.PublishFailure}
		return r.settleDroppedMerge(ctx, state)
	}
	detail := fmt.Sprintf("the forge merged pull request %d into %s", published.Number, state.Integration.TargetBranch)
	var catchup *gitworktree.Catchup
	var comparison configComparison
	if failure := r.confirmQueuedPublication(ctx, state, &published, observed.MergeCommit); failure != nil {
		state.PublishFailure = failure.Error()
		detail = failure.Error()
	} else {
		// The merge is confirmed on the remote, so the local branch is behind it
		// by the commit the forge made of this run's own promotion. A publication
		// that could not be confirmed deliberately does not reach here: that is
		// the state a person has to look at, and moving the local branch on a
		// merge nothing verified would be deciding it for them.
		settled := r.catchUp(ctx, state.Integration.TargetBranch)
		catchup = &settled
		var compareErr error
		comparison, compareErr = r.nameConfigReaders(ctx, &state, published.MergeCommit)
		if compareErr != nil {
			return reconciliationOf(state, ActionUnsettled), compareErr
		}
	}
	// Saving the comparison must retain the outstanding merge settlement.
	// Otherwise a failed finding delivery sends the next sweep straight to
	// ordinary cleanup, skipping the forge outcome and consumed branch removal.
	published.MergeQueued = false
	// The run that integrated the change left the closure to this answer, so this
	// note is where an operator learns how the publication of it ended. It is
	// written before the run is settled: a sweep that stopped in between leaves
	// the merge still queued durably and takes it up again, which repeats a note
	// rather than losing one.
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderQueuedMergeNotes(state, detail, catchup)); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the settled merge for run %s: %w", state.RunID, err)
	}
	// The run that promoted this change deliberately left the closure to the
	// forge's answer, so making it is this step's work. It is made here rather
	// than left to completeIntegrated so the reason says what actually happened:
	// a reviewed promotion the forge has now merged, not a run somebody
	// interrupted. completeIntegrated then finds the item closed and adds nothing,
	// which keeps the item's account of this merge to the one note above.
	settled, err := r.closeSettledMerge(ctx, state)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), err
	}
	// The settlement can decide the item goes somewhere other than where the run
	// claimed, and it answers with the run's landing fields as they now stand. They
	// are carried on rather than dropped, because completeIntegrated is what saves
	// this run's record and every surface reads the disposition off it.
	state = settled
	// The branch the merge consumed is removed last, after the item is already
	// settled, and that ordering is the whole of yoyodyne-ifd.301's first half. It
	// is hygiene rather than part of the publication: the merge is confirmed, the
	// work is on both branches, and the only thing a failed deletion leaves is a
	// dead branch on the remote. Deleting it first is what made a connection reset
	// at the last step keep an item open — twice on yoyodyne-ifd.295, which the
	// scheduler then pulled as ordinary ready work and three developer runs spent
	// re-deriving that the change had already landed.
	if state.PublishFailure == "" {
		if failure := r.deleteMergedBranch(ctx, &state, published); failure != "" {
			detail = failure
		}
	}
	result, err := r.completeIntegrated(ctx, state, false)
	result.Detail = detail
	result.Catchup = catchup
	result.ConfigMismatches = comparison.active
	result.TemplateConfigMismatches = comparison.templates
	// A publication entry this merge had open — a promotion docketed with no
	// request on its record, whose merge the recovering sweep then armed — is
	// closed by the settlement that finished it, for the reason the finishing
	// sweep closes its own: left open it would say a publication needs a person
	// while the record says nothing about it is outstanding, and a docket rebuilt
	// from the record would never re-derive it. A leftover the deletion wrote
	// keeps the entry open, exactly as it does on the finishing sweep.
	if err == nil && state.PublishFailure == "" && r.Docket != nil {
		if _, docketErr := r.Docket.SettlePublication(state, settledPublicationReason(state)); docketErr != nil {
			result.DocketProblem = docketErr.Error()
		}
	}
	return result, err
}

// closeSettledMerge settles the item of a run whose queued merge the forge has
// performed. An item already in the state its run calls for is left alone, so
// settling the same merge twice settles it once — and so does settling one an
// older run closed before the closure waited on the forge at all.
func (r Reconciler) closeSettledMerge(ctx context.Context, state runstate.State) (runstate.State, error) {
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return state, err
	}
	if itemSettled(state, itemStatus) {
		return state, nil
	}
	// The forge merging the change settles where the work is, and not whether the
	// work discharges the item. That is what the run's developer claimed and its
	// reviewer approved, and both are read from the durable record here for the
	// reason they are durable at all: the run that produced them ended before the
	// forge answered.
	if !state.Discharges() {
		settled, err := settleUndischarged(ctx, r.Tracker, state)
		if err != nil {
			return state, fmt.Errorf("reopen the work item run %s did not discharge: %w", state.RunID, err)
		}
		return settled, nil
	}
	if _, err := r.Tracker.Complete(ctx, state.WorkItemID, settledMergeCompletionReason(state)); err != nil {
		return state, fmt.Errorf("close integrated work item for run %s: %w", state.RunID, err)
	}
	return state, nil
}

// itemSettled reports an item already in the state its run calls for. It is what
// makes settling the same run twice record one settlement, and it has two
// answers rather than one because the runs do: a run that discharges its item
// settles on a closed item, and one that does not settles on an item back in the
// backlog.
//
// A closed item settles either. An item an earlier run closed keeps that closure
// whatever this run claimed or its reviewer approved, for the reason a dropped
// merge does not reopen one: rewriting a closure an operator has already read is
// a worse answer than leaving it and recording what happened beside it.
func itemSettled(state runstate.State, itemStatus string) bool {
	if itemStatus == "closed" {
		return true
	}
	return !state.Discharges() && itemStatus == "open"
}

// droppedMerge is what the harness records about a merge the forge gave up on,
// and it is two different facts depending on what has already been done about
// this publication.
//
// A first drop is work for a person, and where the cause turns out to have been
// transient it is also the one thing triage may re-arm: the identical
// already-authorized request, repeated once. A drop of a publication that has
// already been re-armed is not that. The request has been through the forge's
// requirements twice and been dropped twice, which is a repository somebody has
// to look at rather than a transient cause to repeat past — so what is recorded
// says so, and says it on the item, because the blocker this becomes is what the
// development manager reads before deciding anything.
func droppedMerge(published runstate.PullRequest, observedState, targetBranch string) string {
	dropped := fmt.Sprintf("the forge dropped the queued merge of pull request %d: it is %s and has no merge queued for it. A requirement of %s went unmet, and the harness does not merge past one, so the pull request needs a person",
		published.Number, observedState, targetBranch)
	if published.MergeRearms == 0 {
		return dropped
	}
	return fmt.Sprintf("%s. This is the %s drop of this publication: its merge request has already been repeated %d time(s) on triage's decision and the forge has dropped it again, so it is an escalation rather than something to re-arm again",
		dropped, ordinalDrop(published.MergeRearms+1), published.MergeRearms)
}

// ordinalDrop names which drop of one publication this is, in words, because a
// blocker is read by a person and "the 2nd drop" is not how one is written.
func ordinalDrop(count int) string {
	switch count {
	case 2:
		return "second"
	case 3:
		return "third"
	default:
		return fmt.Sprintf("%dth", count)
	}
}

// settleDroppedMerge settles a run whose queued merge the forge gave up on. The
// promotion stands and the item is not closed against it: nothing confirmed the
// merge, and closing an item as integrated on a publication that never happened
// is the early close this path exists to avoid. What it leaves instead is a
// durable blocker naming the dropped merge, which is what puts the item in front
// of triage — the one place a dropped merge may be re-armed, once and bounded.
//
// It writes the terminal record itself rather than through recordTerminalFailure
// for the reason blockContradictedIntegration does: the merge is no longer queued
// and that has to reach disk even for a run that finished successfully, or every
// later sweep asks the forge the same settled question again.
//
// An item an earlier run already closed — one promoted before the closure waited
// on the forge — keeps that closure. Reopening it would rewrite history the
// operator has already read; the outstanding publication on it is what they need.
func (r Reconciler) settleDroppedMerge(ctx context.Context, state runstate.State) (Reconciliation, error) {
	return r.settleDroppedMergeWith(ctx, state, "")
}

// settleDroppedMergeWith is settleDroppedMerge for a merge the harness withdrew
// over its forge checks, whose settlement note carries the forge's account of
// them (renderForgeAccount).
func (r Reconciler) settleDroppedMergeWith(ctx context.Context, state runstate.State, account string) (Reconciliation, error) {
	reason := state.PublishFailure
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	settlement := renderQueuedMergeNotes(state, reason, nil)
	if account != "" {
		settlement += "\n\n" + account
	}
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, settlement); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the settled merge for run %s: %w", state.RunID, err)
	}
	if itemStatus == "closed" && state.CheckFailure == nil {
		result, err := r.completeIntegrated(ctx, state, false)
		result.Detail = reason
		return result, err
	}
	if itemStatus == "closed" && state.CheckFailure.ForgeHeadCommit != "" {
		// A closed item stays closed. Keep the forge failure without reopening it
		// or treating its withdrawn merge as an integration that needs cleanup.
		invalidateForgeApproval(&state)
		settled, err := r.saveTerminalFailure(state, reason)
		result := reconciliationOf(settled, ActionBlocked)
		result.Detail = reason
		return result, err
	}
	// The blocker says what the repository shows of the run's branch and
	// worktree rather than what an empty observation implies. A queued landing
	// keeps both until the forge's merge is confirmed, and until 2026-09-27 this
	// note said they were gone over artifacts standing exactly where the run left
	// them — which read as the change having nothing left to replay.
	observation, err := r.observeArtifacts(ctx, state)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), err
	}
	// The blocker reaches the item before the record is disturbed, so an
	// interruption here leaves the merge still queued durably and takes the whole
	// settlement up again rather than settling a run nobody was told about.
	notes, err := r.recordBlocker(ctx, state, itemStatus, observation, reason)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	state.Blocker = runstate.RecordBlocker(notes)
	if state.CheckFailure != nil && state.CheckFailure.ForgeHeadCommit != "" {
		invalidateForgeApproval(&state)
	}
	settled, saveErr := r.saveTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionBlocked)
	result.Detail = reason
	result.DocketProblem = r.docketStoppedRun(settled)
	return result, saveErr
}

// unconfirmedLanding reports a run that recorded a landing through the pull
// request and holds no answer from the forge about it: not merged, and not
// queued. Only a run still in flight reaches the sweep that way — one that
// ended on such a record stopped and handed its item to a person, and owes
// nothing (runstate.State.Outstanding) — so this is a process that died between
// preparing the landing and hearing what the forge did with it.
func unconfirmedLanding(state runstate.State) bool {
	if state.Integration == nil || !state.Integration.ThroughPullRequest {
		return false
	}
	return state.PullRequest == nil || (!state.PullRequest.Merged && !state.PullRequest.MergeQueued)
}

// settleInterruptedLanding settles a run killed while it was landing its change
// through the pull request, on the forge's answer rather than the repository's.
// A local promotion interrupted there is settled as succeeded because its local
// target already carries the change; a landing moved no local branch, so doing
// the same would close the item as integrated over a change that may be on an
// open pull request and no target branch at all.
//
//   - The forge merged it. The merge is confirmed on the remote, the local target
//     is caught up onto it under the promotion lease, the consumed branch is
//     removed, and the run is completed exactly as an interrupted local promotion
//     is — closing the item, and cleaning up on the containment the catch-up
//     just gave the local target.
//   - The forge holds the merge queued. The run is recorded as the queued landing
//     it is, finished and waiting, and the sweep that settles queued merges takes
//     it from there.
//   - Anything else — a request still open with nothing queued, or a closed one
//     — is a change that landed nowhere. The item is handed to a person with that
//     as the blocker, as the run itself would have done, and the promotion it was
//     preparing stays on the record for a re-arm to repeat.
//
// A forge that cannot be asked settles nothing, and the next sweep asks again.
func (r Reconciler) settleInterruptedLanding(ctx context.Context, state runstate.State) (Reconciliation, error) {
	target := state.Integration.TargetBranch
	if r.Publisher == nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf(
			"run %s was landing its change on %s through its pull request, and reconciliation has no forge access to ask what became of it",
			state.RunID, target)
	}
	observed, err := r.askState(ctx, state.Branch)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("ask the forge about the interrupted landing of run %s: %w", state.RunID, err)
	}
	published := runstate.PullRequest{Branch: state.Branch, Number: observed.Number, URL: observed.URL, HeadCommit: state.Integration.SourceCommit}
	if state.PullRequest != nil {
		published = *state.PullRequest
	}
	published.State = observed.State
	published.Merged = observed.Merged
	state.PullRequest = &published

	switch {
	case observed.Merged:
		var comparison configComparison
		if failure := r.confirmQueuedPublication(ctx, state, &published, observed.MergeCommit); failure != nil {
			// The forge says merged and nothing could check what the merge left; the
			// change is closed on the forge's word, as a settled queued merge is, and
			// the unconfirmed publication stays outstanding for a person and the
			// sweeps that finish publications.
			state.PublishFailure = failure.Error()
		} else {
			r.catchUp(ctx, target)
			var compareErr error
			comparison, compareErr = r.nameConfigReaders(ctx, &state, published.MergeCommit)
			if compareErr != nil {
				return reconciliationOf(state, ActionUnsettled), compareErr
			}
		}
		state.PullRequest = &published
		if failure := r.deleteMergedBranch(ctx, &state, published); failure != "" {
			state.PublishFailure = failure
		}
		result, err := r.completeIntegrated(ctx, state, false)
		result.Detail = fmt.Sprintf("the run was interrupted while landing through pull request %d, and the forge has merged it into %s", published.Number, target)
		result.ConfigMismatches = comparison.active
		result.TemplateConfigMismatches = comparison.templates
		return result, err
	case observed.AutoMerge:
		published.MergeQueued = true
		state.PullRequest = &published
		detail := fmt.Sprintf("the run was interrupted while landing through pull request %d, and the forge holds its merge into %s queued", published.Number, target)
		if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, strings.Join([]string{
			"Yoyodyne found this item's run interrupted while it was landing the change through its pull request.",
			"Run: " + state.RunID,
			fmt.Sprintf("Pull request: #%d %s", published.Number, published.URL),
			"Merge queued: the forge merges this request once the base branch's requirements are met; `yoyo reconcile` settles the run when it does.",
			fmt.Sprintf("The local %s was not moved and is not moved until the forge merges.", target),
		}, "\n")); err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the queued landing for run %s: %w", state.RunID, err)
		}
		completedAt := r.clock().Now()
		state.Status = runstate.StatusSucceeded
		state.CompletedAt = &completedAt
		state.Phase = runstate.PhaseCleaningUp
		state.UpdatedAt = completedAt
		if err := r.Store.Save(state); err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the queued landing of run %s: %w", state.RunID, err)
		}
		result := reconciliationOf(state, ActionQueued)
		result.Detail = detail
		return result, nil
	default:
		reason := fmt.Sprintf("the run was interrupted while landing its change on %s through pull request %d, and the forge has not merged it (the request is %s): the local %s was never moved, so the change is on its pull request and on no target branch",
			target, published.Number, strings.ToLower(nonEmpty(observed.State, "in an unreported state")), target)
		state.PublishFailure = reason
		itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
		if err != nil {
			return reconciliationOf(state, ActionBlocked), err
		}
		observation, err := r.observeArtifacts(ctx, state)
		if err != nil {
			return reconciliationOf(state, ActionUnsettled), err
		}
		notes, err := r.recordBlocker(ctx, state, itemStatus, observation, reason)
		if err != nil {
			return reconciliationOf(state, ActionBlocked), err
		}
		state.Blocker = runstate.RecordBlocker(notes)
		settled, saveErr := r.saveTerminalFailure(state, reason)
		result := reconciliationOf(settled, ActionBlocked)
		result.Detail = reason
		result.DocketProblem = r.docketStoppedRun(settled)
		return result, saveErr
	}
}

// confirmQueuedPublication establishes that the merge the forge reported is what
// reached the remote target, and records the merge commit it made of the
// promotion. That is the evidence the closure below rests on, so a failure here
// is an outstanding publication and nothing is closed against it: the forge says
// it merged and nothing could check what the merge produced.
//
// mergeCommit is the commit the forge named as the merge, where it named one. It
// decides only what is recorded, never whether the merge is confirmed: it is
// recorded where it is the merge of this promotion on the remote, and the merge
// is found in the remote history otherwise.
//
// It is asked once, exactly as it always was. The recoverable-failure rule is
// applied below to the deletion and to nothing else here: a sweep settles its
// runs one at a time under each one's lease, so a boundary that waits out a
// window holds up every run behind it, and that is worth spending on the step
// whose failure a person would otherwise have to finish by hand.
func (r Reconciler) confirmQueuedPublication(ctx context.Context, state runstate.State, published *runstate.PullRequest, mergeCommit string) error {
	remoteTarget, err := r.Worktrees.ConfirmRemoteTarget(ctx, integrationOf(state), mergeCommit)
	if err != nil {
		return fmt.Errorf("confirm the queued merge reached %s: %w", state.Integration.TargetBranch, err)
	}
	published.MergeCommit = remoteTarget
	return nil
}

// integrationOf is the promotion a run recorded, in the shape the repository
// access asks about it.
func integrationOf(state runstate.State) gitworktree.Integration {
	return gitworktree.Integration{
		Branch:               state.Branch,
		TargetBranch:         state.Integration.TargetBranch,
		SourceCommit:         state.Integration.SourceCommit,
		TargetCommit:         state.Integration.TargetCommit,
		PreviousTargetCommit: state.Integration.PreviousTargetCommit,
	}
}

// deleteMergedBranch removes the branch the merge consumed, once the item is
// closed and the work is on both branches. It reports what stopped it and never
// anything else: nothing here can undo the merge, so the worst a failure leaves
// is a dead branch on the remote.
//
// That is still worth recording. Nothing sweeps a remote branch afterwards — the
// convergence sweep only removes local ones — so the leftover is a person's, and
// the outstanding publication is what puts it on the triage docket and holds the
// item out of the pull for as long as it stands. What it must not do, and what
// it did until yoyodyne-ifd.301, is decide whether the item closes.
func (r Reconciler) deleteMergedBranch(ctx context.Context, state *runstate.State, published runstate.PullRequest) string {
	err := r.recovering(ctx, state, runstate.RetryDeleteRemoteBranch, func(ctx context.Context) error {
		return r.Worktrees.DeleteRemoteBranch(ctx, worktreeOf(*state), published.HeadCommit)
	})
	if err == nil {
		return ""
	}
	failure := fmt.Errorf("delete the merged remote branch: %w", err).Error()
	state.PublishFailure = failure
	state.UpdatedAt = r.clock().Now()
	if saveErr := r.Store.Save(*state); saveErr != nil {
		return errors.Join(errors.New(failure), fmt.Errorf("record the leftover remote branch of run %s: %w", state.RunID, saveErr)).Error()
	}
	// The item is settled by now, so this is a second note rather than a line in
	// the settlement's. A settlement that said nothing about the branch it left
	// behind would be the whole of what anybody reading the item is told.
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderLeftoverBranchNotes(*state, failure)); err != nil {
		return errors.Join(errors.New(failure), fmt.Errorf("record the leftover remote branch on %s: %w", state.WorkItemID, err)).Error()
	}
	return failure
}

// recoverIntegration records the promotion an interrupted process made but
// never wrote down, and then finishes the run from there. The evidence is
// reconstructed from what integration guarantees rather than from the target as
// it stands now: integration refuses a target that drifted from the recorded
// base, and it only ever fast-forwards, so the target moved from exactly the
// base commit to exactly the branch's commit.
func (r Reconciler) recoverIntegration(ctx context.Context, state runstate.State, observation gitworktree.Observation) (Reconciliation, error) {
	// The promotion is only this run's to claim if this run's review approved
	// it. Without that the commit in the target needs a person, not a closed
	// item.
	if state.ReviewDecision != runstate.ReviewApprove {
		reason := fmt.Sprintf("commit %s from this run's branch is contained in %s, but the run recorded no approving review for it",
			observation.BranchCommit, state.TargetBranch)
		itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
		if err != nil {
			return reconciliationOf(state, ActionBlocked), err
		}
		return r.blockRun(ctx, state, itemStatus, observation, reason)
	}
	state.Integration = &runstate.Integration{
		TargetBranch:         state.TargetBranch,
		SourceCommit:         observation.BranchCommit,
		TargetCommit:         observation.BranchCommit,
		PreviousTargetCommit: state.BaseCommit,
	}
	// The durable invariants are checked before the tracker is touched, not
	// after. Closing an item and only then discovering that the evidence for it
	// cannot be recorded would leave a closed item behind a run that stays
	// outstanding forever.
	if err := state.Validate(); err != nil {
		reason := fmt.Sprintf("commit %s from this run's branch is contained in %s, but the run's evidence does not support recording that promotion: %v",
			observation.BranchCommit, state.TargetBranch, err)
		itemStatus, statusErr := r.itemStatus(ctx, state.WorkItemID)
		if statusErr != nil {
			return reconciliationOf(state, ActionBlocked), statusErr
		}
		state.Integration = nil
		return r.blockRun(ctx, state, itemStatus, observation, reason)
	}
	return r.completeIntegrated(ctx, state, true)
}

// completeIntegrated carries a run whose work is already promoted to its
// terminal state. It keeps the pipeline's ordering: the outcome and the settled
// item first, then the durable terminal record, and only then the removal of
// artifacts. That order is what stops a settled item from ever sitting behind a
// run that still says something is in flight.
func (r Reconciler) completeIntegrated(ctx context.Context, state runstate.State, recovered bool) (Reconciliation, error) {
	comparison, err := r.nameConfigReaders(ctx, &state, "")
	if err != nil {
		return reconciliationOf(state, ActionCompleted), err
	}
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionCompleted), err
	}
	// An item the interrupted process already settled is left alone, so
	// reconciling twice records one outcome and one settlement. What settling
	// means is what the run's own records say about the change: an integrated
	// change discharges the item, or its developer claimed evidence or its
	// reviewer approved evidence and it does not, and a sweep that read only the
	// promotion would close an item its own run said to leave open.
	if !itemSettled(state, itemStatus) {
		if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderReconciledIntegrationNotes(state, recovered)); err != nil {
			return reconciliationOf(state, ActionCompleted), fmt.Errorf("record reconciled outcome for run %s: %w", state.RunID, err)
		}
		if !state.Discharges() {
			// The settled state is taken back, because the settlement can decide the
			// item goes somewhere other than where the run claimed and this is the
			// record saved below — the one every surface reads the disposition off.
			settled, settleErr := settleUndischarged(ctx, r.Tracker, state)
			if settleErr != nil {
				return reconciliationOf(state, ActionCompleted), fmt.Errorf("reopen the work item run %s did not discharge: %w", state.RunID, settleErr)
			}
			state = settled
		} else if _, err := r.Tracker.Complete(ctx, state.WorkItemID, reconciledCompletionReason(state)); err != nil {
			return reconciliationOf(state, ActionCompleted), fmt.Errorf("close integrated work item for run %s: %w", state.RunID, err)
		}
	}
	if !state.Status.Terminal() {
		completedAt := r.clock().Now()
		state.Status = runstate.StatusSucceeded
		state.SettledQuietSince = settledQuietSince(state, completedAt)
		state.CompletedAt = &completedAt
	}
	// This is the settlement the record most depends on. The work landed and the
	// item is settled on it, so a run whose observation stopped somewhere mid-graph reads
	// afterwards exactly like one that walked the definition to the end — which
	// is the single way the account of a run can be quietly wrong in the
	// direction of looking clean. The recorded baseline holds one of these: a
	// process killed inside integration is settled as succeeded with its instance
	// still standing in `integrate`, and it says so.
	r.noteUnfinishedObservation(&state)
	state.Phase = runstate.PhaseCleaningUp
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionCompleted), fmt.Errorf("save reconciled run state for %s: %w", state.RunID, err)
	}

	cleanup, cleanupErr := r.Worktrees.CleanupIntegrated(ctx, gitworktree.CleanupRequest{
		Worktree:     worktreeOf(state),
		TargetBranch: state.Integration.TargetBranch,
		SourceCommit: state.Integration.SourceCommit,
	})
	state.WorktreeRemoved = cleanup.WorktreeRemoved
	state.BranchRemoved = cleanup.BranchRemoved
	if cleanupErr != nil {
		// The run stays outstanding so the next sweep tries again. Repeating
		// cleanup is safe: it refuses anything that is not this run's proven
		// artifacts and does nothing at all over artifacts already gone.
		cleanupErr = fmt.Errorf("clean up reconciled run %s: %w", state.RunID, cleanupErr)
		state.CleanupFailure = cleanupErr.Error()
		state.UpdatedAt = r.clock().Now()
		if saveErr := r.Store.Save(state); saveErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("record outstanding cleanup for run %s: %w", state.RunID, saveErr))
		}
		return reconciliationOf(state, ActionCompleted), cleanupErr
	}
	// Nothing is left to remove, so the run is complete and the outstanding
	// cleanup marker the interrupted process left is no longer true.
	state.CleanupFailure = ""
	if state.StopClass == runstate.StopCleanup {
		state.StopClass = ""
	}
	state.Phase = runstate.PhaseComplete
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionCompleted), fmt.Errorf("save completed run state for %s: %w", state.RunID, err)
	}
	result := reconciliationOf(state, ActionCompleted)
	result.ConfigMismatches = comparison.active
	result.TemplateConfigMismatches = comparison.templates
	// The detail says what the sweep did to the item, and "closed" is only one of
	// the two things it can have done.
	settled := "closed"
	if !state.Discharges() {
		settled = "put back in the backlog undischarged"
	}
	result.Detail = "integrated work was " + settled + " and its artifacts removed"
	if recovered {
		result.Detail = "integration recovered from the repository, then " + settled + " and cleaned up"
	}
	return result, nil
}

// abandon settles a run whose remaining work cannot be finished from durable
// state: an interrupted developer attempt leaves uncommitted work nobody has
// judged, and re-running the developer is exactly what reconciliation must not
// do. The run becomes terminal either way. When the item is still claimed or
// the run's artifacts survive, the item also carries a durable blocker, because
// something is preserved that a person has to replan, reuse, or retire.
func (r Reconciler) abandon(ctx context.Context, state runstate.State, observation gitworktree.Observation) (Reconciliation, error) {
	reason := fmt.Sprintf("the run was interrupted in the %s phase with nothing integrated, and no attempt of the harness can finish it", nonEmpty(string(state.Phase), "unrecorded"))
	return r.abandonFor(ctx, state, observation, reason)
}

// recordedPark is a wait a run recorded and returned from, which only a
// process continues: what it was, in words the settled run's reason carries, and
// the moment the record last moved for it.
type recordedPark struct {
	says  string
	since time.Time
	// providerStop is why the harness stopped the provider, where the park was
	// that stop. It outlives the park on the settled run's record, because a
	// first silent-stream stall is one the harness continues itself.
	providerStop string
}

// parkNothingServes reports a run parked on something the harness itself will
// never continue, which is every park but three. A usage limit or an overloaded
// provider is continued by the sweep's own last step once its deadline passes
// (ContinueWaits), and before then is the bounded wait it recorded. Work the
// item waits on is continued by a watching session's pull once that work closes
// (Scheduler.nextContinuations), and is a wait on that work until then. The
// operator's pause is theirs while it stands. Everything else — a provider the
// harness stopped on time, an unresolved directive, a tracker that would not
// answer, a provider nobody could reach, and the operator's pause once lifted —
// waits on somebody typing `yoyo run`, which is the wait that held a slot for
// twenty hours on 2026-09-26.
//
// It is asked only under the run's lease, so a park a live process is serving —
// asleep on the operator's pause, or waiting out an outage in-process — never
// reaches here.
func (r Reconciler) parkNothingServes(state runstate.State) (recordedPark, bool) {
	since := state.UpdatedAt
	switch {
	case pausedForOperatorHold(state):
		if r.Holds == nil {
			return recordedPark{}, false
		}
		_, held, err := r.Holds.Held()
		if err != nil || held {
			return recordedPark{}, false
		}
		return recordedPark{says: "it parked on the operator's pause of all harness activity, which has since been lifted", since: since}, true
	case pausedForUsageLimit(state):
		if exitsOnInProcessBound(state) {
			return recordedPark{}, false
		}
		// An outage wait's deadline is its next probe, and nothing has failed to
		// serve it until that has passed.
		if state.UsageLimitResetsAt.After(since) {
			since = *state.UsageLimitResetsAt
		}
		return recordedPark{says: "it was waiting out " + runstate.DescribePause(state.PauseCause, state.UsageLimitKind) + ", and nothing asked the provider again at its recorded probe", since: since}, true
	case stoppedProviderIsResumable(state):
		return recordedPark{says: "the harness stopped the AI session running it because " + describeProviderStop(state.ProviderStop), since: since, providerStop: state.ProviderStop}, true
	case pausedForDirective(state):
		return recordedPark{says: "it paused for unresolved directive " + state.DirectivePause.DirectiveID, since: since}, true
	case pausedForTracker(state):
		return recordedPark{says: "it parked because " + state.TrackerPause.Summary(), since: since}, true
	}
	return recordedPark{}, false
}

// vanished reports a park that has now sat, with nothing continuing it, for the
// whole of the grace. The age is measured from the record's last write, which for
// such a run is the park itself: nothing writes to a parked run until something
// continues it, and a continuation would be holding the lease this sweep has.
func (r Reconciler) vanished(park recordedPark) bool {
	return r.clock().Now().Sub(park.since) >= r.vanishedGrace()
}

func (r Reconciler) vanishedGrace() time.Duration {
	if r.VanishedGrace > 0 {
		return r.VanishedGrace
	}
	return DefaultVanishedGrace
}

// settleVanished ends a parked run that nothing then continued. It is the one
// settlement here the record did not ask for: the run says it may be continued,
// and what this decides is that nobody is going to, on the evidence that the
// grace has passed and the lease was free to take.
//
// The stoppage is recorded as an environmental one — the harness's own doing
// rather than the work's — naming exactly what the sweep observed: no live
// process, no ending recorded, the park it was in, and the last moment the record
// moved. Whether the round it ends is refused, in the class's sense, is decided
// the way every environmental round is: on whether it delivered anything. A
// stopped attempt that left a change in its worktree or on its branch spent what
// it spent, and the change is what the development manager decides about; one
// that left nothing is a round the item must not have paid for, and a repair
// grant it consumed is given back. Neither is read from the run's own account of
// itself, because that account was written by a process that is gone; both are
// read from the repository.
//
// Everything past that is the settlement an interrupted run already gets — the
// item blocked with the account of it, the run terminal with the blocker on it,
// the artifacts untouched, and the stoppage docketed — so a repair-continue the
// development manager decides carries out exactly as it does for a run a killed
// process left. Nothing here removes, moves, or judges the change.
func (r Reconciler) settleVanished(ctx context.Context, state runstate.State, park recordedPark) (Reconciliation, error) {
	observation := gitworktree.Observation{}
	if state.WorktreePath != "" {
		var err error
		observation, err = r.Worktrees.Observe(ctx, worktreeOf(state))
		if err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("observe run %s artifacts: %w", state.RunID, err)
		}
	}
	now := r.clock().Now().UTC()
	state.Environmental = vanishedRefusal(&state, observation, park, now)
	reason := vanishedReason(state, park, r.vanishedGrace())
	// A park is an instruction to continue later, and the record refuses to
	// carry one on a terminal run: what it said is kept in the refusal's detail
	// and in the reason, which is where a reader of the settled run finds it.
	clearRecordedParks(&state)
	return r.abandonFor(ctx, state, observation, reason)
}

// clearRecordedParks takes every instruction to continue later off a run that is
// ending now, as a run ending in its own process clears them: the terminal
// record refuses each of them, and what stopped the run is named by the reason
// rather than by a park nothing will ever resume.
func clearRecordedParks(state *runstate.State) {
	state.UsageLimitResetsAt = nil
	state.UsageLimitPausedSince = nil
	state.UsageLimitResetUnknown = false
	state.PauseCause = ""
	state.ProviderStop = ""
	state.DirectivePause = nil
	state.DependencyPause = nil
	state.TrackerPause = nil
	state.OperatorHeldSince = nil
}

// vanishedRefusal is the environmental account of a run whose process vanished,
// settled where it is written: the sweep is the only process that will ever
// look at this round, so there is no later settle to leave the class to.
//
// The round delivered something if the worktree holds uncommitted work or the
// branch has moved past the base the run recorded. A run with no worktree
// recorded delivered nothing, and that is known rather than guessed — the
// harness never gave it anywhere to deliver to. The grant is returned only on a
// round that delivered nothing, which is the class's own rule; the review round
// is never returned here, because a parked run never reached the verdict that
// would have charged one.
func vanishedRefusal(state *runstate.State, observation gitworktree.Observation, park recordedPark, now time.Time) *runstate.EnvironmentalRefusal {
	refusal := &runstate.EnvironmentalRefusal{
		Cause: runstate.CauseProcessVanished,
		Detail: singleLine(fmt.Sprintf(
			"no live process held run %s, no ending was recorded on it, and it last wrote to its record at %s, when %s",
			state.RunID, park.since.UTC().Format(time.RFC3339), park.says),
			runstate.MaxEnvironmentalDetailBytes),
		RecordedAt:   now,
		ProviderStop: park.providerStop,
		Settled:      true,
	}
	delivered := state.WorktreePath != "" &&
		(observation.WorktreeDirty ||
			(observation.BranchExists && observation.BranchCommit != "" && observation.BranchCommit != state.BaseCommit))
	if delivered {
		return refusal
	}
	refusal.Refused = true
	refusal.GrantReturned = state.ReturnGrantedRound()
	return refusal
}

// vanishedReason is the sweep's account of why it ended a run the record said
// could be continued. It says what was observed and what was decided from it,
// because it is what the work item, the run's own record, and the docket entry
// all carry, and a reader of any of them is owed the same sentence: nobody
// edited this record by hand, the harness settled it, and here is why.
func vanishedReason(state runstate.State, park recordedPark, grace time.Duration) string {
	return fmt.Sprintf(
		"the run was recorded as running in the %s phase with no live process behind it: at %s %s, no ending was ever recorded, and nothing continued the run within %s of that, so the harness ended the run. The cause was outside the work, so nothing about the change was judged, and the change was kept: the branch and worktree are left exactly as the run left them",
		nonEmpty(string(state.Phase), "unrecorded"), park.since.UTC().Format(time.RFC3339), park.says, grace)
}

// honoursStop reports a run the sweep may end on a stop request: one in flight
// that has not reached its promotion. A live run reads a stop only at a
// provider-call boundary, and the last of those is its review, so a run in its
// integrating phase or with a promotion on its record is past every point a
// stop could have reached. Cancelling one would record work already on the
// target as stopped; the sweep completes it from its promotion instead, as it
// always has.
func honoursStop(state runstate.State) bool {
	return state.Status.InFlight() && state.Integration == nil && state.Phase != runstate.PhaseIntegrating
}

// settleStopRequest honours a stop somebody asked for on a run whose process is
// gone. A live run reads the request at its next provider-call boundary and ends
// itself cancelled, its artifacts where they are and its item told; a run with
// no process reaches no boundary, and on 2026-09-27 a stop the development
// manager decided stood unread for most of a day over a run that had been dead
// since the evening before. So the sweep ends it the way the run would have, in
// the same words and to the same record: cancelled, the branch and worktree
// untouched, the item told, and — where the stop carries out her decision — the
// stoppage docketed as settled by it. It is done at once rather than after the
// grace, because the stop is the answer the grace would otherwise be waiting
// for, and the lease this sweep holds is what says no process is there to give
// it.
func (r Reconciler) settleStopRequest(ctx context.Context, state runstate.State, request runstate.StopRequest) (Reconciliation, error) {
	observation, err := r.observeArtifacts(ctx, state)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), err
	}
	reason := operatorStop{request: request}.Error() +
		"; no process was holding the run to honour it at a boundary, so the harness's sweep ended it in that process's place. Nothing about the change was judged, and the branch and worktree are left exactly as the run left them"
	completedAt := r.clock().Now()
	clearRecordedParks(&state)
	state.StopClass = stopRequestClass(request)
	state.Status = runstate.StatusCancelled
	state.SettledQuietSince = settledQuietSince(state, completedAt)
	state.UpdatedAt = completedAt
	state.CompletedAt = &completedAt
	state.Failure = runstate.RecordFailure(reason)
	state.CheckStage.CloseInterrupted(completedAt)
	// The item is told before the record goes terminal, so an interruption here
	// leaves the run outstanding for the next sweep rather than a stopped run
	// its item never heard about.
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderReconciledFailureNotes(state, observation, reason)); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the stop of run %s on its item: %w", state.RunID, err)
	}
	r.noteUnfinishedObservation(&state)
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("save the stopped run %s: %w", state.RunID, err)
	}
	result := reconciliationOf(state, ActionCancelled)
	result.Detail = reason
	// A stop the development manager decided is docketed as settled by her
	// decision, as the run would have docketed it; an operator's stop hands
	// nobody a decision and is docketed nowhere, as it always was.
	if strings.TrimSpace(request.Decision) != "" && r.Docket != nil {
		if err := r.Docket.RecordDecidedStop(state, request); err != nil {
			result.DocketProblem = fmt.Errorf("docket the stop the development manager decided on run %s: %w", state.RunID, err).Error()
		}
	}
	return result, nil
}

// abandonFor is abandon with the reason the caller has, for the one settlement
// whose reason is not an interruption at all.
func (r Reconciler) abandonFor(ctx context.Context, state runstate.State, observation gitworktree.Observation, reason string) (Reconciliation, error) {
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionFailed), err
	}
	if itemStatus == "in_progress" || preservedArtifacts(observation) {
		return r.blockRun(ctx, state, itemStatus, observation, reason)
	}
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderReconciledFailureNotes(state, observation, reason)); err != nil {
		return reconciliationOf(state, ActionFailed), fmt.Errorf("record reconciled failure for run %s: %w", state.RunID, err)
	}
	settled, err := r.recordTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionFailed)
	result.Detail = reason
	return result, err
}

// blockRun records the durable blocker a stopped run leaves behind and makes
// the run terminal. The blocker is recorded before the run is closed out, so an
// interruption here leaves the run outstanding and the blocker recorded rather
// than a settled run nobody was told about.
func (r Reconciler) blockRun(ctx context.Context, state runstate.State, itemStatus string, observation gitworktree.Observation, reason string) (Reconciliation, error) {
	if state.Status == runstate.StatusSucceeded && state.Blocker == "" {
		// This sweep has just disproved the recorded promotion.
		state.StopClass = runstate.StopIntegration
	}
	notes, err := r.recordBlocker(ctx, state, itemStatus, observation, reason)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	// The blocker is kept on the run as well as on the item, so what stopped
	// this run is readable from its own record rather than from whichever of the
	// item's notes turns out to have been this one's.
	//
	// It is written through saveTerminalFailure rather than recordTerminalFailure
	// because the blocker has to reach disk even for a run some killed process
	// already made terminal: a stoppage settled onto such a record and skipped is
	// a run whose durable record never says it stopped, so the docket built after
	// this sweep, the outcome word every listing derives from the blocker, and the
	// channel's reading of the record all miss it.
	state.Blocker = runstate.RecordBlocker(notes)
	settled, saveErr := r.saveTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionBlocked)
	result.Detail = reason
	result.DocketProblem = r.docketStoppedRun(settled)
	return result, saveErr
}

// docketStoppedRun puts a run this sweep stopped in front of the development
// manager. It is idempotent and keyed to the stoppage, so a run that docketed
// its own ending before the process died adds nothing here, and a sweep that
// runs twice over the same run dockets it once.
//
// It never fails the settlement: the blocker is already on the work item and the
// run is already settled by the time this runs, so a docket that could not be
// written is a delivery that did not happen rather than a run that has to be
// settled again. What it could not write is reported instead, and the next
// docket build finds the same stoppage on the run's own record.
func (r Reconciler) docketStoppedRun(state runstate.State) string {
	if r.Docket == nil {
		return ""
	}
	if _, err := r.Docket.RecordStoppedRun(state); err != nil {
		return fmt.Errorf("docket the stopped run %s: %w", state.RunID, err).Error()
	}
	return ""
}

// recordBlocker puts this run's evidence on the work item and returns the words
// it recorded, which are what the run's own record and the triage docket carry
// afterwards. An item already blocked keeps the status it has; the reason is
// still recorded, because this run's evidence is what a replan needs.
func (r Reconciler) recordBlocker(ctx context.Context, state runstate.State, itemStatus string, observation gitworktree.Observation, reason string) (string, error) {
	notes := renderReconcileBlockerNotes(state, observation, reason)
	var err error
	if itemStatus == "blocked" {
		_, err = r.Tracker.RecordOutcome(ctx, state.WorkItemID, notes)
	} else {
		_, err = r.Tracker.Block(ctx, state.WorkItemID, notes)
	}
	if err != nil {
		return "", fmt.Errorf("record blocker for run %s: %w", state.RunID, err)
	}
	return notes, nil
}

// observeArtifacts looks at what the repository holds of a run's branch and
// worktree, for a blocker that has to name them. A run that recorded no
// worktree has nothing to look at, and says so in the note.
func (r Reconciler) observeArtifacts(ctx context.Context, state runstate.State) (gitworktree.Observation, error) {
	if state.WorktreePath == "" {
		return gitworktree.Observation{}, nil
	}
	observation, err := r.Worktrees.Observe(ctx, worktreeOf(state))
	if err != nil {
		return gitworktree.Observation{}, fmt.Errorf("observe run %s artifacts: %w", state.RunID, err)
	}
	return observation, nil
}

// recordTerminalFailure makes an unfinishable run durably terminal in the phase
// it stopped in, so the record still says where it got to. A run that is already
// terminal carries its own record of how it ended and is left exactly as it is.
func (r Reconciler) recordTerminalFailure(state runstate.State, reason string) (runstate.State, error) {
	if state.Status.Terminal() {
		return state, nil
	}
	return r.saveTerminalFailure(state, reason)
}

// saveTerminalFailure writes the terminal record. It is separate from
// recordTerminalFailure because settling can change durable state that has to
// reach disk even when the run was already terminal, and a caller with such a
// change needs the write rather than the skip.
//
// A record that was already terminal keeps the status it wrote for itself, and
// that includes "succeeded": a run that promoted work and then had the promotion
// contradicted said something about itself no sweep witnessed, and rewriting it
// would lose that account. What stops the kept status reading as a run whose work
// landed is the precedence rule on runstate.Ending — the blocker this saves
// outranks every terminal status there, so the outcome every surface prints is
// "stopped" while the durable status stays what the run recorded. The blocker
// reaching disk is what that rule depends on, which is why this path writes even
// where recordTerminalFailure would skip.
// settledQuietSince is the moment a run nothing was carrying last moved, kept
// on the record as the run is settled. The settlement overwrites UpdatedAt and
// writes an end, and both are when the harness noticed rather than when the run
// stopped holding its slot. The stall reading would otherwise take that end as
// activity, and a sweep settling a dead line would silence the alarm for exactly
// the crash it exists to catch.
func settledQuietSince(state runstate.State, settledAt time.Time) *time.Time {
	quiet := state.UpdatedAt
	if quiet.IsZero() || quiet.After(settledAt) {
		quiet = settledAt
	}
	return &quiet
}

func (r Reconciler) saveTerminalFailure(state runstate.State, reason string) (runstate.State, error) {
	reconciled := runstate.RecordFailure("reconciled after an interrupted run: " + reason)
	if !state.Status.Terminal() {
		completedAt := r.clock().Now()
		state.StopClass = runstate.CauseProcessVanished.StopClass()
		if state.Environmental != nil && state.Environmental.Cause == runstate.CauseProcessVanished {
			if state.Environmental.ProviderStop != "" {
				state.StopClass = runstate.ProviderStopClass(state.Environmental.ProviderStop)
			}
		}
		state.Status = runstate.StatusFailed
		state.SettledQuietSince = settledQuietSince(state, completedAt)
		state.UpdatedAt = completedAt
		state.CompletedAt = &completedAt
		state.Failure = reconciled
		// A check stage the dead process left running is closed as interrupted,
		// so the record does not go on saying the checks are running under a run
		// that has ended, with a spend that grows for as long as it stands.
		state.CheckStage.CloseInterrupted(completedAt)
	} else if strings.TrimSpace(state.Failure) == "" && strings.TrimSpace(state.PublishFailure) == "" {
		// A record that was already terminal keeps the status it wrote for itself,
		// but a record that says nothing about why is what left every surface
		// answering "why did this stop" with an empty line. So the sweep's own
		// reason is written exactly where the record gives none. A run that
		// recorded how it ended keeps its own words, and so does one whose reason
		// is an outstanding publication: both say more than settling it does, and
		// a publication that could not be pushed must never be read as a failed
		// piece of work.
		state.Failure = reconciled
	}
	// Asked after the status is settled, because the divergence names the status
	// the run ended as, and before the save that is this run's last word.
	r.noteUnfinishedObservation(&state)
	if err := r.Store.Save(state); err != nil {
		return state, fmt.Errorf("save reconciled run state for %s: %w", state.RunID, err)
	}
	return state, nil
}

// noteUnfinishedObservation records, on a run this sweep is making terminal,
// that the instance observing it never reached a terminal of its own.
//
// A run served by a live pipeline records this for itself, in activeRun.fail. A
// run whose process died does not: settling it is the only ending it gets, so
// without this the runs that end by the routes most worth hearing about — a
// process killed by the network, by the provider, or by the machine — would be
// exactly the runs recorded as having agreed with the definition throughout.
// This repository's own history for the work that added the observation is four
// consecutive runs killed that way, so it is not a corner.
//
// It notes rather than saves, like its counterpart: the caller is writing the
// terminal record next and this is one of the fields that record carries. It
// never fails a settlement — an observation that cannot be read is recorded as
// the divergence it is, and the sweep carries on settling the run either way.
//
// Two call sites cover all three settlements. completeIntegrated is the
// completed one; saveTerminalFailure is the failed one and the blocked one
// both, because blockRun writes its terminal record through it. So a run the
// sweep blocks with its instance still mid-graph records the gap exactly as the
// other two do. TestABlockedSettlementRecordsTheGapItsInstanceLeaves measures
// that at blockRun itself, and the "settled as blocked" case of
// TestASweepRecordsTheGapAnInterruptedObservationLeaves through the sweep:
// remove the call from saveTerminalFailure and both fail on the missing
// divergence while the completed case still passes. What is recorded is the
// gap and not the settlement: the recorded baseline's blocked trace,
// `reconciliation-blocks-a-run-interrupted-while-developing`, carries no
// divergence because the developer's ending sent its instance to the
// `abandoned` terminal before the sweep looked, which is the observation having
// finished rather than this path having been skipped.
// TestASweepRecordsNoDivergenceWhereTheObservationReachedATerminal measures
// that half, and docs/delivery-pipeline-baseline.md discloses it beside the
// trace.
func (r Reconciler) noteUnfinishedObservation(state *runstate.State) {
	if divergence := unfinishedObservation(r.Store, *state); divergence != "" {
		state.WorkflowDivergence = divergence
	}
}

// itemStatus reads the tracker's own view of the item, which is the half of
// reconciliation durable run state cannot supply: a run that died before it
// wrote anything down may still have claimed its item.
func (r Reconciler) itemStatus(ctx context.Context, workItemID string) (string, error) {
	item, err := r.Tracker.Show(ctx, workItemID)
	if err != nil {
		return "", fmt.Errorf("load work item %s: %w", workItemID, err)
	}
	return item.Status, nil
}

// preservedArtifacts reports whether anything this run created is still there.
// A run whose artifacts are already gone leaves nothing to hand to a person.
func preservedArtifacts(observation gitworktree.Observation) bool {
	return observation.WorktreeRegistered || observation.WorktreePresent || observation.BranchExists
}

// worktreeOf rebuilds the worktree identity from what was recorded when it was
// created, never from what the repository looks like now. Every manager call
// revalidates ownership of these fields before acting on them.
func worktreeOf(state runstate.State) gitworktree.Worktree {
	return gitworktree.Worktree{
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		Path:          state.WorktreePath,
		Branch:        state.Branch,
		BaseCommit:    state.BaseCommit,
		TargetBranch:  state.TargetBranch,
		HarnessCommit: state.HarnessCommit,
	}
}

func reconciliationOf(state runstate.State, action ReconcileAction) Reconciliation {
	return Reconciliation{
		RunID:           state.RunID,
		WorkItemID:      state.WorkItemID,
		Action:          action,
		Status:          state.Status,
		Outcome:         state.Outcome(),
		Phase:           state.Phase,
		Integration:     state.Integration,
		Branch:          state.Branch,
		WorktreePath:    state.WorktreePath,
		WorktreeRemoved: state.WorktreeRemoved,
		BranchRemoved:   state.BranchRemoved,
		CleanupFailure:  state.CleanupFailure,
	}
}

func (r Reconciler) validate() error {
	var problems []error
	if r.Tracker == nil {
		problems = append(problems, errors.New("work tracker is required"))
	}
	if r.Worktrees == nil {
		problems = append(problems, errors.New("worktree observer is required"))
	}
	if r.Store == nil {
		problems = append(problems, errors.New("state store is required"))
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

func (r Reconciler) clock() execution.Clock {
	if r.Clock == nil {
		return execution.RealClock{}
	}
	return r.Clock
}

// renderReconciledIntegrationNotes explains a settlement the run itself never
// got to record. It distinguishes integration the run wrote down from
// integration found in the repository afterwards, because only the second one
// is a claim reconciliation made on the run's behalf.
func renderReconciledIntegrationNotes(state runstate.State, recovered bool) string {
	// Where the item goes is what the run's own landing says, and the headline
	// says which: this note is written before the item is settled, so a headline
	// that always said "closed" would be the first line on an item the sweep was
	// about to put back in the backlog.
	settlement := "the item is being closed"
	if !state.Discharges() {
		settlement = "the item is being put back in the backlog rather than closed, because its landing did not discharge it,"
	}
	headline := "Yoyodyne reconciled an interrupted run: the change was already integrated, so " + settlement + " and the run's artifacts removed."
	if recovered {
		headline = "Yoyodyne reconciled an interrupted run: its integration commit was found in the target branch even though the run never recorded it, so " + settlement + " and the run's artifacts removed."
	}
	lines := []string{
		headline,
		"Run: " + state.RunID,
		"Phase when interrupted: " + string(state.Phase),
		"Branch: " + state.Branch,
		"Integrated into: " + state.Integration.TargetBranch,
		"Integrated commit: " + state.Integration.SourceCommit,
		"Previous target commit: " + state.Integration.PreviousTargetCommit,
	}
	if state.ReviewSessionID != "" {
		lines = append(lines, "Reviewer session: "+state.ReviewSessionID)
	}
	if state.ReviewBaseCommit != "" && state.ReviewHeadCommit != "" {
		lines = append(lines, "Reviewed against: base "+state.ReviewBaseCommit+", tip "+state.ReviewHeadCommit)
	}
	if state.ReviewDecision != "" {
		lines = append(lines, "Review decision: "+state.ReviewDecision)
	}
	return strings.Join(lines, "\n")
}

// renderQueuedMergeNotes tells the work item what became of a merge that was
// still queued when its run finished. The run deliberately left the closure to
// this answer, so this note is the only place an operator learns whether the
// publication completed, and what is left if it did not.
func renderQueuedMergeNotes(state runstate.State, detail string, catchup *gitworktree.Catchup) string {
	lines := []string{
		"Yoyodyne settled the merge this run left queued with the forge.",
		"Outcome: " + detail,
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"Pull request merged: " + strconv.FormatBool(state.PullRequest.Merged),
	}
	if state.PullRequest.MergeCommit != "" {
		lines = append(lines, fmt.Sprintf("Remote target commit: %s (the forge's merge commit above the promoted commit)", state.PullRequest.MergeCommit))
	}
	if state.PublishFailure != "" {
		standing := "The change is integrated into the local target branch, which is the authoritative one; only its publication is unfinished."
		// A protected target's local branch was never moved, so saying the change
		// is integrated there, and that the local branch is authoritative, is the
		// wording that read as main having been moved ahead of the forge.
		if state.Integration.ThroughPullRequest {
			standing = fmt.Sprintf("The local %s was not moved: its target is protected, so the change lands only by the forge merging its pull request. The run's branch and worktree are kept until that merge is confirmed.",
				state.Integration.TargetBranch)
		}
		lines = append(lines, "Publication outstanding: "+state.PublishFailure, standing)
	}
	return strings.Join(append(lines, renderCatchupNotes(catchup)...), "\n")
}

// renderLeftoverBranchNotes says what the settlement left on the remote. It is
// a second note rather than a line in the settlement's because it is written
// after the item has been settled, and it says nothing about where the item
// went: that is decided by the run's own landing, and this is about a branch.
func renderLeftoverBranchNotes(state runstate.State, failure string) string {
	return strings.Join([]string{
		"Yoyodyne settled the forge's merge of this item and could not delete the branch that merge consumed.",
		"Publication outstanding: " + failure,
		"Run: " + state.RunID,
		"Merged remote branch: " + state.Branch,
		"Nothing else is left of the publication: the change is on " + state.Integration.TargetBranch +
			" locally and on the forge. Delete the branch, or leave it for whoever reads the triage docket.",
	}, "\n")
}

// settledMergeCompletionReason closes an item on the whole of what happened to
// it: the promotion its own run made and reviewed, and the forge merge that run
// asked for and could not wait out.
func settledMergeCompletionReason(state runstate.State) string {
	if state.Integration.ThroughPullRequest {
		return fmt.Sprintf("Reviewed by Yoyodyne run %s, then merged by the forge into %s through pull request %d at %s",
			state.RunID, state.Integration.TargetBranch, state.PullRequest.Number, nonEmpty(state.PullRequest.MergeCommit, "a merge commit the forge did not name"))
	}
	return fmt.Sprintf("Reviewed and integrated by Yoyodyne run %s, then merged by the forge: %s is at %s",
		state.RunID, state.Integration.TargetBranch, state.Integration.TargetCommit)
}

func reconciledCompletionReason(state runstate.State) string {
	return fmt.Sprintf("Reconciled by Yoyodyne after run %s was interrupted: %s is at %s",
		state.RunID, state.Integration.TargetBranch, state.Integration.TargetCommit)
}

// renderReconcileBlockerNotes hands a stopped run to a person. It names only
// artifacts that were actually observed, so nobody is sent after a worktree or
// a branch that is no longer there.
func renderReconcileBlockerNotes(state runstate.State, observation gitworktree.Observation, reason string) string {
	lines := []string{
		"Yoyodyne stopped this item while reconciling an interrupted run. No developer was restarted for it.",
		"Reason: " + reason,
		"Run: " + state.RunID,
		"Phase when interrupted: " + nonEmpty(string(state.Phase), "unrecorded"),
	}
	// A run that finished with its merge queued was not interrupted: it ended,
	// and what the sweep settled is the forge's answer. Said as an interruption,
	// its cleaning_up phase read as a clean-up somebody cut short.
	if state.Status.Terminal() && state.MergeDrop != nil {
		lines[0] = "Yoyodyne stopped this item while settling the merge its finished run left queued with the forge. No developer was restarted for it."
		lines[3] = "Phase: " + nonEmpty(string(state.Phase), "unrecorded") + ", where the finished run was waiting on the forge's merge"
	}
	if state.Integration != nil && state.Integration.ThroughPullRequest {
		lines = append(lines, fmt.Sprintf("The local %s was not moved: its target is protected, so the change lands only by the forge merging its pull request, and until then it is on its pull request and its branch and on no target branch.",
			state.Integration.TargetBranch))
	}
	if state.RepairAttempts > 0 {
		lines = append(lines, "Repair attempts already spent: "+strconv.Itoa(state.RepairAttempts))
	}
	// The relaunch budget is durable for exactly this reader: a run interrupted
	// after absorbing provider deaths has already spent part of it, and a note
	// that omitted them would describe a run with more room than it has.
	if state.TransientRelaunches > 0 {
		lines = append(lines, "Relaunches after a provider death already spent: "+strconv.Itoa(state.TransientRelaunches))
	}
	lines = append(lines, renderObservedArtifacts(state, observation)...)
	if state.CheckFailure != nil {
		if state.CheckFailure.ForgeHeadCommit != "" {
			lines = append(lines, fmt.Sprintf("Last failing forge check: %s (commit %s)", state.CheckFailure.Command, state.CheckFailure.ForgeHeadCommit))
		} else {
			lines = append(lines, fmt.Sprintf("Last failing check: %s (exit %d)", state.CheckFailure.Command, state.CheckFailure.ExitCode))
		}
	}
	if state.PathRefusal != nil {
		lines = append(lines, "Refused protected paths: "+strings.Join(state.PathRefusal.Paths, ", "))
	}
	if state.ReplayConflict != nil {
		lines = append(lines, "Unreconciled replay conflict against: "+state.ReplayConflict.TargetBranch)
	}
	if state.ReviewSummary != "" {
		lines = append(lines, "Last review summary: "+state.ReviewSummary)
	}
	for _, finding := range state.ReviewFindingDetails {
		location := ""
		if finding.File != "" {
			location = fmt.Sprintf(" (%s:%d)", finding.File, finding.Line)
		}
		label := finding.Severity
		if finding.Disposition != "" {
			label += ", " + finding.Disposition
		}
		lines = append(lines, fmt.Sprintf("Finding [%s]%s: %s", label, location, finding.Message))
	}
	return strings.Join(lines, "\n")
}

// renderReconciledFailureNotes records a run that left nothing behind. It is
// deliberately not a blocker: the item is not claimed and no artifact survives,
// so the work is simply available again.
func renderReconciledFailureNotes(state runstate.State, observation gitworktree.Observation, reason string) string {
	lines := []string{
		"Yoyodyne reconciled an interrupted run that left nothing behind. The item is not blocked and remains available.",
		"Reason: " + reason,
		"Run: " + state.RunID,
	}
	return strings.Join(append(lines, renderObservedArtifacts(state, observation)...), "\n")
}

// renderObservedArtifacts reports what the repository actually shows, which is
// what separates a preserved change from a recorded path that no longer exists.
func renderObservedArtifacts(state runstate.State, observation gitworktree.Observation) []string {
	if state.WorktreePath == "" {
		return []string{"No worktree was recorded for this run."}
	}
	var lines []string
	if observation.WorktreePresent {
		lines = append(lines,
			"Preserved worktree: "+state.WorktreePath,
			"Uncommitted changes in the worktree: "+strconv.FormatBool(observation.WorktreeDirty))
	} else {
		lines = append(lines, "Recorded worktree is gone: "+state.WorktreePath)
	}
	if observation.BranchExists {
		lines = append(lines, "Preserved branch: "+state.Branch+" at "+observation.BranchCommit)
	} else {
		lines = append(lines, "Recorded branch is gone: "+state.Branch)
	}
	if state.TargetBranch != "" {
		lines = append(lines, "Integration target: "+state.TargetBranch)
	}
	return lines
}
