package orchestrator

// Post-merge hygiene. Automatic integration includes its own aftermath: a merge
// whose local convergence needs a human is integration that is only half
// automatic. Everything here is judgement-free by construction — a fast-forward
// onto a commit that already contains the local branch, and the deletion of a
// branch whose work the target provably carries — so it belongs to the harness
// rather than to any role that decides things.
//
// It lives beside reconciliation because it is the same shape of work: a sweep
// over durable state, safe to repeat, that finishes what an interrupted or
// finished run left owed. A run's own settle path catches its target up while
// it still holds the promotion lease; this is what covers every run that could
// not, every merge the forge performed after its run was over, and every branch
// a cleanup could not reach.
//
// The leftover checkouts are the same shape of debris one step further out, and
// they are the half that costs the machine rather than only the repository:
// every worktree registration is a path an agent's sandbox profile denies on
// every command it spawns, so registrations that accumulate with the harness's
// history eventually stop commands spawning at all. Retiring them is as
// judgement-free as the rest — what a checkout carried in commits is on a branch
// this does not touch, and what it carried uncommitted is recorded on a
// run-scoped ref before the directory goes, so retiring one moves work rather
// than deciding anything about it.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// settledWorktreeTail is how many settled runs keep their checkout on disk. It
// is what stops the sweep taking the evidence out from under the person it was
// preserved for: a run that stopped a few minutes ago is the one somebody is
// about to open, and leaving the last few where they are costs nothing.
//
// Past the tail a checkout is debris, and the machine pays for it. Every
// registration is a path an agent's sandbox profile denies on every command it
// spawns, so a repository that keeps them all eventually cannot spawn a command
// at all — the failure this bound exists for, reached at 180 registrations on
// the harness's own machine, 168 of them left by runs that stopped without
// promoting anything.
//
// That population is what makes the bound a real one rather than a bound on a
// subset. A run that stopped is the run most likely to have left a change in its
// checkout, so a sweep that declined to touch anything uncommitted would have
// left most of those 168 exactly where they were. Instead the work is recorded
// on a run-scoped ref and the directory goes: nothing is lost — the ref is
// proven to carry the tree before the removal runs, and every commit the
// checkout carried is on a branch this never touches — and what remains kept is
// anomalies rather than a category, a directory Git is not managing or a
// registration on a branch the run never recorded.
const settledWorktreeTail = 8

// Convergence is what one sweep did to bring local state onto the forge's and
// to keep what the runs left behind bounded. It reports per artifact rather than
// as a total, because the failures a reader acts on are different: a target held
// behind the remote is a checkout that is out of date, a branch kept is work
// somebody still has to decide about, and a checkout kept is a directory
// somebody has to look at by hand.
type Convergence struct {
	Targets []gitworktree.Catchup `json:"targets"`
	// Publications is the forge's half of the same hygiene: the pull requests of
	// runs whose work landed by another vehicle, closed with that vehicle named.
	Publications []PublicationSweep `json:"publications"`
	// Unsuperseded is what is left open: the pull requests still open at the
	// forge that this sweep cannot close, because no recorded landing supersedes
	// them. They are named so that what is open is a list somebody has read;
	// deciding each one is a person's.
	Unsuperseded []OpenPublication `json:"unsuperseded"`
	Worktrees    []WorktreeSweep   `json:"worktrees"`
	Branches     []BranchSweep     `json:"branches"`
	// Findings revisits saved obligations even when their original operation
	// has finished and no artifact sweep returns a result for the run.
	Findings []ReconcileFindingMaintenance `json:"findings"`
	// Registrations is the repository-wide prune that runs between the two. It
	// is not per run because what it removes is exactly what no run record
	// names any more.
	Registrations RegistrationSweep `json:"registrations"`
	// Divergences is every recorded divergence this sweep lifted because it found
	// its target converged with the remote's, and every one it could not lift.
	// A divergence still standing is not listed: its target is in Targets with
	// the catch-up's reason for holding.
	Divergences []DivergenceLift `json:"divergences"`
	// DivergenceProblem is the record of divergences not being readable, in
	// which case none was lifted and the watching session's hold stands.
	DivergenceProblem string `json:"divergence_problem,omitempty"`
}

// PublicationSweep is one superseded run's publication and what became of it.
// Both the run that published it and the vehicle its work landed by are named,
// because that pair is the whole of the justification for closing a pull
// request somebody may still be looking at.
type PublicationSweep struct {
	Finding        *readmodel.Attention `json:"finding,omitempty"`
	FindingProblem string               `json:"finding_problem,omitempty"`
	RunID          string               `json:"run_id"`
	WorkItemID     string               `json:"work_item_id"`
	SupersededBy   Supersession         `json:"superseded_by"`
	PublicationRetirement
}

