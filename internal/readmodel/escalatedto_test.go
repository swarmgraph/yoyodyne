package readmodel

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An escalation up the ladder is the role's it names, and reaches that role
// through the report addressed to it: it is never a finding for the operator.
// One to the operator for a reason on his closed list still is.
func TestAnEscalationToARoleIsNotAFindingForTheOperator(t *testing.T) {
	t.Parallel()

	stopped := moment.Add(-24 * time.Hour)
	runs := []runstate.State{
		heldRun("run-401a", "yoyodyne-ifd.401", stopped),
		heldRun("run-402a", "yoyodyne-ifd.402", stopped),
		heldRun("run-403a", "yoyodyne-ifd.403", stopped),
	}
	escalate := func(run string, to domain.AgentRole, reason ownership.OperatorReason) runstate.TriageDecision {
		return runstate.TriageDecision{
			Decision: runstate.TriageDecisionEscalate, RunID: run, Reason: "it cannot go on as it stands",
			DecidedBy: "development-manager", Conversation: "chat-dm", Turn: 3, DecidedAt: stopped.Add(time.Hour),
			EscalatedTo: to, OperatorReason: reason,
		}
	}
	decisions := recordedDecisions{
		"yoyodyne-ifd.401": {Decisions: []runstate.TriageDecision{escalate("run-401a", domain.RoleProductManager, "")}},
		"yoyodyne-ifd.402": {Decisions: []runstate.TriageDecision{escalate("run-402a", domain.RoleArchitect, "")}},
		"yoyodyne-ifd.403": {Decisions: []runstate.TriageDecision{escalate("run-403a", "", ownership.OperatorCredential)}},
	}

	escalated, problem := EscalatedOperatorActions(runs, decisions, nil, nil)
	if problem != "" || len(escalated) != 1 || escalated[0].WorkItemID != "yoyodyne-ifd.403" {
		t.Fatalf("EscalatedOperatorActions() = %#v, %q, want only the escalation to the operator", escalated, problem)
	}
}
