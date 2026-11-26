package epiccost_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epiccost"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	product            = domain.ProductID("yoyodyne")
	epicID, otherEpic  = "yoyodyne-9k1y", "yoyodyne-5def"
	member, removed    = "yoyodyne-9k1y.2", "yoyodyne-9k1y.3"
	otherMember, piece = "yoyodyne-5def.1", "yoyodyne-9k1y.4"
)

var at = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

// bench is a state root with two open epics and the run records under it.
type bench struct {
	t        *testing.T
	root     string
	runs     *runstate.Store
	progress *epicprogress.Store
}

func newBench(t *testing.T) *bench {
	t.Helper()
	root := t.TempDir()
	operator, err := epicscope.ForOperator(root, product)
	if err != nil {
		t.Fatal(err)
	}
	for epic, items := range map[string][]string{epicID: {member, removed}, otherEpic: {otherMember}} {
		if _, err := operator.Start(epicscope.Naming{Epic: epic, From: "terminal", Members: items}, at); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := runstate.NewStore(root, product)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := epicprogress.Open(root, product)
	if err != nil {
		t.Fatal(err)
	}
	return &bench{t: t, root: root, runs: runs, progress: progress}
}

// ran records a run of an item whose start was reserved for its epics, whose
// developer and reviewer each cost what is given. A negative cost is an
// invocation whose log is gone.
func (b *bench) ran(item string, costs ...float64) string {
	b.t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		b.t.Fatal(err)
	}
	if _, err := b.progress.ReserveRun(runID, []string{item}); err != nil {
		b.t.Fatal(err)
	}
	b.record(runID, item, costs...)
	return runID
}

func (b *bench) record(runID, item string, costs ...float64) {
	b.t.Helper()
	completed := at
	state := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: product, RepositoryID: "yoyodyne", WorkItemID: item,
		Backend: domain.BackendClaudeCode, Status: runstate.StatusSucceeded, StartedAt: at, UpdatedAt: at, CompletedAt: &completed,
		ProviderSessionID: "session-developer", ReviewSessionID: "session-reviewer"}
	if err := b.runs.Create(state); err != nil {
		b.t.Fatal(err)
	}
	roles := []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer}
	for index, cost := range costs {
		if cost < 0 {
			continue
		}
		role := roles[index%len(roles)]
		event, err := execution.NewEvent(runID, uint64(index+1), at, execution.EventRunCompleted, "claude-code",
			map[string]any{"role": string(role), "session_id": "session-" + string(role), "total_cost_usd": cost})
		if err != nil {
			b.t.Fatal(err)
		}
		if err := b.runs.AppendEvent(event); err != nil {
			b.t.Fatal(err)
		}
	}
}

func (b *bench) read(epic string) epiccost.Standing {
	b.t.Helper()
	record, err := epicscope.Read(b.root, product)
	if err != nil {
		b.t.Fatal(err)
	}
	view, err := epicprogress.Read(b.root, product, record)
	if err != nil {
		b.t.Fatal(err)
	}
	named, _ := record.Named(epic)
	standing, err := epiccost.Read(b.root, product, view, named)
	if err != nil {
		b.t.Fatal(err)
	}
	return standing
}