// WorktreeSweep is one settled run's leftover checkout and what became of it.
// The run is named because that is how an operator finds what the checkout was
// for, and a kept one is a fact somebody has to act on: unlike a kept branch, it
// is a registration that goes on costing every command spawned on this machine
// until somebody deals with the work in it.
type WorktreeSweep struct {
	Finding        *readmodel.Attention `json:"finding,omitempty"`
	FindingProblem string               `json:"finding_problem,omitempty"`
	RunID          string               `json:"run_id"`
	WorkItemID     string               `json:"work_item_id"`
	Path           string               `json:"path"`
	Removed        bool                 `json:"removed"`
	Kept           string               `json:"kept,omitempty"`
	Failure        string               `json:"failure,omitempty"`
	// PreservedWork is the ref the checkout's uncommitted work was recorded on
	// before the directory went, empty when it held none. It is reported because
	// it is the answer to the only question retiring a half-finished change
	// raises: where did it go.
	PreservedWork string `json:"preserved_work,omitempty"`
	// RecordProblem is a checkout that was retired and whose run's record could
	// not be told so. It is deliberately not a Failure: the directory is gone
	// either way, and the thing to act on is the opposite of a retirement that
	// did not happen — every reader of that run will now name a directory that is
	// not there. Reporting it as a failed retirement would send somebody to
	// remove what is already removed.
	RecordProblem string `json:"record_problem,omitempty"`
	// ItemProblem is a capture the work item could not be told about. It is beside
	// RecordProblem because it is the same class and not the same reader: the run
	// record is what a sweep or a re-run consults, and the item is what a person
	// picking the work up reads. A capture only the run record names is how the
	// work of run-48216ea9 came to be reported destroyed while it sat on a ref.
	ItemProblem string `json:"item_problem,omitempty"`
}

// RegistrationSweep is what the repository-wide prune removed, and what stopped
// it where it could not run. A prune that failed never fails the sweep, for the
// reason one unremovable branch does not: what could not be done is reported
// beside everything that could.
type RegistrationSweep struct {
	Pruned []string `json:"pruned"`
	// Unfinished is every registration a `git worktree add` never finished
	// filling in, cleared or kept with the reason. One of these used to stop
	// every later run on the repository at its own creation, so each is said
	// rather than counted.
	Unfinished []gitworktree.UnfinishedRegistration `json:"unfinished"`
	Failure    string                               `json:"failure,omitempty"`
}

// BranchSweep is one settled run's leftover branch and what became of it. The
// run is named because that is how an operator finds what the branch was for;
// the branch is only ever removed on containment proved in the repository.
type BranchSweep struct {
	Finding        *readmodel.Attention `json:"finding,omitempty"`
	FindingProblem string               `json:"finding_problem,omitempty"`
	RunID          string               `json:"run_id"`
	WorkItemID     string               `json:"work_item_id"`
	Branch         string               `json:"branch"`
	TargetBranch   string               `json:"target_branch"`
	Commit         string               `json:"commit,omitempty"`
	Removed        bool                 `json:"removed"`
	Kept           string               `json:"kept,omitempty"`
	Failure        string               `json:"failure,omitempty"`
	// RecordProblem is a branch that is gone and whose run's record could not be
	// told so, and it is the same class as the checkout sweep's: the branch is
	// gone either way, and what needs acting on is that every reader of that run
	// will go on being told its change is preserved on it.
	RecordProblem string `json:"record_problem,omitempty"`
	// ReleaseCorrected reports a branch still standing whose work item the claim
	// audit had given back without saying so, and which this sweep told. It is
	// the correction run-838ffc48 needed: its release said nothing was working on
	// yoyodyne-ifd.432.10 and nothing about the approved change on its branch,
	// and was read as the run having preserved nothing.
	ReleaseCorrected bool `json:"release_corrected,omitempty"`
	// ItemProblem is that correction failing to reach the item, or to be recorded
	// on the run so it is made once.
	ItemProblem string `json:"item_problem,omitempty"`
}

