package orchestrator

// The run that resolves a conflict met while bringing the target branch up to
// date with the team's branch (docs/designs/integrating-to-a-local-branch.md,
// section 3, "A conflict").
//
// The harness never resolves a conflict itself. The development manager decides
// which item's run does, and this is that run. It is a run of that item in its
// own right, with its own worktree cut from the target branch's head, its own
// developer session, and its own record (runstate.State.TeamConflict), so what
// it spends is recorded against the item like any run's. It is not charged to
// the item's repair attempts: it is one of the update's three rounds
// (runstate.TeamBranchUpdate.ConflictRounds), because another contributor's work
// caused the conflict.
//
// What differs from an ordinary run is what it hands over. The harness begins
// the merge of the team's tip in the worktree and leaves it open with Git's
// conflict markers; the developer settles them; the harness commits what is
// left, which is a merge commit with the target as its first parent and the
// team's tip as its second. The run ends there. It does not check or review the
// merge, and it never promotes anything: the update takes the merge as its
// candidate and puts it through the same checks, independent review, check on
// protected paths and compare and swap as a merge it built itself
// (teambranchupdate.go), so nothing about a landing is earned twice or skipped.
// The run claims no tracker status, because the item is already integrated and
// waiting for the team's merge.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TeamBranchConflictRequest is what the update asks a run to resolve.
type TeamBranchConflictRequest struct {
	TargetBranch   string
	UpstreamBranch string
	// TeamCommit is the tip of the team's branch to merge in.
	TeamCommit string
	// Item is the work item the development manager named.
	Item runstate.TeamBranchUpdateConflictItem
	// Reason, DecidedBy and Round are from the decision, recorded on the run.
	Reason    string
	DecidedBy string
	Round     int
	// Paths are the files the harness's own merge conflicts in.
	Paths []string
}

// TeamBranchConflictResult is what the run made.
type TeamBranchConflictResult struct {
	// RunID is the run, set whenever one was reserved.
	RunID string
	// Merge is the merge commit the run made; empty where it made none.
	Merge string
	// Base is the target commit the run's merge is built on, its first parent.
	Base string
}

// teamConflictPassing is a refusal to start the run that will clear by itself:
// the operator's pause or hold, no free developer slot, a run of the item already
// in flight, an item outside the epics the operator named, or the repository not
// being ready yet. The update waits and asks
// again at the next check. Any other refusal to start is lasting, and the
// decision is dropped and told (teamBranchRun.resolve).
type teamConflictPassing struct{ error }

func (e teamConflictPassing) Unwrap() error { return e.error }

// errTeamConflictGone says the merge the run was to resolve applied cleanly
// when it was started, so there was nothing to resolve.
var errTeamConflictGone = errors.New("the merge of the team's branch no longer conflicts, so there is nothing for a run to resolve")

// teamMergeStarter is the part of the worktree manager that begins the merge.
// It is asked for by this run alone, so a manager that does not offer it costs
// every other run nothing.
type teamMergeStarter interface {
	StartMerge(ctx context.Context, worktree gitworktree.Worktree, commit, message string) ([]string, error)
}

