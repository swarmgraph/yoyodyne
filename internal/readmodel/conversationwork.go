package readmodel

import (
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ConversationWork is the live work carried by one role's conversation, in the
// same priority and admission-time order as a developer pull. Parked work waits
// for release rather than being put to the role on every pass.
//
// Ahead of all of it come the items the Lead Product Manager named for this
// role's next pass (beads.PassFirst), earliest named first, whatever carries
// their own execution and whatever their priority or parking: a step the role
// owes on a developer's item is the case they exist for, and naming one is her
// deciding it cannot wait behind the order.
func ConversationWork(items []beads.WorkItem, role domain.AgentRole) []beads.WorkItem {
	var named, waiting []beads.WorkItem
	for _, item := range items {
		switch item.Status {
		case "open", "blocked", "in_progress":
		default:
			continue
		}
		if item.FirstInPassOf(role) != nil {
			named = append(named, item)
			continue
		}
		if item.Executor != domain.ConversationWith(role) || item.Parking.Parked() {
			continue
		}
		waiting = append(waiting, item)
	}
	sort.SliceStable(named, func(i, j int) bool {
		return named[i].PassFirst.At.Before(named[j].PassFirst.At)
	})
	backlog.Sort(waiting)
	return append(named, waiting...)
}
