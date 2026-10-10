package contextbundle

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// ConversationWorkSection renders the read model's ordered queue for a recurring
// turn. Its listing is bounded like the opening briefing's work-item listing.
func ConversationWorkSection(items []beads.WorkItem, problem string) string {
	var rendered strings.Builder
	rendered.WriteString("## Work waiting in your conversation\n\n")
	if problem != "" {
		fmt.Fprintf(&rendered, "The work waiting on you could not be read: %s. Do not assume nothing is waiting.\n", problem)
		return rendered.String()
	}
	rendered.WriteString("Read for this turn, in backlog order: highest priority first (P0 before P1), then oldest admitted within each priority. Parked items are excluded. Read each item in full before deciding.\n\n")
	if len(items) == 0 {
		rendered.WriteString("Nothing is waiting in your conversation.\n")
		return rendered.String()
	}
	listed := ConversationWorkListed(items)
	for _, item := range listed {
		fmt.Fprintf(&rendered, "- %s (%s) [P%d, %s]\n", singleLine(item.Title, maxWorkItemTitleBytes), item.ID, item.Priority, item.Status)
	}
	if len(items) > len(listed) {
		fmt.Fprintf(&rendered, "\n%d further work item(s) are not listed here.\n", len(items)-len(listed))
	}
	return rendered.String()
}

// ConversationWorkListed is the part of the queue the section names, in the
// order it names it. A recurring pass records these as what it was handed, so
// the record and the section cannot disagree about where the listing stopped.
func ConversationWorkListed(items []beads.WorkItem) []beads.WorkItem {
	if len(items) > maxProductWorkItems {
		return items[:maxProductWorkItems]
	}
	return items
}