// ResolveTeamBranchConflict runs the item's developer on the conflict and
// returns the merge commit it left. An error with a run id on the result is a
// run that was started and did not make a merge; one without is a run that was
// never started.
func (p Pipeline) ResolveTeamBranchConflict(ctx context.Context, request TeamBranchConflictRequest) (TeamBranchConflictResult, error) {
	itemID := request.Item.ID
	ctx = beads.OnBehalfOf(ctx, runTrackerCaller("", itemID))
	if err := p.validateDispatch(); err != nil {
		return TeamBranchConflictResult{}, err
	}
	starter, ok := p.Worktrees.(teamMergeStarter)
	if !ok {
		return TeamBranchConflictResult{}, errors.New("this worktree manager cannot begin the merge a run that resolves a conflict settles")
	}
	if hold, held, err := p.operatorHold(); err != nil {
		return TeamBranchConflictResult{}, teamConflictPassing{err}
	} else if held {
		return TeamBranchConflictResult{}, teamConflictPassing{fmt.Errorf("the operator has paused harness activity since %s", hold.HeldAt.Local().Format("2006-01-02 15:04 MST"))}
	}
	// The harness is choosing this item on the development manager's decision, so
	// the run records who chose it and why, and starts nothing while the
	// operator's hold on the work the harness chooses stands. Nothing is reserved
	// in that case, so the round is not spent and the next check tries again.
	p.Selection = runstate.Selection{
		By: runstate.SelectedByDevelopmentManager,
		Reason: fmt.Sprintf("%s decided that a run of this item resolves the conflict in bringing %s up to date with %s (round %d of %d for the update): %s",
			request.DecidedBy, request.TargetBranch, request.UpstreamBranch, request.Round, runstate.MaxTeamBranchConflictRounds, request.Reason),
	}
	if _, held, err := p.holdIntake(itemID); err != nil {
		return TeamBranchConflictResult{}, teamConflictPassing{err}
	} else if held {
		return TeamBranchConflictResult{}, teamConflictPassing{errors.New("the operator is holding the work the harness chooses, so no run is started until it is released")}
	}
	// Asked of the item she named, and waited out as the hold is.
	if refused := p.MayStart.refuseRun(maystart.ResolveConflict, itemID); refused != nil {
		return TeamBranchConflictResult{}, teamConflictPassing{refused}
	}
	publishing, skipped, err := p.resolvePublishing(ctx)
	if err != nil {
		return TeamBranchConflictResult{}, err
	}
	item, err := p.readWorkItem(ctx, itemID)
	if err != nil {
		return TeamBranchConflictResult{}, fmt.Errorf("load work item: %w", err)
	}
	if item.Status == "closed" {
		// An item waiting for the team's merge stays open, so a closed one has had
		// everything asked of it.
		return TeamBranchConflictResult{}, fmt.Errorf("work item %s is closed, so no run of it can resolve the conflict", itemID)
	}
	invariants, err := p.loadInvariants()
	if err != nil {
		return TeamBranchConflictResult{}, err
	}
	if err := p.Worktrees.ValidateReady(ctx); err != nil {
		return TeamBranchConflictResult{}, teamConflictPassing{fmt.Errorf("repository is not ready for an isolated run: %w",
			refusedByEnvironment("the repository is not ready for an isolated run", err))}
	}
	dispatchProvider, dispatchNamed, err := p.dispatchBackend(ctx)
	if err != nil {
		return TeamBranchConflictResult{}, err
	}
	if err := p.requireBackendReady(ctx, itemID, dispatchProvider, dispatchNamed); err != nil {
		return TeamBranchConflictResult{}, err
	}
	if err := p.requireLaunchSettings(ctx, "the run that resolves a conflict for "+itemID, dispatchProvider, dispatchNamed); err != nil {
		return TeamBranchConflictResult{}, err
	}
	runID, err := p.NewRunID()
	if err != nil {
		return TeamBranchConflictResult{}, refusedByEnvironment("a run identifier could not be generated", err)
	}
	state := runstate.State{
		SchemaVersion:  runstate.StateSchemaVersion,
		RunID:          runID,
		ProductID:      p.Config.Product.ID,
		RepositoryID:   string(p.Config.Product.RepositoryID),
		WorkItemID:     itemID,
		WorkItemTitle:  item.Title,
		WorkItemLabels: append([]string(nil), item.Labels...),
		Backend:        p.developer().Backend,
		ConfigRevision: p.Config.Revision(),
		Build:          p.Build,
		Status:         runstate.StatusPending,
		TeamConflict: &runstate.TeamConflictRun{
			TargetBranch: request.TargetBranch, UpstreamBranch: request.UpstreamBranch, TeamCommit: request.TeamCommit,
			Round: request.Round, Reason: request.Reason, DecidedBy: request.DecidedBy,
		},
	}
	state, reservation, err := p.reserveRun(ctx, state)
	if err != nil {
		var existing runstate.ExistingWorkItemError
		if errors.As(err, &existing) {
			return TeamBranchConflictResult{}, teamConflictPassing{ExistingRunError{State: existing.State}}
		}
		// No slot free, or the reservation could not be written: either clears.
		return TeamBranchConflictResult{}, teamConflictPassing{err}
	}
	defer reservation.Release()
	result := TeamBranchConflictResult{RunID: runID}
	run := &activeRun{
		pipeline:   p,
		state:      state,
		outcome:    Outcome{RunID: runID, WorkItemID: itemID, Status: runstate.StatusPending, PublishSkipped: skipped},
		item:       item,
		publishing: publishing,
		invariants: invariants,
	}
	if err := run.pinDeveloperRouting(ctx); err != nil {
		_, failure := run.fail(err, runstate.StatusFailed)
		return result, failure
	}
	// The item is not claimed: it is integrated already and waiting for the
	// team's merge, and what this run makes is not a change to land.
	bundle, err := contextbundle.Assemble(p.contextRequest(item))
	if err != nil {
		_, failure := run.fail(fmt.Errorf("assemble the work item context: %w", err), runstate.StatusFailed)
		return result, failure
	}
	run.context = bundle.Text
	worktree, err := run.createWorktree(ctx, gitworktree.CreateRequest{
		RunID: runID, WorkItemID: itemID, BaseRef: request.TargetBranch, TargetBranch: request.TargetBranch,
	})
	if err != nil {
		if worktree.Path != "" {
			run.recordWorktree(worktree)
		}
		_, failure := run.fail(fmt.Errorf("create isolated worktree: %w", err), runstate.StatusFailed)
		return result, failure
	}
	run.recordWorktree(worktree)
	result.Base = worktree.BaseCommit
	if err := run.prepareScratch(); err != nil {
		_, failure := run.fail(err, runstate.StatusFailed)
		return result, failure
	}
	// What the harness merges in is named on the worktree from here, so the
	// commits it brings in are not taken for ones somebody else made above the base.
	worktree.MergedCommit = request.TeamCommit
	run.worktree = worktree
	paths, err := starter.StartMerge(ctx, worktree, request.TeamCommit, fmt.Sprintf("yoyodyne: bring %s into %s", request.UpstreamBranch, request.TargetBranch))
	if err != nil {
		_, failure := run.fail(fmt.Errorf("begin the merge of %s in the worktree: %w", request.UpstreamBranch, err), runstate.StatusFailed)
		return result, failure
	}
	if len(paths) == 0 {
		_, failure := run.fail(errTeamConflictGone, runstate.StatusFailed)
		return result, errors.Join(errTeamConflictGone, failure)
	}
	started, err := p.Store.Update(ctx, runID, runstate.TeamConflictStarted{WorktreePath: worktree.Path, Branch: worktree.Branch, BaseCommit: worktree.BaseCommit, TargetBranch: worktree.TargetBranch, At: p.clock().Now()})
	if err != nil {
		_, failure := run.fail(fmt.Errorf("save running state: %w", err), runstate.StatusFailed)
		return result, failure
	}
	run.state = started
	run.outcome.Status, run.outcome.Phase = runstate.StatusRunning, run.state.Phase
	if err := run.installBeforeDeveloper(ctx); err != nil {
		_, failure := run.fail(err, failureStatus(ctx, err))
		return result, failure
	}
	prompt := withInstallSection(teamConflictPrompt(p.developer().Persona.Text, run.deliveredInvariants().Text(), run.context, run.scratch, p.Config.Checks, p.reviewDiffLimits(), request, worktree.BaseCommit, paths), p.Config.Install)
	if err := run.develop(ctx, prompt, ""); err != nil {
		_, failure := run.stop(ctx, err)
		return result, failure
	}
	merge := run.state.HarnessCommit
	if merge == "" || merge == worktree.BaseCommit {
		_, failure := run.fail(errors.New("the developer left nothing to commit, so the merge of the team's branch was not made"), runstate.StatusFailed)
		return result, failure
	}
	result.Merge = merge
	// The run ends here, made terminal before anything else reads it. Its branch
	// and worktree stay: the merge commit is kept by the branch until the update
	// has landed it or dropped it.
	finished, err := p.Store.Update(ctx, runID, runstate.TeamConflictMerged{Merge: merge, At: p.clock().Now()})
	if err != nil {
		return result, fmt.Errorf("save the finished run %s: %w", runID, err)
	}
	run.state = finished
	return result, nil
}

