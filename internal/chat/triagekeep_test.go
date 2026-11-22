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
	rendered = renderTrackerOutcomes(domain.RoleDevelopmentManager, named.Actions)
	if strings.Contains(rendered, "still stands") {
		t.Fatal("a wait that did replace the repair was reported as leaving it standing")
	}
	for _, want := range []string{"this replaces the repair you decided on run " + stoppedRun, "had not been carried out yet", "will not carry it out", "decide it again"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("she was not told her wait ended the repair (%q missing):\n%s", want, rendered)
		}
	}
}

// A re-run decided about a stopped run and not yet carried out is ended by a
// wait naming work recorded about the same run, whichever of the run's docket
// entries the wait was meant for, and she is told so as she records it rather
// than left reading the re-run as queued for a developer slot. A wait recorded
// once nothing is left to carry out says nothing of the kind.
func TestAWaitNamingWorkThatEndsADecidedRerunSaysSo(t *testing.T) {
	t.Parallel()

	gate := newRunStoreBudgetGate(t)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.432.25.15": {ID: "yoyodyne-ifd.432.25.15", Title: "the guides item", Status: "blocked"},
		"yoyodyne-ifd.428.99.31": {ID: "yoyodyne-ifd.428.99.31", Title: "the fix the publication waits on", Status: "open"},
	}}
	rerun := triageReply(t, tracker, gate, trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.432.25.15","run":"`+stoppedRun+`","decision":"rerun","reason":"a fresh run that resolves only the Makefile conflict"}`))
	if len(rerun.Actions) != 1 || !rerun.Actions[0].Applied {
		t.Fatalf("actions = %#v", rerun.Actions)
	}

	waited := triageReply(t, tracker, gate, trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.432.25.15","run":"`+stoppedRun+`","decision":"wait","waits_on":"yoyodyne-ifd.428.99.31","reason":"nothing more is owed on this publication; the fresh run carries the work"}`))
	if len(waited.Actions) != 1 || !waited.Actions[0].Applied {
		t.Fatalf("actions = %#v", waited.Actions)
	}
	standing, _ := gate.counters(t, "yoyodyne-ifd.432.25.15").DecisionOf(stoppedRun)
	if standing.Decision != runstate.TriageDecisionWait {
		t.Fatalf("decision = %#v, want the wait on named work in place of the re-run", standing)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, waited.Actions)
	for _, want := range []string{"this replaces the re-run you decided on run " + stoppedRun, "will not carry it out", "If you meant that re-run to stand, decide it again"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("she was not told her wait ended the re-run (%q missing):\n%s", want, rendered)
		}
	}

	// The same wait recorded again replaces only a wait, so it says nothing of an
	// ended re-run.
	again := triageReply(t, tracker, gate, trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.432.25.15","run":"`+stoppedRun+`","decision":"wait","waits_on":"yoyodyne-ifd.428.99.31","reason":"still nothing owed on this publication"}`))
	if len(again.Actions) != 1 || !again.Actions[0].Applied {
		t.Fatalf("actions = %#v", again.Actions)
	}
	if rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, again.Actions); strings.Contains(rendered, "this replaces the") {
		t.Fatalf("a wait that replaced only a wait was reported as ending a re-run:\n%s", rendered)
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
