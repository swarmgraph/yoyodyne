package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type assigneeTrackerFake struct {
	items   []beads.WorkItem
	refuse  map[string]bool
	cleared []string
	marked  []string
}

func (f *assigneeTrackerFake) MarkClaimAssignee(_ context.Context, id string) (beads.WorkItem, error) {
	if f.refuse[id] {
		return beads.WorkItem{}, errors.New("database is locked")
	}
	f.marked = append(f.marked, id)
	return beads.WorkItem{ID: id, ClaimAssignee: true}, nil
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
	// The held item whose name an earlier run's claim set, unmarked, is marked
	// rather than cleared from under its run, so the give-back that ends the
	// hold clears it (beads.TestEveryGiveBackClearsTheClaimsAssigneeAndNoPersons,
	// beads.TestAnOldClaimsNameMarkedLaterIsClearedOnGiveBackConformance). The
	// one already marked is not written again.
	if want := []string{"yoyodyne-running"}; !reflect.DeepEqual(tracker.marked, want) {
		t.Fatalf("marked %v, want %v", tracker.marked, want)
	}
	if len(reporting.marked) != 0 {
		t.Fatalf("a report-only sweep marked %v", reporting.marked)
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
	// A refused mark is a problem too, and nothing else stops.
	unmarkable := &assigneeTrackerFake{items: items, refuse: map[string]bool{"yoyodyne-running": true}}
	sweep, err = AssigneeCleanup{Tracker: unmarkable, Runs: runs}.Sweep(context.Background(), true)
	if err != nil || len(sweep.Problems) != 1 || len(unmarkable.cleared) != 2 {
		t.Fatalf("a refused mark: problems %v, cleared %v, error %v", sweep.Problems, unmarkable.cleared, err)
	}
}

type assigneeRecordsFake struct {
	kept    *runstate.AssigneeCleanupRecord
	refuse  error
	records int
}

func (f *assigneeRecordsFake) Load() (*runstate.AssigneeCleanupRecord, error) { return f.kept, nil }
func (f *assigneeRecordsFake) Path() string                                   { return "/state/assignee-cleanup.json" }
func (f *assigneeRecordsFake) Record(record runstate.AssigneeCleanupRecord) error {
	f.records++
	if f.refuse != nil {
		return f.refuse
	}
	f.kept = &record
	return nil
}

// The scheduler's pass clears the names by itself on its first pass, records
// what it did and says so on the schedule, and on every later pass does
// nothing; a pass that could not record it says so and the next one tries
// again.
func TestTheSchedulerClearsLeftoverAssigneesOnce(t *testing.T) {
	t.Parallel()

	tracker := &assigneeTrackerFake{items: []beads.WorkItem{
		{ID: "yoyodyne-ran", Status: "open", Assignee: "Mason Bryant"},
		{ID: "yoyodyne-byhand", Status: "open", Assignee: "Ada"},
	}}
	runs := assigneeRunsFake{{RunID: "run-old", WorkItemID: "yoyodyne-ran", Status: runstate.StatusCancelled}}
	records := &assigneeRecordsFake{}
	once := OnceAssigneeCleanup{
		Cleanup:   AssigneeCleanup{Tracker: tracker, Runs: runs},
		Records:   records,
		ProductID: "yoyodyne",
	}

	var first Schedule
	clearLeftoverAssignees(context.Background(), &first, Pull{Assignees: once})
	if !reflect.DeepEqual(tracker.cleared, []string{"yoyodyne-ran"}) || records.records != 1 || records.kept == nil {
		t.Fatalf("the first pass cleared %v and recorded %d time(s)", tracker.cleared, records.records)
	}
	if first.AssigneeCleanupProblem != "" || !strings.Contains(first.AssigneeCleanup, "cleared the assignee on 1 unfinished item(s)") ||
		!strings.Contains(first.AssigneeCleanup, "left 1 a person set") || !strings.Contains(first.AssigneeCleanup, "/state/assignee-cleanup.json") {
		t.Fatalf("the first pass said %q (problem %q)", first.AssigneeCleanup, first.AssigneeCleanupProblem)
	}

	var second Schedule
	clearLeftoverAssignees(context.Background(), &second, Pull{Assignees: once})
	if len(tracker.cleared) != 1 || records.records != 1 || second.AssigneeCleanup != "" || second.AssigneeCleanupProblem != "" {
		t.Fatalf("a later pass ran the clean-up again: cleared %v, recorded %d time(s), said %q", tracker.cleared, records.records, second.AssigneeCleanup)
	}

	// A sweep the tracker refused a write in is not recorded, so a later pass
	// runs it again and finishes the item, with nobody running anything by hand.
	flaky := &assigneeTrackerFake{
		items:  []beads.WorkItem{{ID: "yoyodyne-ran", Status: "open", Assignee: "Mason Bryant"}},
		refuse: map[string]bool{"yoyodyne-ran": true},
	}
	flakyRecords := &assigneeRecordsFake{}
	retried := OnceAssigneeCleanup{Cleanup: AssigneeCleanup{Tracker: flaky, Runs: runs}, Records: flakyRecords, ProductID: "yoyodyne"}
	var refused Schedule
	clearLeftoverAssignees(context.Background(), &refused, Pull{Assignees: retried})
	if flakyRecords.records != 0 || !strings.Contains(refused.AssigneeCleanupProblem, "the next pass tries again") {
		t.Fatalf("a sweep with a refused clear was recorded %d time(s) and said %q", flakyRecords.records, refused.AssigneeCleanupProblem)
	}
	flaky.refuse = nil
	var later Schedule
	clearLeftoverAssignees(context.Background(), &later, Pull{Assignees: retried})
	if !reflect.DeepEqual(flaky.cleared, []string{"yoyodyne-ran"}) || flakyRecords.records != 1 || later.AssigneeCleanupProblem != "" {
		t.Fatalf("the pass after a refused clear cleared %v, recorded %d time(s), said %q", flaky.cleared, flakyRecords.records, later.AssigneeCleanupProblem)
	}

	unrecorded := OnceAssigneeCleanup{
		Cleanup:   AssigneeCleanup{Tracker: &assigneeTrackerFake{}, Runs: runs},
		Records:   &assigneeRecordsFake{refuse: errors.New("disk full")},
		ProductID: "yoyodyne",
	}
	var failed Schedule
	clearLeftoverAssignees(context.Background(), &failed, Pull{Assignees: unrecorded})
	if !strings.Contains(failed.AssigneeCleanupProblem, "the next pass tries again") || failed.AssigneeCleanup != "" {
		t.Fatalf("a pass that could not record the clean-up said %q / %q", failed.AssigneeCleanup, failed.AssigneeCleanupProblem)
	}
}
