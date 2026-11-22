package ownership

import (
	"regexp"
	"slices"
	"strings"
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

// A list of the operator's reasons that leaves some out is found wherever it
// is written, and a full list, a single reason, and the person-only remedy's
// own shorter list are not.
func TestShortReasonListsFindsAListMissingAReason(t *testing.T) {
	t.Parallel()

	full := "given as `fundamental-intent`, `credential`, `repository-setting`, `protected-file`,\n`human-gate`, `own-hold`, or `diverged-history`."
	short := "given as \"fundamental-intent\", \"credential\",\n\"repository-setting\", or \"protected-file\"."
	single := "such as `protected-file: …`, opening the message."
	personOnly := "The reasons `person_only` permits are `credential`, `repository-setting`,\nand `protected-file`."
	notPersonOnly := "The reasons are `credential`, `repository-setting`, and `protected-file`."

	got := ShortReasonLists(strings.Join([]string{full, short, single, personOnly, notPersonOnly}, "\n\n"))
	if len(got) != 2 || got[0] != short || got[1] != notPersonOnly {
		t.Fatalf("ShortReasonLists = %q, want the four-reason list and the three-reason list outside person_only", got)
	}
	if got := ShortReasonLists(OperatorReasonsInContract); len(got) != 0 {
		t.Fatalf("the contract's own list reads as short: %q", got)
	}
}
