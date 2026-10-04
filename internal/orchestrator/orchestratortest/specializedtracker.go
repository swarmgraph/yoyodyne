package orchestratortest

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// LandingTracker records the landings and closes a sweep makes, and refuses
// the ones it is told to.
type LandingTracker struct {
	Closed   map[string]string
	Landings map[string][]string
	Refused  map[string]error
	// unrecordable refuses every landing write on an item, and unclearable
	// refuses only the write that clears one.
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

// Current is what the item carries as its landing after everything the sweep
// wrote to it, which is what the next pull's queue reads back.
func (t *LandingTracker) Current(id string) string {
	written := t.Landings[id]
	if len(written) == 0 {
		return ""
	}
	return written[len(written)-1]
}

// RecordingFiler is a tracker that takes the items a red landing files, or
// refuses them, and lists what it has taken as open work.
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

// OpenWorkItem is a tracker reporting one item in a state a fresh run may start
// on, for the sequences whose subject is something other than the item itself.
type OpenWorkItem string

func (id OpenWorkItem) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: string(id), Status: "open"}, nil
}

func (id OpenWorkItem) Release(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: string(id), Status: "open"}, nil
}
