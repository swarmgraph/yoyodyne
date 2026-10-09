package backlog

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// An open child the tracker does not offer, whose own links say nothing, is held
// back because its parent waits. The queue says so in the words an item read
// uses, rather than that the tracker simply did not offer it.
func TestAChildTheTrackerHoldsBackWithItsParentSaysWhatTheParentWaitsOn(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.433.21.1", Title: "First slice", Status: statusOpen, Priority: 0, Parent: "yoyodyne-ifd.433.21"},
		{ID: "yoyodyne-ifd.433.21", Title: "Publication", Status: statusOpen, Priority: 1,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.437.14", Type: beads.BlocksDependency}}},
		{ID: "yoyodyne-ifd.437.14", Title: "Amendment ownership design", Status: statusOpen, Priority: 2},
	}, []string{"yoyodyne-ifd.437.14"}, ReadHolds(nil), nil)

	child := queue.Entries[0]
	if child.ID != "yoyodyne-ifd.433.21.1" || child.Ready {
		t.Fatalf("child = %#v, want it first and unready", child)
	}
	if child.HoldKind() != HeldInherited {
		t.Fatalf("kind = %q, want %q", child.HoldKind(), HeldInherited)
	}
	want := "blocked through its parent yoyodyne-ifd.433.21, which waits on yoyodyne-ifd.437.14. The item records no such link itself"
	if !strings.Contains(child.Hold(), want) {
		t.Fatalf("hold = %q, want it to contain %q", child.Hold(), want)
	}
	if !strings.Contains(queue.Render(), want) {
		t.Fatalf("rendered backlog does not carry the inherited block:\n%s", queue.Render())
	}
	// The parent says what it waits on itself, as it always did.
	if parent := queue.Entries[1]; parent.HoldKind() != HeldWaitingOn || len(parent.InheritedBlocks) != 0 {
		t.Fatalf("parent = %#v, want it waiting on its own link and inheriting nothing", parent)
	}
}

// An item with no waiting ancestor still says the tracker did not offer it, and
// a ready item carries no inherited block whatever its parent records.
func TestAnItemWithNoWaitingAncestorInheritsNothing(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.5.1", Title: "Not offered", Status: statusOpen, Priority: 0, Parent: "yoyodyne-ifd.5"},
		{ID: "yoyodyne-ifd.5", Title: "Parent", Status: statusOpen, Priority: 1},
	}, []string{"yoyodyne-ifd.5"}, ReadHolds(nil), nil)

	child := queue.Entries[0]
	if len(child.InheritedBlocks) != 0 || child.HoldKind() != HeldUnread || child.Hold() != "the tracker does not report it as ready to pull" {
		t.Fatalf("child = %#v, hold %q, want nothing inherited", child, child.Hold())
	}
}
