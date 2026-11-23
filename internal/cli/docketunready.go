package cli

import (
	"context"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readiness"
)

// unreadyStatuses are the tracker statuses of work still in the backlog, which
// is what an item held for its text has to be for the hold to mean anything.
var unreadyStatuses = []string{"open", "in_progress", "blocked"}

// unreadyRereadTimeout bounds the one tracker listing a docket build makes to
// read its unready entries again. A listing that does not answer in time
// settles nothing, and the next pull reads the items anyway.
const unreadyRereadTimeout = 2 * time.Minute

// statusLister is a tracker that lists several statuses in one command.
// beads.Client is one.
type statusLister interface {
	ListStatuses(ctx context.Context, statuses []string) ([]beads.WorkItem, error)
}

// docketUnready reads the items the docket holds as unready again, as the
// tracker and the primary checkout now hold them, when the docket is built for
// the development manager to read (orchestrator.Docketer.Unready). The tree is
// the primary checkout for the reason the pull's is: what the check is about is
// what a run cut from there would find.
type docketUnready struct {
	tracker    statusLister
	repository string
}

func (u docketUnready) RereadUnready(workItemIDs []string) (func(string) orchestrator.UnreadyReading, error) {
	ctx, cancel := context.WithTimeout(context.Background(), unreadyRereadTimeout)
	defer cancel()
	listed, err := u.tracker.ListStatuses(ctx, unreadyStatuses)
	if err != nil {
		return nil, err
	}
	items := make(map[string]beads.WorkItem, len(listed))
	for _, item := range listed {
		items[strings.TrimSpace(item.ID)] = item
	}
	related := func(id string) (beads.WorkItem, bool) {
		item, found := items[id]
		return item, found
	}
	// One tree for the whole reading, so the source is read at most once
	// however many entries stand.
	tree := &readiness.Repository{Root: u.repository}
	return func(workItemID string) orchestrator.UnreadyReading {
		item, present := items[strings.TrimSpace(workItemID)]
		if !present {
			return orchestrator.UnreadyReading{}
		}
		reading, err := readiness.ReadWith(item, tree, related)
		return orchestrator.UnreadyReading{Present: true, Unmet: reading.Unmet, Unreadable: err != nil}
	}, nil
}
