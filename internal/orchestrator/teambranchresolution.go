package orchestrator

// The run that resolves a conflict between the team's branch and the target
// branch (docs/designs/integrating-to-a-local-branch.md, section 3).
//
// When merging the team's branch into the target conflicts, the update waits
// (teambranchupdate.go), and the development manager names one item whose run
// resolves it. That run is the harness's own, recorded under a bookkeeping item
// the way a run that brings a pinned outside file in is (outsidefilelanding.go),
// but unlike that one it invokes a developer: the checkout is cut from a commit
// that carries the merge as Git left it, markers and all, and the developer's
// change on top of it is the resolution.
//
// It goes through the configured checks and the independent reviewer like any
// run, and it ends at promotion. It integrates nothing: its approved head is
// handed back to the update, which builds the merge commit from it, checks and
// reviews that exact commit, and lands it as it lands any update. The run is
// counted against a limit of its own, three rounds per conflict, and never
// against the item's repair budget; its repair attempts are its bookkeeping
// item's. The real item is only ever written a note.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ConflictMarkersCheck is the name the check that finds conflict markers left in
// a resolution is reported under.
const ConflictMarkersCheck = "harness: no conflict markers are left in the files that conflicted"

// maxConflictFileBytes bounds a conflicting file the marker check reads.
const maxConflictFileBytes = 16 << 20

// maxResolutionItemText bounds the real item's text the developer is shown.
const maxResolutionItemText = 6000

// TeamBranchResolutionRequest names one round of resolving one conflict.
type TeamBranchResolutionRequest struct {
	TargetBranch   string
	UpstreamBranch string
	// TeamCommit and TargetBase are the merge that conflicts, and ConflictedBase
	// the commit that carries it as Git left it (gitworktree.BuildConflictedMerge).
	TeamCommit     string
	TargetBase     string
	ConflictedBase string
	Paths          []string
	// WorkItemID and WorkItemTitle are the item the development manager named.
	WorkItemID    string
	WorkItemTitle string
	// Reason and DecidedBy are the decision's.
	Reason    string
	DecidedBy string
	// Items are the items on the target whose landed commits touched the paths.
	Items []runstate.TeamBranchConflictItem
	// Round counts from one. Earlier says, in plain words, why each earlier
	// round did not resolve the conflict.
	Round   int
	Earlier []string
	// Attempt is TeamBranchResolution.Restarts, which names the run.
	Attempt int
}

// TeamBranchResolutionOutcome is what asking for one round came to.
type TeamBranchResolutionOutcome struct {
	RunID string
	// Resolved reports the run ended succeeded with an approved head.
	Resolved     bool
	ApprovedHead string
	// Why is, in plain words, why the round did not resolve the conflict, or
	// why it has not ended.
	Why string
	// NotStarted reports no run was started and none exists: a developer slot,
	// the operator's pause or an intake hold is in the way, and the round is
	// asked for again at the next check without having been used.
	NotStarted bool
	// Running reports the run exists and has not ended: another process holds it,
	// or it is parked, and the round is not over.
	Running bool
}

func (r TeamBranchResolutionRequest) run() runstate.TeamBranchResolutionRun {
	return runstate.TeamBranchResolutionRun{
		TargetBranch: r.TargetBranch, UpstreamBranch: r.UpstreamBranch, TeamCommit: r.TeamCommit, TargetBase: r.TargetBase,
		ConflictedBase: r.ConflictedBase, Paths: slices.Clone(r.Paths), WorkItemID: r.WorkItemID, Round: r.Round, Attempt: r.Attempt,
	}
}

// RunID is the run that makes this round.
func (r TeamBranchResolutionRequest) RunID() string { return r.run().RunID() }

func (r TeamBranchResolutionRequest) says() string {
	return runstate.TeamBranchConflictItem{ID: r.WorkItemID, Title: r.WorkItemTitle}.Says()
}