// Converge brings the primary checkout and the local branches onto what the
// forge has: every target branch the harness knows about is fast-forwarded onto
// its remote counterpart, every superseded run's publication is closed with the
// vehicle its work landed by named, the checkouts of settled runs past the tail
// are retired, the registrations of checkouts that are already gone are pruned,
// and then every settled run's leftover branch whose work those targets already
// carry is deleted.
//
// The order matters and is not an accident. A target that has just caught up
// contains more than it did a moment ago, so the branches are swept afterwards
// and against the branch as it now stands — and the checkouts go before the
// branches for the reason a retirement does them in that order, because a
// branch a checkout still holds is kept and that checkout is the one being
// retired just above. The prune sits between them, so a registration something
// else emptied stops holding its branch in the same pass rather than the next.
// The publications come after the targets because the landing they are closed
// on is what the target has just caught up onto, and before the checkouts and
// branches because they touch neither: what a superseded run kept locally is
// the checkout sweep's and the branch sweep's, judged by their own rules.
//
// One branch that cannot be converged never stops the sweep, for the reason one
// unreconcilable run does not: what could not be done is reported beside
// everything that could, so an operator reading the sweep sees the whole of it.
func (r Reconciler) Converge(ctx context.Context) (Convergence, error) {
	if err := r.validate(); err != nil {
		return Convergence{}, err
	}
	recorded, err := r.Store.Recorded()
	if err != nil {
		return Convergence{}, fmt.Errorf("discover recorded runs: %w", err)
	}
	convergence := Convergence{
		Targets:       make([]gitworktree.Catchup, 0),
		Publications:  make([]PublicationSweep, 0),
		Unsuperseded:  make([]OpenPublication, 0),
		Worktrees:     make([]WorktreeSweep, 0),
		Branches:      make([]BranchSweep, 0),
		Registrations: RegistrationSweep{Pruned: make([]string, 0), Unfinished: make([]gitworktree.UnfinishedRegistration, 0)},
		Divergences:   make([]DivergenceLift, 0),
	}
	targets, divergenceProblem := r.divergedTargetsToCatchUp(recordedTargets(recorded))
	for _, target := range targets {
		convergence.Targets = append(convergence.Targets, r.catchUp(ctx, target))
	}
	// A target found converged lifts the divergence recorded on it, which is what
	// lets a watching session held on that divergence choose again at its next
	// poll with nothing released. It is the only thing that lifts one: the
	// branches converging is the whole of what the hold waits on.
	convergence.DivergenceProblem = divergenceProblem
	if divergenceProblem == "" {
		convergence.Divergences = r.liftDivergences(convergence.Targets)
	}
	superseded, open := partitionPublications(recorded)
	convergence.Unsuperseded = open
	for _, publication := range superseded {
		sweep, swept := r.sweepPublication(ctx, publication)
		if swept {
			convergence.Publications = append(convergence.Publications, sweep)
		}
	}
	for _, state := range sweepableWorktrees(recorded) {
		sweep, swept := r.sweepWorktree(ctx, state)
		if swept {
			convergence.Worktrees = append(convergence.Worktrees, sweep)
		}
	}
	convergence.Registrations = r.pruneRegistrations(ctx)
	released := r.releasedClaims()
	for _, state := range recorded {
		sweep, swept := r.sweepBranch(ctx, state, released)
		if swept {
			convergence.Branches = append(convergence.Branches, sweep)
		}
	}
	for index := range convergence.Publications {
		result := &convergence.Publications[index]
		result.Finding, result.FindingProblem = r.recordReconcileFinding(ctx, result.RunID, runstate.ReconcileSuperseded, result.Failure)
	}
	for index := range convergence.Worktrees {
		result := &convergence.Worktrees[index]
		result.Finding, result.FindingProblem = r.recordReconcileFinding(ctx, result.RunID, runstate.ReconcileWorktree, strings.Join(nonEmptyProblems(result.Failure, result.RecordProblem, result.ItemProblem), "; "))
	}
	for index := range convergence.Branches {
		result := &convergence.Branches[index]
		result.Finding, result.FindingProblem = r.recordReconcileFinding(ctx, result.RunID, runstate.ReconcileBranch, strings.Join(nonEmptyProblems(result.Failure, result.RecordProblem, result.ItemProblem), "; "))
	}
	convergence.Findings, err = r.maintainReconcileFindings(ctx)
	return convergence, err
}

// sweepPublication retires one superseded run's pull request, and reports
// whether this sweep is what had anything to say about it.
//
// The run's record is taken under its own lease and re-read there, for the
// reason settling a run is: the listing is a snapshot another process may have
// moved on from, and the close and the note of it have to be one act. A run a
// live process holds is left to that process and reported by nobody — it is not
// a failure, and a line about it on every sweep would bury the ones that are.
//
// The checkout and the local branch the superseded run kept are deliberately
// not touched here. The checkout is the checkout sweep's, which captures what
// it holds before retiring it; the branch is the branch sweep's, which needs the
// target to judge it — and a local branch carrying work nothing promoted is the
// one copy of that work left once the remote branch has gone, so it is kept
// with the reason rather than deleted with the request.
func (r Reconciler) sweepPublication(ctx context.Context, superseded supersededPublication) (PublicationSweep, bool) {
	published := *superseded.state.PullRequest
	sweep := PublicationSweep{
		RunID:        superseded.state.RunID,
		WorkItemID:   superseded.state.WorkItemID,
		SupersededBy: superseded.by,
		PublicationRetirement: PublicationRetirement{
			Number: published.Number,
			URL:    published.URL,
			Branch: published.Branch,
		},
	}
	if r.Publisher == nil {
		sweep.Failure = fmt.Sprintf(
			"run %s left pull request %d open and run %s landed the work instead, and this sweep has no forge access to close it",
			superseded.state.RunID, published.Number, superseded.by.RunID)
		return sweep, true
	}
	state, lease, err := r.Store.AdoptRun(ctx, superseded.state.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		return PublicationSweep{}, false
	case err != nil:
		sweep.Failure = fmt.Errorf("adopt run %s to retire the publication it left: %w", superseded.state.RunID, err).Error()
		return sweep, true
	}
	defer lease.Release()

	// What was read from the listing is asked again of the record just adopted.
	// Another process may have retired this publication in between, and a second
	// close would put a second comment on somebody's pull request.
	if !retirablePublication(state) {
		return PublicationSweep{}, false
	}
	sweep.PublicationRetirement = retirePublication(ctx, r.Publisher, r.Worktrees, state, superseded.by)
	if sweep.Failure != "" {
		return sweep, true
	}
	published = *state.PullRequest
	published.Superseded = superseded.by.Vehicle()
	// The forge's last word is recorded beside it, so a reader of the record is
	// not told the request is open under the line saying what closed it.
	published.State = "CLOSED"
	state.PullRequest = &published
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		// The forge is settled and the record is not, so the next sweep asks the
		// forge about a request it has already closed. That costs a query and adds
		// no second comment — Close reports an already-closed request rather than
		// closing it again — but it repeats forever until somebody looks, so it is
		// never left unsaid.
		sweep.Failure = fmt.Errorf("record that pull request %d of run %s was retired as superseded: %w",
			published.Number, state.RunID, err).Error()
	}
	return sweep, true
}

