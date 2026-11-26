package readmodel

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// costed is a stateBench read through the records the harness wires, with a run
// of the first member recorded and reserved for the epic at the cost given. A
// negative cost is a run whose record is gone.
func costed(t *testing.T, costs ...float64) *stateBench {
	t.Helper()
	b := newStateBench(t)
	store, err := runstate.NewStore(b.root, "yoyodyne")
	b.must(err)
	for _, cost := range costs {
		runID, err := runstate.NewRunID()
		b.must(err)
		b.entry.Reservations = append(b.entry.Reservations, epicprogress.Reservation{ID: "run:" + runID, For: "run " + runID, At: hours(1), HeldBy: "the record of run " + runID})
		if cost < 0 {
			continue
		}
		completed := hours(1)
		b.must(store.Create(runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: "yoyodyne", RepositoryID: "yoyodyne", WorkItemID: stateOne,
			Backend: domain.BackendClaudeCode, Status: runstate.StatusSucceeded, StartedAt: hours(1), UpdatedAt: hours(1), CompletedAt: &completed, ProviderSessionID: "session-developer"}))
		event, err := execution.NewEvent(runID, 1, hours(1), execution.EventRunCompleted, "claude-code",
			map[string]any{"role": string(domain.RoleDeveloper), "session_id": "session-developer", "total_cost_usd": cost})
		b.must(err)
		b.must(store.AppendEvent(event))
	}
	return b
}

// readCosted reads the epic as status does, with the cost the records give.
func (b *stateBench) readCosted() EpicStanding {
	b.t.Helper()
	b.read()
	b.sources.Epics = NewEpicRecords(b.root, "yoyodyne")
	epics := ReadStanding(context.Background(), b.sources).Epics
	if len(epics.Open) != 1 {
		b.t.Fatalf("open epics: %+v", epics.Open)
	}
	return epics.Open[0].EpicStanding
}

func (b *stateBench) setBudget(limit float64) {
	b.t.Helper()
	_, err := operatorOf(b.t, b.root).SetBudget(stateEpic, limit, hours(2))
	b.must(err)
}

func TestStatusShowsWhatANamedEpicCost(t *testing.T) {
	t.Parallel()
	b := costed(t, 2.5, 0.5, -1)
	standing := b.readCosted()
	if standing.Cost == nil || standing.Cost.Runs != 3 || standing.Cost.UnknownRuns != 1 {
		t.Fatalf("the cost is %+v (%s)", standing.Cost, standing.CostProblem)
	}
	wantSaid(t, standing.render(), "Cost: $3.00 in provider reported cost, over 3 runs; no budget is set",
		"Not in that figure, and not counted toward a limit: 1 run whose cost is unknown",
		"To set the limit: yoyo epic budget "+stateEpic+" <dollars>")
}

func TestStatusSaysWhenAnEpicIsAtFourFifthsOfItsLimit(t *testing.T) {
	t.Parallel()
	b := costed(t, 3.4)
	b.setBudget(4)
	standing := b.readCosted()
	wantSaid(t, standing.render(), "Cost: $3.40 in provider reported cost, over 1 run, which is four fifths or more of its limit of $4.00")
	if standing.State == EpicOutOfBudget || standing.Budget != "" {
		t.Fatalf("below its limit the epic is %q (%s)", standing.State, standing.Budget)
	}
}

// Past its limit, with nothing under way, the epic is out of budget, and status
// says by how much it is above.
func TestStatusShowsAnEpicOutOfBudgetAndByHowMuch(t *testing.T) {
	t.Parallel()
	b := costed(t, 3.4)
	b.setBudget(3)
	b.ready = []string{stateOne}
	b.set(stateTwo, func(item *beads.WorkItem) { item.Description = "human-gate: looked-at — the operator has looked\n" })
	standing := b.readCosted()
	if standing.State != EpicOutOfBudget {
		t.Fatalf("the state is %q (%s), want out of budget:\n%s", standing.State, standing.Problem, standing.render())
	}
	wantSaid(t, standing.render(), "Out of budget; the next start is refused by its budget: spent $3.40 of its limit of $3.00 in provider reported cost",
		"$0.40 above its limit of $3.00; nothing new starts in it, and what is under way finishes", "To change the limit: yoyo epic budget "+stateEpic)
}

// A run under way keeps the epic working, above its limit or not.
func TestAnEpicAboveItsLimitWithARunUnderWayIsWorking(t *testing.T) {
	t.Parallel()
	b := costed(t, 5)
	b.setBudget(3)
	b.inFlight = []runstate.State{{RunID: "run-under-way", WorkItemID: stateOne, Status: runstate.StatusRunning, StartedAt: hours(3)}}
	standing := b.readCosted()
	if standing.State != EpicWorking {
		t.Fatalf("the state is %q (%s), want working", standing.State, standing.Problem)
	}
	if !strings.Contains(standing.render(), "$2.00 above its limit") {
		t.Fatalf("status does not say by how much:\n%s", standing.render())
	}
}
