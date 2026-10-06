package orchestratortest

import (
	"context"
	"fmt"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// ScheduleTracker holds the queue shared by scheduler tests and their pipelines.
// Its mutex also protects the scheduler fixture's run state.
type ScheduleTracker struct {
	Mu         sync.Mutex
	Items      []beads.WorkItem
	ReadyItems map[string]bool
}

func (h *ScheduleTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	var matching []beads.WorkItem
	for _, item := range h.Items {
		if item.Status == status {
			matching = append(matching, item)
		}
	}
	return matching, nil
}

func (h *ScheduleTracker) Ready(context.Context) ([]beads.WorkItem, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	var pullable []beads.WorkItem
	for _, item := range h.Items {
		if h.ReadyItems[item.ID] && item.Status == "open" {
			pullable = append(pullable, item)
		}
	}
	return pullable, nil
}

// Release gives a claimed item back to the harness's queue, exactly as the
// tracker does: the item is open, and pullable again.
func (h *ScheduleTracker) Release(_ context.Context, id, _ string) (beads.WorkItem, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	for index, item := range h.Items {
		if item.ID == id {
			h.Items[index].Status = "open"
			h.ReadyItems[id] = true
			return h.Items[index], nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no such work item %s", id)
}