// sweepableWorktrees lists the settled runs whose checkout this sweep may
// retire: the ones past the tail.
//
// A run that still owes a step is never a candidate, for the reason its branch
// is not — a live developer is working in that checkout, and whether it is still
// needed is reconciliation's question rather than hygiene's. A retired checkout
// with a finding or an undelivered recovery note still needs that obligation
// settled, but it occupies no slot in the tail of checkouts kept on disk.
//
// The tail is held back from the newest end, and it is a tail of checkouts that
// are actually there rather than of records: a run whose checkout this sweep
// found gone has that written onto it whether the sweep is what removed it or
// something else was, so it drops out of the candidates instead of occupying a
// slot that was meant to keep somebody's evidence on disk.
func sweepableWorktrees(recorded []runstate.State) []runstate.State {
	candidates := make([]runstate.State, 0, len(recorded))
	retired := make([]runstate.State, 0)
	for _, state := range recorded {
		if state.Retirement != nil {
			continue
		}
		if state.WorktreePath == "" {
			continue
		}
		if state.WorktreeRemoved {
			if hasReconcileFinding(state, runstate.ReconcileWorktree) || (state.PreservedWorkRef != "" && state.PreservedWorkNotedAt == nil) {
				retired = append(retired, state)
			}
			continue
		}
		if state.Outstanding() {
			continue
		}
		candidates = append(candidates, state)
	}
	// Newest first, so what is held back is the most recent evidence rather than
	// whichever runs the store happened to list first.
	sort.SliceStable(candidates, func(left, right int) bool {
		first, second := settledAt(candidates[left]), settledAt(candidates[right])
		if first.Equal(second) {
			return candidates[left].RunID > candidates[right].RunID
		}
		return first.After(second)
	})
	if len(candidates) <= settledWorktreeTail {
		return retired
	}
	return append(retired, candidates[settledWorktreeTail:]...)
}

// settledAt is when a run stopped being something anybody was watching. A run
// with no completion time recorded is ordered by when its record was last
// written, which is the closest thing it has to one.
func settledAt(state runstate.State) time.Time {
	if state.CompletedAt != nil {
		return *state.CompletedAt
	}
	return state.UpdatedAt
}

// A failed carry-out still needs the artifacts its standing decision names.
// AwaitingCarryOut excludes refusals for scheduling; retirement must keep them
// while the development manager resolves the refusal. An unread record keeps
// the artifacts too, rather than treating missing evidence as permission. An
// automatic check or silent-stall continuation also keeps its artifacts until
// it is superseded, completed, or refused, even when intake or capacity delays
// it past the tail.
func (r Reconciler) recoveryNeedsArtifacts(ctx context.Context, state runstate.State) (string, func()) {
	noRelease := func() {}
	if state.Retirement != nil {
		return "the run was retired after its item merged; its branch and checkout are preserved", noRelease
	}
	if state.ArtifactsRetiredBy != "" {
		return "", noRelease
	}
	if state.IntegrationStop != nil {
		return "the stopped integration still needs its recorded branch and checkout", noRelease
	}
	counters, release, err := r.Store.Triage().LockCounters(ctx, state.WorkItemID)
	if err != nil {
		return fmt.Sprintf("the recovery decision could not be read, so the artifacts are kept: %v", err), noRelease
	}
	standing := counters.StandingOf(state)
	if !standing.Decided && state.HarnessContinuesCheckStage() {
		return "the outstanding automatic check continuation still needs this run's branch and checkout", release
	}
	if !standing.Decided && state.HarnessContinuesStall() {
		return "the harness has still to resume this run in the same AI session, which needs this run's branch and checkout", release
	}
	if standing.Decided && standing.Spends && (!standing.Repair || standing.GrantOutstanding) {
		return "the development manager's outstanding recovery decision still needs this run's artifacts", release
	}
	return "", release
}

