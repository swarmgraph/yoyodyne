package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type assigneeTrackerFake struct {
	items   []beads.WorkItem
	refuse  map[string]bool
	cleared []string
}

func (f *assigneeTrackerFake) ListStatuses(context.Context, []string) ([]beads.WorkItem, error) {
	return f.items, nil
}

func (f *assigneeTrackerFake) ClearLeftoverAssignee(_ context.Context, id, note string) (beads.WorkItem, error) {
	if f.refuse[id] {
		return beads.WorkItem{}, errors.New("database is locked")
	}
	if note == "" {
		return beads.WorkItem{}, errors.New("no note")
	}
	f.cleared = append(f.cleared, id)
	return beads.WorkItem{ID: id}, nil
}

type assigneeRunsFake []runstate.State

func (f assigneeRunsFake) Recorded() ([]runstate.State, error) { return f, nil }

// The clean-up clears an assignee a run's claim set on an item nothing is
// working on, and leaves one a person set, and any item a run holds now.
func TestAssigneeCleanupClearsOnlyWhatClaimsLeft(t *testing.T) {
	t.Parallel()

	items := []beads.WorkItem{
		{ID: "yoyodyne-ran", Status: "open", Assignee: "Mason Bryant"},
		{ID: "yoyodyne-marked", Status: "blocked", Assignee: "Mason Bryant", ClaimAssignee: true},
		{ID: "yoyodyne-byhand", Status: "open", Assignee: "Mason Bryant"},
		{ID: "yoyodyne-running", Status: "in_progress", Assignee: "Mason Bryant"},
		{ID: "yoyodyne-stuck", Status: "in_progress", Assignee: "Mason Bryant", ClaimAssignee: true},
		{ID: "yoyodyne-nobody", Status: "open"},
	}
	runs := assigneeRunsFake{
		{RunID: "run-old", WorkItemID: "yoyodyne-ran", Status: runstate.StatusCancelled},
		{RunID: "run-now", WorkItemID: "yoyodyne-running", Status: runstate.StatusRunning},
	}
	ids := func(entries []LeftoverAssignee) []string {
		var named []string
		for _, entry := range entries {
			named = append(named, entry.WorkItemID)
		}
		return named
	}

	// Reporting only writes nothing and names what it would clear.
	reporting := &assigneeTrackerFake{items: items}
	sweep, err := AssigneeCleanup{Tracker: reporting, Runs: runs}.Sweep(context.Background(), false)
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(reporting.cleared) != 0 || sweep.Applied {
		t.Fatalf("a report-only sweep cleared %v", reporting.cleared)
	}

	tracker := &assigneeTrackerFake{items: items}
	sweep, err = AssigneeCleanup{Tracker: tracker, Runs: runs}.Sweep(context.Background(), true)
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if want := []string{"yoyodyne-ran", "yoyodyne-marked"}; !reflect.DeepEqual(tracker.cleared, want) || !reflect.DeepEqual(ids(sweep.Cleared), want) {
		t.Fatalf("cleared %v (reported %v), want %v", tracker.cleared, ids(sweep.Cleared), want)
	}
	if want := []string{"yoyodyne-byhand"}; !reflect.DeepEqual(ids(sweep.ByPerson), want) {
		t.Fatalf("left as a person's %v, want %v", ids(sweep.ByPerson), want)
	}
	if want := []string{"yoyodyne-running", "yoyodyne-stuck"}; !reflect.DeepEqual(ids(sweep.Held), want) {
		t.Fatalf("left as held %v, want %v", ids(sweep.Held), want)
	}

	// A clear the tracker refuses is reported and the rest go on.
	refusing := &assigneeTrackerFake{items: items, refuse: map[string]bool{"yoyodyne-ran": true}}
	sweep, err = AssigneeCleanup{Tracker: refusing, Runs: runs}.Sweep(context.Background(), true)
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(sweep.Problems) != 1 || !reflect.DeepEqual(refusing.cleared, []string{"yoyodyne-marked"}) {
		t.Fatalf("a refused clear: problems %v, cleared %v", sweep.Problems, refusing.cleared)
	}
}
