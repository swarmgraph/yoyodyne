package chat

// The development manager deciding that a run of one item resolves a conflict
// met while the target branch is brought up to date with the team's branch
// (docs/designs/integrating-to-a-local-branch.md, section 3).
//
// Deciding records the decision and counts the round on the update's own
// record, and the harness starts the run at the forge check that follows. The
// round limit and the refusals are the update's (runstate.TeamBranchUpdate.
// DecideConflict), so what she is told when a fourth round is refused is the
// record's own refusal, in plain words.

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// TeamConflicts records a decision on the conflict the target branch's update
// is waiting on, and answers what was recorded in the words her reply carries.
type TeamConflicts interface {
	DecideConflict(ctx context.Context, decision TeamConflictDecision) (string, error)
}

// TeamConflictDecision is one decision as the harness asks the update to record it.
type TeamConflictDecision struct {
	ItemID string
	Reason string
	// By is who decided it, written by the conversation from its own facts rather
	// than taken from the reply, so it cannot name anybody else.
	By string
}

var errNoTeamConflicts = errors.New("this conversation cannot reach the update of the target branch, so no decision was recorded; the development manager's conversation is the one that can")

// decideTeamConflict records the decision and reports what became of it.
func (s *Session) decideTeamConflict(ctx context.Context, outcome *TrackerOutcome) {
	if s.options.TeamConflicts == nil {
		outcome.fail(errNoTeamConflicts)
		return
	}
	action := outcome.Action
	said, err := s.options.TeamConflicts.DecideConflict(ctx, TeamConflictDecision{
		ItemID: strings.TrimSpace(action.ID),
		Reason: strings.TrimSpace(action.Reason),
		By:     fmt.Sprintf("the %s in conversation %s, turn %d", RoleTitle(s.state.Role), s.state.ConversationID, s.state.Turns),
	})
	if err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("%s", said)
}
