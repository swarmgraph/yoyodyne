package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeLandingHolds struct {
	holds []runstate.LandingHold
	err   error
}

func (f fakeLandingHolds) List(time.Time) ([]runstate.LandingHold, error) { return f.holds, f.err }

func TestLandingHoldIsShownWithItsReasonAndTimes(t *testing.T) {
	t.Parallel()
	setAt := time.Date(2026, 11, 9, 12, 2, 0, 0, time.UTC)
	hold := runstate.LandingHold{TargetBranch: "staging", Reason: "cutting release 1.2", SetAt: setAt, Until: setAt.Add(4 * time.Hour)}
	rendered := Standing{LandingHolds: []runstate.LandingHold{hold}}.RenderLandingHolds()
	for _, want := range []string{
		"Landing on staging is held: cutting release 1.2.",
		"Set at " + setAt.Local().Format("2006-01-02 15:04 MST"),
		"ends at " + setAt.Add(4*time.Hour).Local().Format("2006-01-02 15:04 MST"),
		"`yoyo landing release staging`",
		"Checks and reviews carry on",
		"waits with its evidence kept",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderLandingHolds() = %q, want it to contain %q", rendered, want)
		}
	}
}

func TestNoLandingHoldRendersNothing(t *testing.T) {
	t.Parallel()
	if rendered := (Standing{}).RenderLandingHolds(); rendered != "" {
		t.Fatalf("RenderLandingHolds() = %q, want nothing", rendered)
	}
}

func TestLandingHoldsThatCannotBeListedAreSaid(t *testing.T) {
	t.Parallel()
	standing := ReadStanding(context.Background(), Sources{LandingHolds: fakeLandingHolds{err: errors.New("disk on fire")}})
	if !strings.Contains(standing.LandingHoldsProblem, "disk on fire") {
		t.Fatalf("LandingHoldsProblem = %q, want the error", standing.LandingHoldsProblem)
	}
	if rendered := standing.RenderLandingHolds(); !strings.Contains(rendered, "disk on fire") {
		t.Fatalf("RenderLandingHolds() = %q, want the problem said", rendered)
	}
}

func TestStandingCarriesTheLandingHolds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	hold := runstate.LandingHold{TargetBranch: "staging", Reason: "migration", SetAt: now, Until: now.Add(time.Hour)}
	standing := ReadStanding(context.Background(), Sources{LandingHolds: fakeLandingHolds{holds: []runstate.LandingHold{hold}}, Now: func() time.Time { return now }})
	if len(standing.LandingHolds) != 1 || standing.LandingHolds[0].TargetBranch != "staging" || standing.LandingHoldsProblem != "" {
		t.Fatalf("LandingHolds = %#v, problem %q, want the staging hold", standing.LandingHolds, standing.LandingHoldsProblem)
	}
}
