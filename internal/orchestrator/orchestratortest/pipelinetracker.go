package orchestratortest

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// PipelineTracker drives real pipelines over the scheduler fixture's queue.
type PipelineTracker struct {
	Queue *ScheduleTracker
	Notes []string
}

// Show, Claim, RecordOutcome, Block, and Complete are the tracker the real
// pipeline drives. They are the fake harness's items behind its own mutex,
// because three runs are calling them at once.
func (h *PipelineTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	h.Queue.Mu.Lock()
	defer h.Queue.Mu.Unlock()
	for _, item := range h.Queue.Items {
		if item.ID == id {
			return item, nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

func (h *PipelineTracker) Claim(_ context.Context, id string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	item, err := h.SetStatus(id, "in_progress")
	return item, nil, err
}

func (h *PipelineTracker) RecordOutcome(_ context.Context, id, notes string) (beads.WorkItem, error) {
	h.Queue.Mu.Lock()
	defer h.Queue.Mu.Unlock()
	h.Notes = append(h.Notes, notes)
	return h.itemLocked(id)
}

// RecordedNotes is every note the runs appended to an item, in order.
func (h *PipelineTracker) RecordedNotes() []string {
	h.Queue.Mu.Lock()
	defer h.Queue.Mu.Unlock()
	return append([]string(nil), h.Notes...)
}

func (h *PipelineTracker) Block(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.SetStatus(id, "blocked")
}

func (h *PipelineTracker) Release(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.SetStatus(id, "open")
}

func (h *PipelineTracker) Complete(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.SetStatus(id, "closed")
}

// Reopen puts the item back in the backlog under the parking it was given, which
// is the whole of what a later pull reads: an item returned open and unparked is
// one the very next poll offers again.
func (h *PipelineTracker) Reopen(_ context.Context, id, _ string, parking domain.WorkItemParking) (beads.WorkItem, error) {
	h.Queue.Mu.Lock()
	for index := range h.Queue.Items {
		if h.Queue.Items[index].ID == id {
			h.Queue.Items[index].Parking = parking
		}
	}
	h.Queue.Mu.Unlock()
	return h.SetStatus(id, "open")
}

func (h *PipelineTracker) AddBlocker(_ context.Context, id, blockerID string) error {
	h.Queue.Mu.Lock()
	defer h.Queue.Mu.Unlock()
	for index := range h.Queue.Items {
		if h.Queue.Items[index].ID == id {
			h.Queue.Items[index].Dependencies = append(h.Queue.Items[index].Dependencies,
				beads.Dependency{IssueID: id, ID: blockerID, Type: "blocks"})
			return nil
		}
	}
	return fmt.Errorf("no work item %s", id)
}

func (h *PipelineTracker) SetStatus(id, status string) (beads.WorkItem, error) {
	h.Queue.Mu.Lock()
	defer h.Queue.Mu.Unlock()
	for index := range h.Queue.Items {
		if h.Queue.Items[index].ID == id {
			h.Queue.Items[index].Status = status
			return h.Queue.Items[index], nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

func (h *PipelineTracker) itemLocked(id string) (beads.WorkItem, error) {
	for _, item := range h.Queue.Items {
		if item.ID == id {
			return item, nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}