// teamBranchResolutionItem is the bookkeeping item the run is recorded under.
// What the developer is asked to do is its description; no protected-path grant
// is given, so the run may change only what a run of any item may.
func teamBranchResolutionItem(request TeamBranchResolutionRequest, real beads.WorkItem) beads.WorkItem {
	var description strings.Builder
	fmt.Fprintf(&description, "Merging %s at %s into %s at %s conflicts in %s. The files in your checkout carry Git's conflict markers, and every part of the merge that did not conflict is already applied.\n\n",
		request.UpstreamBranch, shortCommit(request.TeamCommit), request.TargetBranch, shortCommit(request.TargetBase), strings.Join(request.Paths, ", "))
	fmt.Fprintf(&description, "What to do: resolve each conflict so the result keeps both the team's change and the target's. Use the item %s as the context for what the target's side was for. Remove every conflict marker (lines that start with <<<<<<<, ======= or >>>>>>>). Change nothing outside those files unless the resolution needs it, and say why in your summary.\n\n", request.says())
	if reason := strings.TrimSpace(request.Reason); reason != "" {
		fmt.Fprintf(&description, "Why this item was chosen: %s\n\n", oneline.Fold(reason, 1500))
	}
	if len(request.Items) > 1 {
		names := make([]string, 0, len(request.Items))
		for _, item := range request.Items {
			names = append(names, item.Says())
		}
		fmt.Fprintf(&description, "Items on %s whose landed changes touched these files: %s.\n\n", request.TargetBranch, strings.Join(names, ", "))
	}
	if text := strings.TrimSpace(real.Description); text != "" {
		fmt.Fprintf(&description, "The item %s, for context:\n\n%s\n\n", request.says(), boundedItemText(text))
	}
	for i, why := range request.Earlier {
		if i == 0 {
			fmt.Fprintf(&description, "This is round %d of %d. The earlier rounds did not resolve the conflict:\n", request.Round, runstate.MaxTeamBranchResolutionRounds)
		}
		fmt.Fprintf(&description, "- Round %d: %s\n", i+1, oneline.Fold(strings.TrimSpace(why), 1200))
	}
	return beads.WorkItem{
		ID:     request.run().WorkItemBookkeepingID(),
		Title:  fmt.Sprintf("Resolve the conflict merging %s into %s, for %s", request.UpstreamBranch, request.TargetBranch, request.says()),
		Status: "open", Description: strings.TrimRight(description.String(), "\n"),
		AcceptanceCriteria: fmt.Sprintf("No conflict marker is left in %s, and the result keeps both the team's change and the target's.", strings.Join(request.Paths, ", ")),
	}
}

