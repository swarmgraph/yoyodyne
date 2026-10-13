package sweep

import (
	"strings"
	"testing"
)

func TestASettledItemIsDecidedWithAssumptionsOrAskedAsOneQuestion(t *testing.T) {
	t.Parallel()
	const item = "yoyodyne-ifd.429.43"
	for _, test := range []struct {
		name    string
		settled Settled
		want    string
	}{
		{"decided", Settled{Item: item, Decision: "two at once", Assumptions: []string{"eight cores"}}, ""},
		{"asked", Settled{Item: item, Question: "More than two?", AskedOf: "the Lead Product Manager"}, ""},
		{"no item", Settled{Decision: "two at once", Assumptions: []string{"eight cores"}}, "item is required"},
		{"neither", Settled{Item: item}, "carries the decision made or the one question asked"},
		{"both", Settled{Item: item, Decision: "two", Assumptions: []string{"a"}, Question: "q?", AskedOf: "x"}, "not both"},
		{"decided without assumptions", Settled{Item: item, Decision: "two at once"}, "writes down the assumptions"},
		{"asked of nobody", Settled{Item: item, Question: "More than two?"}, "names who it was put to"},
		{"blank assumption", Settled{Item: item, Decision: "two", Assumptions: []string{" "}}, "assumptions[0] is blank"},
		{"too many assumptions", Settled{Item: item, Decision: "two", Assumptions: strings.Split(strings.Repeat("a,", MaxAssumptions), ",")}, "limit is"},
	} {
		err := test.settled.Validate()
		switch {
		case test.want == "" && err != nil:
			t.Errorf("%s: Validate() = %v, want none", test.name, err)
		case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
			t.Errorf("%s: Validate() = %v, want %q", test.name, err, test.want)
		}
	}
}

// A block written to the contract decodes, a later turn's entry stands over an
// earlier one, and the contract names both ways and the reasons it was given.
func TestTheSettleContractDecodesAndTheLastEntryStands(t *testing.T) {
	t.Parallel()
	first, err := Decode(`{"status":"more","summary":"asked","settled":[{"item":"x-1","question":"q?","asked_of":"the architect"}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	second, err := Decode(`{"status":"complete","summary":"decided","settled":[{"item":"x-1","decision":"d","assumptions":["a"]}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	merged := first.Merge(*second)
	if settled, found := merged.SettledItem("x-1"); !found || !settled.Decided() {
		t.Errorf("SettledItem() = %+v, %v, want the later decision", settled, found)
	}
	if _, err := Decode(`{"status":"complete","summary":"s","settled":[{"item":"x-1","decision":"d"}]}`); err == nil {
		t.Error("Decode() accepted a decision with no assumptions")
	}
	contract := SettleContract("x-1", 2, []string{"open points are spread over earlier replies", ""})
	for _, want := range []string{"x-1, first in your order, 2 times in a row", "- open points are spread over earlier replies", "- no reason given", `"decision"`, `"question"`, "sent back once"} {
		if !strings.Contains(contract, want) {
			t.Errorf("contract does not say %q:\n%s", want, contract)
		}
	}
}
