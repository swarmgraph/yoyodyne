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

type fakeWeeklyTracker struct {
	items []beads.WorkItem
	err   error
}

func (f fakeWeeklyTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return f.items, f.err
}

func weeklySpend(at time.Time, role domain.AgentRole, item string, amount float64) runstate.Spend {
	return runstate.Spend{At: at, Role: role, WorkItemID: item, Classification: runstate.SpendKnown, AmountUSD: amount}
}

func landedRun(t *testing.T, id, item, at string, document bool) runstate.State {
	t.Helper()
	ended := atPacific(t, at)
	state := runstate.State{RunID: id, WorkItemID: item, Status: runstate.StatusSucceeded, StartedAt: ended.Add(-time.Hour), CompletedAt: &ended, Integration: &runstate.Integration{TargetBranch: "main"}}
	if document {
		state.Document = &runstate.DocumentPublication{}
	}
	return state
}

func closedItem(t *testing.T, id, at string) beads.WorkItem {
	t.Helper()
	return beads.WorkItem{ID: id, Status: "closed", ClosedAt: atPacific(t, at)}
}

// The reading's weeks run Monday to Sunday in Pacific time, so "now" on a
// Wednesday makes the current week and the three before it.
func weeklyNow(t *testing.T) func() time.Time {
	return func() time.Time { return atPacific(t, "2026-11-18 12:00") }
}

func TestWeeklyCostDividesAWeeksSpendByItsClosedItemsAndLandedChangesByRole(t *testing.T) {
	sources := WeeklyCostSources{
		Spend: fakeAccomplishmentSpend{
			// Last week, Monday November 9 to Sunday November 15.
			weeklySpend(atPacific(t, "2026-11-10 09:00"), domain.RoleDeveloper, "item-a", 30),
			weeklySpend(atPacific(t, "2026-11-10 10:00"), domain.RoleReviewer, "item-a", 10),
			weeklySpend(atPacific(t, "2026-11-12 10:00"), domain.RoleDeveloper, "item-b", 20),
			// A conversation turn names no work item: it is in the week's cost,
			// its role's, and the part no item names, and in no count.
			weeklySpend(atPacific(t, "2026-11-13 10:00"), domain.RoleProductManager, "", 40),
			{At: atPacific(t, "2026-11-14 10:00"), Role: domain.RoleDevelopmentManager, Classification: runstate.SpendUnknown, Unknown: "not reported"},
			// The week before, outside last week.
			weeklySpend(atPacific(t, "2026-11-08 23:59"), domain.RoleDeveloper, "item-z", 1000),
		},
		RunsRead: true,
		Runs: []runstate.State{
			landedRun(t, "run-a", "item-a", "2026-11-11 15:00", false),
			landedRun(t, "run-b", "item-b", "2026-11-15 23:00", false),
			landedRun(t, "run-doc", "doc", "2026-11-12 08:00", true),
			// Ended without landing: not a landed change.
			{RunID: "run-c", WorkItemID: "item-c", Status: runstate.StatusFailed, StartedAt: atPacific(t, "2026-11-12 08:00"), UpdatedAt: atPacific(t, "2026-11-12 09:00")},
		},
		Tracker: fakeWeeklyTracker{items: []beads.WorkItem{
			closedItem(t, "item-a", "2026-11-11 16:00"),
			closedItem(t, "item-b", "2026-11-16 00:30"), // this week
			closedItem(t, "item-x", "2026-11-09 00:00"),
			closedItem(t, "item-y", "2026-11-15 12:00"),
			closedItem(t, "item-w", "2026-11-12 12:00"),
			{ID: "item-open", Status: "open"},
		}},
		Now: weeklyNow(t),
	}
	reading := ReadWeeklyCost(context.Background(), sources)
	if reading.Problem != "" || len(reading.NotRead) != 0 {
		t.Fatalf("reading = %+v", reading)
	}
	if len(reading.Weeks) != weeklyCostWeeks {
		t.Fatalf("weeks = %d, want %d", len(reading.Weeks), weeklyCostWeeks)
	}
	last := reading.Weeks[2]
	if last.From != "2026-11-09" || last.To != "2026-11-15" || last.Current {
		t.Fatalf("last week = %+v", last)
	}
	if last.CostUSD != 100 || last.Calls != 5 || last.Unpriced != 1 || !last.Floor {
		t.Fatalf("last week's spend = %v over %d calls, %d unpriced", last.CostUSD, last.Calls, last.Unpriced)
	}
	if last.NoItemUSD != 40 || last.NoItemCalls != 2 {
		t.Fatalf("last week's spend naming no item = %v over %d calls", last.NoItemUSD, last.NoItemCalls)
	}
	if last.Closed == nil || *last.Closed != 4 || last.CostPerClosedItem == nil || *last.CostPerClosedItem != 25 {
		t.Fatalf("closed = %v at %v each", last.Closed, last.CostPerClosedItem)
	}
	if last.Landed == nil || *last.Landed != 2 || last.Published == nil || *last.Published != 1 || last.CostPerLandedChange == nil || *last.CostPerLandedChange != 50 {
		t.Fatalf("landed = %v, published = %v, at %v each", last.Landed, last.Published, last.CostPerLandedChange)
	}
	if last.ManagementUSD != 40 || last.ManagementShare != 0.4 {
		t.Fatalf("management = %v, share %v", last.ManagementUSD, last.ManagementShare)
	}
	var order []string
	for _, role := range last.Roles {
		order = append(order, role.Says)
	}
	if got := strings.Join(order, ", "); got != "developer runs, reviewers, Lead Product Manager, development manager" {
		t.Fatalf("roles = %s", got)
	}
	developer := last.Roles[0]
	if developer.CostUSD != 50 || developer.Share != 0.5 || *developer.PerClosedItem != 12.5 || *developer.PerLandedChange != 25 {
		t.Fatalf("developer runs = %+v", developer)
	}
	for _, phrase := range []string{
		"at least $100.00 known spend across 5 provider calls, 1 of them with no cost reported",
		"4 items closed, $25.00 per item",
		"2 code changes landed, $50.00 per change",
		"1 document published, not counted as code changes",
		"management roles 40% of known spend",
	} {
		if !strings.Contains(last.Says, phrase) {
			t.Errorf("last week says %q, want it to contain %q", last.Says, phrase)
		}
	}
	if !strings.Contains(last.RolesSays, "developer runs $50.00 ($12.50 per closed item, $25.00 per landed change)") {
		t.Errorf("by role says %q", last.RolesSays)
	}

	this := reading.Weeks[3]
	if !this.Current || this.From != "2026-11-16" || *this.Closed != 1 || *this.Landed != 0 || this.CostPerLandedChange != nil {
		t.Fatalf("this week = %+v", this)
	}
	before := reading.Weeks[1]
	if before.CostUSD != 1000 || *before.Closed != 0 || *before.Landed != 0 {
		t.Fatalf("the week before = %+v", before)
	}
}

