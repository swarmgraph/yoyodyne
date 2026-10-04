package orchestratortest

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// LandingTracker records landings and closures, with scripted refusals.
type LandingTracker struct {
	Closed   map[string]string
	Landings map[string][]string
	Refused  map[string]error
	// Unrecordable refuses all landing writes; Unclearable only refuses clears.
	Unrecordable map[string]error
	Unclearable  map[string]error
}

func (t *LandingTracker) RecordLanding(_ context.Context, id, landing string) (beads.WorkItem, error) {
	if err := t.Unrecordable[id]; err != nil {
		return beads.WorkItem{}, err
	}
	if err := t.Unclearable[id]; err != nil && landing == "" {
		return beads.WorkItem{}, err
	}
	if t.Landings == nil {
		t.Landings = map[string][]string{}
	}
	t.Landings[id] = append(t.Landings[id], landing)
	return beads.WorkItem{ID: id, Status: "open", Landing: landing}, nil
}

func (t *LandingTracker) Complete(_ context.Context, id, reason string) (beads.WorkItem, error) {
	if err := t.Refused[id]; err != nil {
		return beads.WorkItem{}, err
	}
	if t.Closed == nil {
		t.Closed = map[string]string{}
	}
	t.Closed[id] = reason
	return beads.WorkItem{ID: id, Status: "closed"}, nil
}

// Current returns the last landing written to an item.
func (t *LandingTracker) Current(id string) string {
	written := t.Landings[id]
	if len(written) == 0 {
		return ""
	}
	return written[len(written)-1]
}

// RecordingFiler records created items and lists them as open work.
type RecordingFiler struct {
	Filed  []beads.NewWorkItem
	Open   []beads.WorkItem
	Refuse error
}

func (f *RecordingFiler) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	if f.Refuse != nil {
		return beads.WorkItem{}, f.Refuse
	}
	f.Filed = append(f.Filed, item)
	created := beads.WorkItem{ID: fmt.Sprintf("yoyodyne-red-%d", len(f.Filed)), Title: item.Title, Notes: item.Notes, Status: "open"}
	f.Open = append(f.Open, created)
	return created, nil
}

func (f *RecordingFiler) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	if status != "open" {
		return nil, nil
	}
	return f.Open, nil
}

// OpenWorkItem always reports its item as open.
type OpenWorkItem string

func (id OpenWorkItem) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: string(id), Status: "open"}, nil
}

func (id OpenWorkItem) Release(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: string(id), Status: "open"}, nil
}
