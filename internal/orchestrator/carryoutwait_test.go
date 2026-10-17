package orchestrator

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A decided re-run a pass offers and does not start is named on that pass's log
// with what it waits on, whether a slot, a shut gate, or ready work ahead of it
// is what keeps it back, and before anything is written onto its item.
func TestADecidedRerunThatWaitsIsNamedOnThePassLog(t *testing.T) {
	t.Parallel()
	first := CarryOutTask{WorkItemID: "yoyodyne-ifd.435.10", RunID: "run-07feebbe", Decision: runstate.TriageDecisionRerun}
	second := CarryOutTask{WorkItemID: "yoyodyne-ifd.433.25", RunID: "run-f2ee2b07", Decision: runstate.TriageDecisionRerun}
	gated := CarryOutTask{WorkItemID: "yoyodyne-ifd.429.68", RunID: "run-5d62f75e", Decision: runstate.TriageDecisionRerun}
	harness := newScheduleHarness()
	harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) {
		return []CarryOutTask{first, second, gated}, nil
	}
	schedule := Schedule{}
	waitingOn := map[string]string{gated.WorkItemID: runstate.TriageGateSpendingPause}
	chosen, passed, _ := Scheduler{}.nextCarryOuts(&schedule, Pull{CarryOut: harness}, map[string]runstate.State{}, nil, waitingOn, closedGates{pause: true}, 1, 0, nil)
	if len(chosen) != 1 || chosen[0].RunID != first.RunID {
		t.Fatalf("chosen = %+v, want the oldest re-run given the one free slot", chosen)
	}
	if _, ok := passed[second.RunID]; !ok {
		t.Fatalf("passed = %+v, want the second re-run passed over for the slot", passed)
	}
	if _, ok := passed[gated.RunID]; ok {
		t.Fatalf("passed = %+v, want the re-run its gate already refused left to the refusal on its item", passed)
	}

	rendered := schedule.Render()
	for _, want := range []string{
		"yoyodyne-ifd.433.25 was not started this pass: the re-run the development manager decided about run run-f2ee2b07 waits, because every developer slot this pull had was spent",
		"yoyodyne-ifd.429.68 was not started this pass: the re-run the development manager decided about run run-5d62f75e waits, because the operator's pause on harness spending refused its last attempt and is still shut",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the pass log reads:\n%s\nwant it to say %q", rendered, want)
		}
	}
	if strings.Contains(rendered, first.WorkItemID+" was not started") {
		t.Fatalf("the pass log reads:\n%s\nwant nothing said of the re-run it started as waiting", rendered)
	}
}

// A decision this pass also wrote onto its item as unattempted is said once.
func TestACarryOutWaitAlreadyWrittenUpIsNotSaidTwice(t *testing.T) {
	t.Parallel()
	task := CarryOutTask{WorkItemID: "yoyodyne-ifd.435.10", RunID: "run-07feebbe", Decision: runstate.TriageDecisionRerun}
	schedule := Schedule{CarryOutUnattempted: []CarriedOut{{WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision, Problem: "the \"rerun\" has not been attempted by any pass"}}}
	schedule.waitCarryOut(task, "every developer slot this pull had was spent on 1 decision(s) ahead of it on the docket")
	if rendered := schedule.Render(); strings.Contains(rendered, "was not started this pass") {
		t.Fatalf("the pass log reads:\n%s\nwant the decision said once, as written onto its item", rendered)
	}
}
