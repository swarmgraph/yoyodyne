package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What the development manager is told to do about a pull request the
// clean-up of earlier runs kept or could not close says what is true of that
// case, not the instruction meant for a leftover remote branch.
func TestAKeptOrUnclosedPullRequestTellsTheDevelopmentManagerWhatIsTrue(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		problem string
		says    []string
	}{
		"a closed item's kept request": {
			problem: "the work item is closed, but nothing shows its change on main or names what replaced it, so this may be the only copy of the work; pull request #863 " + ownership.PublicationKeptForDecision + " to decide",
			says:    []string{"close the pull request", "reopen the item"},
		},
		"a retired item's kept request": {
			problem: "the work item was retired without being done, and nothing shows its change on main or names what replaced it; pull request #959 " + ownership.PublicationKeptForDecision,
			says:    []string{"next `yoyo reconcile` closes this pull request", "reopen the item before then"},
		},
		"a close the forge refused": {
			problem: "pull request #1496 " + ownership.PublicationNotClosed + ": gh: timed out",
			says:    []string{"tries the close again"},
		},
	}
	for name, c := range cases {
		whose := reconcileFindingWhose(MoverDevelopmentManager, []runstate.ReconcileFinding{{Step: runstate.ReconcileSuperseded, Problem: c.problem}})
		for _, said := range c.says {
			if !strings.Contains(whose, said) {
				t.Errorf("%s: %q does not say %q", name, whose, said)
			}
		}
		if strings.Contains(whose, "retries the deletion") || strings.Contains(whose, "leftover remote branch") {
			t.Errorf("%s: %q gives the leftover-branch instruction", name, whose)
		}
	}
}