// sweepWorktree retires one settled run's checkout, and reports whether an
// operator has anything to read about it.
//
// Those are two decisions and they are deliberately made apart. Whether the
// record is written turns on whether the checkout is gone; whether a line is
// printed turns on whether this sweep is what changed something. A checkout that
// was already gone — cleanup took it when the run integrated, an operator
// removed it by hand, an external `git worktree prune` unregistered it — is
// exactly the case where those two answers differ: nobody needs to read about
// it, and its record is the one most in need of correcting, because until it is
// written every reader of that run is sent to a directory that is not there.
//
// Nothing here decides anything: the retirement keeps a checkout holding
// uncommitted work, keeps a directory Git is not managing, and never touches the
// branch.
//
// It is done under the run's own lease, which is what makes the removal and the
// record of it one act. `yoyo status`, the triage docket, and a re-run all read
// the run's record as the answer to whether that directory is still there, so a
// removal written down under a different snapshot than the one it acted on is
// exactly the reader sent after a checkout that is gone.
func (r Reconciler) sweepWorktree(ctx context.Context, recorded runstate.State) (WorktreeSweep, bool) {
	sweep := WorktreeSweep{
		RunID:      recorded.RunID,
		WorkItemID: recorded.WorkItemID,
		Path:       recorded.WorktreePath,
	}
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		// A live process owns this run, so it is that process's checkout rather
		// than debris, whatever the listing snapshot said a moment ago.
		return WorktreeSweep{}, false
	case err != nil:
		// Nothing is removed: an artifact retired without its record being
		// writable is the stale record this took the lease to avoid.
		sweep.Failure = fmt.Errorf("take the record of run %s to retire its checkout: %w", recorded.RunID, err).Error()
		return sweep, true
	}
	defer lease.Release()

	// The state is re-read by AdoptRun, so a run something else settled, retired,
	// or re-entered in the meantime is never swept from the snapshot this loop
	// started with.
	if state.WorktreePath == "" {
		return WorktreeSweep{}, false
	}
	if state.WorktreeRemoved {
		pendingNote := state.PreservedWorkRef != "" && state.PreservedWorkNotedAt == nil
		if pendingNote {
			sweep.PreservedWork = state.PreservedWorkRef
			sweep.ItemProblem = r.recordPreservedWork(ctx, &state, state.PreservedWorkRef)
		}
		return sweep, pendingNote || hasReconcileFinding(state, runstate.ReconcileWorktree)
	}
	if state.Outstanding() {
		return WorktreeSweep{}, false
	}
	kept, releaseDecision := r.recoveryNeedsArtifacts(ctx, state)
	defer releaseDecision()
	if kept != "" {
		sweep.Kept = kept
		return sweep, true
	}
	// The work in the checkout is captured rather than kept, because "keep
	// anything dirty" is not a bound: a stopped run is the population most likely
	// to have left a change behind, so declining to act on those would leave most
	// of the registrations exactly where they were.
	removal, err := r.Worktrees.RemovePreservedWorktree(ctx, worktreeOf(state), gitworktree.CaptureUncommittedWork)
	if err != nil {
		sweep.Failure = fmt.Errorf("retire the checkout of run %s: %w", state.RunID, err).Error()
	}
	sweep.Kept = removal.Kept
	sweep.PreservedWork = removal.PreservedWork
	// Removed covers both "this retired it" and "it was already gone", which is
	// what the record has to say either way. Only the first is something this
	// sweep did, and only the first is reported as a retirement.
	if removal.Removed {
		sweep.Removed = removal.Registered
		sweep.RecordProblem = r.recordSweptWorktree(&state, removal.PreservedWork)
		// The item is told wherever a capture happened, and only then. What the run
		// failed with was written on that item hours or days earlier, naming a
		// checkout that was there when it was written; this is the correction, and
		// it is the only thing a person reading the item can follow to the work.
		if removal.PreservedWork != "" {
			sweep.ItemProblem = r.recordPreservedWork(ctx, &state, removal.PreservedWork)
		}
	}
	if !sweep.Removed && sweep.Kept == "" && sweep.Failure == "" && sweep.RecordProblem == "" && sweep.ItemProblem == "" {
		// The checkout was gone before this sweep reached it and its record now
		// says so. There is nothing left for anybody to read, and nothing left to
		// probe: the run drops out of the candidates on every later pass.
		return sweep, hasReconcileFinding(state, runstate.ReconcileWorktree)
	}
	return sweep, true
}

