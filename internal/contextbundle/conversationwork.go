package contextbundle

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ConversationWorkSection renders the read model's ordered queue for a recurring
// turn of the role's pass. Its listing is bounded like the opening briefing's
// work-item listing.
func ConversationWorkSection(role domain.AgentRole, items []beads.WorkItem, problem string) string {
	var rendered strings.Builder
	rendered.WriteString("## Work waiting in your conversation\n\n")
	if problem != "" {
		fmt.Fprintf(&rendered, "The work waiting on you could not be read: %s. Do not assume nothing is waiting.\n", problem)
		return rendered.String()
	}
	rendered.WriteString("Read for this turn, in backlog order: highest priority first (P0 before P1), then, within each priority, the items the Lead Product Manager placed first (earliest placement first, each saying so), then oldest admitted. Parked items are excluded. Ahead of all of it come any items the Lead Product Manager named first for this pass, each saying so, whatever their priority and whoever carries the rest of their work: take those first. Read each item in full before deciding.\n\n")
	if len(items) == 0 {
		rendered.WriteString("Nothing is waiting in your conversation.\n")
		return rendered.String()
	}
	listed := ConversationWorkListed(items)
	for _, item := range listed {
		fmt.Fprintf(&rendered, "- %s (%s) [P%d, %s]\n", singleLine(item.Title, maxWorkItemTitleBytes), item.ID, item.Priority, item.Status)
		if named := item.FirstInPassOf(role); named != nil {
			fmt.Fprintf(&rendered, "    TAKE FIRST: %s\n", named.Line())
		}
		if placement := item.PlacedFirst(); placement != nil {
			fmt.Fprintf(&rendered, "    %s\n", placement.Line())
		}
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

// ConversationWorkNamed is what a pass is told before anything else it
// carries — the triage docket included — where the Lead Product Manager named
// items for it: each item, and the step and its reason in her words. It is
// empty where she named none. The same items lead the queue section, so the
// pass is judged against an order that starts with them.
func ConversationWorkNamed(role domain.AgentRole, items []beads.WorkItem) string {
	var lines []string
	for _, item := range ConversationWorkListed(items) {
		if named := item.FirstInPassOf(role); named != nil {
			lines = append(lines, fmt.Sprintf("- %s (%s): %s", singleLine(item.Title, maxWorkItemTitleBytes), item.ID, named.Line()))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Take these first\n\nThe Lead Product Manager named these items for this pass to take before anything else in it, the docket and the rest of your order included. Take each first, or say in \"left\" why you could not; an item you leave stays named for your next pass, and `yoyo status` shows it as not taken with your reason.\n\n" +
		strings.Join(lines, "\n") + "\n"
}