func boundedItemText(text string) string {
	if len(text) <= maxResolutionItemText {
		return text
	}
	cut := maxResolutionItemText
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n[the rest of the item's description is left out]"
}

// teamResolutionRecorder writes a round's start and end as notes on the item the
// development manager named, so a reader of that item sees it without finding
// the run. The writes are bounded and never stop the run, and they change
// nothing about the item but its notes.
type teamResolutionRecorder struct {
	tracker WorkTracker
	itemID  string
}

func (r teamResolutionRecorder) note(ctx context.Context, note string) {
	if r.tracker == nil || r.itemID == "" {
		return
	}
	bounded, cancel := trackerRecordContext(ctx)
	defer cancel()
	if _, err := r.tracker.Show(bounded, r.itemID); err != nil {
		return
	}
	_, _ = r.tracker.RecordOutcome(bounded, r.itemID, note)
}

func (r TeamBranchResolutionRequest) validate() error {
	run := r.run()
	if err := run.Validate(); err != nil {
		return fmt.Errorf("the request to resolve the conflict is not well formed: %w", err)
	}
	return nil
}

// ResolveTeamBranchConflict makes one round of resolving a conflict: a run of
// the item the development manager named, in a checkout that carries the
// conflict, ended once its change is checked, reviewed and committed. A run of
// the same round already recorded is continued or settled rather than started
// again. The outcome says whether the run resolved the conflict and its
// approved head, or why it did not.
func (p Pipeline) ResolveTeamBranchConflict(ctx context.Context, request TeamBranchResolutionRequest) (TeamBranchResolutionOutcome, error) {
	if err := request.validate(); err != nil {
		return TeamBranchResolutionOutcome{}, err
	}
	runID := request.RunID()
	outcome := TeamBranchResolutionOutcome{RunID: runID}
	// The real tracker is kept for the notes on the item the development manager
	// named, and for what that item says; the run itself is recorded under an
	// item of its own and prices, dockets and closes nothing.
	record := teamResolutionRecorder{tracker: p.Tracker, itemID: request.WorkItemID}
	named := beads.WorkItem{ID: request.WorkItemID, Title: request.WorkItemTitle}
	if p.Tracker != nil {
		bounded, cancel := trackerRecordContext(ctx)
		if shown, err := p.Tracker.Show(bounded, request.WorkItemID); err == nil {
			named = shown
			if strings.TrimSpace(request.WorkItemTitle) == "" {
				request.WorkItemTitle = shown.Title
			}
		}
		cancel()
	}
	item := teamBranchResolutionItem(request, named)
	p.Docket, p.Prices = nil, nil
	p.Tracker = &documentTracker{item: item}
	by := runstate.SelectedByDevelopmentManager
	if request.DecidedBy != "development-manager" {
		by = runstate.SelectedByOperator
	}
	p.Selection = runstate.Selection{By: by, Reason: fmt.Sprintf("Merging %s into %s conflicts in %s; %s decided that a run of %s resolves it, round %d of %d.", request.UpstreamBranch, request.TargetBranch, strings.Join(request.Paths, ", "), nonEmpty(strings.ReplaceAll(request.DecidedBy, "-", " "), "a person"), request.says(), request.Round, runstate.MaxTeamBranchResolutionRounds)}
	if err := p.validateDispatch(); err != nil {
		return outcome, err
	}
	if hold, held, err := p.operatorHold(); err != nil || held {
		if err != nil {
			return outcome, err
		}
		outcome.NotStarted, outcome.Why = true, fmt.Sprintf("the operator has paused harness activity since %s", hold.HeldAt.Local().Format("2006-01-02 15:04 MST"))
		return outcome, nil
	}
	if existing, err := p.Store.Load(runID); err == nil {
		return p.continueTeamBranchResolution(ctx, existing, request, item, record)
	} else if !errors.Is(err, os.ErrNotExist) {
		return outcome, err
	}
	if _, held, err := p.holdIntake(item.ID); err != nil || held {
		if err != nil {
			return outcome, err
		}
		outcome.NotStarted, outcome.Why = true, "intake is held, so no new run starts"
		return outcome, nil
	}
	if err := p.Worktrees.ValidateReady(ctx); err != nil {
		return outcome, fmt.Errorf("repository is not ready for an isolated run: %w",
			refusedByEnvironment("the repository is not ready for an isolated run", err))
	}
	dispatchProvider, dispatchNamed, err := p.dispatchBackend(ctx)
	if err != nil {
		return outcome, err
	}
	if err := p.requireBackendReady(ctx, item.ID, dispatchProvider, dispatchNamed); err != nil {
		return outcome, err
	}
	if err := p.requireLaunchSettings(ctx, "the run that resolves the conflict in "+request.TargetBranch, dispatchProvider, dispatchNamed); err != nil {
		return outcome, err
	}
	resolution := request.run()
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: p.Config.Product.ID,
		RepositoryID: string(p.Config.Product.RepositoryID), WorkItemID: item.ID, WorkItemTitle: item.Title,
		Backend: p.developer().Backend, ConfigRevision: p.Config.Revision(), Build: p.Build, Status: runstate.StatusPending,
		TeamBranchResolution: &resolution, TargetBranch: request.TargetBranch,
	}
	state, lease, err := p.reserveRun(ctx, state)
	var capacity runstate.CapacityError
	var existing runstate.ExistingWorkItemError
	switch {
	case errors.As(err, &capacity):
		outcome.NotStarted, outcome.Why = true, "every developer slot is in use"
		return outcome, nil
	case errors.As(err, &existing):
		// Another process reserved this very round between the look above and the
		// reservation; it is looked at again at the next check.
		outcome.Running, outcome.Why = true, "another process has just started this round"
		return outcome, nil
	case err != nil:
		return outcome, err
	}
	defer lease.Release()
	record.note(ctx, fmt.Sprintf("Yoyodyne started run %s, round %d of %d, to resolve the conflict merging %s into %s in %s, as %s decided: a developer works on %s with the conflict in front of them, and the result is checked and reviewed independently before it is merged. The run is not counted against this item's repair attempts, and this item's status is not changed.",
		runID, request.Round, runstate.MaxTeamBranchResolutionRounds, request.UpstreamBranch, request.TargetBranch, strings.Join(request.Paths, ", "), nonEmpty(strings.ReplaceAll(request.DecidedBy, "-", " "), "a person"), request.TargetBranch))
	a, err := p.teamResolutionRun(ctx, state, item)
	if err != nil {
		return outcome, err
	}
	if err := a.pinDeveloperRouting(ctx); err != nil {
		_, failErr := a.fail(err, runstate.StatusFailed)
		return p.settleTeamBranchResolution(ctx, runID, request, record, failErr)
	}
	if err := a.claim(ctx); err != nil {
		_, failErr := a.fail(err, failureStatus(ctx, err))
		return p.settleTeamBranchResolution(ctx, runID, request, record, failErr)
	}
	a.claimed = true
	// The checkout is cut from the commit that carries the conflict rather than
	// from the target, so it names no target branch of its own: this run is never
	// integrated, and the manager insists a worktree with a target branch starts
	// on that branch.
	worktree, err := a.createWorktree(ctx, gitworktree.CreateRequest{RunID: runID, WorkItemID: item.ID, BaseRef: request.ConflictedBase})
	if err != nil {
		if worktree.Path != "" {
			a.recordWorktree(worktree)
		}
		_, failErr := a.fail(stoppedBy(runstate.StopHarness, fmt.Errorf("cut the checkout for the run that resolves the conflict: %w", err)), runstate.StatusFailed)
		return p.settleTeamBranchResolution(ctx, runID, request, record, failErr)
	}
	a.recordWorktree(worktree)
	a.state.TargetBranch = request.TargetBranch
	if err := a.prepareScratch(); err != nil {
		_, failErr := a.fail(err, runstate.StatusFailed)
		return p.settleTeamBranchResolution(ctx, runID, request, record, failErr)
	}
	a.state.Status, a.state.Phase = runstate.StatusRunning, runstate.PhaseDeveloping
	a.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		_, failErr := a.fail(fmt.Errorf("save running state: %w", err), runstate.StatusFailed)
		return p.settleTeamBranchResolution(ctx, runID, request, record, failErr)
	}
	a.outcome.Status, a.outcome.Phase = a.state.Status, a.state.Phase
	// What follows is what Pipeline.Run does between cutting the worktree and
	// the gate: the project's install steps, then the developer's first attempt.
	if err := a.installBeforeDeveloper(ctx); err != nil {
		var stoppedByOperator operatorStop
		if errors.As(err, &stoppedByOperator) {
			_, stopErr := a.stop(ctx, err)
			return p.settleTeamBranchResolution(ctx, runID, request, record, stopErr)
		}
		_, failErr := a.fail(err, failureStatus(ctx, err))
		return p.settleTeamBranchResolution(ctx, runID, request, record, failErr)
	}
	if err := a.develop(ctx, withInstallSection(developerPrompt(p.developer().Persona.Text, a.deliveredInvariants().Text(), a.context, a.scratch, p.Config.Checks), p.Config.Install), ""); err != nil {
		_, stopErr := a.stop(ctx, err)
		return p.settleTeamBranchResolution(ctx, runID, request, record, stopErr)
	}
	_, err = a.verifyReviewAndFinish(ctx)
	return p.settleTeamBranchResolution(ctx, runID, request, record, err)
}

