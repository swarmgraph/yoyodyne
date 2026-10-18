package amendment

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestADecisionIsTakenUnderTheOwnersAuthorityWhoeverExercisesIt(t *testing.T) {
	t.Parallel()

	proposal := validProposal()
	decided := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	decision, err := proposal.Decide(VerdictApproved, DeciderOperator, "the ordering was never settled", decided)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	// The authority follows the document rather than the person, so an operator
	// deciding in an absent architect's stead is recorded as exercising the
	// architect's authority rather than as the architect having answered.
	if decision.Authority != domain.RoleArchitect || decision.Decider != DeciderOperator {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.ProposalID != proposal.ID || !decision.DecidedAt.Equal(decided) {
		t.Fatalf("decision does not name what it settled: %#v", decision)
	}
}

func TestADeclineKeepsWhyAndAnApprovalNeedNot(t *testing.T) {
	t.Parallel()

	// A proposal turned down with no reason is one the same argument arrives to
	// make again next week.
	if _, err := validProposal().Decide(VerdictDeclined, DeciderOperator, "  ", time.Now()); err == nil {
		t.Fatal("Decide() accepted a decline with no reason")
	}
	// An approval owes none: what follows it is the owner's own revision, which
	// records why the document changed.
	if _, err := validProposal().Decide(VerdictApproved, DeciderOperator, "", time.Now()); err != nil {
		t.Fatalf("Decide() refused an approval with no reason: %v", err)
	}
}

func TestAnApprovalSaysThatNothingWasWrittenToTheDocument(t *testing.T) {
	t.Parallel()

	// The one way this mechanism could quietly become an edit is somebody
	// believing the document already says what was approved, so every reading of
	// an approval says otherwise.
	decision, err := validProposal().Decide(VerdictApproved, DeciderOperator, "", time.Now())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	rendered := decision.Render()
	if !strings.Contains(rendered, "nothing was written to the artifact") {
		t.Fatalf("Render() = %q", rendered)
	}
	if !strings.Contains(rendered, "the architect makes the change") {
		t.Fatalf("Render() does not say who makes the change: %q", rendered)
	}
}

func TestPendingIsWhatNobodyHasDecided(t *testing.T) {
	t.Parallel()

	first := validProposal()
	second := validProposal()
	second.ID = "amendment-fedcba9876543210fedcba9876543210"
	second.Artifact = "v1-goals"
	second.Kind = artifact.KindGoals
	second.Owner = domain.RoleProductManager
	decision, err := first.Decide(VerdictDeclined, DeciderOperator, "the design is right and the item was wrong", time.Now())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	records := []Record{{Proposal: &first}, {Proposal: &second}, {Decision: &decision}}

	pending := Pending(records)
	if len(pending) != 1 || pending[0].ID != second.ID {
		t.Fatalf("Pending() = %#v", pending)
	}
	// Each owner reads its own queue: what the architect is being asked is not
	// what the product manager is being asked.
	if forArchitect := PendingFor(records, domain.RoleArchitect); len(forArchitect) != 0 {
		t.Fatalf("PendingFor(architect) = %#v, want nothing after it was decided", forArchitect)
	}
	if forProductManager := PendingFor(records, domain.RoleProductManager); len(forProductManager) != 1 {
		t.Fatalf("PendingFor(product-manager) = %#v", forProductManager)
	}
}

func TestTheFirstDecisionOnAProposalIsTheOneThatStands(t *testing.T) {
	t.Parallel()

	// The log is append-only and two decisions on one proposal can only be a race
	// or a mistake. Reading the later one as an override would let a second
	// answer quietly replace the decision somebody already acted on.
	proposal := validProposal()
	first, err := proposal.Decide(VerdictApproved, DeciderOperator, "", time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	second, err := proposal.Decide(VerdictDeclined, DeciderOperator, "changed my mind", time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	settled, decided := DecisionOn([]Record{{Proposal: &proposal}, {Decision: &first}, {Decision: &second}}, proposal.ID)
	if !decided || settled.Verdict != VerdictApproved {
		t.Fatalf("DecisionOn() = %#v, %v", settled, decided)
	}
}

func TestARecordCarriesExactlyOneThing(t *testing.T) {
	t.Parallel()

	proposal := validProposal()
	decision, err := proposal.Decide(VerdictApproved, DeciderOperator, "", time.Now())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if err := (Record{}).Validate(); err == nil {
		t.Fatal("Validate() accepted a record that says nothing happened")
	}
	// A proposal decided in the same breath it was raised is something no path
	// here can produce, so a log line claiming it is refused rather than read.
	if err := (Record{Proposal: &proposal, Decision: &decision}).Validate(); err == nil {
		t.Fatal("Validate() accepted a record that both raised and decided")
	}
}

func TestTheOwningRoleDecidesItsOwnDocumentFromItsConversation(t *testing.T) {
	t.Parallel()

	proposal := validProposal()
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	approved, err := proposal.DecideAsOwner(domain.RoleArchitect, VerdictApproved, "the ordering was never settled", "chat-architect", at)
	if err != nil {
		t.Fatalf("DecideAsOwner(architect, approve) error = %v", err)
	}
	if approved.Decider != DeciderOwner || approved.Authority != domain.RoleArchitect || approved.Conversation != "chat-architect" {
		t.Fatalf("DecideAsOwner() = %+v, want the architect deciding as owner, from chat-architect", approved)
	}
	if rendered := approved.Render(); !strings.Contains(rendered, "approved by the architect under the architect's authority") ||
		!strings.Contains(rendered, "decided in conversation chat-architect") {
		t.Fatalf("Render() = %q, want the owner and the conversation named", rendered)
	}
	declined, err := proposal.DecideAsOwner(domain.RoleArchitect, VerdictDeclined, "the design already says this", "chat-architect", at)
	if err != nil || declined.Verdict != VerdictDeclined {
		t.Fatalf("DecideAsOwner(architect, decline) = %+v, %v", declined, err)
	}
	// A decline still owes its reason, whoever decides it.
	if _, err := proposal.DecideAsOwner(domain.RoleArchitect, VerdictDeclined, " ", "chat-architect", at); err == nil {
		t.Fatal("DecideAsOwner() accepted a decline with no reason")
	}
	// And an owner's decision says where it was made.
	if _, err := proposal.DecideAsOwner(domain.RoleArchitect, VerdictApproved, "", "", at); err == nil {
		t.Fatal("DecideAsOwner() accepted an owner's decision naming no conversation")
	}
}

func TestARoleCannotDecideADocumentItDoesNotOwn(t *testing.T) {
	t.Parallel()

	_, err := validProposal().DecideAsOwner(domain.RoleProductManager, VerdictApproved, "", "chat-product", time.Now())
	if err == nil || !strings.Contains(err.Error(), "the architect's to decide") {
		t.Fatalf("DecideAsOwner(product manager) on a design = %v, want it refused as the architect's", err)
	}
}

func TestAGoalsChangeOfFundamentalOrUnstatedIntentStaysTheOperators(t *testing.T) {
	t.Parallel()

	goals := validProposal()
	goals.Artifact, goals.Kind, goals.Owner = "v1-goals", artifact.KindGoals, domain.RoleProductManager
	for _, intent := range []artifact.Intent{"", artifact.IntentFundamental} {
		goals.Intent = intent
		if !goals.ReservedForOperator() {
			t.Errorf("ReservedForOperator() with intent %q = false, want the operator's", intent)
		}
		if _, err := goals.DecideAsOwner(domain.RoleProductManager, VerdictApproved, "", "chat-product", time.Now()); err == nil ||
			!strings.Contains(err.Error(), "the operator's to decide") {
			t.Errorf("DecideAsOwner() with intent %q = %v, want it refused as the operator's", intent, err)
		}
		if !strings.Contains(goals.Render(), "decided by the operator") {
			t.Errorf("Render() with intent %q = %q, want it to say the operator decides", intent, goals.Render())
		}
		// The operator himself still decides it.
		if _, err := goals.Decide(VerdictApproved, DeciderOperator, "", time.Now()); err != nil {
			t.Errorf("Decide(operator) with intent %q error = %v", intent, err)
		}
	}
	goals.Intent = artifact.IntentConsistent
	if goals.ReservedForOperator() {
		t.Fatal("ReservedForOperator() on a consistent goals change = true, want the Lead Product Manager's")
	}
	if _, err := goals.DecideAsOwner(domain.RoleProductManager, VerdictApproved, "", "chat-product", time.Now()); err != nil {
		t.Fatalf("DecideAsOwner() on a consistent goals change error = %v", err)
	}
	// The non-goals are the Lead Product Manager's, as the architect's ruling
	// keeps only the brief and the goals for the operator.
	nonGoals := goals
	nonGoals.Artifact, nonGoals.Kind, nonGoals.Intent = "v1-non-goals", artifact.KindNonGoals, ""
	if nonGoals.ReservedForOperator() {
		t.Fatal("ReservedForOperator() on a non-goals change = true, want the Lead Product Manager's")
	}
	// A design is never reserved, whatever its proposer said about intent.
	design := validProposal()
	design.Intent = artifact.IntentFundamental
	if design.ReservedForOperator() {
		t.Fatal("ReservedForOperator() on a design = true, want the architect's")
	}
}

func TestAProposalIsNamedByItsWholeIdOrAUniquePrefix(t *testing.T) {
	t.Parallel()

	first := validProposal()
	second := validProposal()
	second.ID = "amendment-0123456700000000000000000000000f"
	records := []Record{{Proposal: &first}, {Proposal: &second}}
	for _, named := range []string{first.ID, "0123456789ab", "amendment-0123456789"} {
		found, err := Named(records, named)
		if err != nil || found.ID != first.ID {
			t.Errorf("Named(%q) = %q, %v, want %s", named, found.ID, err, first.ID)
		}
	}
	for _, named := range []string{"01234567", "0123", "not-a-proposal", "ffffffffff"} {
		if _, err := Named(records, named); err == nil {
			t.Errorf("Named(%q) resolved, want it refused", named)
		}
	}
}
