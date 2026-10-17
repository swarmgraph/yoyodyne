package ownership

import "testing"

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
