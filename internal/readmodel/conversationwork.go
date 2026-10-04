package readmodel

import (
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ConversationWork is the live work carried by one role's conversation, in the
// same priority and admission-time order as a developer pull. Parked work waits
// for release rather than being put to the role on every pass.
func ConversationWork(items []beads.WorkItem, role domain.AgentRole) []beads.WorkItem {
	var waiting []beads.WorkItem
	for _, item := range items {
		if item.Executor != domain.ConversationWith(role) || item.Parking.Parked() {
			continue
		}
		switch item.Status {
		case "open", "blocked", "in_progress":
			waiting = append(waiting, item)
		}
	}
	backlog.Sort(waiting)
	return waiting
}
