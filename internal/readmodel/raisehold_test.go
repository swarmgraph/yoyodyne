package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// raisedRun is a run that ended by its reviewer raising the item as unmeetable:
// it succeeded, carries no blocker, and left its change on its branch.
func raisedRun(runID, workItemID string, at time.Time) runstate.State {
	return runstate.State{
		RunID:          runID,
		WorkItemID:     workItemID,
		Status:         runstate.StatusSucceeded,
		StartedAt:      at,
		UpdatedAt:      at,
		Branch:         "yoyodyne/" + workItemID + "/" + runID,
		WorktreePath:   "/state/worktrees/" + runID,
		ReviewDecision: runstate.ReviewEscalate,
		ReviewSummary:  "the done-means requires a review no developer can produce",
	}
}

// A raise holds nothing by itself once its owner has released it: that release
// is what lets a pull select the item. What holds it is a re-run the development
// manager has decided and the harness has not carried out, because that re-run
// starts from the raise's preserved change and a pull would start over beside it.
// Once the re-run is the item's latest run, nothing here holds it.
func TestARaiseIsHeldOnlyForADecidedRerunNotYetCarriedOut(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 27, 7, 0, 0, 0, time.UTC)
	raise := raisedRun("run-21d916b6", "yoyodyne-ifd.437.13", at)
	rerun := decisions(map[string]runstate.TriageCounters{
		"yoyodyne-ifd.437.13": {Decisions: []runstate.TriageDecision{{Decision: runstate.TriageDecisionRerun, RunID: raise.RunID}}},
	})

	if _, held := heldForAPerson([]runstate.State{raise}, nil, nothingDecided, asRecorded).Reason("yoyodyne-ifd.437.13"); held {
		t.Fatalf("a raise nobody decided a re-run of is held, want its release to leave it pullable")
	}

	held := heldForAPerson([]runstate.State{raise}, nil, rerun, asRecorded)
	reason := heldReason(t, held, "yoyodyne-ifd.437.13")
	if !strings.Contains(reason, "raised it as one that cannot be met") || !strings.Contains(reason, "already decided") {
		t.Fatalf("held for %q, want the decided re-run of the raise named as the harness's move", reason)
	}
	if !held.Decided("yoyodyne-ifd.437.13") {
		t.Fatalf("the hold names the development manager, want the harness carrying her decision out")
	}

	fresh := runstate.State{RunID: "run-fedcba98", WorkItemID: "yoyodyne-ifd.437.13", Status: runstate.StatusRunning, StartedAt: at.Add(time.Hour), UpdatedAt: at.Add(time.Hour)}
	if _, held := heldForAPerson([]runstate.State{raise, fresh}, nil, rerun, asRecorded).Reason("yoyodyne-ifd.437.13"); held {
		t.Fatalf("the item is still held for a re-run that has started")
	}
	raise.UpdatedAt = fresh.UpdatedAt.Add(time.Hour)
	if reason, held := heldForAPerson([]runstate.State{fresh, raise}, nil, rerun, asRecorded).Reason(raise.WorkItemID); held {
		t.Fatalf("maintenance of the raising run restored a carried-out re-run hold: %q", reason)
	}
}

// The shape yoyodyne-ifd.437.13 was in, read from every record production keeps
// and taken through the backlog's own readiness rather than through the hold
// alone. The raise was delivered to the development manager and answered with
// no decision recorded on the delivery; her escalation stands on the item's
// triage record; and the item reads blocked. Once its owner has amended it and
// released the raise's parking, it is the next thing to pull, whatever its
// status says. Before the release the raise's parking is what holds it, and a
// re-run she has decided holds it until that re-run starts.
func TestAReleasedRaiseIsPullableFromTheRecordsProductionKeeps(t *testing.T) {
	t.Parallel()

	const item = "yoyodyne-ifd.437.13"
	at := time.Date(2026, 9, 27, 7, 0, 0, 0, time.UTC)
	raise := raisedRun("run-21d916b6", item, at)
	delivered := at.Add(time.Minute)
	stoppages := fakeStoppages{
		runs: []runstate.State{raise},
		escalations: []runstate.Escalation{{
			DocketKey:   triage.Key(triage.ClassEscalation, raise.RunID),
			RunID:       raise.RunID,
			WorkItemID:  item,
			Attempts:    1,
			DeliveredAt: &delivered,
		}},
	}
	escalated := recordedDecisions{item: {Decisions: []runstate.TriageDecision{{
		Decision: runstate.TriageDecisionEscalate, RunID: raise.RunID, DecidedAt: at.Add(time.Hour),
	}}}}

	pull := func(t *testing.T, decisions Decisions, parking domain.WorkItemParking, status string) backlog.Entry {
		t.Helper()
		held, err := HeldForAPerson(context.Background(), stoppages, decisions, nil)
		if err != nil {
			t.Fatalf("HeldForAPerson() error = %v", err)
		}
		items := []beads.WorkItem{{ID: item, Title: "README rewrite", Status: status, Parking: parking}}
		// A blocked item is never in the tracker's own ready answer, which is
		// the status nothing maintains; an open one is.
		var ready []string
		if status == "open" {
			ready = []string{item}
		}
		return backlog.Order(items, ready, held, nil).Entries[0]
	}

	parked := domain.WorkItemParking(runstate.RaiseParking(raise.RunID, "the architect's review is not recorded"))
	if entry := pull(t, escalated, parked, "blocked"); entry.Ready {
		t.Fatalf("entry = %#v, want the raise's parking to hold the item until its owner releases it", entry)
	}
	for _, status := range []string{"open", "blocked"} {
		entry := pull(t, escalated, "", status)
		if !entry.Ready || entry.Awaiting != "" {
			t.Fatalf("%s entry = %#v, want the released raise pullable with nothing holding it", status, entry)
		}
	}

	rerun := recordedDecisions{item: {Decisions: []runstate.TriageDecision{{
		Decision: runstate.TriageDecisionRerun, RunID: raise.RunID, DecidedAt: at.Add(time.Hour),
	}}}}
	entry := pull(t, rerun, "", "open")
	if entry.Ready || !entry.AwaitingCarryOut || !strings.Contains(entry.Awaiting, "raised it as one that cannot be met") {
		t.Fatalf("entry = %#v, want the item held for the decided re-run the harness has not started", entry)
	}
}