// recordSweptWorktree writes the removal onto the run it belongs to, and reports
// what stopped it where it could not. A record left saying the checkout is still
// there is worse than one that was never swept: the artifact is gone either way,
// and only one of the two sends somebody looking for it.
//
// The ref the work was captured onto is recorded beside it, because the run's
// own record is the only place anybody would think to look for where a stopped
// run's half-finished change went.
func (r Reconciler) recordSweptWorktree(state *runstate.State, preservedWork string) string {
	swept := r.clock().Now()
	state.WorktreeRemoved = true
	state.WorktreeSweptAt = &swept
	if preservedWork != "" {
		state.PreservedWorkRef = preservedWork
		state.PreservedWorkNotedAt = nil
	}
	// When the run ended is what dates it; this dates the last thing the harness
	// did to what it left behind, which is what UpdatedAt has always meant.
	state.UpdatedAt = swept
	if err := r.Store.Save(*state); err != nil {
		return fmt.Sprintf(
			"the checkout of run %s was retired and its own record still says otherwise, so anything reading that run will name a directory that is gone: %v",
			state.RunID, err)
	}
	return ""
}

// recordPreservedWork tells the work item where a retired checkout's
// uncommitted work went, and reports what stopped it where it could not.
//
// It is the correction to a note the item already carries. A run that failed
// wrote its own ending onto this item naming a checkout that was there at the
// time; retiring that checkout makes those words describe a directory nobody
// will find, and the ref the work moved to is recorded on the run rather than
// anywhere a person picking the item up would look. Run-48216ea9's 23 files were
// reported destroyed for exactly that gap, while the ref that held them was two
// commands away —
// `docs/diagnoses/yoyodyne-ifd-275-preservation-claimed-without-a-check.md`.
//
// A note that could not be written never fails the sweep, for the reason the
// record problem beside it does not: the capture happened, the ref is a
// garbage-collection root, and what is missing is somebody being told.
func (r Reconciler) recordPreservedWork(ctx context.Context, state *runstate.State, preservedWork string) string {
	recordCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	note := renderPreservedWorkNotes(*state, preservedWork)
	// Delivery can succeed just before its marker save fails. The item is the
	// other durable copy, so verify it before appending the recovery note again.
	item, err := r.Tracker.Show(recordCtx, state.WorkItemID)
	if err != nil {
		return fmt.Sprintf("read prior preservation notes of %s: %v", state.WorkItemID, err)
	}
	if !strings.Contains(item.Notes, note) {
		if _, err := r.Tracker.RecordOutcome(recordCtx, state.WorkItemID, note); err != nil {
			return fmt.Sprintf(
				"the checkout of run %s was retired and what it held was recorded on %s, and %s could not be told, so anything read from that item still names a directory that is gone: %v",
				state.RunID, preservedWork, state.WorkItemID, err)
		}
	}
	noted := r.clock().Now()
	state.PreservedWorkNotedAt = &noted
	state.UpdatedAt = noted
	if err := r.Store.Save(*state); err != nil {
		return fmt.Sprintf("record delivery of the preserved work note for run %s: %v", state.RunID, err)
	}
	return ""
}

// renderPreservedWorkNotes is what the item is told: where the work went, and
// the one command that gets it back.
func renderPreservedWorkNotes(state runstate.State, preservedWork string) string {
	return strings.Join([]string{
		"Yoyodyne retired the checkout of a run of this item and kept what it held.",
		"Run: " + state.RunID,
		"Retired worktree: " + state.WorktreePath,
		"Preserved work: " + preservedWork,
		"Any earlier note of this run naming that worktree describes a directory that is no longer there. Everything it held, committed and uncommitted, is on the ref above; recover it with:",
		"    git worktree add --detach <path> " + preservedWork,
	}, "\n")
}

// pruneRegistrations removes the registrations of checkouts that are no longer
// on disk, whichever run or person left them behind, and the registrations a
// `git worktree add` never finished. It is what covers the ones no run record
// names any more — a checkout somebody deleted by hand, one whose run record is
// itself gone, one from a product this harness no longer holds, one left by an
// add that was killed while registering — which a sweep driven from run state
// cannot see and which cost every later command a deny path in its sandbox
// profile all the same, and the last of which cost every later creation.
func (r Reconciler) pruneRegistrations(ctx context.Context) RegistrationSweep {
	prune, err := r.Worktrees.PruneRegistrations(ctx)
	sweep := RegistrationSweep{Pruned: prune.Pruned, Unfinished: prune.Unfinished}
	if sweep.Pruned == nil {
		sweep.Pruned = make([]string, 0)
	}
	if sweep.Unfinished == nil {
		sweep.Unfinished = make([]gitworktree.UnfinishedRegistration, 0)
	}
	if err != nil {
		sweep.Failure = fmt.Errorf("prune the registrations of worktrees that are already gone: %w", err).Error()
	}
	return sweep
}