// continueTeamBranchResolution takes up a round already recorded: one that ended
// is settled, one another process holds or one parked waiting is left alone, and
// one in flight with nobody behind it is continued from its record where its
// change was committed, and otherwise ended, so that the next round starts from
// the conflict afresh.
func (p Pipeline) continueTeamBranchResolution(ctx context.Context, existing runstate.State, request TeamBranchResolutionRequest, item beads.WorkItem, record teamResolutionRecorder) (TeamBranchResolutionOutcome, error) {
	if existing.Status.Terminal() {
		return p.settleTeamBranchResolution(ctx, existing.RunID, request, record, nil)
	}
	state, lease, err := p.Store.AdoptRun(ctx, existing.RunID)
	if errors.Is(err, runstate.ErrRunHeld) {
		return TeamBranchResolutionOutcome{RunID: existing.RunID, Running: true, Why: "another process is working on this round now"}, nil
	}
	if err != nil {
		return TeamBranchResolutionOutcome{RunID: existing.RunID}, err
	}
	defer lease.Release()
	if state.TeamBranchResolution == nil {
		return TeamBranchResolutionOutcome{RunID: existing.RunID}, fmt.Errorf("run %s is not a run that resolves a team branch conflict", state.RunID)
	}
	item.Status = "in_progress"
	p.Tracker = &documentTracker{item: item}
	a, err := p.teamResolutionRun(ctx, state, item)
	if err != nil {
		return TeamBranchResolutionOutcome{RunID: existing.RunID}, err
	}
	// Nothing continues a parked run of this kind but this call, and a run whose
	// process died before its change was committed has nothing to continue. Either
	// is ended for a cause outside the work, and reported as not started, so the
	// round is not used and the next check starts it again as a new run.
	outside := ""
	switch {
	case pausedForUsageLimit(state) || pausedForOperatorHold(state) || pausedForTracker(state) || pausedForDirective(state) || stoppedProviderIsResumable(state):
		outside = "the run was paused for a cause outside the work, so it is ended and the round starts again as a new run"
	case state.TeamBranchResolution.ApprovedHead == "" && (state.HarnessCommit == "" || state.WorktreePath == ""):
		outside = "the process that was working on this round stopped before the developer's change was committed, so it is ended and the round starts again as a new run"
	}
	if outside != "" {
		if _, failErr := a.fail(stoppedBy(runstate.StopOutside, errors.New(outside)), runstate.StatusCancelled); failErr != nil {
			if saved, loadErr := p.Store.Load(state.RunID); loadErr != nil || !saved.Status.Terminal() {
				return TeamBranchResolutionOutcome{RunID: existing.RunID}, failErr
			}
		}
		record.note(ctx, fmt.Sprintf("Yoyodyne ended run %s, round %d of %d of resolving the conflict merging %s into %s: %s. The round is not counted.",
			state.RunID, request.Round, runstate.MaxTeamBranchResolutionRounds, request.UpstreamBranch, request.TargetBranch, outside))
		return TeamBranchResolutionOutcome{RunID: existing.RunID, NotStarted: true, Why: outside}, nil
	}
	a.carryReviewEvidence()
	_, err = a.verifyReviewAndFinish(ctx)
	return p.settleTeamBranchResolution(ctx, state.RunID, request, record, err)
}

