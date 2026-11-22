package doclink

// The rules that keep an item out of a scheduling pass are stated in more than
// one guide, and the copies drift. On 2026-09-04 the configuration guide named
// seven where the work guide named nine, and the two it had lost were parking —
// the rule added after a draining queue started deferred work and spent $34.38 —
// and the conversation executor. An operator reading only the configuration
// guide could not learn that parking exists. So each guide carries the list
// between two markers, and this holds the lists to the same names in the same
// order.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// selectionRuleGuides are the documents that state the list. The first is the
// one the others are compared against.
var selectionRuleGuides = []string{
	"docs/work.md",
	"docs/configuration/runs.md",
}

const (
	selectionRulesOpen  = "<!-- selection-rules"
	selectionRulesClose = "<!-- /selection-rules -->"
)

// selectionRuleName is a numbered entry's opening bold phrase: the rule's name.
var selectionRuleName = regexp.MustCompile(`^\d+\. \*\*([^*]+)\*\*`)

func selectionRules(t *testing.T, document string) []string {
	t.Helper()
	content, err := read(filepath.Join(repositoryRoot, filepath.FromSlash(document)))
	if err != nil {
		t.Fatalf("read %s: %v", document, err)
	}
	if strings.Count(content, selectionRulesOpen) != 1 || strings.Count(content, selectionRulesClose) != 1 {
		t.Fatalf("%s must carry exactly one selection-rules list between %q and %q", document, selectionRulesOpen, selectionRulesClose)
	}
	start := strings.Index(content, selectionRulesOpen)
	end := strings.Index(content, selectionRulesClose)
	if end < start {
		t.Fatalf("%s closes its selection-rules list before opening it", document)
	}
	var names []string
	for _, line := range strings.Split(content[start:end], "\n") {
		if match := selectionRuleName.FindStringSubmatch(line); match != nil {
			names = append(names, match[1])
		}
	}
	return names
}

func TestEveryGuideNamesTheSameSelectionRulesInTheSameOrder(t *testing.T) {
	t.Parallel()

	want := selectionRules(t, selectionRuleGuides[0])
	// An empty list would agree with every other empty list, which is the one way
	// this could pass while holding nothing together.
	if len(want) == 0 {
		t.Fatalf("%s carries no numbered, bold-named rules between its selection-rules markers", selectionRuleGuides[0])
	}
	for _, document := range selectionRuleGuides[1:] {
		got := selectionRules(t, document)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s names the selection rules\n  %s\nwhere %s names\n  %s\nthe lists must match in names and order",
				document, strings.Join(got, "\n  "), selectionRuleGuides[0], strings.Join(want, "\n  "))
		}
	}
}

// Parking and the conversation executor are the two a guide lost before, so
// they are asserted by name rather than only by agreement: two guides that both
// dropped them would still agree.
func TestTheSelectionRulesIncludeParkingAndTheConversationExecutor(t *testing.T) {
	t.Parallel()

	rules := selectionRules(t, selectionRuleGuides[0])
	for _, required := range []string{"Parking", "A conversation executor"} {
		found := false
		for _, rule := range rules {
			if rule == required {
				found = true
			}
		}
		if !found {
			t.Errorf("%s's selection rules do not include %q: %v", selectionRuleGuides[0], required, rules)
		}
	}
}
