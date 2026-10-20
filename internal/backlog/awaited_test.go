package backlog

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// The long-paste rebuild (yoyodyne-etw.1), as the tracker held it on October
// 18: filed under the stopped item and then waited on by it. The tracker passes
// the parent's wait down to the child, so it never offered the child, and the
// parent waited on it for ever. The queue offers the child.
func TestAChildItsParentWaitsOnIsOffered(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-etw", Title: "Long paste", Status: statusBlocked, Priority: 1,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-etw.1", Type: beads.BlocksDependency}}},
		{ID: "yoyodyne-etw.1", Title: "Rebuild from current main", Status: statusOpen, Priority: 0, Parent: "yoyodyne-etw"},
	}, nil, ReadHolds(map[string]Hold{"yoyodyne-etw": {Reason: "run run-d8b6bd17 stopped on it", WaitsOn: "yoyodyne-etw.1"}}), nil)

	child := queue.Entries[0]
	if child.ID != "yoyodyne-etw.1" || !child.Ready || len(child.InheritedBlocks) != 0 {
		t.Fatalf("child = %#v, want the rebuild offered with nothing said to hold it", child)
	}
	if next, found := queue.Next(); !found || next.ID != "yoyodyne-etw.1" {
		t.Fatalf("next = %#v, %v, want the rebuild", next, found)
	}
	if parent := queue.Entries[1]; parent.Ready {
		t.Fatalf("parent = %#v, want it still held on its stopped run and its wait", parent)
	}
}

// Every case in which the tracker's hold on a child stands, because something
// other than a wait on the child itself could be what it holds the child for.
func TestAChildHeldForAnythingElseStaysHeld(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		child string
		items []beads.WorkItem
	}{
		{
			name:  "its parent waits on other work",
			child: "p.1",
			items: []beads.WorkItem{
				{ID: "p", Status: statusBlocked, Dependencies: []beads.Dependency{{ID: "other", Type: beads.BlocksDependency}}},
				{ID: "p.1", Status: statusOpen, Parent: "p"},
				{ID: "other", Status: statusOpen},
			},
		},
		{
			name:  "its parent waits on a sibling, which is offered and it is not",
			child: "p.1",
			items: []beads.WorkItem{
				{ID: "p", Status: statusBlocked, Dependencies: []beads.Dependency{{ID: "p.2", Type: beads.BlocksDependency}}},
				{ID: "p.1", Status: statusOpen, Parent: "p"},
				{ID: "p.2", Status: statusOpen, Parent: "p"},
			},
		},
		{
			name:  "its parent waits on it, and its grandparent on other work",
			child: "g.1.1",
			items: []beads.WorkItem{
				{ID: "g", Status: statusOpen, Dependencies: []beads.Dependency{{ID: "other", Type: beads.BlocksDependency}}},
				{ID: "g.1", Status: statusBlocked, Parent: "g", Dependencies: []beads.Dependency{{ID: "g.1.1", Type: beads.BlocksDependency}}},
				{ID: "g.1.1", Status: statusOpen, Parent: "g.1"},
				{ID: "other", Status: statusOpen},
			},
		},
		{
			name:  "its parent waits on it, and it records a link of its own nothing says is closed",
			child: "p.1",
			items: []beads.WorkItem{
				{ID: "p", Status: statusBlocked, Dependencies: []beads.Dependency{{ID: "p.1", Type: beads.BlocksDependency}}},
				// The listing names the link without its status, which is how bd lists it,
				// and the work it names has left the backlog: it may be finished, or a run
				// may be working it right now.
				{ID: "p.1", Status: statusOpen, Parent: "p", Dependencies: []beads.Dependency{{ID: "in-flight", Type: beads.BlocksDependency}}},
			},
		},
		{
			// The grandparent is being worked, so it has left the backlog, and it may be
			// held by a parent of its own that the backlog cannot see.
			name:  "its parent waits on it, and a grandparent outside the backlog may hold it",
			child: "g.1.1",
			items: []beads.WorkItem{
				{ID: "g.1", Status: statusBlocked, Parent: "g", Dependencies: []beads.Dependency{{ID: "g.1.1", Type: beads.BlocksDependency}}},
				{ID: "g.1.1", Status: statusOpen, Parent: "g.1"},
			},
		},
		{
			name:  "no ancestor in the backlog says why the tracker holds it",
			child: "p.1",
			items: []beads.WorkItem{
				{ID: "p.1", Status: statusOpen, Parent: "p"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			queue := Order(test.items, []string{"other"}, ReadHolds(nil), nil)
			for _, entry := range queue.Entries {
				if entry.ID == test.child {
					if entry.Ready {
						t.Fatalf("%s = %#v, want it held as the tracker holds it", entry.ID, entry)
					}
					return
				}
			}
			t.Fatalf("%s is not in the queue: %#v", test.child, queue.Entries)
		})
	}
}

// A link of the child's own that the reading shows closed holds nothing, so it
// does not stop the child being offered.
func TestAChildWhoseOwnLinksAreClosedIsOffered(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "p", Status: statusBlocked, Dependencies: []beads.Dependency{{ID: "p.1", Type: beads.BlocksDependency}}},
		{ID: "p.1", Status: statusOpen, Parent: "p",
			Dependencies: []beads.Dependency{{ID: "done", Type: beads.BlocksDependency, Status: "closed"}}},
	}, nil, ReadHolds(nil), nil)

	if child := queue.Entries[1]; child.ID != "p.1" || !child.Ready {
		t.Fatalf("child = %#v, want it offered", child)
	}
}