// catchUp moves one target branch under that branch's promotion lease, so a
// catch-up and a promotion never read the same branch and then both move it.
// The lease is the harness's own and this is the harness's own process; nothing
// an agent can reach acquires it, and nothing here performs a promotion.
func (r Reconciler) catchUp(ctx context.Context, targetBranch string) gitworktree.Catchup {
	lease, err := r.Store.LeasePromotion(ctx, targetBranch)
	if err != nil {
		return gitworktree.Catchup{
			TargetBranch: targetBranch,
			Held:         fmt.Errorf("wait for a turn to move %s: %w", targetBranch, err).Error(),
		}
	}
	// Releasing is this process letting the next promotion in, and the operating
	// system does it anyway when the process exits, so a close that failed says
	// nothing about the branch below.
	defer func() { _ = lease.Release() }()

	catchup, err := r.Worktrees.CatchUpTarget(ctx, targetBranch)
	if err != nil {
		catchup.TargetBranch = targetBranch
		catchup.Held = err.Error()
	}
	return catchup
}

// sweepBranch retires one settled run's branch, and reports whether the run had
// a branch worth asking about at all.
//
// A run that still owes a step is left entirely alone: settling it may yet need
// the branch, and reconciliation is what decides that. A run in flight is one of
// those, so a live developer's branch is never a candidate here.
//
// The deletion is written onto the run, as the checkout sweep's is and for the
// same reason. `Artifacts.Preserved()` asks BranchRemoved and nothing else, so a
// branch this deleted with nothing recorded goes on reading everywhere as a
// change preserved on a branch — which is what run-48216ea9 read as, both its
// artifacts gone, when a developer went looking for the work and reported it
// destroyed. It is taken under the run's own lease so the deletion and the
// record of it are one act rather than a write against a snapshot something else
// has moved on from.
func (r Reconciler) sweepBranch(ctx context.Context, recorded runstate.State, released map[string]runstate.ReleasedClaim) (BranchSweep, bool) {
	if recorded.Retirement != nil || recorded.Outstanding() || recorded.Branch == "" || recorded.TargetBranch == "" {
		return BranchSweep{}, false
	}
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		// A live process owns this run, so the branch is that process's rather than
		// debris, whatever the listing snapshot said a moment ago.
		return BranchSweep{}, false
	case err != nil:
		return BranchSweep{
			RunID:        recorded.RunID,
			WorkItemID:   recorded.WorkItemID,
			Branch:       recorded.Branch,
			TargetBranch: recorded.TargetBranch,
			Failure:      fmt.Errorf("take the record of run %s to delete its branch: %w", recorded.RunID, err).Error(),
		}, true
	}
	defer lease.Release()

	// Re-read under the lease, so a run something else settled, retired, or
	// re-entered in the meantime is never swept from the snapshot this loop
	// started with.
	if state.Retirement != nil || state.Outstanding() || state.Branch == "" || state.TargetBranch == "" {
		return BranchSweep{}, false
	}
	sweep := BranchSweep{
		RunID:        state.RunID,
		WorkItemID:   state.WorkItemID,
		Branch:       state.Branch,
		TargetBranch: state.TargetBranch,
	}
	kept, releaseDecision := r.recoveryNeedsArtifacts(ctx, state)
	defer releaseDecision()
	if kept != "" {
		sweep.Kept = kept
		return sweep, true
	}
	removal, err := r.Worktrees.RemoveMergedBranch(ctx, state.Branch, state.TargetBranch)
	if err != nil {
		sweep.Commit = removal.Commit
		sweep.Failure = fmt.Errorf("remove the merged branch of run %s: %w", state.RunID, err).Error()
		return sweep, true
	}
	// A branch that is already gone is the ordinary outcome — cleanup removed it
	// when the run finished — and reporting it as a sweep would bury the ones
	// that are actually still there. The record is still corrected where it
	// claims a branch nothing can find, for the reason the checkout sweep
	// corrects one: the artifact is gone either way, and only one of the two
	// answers sends somebody after it.
	if removal.Commit == "" {
		if state.BranchRemoved {
			return sweep, hasReconcileFinding(state, runstate.ReconcileBranch)
		}
		if problem := r.recordSweptBranch(state); problem != "" {
			sweep.RecordProblem = problem
			return sweep, true
		}
		return sweep, hasReconcileFinding(state, runstate.ReconcileBranch)
	}
	sweep.Commit = removal.Commit
	sweep.Removed = removal.Removed
	sweep.Kept = removal.Kept
	// A branch still standing is the one case a release could have misstated, so
	// it is the one case asked about it.
	if !removal.Removed {
		sweep.ReleaseCorrected, sweep.ItemProblem = r.correctRelease(ctx, state, released, removal)
	}
	// A record that already says the branch is gone needs no second writing. That
	// is a run whose own cleanup removed it and whose branch something put back —
	// the leftover this sweep exists for — and the record was right both times.
	if removal.Removed && !state.BranchRemoved {
		sweep.RecordProblem = r.recordSweptBranch(state)
	}
	return sweep, true
}

