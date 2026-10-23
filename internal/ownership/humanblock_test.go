package ownership

import (
	"regexp"
	"slices"
	"testing"
)

func TestBlockedOnAHumanNamesTheOperatorOnlyForAReasonOnTheList(t *testing.T) {
	for _, reason := range OperatorReasons() {
		if ResolveBlockedOnAHuman(reason) != MoverOperator || reason.Check() != nil {
			t.Fatalf("reason %q is on the list and should name the operator", reason)
		}
	}
	for _, reason := range []OperatorReason{"", "approval", "decision", "review"} {
		if ResolveBlockedOnAHuman(reason) == MoverOperator || reason.Check() == nil {
			t.Fatalf("reason %q is not on the list and must not name the operator", reason)
		}
	}
}

// The list the role contracts embed names exactly the reasons the resolver
// accepts, in the same order: a reason added to OperatorReasons and not to the
// contracts' wording, or worded there and not accepted, fails here.
func TestTheContractsListIsTheResolversList(t *testing.T) {
	named := regexp.MustCompile(`"([a-z-]+)" for `).FindAllStringSubmatch(OperatorReasonsInContract, -1)
	var got []OperatorReason
	for _, match := range named {
		got = append(got, OperatorReason(match[1]))
	}
	if want := OperatorReasons(); !slices.Equal(got, want) {
		t.Fatalf("OperatorReasonsInContract names %v, and the resolver accepts %v", got, want)
	}
}
