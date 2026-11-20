package chat

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func (b *triageBudgetGate) GrantCap(ctx context.Context, workItemID, budget, reason string) (runstate.TriageCrossing, error) {
	return b.store.GrantCap(ctx, workItemID, budget, reason, grantedItemCost, b.now(), b.caps)
}

// grantedItemCost is what the item has cost when these tests' grants are
// recorded, standing in for the run records the command line prices it from.
var grantedItemCost = runstate.TriageGrantCost{TotalUSD: 41.27, Runs: 3}

// grantReply is one turn of the Lead Product Manager's conversation with the
// triage record wired for her grants, as the command line wires it.
func grantReply(t *testing.T, budgets *triageBudgetGate, answer string) (Reply, *fakeTracker) {
	t.Helper()
	tracker := crossingTracker()
	options := triageOptions(t, tracker, nil, answer)
	options.Role = domain.RoleProductManager
	options.Agent = string(domain.RoleProductManager)
	options.Reports = &fakeReports{}
	options.CapGrants = budgets
	return triageSend(t, options), tracker
}

func grantAction(reason string) string {
	return `{"action":"grant","id":"` + crossedItem + `","budget":"` + runstate.TriageReviewRoundBudget + `","reason":"` + reason + `"}`
}

// Past the development manager's five, the Lead Product Manager records a raise
// from her own conversation. The harness carries it out with nobody typing
// anything: the raise is on the item's triage record as her decision with her
// reason, a note names it on the item, and the figures travel with the outcome
// the operator is told from.
func TestTheLeadProductManagerGrantsARaisePastTheDevelopmentManagersFive(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	ctx := context.Background()
	for crossing := 1; crossing <= runstate.MaxDelegatedCapCrossings; crossing++ {
		if _, err := budgets.CrossCap(ctx, crossedItem, runstate.TriageReviewRoundBudget, "narrow findings"); err != nil {
			t.Fatalf("CrossCap() %d error = %v", crossing, err)
		}
	}

	reply, tracker := grantReply(t, budgets, trackerReply("Granting one more.", grantAction("worth more than the work behind it")))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	if outcome.Crossing == nil || outcome.Crossing.Crossing != 1 || outcome.Crossing.Crossings != runstate.MaxLeadCapGrants {
		t.Fatalf("the grant's figures are %+v, want grant 1 of %d", outcome.Crossing, runstate.MaxLeadCapGrants)
	}
	if !strings.Contains(outcome.Summary, "the operator is told afterwards") {
		t.Fatalf("the grant is reported as %q", outcome.Summary)
	}
	recorded, found := budgets.counters(t, crossedItem).OverrideOf(runstate.TriageReviewRoundBudget)
	if !found || !recorded.Granted() || !strings.Contains(recorded.Reason, "worth more than the work behind it") {
		t.Fatalf("the triage record holds %+v, want her grant with her reason", recorded)
	}
	if len(tracker.updates) != 1 || !strings.Contains(tracker.updates[0].change.AppendNotes, "on the Lead Product Manager's decision") ||
		!strings.Contains(tracker.updates[0].change.AppendNotes, "raise 6 of the 10 no role may pass") {
		t.Fatalf("updates = %#v, want a note naming her grant and where the item stands against ten", tracker.updates)
	}
	// The grant carries the item's cost so far: on the triage record, on the
	// item's note, and on the figures the operator's message is built from.
	if recorded.CostSoFar == nil || *recorded.CostSoFar != grantedItemCost {
		t.Fatalf("the triage record's grant carries cost %+v, want %+v", recorded.CostSoFar, grantedItemCost)
	}
	if !strings.Contains(tracker.updates[0].change.AppendNotes, "The item has cost $41.27 over 3 runs so far") {
		t.Fatalf("the grant's note is %q, want it to state the item's cost so far", tracker.updates[0].change.AppendNotes)
	}
	if cost := outcome.Crossing.CostSoFar; cost == nil || cost.TotalUSD != 41.27 || cost.Runs != 3 {
		t.Fatalf("the outcome carries cost %+v, want $41.27 over 3 runs", cost)
	}
}