// teamResolutionRun is the active run a round is carried by. It never publishes,
// so a pull request is never opened for it, and it waits for nobody's merge.
func (p Pipeline) teamResolutionRun(ctx context.Context, state runstate.State, item beads.WorkItem) (*activeRun, error) {
	invariants, err := p.loadInvariants()
	if err != nil {
		return nil, err
	}
	a := &activeRun{pipeline: p, state: state, item: item, invariants: invariants,
		outcome: Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: state.Status, Phase: state.Phase, Summary: "Resolve a conflict between the team's branch and the target branch."}}
	a.worktree = gitworktree.Worktree{RunID: state.RunID, WorkItemID: state.WorkItemID, Path: state.WorktreePath, Branch: state.Branch, BaseCommit: state.BaseCommit, HarnessCommit: state.HarnessCommit}
	a.outcome.Branch, a.outcome.WorktreePath, a.outcome.BaseCommit = state.Branch, state.WorktreePath, state.BaseCommit
	if state.WorktreePath != "" {
		// A continued run reads its item and context again, as a resumed run does.
		if err := a.claim(ctx); err != nil {
			return nil, err
		}
		a.claimed = true
		if err := a.prepareScratch(); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// endResolution is the end of a round's gate: the repair loop earns the checks
// and the independent approval, and the approved change is committed and
// recorded as the run's approved head, where an ordinary run would be promoted.
// It integrates nothing and closes nothing; what happens to the head is the
// update's.
func (a *activeRun) endResolution(ctx context.Context) (Outcome, error) {
	p := a.pipeline
	resolution := a.state.TeamBranchResolution
	head := resolution.ApprovedHead
	if head == "" {
		if err := a.repairLoop(ctx); err != nil {
			return a.stop(ctx, err)
		}
		if err := a.validateIndependentReview(); err != nil {
			return a.fail(stoppedBy(runstate.StopReview, err), runstate.StatusFailed)
		}
		if err := a.holdForDirective(); err != nil {
			return a.stop(ctx, err)
		}
		if err := a.integrationEarned(ctx); err != nil {
			return a.endPromotion(ctx, stoppedBy(runstate.StopIntegration, err))
		}
		var err error
		head, err = p.Worktrees.CommitAttempt(ctx, a.worktree, fmt.Sprintf("yoyodyne: resolve the conflict merging %s into %s\n\nRun: %s\nBase: %s\nDeveloper session: %s\nReviewer session: %s\n",
			resolution.UpstreamBranch, resolution.TargetBranch, a.state.RunID, a.outcome.BaseCommit, a.outcome.ProviderSessionID, a.outcome.ReviewSessionID))
		if err != nil {
			return a.endPromotion(ctx, stoppedBy(runstate.StopIntegration, fmt.Errorf("commit the approved resolution: %w", err)))
		}
	}
	// The approved head and the end of the run are one narrow update, so a
	// process that dies after the commit finds the change committed and not an
	// approval half recorded.
	ended, err := p.Store.Update(ctx, a.state.RunID, runstate.TeamBranchResolutionApproved{Head: head, At: p.clock().Now()})
	if err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("record the approved head of the resolution: %w", err)), runstate.StatusFailed)
	}
	a.state = ended
	a.worktree.HarnessCommit = head
	a.outcome.Status, a.outcome.Phase = a.state.Status, a.state.Phase
	return a.outcome, nil
}

