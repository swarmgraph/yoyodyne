package readmodel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// What waits on the operator's approve or decline is read from the records
// that hold each kind and from nothing else: every undecided proposed change,
// with what its owner argued, and every undecided document and proposed work
// in each conversation — but not a document confirmed for publication or held
// for sending, which is not his to decide.
func TestTheOperatorsDecisionsAreReadFromTheirRecords(t *testing.T) {
	t.Parallel()

	waiting := queuedProposal(1, time.Hour)
	decided := queuedProposal(2, 2*time.Hour)
	decision, err := decided.Decide(amendment.VerdictApproved, amendment.DeciderOperator, "", moment)
	if err != nil {
		t.Fatal(err)
	}
	conversation := runstate.Conversation{
		ConversationID: "chat-1", Agent: "product-manager", Role: domain.RoleProductManager,
		PendingWrites: []runstate.PendingWrite{
			{ID: "document-4.1", Turn: 4, Action: "revise", Artifact: "v1-non-goals", Title: "V1 non-goals", Body: "the body", Reason: "the operator asked"},
			{ID: "document-4.2", Turn: 4, Action: "revise", Artifact: "brief", Body: "held", Hold: &artifact.SendingHold{}},
			{ID: "document-5.1", Turn: 5, Action: "revise", Artifact: "v1-goals", Body: "sent", Publication: &runstate.DocumentPublication{}},
		},
		PendingProposals: []runstate.PendingProposal{{ID: "6.1", Turn: 6, Title: "Decline keeps its reason", Description: "the description", Rationale: "why", Asking: "it names a goal"}},
	}
	sources := Sources{
		Amendments:    fakeAmendments{records: []amendment.Record{{Proposal: &waiting}, {Proposal: &decided}, {Decision: &decision}}},
		Sweeps:        fakeSweeps{recorded: []runstate.Sweep{architectPass(moment.Add(-time.Minute), sweep.Recommendation{Proposal: waiting.ID, Verdict: sweep.RecommendApprove, Reason: "it is right"})}},
		Conversations: fakeConversations{recorded: []runstate.Conversation{conversation}},
	}
	decisions, problem := ReadOperatorDecisions(sources)
	if problem != "" {
		t.Fatalf("problem = %q", problem)
	}
	var keys []string
	for _, each := range decisions {
		keys = append(keys, each.Key())
	}
	want := []string{"amendment::" + waiting.ID, "document:product-manager:document-4.1", "proposal:product-manager:6.1"}
	if strings.Join(keys, " ") != strings.Join(want, " ") {
		t.Fatalf("decisions = %v, want %v", keys, want)
	}
	change := decisions[0]
	if change.Owner != domain.RoleArchitect || change.Detail != waiting.Change || !change.DeclineNeedsReason || change.AskAgent != "developer" ||
		change.Recommendation == nil || change.Recommendation.Reason != "it is right" || change.Label != "proposed document change" {
		t.Fatalf("the proposed change = %+v", change)
	}
	written := decisions[1]
	if written.Subject != "V1 non-goals" || written.Detail != "the body" || written.ConversationID != "chat-1" || written.AskRole != domain.RoleProductManager || written.DeclineNeedsReason {
		t.Fatalf("the document = %+v", written)
	}
	if proposed := decisions[2]; proposed.Asking != "it names a goal" || !strings.Contains(proposed.Says, "6.1 (Decline keeps its reason)") {
		t.Fatalf("the proposed work = %+v", proposed)
	}

	found, ok, _ := FindOperatorDecision(sources, DecisionDocument, "product-manager", "document-4.1")
	if !ok || found.ID != "document-4.1" {
		t.Fatalf("FindOperatorDecision() = %+v, %v", found, ok)
	}
	for _, missing := range [][3]string{
		{string(DecisionAmendment), "", decided.ID},
		{string(DecisionDocument), "product-manager", "document-4.2"},
		{string(DecisionDocument), "architect", "document-4.1"},
	} {
		if _, ok, _ := FindOperatorDecision(sources, DecisionKind(missing[0]), missing[1], missing[2]); ok {
			t.Fatalf("FindOperatorDecision(%v) found something no longer the operator's", missing)
		}
	}
}

// A source that cannot be read costs its own part of the list, said, and
// never an empty answer standing for one nobody read.
func TestAnUnreadableSourceOfDecisionsIsSaid(t *testing.T) {
	t.Parallel()

	waiting := queuedProposal(1, time.Hour)
	decisions, problem := ReadOperatorDecisions(Sources{
		Amendments:    fakeAmendments{records: []amendment.Record{{Proposal: &waiting}}},
		Conversations: fakeConversations{fail: errors.New("permission denied")},
	})
	if len(decisions) != 1 || !strings.Contains(problem, "the conversations could not be read: permission denied") {
		t.Fatalf("decisions = %+v, problem = %q", decisions, problem)
	}
}
