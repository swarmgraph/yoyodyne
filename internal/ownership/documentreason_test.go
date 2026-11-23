package ownership

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A waiting change to one of the product's documents is the operator's for a
// change of fundamental intent, and its reason names which document it would
// change and why it waits rather than calling every one a change to the
// fundamental goals. The reason on the closed list is unchanged; only the words
// are particular. A document outside the product's intent stays its role's and
// carries no reason at all.
func TestAWaitingIntentDocumentsReasonNamesItsKind(t *testing.T) {
	t.Parallel()
	for kind, names := range map[string]string{
		"rules":     "a change to the operating rules ",
		"goals":     "a change to the goals ",
		"non-goals": "a change to the non-goals ",
		"brief":     "a change to the brief ",
		"":          "a change to a document of the product's intent ",
	} {
		answer := Resolve(KindDocumentApproval, Record{Role: domain.RoleProductManager, FundamentalIntent: true, DocumentKind: kind})
		if !answer.Owner.IsOperator() || answer.Reason != OperatorFundamentalIntent {
			t.Fatalf("%q: answer = %+v, want the operator's for a change of fundamental intent", kind, answer)
		}
		if says := answer.ReasonSays(); !strings.HasPrefix(says, names) || !strings.HasSuffix(says, "could not approve as consistent with intent, so it waits for the operator to decide") {
			t.Errorf("%q: reason says %q", kind, says)
		}
	}
	design := Resolve(KindDocumentApproval, Record{Role: domain.RoleArchitect, DocumentKind: "design"})
	if design.Owner.IsOperator() || design.ReasonSays() != "" || design.ReasonDetail != "" {
		t.Errorf("a design resolves to %+v, want the architect's with no reason", design)
	}
}