// settleTeamBranchResolution reads back what a round left and says what it came
// to, with the note on the item the development manager named.
func (p Pipeline) settleTeamBranchResolution(ctx context.Context, runID string, request TeamBranchResolutionRequest, record teamResolutionRecorder, err error) (TeamBranchResolutionOutcome, error) {
	outcome := TeamBranchResolutionOutcome{RunID: runID}
	saved, loadErr := p.Store.Load(runID)
	if loadErr != nil {
		return outcome, errors.Join(err, loadErr)
	}
	if !saved.Status.Terminal() {
		outcome.Running = true
		outcome.Why = nonEmpty(oneline.Fold(runstate.StopReason(saved.RecordedStopClass(), saved.Failure), 600), "the run has not ended")
		return outcome, err
	}
	if resolution := saved.TeamBranchResolution; saved.Status == runstate.StatusSucceeded && resolution != nil && resolution.ApprovedHead != "" {
		outcome.Resolved, outcome.ApprovedHead = true, resolution.ApprovedHead
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s resolved the conflict merging %s into %s in %s, as %s decided; its checks and an independent review passed, at %s. The merged result is checked and reviewed again before it lands on %s.",
			saved.RunID, request.UpstreamBranch, request.TargetBranch, strings.Join(request.Paths, ", "), nonEmpty(strings.ReplaceAll(request.DecidedBy, "-", " "), "a person"), shortCommit(resolution.ApprovedHead), request.TargetBranch))
		return outcome, nil
	}
	stop := runstate.StopReason(saved.RecordedStopClass(), saved.Failure)
	outcome.Why = fmt.Sprintf("run %s did not resolve it: %s", saved.RunID, nonEmpty(oneline.Fold(stop, 900), "it recorded no reason"))
	record.note(ctx, fmt.Sprintf("Yoyodyne's run %s, round %d of %d, did not resolve the conflict merging %s into %s: %s.", saved.RunID, request.Round, runstate.MaxTeamBranchResolutionRounds, request.UpstreamBranch, request.TargetBranch, nonEmpty(oneline.Fold(strings.TrimSuffix(stop, "."), 900), "it recorded no reason")))
	return outcome, nil
}

// gateConflictMarkers refuses a resolution that leaves a conflict marker in a
// file that conflicted, before any configured check runs. It is handed back to
// the developer as a failed check, because the developer can answer it.
func (a *activeRun) gateConflictMarkers() error {
	resolution := a.state.TeamBranchResolution
	if resolution == nil {
		return nil
	}
	root, err := repowrite.NewRoot(a.worktree.Path)
	if err != nil {
		return stoppedBy(runstate.StopChecks, err)
	}
	read := worktreeReader(root)
	var lines []string
	for _, path := range resolution.Paths {
		content, present, err := read(path, maxConflictFileBytes)
		if err != nil {
			return stoppedBy(runstate.StopChecks, fmt.Errorf("read %s to look for conflict markers: %w", path, err))
		}
		if !present {
			continue
		}
		for number, line := range bytes.Split(content, []byte("\n")) {
			if conflictMarkerLine(line) {
				lines = append(lines, fmt.Sprintf("- %s:%d: %s", path, number+1, strings.TrimSpace(string(line))))
				if len(lines) >= 40 {
					break
				}
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	output := "The harness looked for conflict markers in the files that conflicted before the checks, and found:\n" + strings.Join(lines, "\n") + "\nResolve each conflict so the file keeps both the team's change and the target's, and remove every marker line.\n"
	result := checks.Result{
		Command:       ConflictMarkersCheck,
		Process:       execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stdout: output},
		FailureOutput: output,
	}
	a.outcome.Checks = []checks.Result{result}
	return checkFailure{result: result}
}

// conflictMarkerLine reports a line Git's conflict markers start with: seven
// of one of the three marker characters at the start of the line, followed by
// nothing or a space.
func conflictMarkerLine(line []byte) bool {
	line = bytes.TrimSuffix(line, []byte("\r"))
	for _, marker := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
		rest, found := bytes.CutPrefix(line, []byte(marker))
		if found && (len(rest) == 0 || rest[0] == ' ') {
			return true
		}
	}
	return false
}
