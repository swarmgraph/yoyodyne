package chat

// A block an item is under through the items it was broken out of.
//
// The tracker holds every item under a waiting parent back with it, and nothing
// on the child's own links says so: a read of the child showed its own links and
// nothing holding it, and roles read it as ready while the tracker offered it to
// nobody. So a read walks the item's parents as the tracker holds them now and
// says, after the item, which of them waits and on what. It reports a state the
// tracker already holds and changes nothing about readiness; where every parent
// that waits is waiting on the item itself, it says the scheduler offers the
// item anyway (beads.AwaitedByWhatHoldsIt).

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// renderInheritedBlocks is the inherited-block line of one item's read, and
// empty for an item no parent of which waits on anything. An ancestor that
// could not be read is said as unread rather than left out, because silence
// here is exactly the reading that cost the time this exists to save.
func (s *Session) renderInheritedBlocks(ctx context.Context, item beads.WorkItem) string {
	if s.options.Tracker == nil || item.DecomposedFrom() == "" {
		return ""
	}
	blocks, err := beads.InheritedBlocks(item,
		func(id string) (beads.WorkItem, bool, error) {
			ancestor, err := s.options.Tracker.Show(ctx, id)
			if err != nil {
				return beads.WorkItem{}, false, err
			}
			// A parent that is closed has left the backlog and holds nothing back.
			return ancestor, ancestor.Status != "closed", nil
		},
		// A show carries each dependency's real status, so a link is a wait where
		// that status says the work is unfinished.
		func(w beads.WorkItem) []string { return w.WaitingOn(nil) })
	described := beads.DescribeInheritedBlocks(blocks)
	if err != nil {
		unread := fmt.Sprintf("whether an item it was broken out of holds it back could not be read in full: %s",
			singleLine(err.Error(), maxTrackerFailureBytes))
		if described == "" {
			return fmt.Sprintf("\ninherited block: %s\n", unread)
		}
		return fmt.Sprintf("\ninherited block: %s; and %s\n", described, unread)
	}
	if described == "" {
		return ""
	}
	if beads.AwaitedByWhatHoldsIt(item, blocks) {
		waits := make([]string, 0, len(blocks))
		for _, block := range blocks {
			waits = append(waits, block.Describe())
		}
		return fmt.Sprintf("\ninherited block: %s. The tracker holds it back with that parent, but the parent is waiting on this item itself, "+
			"so the scheduler offers it all the same and nothing holds it.\n", strings.Join(waits, "; and "))
	}
	return fmt.Sprintf("\ninherited block: %s.\n", described)
}
