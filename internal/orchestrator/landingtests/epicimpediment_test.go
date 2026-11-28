package landingtests

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
)

// A landing is the developer's way of making its item wait on other work, so it
// is held to the rule every role's link is: while epic mode is on, a member of a
// named epic is not made to wait on open work outside it, because nothing would
// start that work without the operator. The item is parked instead, and the
// reason names the epic and the add that would bring the work in. Work inside the
// same epic is waited on as asked.
func TestALandingCannotMakeAMemberWaitOnOpenWorkOutsideItsEpic(t *testing.T) {
	t.Parallel()

	for _, inside := range []bool{false, true} {
		tracker := testharness.NewOutcomeTracker().Holds("yoyodyne-impediment")
		provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
		provider.DeveloperFinalText = "the diagnosis\n\n" +
			testharness.LandingBlock(`{"outcome":"evidence","why":"it needs yoyodyne-impediment first","blocked_by":"yoyodyne-impediment"}`)
		pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})
		member := tracker.Item.ID
		pipeline.EpicHolding = func(item string) (string, func(string) bool, error) {
			if item != member {
				return "", nil, nil
			}
			return "yoyodyne-epic", func(other string) bool { return other == member || inside && other == "yoyodyne-impediment" }, nil
		}

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if inside {
			if len(tracker.Blockers) != 1 || tracker.Item.Parking.Parked() {
				t.Fatalf("work inside the epic: blockers %v, parking %q; want the item waiting on it", tracker.Blockers, tracker.Item.Parking)
			}
			continue
		}
		if len(tracker.Blockers) != 0 {
			t.Fatalf("the member was made to wait on open work outside its epic: %v", tracker.Blockers)
		}
		if !tracker.Item.Parking.Parked() || outcome.LandingBlockedBy != "" {
			t.Fatalf("parking %q, marker %q; want the item parked with no marker", tracker.Item.Parking, outcome.LandingBlockedBy)
		}
		for _, want := range []string{"is in epic yoyodyne-epic", "open outside it", "`yoyo epic add yoyodyne-epic yoyodyne-impediment`"} {
			if !strings.Contains(outcome.LandingImpedimentProblem, want) {
				t.Errorf("the outcome does not say %q: %q", want, outcome.LandingImpedimentProblem)
			}
		}
		// The parking reason is cut to what the tracker holds, so it is held only to
		// naming the epic.
		if !strings.Contains(tracker.Item.Parking.Reason(), "is in epic yoyodyne-epic") {
			t.Errorf("the parking reason does not name the epic: %q", tracker.Item.Parking)
		}
	}
}
