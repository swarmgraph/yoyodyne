package beads

import (
	"errors"
	"strings"
	"testing"
)

// circularLookup answers from a fixed set of items, as a read of the tracker
// would, reporting an item it does not hold as not found.
func circularLookup(items ...WorkItem) func(string) (WorkItem, bool, error) {
	byID := map[string]WorkItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	return func(id string) (WorkItem, bool, error) {
		item, found := byID[id]
		return item, found, nil
	}
}

func blocksOn(ids ...string) []Dependency {
	var dependencies []Dependency
	for _, id := range ids {
		dependencies = append(dependencies, Dependency{ID: id, Type: BlocksDependency})
	}
	return dependencies
}

// The October 15 arrangement: the item coordinating the orchestrator test move
// was linked to wait on its own closing measure, which waits on two of its
// siblings, so the measure and both groups read as blocked through their parent
// and none could be pulled.
func TestAParentLinkedToWaitOnItsOwnChildIsACircularWait(t *testing.T) {
	t.Parallel()

	lookup := circularLookup(
		WorkItem{ID: "yoyodyne-ifd.429.14", Title: "Move the orchestrator tests", Status: "open", Parent: "yoyodyne-ifd.429"},
		WorkItem{ID: "yoyodyne-ifd.429.14.3", Title: "Re-take the race-binary measure", Status: "open", Parent: "yoyodyne-ifd.429.14",
			Dependencies: blocksOn("yoyodyne-ifd.429.14.2", "yoyodyne-ifd.429.14.5")},
		WorkItem{ID: "yoyodyne-ifd.429.14.2", Title: "Branch-protection group", Status: "open", Parent: "yoyodyne-ifd.429.14"},
		WorkItem{ID: "yoyodyne-ifd.429.14.5", Title: "Remaining-tests group", Status: "open", Parent: "yoyodyne-ifd.429.14"},
		WorkItem{ID: "yoyodyne-ifd.429", Title: "Race binary", Status: "open"},
	)
	wait, err := FindCircularWait("yoyodyne-ifd.429.14", WaitStep{ID: "yoyodyne-ifd.429.14.3", Via: WaitViaLink}, lookup)
	if err != nil {
		t.Fatalf("FindCircularWait() error = %v", err)
	}
	if wait == nil {
		t.Fatal("FindCircularWait() = nil, want the parent's wait on its own child found")
	}
	want := "'Move the orchestrator tests' (yoyodyne-ifd.429.14) would wait on 'Re-take the race-binary measure' (yoyodyne-ifd.429.14.3), " +
		"which was broken out of 'Move the orchestrator tests' (yoyodyne-ifd.429.14). " +
		"The tracker holds work broken out of an item back until that item stops waiting, so " +
		"'Move the orchestrator tests' (yoyodyne-ifd.429.14) would never stop waiting and that work would never be offered"
	if got := wait.Describe(); got != want {
		t.Fatalf("Describe() =\n%s\nwant\n%s", got, want)
	}
}

// The arrangement four design items were found in: the design an
// implementation needs was broken out of that implementation, and the
// implementation linked to wait on it. A grandchild is the implementation's own
// work as much as a child is.
func TestAnImplementationWaitingOnADesignBrokenOutOfItIsACircularWait(t *testing.T) {
	t.Parallel()

	lookup := circularLookup(
		WorkItem{ID: "yoyodyne-ifd.428.75", Title: "Scheduled passes run side by side", Status: "open", Parent: "yoyodyne-ifd.428"},
		WorkItem{ID: "yoyodyne-ifd.428.75.9", Title: "Design slices", Status: "open", Parent: "yoyodyne-ifd.428.75"},
		WorkItem{ID: "yoyodyne-ifd.428.75.9.1", Title: "Reconcile scheduled-pass concurrency", Status: "open", Parent: "yoyodyne-ifd.428.75.9"},
		WorkItem{ID: "yoyodyne-ifd.428", Title: "Scheduling", Status: "open"},
	)
	wait, err := FindCircularWait("yoyodyne-ifd.428.75", WaitStep{ID: "yoyodyne-ifd.428.75.9.1", Via: WaitViaLink}, lookup)
	if err != nil || wait == nil {
		t.Fatalf("FindCircularWait() = %v, %v; want the wait on a grandchild found", wait, err)
	}
	if len(wait.Chain) != 1 || wait.Chain[0].ID != "yoyodyne-ifd.428.75.9.1" {
		t.Fatalf("chain = %+v, want the one link the write names", wait.Chain)
	}
}

