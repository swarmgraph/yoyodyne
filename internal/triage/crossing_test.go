package triage

import (
	"strings"
	"testing"
	"time"
)

// A crossing the development manager took himself is the best evidence there is
// about whether taking another will help, so the entry says how many he has
// taken and how many he has left. The counters above it cannot answer that: they
// count what was spent, and a crossing spends none of them.
func TestARenderedEntrySaysWhereTheDelegatedCrossingsStand(t *testing.T) {
	t.Parallel()

	// An item nobody has crossed anything on says nothing at all, which is every
	// item: a line announcing an untouched delegation on every stoppage is one
	// every reader learns to skip.
	if rendered := stoppedRunEntry().Render(); strings.Contains(rendered, "cap crossing(s)") {
		t.Fatalf("an item with no crossing announced the delegation anyway:\n%s", rendered)
	}

	crossed := stoppedRunEntry()
	crossed.Counters.Crossings, crossed.Counters.CrossingsBound = 2, 5
	crossed.Overrides = []Override{{
		Budget:    "review round",
		Cap:       5,
		DecidedBy: "development manager",
		DecidedAt: time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC),
		Reason:    "the ground moved under the change",
		// The role as the orchestrator carries it onto an entry: the title a reader
		// reads rather than the marker the record keys it by.
		CrossedBy: "development manager",
	}, {
		Budget:    "re-run",
		Cap:       4,
		DecidedBy: "mason",
		DecidedAt: time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC),
		Reason:    "driven by hand until it lands",
	}}
	rendered := crossed.Render()
	for _, want := range []string{
		// The two are labelled apart: one is an answered escalation and the other
		// counts against what this role has left.
		"Cap crossed on delegated authority: raised the review round cap to 5, crossed by the development manager on delegated authority",
		"Operator override: raised the re-run cap to 4, decided by mason",
		"2 of 5 cap crossing(s) on your own authority are recorded against yoyodyne-task",
		"reported to the operator as you record it",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}

	// At the bound it says what happens instead rather than only that the room is
	// gone: a development manager who learns that by being refused has spent a
	// turn finding out something the entry could have told him.
	spent := crossed
	spent.Counters.Crossings = 5
	rendered = spent.Render()
	for _, want := range []string{
		"A further cap crossing for yoyodyne-task is not yours",
		"5 of 5 crossing(s) on your own authority are recorded",
		"past the bound a crossing is not a development manager's to record. Escalate to the Lead Product Manager",
		"and say which cap and why.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry at the bound is missing %q:\n%s", want, rendered)
		}
	}
	if !spent.Counters.CrossingsSpent() {
		t.Fatal("CrossingsSpent() is false at the bound")
	}
	// An entry written before the delegation existed carries no bound, and reads
	// as an item nobody has crossed anything on rather than as one already spent.
	if (Counters{Crossings: 0, CrossingsBound: 0}).CrossingsSpent() {
		t.Fatal("CrossingsSpent() is true on a record written before crossings existed")
	}
}
