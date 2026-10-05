package goal

import (
	"slices"
	"testing"
)

func TestRelevantGoalsResolveLikeTheServedGoal(t *testing.T) {
	set := setWithGoals(t, "Keep the work traceable.", "Use ordinary words.")
	got, err := set.ResolveRelevant([]string{"keep the work traceable", "Use ordinary words."})
	if err != nil || !slices.Equal(got, []string{"Keep the work traceable.", "Use ordinary words."}) {
		t.Fatalf("resolved = %q, %v", got, err)
	}
	for _, invalid := range [][]string{{"Unknown goal."}, {""}, {"two\nlines"}, make([]string, MaxRelevantGoals+1)} {
		if _, err := set.ResolveRelevant(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	untouched, err := set.ResolveRelevant(nil)
	if err != nil || untouched != nil {
		t.Fatalf("omitted list = %v, %v", untouched, err)
	}
	cleared, err := set.ResolveRelevant([]string{})
	if err != nil || cleared == nil || len(cleared) != 0 {
		t.Fatalf("clear = %v, %v", cleared, err)
	}
}
