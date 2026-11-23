package sweep

import (
	"strings"
	"testing"
)

func TestAnOverdueEntryNamesItsItemItsActAndWhy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		entry Overdue
		want  string
	}{
		{"acted on", Overdue{Item: "x-1", Act: ActOrdered, Reason: "placed first"}, ""},
		// An act off the list is read as a deferral and sent back by the
		// harness, not refused with the whole account.
		{"an act off the list", Overdue{Item: "x-1", Act: "deferred", Reason: "later"}, ""},
		{"no item", Overdue{Act: ActOrdered, Reason: "placed first"}, "item is required"},
		{"no act", Overdue{Item: "x-1", Reason: "placed first"}, "act is required"},
		{"no reason", Overdue{Item: "x-1", Act: ActExplained}, "reason says what was done and why"},
	} {
		err := test.entry.Validate()
		switch {
		case test.want == "" && err != nil:
			t.Errorf("%s: Validate() = %v, want none", test.name, err)
		case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
			t.Errorf("%s: Validate() = %v, want %q", test.name, err, test.want)
		}
	}
}

func TestOnlyAnExplanationIsAnActThatChangesNothingInTheTracker(t *testing.T) {
	t.Parallel()
	for _, act := range acts {
		if act.ChangesTheTracker() == (act == ActExplained) {
			t.Errorf("%q changes the tracker = %t", act, act.ChangesTheTracker())
		}
	}
	if Act("deferred").Valid() || Act("deferred").ChangesTheTracker() {
		t.Error("deferring is an act on the list")
	}
}

func TestTheOverdueListDecodesMergesAndTheLastEntryStands(t *testing.T) {
	t.Parallel()
	first, err := Decode(`{"status":"more","summary":"looked","overdue":[{"item":"x-1","act":"explained","reason":"waits on the run in flight"}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	second, err := Decode(`{"status":"complete","summary":"acted","overdue":[{"item":"x-1","act":"ordered","reason":"placed first"}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	merged := first.Merge(*second)
	if entry, found := merged.OverdueEntry("x-1"); !found || entry.Act != ActOrdered {
		t.Errorf("OverdueEntry() = %+v, %t, want the later turn's act", entry, found)
	}
	if _, found := merged.OverdueEntry("x-2"); found {
		t.Error("an item the account says nothing about was found")
	}
	if err := merged.Validate(); err != nil {
		t.Errorf("merged Validate() = %v", err)
	}
}

func TestTheOverdueContractNamesEveryActAndNoneForDeferring(t *testing.T) {
	t.Parallel()
	contract := OverdueContract()
	for _, act := range acts {
		if !strings.Contains(contract, `"`+string(act)+`"`) {
			t.Errorf("contract does not name %q:\n%s", act, contract)
		}
	}
	for _, want := range []string{`"overdue"`, "at most " + maxLeftText, "There is no act for deferring", "keeps it with this account on the pass's record"} {
		if !strings.Contains(contract, want) {
			t.Errorf("contract does not say %q", want)
		}
	}
	back := OverdueSentBack([]string{"x-1 (the account records nothing about it)"})
	if !strings.Contains(back, "x-1 (the account records nothing about it)") || !strings.Contains(back, "Everything else this pass did stands") {
		t.Errorf("sent back = %q", back)
	}
	if !strings.Contains(strings.Join(accountShape, "\n"), `"overdue"`) {
		t.Error("the account shape a correction quotes leaves out the overdue list")
	}
}