func TestWeeklyCostSaysAWeekWithNothingAsNothing(t *testing.T) {
	reading := ReadWeeklyCost(context.Background(), WeeklyCostSources{
		Spend:    fakeAccomplishmentSpend{},
		Runs:     nil,
		RunsRead: true,
		Tracker:  fakeWeeklyTracker{},
		Now:      weeklyNow(t),
	})
	for _, week := range reading.Weeks {
		if week.CostUSD != 0 || week.Calls != 0 || *week.Closed != 0 || *week.Landed != 0 || week.CostPerClosedItem != nil || week.CostPerLandedChange != nil || week.Floor || len(week.Roles) != 0 {
			t.Fatalf("an empty week = %+v", week)
		}
		if week.Says != "nothing spent; no item closed; no code change landed" || week.RolesSays != "no role spent anything" {
			t.Fatalf("an empty week says %q and %q", week.Says, week.RolesSays)
		}
	}
}

func TestWeeklyCostGivesNoFigureDividedByACountItCouldNotRead(t *testing.T) {
	reading := ReadWeeklyCost(context.Background(), WeeklyCostSources{
		Spend:       fakeAccomplishmentSpend{weeklySpend(atPacific(t, "2026-11-17 09:00"), domain.RoleDeveloper, "item-a", 30)},
		RunsProblem: "the run records could not be read: permission denied",
		Tracker:     fakeWeeklyTracker{err: errors.New("bd timed out")},
		Now:         weeklyNow(t),
	})
	this := reading.Weeks[len(reading.Weeks)-1]
	if this.Closed != nil || this.Landed != nil || this.CostPerClosedItem != nil || this.CostPerLandedChange != nil || this.Roles[0].PerClosedItem != nil {
		t.Fatalf("a week whose counts could not be read = %+v", this)
	}
	if !strings.Contains(this.Says, "items closed not known") || !strings.Contains(this.Says, "code changes landed not known") {
		t.Fatalf("the week says %q", this.Says)
	}
	if len(reading.NotRead) != 2 || !strings.Contains(reading.NotRead[0], "permission denied") || !strings.Contains(reading.NotRead[1], "bd timed out") {
		t.Fatalf("not read = %v", reading.NotRead)
	}

	unread := ReadWeeklyCost(context.Background(), WeeklyCostSources{SpendProblem: "no state root", Now: weeklyNow(t)})
	if unread.Problem == "" || len(unread.Weeks) != 0 {
		t.Fatalf("a reading with no spend log = %+v", unread)
	}
	if !strings.Contains(unread.Render(), "could not be read just now") {
		t.Fatalf("it renders as %q", unread.Render())
	}
}

