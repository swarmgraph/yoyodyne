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

// The list puts nothing in front of the operator that the attention line does
// not already name as his. An ordinary proposed change to a design is the
// architect's, as its entry on the line says, so it is not listed; a proposal
// in the architect's batch of recommendations is listed, because the line
// names that batch as the operator's finding; a proposal already decided is
// not. A document waiting for approval in the Lead Product Manager's
// conversation is on the line as the operator's, because the ownership
// resolver names him for a document that says what the product is for, and is
// listed; one waiting in the architect's conversation, which the resolver
// names nobody for, is on neither; and neither is proposed work, which the
// line names nobody for.
func TestTheOperatorsDecisionsAreOnlyWhatTheLineNamesAsHis(t *testing.T) {
	t.Parallel()

	ordinary := queuedProposal(1, 3*time.Hour)
	argued := queuedProposal(2, 2*time.Hour)
	decided := queuedProposal(3, time.Hour)
	decision, err := decided.Decide(amendment.VerdictApproved, amendment.DeciderOperator, "", moment)
	if err != nil {
		t.Fatal(err)
	}
	conversation := runstate.Conversation{
		ConversationID: "chat-1", Agent: "product-manager", Role: domain.RoleProductManager,
		PendingWrites: []runstate.PendingWrite{
			{ID: "document-4.1", Turn: 4, Action: "revise", Artifact: "v1-non-goals", Title: "V1 non-goals", Body: "the body", Reason: "why"},
			{ID: "document-4.2", Turn: 4, Action: "revise", Artifact: "brief", Body: "held", Hold: &artifact.SendingHold{}},
		},
		PendingProposals: []runstate.PendingProposal{{ID: "6.1", Turn: 6, Title: "Decline keeps its reason"}},
	}
	design := runstate.Conversation{
		ConversationID: "chat-2", Agent: "architect", Role: domain.RoleArchitect,
		PendingWrites: []runstate.PendingWrite{{ID: "document-9.1", Turn: 9, Action: "revise", Artifact: "v1-design", Body: "a design", Reason: "why"}},
	}
	sources := Sources{
		Now:           func() time.Time { return moment },
		Amendments:    fakeAmendments{records: []amendment.Record{{Proposal: &ordinary}, {Proposal: &argued}, {Proposal: &decided}, {Decision: &decision}}},
		Sweeps:        fakeSweeps{recorded: []runstate.Sweep{architectPass(moment.Add(-time.Minute), sweep.Recommendation{Proposal: argued.ID, Verdict: sweep.RecommendApprove, Reason: "it is right"})}},
		Conversations: fakeConversations{recorded: []runstate.Conversation{design, conversation}},
	}

	// The line itself: the ordinary proposal is the architect's, the batch is
	// the operator's, and of the waiting documents only the one in the product
	// home is on it, as his.
	reading := readAmendments(sources, moment)
	for _, entry := range reading.attention() {
		if entry.Mover == MoverOperator {
			t.Fatalf("the line names the operator for an ordinary proposal: %+v", entry)
		}
	}
	documents, problem := readPendingDocuments(sources)
	if problem != "" || len(documents) != 1 || documents[0].ID != "document-4.1" || documents[0].Mover != MoverOperator || documents[0].Kind != AttentionPendingDocument {
		t.Fatalf("the waiting documents on the line = %+v (problem %q), want document-4.1 alone, as the operator's", documents, problem)
	}
	if whose := documents[0].Whose(); !strings.Contains(whose, "`yoyo chat --message \"approve document-4.1\"`") {
		t.Fatalf("whose = %q, want the command that approves it", whose)
	}

	decisions, problem := ReadOperatorDecisions(sources)
	if problem != "" {
		t.Fatalf("problem = %q", problem)
	}
	if len(decisions) != 2 || decisions[0].ID != argued.ID || decisions[1].ID != "document-4.1" {
		t.Fatalf("decisions = %+v, want the proposal in the operator's batch and the product-home document", decisions)
	}
	document := decisions[1]
	if document.Kind != DecisionDocument || document.Agent != "product-manager" || document.ConversationID != "chat-1" || document.Subject != "V1 non-goals" ||
		document.Detail != "the body" || document.AskAgent != "product-manager" || document.DeclineNeedsReason || document.Label != "document to approve" {
		t.Fatalf("the listed document = %+v", document)
	}
	listed := decisions[0]
	if listed.Owner != domain.RoleArchitect || listed.Detail != argued.Change || !listed.DeclineNeedsReason || listed.AskAgent != "developer" ||
		listed.Recommendation == nil || listed.Recommendation.Reason != "it is right" || listed.Label != "proposed document change" {
		t.Fatalf("the listed change = %+v", listed)
	}

	if _, ok, _ := FindOperatorDecision(sources, DecisionAmendment, "", argued.ID); !ok {
		t.Fatal("FindOperatorDecision() did not find the proposal the line names as the operator's")
	}
	if _, ok, _ := FindOperatorDecision(sources, DecisionDocument, "product-manager", "document-4.1"); !ok {
		t.Fatal("FindOperatorDecision() did not find the product-home document")
	}
	for _, missing := range []struct {
		kind  DecisionKind
		agent string
		id    string
	}{
		{DecisionAmendment, "", ordinary.ID},
		{DecisionAmendment, "", decided.ID},
		{DecisionDocument, "product-manager", "document-4.2"},
		{DecisionDocument, "architect", "document-9.1"},
		{DecisionDocument, "architect", "document-4.1"},
		{DecisionProposal, "product-manager", "6.1"},
	} {
		if _, ok, _ := FindOperatorDecision(sources, missing.kind, missing.agent, missing.id); ok {
			t.Fatalf("FindOperatorDecision(%+v) found something the line does not name as the operator's", missing)
		}
	}
}

// A log that cannot be read is said, and never read as nothing waiting.
func TestAnUnreadableSourceOfDecisionsIsSaid(t *testing.T) {
	t.Parallel()

	decisions, problem := ReadOperatorDecisions(Sources{Amendments: fakeAmendments{fail: errors.New("permission denied")}})
	if len(decisions) != 0 || !strings.Contains(problem, "the proposed changes could not be read: permission denied") {
		t.Fatalf("decisions = %+v, problem = %q", decisions, problem)
	}
}