// teamConflictPrompt is what the developer is told: the open merge, the files
// it conflicts in, and what to leave.
func teamConflictPrompt(persona, invariants, bundle, scratchDirectory string, checks []string, limits gitworktree.DiffLimits, request TeamBranchConflictRequest, base string, paths []string) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks, limits))
	prompt.WriteString("\n\n")
	prompt.WriteString(personaSection(persona))
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString(bundle)
	prompt.WriteString("\n# Resolve a conflict with the team's branch\n\n")
	fmt.Fprintf(&prompt, "The team merged work into `%s`, and bringing that work into `%s` conflicts with what has landed on `%s` since. The development manager chose you to resolve it, because your work item `%s` is one of the changes on `%s` that touched these files. This is round %d of %d for this update; it is not counted against your repair attempts.\n\n",
		request.UpstreamBranch, request.TargetBranch, request.TargetBranch, request.Item.ID, request.TargetBranch, request.Round, runstate.MaxTeamBranchConflictRounds)
	fmt.Fprintf(&prompt, "The harness has begun the merge of `%s` at `%s` into `%s` at `%s` in your worktree and left it open. Everything that merged cleanly is already in place. These files are in conflict, between Git's conflict markers:\n\n", request.UpstreamBranch, request.TeamCommit, request.TargetBranch, base)
	for _, path := range paths {
		prompt.WriteString("- " + path + "\n")
	}
	if request.Reason != "" {
		fmt.Fprintf(&prompt, "\nThe development manager's reason for choosing a run of this item: %s\n", request.Reason)
	}
	prompt.WriteString("\nEdit each conflicting file so it says what both sides meant, keeping the team's change and the target's, and leave no conflict markers behind. `git status` lists every file still in conflict, and both histories are in this repository. Make no other change: the merge is checked and reviewed as it is, and an unrelated edit in it is refused. Do not run `git commit`, `git merge --continue`, or `git merge --abort`, and do not commit: the harness commits the resolved merge itself, and refuses one it did not make.\n")
	prompt.WriteString("\nIf the two sides cannot both stand, say so plainly in your summary rather than forcing a resolution you do not believe in.\n")
	return prompt.String()
}

