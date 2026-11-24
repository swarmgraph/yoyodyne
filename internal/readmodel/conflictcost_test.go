package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func conflictSpend(t *testing.T, at string, conflict runstate.SpendConflict, amount float64) runstate.Spend {
	t.Helper()
	line := weeklySpend(atPacific(t, at), domain.RoleDeveloper, "item-a", amount)
	line.RunID, line.Conflict = "run-a", conflict
	if amount == 0 {
		line.Classification, line.Unknown = runstate.SpendUnknown, "not reported"
	}
	return line
}

func handedBack(t *testing.T, run *runstate.State, at string, lines int, paths ...string) {
	t.Helper()
	handback := runstate.ConflictHandback{From: runstate.ConflictFromLanding, At: atPacific(t, at), TargetBranch: "main", Paths: paths}
	if lines > 0 {
		handback.Lines = &lines
	}
	run.RecordConflictHandback(handback)
}

func TestTheWeeklyFiguresShowWhatConflictsCostAndTheFilesTheyWereIn(t *testing.T) {
	a := landedRun(t, "run-a", "item-a", "2026-11-11 15:00", false)
	b := landedRun(t, "run-b", "item-b", "2026-11-12 15:00", false)
	b.WorkItemTitle = "The second item"
	handedBack(t, &a, "2026-11-10 09:00", 12, "x.go", "y.go")
	handedBack(t, &a, "2026-11-10 12:00", 0, "x.go")
	handedBack(t, &b, "2026-11-11 09:00", 3, "x.go")
	handedBack(t, &b, "2026-11-02 09:00", 99, "before.go") // the week before
	reading := ReadWeeklyCost(context.Background(), WeeklyCostSources{
		Spend: fakeAccomplishmentSpend{
			conflictSpend(t, "2026-11-10 09:00", runstate.SpendConflictNone, 50),
			conflictSpend(t, "2026-11-10 10:00", runstate.SpendConflictHandback, 6),
			conflictSpend(t, "2026-11-10 11:00", runstate.SpendConflictHandback, 2),
			conflictSpend(t, "2026-11-17 11:00", runstate.SpendConflictUnclear, 5),
			conflictSpend(t, "2026-11-17 12:00", runstate.SpendConflictNotRecorded, 7),
		},
		RunsRead: true,
		Runs:     []runstate.State{a, b},
		Tracker:  fakeWeeklyTracker{items: []beads.WorkItem{{ID: "item-a", Title: "The first item"}}},
		Now:      weeklyNow(t),
	})
	last := reading.Weeks[2].Conflicts
	if !last.Known || last.Floor || last.CostUSD != 8 || last.Calls != 2 || last.PerLandedChange == nil || *last.PerLandedChange != 4 {
		t.Fatalf("last week's conflicts = %+v", last)
	}
	if last.Says != "conflicts cost $8.00 across 2 provider calls, $4.00 per landed code change" {
		t.Fatalf("says %q", last.Says)
	}
	files := last.Files
	if files == nil || files.Handbacks != 3 || files.Lines != 15 || files.LinesUnknown != 1 || len(files.Files) != 2 || files.Files[0].Path != "x.go" || files.Files[0].Handbacks != 3 {
		t.Fatalf("files = %+v", files)
	}
	for _, phrase := range []string{"at least 15 conflicting lines (not known for 1 of them)", "x.go in 3, from The first item (item-a), The second item (item-b)", "y.go in 1"} {
		if !strings.Contains(files.Says, phrase) {
			t.Errorf("files say %q, want %q", files.Says, phrase)
		}
	}
	// This week holds only spend that cannot be told apart: the cost is not
	// known, and neither amount is taken for a conflict's.
	current := reading.Weeks[3].Conflicts
	if current.Known || current.PerLandedChange != nil || current.CostUSD != 0 || current.UnclearUSD != 5 || current.NotRecordedUSD != 7 {
		t.Fatalf("this week's conflicts = %+v", current)
	}
	rendered := reading.Render()
	for _, phrase := range []string{"what conflicts cost is not known", "1 call after a conflict ($5.00 known) cannot be told apart from other work", "1 call by runs ($7.00 known) did not record whether a conflict caused them", last.Says} {
		if !strings.Contains(rendered, phrase) {
			t.Errorf("rendered without %q:\n%s", phrase, rendered)
		}
	}
}

func TestAConflictsUnpricedCallsAreNeverEstimated(t *testing.T) {
	landed := 2
	var floor, unknown ConflictCost
	floor.add(conflictSpend(t, "2026-11-10 09:00", runstate.SpendConflictHandback, 3))
	floor.add(conflictSpend(t, "2026-11-10 10:00", runstate.SpendConflictHandback, 0))
	floor.settle(&landed)
	if !floor.Known || !floor.Floor || floor.CostUSD != 3 || !strings.Contains(floor.Says, "at least $3.00") || !strings.Contains(floor.Says, "1 call for conflicts had no cost reported") {
		t.Fatalf("a partly priced conflict = %+v", floor)
	}
	unknown.add(conflictSpend(t, "2026-11-10 10:00", runstate.SpendConflictHandback, 0))
	unknown.settle(&landed)
	if unknown.Known || unknown.PerLandedChange != nil || !strings.HasPrefix(unknown.Says, "what conflicts cost is not known") {
		t.Fatalf("an unpriced conflict = %+v", unknown)
	}
	// A conversation's line says nothing of conflicts and hides none.
	var talk ConflictCost
	talk.add(weeklySpend(atPacific(t, "2026-11-10 09:00"), domain.RoleArchitect, "", 9))
	talk.settle(&landed)
	if !talk.Known || talk.Floor || talk.Says != "conflicts cost nothing" {
		t.Fatalf("a week of conversation = %+v", talk)
	}
}

type conflictRuns struct {
	runs []runstate.State
	err  error
}

func (c conflictRuns) ConflictHandbacksSince(time.Time) ([]runstate.State, error) {
	return c.runs, c.err
}

func TestTheDevelopmentManagersPassIsToldTheFilesInConflict(t *testing.T) {
	now := weeklyNow(t)()
	if said := ConflictFilesSection(conflictRuns{}, now); said != "" {
		t.Fatalf("with no hand-back the section says %q", said)
	}
	run := landedRun(t, "run-a", "item-a", "2026-11-17 15:00", false)
	run.WorkItemTitle = "The first item"
	handedBack(t, &run, "2026-11-11 09:00", 4, "old.go")
	handedBack(t, &run, "2026-11-17 09:00", 4, "x.go")
	said := ConflictFilesSection(conflictRuns{runs: []runstate.State{run}}, now)
	for _, phrase := range []string{"This week, from 2026-11-16: changes handed back for a conflict 1 time", "x.go in 1, from The first item (item-a)", "Last week, from 2026-11-09", "old.go in 1"} {
		if !strings.Contains(said, phrase) {
			t.Errorf("section = %q, want %q", said, phrase)
		}
	}
	if said := ConflictFilesSection(conflictRuns{err: errors.New("disk gone")}, now); !strings.Contains(said, "disk gone") {
		t.Fatalf("an unreadable store says %q", said)
	}
}