// A wait that reaches the item's own work through somebody else's link is the
// same loop one step longer, and the chain names every step of it.
func TestAWaitThatLeadsBackIntoTheItemsOwnWorkThroughAnotherItemIsCircular(t *testing.T) {
	t.Parallel()

	lookup := circularLookup(
		WorkItem{ID: "yoyodyne-ifd.7", Title: "Implementation", Status: "open"},
		WorkItem{ID: "yoyodyne-ifd.7.1", Title: "Its slice", Status: "open", Parent: "yoyodyne-ifd.7"},
		WorkItem{ID: "yoyodyne-ifd.8", Title: "Outside work", Status: "open", Parent: "yoyodyne-ifd.9"},
		WorkItem{ID: "yoyodyne-ifd.9", Title: "Outside parent", Status: "open", Dependencies: blocksOn("yoyodyne-ifd.7.1")},
	)
	wait, err := FindCircularWait("yoyodyne-ifd.7", WaitStep{ID: "yoyodyne-ifd.8", Via: WaitViaLink}, lookup)
	if err != nil || wait == nil {
		t.Fatalf("FindCircularWait() = %v, %v; want the longer loop found", wait, err)
	}
	want := "'Implementation' (yoyodyne-ifd.7) would wait on 'Outside work' (yoyodyne-ifd.8), " +
		"which is held back with its parent 'Outside parent' (yoyodyne-ifd.9), which waits on 'Its slice' (yoyodyne-ifd.7.1), " +
		"which was broken out of 'Implementation' (yoyodyne-ifd.7)."
	if got := wait.Describe(); !strings.HasPrefix(got, strings.TrimSuffix(want, ".")) {
		t.Fatalf("Describe() =\n%s\nwant it to begin\n%s", got, want)
	}
}

// Placing work under a parent that already waits on it is the same loop made
// from the other end.
func TestPlacingWorkUnderAParentThatWaitsOnItIsACircularWait(t *testing.T) {
	t.Parallel()

	lookup := circularLookup(
		WorkItem{ID: "yoyodyne-ifd.428.75", Title: "Scheduled passes run side by side", Status: "open", Parent: "yoyodyne-ifd.428",
			Dependencies: blocksOn("yoyodyne-ifd.428.75.1")},
		WorkItem{ID: "yoyodyne-ifd.428.75.1", Title: "Reconcile scheduled-pass concurrency", Status: "open", Parent: "yoyodyne-ifd.428"},
		WorkItem{ID: "yoyodyne-ifd.428", Title: "Scheduling", Status: "open"},
	)
	wait, err := FindCircularWait("yoyodyne-ifd.428.75.1", WaitStep{ID: "yoyodyne-ifd.428.75", Via: WaitViaParent}, lookup)
	if err != nil || wait == nil {
		t.Fatalf("FindCircularWait() = %v, %v; want the placement's loop found", wait, err)
	}
	want := "'Reconcile scheduled-pass concurrency' (yoyodyne-ifd.428.75.1) would be held back with its new parent " +
		"'Scheduled passes run side by side' (yoyodyne-ifd.428.75), which waits on 'Reconcile scheduled-pass concurrency' (yoyodyne-ifd.428.75.1). "
	if got := wait.Describe(); !strings.HasPrefix(got, want) {
		t.Fatalf("Describe() =\n%s\nwant it to begin\n%s", got, want)
	}
}

