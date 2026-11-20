package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An item's budgets are read for where the room came from as well as for how
// much of it is left. The operator's own override is something they did and can
// remember doing; a delegated crossing is something that happened while they
// were not asked, so the two are labelled apart and the count of the second is
// said under them.
//
// docs/configuration.md quotes these lines as an example of what `yoyo status`
// prints, so this is also what keeps that document from going stale silently.
func TestStatusSaysWhichCrossingsWereTheDevelopmentManagersOwn(t *testing.T) {
	t.Parallel()

	crossed := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	counters := runstate.TriageCounters{
		WorkItemID:   "yoyodyne-ifd.143",
		ReviewRounds: 4,
		Overrides: []runstate.TriageOverride{
			{
				Budget:    runstate.TriageReviewRoundBudget,
				Cap:       5,
				DecidedBy: domain.RoleDevelopmentManager.Title(),
				DecidedAt: crossed,
				Reason:    "the change was right and the ground moved under it",
				CrossedBy: domain.RoleDevelopmentManager,
			},
			{
				Budget:    runstate.TriageRerunBudget,
				Cap:       3,
				DecidedBy: "mason",
				DecidedAt: crossed.Add(time.Hour),
				Reason:    "driven by hand until it lands",
			},
		},
	}

	var out bytes.Buffer
	printItemTriage(&out, counters, runstate.TriageCaps{ReviewRounds: 5, Reruns: 3, MergeRearms: 2})
	rendered := out.String()
	for _, want := range []string{
		"cap crossed on delegated authority: raised the review round cap to 5, crossed by the development manager on delegated authority at 2026-09-06T09:00:00Z: the change was right and the ground moved under it",
		"operator override: raised the re-run cap to 3, decided by mason",
		"1 of 5 cap crossing(s) the development manager may take herself are recorded; past those the Lead Product Manager may grant 5 more, and no role raises the item past 10 in all",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}

	// A raise the Lead Product Manager granted is labelled as hers, with her count.
	granted := counters
	granted.Overrides = append(append([]runstate.TriageOverride{}, counters.Overrides...), runstate.TriageOverride{
		Budget:    runstate.TriageReviewRoundBudget,
		Cap:       6,
		DecidedBy: domain.RoleProductManager.Title(),
		DecidedAt: crossed.Add(2 * time.Hour),
		Reason:    "worth more than the work waiting behind it",
		CrossedBy: domain.RoleProductManager,
	})
	var lead bytes.Buffer
	printItemTriage(&lead, granted, runstate.TriageCaps{ReviewRounds: 5, Reruns: 3, MergeRearms: 2})
	for _, want := range []string{
		"raise granted by the Lead Product Manager: raised the review round cap to 6, granted by the Lead Product Manager past the development manager's own raises",
		"1 of 5 raise(s) the Lead Product Manager may grant past those are recorded; you are told of each afterwards, and none is asked of you",
	} {
		if !strings.Contains(lead.String(), want) {
			t.Fatalf("rendered = %q, want it to contain %q", lead.String(), want)
		}
	}

	// The count is said only where there is one. An item nobody has crossed
	// anything on is every item, and a line announcing an untouched delegation on
	// each of them is one an operator learns to skip.
	var plain bytes.Buffer
	printItemTriage(&plain, runstate.TriageCounters{WorkItemID: "yoyodyne-ifd.143", ReviewRounds: 1},
		runstate.TriageCaps{ReviewRounds: 4, MergeRearms: 2})
	if strings.Contains(plain.String(), "cap crossing(s)") {
		t.Fatalf("rendered = %q, want no crossing count on an item nobody has crossed", plain.String())
	}
}
