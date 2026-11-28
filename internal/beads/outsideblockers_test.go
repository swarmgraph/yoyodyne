package beads

import (
	"reflect"
	"slices"
	"testing"
)

func waits(on ...string) []Dependency {
	var dependencies []Dependency
	for _, id := range on {
		dependencies = append(dependencies, Dependency{ID: id, Type: BlocksDependency})
	}
	return dependencies
}

// The walk follows dependencies and children from each item inside, at any
// depth, through items outside, and stops at closed work and at work inside.
func TestOutsideBlockersFollowsEveryLinkToItsEnd(t *testing.T) {
	items := []WorkItem{
		{ID: "e", Status: "open"},
		{ID: "e.1", Status: "open", Parent: "e", Dependencies: waits("x", "done", "e.2")},
		{ID: "e.2", Status: "open", Parent: "e"},
		{ID: "x", Title: "First", Status: "open", Dependencies: waits("y")},
		{ID: "y", Title: "Second", Status: "open"},
		{ID: "y.1", Title: "Under the second", Status: "open", Parent: "y", Dependencies: waits("e.2")},
		{ID: "done", Status: "closed", Dependencies: waits("never")},
		{ID: "never", Status: "open"},
		// A listing carrying another item's edge does not make e.2 wait on it.
		{ID: "e.2.other", Status: "open", Dependencies: []Dependency{{IssueID: "elsewhere", ID: "ignored", Type: BlocksDependency}}},
		{ID: "ignored", Status: "open"},
	}
	inside := []string{"e", "e.1", "e.2"}
	found := OutsideBlockers(inside, func(id string) bool { return slices.Contains(inside, id) }, items)
	want := []OutsideBlocker{
		{ID: "x", Title: "First", Member: "e.1", From: "e.1"},
		{ID: "y", Title: "Second", Member: "e.1", From: "x"},
		{ID: "y.1", Title: "Under the second", Member: "e.1", From: "y", Child: true},
	}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("blockers = %+v, want %+v", found, want)
	}
	if got := found[2].Describe(); got != "y.1 (Under the second) could block e.1: it is a child of y, which could block e.1" {
		t.Fatalf("Describe() = %q", got)
	}
	if got := found[0].Describe(); got != "x (First) could block e.1: e.1 waits on it" {
		t.Fatalf("Describe() = %q", got)
	}

	// Once every one of them is inside, nothing outside could block a member.
	inside = append(inside, "x", "y", "y.1")
	if found := OutsideBlockers(inside, func(id string) bool { return slices.Contains(inside, id) }, items); len(found) != 0 {
		t.Fatalf("blockers = %+v, want none once all are inside", found)
	}
}

// A linked item the listing does not hold could be open, so it is named.
func TestOutsideBlockersNamesALinkedItemTheListingLacks(t *testing.T) {
	items := []WorkItem{{ID: "e", Status: "open", Dependencies: waits("gone")}}
	found := OutsideBlockers([]string{"e"}, func(id string) bool { return id == "e" }, items)
	if len(found) != 1 || found[0].ID != "gone" {
		t.Fatalf("blockers = %+v, want the unlisted item", found)
	}
}