// A grant is refused while the development manager still has raises of her own,
// and refused once the item has ten in all, saying ten is the limit no role may
// pass. Neither writes anything.
func TestAGrantIsRefusedBeforeTheFiveAreUsedAndPastTen(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	early, tracker := grantReply(t, budgets, trackerReply("Granting one more.", grantAction("worth it")))
	if len(early.Actions) != 1 || early.Actions[0].Applied || !strings.Contains(early.Actions[0].Failure, "still hers to record") || len(tracker.updates) != 0 {
		t.Fatalf("a grant before her five = %#v, updates %#v, want it refused as hers", early.Actions, tracker.updates)
	}

	ctx := context.Background()
	for crossing := 1; crossing <= runstate.MaxDelegatedCapCrossings; crossing++ {
		if _, err := budgets.CrossCap(ctx, crossedItem, runstate.TriageReviewRoundBudget, "narrow findings"); err != nil {
			t.Fatalf("CrossCap() %d error = %v", crossing, err)
		}
	}
	for grant := 1; grant <= runstate.MaxLeadCapGrants; grant++ {
		if _, err := budgets.GrantCap(ctx, crossedItem, runstate.TriageReviewRoundBudget, "worth it"); err != nil {
			t.Fatalf("GrantCap() %d error = %v", grant, err)
		}
	}
	past, tracker := grantReply(t, budgets, trackerReply("One more.", grantAction("one more would settle it")))
	if len(past.Actions) != 1 || past.Actions[0].Applied || !strings.Contains(past.Actions[0].Failure, "ten is the limit no role may pass") || len(tracker.updates) != 0 {
		t.Fatalf("an eleventh raise = %#v, updates %#v, want it refused naming the limit of ten", past.Actions, tracker.updates)
	}
}

// A grant names the cap and the reason, and is the Lead Product Manager's alone:
// the development manager's conversation is refused it, since past her five the
// raise is not hers.
func TestAGrantNamesItsCapAndReasonAndIsTheLeadProductManagersAlone(t *testing.T) {
	t.Parallel()

	for _, action := range []TrackerAction{
		{Action: actionGrant, ID: crossedItem, Reason: "worth it"},
		{Action: actionGrant, ID: crossedItem, Budget: "rounds", Reason: "worth it"},
		{Action: actionGrant, ID: crossedItem, Budget: runstate.TriageReviewRoundBudget},
		{Action: actionGrant, Budget: runstate.TriageReviewRoundBudget, Reason: "worth it"},
		{Action: actionGrant, ID: crossedItem, Budget: runstate.TriageReviewRoundBudget, Run: stoppedRun, Reason: "worth it"},
	} {
		if err := action.Validate(); err == nil {
			t.Fatalf("Validate(%#v) = nil, want it refused", action)
		}
	}

	if lead, _ := AuthorityFor(domain.RoleProductManager); !slices.Contains(lead.TrackerActions, actionGrant) {
		t.Fatalf("the Lead Product Manager may not ask for %q: %v", actionGrant, lead.TrackerActions)
	}
	for _, role := range []domain.AgentRole{domain.RoleDevelopmentManager, domain.RoleArchitect, domain.RoleProgramManager} {
		if held, _ := AuthorityFor(role); slices.Contains(held.TrackerActions, actionGrant) {
			t.Fatalf("the %s may ask for %q, which is the Lead Product Manager's alone", role.Title(), actionGrant)
		}
	}
}

// The bound the Lead Product Manager is told is the bound the store enforces,
// and the caps she is told she may name are the store's.
func TestTheLeadProductManagersContractStatesTheGrantBoundAndCaps(t *testing.T) {
	t.Parallel()

	if spelled, known := spelledNumbers[maxLeadCapGrantsText]; !known || spelled != runstate.MaxLeadCapGrants {
		t.Fatalf("the contract states %q grants and the store enforces %d", maxLeadCapGrantsText, runstate.MaxLeadCapGrants)
	}
	for _, budget := range runstate.TriageOverrideBudgets() {
		if !strings.Contains(grantBudgetsText, `"`+budget+`"`) {
			t.Fatalf("the contract's caps %s do not name the store's %q", grantBudgetsText, budget)
		}
	}
	prompt := SystemPrompt(domain.RoleProductManager, Admission{}, nil, hostilePersona)
	for _, required := range []string{
		`{"action":"grant","id":"beads-id","budget":`,
		"up to " + maxLeadCapGrantsText + " raises for one item",
		"ten raises in all, which is the limit no role may pass",
		"The operator is never asked for a raise.",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the Lead Product Manager's contract does not state %q", required)
		}
	}
}
