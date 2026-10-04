package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestReconcileFindingsDoNotFailTheMaintenancePass(t *testing.T) {
	t.Parallel()
	state := runstate.State{RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-refused",
		ReconcileFindings: []runstate.ReconcileFinding{{Step: runstate.ReconcilePublication, Problem: "delete the merged remote branch: tip outside target, want the published commit"}}}
	finding := readmodel.ReconcileFindingAttention(state)
	sweep := reconcileSweep{
		Updates: []orchestrator.UpdateContinuation{{RunID: state.RunID, WorkItemID: state.WorkItemID, Failure: "validate resumed work item: closed", Finding: &finding}},
		Runs: []orchestrator.Reconciliation{
			{RunID: state.RunID, WorkItemID: state.WorkItemID, Action: orchestrator.ActionUnsettled, Failure: "forge answer unreadable", Finding: &finding},
			{RunID: "other-run", WorkItemID: "other-item", Action: orchestrator.ActionCompleted},
		},
		Recoveries:   []orchestrator.PublicationRecovery{{Failure: "forge unreadable", Finding: &finding}},
		Publications: []orchestrator.PublicationRefresh{{Failure: "forge unreadable", Finding: &finding}},
		Convergence: orchestrator.Convergence{
			Branches:     []orchestrator.BranchSweep{{Failure: "branch unreadable", Finding: &finding}},
			Worktrees:    []orchestrator.WorktreeSweep{{Failure: "checkout unreadable", Finding: &finding}},
			Publications: []orchestrator.PublicationSweep{{PublicationRetirement: orchestrator.PublicationRetirement{Failure: "forge unreadable"}, Finding: &finding}},
			Findings:     []orchestrator.ReconcileFindingMaintenance{{RunID: state.RunID, WorkItemID: state.WorkItemID, Finding: &finding, FindingProblem: "finding clearing save refused"}},
		},
		RedTargets:       []orchestrator.RedTargetResumption{{Failure: "target answer unreadable", Finding: &finding}},
		EscalationsEnded: []orchestrator.EscalationSettlement{{Failure: "item unreadable", Finding: &finding}},
		Settlements: []orchestrator.PublicationSettlement{
			{WorkItemID: state.WorkItemID, Number: 1, Remaining: state.ReconcileFindings[0].Problem, Finding: &finding},
			{WorkItemID: "other-item", Number: 2, Settled: true},
		},
	}
	var updatesOut, updatesErr bytes.Buffer
	if printUpdates(&updatesOut, &updatesErr, sweep.Updates) {
		t.Fatal("a queued continuation's per-item finding failed the text command")
	}
	for _, jsonOutput := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		if code := reportReconcileResult(&stdout, &stderr, jsonOutput, sweep, nil); code != 0 {
			t.Fatalf("code = %d, stdout = %s, stderr = %s", code, &stdout, &stderr)
		}
		for _, want := range []string{"yoyodyne-refused", "other-item", "preserve any branch work", "development manager"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("output = %s, want %q", &stdout, want)
			}
		}
		if !strings.Contains(stdout.String()+stderr.String(), "finding clearing save refused") {
			t.Fatalf("finding maintenance refusal was not reported: stdout = %s, stderr = %s", &stdout, &stderr)
		}
		if code := reportReconcileResult(&stdout, &stderr, jsonOutput, sweep, errors.New("discover outstanding runs: state unreadable")); code != 1 {
			t.Fatalf("unreadable pass state code = %d, want failure", code)
		}
	}
}
