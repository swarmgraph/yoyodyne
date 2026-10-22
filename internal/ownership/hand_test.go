package ownership

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A role's decision the harness cannot yet carry out keeps the role as its
// owner and carries the operator's hand: the capability the harness lacks, what
// he types, and the work item that closes the gap. None of them names him as
// the owner, because none is his decision.
func TestARolesDecisionThatNeedsTheOperatorsHandKeepsTheRoleAsOwner(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		kind     Kind
		record   Record
		owner    Mover
		need     HandNeed
		closedBy string
		types    string
	}{
		{
			// The shipped owners decide their own amendments, so the case is an
			// owner the harness has not given the decision.
			name:     "an amendment whose owner cannot decide it",
			kind:     KindAmendment,
			record:   Record{Role: domain.RoleDevelopmentManager, Command: "yoyo amendment approve|decline amendment-1 --reason \"<why>\""},
			owner:    MoverDevelopmentManager,
			need:     HandAmendmentDecision,
			closedBy: "yoyodyne-ifd.437.14",
			types:    "yoyo amendment approve|decline amendment-1",
		},
		{
			name:     "an operational-configuration recommendation the Lead Product Manager handled",
			kind:     KindOperatorAction,
			record:   Record{FromReport: true, Role: domain.RoleProductManager, Hand: HandOperationalConfiguration},
			owner:    MoverProductManager,
			need:     HandOperationalConfiguration,
			closedBy: "yoyodyne-ifd.434.8",
			types:    ".yoyodyne/config.yaml",
		},
		{
			name:     "a document an architect wrote from a conversation",
			kind:     KindDocumentApproval,
			record:   Record{Role: domain.RoleArchitect, Command: "yoyo agent chat architect --message \"approve document-7.1\""},
			owner:    MoverArchitect,
			need:     HandDocumentConfirmation,
			closedBy: "yoyodyne-ifd.433.21",
			types:    "yoyo agent chat architect --message \"approve document-7.1\"",
		},
		{
			name:   "a cap crossing past the development manager's own",
			kind:   KindOperatorAction,
			record: Record{FromEscalation: true, Hand: HandCapCrossing, Command: "yoyo triage override --budget \"review-rounds\" --cap <n> --by \"<you>\" --reason \"<why>\" item-1"},
			owner:  MoverDevelopmentManager,
			need:   HandCapCrossing,
			types:  "yoyo triage override",
		},
	}
	for _, c := range cases {
		answer := Resolve(c.kind, c.record)
		if answer.Owner != c.owner {
			t.Errorf("%s: owned by %q, want the role that decided it, %q", c.name, answer.Owner, c.owner)
		}
		if answer.Owner.IsOperator() || answer.Reason != "" {
			t.Errorf("%s: names the operator as owner (%+v), want the role's decision with his hand", c.name, answer)
		}
		if !answer.NeedsHand() {
			t.Fatalf("%s: carries no hand: %+v", c.name, answer)
		}
		hand := answer.Hand
		if !hand.Who.IsOperator() || hand.Need != c.need || hand.ClosedBy != c.closedBy {
			t.Errorf("%s: hand %+v, want the operator's for %q closed by %q", c.name, hand, c.need, c.closedBy)
		}
		if strings.TrimSpace(hand.Missing) == "" || !strings.Contains(hand.Types, c.types) {
			t.Errorf("%s: hand %+v does not name the missing capability and %q to type", c.name, hand, c.types)
		}
		said := hand.Says(answer.Owner)
		if !strings.HasPrefix(said, answer.Owner.Possessive()+" decision") || !strings.Contains(said, hand.Types) {
			t.Errorf("%s: says %q, want the deciding role and what to type", c.name, said)
		}
		if c.closedBy != "" && !strings.Contains(said, c.closedBy) {
			t.Errorf("%s: says %q, want the item that ends the need", c.name, said)
		}
	}
}

// The owners the harness ships decide their own amendments, so an amendment
// against their documents needs nobody's hand; a document stating the
// product's intent is the operator's own decision and is no role's needing his
// hand.
func TestAHandIsCarriedOnlyWhereTheHarnessLacksTheCapability(t *testing.T) {
	t.Parallel()
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleProductManager} {
		if answer := Resolve(KindAmendment, Record{Role: role}); answer.Hand != nil {
			t.Errorf("an amendment owned by the %s carries a hand %+v, though the %s decides it", role, answer.Hand, role)
		}
	}
	answer := Resolve(KindDocumentApproval, Record{Role: domain.RoleProductManager, FundamentalIntent: true})
	if !answer.Owner.IsOperator() || answer.Hand != nil {
		t.Errorf("a document stating the product's intent resolves to %+v, want the operator's with no hand", answer)
	}
	// A hand naming a gap nobody recorded is no hand: the finding goes back as
	// one naming the operator for no reason.
	answer = Resolve(KindOperatorAction, Record{FromReport: true, Hand: "a-gap-nobody-named"})
	if answer.Hand != nil || answer.Owner != MoverProductManager {
		t.Errorf("a finding with an unknown hand resolves to %+v, want the Lead Product Manager's with no hand", answer)
	}
}

// Every hand in every fixture record is on an answer naming a role, never the
// operator, the harness, or nobody.
func TestNoAnswerNamingAnybodyButARoleCarriesAHand(t *testing.T) {
	t.Parallel()
	for _, kind := range Kinds() {
		for _, record := range fixtureRecords() {
			for _, need := range append(HandNeeds(), "") {
				record.Hand = need
				answer := Resolve(kind, record)
				if answer.Hand != nil && !domain.AgentRole(answer.Owner).Valid() {
					t.Errorf("%s %+v resolves to %+v, a hand on an owner that is no role", kind, record, answer)
				}
			}
		}
	}
}

func TestEveryHandNeedNamesWhatIsMissing(t *testing.T) {
	t.Parallel()
	for _, need := range HandNeeds() {
		if !need.Valid() || strings.TrimSpace(need.Missing()) == "" || need.Check() != nil {
			t.Errorf("hand %q does not say what the harness lacks", need)
		}
	}
	if err := HandNeed("unknown").Check(); err == nil {
		t.Error("an unknown hand passes its check")
	}
}
