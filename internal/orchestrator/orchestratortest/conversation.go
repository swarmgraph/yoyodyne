package orchestratortest

import (
	"context"
	"errors"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ReplayBackend answers scripted turns and keeps the prompts it was given, which
// is what says the refusal reached the woken turn.
type ReplayBackend struct {
	Replies []string
	Prompts []string
}

func (b *ReplayBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	index := len(b.Prompts)
	b.Prompts = append(b.Prompts, request.Prompt)
	if index >= len(b.Replies) {
		return backendapi.RunResult{LastEvent: request.LastSequence + 1}, errors.New("unexpected conversation turn")
	}
	return backendapi.RunResult{
		Backend:   domain.BackendClaudeCode,
		SessionID: "session-1",
		FinalText: b.Replies[index],
		LastEvent: request.LastSequence + 1,
	}, nil
}

// ParkingTracker is the work tracker as this replay needs it: the parks that were
// actually carried out, and enough of an item to park.
type ParkingTracker struct {
	Parked []string
}

func (t *ParkingTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Title: "an admitted item", Status: "open"}, nil
}

func (t *ParkingTracker) List(_ context.Context, _ string) ([]beads.WorkItem, error) { return nil, nil }

func (t *ParkingTracker) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	return beads.WorkItem{ID: "yoyodyne-new", Title: item.Title, Status: "open"}, nil
}

func (t *ParkingTracker) Update(_ context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	if change.Parking != nil {
		t.Parked = append(t.Parked, id)
	}
	return beads.WorkItem{ID: id, Status: "open"}, nil
}

func (t *ParkingTracker) Block(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Status: "open"}, nil
}

func (t *ParkingTracker) Unblock(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Status: "open"}, nil
}

func (t *ParkingTracker) AddBlocker(_ context.Context, _, _ string) error { return nil }

func (t *ParkingTracker) RemoveBlocker(_ context.Context, _, _ string) error { return nil }

func (t *ParkingTracker) Complete(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Status: "closed"}, nil
}
