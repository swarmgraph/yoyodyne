package readmodel

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// Who holds an item is said as a running run, a person, or nobody, and a name
// a run's claim set is never said to be a person's.
func TestItemHolderSaysARunAPersonOrNobody(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		item     beads.WorkItem
		running  string
		runsRead bool
		kind     HolderKind
		says     string
	}{
		{"a running run", beads.WorkItem{Status: "in_progress", Assignee: "Mason Bryant", ClaimAssignee: true}, "run-1", true,
			HeldByRun, "a running run, run-1"},
		{"a claim the runs were not read for", beads.WorkItem{Status: "in_progress", Assignee: "Mason Bryant", ClaimAssignee: true}, "", false,
			HeldByRun, "a run the harness started; the tracker has the item claimed for it"},
		{"a person", beads.WorkItem{Status: "open", Assignee: "Ada"}, "", true,
			HeldByPerson, "a person, Ada, who set the assignee by hand"},
		{"a claim's name left behind", beads.WorkItem{Status: "open", Assignee: "Mason Bryant", ClaimAssignee: true}, "", true,
			HeldByNobody, "nobody; the assignee Mason Bryant was left by a run's claim that has ended"},
		{"a claim with no run behind it", beads.WorkItem{Status: "in_progress", Assignee: "Mason Bryant", ClaimAssignee: true}, "", true,
			HeldByNobody, "nobody; the tracker has the item claimed, but no run is working on it"},
		{"a closed item", beads.WorkItem{Status: "closed", Assignee: "Mason Bryant"}, "", true,
			HeldByNobody, "nobody; the item is closed"},
		{"nothing at all", beads.WorkItem{Status: "open"}, "", true, HeldByNobody, "nobody"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			holder := ItemHolder(test.item, test.running, test.runsRead)
			if holder.Kind != test.kind || holder.Says != test.says {
				t.Fatalf("ItemHolder() = %+v, want %s saying %q", holder, test.kind, test.says)
			}
		})
	}
}
