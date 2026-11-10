package orchestrator

// Dependencies on work that waits for the team's merge.
//
// An item whose change landed on the target branch and waits for the team to
// merge it into theirs (runstate.TeamMerge) stays claimed, so the tracker still
// reports it unfinished and holds back whatever depends on it. For an item that
// lands on the same target branch, though, the dependency is met: the work it
// needs is already on the branch this item is cut from. These helpers lift
// such a dependency from the readings that decide whether an item may start —
// the dispatch (Pipeline.Run and its continuations) and the scheduler's queue
// (Pull.queue) — and from nothing else: the tracker is not told, and the
// dependency stays on the item until the waiting one closes.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TeamMergeWaitingOn returns the work items whose change landed on
// targetBranch and waits for the team's merge. An empty targetBranch returns
// the items waiting on any target branch.
func TeamMergeWaitingOn(waits TeamMergeWaits, targetBranch string) (map[string]bool, error) {
	waiting, err := waits.WaitingForTeamMerge()
	if err != nil {
		return nil, err
	}
	return teamMergeWaitingOf(waiting, targetBranch), nil
}

func teamMergeWaitingOf(waiting []runstate.State, targetBranch string) map[string]bool {
	met := map[string]bool{}
	for _, state := range waiting {
		if !state.WaitingForTeamMerge() {
			continue
		}
		if targetBranch != "" && state.TeamMerge.TargetBranch != targetBranch {
			continue
		}
		met[state.WorkItemID] = true
	}
	return met
}

// withoutMetDependencies is the item with the blocks-dependencies in met
// removed, on a copy: the item the caller holds is not changed.
func withoutMetDependencies(item beads.WorkItem, met map[string]bool) beads.WorkItem {
	if len(met) == 0 {
		return item
	}
	kept := make([]beads.Dependency, 0, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		if dependency.Type == blocksDependency && met[dependency.ID] {
			continue
		}
		kept = append(kept, dependency)
	}
	item.Dependencies = kept
	return item
}

// releaseTeamMerged is the item with its dependencies on work waiting for the
// team's merge on the run's target branch removed, which is what the dispatch's
// dependency checks read. targetBranch is the branch the run lands on; empty
// resolves the current branch, as Run does. It does nothing unless the project
// sets execution.upstream_branch, the item has an unclosed blocks-dependency
// and the run store can list the waits; where the branch or the waits cannot be
// read the item is returned as it is, so a dependency is never lifted on a
// guess.
func (p Pipeline) releaseTeamMerged(ctx context.Context, item beads.WorkItem, targetBranch string) beads.WorkItem {
	if strings.TrimSpace(p.Config.Execution.UpstreamBranch) == "" || len(blockingDependencies(item)) == 0 {
		return item
	}
	waits, ok := p.Store.(TeamMergeWaits)
	if !ok {
		return item
	}
	if targetBranch == "" {
		if p.Worktrees == nil {
			return item
		}
		current, err := p.Worktrees.CurrentBranch(ctx)
		if err != nil {
			return item
		}
		targetBranch = current
	}
	met, err := TeamMergeWaitingOn(waits, targetBranch)
	if err != nil {
		return item
	}
	return withoutMetDependencies(item, met)
}

// teamMergeMet is the work items whose change waits for the team's merge on
// this pull's target branch, which count as met for what depends on them.
// A pull wired without TeamMerges has none.
func (p Pull) teamMergeMet(ctx context.Context) (map[string]bool, error) {
	if p.TeamMerges == nil {
		return nil, nil
	}
	target := ""
	if p.TargetBranch != nil {
		var err error
		if target, err = p.TargetBranch(ctx); err != nil {
			return nil, fmt.Errorf("resolve the branch work lands on, to tell which dependencies wait only for the team's merge: %w", err)
		}
	}
	met, err := TeamMergeWaitingOn(p.TeamMerges, target)
	if err != nil {
		return nil, fmt.Errorf("list the runs waiting for the team to merge their change: %w", err)
	}
	return met, nil
}

// withTeamMergeDependents adds to the ids the tracker reports ready every
// admitted item it does not, whose every unclosed blocks-dependency waits for
// the team's merge on this pull's target branch. An item with no unclosed
// dependency that the tracker did not report ready is unready for another
// reason and is not added.
//
// An item waiting for the team's merge is itself never offered again: it stays
// in_progress, and the queue lists only the statuses in scheduledStatuses.
func (p Pull) withTeamMergeDependents(ctx context.Context, pullable []string, admitted []beads.WorkItem) ([]string, error) {
	met, err := p.teamMergeMet(ctx)
	if err != nil || len(met) == 0 {
		return pullable, err
	}
	reported := make(map[string]bool, len(pullable))
	for _, id := range pullable {
		reported[id] = true
	}
	for _, item := range admitted {
		if reported[item.ID] {
			continue
		}
		unclosed := 0
		for _, dependency := range item.Dependencies {
			if dependency.Type == blocksDependency && dependency.Status != closedStatus {
				unclosed++
			}
		}
		if unclosed == 0 || len(blockingDependencies(withoutMetDependencies(item, met))) > 0 {
			continue
		}
		reported[item.ID] = true
		pullable = append(pullable, item.ID)
	}
	return pullable, nil
}

// releaseTeamMergedFrom is the item with its dependencies on work waiting for
// the team's merge removed, for the paths that continue, resume or re-run a
// stopped run and have no Pipeline to ask. The run store is the evidence: it
// lists the waits, and only a project that sets execution.upstream_branch ever
// records one, so no setting is read here. targetBranch is the stopped run's
// recorded target; empty counts a wait on any target, as Pull does without a
// target. A store that cannot list waits, or fails to, leaves the item as it
// was.
func releaseTeamMergedFrom(runs any, item beads.WorkItem, targetBranch string) beads.WorkItem {
	waits, ok := runs.(TeamMergeWaits)
	if !ok || len(blockingDependencies(item)) == 0 {
		return item
	}
	met, err := TeamMergeWaitingOn(waits, targetBranch)
	if err != nil {
		return item
	}
	return withoutMetDependencies(item, met)
}

// BlockingDependenciesAfterTeamMerges is BlockingDependencies without the work
// waiting for the team's merge, read from the run store given (any target
// branch), for a caller outside the package that decides whether a run may be
// recovered.
func BlockingDependenciesAfterTeamMerges(runs any, item beads.WorkItem) []string {
	return blockingDependencies(releaseTeamMergedFrom(runs, item, ""))
}
