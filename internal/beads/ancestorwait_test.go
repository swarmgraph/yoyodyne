package beads

import (
	"errors"
	"slices"
	"testing"
)

func ancestry(items ...WorkItem) func(string) (WorkItem, bool, error) {
	byID := map[string]WorkItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	return func(id string) (WorkItem, bool, error) {
		item, found := byID[id]
		return item, found, nil
	}
}

// A wait on the item's parent, or on an item further up, is found with the
// chain that leads there; one on an unrelated item, or a sibling, is not.
func TestAncestorChainFindsAParentAndAGrandparentAndNothingElse(t *testing.T) {
	t.Parallel()

	lookup := ancestry(
		WorkItem{ID: "p-1.2.3", Parent: "p-1.2"},
		WorkItem{ID: "p-1.2", Parent: "p-1"},
		WorkItem{ID: "p-1"},
		WorkItem{ID: "p-1.2.4", Parent: "p-1.2"},
		WorkItem{ID: "p-9"},
	)
	for _, test := range []struct {
		waitsOn string
		want    []string
	}{
		{"p-1.2", []string{"p-1.2"}},
		{"p-1", []string{"p-1.2", "p-1"}},
		{"p-9", nil},
		{"p-1.2.4", nil},
		{"p-1.2.3", nil},
	} {
		chain, err := AncestorChain("p-1.2.3", test.waitsOn, lookup)
		if err != nil {
			t.Fatalf("AncestorChain(%s) error = %v", test.waitsOn, err)
		}
		if !slices.Equal(chain, test.want) {
			t.Fatalf("AncestorChain(%s) = %v, want %v", test.waitsOn, chain, test.want)
		}
	}
	if got := DescribeAncestry("p-1.2.3", []string{"p-1.2", "p-1"}); got != "p-1.2.3 is under p-1.2, which is under p-1" {
		t.Fatalf("DescribeAncestry() = %q", got)
	}
}

// A parent named only by a parent-child link is a parent all the same, and a
// tracker that cannot be read is said rather than taken for no ancestor.
func TestAncestorChainReadsParentLinksAndReportsAnUnreadTracker(t *testing.T) {
	t.Parallel()

	linked := ancestry(
		WorkItem{ID: "p-6u6", Dependencies: []Dependency{{ID: "p-78", Type: "parent-child"}}},
		WorkItem{ID: "p-78"},
	)
	if chain, err := AncestorChain("p-6u6", "p-78", linked); err != nil || !slices.Equal(chain, []string{"p-78"}) {
		t.Fatalf("AncestorChain(linked parent) = %v, %v", chain, err)
	}
	failing := func(string) (WorkItem, bool, error) { return WorkItem{}, false, errors.New("store locked") }
	if _, err := AncestorChain("p-6u6", "p-78", failing); err == nil {
		t.Fatal("AncestorChain() over an unreadable tracker = nil error, want it said")
	}
}
