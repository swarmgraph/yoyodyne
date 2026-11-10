package readmodel

// The items whose change landed on the target branch and waits for the team to
// merge it into theirs (execution.upstream_branch). Every surface says them in
// the words runstate.WaitingForTeamMergeItem.Says gives, so the status line and
// the dashboard are two renderings of this one list.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// teamMergeWaits is the run index's listing of the runs waiting for the team's
// merge. Like personMergeWaits it is an optional capability of Runs, satisfied
// by *runstate.Store.
type teamMergeWaits interface {
	WaitingForTeamMerge() ([]runstate.State, error)
}

// WaitingForTeamMerge is the items waiting for the team's merge, oldest wait
// first, and why they could not be read where they could not. Runs without the
// capability read as none waiting.
func WaitingForTeamMerge(sources Sources) ([]runstate.WaitingForTeamMergeItem, string) {
	if sources.Runs == nil {
		return nil, ""
	}
	waits, ok := sources.Runs.(teamMergeWaits)
	if !ok {
		return nil, ""
	}
	states, err := waits.WaitingForTeamMerge()
	if err != nil {
		return nil, fmt.Sprintf("the items waiting for the team to merge them could not be read: %v", err)
	}
	return runstate.WaitingForTeamMergeOf(states), ""
}

// withTrackerTitles gives each item the title the tracker holds for it now,
// where the tracker was read, in place of the one its run recorded.
func withTrackerTitles(items []runstate.WaitingForTeamMergeItem, titles *WorkItemTitles) []runstate.WaitingForTeamMergeItem {
	if len(items) == 0 || !titles.Readable() {
		return items
	}
	named := make([]runstate.WaitingForTeamMergeItem, len(items))
	copy(named, items)
	for index := range named {
		if title, known := titles.Title(named[index].WorkItemID); known && strings.TrimSpace(title) != "" {
			named[index].Title = strings.TrimSpace(title)
		}
	}
	return named
}

// renderWaitingForTeamMerge is one line per waiting item, and nothing where
// none waits.
func (s Standing) renderWaitingForTeamMerge() string {
	var rendered strings.Builder
	if s.WaitingForTeamMergeProblem != "" {
		rendered.WriteString(s.WaitingForTeamMergeProblem + "\n")
	}
	for _, said := range s.WaitingForTeamMergeSays {
		rendered.WriteString(said + "\n")
	}
	return rendered.String()
}