// The controls: a genuine wait is never reported. The design placed beside the
// implementation, a parent whose wait is on outside work, a child that is
// finished, and a detached item all leave nothing circular.
func TestGenuineWaitsAreNotCircular(t *testing.T) {
	t.Parallel()

	items := []WorkItem{
		{ID: "yoyodyne-ifd.428", Title: "Scheduling", Status: "open"},
		{ID: "yoyodyne-ifd.428.75", Title: "Implementation", Status: "open", Parent: "yoyodyne-ifd.428",
			Dependencies: blocksOn("yoyodyne-ifd.430.57")},
		{ID: "yoyodyne-ifd.428.75.1", Title: "Design beside it", Status: "open", Parent: "yoyodyne-ifd.428",
			Dependencies: blocksOn("yoyodyne-ifd.430.57")},
		{ID: "yoyodyne-ifd.428.75.2", Title: "Finished slice", Status: "closed", Parent: "yoyodyne-ifd.428.75"},
		{ID: "yoyodyne-ifd.428.75.3", Title: "Open slice", Status: "open", Parent: "yoyodyne-ifd.428.75"},
		{ID: "yoyodyne-ifd.430.57", Title: "Outside work", Status: "open"},
	}
	cases := []struct {
		name   string
		waiter string
		first  WaitStep
	}{
		{"an implementation waiting on the design beside it", "yoyodyne-ifd.428.75", WaitStep{ID: "yoyodyne-ifd.428.75.1", Via: WaitViaLink}},
		{"an item waiting on outside work", "yoyodyne-ifd.428.75.1", WaitStep{ID: "yoyodyne-ifd.430.57", Via: WaitViaLink}},
		{"a parent waiting on a child already finished", "yoyodyne-ifd.428.75", WaitStep{ID: "yoyodyne-ifd.428.75.2", Via: WaitViaLink}},
		{"a child waiting on its sibling", "yoyodyne-ifd.428.75.3", WaitStep{ID: "yoyodyne-ifd.428.75.1", Via: WaitViaLink}},
		{"work placed under a parent that waits on outside work", "yoyodyne-ifd.428.75.1", WaitStep{ID: "yoyodyne-ifd.428.75", Via: WaitViaParent}},
	}
	for _, tc := range cases {
		wait, err := FindCircularWait(tc.waiter, tc.first, circularLookup(items...))
		if err != nil || wait != nil {
			t.Errorf("%s: FindCircularWait() = %+v, %v; want nothing circular", tc.name, wait, err)
		}
	}
}

// A tracker that cannot be read is said as unread rather than taken as an
// answer, because the loop being prevented is the cost of guessing.
func TestACircularWaitThatCannotBeReadSaysSo(t *testing.T) {
	t.Parallel()

	_, err := FindCircularWait("yoyodyne-ifd.1", WaitStep{ID: "yoyodyne-ifd.2", Via: WaitViaLink}, func(string) (WorkItem, bool, error) {
		return WorkItem{}, false, errors.New("bd show timed out")
	})
	if err == nil || !strings.Contains(err.Error(), "read yoyodyne-ifd.2: bd show timed out") {
		t.Fatalf("FindCircularWait() error = %v, want the failed read named", err)
	}
}

// The walk is bounded, and a search that reaches the bound says it could not
// settle the question rather than reporting no loop.
func TestACircularWaitSearchPastItsBoundSaysSo(t *testing.T) {
	t.Parallel()

	lookup := func(id string) (WorkItem, bool, error) {
		// An endless chain of open work, each waiting on the next.
		return WorkItem{ID: id, Status: "open", Dependencies: blocksOn(id + "x")}, true, nil
	}
	_, err := FindCircularWait("yoyodyne-ifd.1", WaitStep{ID: "yoyodyne-ifd.2", Via: WaitViaLink}, lookup)
	if err == nil || !strings.Contains(err.Error(), "could not be settled") {
		t.Fatalf("FindCircularWait() error = %v, want the bound said", err)
	}
}
