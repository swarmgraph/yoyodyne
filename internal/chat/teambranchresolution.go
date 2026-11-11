package chat

// The development manager deciding which item's run resolves a conflict between
// the target branch and the team's branch.
//
// With execution.upstream_branch set, the harness keeps the target branch up to
// date with the team's branch. When merging the team's branch into the target
// conflicts, the update waits and a report on her list says so
// (internal/orchestrator/teambranchupdate.go). Her decision names one work
// item, and the harness starts a run of that item at its next forge check with
// the conflict in front of the developer, which resolves it on the target
// branch. Recording the decision is the whole of her part: the store refuses it
// in plain words where it cannot stand (no conflict is waiting, the rounds are
// spent, a round is running), and that refusal is this action's refusal.
//
// Nothing here starts a run or touches a branch. Like a triage decision, this
// is a role deciding, and what carries it out is the harness's own hand.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// actionResolveConflict records which item's run resolves the conflict the
// update of a target branch is waiting on. It names an item, and a target branch
// where the project has more than one waiting.
const actionResolveConflict = "resolve-conflict"

// maxResolutionPathsNamed bounds how many conflicting files the note on the item
// names; the rest are counted.
const maxResolutionPathsNamed = 10

// maxTargetBranchBytes bounds the target branch an action names.
const maxTargetBranchBytes = 255

// TeamBranchResolutions is where the development manager's decision about a
// conflict between the target branch and the team's branch is recorded. It is
// present only where the project names a team's branch
// (execution.upstream_branch) and only in her conversation; a conversation
// without one refuses the action rather than appearing to have recorded it.
type TeamBranchResolutions interface {
	// DecideResolution records the decision on the update waiting on a conflict
	// and returns the record as it now stands. It refuses in plain words where
	// the decision cannot stand.
	DecideResolution(ctx context.Context, decision runstate.TeamBranchResolutionDecision) (runstate.TeamBranchUpdate, error)
	// All reports every target branch's record, which is how an action that names
	// no target finds the one waiting.
	All() ([]runstate.TeamBranchUpdate, error)
}

// resolveConflictProblems checks the action as far as it can be checked alone.
func (a TrackerAction) resolveConflictProblems() []error {
	target := strings.TrimSpace(a.Target)
	switch {
	case target == "":
		return nil
	case len(target) > maxTargetBranchBytes:
		return []error{fmt.Errorf("resolve-conflict target is %d bytes, limit is %d", len(target), maxTargetBranchBytes)}
	case strings.ContainsAny(target, " \t\r\n"):
		return []error{errors.New("resolve-conflict target is a branch name and has no spaces in it; leave it out where the project has one target branch")}
	}
	return nil
}

// targetBranchFor is the target branch the decision is about: the one the action
// names, or the one branch with a conflict waiting where it names none.
func (s *Session) targetBranchFor(named string) (string, error) {
	if named = strings.TrimSpace(named); named != "" {
		return named, nil
	}
	updates, err := s.options.TeamBranchResolutions.All()
	if err != nil {
		return "", fmt.Errorf("the records of bringing the target branch up to date could not be read, so nothing was recorded: %w", err)
	}
	var waiting []string
	for _, update := range updates {
		if update.Pending != nil && update.Pending.Conflict != nil {
			waiting = append(waiting, update.TargetBranch)
		}
	}
	switch {
	case len(waiting) == 1:
		return waiting[0], nil
	case len(waiting) > 1:
		slices.Sort(waiting)
		return "", fmt.Errorf("more than one target branch is waiting on a conflict (%s); name the one this decision is about in \"target\"", strings.Join(waiting, ", "))
	case len(updates) == 1:
		return updates[0].TargetBranch, nil
	}
	return "", errors.New("no target branch is waiting on a conflict with the team's branch, so there is nothing to resolve; nothing was recorded")
}

// carryOutResolveConflict records the decision on the update's record and a note
// on the item.
func (s *Session) carryOutResolveConflict(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	id := strings.TrimSpace(action.ID)
	if s.options.TeamBranchResolutions == nil {
		outcome.refused(errors.New("this project names no team's branch (execution.upstream_branch is not set), so there is no conflict with it to resolve; nothing was recorded"))
		return
	}
	item := outcome.target
	if item == nil {
		read, err := s.options.Tracker.Show(ctx, id)
		if err != nil {
			outcome.refused(fmt.Errorf("the tracker does not hold %s, or it could not be read, so no run of it can resolve the conflict; nothing was recorded: %w", id, err))
			return
		}
		item = &read
	}
	target, err := s.targetBranchFor(action.Target)
	if err != nil {
		outcome.refused(err)
		return
	}
	update, err := s.options.TeamBranchResolutions.DecideResolution(ctx, runstate.TeamBranchResolutionDecision{
		TargetBranch:  target,
		WorkItemID:    id,
		WorkItemTitle: item.Title,
		Reason:        action.Reason,
		DecidedBy:     "development-manager",
		Conversation:  s.state.ConversationID,
		Turn:          s.state.Turns,
		At:            s.options.clock().Now(),
	})
	if err != nil {
		outcome.refused(err)
		return
	}
	upstream, paths := update.UpstreamBranch, conflictedPaths(update)
	what := fmt.Sprintf("Decided that a run of this item resolves the conflict merging %s into %s in %s", upstream, update.TargetBranch, paths)
	note := s.trackerProvenance(what, singleLine(action.Reason, maxTrackerFailureBytes))
	if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, id, note, "the decision recorded on the item")
		return
	}
	outcome.applied("decided that a run of %s resolves the conflict merging %s into %s in %s; the harness starts it at the next forge check, with the conflict in front of the developer, for up to %d rounds",
		runstate.TeamBranchConflictItem{ID: id, Title: strings.TrimSpace(item.Title)}.Says(), upstream, update.TargetBranch, paths, runstate.MaxTeamBranchResolutionRounds)
}

// conflictedPaths names the files the conflict is in, bounded.
func conflictedPaths(update runstate.TeamBranchUpdate) string {
	var paths []string
	if update.Pending != nil {
		switch {
		case update.Pending.Conflict != nil:
			paths = update.Pending.Conflict.Paths
		case update.Pending.Resolution != nil:
			paths = update.Pending.Resolution.Paths
		}
	}
	if len(paths) == 0 {
		return "the files it names"
	}
	if len(paths) <= maxResolutionPathsNamed {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:maxResolutionPathsNamed], ", "), len(paths)-maxResolutionPathsNamed)
}
