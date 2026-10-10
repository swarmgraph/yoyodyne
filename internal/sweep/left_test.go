package sweep

import (
	"strings"
	"testing"
)

func TestALeftItemIsHeldToItsContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		left Left
		want string
	}{
		{"whole", Left{Item: "yoyodyne-ifd.414.1", Reason: "waits on the secret store"}, ""},
		{"no item", Left{Reason: "why"}, "item is required"},
		{"no reason", Left{Item: "yoyodyne-ifd.414.1"}, "reason says why"},
		{"long reason", Left{Item: "yoyodyne-ifd.414.1", Reason: strings.Repeat("x", maxLeftBytes+1)}, "limit is"},
	} {
		err := test.left.Validate()
		switch {
		case test.want == "" && err != nil:
			t.Errorf("%s: Validate() = %v, want none", test.name, err)
		case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
			t.Errorf("%s: Validate() = %v, want %q", test.name, err, test.want)
		}
	}
}

// The contract states the field and its bound, and a block written to it
// decodes; one turn past the bound is refused.
func TestTheLeftContractDecodesAndItsBoundHolds(t *testing.T) {
	t.Parallel()
	if !strings.Contains(LeftContract(), `"left"`) || !strings.Contains(LeftContract(), "at most "+maxLeftText) || !strings.Contains(LeftContract(), "no reason given") {
		t.Errorf("contract = %q, want the field, its bound, and what is written where no reason is given", LeftContract())
	}
	if maxLeftText != "10" || MaxLeft != 10 {
		t.Errorf("the contract says %s left items and the code enforces %d", maxLeftText, MaxLeft)
	}
	decoded, err := Decode(`{"status":"complete","summary":"took the addendum","left":[{"item":"yoyodyne-ifd.414.1","reason":"waits on the secret store"}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if reason, found := decoded.LeftReason("yoyodyne-ifd.414.1"); !found || reason != "waits on the secret store" {
		t.Errorf("LeftReason() = %q, %v", reason, found)
	}
	over := Result{Status: StatusComplete, Summary: "too many"}
	for i := 0; i <= MaxLeft; i++ {
		over.Left = append(over.Left, Left{Item: "item", Reason: "why"})
	}
	if err := over.validateTurn(); err == nil {
		t.Error("a turn past the left bound was accepted")
	}
}

// A later turn's word on an item stands over an earlier one.
func TestMergeKeepsTheLastWordOnALeftItem(t *testing.T) {
	t.Parallel()
	first := Result{Status: StatusMore, Summary: "half", Left: []Left{{Item: "a", Reason: "not yet read"}}}
	second := Result{Status: StatusComplete, Summary: "done", Left: []Left{{Item: "a", Reason: "waits on the operator"}}}
	merged := first.Merge(second)
	if reason, _ := merged.LeftReason("a"); reason != "waits on the operator" {
		t.Errorf("LeftReason() = %q, want the later turn's", reason)
	}
	if err := merged.Validate(); err != nil {
		t.Errorf("merged account does not validate: %v", err)
	}
}
