package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestSettlementFindingsUseTheSharedAttentionAndOwner(t *testing.T) {
	t.Parallel()
	state := runstate.State{RunID: "run-refused", WorkItemID: "yoyodyne-refused", Status: runstate.StatusFailed, Phase: runstate.PhaseCleaningUp,
		ReconcileFindings: []runstate.ReconcileFinding{{Step: runstate.ReconcilePublication, Problem: "delete the merged remote branch: want the published commit; tip outside target"}}}
	if attention := ReconcileFindingAttention(state); attention.OwedStep.Status != state.Status || attention.OwedStep.Phase != state.Phase {
		t.Fatalf("finding lost the run status or phase: %+v", attention.OwedStep)
	}
	for _, attention := range []Attention{ReconcileFindingAttention(state), owedStepAttention(state), awaitingForgeAttention(state)} {
		if attention.Mover != MoverDevelopmentManager || !strings.Contains(attention.What(), state.ReconcileFindings[0].Problem) || !strings.Contains(attention.Whose(), "preserve any branch work outside the target") {
			t.Fatalf("attention = %+v, what = %q, whose = %q", attention, attention.What(), attention.Whose())
		}
	}
	state.ReconcileFindings[0].Problem = "ask the forge: answer unreadable"
	attention := ReconcileFindingAttention(state)
	if attention.Mover != MoverHarness || !strings.Contains(attention.Whose(), "restore forge access") {
		t.Fatalf("forge refusal = %+v, whose = %q", attention, attention.Whose())
	}
}

func TestResolvedSettlementFindingsLeaveOnlyTheHarnessDeliveryObligation(t *testing.T) {
	t.Parallel()
	state := runstate.State{RunID: "run-refused", WorkItemID: "yoyodyne-refused", Status: runstate.StatusSucceeded, Phase: runstate.PhaseComplete,
		ReconcileFindings: []runstate.ReconcileFinding{{Step: runstate.ReconcilePublication, Problem: "delete the merged remote branch: want the published commit; tip outside target", Pending: true, Resolved: true}}}
	attention := ReconcileFindingAttention(state)
	if attention.Mover != MoverHarness || !strings.Contains(attention.What(), "settlement of yoyodyne-refused finished") || !strings.Contains(attention.Whose(), "delivers any pending finding note") || strings.Contains(attention.Whose(), "preserve any branch work") {
		t.Fatalf("resolved finding = %+v, what = %q, whose = %q", attention, attention.What(), attention.Whose())
	}
	state.ReconcileFindings = append(state.ReconcileFindings, runstate.ReconcileFinding{Step: runstate.ReconcileRefresh, Problem: "forge answer unreadable"})
	attention = ReconcileFindingAttention(state)
	if attention.Mover != MoverHarness || !strings.Contains(attention.What(), "forge answer unreadable") || !strings.Contains(attention.What(), "completed settlements still need delivery") || !strings.Contains(attention.Whose(), "restore forge access") {
		t.Fatalf("mixed findings = %+v, what = %q, whose = %q", attention, attention.What(), attention.Whose())
	}
}