// releasedClaims is every claim the audit gave back, keyed by the item and the
// run whose death left it. A log that cannot be read corrects nothing, and the
// sweep carries on: the correction is a note, and the branch it is about is
// standing either way.
func (r Reconciler) releasedClaims() map[string]runstate.ReleasedClaim {
	if r.Releases == nil {
		return nil
	}
	listed, err := r.Releases.List()
	if err != nil {
		return nil
	}
	released := make(map[string]runstate.ReleasedClaim, len(listed))
	for _, record := range listed {
		released[releaseKey(record.WorkItemID, record.RunID)] = record
	}
	return released
}

func releaseKey(workItemID, runID string) string { return workItemID + "\x00" + runID }

// correctRelease tells a work item that the claim audit gave it back without
// saying that the run behind the claim still had its change on a branch, and
// reports whether it did and what stopped it where it could not.
//
// A release written before the audit looked in the repository said only that
// nothing was working on the item. That is a statement about processes, and it
// was read as one about the change: yoyodyne-ifd.432.10's re-run budget was
// crossed on the reasoning that run-838ffc48 left no preserved change, while its
// branch held the approved change at 8b06428b. So wherever a release left the
// branch out, or said it was not there, and the branch is standing now, the item
// is told once — and the run's record says it was, under the lease this sweep
// already holds, so the next sweep does not say it again.
func (r Reconciler) correctRelease(ctx context.Context, state runstate.State, released map[string]runstate.ReleasedClaim, removal gitworktree.Removal) (bool, string) {
	release, found := released[releaseKey(state.WorkItemID, state.RunID)]
	if !found || state.ReleaseCorrectedAt != nil || removal.Commit == "" {
		return false, ""
	}
	if release.Found != nil && release.Found.Looked() && release.Found.BranchThere {
		return false, ""
	}
	recordCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := r.Tracker.RecordOutcome(recordCtx, state.WorkItemID, renderReleaseCorrection(state, release, removal, r.clock().Now())); err != nil {
		return false, fmt.Sprintf(
			"the branch of run %s is standing and %s was given back without being told so, and could not be told now: %v",
			state.RunID, state.WorkItemID, err)
	}
	corrected := r.clock().Now()
	state.ReleaseCorrectedAt = &corrected
	state.UpdatedAt = corrected
	if err := r.Store.Save(state); err != nil {
		return true, fmt.Sprintf(
			"%s was told that run %s's branch is standing, and the run's record could not say so, so the next sweep will tell it again: %v",
			state.WorkItemID, state.RunID, err)
	}
	return true, ""
}

// renderReleaseCorrection is what the item is told: which release it corrects,
// what the repository holds, and what that means for the next run of the item.
func renderReleaseCorrection(state runstate.State, release runstate.ReleasedClaim, removal gitworktree.Removal, now time.Time) string {
	lines := []string{
		fmt.Sprintf("Correction: the harness gave this item back to the queue at %s over run %s and did not say that the run's change was still there. It is.",
			release.ReleasedAt.UTC().Format(time.RFC3339), state.RunID),
		"Run: " + state.RunID,
		fmt.Sprintf("Branch: %s (checked and there at %s, at %s)", state.Branch, now.UTC().Format(time.RFC3339), removal.Commit),
	}
	if removal.Kept != "" {
		lines = append(lines, "Why it is kept: "+removal.Kept)
	}
	lines = append(lines, "Anything read off that release as the run having left no preserved change is wrong: the branch above holds what the run committed, and a run of this item should start from it rather than derive it again.")
	return strings.Join(lines, "\n")
}

// recordSweptBranch writes the deletion onto the run it belongs to, and reports
// what stopped it where it could not. A record left saying the branch is still
// there is the claim this whole path exists to stop: it is what `yoyo status`,
// the triage docket, and a re-run all read as the answer to whether the change
// survived.
func (r Reconciler) recordSweptBranch(state runstate.State) string {
	swept := r.clock().Now()
	state.BranchRemoved = true
	state.BranchSweptAt = &swept
	// When the run ended is what dates it; this dates the last thing the harness
	// did to what it left behind, which is what UpdatedAt has always meant.
	state.UpdatedAt = swept
	if err := r.Store.Save(state); err != nil {
		return fmt.Sprintf(
			"the branch of run %s is gone and its own record still says otherwise, so anything reading that run will name a branch that is not there: %v",
			state.RunID, err)
	}
	return ""
}

// recordedTargets lists the distinct target branches the recorded runs name, in
// a fixed order so one sweep's report reads like the next one's.
func recordedTargets(recorded []runstate.State) []string {
	seen := make(map[string]struct{}, len(recorded))
	targets := make([]string, 0, len(recorded))
	for _, state := range recorded {
		if state.TargetBranch == "" {
			continue
		}
		if _, known := seen[state.TargetBranch]; known {
			continue
		}
		seen[state.TargetBranch] = struct{}{}
		targets = append(targets, state.TargetBranch)
	}
	sort.Strings(targets)
	return targets
}
