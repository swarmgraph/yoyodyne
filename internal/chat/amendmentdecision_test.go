package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// decidingConversation opens a conversation with one role over a log holding
// the proposals given, whose one turn replies with the actions given.
func decidingConversation(t *testing.T, role domain.AgentRole, log *fakeAmendmentLog, actions ...string) (*Session, *fakeBackend) {
	t.Helper()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{FinalText: trackerReply("Deciding the proposed changes.", actions...), SessionID: "session-1"},
		{FinalText: "Done.", SessionID: "session-1"},
	}}
	options := testOptions(t, provider)
	options.Role = role
	options.Agent = string(role)
	options.Amendments = log
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "decide what is yours"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return session, provider
}

// An owning role approves and declines changes proposed to its own documents
// from its conversation, and the decision is the record `yoyo amendment`
// writes, with the owner named as the one who made it.
func TestTheArchitectApprovesAndDeclinesChangesToHerDesigns(t *testing.T) {
	t.Parallel()

	approved := testDesignProposal("amendment-aaaaaaaa000000000000000000000001")
	declined := testDesignProposal("amendment-bbbbbbbb000000000000000000000002")
	log := &fakeAmendmentLog{records: []amendment.Record{{Proposal: ptr(approved)}, {Proposal: ptr(declined)}}}
	session, provider := decidingConversation(t, domain.RoleArchitect, log,
		`{"action":"amendment","amendment":"`+approved.ID+`","decision":"approve","reason":"recorded earlier and never written down"}`,
		`{"action":"amendment","amendment":"bbbbbbbb","decision":"decline","reason":"the design already says this"}`,
	)

	for _, want := range []struct {
		id      string
		verdict amendment.Verdict
	}{{approved.ID, amendment.VerdictApproved}, {declined.ID, amendment.VerdictDeclined}} {
		decision, decided := amendment.DecisionOn(log.records, want.id)
		if !decided {
			t.Fatalf("%s was not decided; the log holds %+v; the next turn was told:\n%s", want.id, log.records, provider.requests[len(provider.requests)-1].Prompt)
		}
		if decision.Verdict != want.verdict || decision.Decider != amendment.DeciderOwner ||
			decision.Authority != domain.RoleArchitect || decision.Conversation != session.state.ConversationID {
			t.Fatalf("decision on %s = %+v, want %s by the architect as owner from %s", want.id, decision, want.verdict, session.state.ConversationID)
		}
	}
	// The architect is told what was recorded, and that an approval wrote
	// nothing to the document.
	results := provider.requests[1].Prompt
	if !strings.Contains(results, "approved the proposed change "+approved.ID) || !strings.Contains(results, "the change is yours to make there") {
		t.Fatalf("the approval was not reported back to the architect:\n%s", results)
	}
	if !strings.Contains(results, "declined the proposed change "+declined.ID) {
		t.Fatalf("the decline was not reported back to the architect:\n%s", results)
	}
}

// A role cannot decide a change to a document it does not own, and nothing is
// written when it tries.
func TestARoleIsRefusedADecisionOnADocumentItDoesNotOwn(t *testing.T) {
	t.Parallel()

	goals := testGoalsProposal("amendment-cccccccc000000000000000000000003")
	goals.Intent = artifact.IntentConsistent
	log := &fakeAmendmentLog{records: []amendment.Record{{Proposal: ptr(goals)}}}
	_, provider := decidingConversation(t, domain.RoleArchitect, log,
		`{"action":"amendment","amendment":"`+goals.ID+`","decision":"approve","reason":"looks right"}`)

	if _, decided := amendment.DecisionOn(log.records, goals.ID); decided {
		t.Fatal("the architect decided a change to the goals")
	}
	if results := provider.requests[1].Prompt; !strings.Contains(results, "Lead Product Manager's to decide and not the architect's") {
		t.Fatalf("the refusal did not say whose the proposal is:\n%s", results)
	}

	// And a role holding no part of the capability is refused before anything
	// is read: the development manager owns no document.
	if (Authority{TrackerActions: authorities[domain.RoleDevelopmentManager].TrackerActions}).MayAct(actionAmendment) {
		t.Fatal("the development manager may decide proposed changes")
	}
}

// A change to the goals that says it changes their intent, or does not say,
// stays the operator's: the Lead Product Manager who owns the goals is refused
// it, and one that says it is consistent is hers.
func TestAGoalsChangeOfFundamentalIntentIsKeptForTheOperator(t *testing.T) {
	t.Parallel()

	unstated := testGoalsProposal("amendment-dddddddd000000000000000000000004")
	fundamental := testGoalsProposal("amendment-eeeeeeee000000000000000000000005")
	fundamental.Intent = artifact.IntentFundamental
	consistent := testGoalsProposal("amendment-ffffffff000000000000000000000006")
	consistent.Intent = artifact.IntentConsistent
	log := &fakeAmendmentLog{records: []amendment.Record{{Proposal: ptr(unstated)}, {Proposal: ptr(fundamental)}, {Proposal: ptr(consistent)}}}
	_, provider := decidingConversation(t, domain.RoleProductManager, log,
		`{"action":"amendment","amendment":"`+unstated.ID+`","decision":"approve","reason":"fine"}`,
		`{"action":"amendment","amendment":"`+fundamental.ID+`","decision":"decline","reason":"not now"}`,
		`{"action":"amendment","amendment":"`+consistent.ID+`","decision":"approve","reason":"a rewording"}`,
	)

	for _, kept := range []string{unstated.ID, fundamental.ID} {
		if _, decided := amendment.DecisionOn(log.records, kept); decided {
			t.Errorf("the Lead Product Manager decided %s, which is the operator's", kept)
		}
	}
	if decision, decided := amendment.DecisionOn(log.records, consistent.ID); !decided || decision.Decider != amendment.DeciderOwner {
		t.Fatalf("the consistent goals change was not decided by its owner: %+v, %t", decision, decided)
	}
	if results := provider.requests[1].Prompt; strings.Count(results, "is the operator's to decide") != 2 {
		t.Fatalf("each reserved proposal was not refused as the operator's:\n%s", results)
	}
}

func TestAnAmendmentActionNamesAProposalAndADecision(t *testing.T) {
	t.Parallel()

	for _, invalid := range []string{
		`{"action":"amendment","decision":"approve","reason":"x"}`,
		`{"action":"amendment","amendment":"amendment-0123456789abcdef0123456789abcdef","reason":"x"}`,
		`{"action":"amendment","amendment":"amendment-0123456789abcdef0123456789abcdef","decision":"merge","reason":"x"}`,
		`{"action":"amendment","amendment":"amendment-0123456789abcdef0123456789abcdef","decision":"approve"}`,
		`{"action":"amendment","id":"yoyodyne-1","amendment":"amendment-0123456789abcdef0123456789abcdef","decision":"approve","reason":"x"}`,
	} {
		if _, _, err := decodeTrackerActions(`{"actions":[` + invalid + `]}`); err == nil {
			t.Errorf("decodeTrackerActions(%s) accepted an amendment action it should refuse", invalid)
		}
	}
	if _, _, err := decodeTrackerActions(`{"actions":[{"action":"amendment","amendment":"amendment-0123456789abcdef0123456789abcdef","decision":"decline","reason":"x"}]}`); err != nil {
		t.Fatalf("decodeTrackerActions() refused a well-formed amendment action: %v", err)
	}
}
