package orchestrator

// An item the Lead Product Manager named for one role's next pass to take
// first (beads.PassFirst) leads what that pass is handed and is said above
// everything else its turns carry (recurring.go). This is how the request
// ends: once a pass of that role answered with its account and took the item,
// the harness ends the request and notes on the item which pass took it. A
// pass that left the item ends nothing, so the next pass is handed it first
// again; the pass record names it as not taken with the role's reason, which
// is what `yoyo status` shows (readmodel/conversationpasses.go), and the
// passed-over note says it on the item.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RecurringFirstEnder is the write a RecurringItemNotes may also offer: end
// the request that put an item first in the role's next pass, with a note
// saying what ended it. It ends only a request to that role that still
// stands, so a request made again since, to this role or another, is kept.
type RecurringFirstEnder interface {
	EndFirstInPass(ctx context.Context, id string, role domain.AgentRole, note string) error
}

// endTakenFirst ends the request on every named item the pass took, and
// returns what the pass's record should say where one could not be ended.
func (t Trigger) endTakenFirst(ctx context.Context, pass string, role domain.AgentRole, recorded runstate.Sweep) string {
	if recorded.Turns == 0 || recorded.Result == nil {
		return ""
	}
	ender, ok := t.ItemNotes.(RecurringFirstEnder)
	if !ok {
		return ""
	}
	write, stopWriting := trackerRecordContext(ctx)
	defer stopWriting()
	var failed []string
	for _, item := range recorded.Delivered {
		if item.NamedFirst == "" || !item.Taken {
			continue
		}
		note := fmt.Sprintf("Taken first by the %s's pass %s, as it had been %s. The request ended with it.", role.Title(), pass, item.NamedFirst)
		if err := ender.EndFirstInPass(write, item.ID, role, note); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", item.ID, err))
		}
	}
	if len(failed) == 0 {
		return ""
	}
	return fmt.Sprintf("the pass took items it was handed first, and the request putting them first could not be ended, so its next pass is handed them first again: %s", strings.Join(failed, "; "))
}
