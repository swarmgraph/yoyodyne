package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TrackerMergeDecisions is where a decision about a merge hold is recorded. It is
// satisfied by *runstate.TrackerMergeStore.
type TrackerMergeDecisions interface {
	DecideHold(ctx context.Context, workItemID string, keep runstate.TrackerVersion, reason, by string, at time.Time) (runstate.TrackerMergeHold, error)
}

// decideKeepVersion records which of two machines' versions of a held item
// stands. Nothing is edited here: the harness's next forge check carries it out
// and ends the hold, for the reason a triage decision carries nothing out.
func (s *Session) decideKeepVersion(ctx context.Context, outcome *TrackerOutcome) {
	if s.options.TrackerMerge == nil {
		outcome.fail(errors.New("nothing was recorded: items are held for two machines' versions that cannot be merged only with team mode on, and with it off, or with that record unreadable, no forge check would carry a decision out"))
		return
	}
	id := strings.TrimSpace(outcome.Action.ID)
	keep := runstate.TrackerVersion(strings.TrimSpace(outcome.Action.Decision))
	by := fmt.Sprintf("the %s in conversation %s, turn %d", RoleTitle(s.state.Role), s.state.ConversationID, s.state.Turns)
	hold, err := s.options.TrackerMerge.DecideHold(ctx, id, keep, strings.TrimSpace(outcome.Action.Reason), by, s.options.clock().Now())
	if err != nil {
		if errors.Is(err, runstate.ErrNoTrackerMergeHold) {
			outcome.fail(fmt.Errorf("nothing was recorded: %w", err))
			return
		}
		outcome.fail(err)
		return
	}
	outcome.applied("recorded the decision to keep the %s of %s; the next forge check carries it out and ends the hold", keep.Says(), hold.Names())
}
