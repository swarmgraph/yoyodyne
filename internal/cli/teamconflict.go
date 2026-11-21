package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// conversationTeamConflicts wires the development manager's decision that a run
// of one item resolves a conflict in bringing the target branch up to date with
// the team's branch into her conversation and no other.
func conversationTeamConflicts(parts components, role domain.AgentRole) chat.TeamConflicts {
	if role != domain.RoleDevelopmentManager || parts.store == nil || parts.worktrees == nil || strings.TrimSpace(parts.config.Execution.UpstreamBranch) == "" {
		return nil
	}
	return conversationTeamConflict{parts: parts}
}

type conversationTeamConflict struct {
	parts components
}

// DecideConflict records the decision on the target branch's update, after
// checking the item named is one waiting for the team's merge on that branch,
// and says what was recorded. The harness starts the run at its next forge
// check, which is what the answer says.
func (c conversationTeamConflict) DecideConflict(ctx context.Context, decision chat.TeamConflictDecision) (string, error) {
	target, err := c.parts.worktrees.CurrentBranch(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve the branch work lands on: %w", err)
	}
	waiting, err := c.parts.store.WaitingForTeamMerge()
	if err != nil {
		return "", fmt.Errorf("list the items waiting for the team's merge: %w", err)
	}
	var named *runstate.TeamBranchUpdateConflictItem
	var others []string
	for _, state := range waiting {
		if state.TeamMerge == nil || state.TeamMerge.TargetBranch != target {
			continue
		}
		item := runstate.TeamBranchUpdateConflictItem{ID: state.WorkItemID, Title: state.WorkItemTitle}
		if state.WorkItemID == decision.ItemID {
			named = &item
			break
		}
		others = append(others, item.Name())
	}
	if named == nil {
		sort.Strings(others)
		if len(others) > 10 {
			others = append(others[:10], fmt.Sprintf("and %d more", len(others)-10))
		}
		listed := "none is waiting for the team's merge on " + target
		if len(others) > 0 {
			listed = "the items waiting for the team's merge on " + target + " are " + strings.Join(others, "; ")
		}
		return "", fmt.Errorf("%s is not waiting for the team's merge on %s, so no run of it was decided on; %s", decision.ItemID, target, listed)
	}
	records, err := runstate.NewTeamBranchUpdateStore(c.parts.stateRoot, c.parts.config.Product.ID)
	if err != nil {
		return "", err
	}
	update, err := records.DecideConflict(ctx, target, runstate.TeamBranchConflictDecision{
		Item: *named, Reason: decision.Reason, DecidedBy: decision.By, DecidedAt: time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}
	if update.Decision == nil {
		return "", errors.New("the decision was not recorded on the update")
	}
	return fmt.Sprintf("recorded your decision that a run of %s resolves the conflict in bringing %s up to date with %s: round %d of %d for this update, not charged to the item's repair attempts. The harness starts the run at its next forge check, and the merge it makes then goes through the checks and independent review like any update",
		named.Name(), update.TargetBranch, update.UpstreamBranch, update.Decision.Round, runstate.MaxTeamBranchConflictRounds), nil
}
