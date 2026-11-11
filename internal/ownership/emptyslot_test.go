package ownership

import "testing"

// Every cause of an empty slot names somebody, and a hold is whoever the hold's
// own rule names: the operator only for a hold he placed, under his own-hold
// reason, and the development manager for a hold the brake placed.
func TestEveryEmptySlotCauseNamesWhoMovesIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		record Record
		owner  Mover
		reason OperatorReason
	}{
		{"the operator's own hold", Record{EmptySlot: EmptySlotHold, HeldByOperator: true}, MoverOperator, OperatorOwnHold},
		{"the brake's hold", Record{EmptySlot: EmptySlotHold, HeldByBrake: true, Braked: true}, MoverDevelopmentManager, ""},
		{"a rule keeping work out", Record{EmptySlot: EmptySlotRule}, MoverHarness, ""},
		{"a slot still counted", Record{EmptySlot: EmptySlotStillCounted}, MoverDevelopmentManager, ""},
		{"no eligible item", Record{EmptySlot: EmptySlotNothingEligible}, MoverDevelopmentManager, ""},
		{"not known", Record{EmptySlot: EmptySlotNotKnown}, MoverDevelopmentManager, ""},
		{"a cause nobody named", Record{EmptySlot: "something-else"}, MoverProductManager, ""},
	}
	for _, c := range cases {
		got := Resolve(KindEmptySlot, c.record)
		if got.Owner != c.owner || got.Reason != c.reason || got.Remedy == "" {
			t.Errorf("%s: Resolve = %+v, want owner %s with reason %q and a remedy", c.name, got, c.owner, c.reason)
		}
	}
	for _, cause := range EmptySlotCauses() {
		if !cause.Valid() {
			t.Errorf("cause %q is not valid", cause)
		}
	}
}
