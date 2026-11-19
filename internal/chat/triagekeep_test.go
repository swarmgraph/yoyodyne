package chat

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// runStoreBudgetGate is the durable budget wired the way the command line wires
// it for the development manager: a decision that spends nothing is recorded
// through the run store, which reads the run's own record and the item's re-run
// claims beside the ledger.
type runStoreBudgetGate struct {
	*triageBudgetGate
	runs *runstate.Store
}

func newRunStoreBudgetGate(t *testing.T) runStoreBudgetGate {
	t.Helper()

	runs, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return runStoreBudgetGate{
		triageBudgetGate: &triageBudgetGate{
			store:  runs.Triage(),
			caps:   runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 2, Reruns: 2, MergeRearms: 1},
			rounds: 2,
		},
		runs: runs,
	}
}

func (g runStoreBudgetGate) RecordDecision(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.TriageCounters, error) {
	return g.runs.RecordTriageDecision(ctx, workItemID, decision, g.now())
}

// A repair decided and still waiting for a developer slot is what a later wait
// with nothing named to wait on is waiting for. The wait leaves the repair
// standing, and she is told so as she records it, with what to record instead
// if she meant to cancel it.
func TestAWaitNamingNoWorkLeavesADecidedRepairStanding(t *testing.T) {
	t.Parallel()

	gate := newRunStoreBudgetGate(t)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.311": {ID: "yoyodyne-ifd.311", Title: "the item that stopped", Status: "open"},
		"yoyodyne-ifd.398": {ID: "yoyodyne-ifd.398", Title: "the work it depends on", Status: "open"},
	}}
	repaired := triageReply(t, tracker, gate, trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.311","run":"`+stoppedRun+`","decision":"repair","reason":"move the kept change onto current main and check and review it again"}`))
	if len(repaired.Actions) != 1 || !repaired.Actions[0].Applied {
		t.Fatalf("actions = %#v", repaired.Actions)
	}

	// The run's pull request is put to her and she answers it with a wait.
	waited := triageReply(t, tracker, gate, trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.311","run":"`+stoppedRun+`","decision":"wait","reason":"waiting for the repair to get a developer slot"}`))
	if len(waited.Actions) != 1 || !waited.Actions[0].Applied {
		t.Fatalf("actions = %#v", waited.Actions)
	}
	standing, found := gate.counters(t, "yoyodyne-ifd.311").DecisionOf(stoppedRun)
	if !found || standing.Decision != runstate.TriageDecisionRepair {
		t.Fatalf("decision = %#v, want the repair still standing under a wait that names no work", standing)
	}
	if !gate.counters(t, "yoyodyne-ifd.311").AwaitingCarryOut(stoppedRun) {
		t.Fatal("the repair no longer reads as waiting for the harness to carry it out")
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, waited.Actions)
	for _, want := range []string{"the repair you decided on this run", "does not cancel it", "still stands", "naming the work item it waits on"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("she was not told the repair still stands (%q missing):\n%s", want, rendered)
		}
	}

	// A wait naming the work it depends on is a decision about what happens next,
	// and it replaces the repair.
	named := triageReply(t, tracker, gate, trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.311","run":"`+stoppedRun+`","decision":"wait","waits_on":"yoyodyne-ifd.398","reason":"the repair is moot until the work it depends on lands"}`))
	if len(named.Actions) != 1 || !named.Actions[0].Applied {
		t.Fatalf("actions = %#v", named.Actions)
	}
	standing, _ = gate.counters(t, "yoyodyne-ifd.311").DecisionOf(stoppedRun)
	if standing.Decision != runstate.TriageDecisionWait || standing.WaitsOn != "yoyodyne-ifd.398" {
		t.Fatalf("decision = %#v, want the wait on named work in place of the repair", standing)
	}
	if strings.Contains(renderTrackerOutcomes(domain.RoleDevelopmentManager, named.Actions), "still stands") {
		t.Fatal("a wait that did replace the repair was reported as leaving it standing")
	}
}

// A repair decided about a stopped run is her answer for the whole run, its
// pull request included, so the repair closes the pull request's entry too
// rather than leaving the same run to be put to her a second time.
func TestARepairClosesTheRunsPublicationEntryToo(t *testing.T) {
	t.Parallel()

	gate := newRunStoreBudgetGate(t)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.311": {ID: "yoyodyne-ifd.311", Title: "the item that stopped", Status: "open"},
	}}
	docket := &fakeDocket{closes: 2}
	options := triageOptions(t, tracker, gate, trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.311","run":"`+stoppedRun+`","decision":"repair","reason":"move the kept change onto current main"}`))
	options.Reports = &fakeReports{}
	options.Docket = docket
	reply := triageSend(t, options)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(docket.closed) != 1 {
		t.Fatalf("closed = %#v, want one closure", docket.closed)
	}
	for _, class := range []triage.Class{triage.ClassStoppedRun, triage.ClassUnstartedRun, triage.ClassEscalation, triage.ClassPublication} {
		if !slices.Contains(docket.closed[0].Classes, class) {
			t.Fatalf("classes = %#v, want %q among what a repair answers", docket.closed[0].Classes, class)
		}
	}
}
