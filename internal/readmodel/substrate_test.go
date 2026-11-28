package readmodel

// The run records are what say a re-scoped parent's change never landed, and
// the same derivation every surface reads holds is what hands that to the
// queue: a child building on the change waits for it, and is released by a
// later run promoting it, without anything written onto the child.

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestAChildBuildingOnAParentWhoseRunNeverLandedIsHeldUntilARunPromotesIt(t *testing.T) {
	t.Parallel()

	const parent, child = "yoyodyne-ifd.429.13", "yoyodyne-ifd.429.13.1"
	started := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	stopped := runstate.State{
		RunID:         "run-757",
		WorkItemID:    parent,
		Status:        runstate.StatusFailed,
		Branch:        "yoyodyne/yoyodyne-ifd-429-13/757",
		TargetBranch:  "main",
		HarnessCommit: "abcdef0123456789abcdef0123456789abcdef01",
		PullRequest:   &runstate.PullRequest{Number: 757},
		StartedAt:     started,
		UpdatedAt:     started,
	}
	items := []beads.WorkItem{
		{ID: parent, Title: "re-scoped", Status: "open", Priority: 1},
		{ID: child, Title: "carry the rest", Description: "Builds on the parent's change.", Status: "open", Priority: 1, Parent: parent},
	}

	held := heldForAPerson([]runstate.State{stopped}, nil, nothingDecided, asRecorded)
	entry := backlog.Order(items, []string{child}, held, nil).Entries[1]
	if entry.ID != child || entry.Ready || !entry.AwaitingLanding {
		t.Fatalf("entry = %#v, want %s held for its parent's change", entry, child)
	}
	for _, want := range []string{"run run-757", "yoyodyne/yoyodyne-ifd-429-13/757", "at commit abcdef01", "pull request #757"} {
		if !strings.Contains(entry.Hold(), want) {
			t.Fatalf("the hold is missing %q: %s", want, entry.Hold())
		}
	}

	promoted := stopped
	promoted.RunID = "run-758"
	promoted.Status = runstate.StatusSucceeded
	promoted.Integration = &runstate.Integration{TargetBranch: "main"}
	promoted.StartedAt = started.Add(time.Hour)
	promoted.UpdatedAt = promoted.StartedAt
	held = heldForAPerson([]runstate.State{stopped, promoted}, nil, nothingDecided, asRecorded)
	if entry := backlog.Order(items, []string{child}, held, nil).Entries[1]; !entry.Ready {
		t.Fatalf("the child is still held after its parent's change was promoted: %s", entry.Hold())
	}
}

// queuedParentRun is the founding case's parent run: the first piece of
// stopping a run's processes, approved and waiting in the merge queue, its run
// ended succeeded when the change was admitted.
func queuedParentRun(parent string, at time.Time) runstate.State {
	return runstate.State{
		RunID:        "run-218",
		WorkItemID:   parent,
		Status:       runstate.StatusSucceeded,
		Branch:       "yoyodyne/yoyodyne-ifd-428-218/218",
		TargetBranch: "main",
		MergeQueue:   &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-218", Order: 7, TargetBranch: "main", AdmittedAt: at},
		StartedAt:    at.Add(-time.Hour),
		UpdatedAt:    at,
	}
}

func TestAChildOfAParentWhoseApprovedChangeIsInTheMergeQueueIsHeldUntilItLands(t *testing.T) {
	t.Parallel()

	const parent, child = "yoyodyne-ifd.428.218", "yoyodyne-ifd.428.218.1"
	admitted := time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC)
	queued := queuedParentRun(parent, admitted)
	// The child says nothing about building on its parent: an approved change in
	// the queue holds every child it has.
	items := []beads.WorkItem{
		{ID: child, Title: "the second piece", Description: "Stop the rest of a run's processes.", Status: "open", Priority: 0, Parent: parent},
	}

	held := heldForAPerson([]runstate.State{queued}, nil, nothingDecided, asRecorded)
	entry := backlog.Order(items, []string{child}, held, nil).Entries[0]
	if entry.Ready || !entry.AwaitingLanding || entry.HoldKind() != backlog.HeldWaitingOn {
		t.Fatalf("entry = %#v, want %s waiting on its parent's change to land", entry, child)
	}
	for _, want := range []string{"broken out of " + parent, "run run-218", "merge queue for main as entry 7", "has not landed yet", "pulled once that change lands"} {
		if !strings.Contains(entry.Hold(), want) {
			t.Fatalf("the hold is missing %q: %s", want, entry.Hold())
		}
	}

	landed := queued
	landed.Integration = &runstate.Integration{TargetBranch: "main"}
	held = heldForAPerson([]runstate.State{landed}, nil, nothingDecided, asRecorded)
	if entry := backlog.Order(items, []string{child}, held, nil).Entries[0]; !entry.Ready {
		t.Fatalf("the child is still held after its parent's change landed: %s", entry.Hold())
	}
}

func TestAChildOfAParentWithNothingToLandIsNotHeld(t *testing.T) {
	t.Parallel()

	const parent, child = "yoyodyne-ifd.9k1y.23", "yoyodyne-ifd.9k1y.23.1"
	admitted := time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC)
	items := []beads.WorkItem{
		{ID: child, Title: "the rest of the check", Status: "open", Priority: 1, Parent: parent},
	}
	document := queuedParentRun(parent, admitted)
	document.Document = &runstate.DocumentPublication{}
	closedParent := items[0]
	closedParent.Dependencies = []beads.Dependency{{ID: parent, Type: "parent-child", Status: "closed"}}

	for name, tc := range map[string]struct {
		runs []runstate.State
		item beads.WorkItem
	}{
		"no run at all":                     {item: items[0]},
		"a run that ended with no change":   {runs: []runstate.State{{RunID: "run-1", WorkItemID: parent, Status: runstate.StatusSucceeded, StartedAt: admitted, UpdatedAt: admitted}}, item: items[0]},
		"a document a conversation carries": {runs: []runstate.State{document}, item: items[0]},
		"a parent closed with it queued":    {runs: []runstate.State{queuedParentRun(parent, admitted)}, item: closedParent},
	} {
		held := heldForAPerson(tc.runs, nil, nothingDecided, asRecorded)
		if entry := backlog.Order([]beads.WorkItem{tc.item}, []string{child}, held, nil).Entries[0]; !entry.Ready {
			t.Fatalf("%s: the child is held: %s", name, entry.Hold())
		}
	}
}
