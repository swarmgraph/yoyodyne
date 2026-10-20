package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeDivergences struct {
	standing []runstate.DivergedTarget
	fail     error
}

func (f fakeDivergences) Standing() ([]runstate.DivergedTarget, error) {
	return f.standing, f.fail
}

var wedged = time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)

func wedgedMain() runstate.DivergedTarget {
	return runstate.DivergedTarget{
		TargetBranch: "main",
		Remote:       "origin",
		LocalCommit:  "4d7e805",
		RemoteCommit: "9f1c2ab",
		Held:         "main on origin is at 9f1c2ab, which does not contain the local main at 4d7e805; only a person can say which history is right",
		Since:        wedged,
		LastSeen:     wedged,
		Refusals:     1,
		RunID:        "run-one",
		WorkItemID:   "yoyodyne-one",
	}
}

// A diverged target is why nothing starts, named apart from the brake and from
// an intake hold somebody placed, as the operator's with the recovery steps —
// and it is read ahead of a full machine because every run holding a slot will
// stop on it too.
func TestADivergedTargetIsWhyNothingStartsAndTheOperatorsWithTheRecovery(t *testing.T) {
	t.Parallel()

	stall := WhyNothingStarts(Conditions{Diverged: []runstate.DivergedTarget{wedgedMain()}, Running: 2, Capacity: 2})
	if stall.Reason != ReasonDivergedTarget || stall.Reason == ReasonIntakeHold {
		t.Fatalf("stall = %+v, want the diverged target named as its own reason", stall)
	}
	for _, want := range []string{"main will not catch up to the remote's", "9f1c2ab", "4d7e805"} {
		if !strings.Contains(stall.Says, want) {
			t.Fatalf("says = %q, want %q in it", stall.Says, want)
		}
	}
	if !strings.Contains(stall.Refusal(), "Unwedging a target branch that diverged from the forge") {
		t.Fatalf("refusal = %q, want the recovery named against each item", stall.Refusal())
	}
	waiting, attention := stall.Waiting()
	if !attention || waiting.Mover != ownership.MoverOperator {
		t.Fatalf("waiting = %+v, %t; want the divergence waiting on the operator", waiting, attention)
	}
	whose := waiting.Whose()
	if !strings.HasPrefix(whose, "the operator's") || !strings.Contains(whose, "Unwedging a target branch that diverged from the forge") || !strings.Contains(whose, "yoyo reconcile") {
		t.Fatalf("whose = %q, want the operator's move with the recovery and what lifts it", whose)
	}
	if strings.Contains(whose, "yoyo release") {
		t.Fatalf("whose = %q, want no release prescribed for a hold it does not lift", whose)
	}
	// The switches somebody placed still answer first.
	if held := WhyNothingStarts(Conditions{IntakeHeld: true, Diverged: []runstate.DivergedTarget{wedgedMain()}}); held.Reason != ReasonIntakeHold {
		t.Fatalf("stall = %+v, want an intake hold somebody placed answered first", held)
	}
}

// The divergence is on the attention line whatever the queue holds, once, and
// carried whole for the surfaces that read the model.
func TestADivergedTargetIsOnTheAttentionLineOnce(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.DivergedTargets = fakeDivergences{standing: []runstate.DivergedTarget{wedgedMain()}}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.DivergedTargets) != 1 || standing.DivergedTargets[0].TargetBranch != "main" {
		t.Fatalf("diverged targets = %+v, want the divergence carried", standing.DivergedTargets)
	}
	found := 0
	for _, attention := range standing.NeedsHuman {
		if attention.ID == "diverged-target:main" {
			found++
			if attention.Mover != ownership.MoverOperator || !strings.Contains(attention.Whose(), "docs/operations.md") {
				t.Fatalf("attention = %+v, want the operator's with the recovery", attention)
			}
		}
	}
	if found != 1 {
		t.Fatalf("needs a human = %+v, want the divergence exactly once", standing.NeedsHuman)
	}
}

// Two branches diverged at once are two entries a surface reading the
// structured line can tell apart, each keyed to its branch.
func TestTwoDivergedTargetsAreTwoAttentionEntries(t *testing.T) {
	t.Parallel()

	release := wedgedMain()
	release.TargetBranch = "release"
	sources := quietSources()
	sources.DivergedTargets = fakeDivergences{standing: []runstate.DivergedTarget{wedgedMain(), release}}

	standing := ReadStanding(context.Background(), sources)
	ids := map[string]int{}
	for _, attention := range standing.NeedsHuman {
		if attention.Stall != nil && attention.Stall.Reason == ReasonDivergedTarget {
			ids[attention.ID]++
		}
	}
	if len(ids) != 2 || ids["diverged-target:main"] != 1 || ids["diverged-target:release"] != 1 {
		t.Fatalf("diverged-target attention ids = %v, want one keyed to each branch", ids)
	}
}

// A record that could not be read is said rather than read as no divergence.
func TestAnUnreadableDivergenceRecordIsSaid(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.DivergedTargets = fakeDivergences{fail: errors.New("permission denied")}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.DivergedTargets) != 0 {
		t.Fatalf("diverged targets = %+v, want none invented over a record nobody could read", standing.DivergedTargets)
	}
	if !strings.Contains(standing.NeedsHumanProblem, "whether a target branch stands diverged") {
		t.Fatalf("problem = %q, want the unreadable record named", standing.NeedsHumanProblem)
	}
}

// A line held on a divergence is held on purpose and says why, so the stall
// alarm does not read it as a machine that died.
func TestADivergedTargetAccountsForTheQuiet(t *testing.T) {
	t.Parallel()

	activity := Activity{
		Since:    wedged.Add(-time.Hour),
		Diverged: []runstate.DivergedTarget{wedgedMain()},
		Watched:  true,
		Now:      wedged.Add(24 * time.Hour),
	}
	if activity.Unexplained() {
		t.Fatal("a line held on a diverged target was read as unexplained, want it accounted for")
	}
}
