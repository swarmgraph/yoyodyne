package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
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

// An escalation recorded before the closed list was enforced names the operator
// and gives no reason, and is not his: it goes back down the ladder to the
// development manager, whose decision about the kept run it is, and the ending
// says so. One with a reason on the list is still his.
func TestAnEscalationWithoutAClosedListReasonGoesBackToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	stopped := moment.Add(-24 * time.Hour)
	escalate := func(run string, reason ownership.OperatorReason) runstate.TriageDecision {
		return runstate.TriageDecision{
			Decision: runstate.TriageDecisionEscalate, RunID: run, Reason: "only a person can say what becomes of this",
			DecidedBy: "development-manager", Conversation: "chat-dm", Turn: 3, DecidedAt: stopped.Add(time.Hour),
			OperatorReason: reason,
		}
	}
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"blocked": {
			{ID: "yoyodyne-ifd.153", Title: "Escalated before the list", Status: "blocked"},
			{ID: "yoyodyne-ifd.403", Title: "Escalated for a credential", Status: "blocked"},
		}},
	}}
	runs := []runstate.State{
		heldRun("run-153a", "yoyodyne-ifd.153", stopped),
		heldRun("run-403a", "yoyodyne-ifd.403", stopped),
	}
	sources.Stoppages = fakeStoppages{runs: runs}
	sources.Decisions = recordedDecisions{
		"yoyodyne-ifd.153": {Decisions: []runstate.TriageDecision{escalate("run-153a", "")}},
		"yoyodyne-ifd.403": {Decisions: []runstate.TriageDecision{escalate("run-403a", ownership.OperatorCredential)}},
	}
	// The repository still holds both runs' changes.
	sources.Remains = survivingExcept{}

	standing := ReadStanding(context.Background(), sources)
	var operators []string
	heldByHer := 0
	for _, entry := range standing.NeedsHuman {
		switch {
		case entry.Kind == AttentionOperatorAction:
			operators = append(operators, entry.OperatorAction.WorkItemID)
		case entry.Kind == AttentionHeldWork && entry.Mover == MoverDevelopmentManager:
			heldByHer = entry.HeldWork.Count
		}
	}
	if len(operators) != 1 || operators[0] != "yoyodyne-ifd.403" {
		t.Fatalf("operator findings = %v, want only the escalation with a closed-list reason", operators)
	}
	if heldByHer != 2 {
		t.Fatalf("held work waiting on the development manager = %d, want both kept runs, the one escalated without a reason among them", heldByHer)
	}

	standingActions, ended, problem := Escalations(runs, sources.Decisions, nil, nil)
	if problem != "" || len(standingActions) != 1 || standingActions[0].RunID != "run-403a" || len(ended) != 1 || ended[0].RunID != "run-153a" || ended[0].Recorded {
		t.Fatalf("Escalations() = %#v, %#v, %q, want run-403a standing and run-153a ended and not yet recorded", standingActions, ended, problem)
	}
	for _, want := range []string{
		"it was recorded before an escalation to the operator had to give a reason",
		"so it is not his",
		"what becomes of run run-153a is the development manager's decision",
		"listed under her as a stoppage waiting on that decision",
	} {
		if !strings.Contains(ended[0].Why, want) {
			t.Fatalf("ending = %q, want it to say %q", ended[0].Why, want)
		}
	}
}

// The ending the sweep recorded on a run is about the escalation it ended. Once
// the development manager decides that run again and escalates it to the
// operator for a reason on his list, the old ending does not hide the new one.
func TestAnEndingRecordedOnARunDoesNotEndALaterEscalation(t *testing.T) {
	t.Parallel()

	stopped := moment.Add(-24 * time.Hour)
	run := heldRun("run-153a", "yoyodyne-ifd.153", stopped)
	run.EscalationEnded = &runstate.EscalationEnding{At: stopped.Add(2 * time.Hour), Why: unreasonedEscalationEnding("run-153a")}
	again := runstate.TriageDecision{
		Decision: runstate.TriageDecisionEscalate, RunID: "run-153a", Reason: "the forge token lacks the scope to merge",
		DecidedBy: "development-manager", Conversation: "chat-dm", Turn: 9, DecidedAt: stopped.Add(3 * time.Hour),
		OperatorReason: ownership.OperatorCredential,
	}
	decisions := recordedDecisions{"yoyodyne-ifd.153": {Decisions: []runstate.TriageDecision{again}}}

	standing, ended, problem := Escalations([]runstate.State{run}, decisions, nil, nil)
	if problem != "" || len(ended) != 0 || len(standing) != 1 || standing[0].RunID != "run-153a" {
		t.Fatalf("Escalations() = %#v, %#v, %q, want the later escalation standing as the operator's", standing, ended, problem)
	}
}