// resolve carries out the development manager's decision on a conflict: it
// starts the run of the item she named, and where the run makes a merge, takes
// that merge as the update's candidate and works the update on from there. The
// run is written down as started before it is, so a record that has a start and
// no merge says the process died with the run, which is a round spent.
func (r *teamBranchRun) resolve(ctx context.Context) error {
	pending, d := r.pending(), r.record.Decision
	if d.Started() {
		r.record.GiveUpConflictRun()
		return r.resolutionFailed(fmt.Sprintf("the run of %s that was to resolve the conflict (round %d of %d) did not finish: the process running it ended first", d.Item.Name(), d.Round, runstate.MaxTeamBranchConflictRounds))
	}
	started := r.now()
	d.StartedAt = &started
	if err := r.save(); err != nil {
		return err
	}
	result, err := r.p.ResolveTeamBranchConflict(ctx, TeamBranchConflictRequest{
		TargetBranch: r.target, UpstreamBranch: r.upstream, TeamCommit: pending.TeamCommit,
		Item: d.Item, Reason: d.Reason, DecidedBy: d.DecidedBy, Round: d.Round, Paths: pending.Conflict.Paths,
	})
	d.RunID = result.RunID
	switch {
	case errors.Is(err, errTeamConflictGone):
		// Nothing was resolved and nothing was spent: the merge goes in cleanly
		// and the next build makes it.
		d.StartedAt = nil
		r.record.RefundUnstartedRound()
		r.record.Pending = nil
		r.wait("the merge of %s no longer conflicts, so it is built again at the next check", r.upstream)
		return r.save()
	case err != nil && ctx.Err() != nil:
		return err
	case err != nil && result.RunID == "":
		// No run was started: the harness is paused, no developer slot was free,
		// or the item could not be run. The round is not spent, and the next check
		// tries again.
		var passing teamConflictPassing
		if errors.As(err, &passing) {
			d.StartedAt = nil
			r.wait("a run of %s could not be started yet: %v; it is tried again at the next check", d.Item.Name(), err)
			return r.save()
		}
		// A refusal that will not clear by itself, such as an item that has since
		// closed: the decision is dropped with its round given back, and the
		// development manager is told, so she can decide again.
		item, round := d.Item.Name(), d.Round
		d.StartedAt = nil
		r.record.RefundUnstartedRound()
		return r.resolutionFailed(fmt.Sprintf("the run of %s that was to resolve the conflict (round %d of %d) could not be started, and the round was not spent: %v", item, round, runstate.MaxTeamBranchConflictRounds, err))
	case err != nil:
		r.record.GiveUpConflictRun()
		return r.resolutionFailed(fmt.Sprintf("the run %s of %s that was to resolve the conflict (round %d of %d) ended without a merge: %v", result.RunID, d.Item.Name(), d.Round, runstate.MaxTeamBranchConflictRounds, err))
	}
	parents, err := r.u.Git.CommitParents(ctx, result.Merge)
	if err != nil {
		return fmt.Errorf("read the parents of the merge %s that run %s made: %w", result.Merge, result.RunID, err)
	}
	if len(parents) != 2 || parents[1] != pending.TeamCommit {
		r.record.GiveUpConflictRun()
		return r.resolutionFailed(fmt.Sprintf("run %s of %s (round %d of %d) left commit %s, which is not a merge of %s at %s (its parents are %s)", result.RunID, d.Item.Name(), d.Round, runstate.MaxTeamBranchConflictRounds, shortCommit(result.Merge), r.upstream, shortCommit(pending.TeamCommit), shortCommits(parents)))
	}
	// The merge is the update's candidate from here. It was built on the target
	// as the run found it, which is its first parent.
	d.Merge = result.Merge
	pending.Candidate, pending.TargetBase = result.Merge, parents[0]
	pending.ResolvedBy, pending.ResolvedItem, pending.ResolvedItemTitle = result.RunID, d.Item.ID, d.Item.Title
	pending.ResolvedPaths = append([]string(nil), pending.Conflict.Paths...)
	pending.Conflict, pending.Refused, pending.RefusedAt = nil, "", nil
	pending.Checks, pending.ChecksFinishedAt, pending.Review = nil, nil, nil
	if err := r.save(); err != nil {
		return err
	}
	return r.work(ctx)
}

// resolutionFailed says a run did not make the merge, and tells the development
// manager once, so she can decide again while rounds are left. The conflict
// stays waiting.
func (r *teamBranchRun) resolutionFailed(reason string) error {
	if err := r.save(); err != nil {
		return err
	}
	r.wait("%s; the conflict waits for the development manager's next decision", reason)
	left := runstate.MaxTeamBranchConflictRounds - r.record.ConflictRounds
	next := "No round is left for this update, so the conflict stays on your list for a person to settle."
	if left > 0 {
		next = fmt.Sprintf("%d of the update's %d rounds are left; you can decide again.", left, runstate.MaxTeamBranchConflictRounds)
	}
	return r.tell(fmt.Sprintf("Bringing %s up to date with %s is still waiting on its conflict: %s. %s Nothing was landed, and %s keeps taking new work.",
		r.target, r.upstream, strings.TrimRight(strings.TrimSpace(reason), "."), next, r.target))
}