func (b *bench) setBudget(epic string, limit float64) {
	b.t.Helper()
	operator, err := epicscope.ForOperator(b.root, product)
	if err != nil {
		b.t.Fatal(err)
	}
	if _, err := operator.SetBudget(epic, limit, at); err != nil {
		b.t.Fatal(err)
	}
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

// A member's runs are counted with their reviews, each run once, and another
// epic's run is not counted at all.
func TestAnEpicCostsWhatTheRunsMadeForItCost(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.ran(member, 6, 1.5)
	b.ran(member, 2)
	b.ran(otherMember, 100)
	got := b.read(epicID)
	if !near(got.KnownUSD, 9.5) || got.Runs != 2 || got.UnknownRuns != 0 {
		t.Fatalf("the epic's cost is %+v, want $9.50 over 2 runs, the reviewer's $1.50 included and the other epic's run left out", got.Spend)
	}
	if other := b.read(otherEpic); !near(other.KnownUSD, 100) || other.Runs != 1 {
		t.Fatalf("the other epic's cost is %+v, want $100 over 1 run", other.Spend)
	}
}

// A run that cannot be priced is counted and shown as unknown, never as nothing,
// and is not in the figure a limit is measured against.
func TestARunWhoseCostIsUnknownIsCountedApartAndNotTowardTheLimit(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.ran(member, 4)
	b.ran(member, -1)
	// A start reserved whose run record cannot be read.
	lost, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.progress.ReserveRun(lost, []string{member}); err != nil {
		t.Fatal(err)
	}
	b.setBudget(epicID, 4.5)
	got := b.read(epicID)
	if !near(got.KnownUSD, 4) || got.Runs != 3 || got.UnknownRuns != 2 {
		t.Fatalf("the cost is %+v, want $4 known over 3 runs with 2 unknown", got.Spend)
	}
	if got.Refuses() {
		t.Fatal("unknown cost was counted toward the limit")
	}
	says := got.Says(epicID)
	for _, want := range []string{"$4.00 in provider reported cost", "2 runs whose cost is unknown", "not counted toward a limit"} {
		if !strings.Contains(says, want) {
			t.Errorf("%q does not say %q", says, want)
		}
	}
}

// A run reserved for two epics is shared between them, so the two add up to
// what it cost once.
func TestARunForTwoEpicsIsNotCountedTwice(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.progress.ReserveRun(runID, []string{member, otherMember}); err != nil {
		t.Fatal(err)
	}
	b.record(runID, member, 10)
	first, second := b.read(epicID), b.read(otherEpic)
	if !near(first.KnownUSD, 5) || !near(second.KnownUSD, 5) {
		t.Fatalf("the two epics cost %v and %v, want $5 each and $10 between them", first.KnownUSD, second.KnownUSD)
	}
	// Reserving it again adds nothing.
	if _, err := b.progress.ReserveRun(runID, []string{member}); err != nil {
		t.Fatal(err)
	}
	if again := b.read(epicID); !near(again.KnownUSD, 5) || again.Runs != 1 {
		t.Fatalf("a run reserved again is counted as %+v", again.Spend)
	}
}

// What an item cost before the operator took it out stays counted.
func TestARemovedItemsEarlierCostStaysCounted(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.ran(removed, 3)
	b.ran(member, 1)
	operator, err := epicscope.ForOperator(b.root, product)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Remove(epicID, removed, "not needed", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := b.read(epicID); !near(got.KnownUSD, 4) || got.Runs != 2 {
		t.Fatalf("after the removal the cost is %+v, want $4 over 2 runs", got.Spend)
	}
}

// A paid call reserved for the epic that is not a run is counted as unknown.
func TestAnotherPaidCallIsCountedAsUnknown(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	naming := epicprogress.Naming{WorkItemID: epicID, NamedAt: at}
	if _, err := b.progress.Call(naming, "judgment:1", "judging a piece", nil, func() (string, error) { return "inside", nil }); err != nil {
		t.Fatal(err)
	}
	got := b.read(epicID)
	if got.UnpricedCalls != 1 || got.KnownUSD != 0 {
		t.Fatalf("the cost is %+v, want one paid call counted as unknown and nothing known", got.Spend)
	}
	if says := got.Says(epicID); !strings.Contains(says, "1 other paid call whose cost is unknown") {
		t.Errorf("%q does not count the call", says)
	}
}

// The limit: none unless set, the notice at four fifths, reaching it, and the
// amount above it.
func TestWhereAnEpicStandsAgainstItsLimit(t *testing.T) {
	t.Parallel()
	limit := &epicscope.Budget{LimitUSD: 10, SetAt: at}
	for name, test := range map[string]struct {
		standing      epiccost.Standing
		refuses, near bool
		above         float64
		says          []string
	}{
		"no limit":        {epiccost.Standing{Spend: epiccost.Spend{KnownUSD: 50, Runs: 2}}, false, false, 0, []string{"no budget is set", "To set the limit: yoyo epic budget " + epicID}},
		"below":           {epiccost.Standing{Spend: epiccost.Spend{KnownUSD: 7.99, Runs: 1}, Budget: limit}, false, false, 0, []string{"of a limit of $10.00", "To change the limit"}},
		"four fifths":     {epiccost.Standing{Spend: epiccost.Spend{KnownUSD: 8, Runs: 1}, Budget: limit}, false, true, 0, []string{"four fifths or more of its limit of $10.00"}},
		"at the limit":    {epiccost.Standing{Spend: epiccost.Spend{KnownUSD: 10, Runs: 1}, Budget: limit}, true, false, 0, []string{"has reached its limit of $10.00", "what is under way finishes"}},
		"above the limit": {epiccost.Standing{Spend: epiccost.Spend{KnownUSD: 12.5, Runs: 3}, Budget: limit}, true, false, 2.5, []string{"$2.50 above its limit of $10.00", "nothing new starts in it"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := test.standing
			if got.Refuses() != test.refuses || got.Near() != test.near || !near(got.Above(), test.above) {
				t.Fatalf("refuses %v, near %v, above %v; want %v, %v, %v", got.Refuses(), got.Near(), got.Above(), test.refuses, test.near, test.above)
			}
			if (got.Why() != "") != test.refuses {
				t.Errorf("why = %q", got.Why())
			}
			says := got.Says(epicID)
			for _, want := range append(test.says, "in provider reported cost") {
				if !strings.Contains(says, want) {
					t.Errorf("%q does not say %q", says, want)
				}
			}
		})
	}
}