func TestWeeklyCostRendersTheCurrentWeekAndTheOneBefore(t *testing.T) {
	reading := ReadWeeklyCost(context.Background(), WeeklyCostSources{
		Spend: fakeAccomplishmentSpend{
			weeklySpend(atPacific(t, "2026-11-17 09:00"), domain.RoleDeveloper, "item-a", 30),
			weeklySpend(atPacific(t, "2026-11-10 09:00"), domain.RoleReviewer, "item-b", 12),
			weeklySpend(atPacific(t, "2026-11-03 09:00"), domain.RoleArchitect, "", 99),
		},
		RunsRead: true,
		Runs:     []runstate.State{landedRun(t, "run-a", "item-a", "2026-11-17 10:00", false)},
		Tracker:  fakeWeeklyTracker{items: []beads.WorkItem{closedItem(t, "item-a", "2026-11-17 11:00")}},
		Now:      weeklyNow(t),
	})
	rendered := reading.Render()
	want := "Weekly cost, Monday to Sunday in America/Los_Angeles:\n" +
		"  This week, from 2026-11-16: $30.00 known spend across 1 provider call; 1 item closed, $30.00 per item; 1 code change landed, $30.00 per change; management roles 0% of known spend\n" +
		"    by role: developer runs $30.00 ($30.00 per closed item, $30.00 per landed change)\n" +
		"  Last week, 2026-11-09 to 2026-11-15: $12.00 known spend across 1 provider call; no item closed; no code change landed; management roles 0% of known spend\n" +
		"    by role: reviewers $12.00\n"
	if rendered != want {
		t.Fatalf("rendered:\n%s\nwant:\n%s", rendered, want)
	}
}

func TestAccomplishmentCarriesTheWeeklyCostForItsWeeks(t *testing.T) {
	reading := ReadAccomplishment(context.Background(), AccomplishmentSources{
		Spend:   fakeAccomplishmentSpend{weeklySpend(atPacific(t, "2026-11-10 09:00"), domain.RoleDeveloper, "item-a", 30)},
		Runs:    fakeAccomplishmentRuns{states: []runstate.State{landedRun(t, "run-a", "item-a", "2026-11-11 15:00", false)}},
		Tracker: fakeWeeklyTracker{items: []beads.WorkItem{closedItem(t, "item-a", "2026-11-11 16:00")}},
		Now:     weeklyNow(t),
	}, AccomplishmentRange{})
	if len(reading.WeeklyCost.Weeks) != len(reading.Weeks) {
		t.Fatalf("weekly cost weeks = %d, accomplishment weeks = %d", len(reading.WeeklyCost.Weeks), len(reading.Weeks))
	}
	last := reading.WeeklyCost.Weeks[len(reading.WeeklyCost.Weeks)-2]
	if last.From != "2026-11-09" || *last.Closed != 1 || *last.Landed != 1 || *last.CostPerLandedChange != 30 {
		t.Fatalf("last week = %+v", last)
	}
}
