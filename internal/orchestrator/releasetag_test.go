package orchestrator

// A release item's run lands the release's notes, and its tag is cut after
// them, so the run that lands the notes must not close the item: the v0.6.0 and
// v0.7.0 items both closed that way with no tag ever pushed. The run is driven
// through the whole pipeline with a developer that claims nothing, which is the
// default that closed both.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/releasecut"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func decidedRelease() beads.WorkItem {
	return beads.WorkItem{
		ID: "yoyodyne-rel", Title: "Cut v0.7.0: the release since v0.6.0", Status: "open",
		Origin: domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager},
	}
}

func TestAReleaseItemStaysOpenForItsTagWhenItsNotesLand(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.Tracker{Item: decidedRelease()}
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the notes integrated", outcome)
	}
	if outcome.WorkItemClosed || tracker.Closed || tracker.Item.Status == "closed" {
		t.Fatalf("the release item closed when its notes landed; calls = %v", tracker.Calls)
	}
	if want := releasecut.TagOwedParking("v0.7.0", outcome.RunID); tracker.Item.Parking != want {
		t.Fatalf("parking = %q, want %q", tracker.Item.Parking, want)
	}
	if version, owed := releasecut.OwesTag(tracker.Item); !owed || version != "v0.7.0" {
		t.Errorf("the release step would not read the item as owing v0.7.0: %q, %v", version, owed)
	}
	if !strings.Contains(tracker.ReopenReason, "notes") || !strings.Contains(tracker.ReopenReason, "the v0.7.0 tag is still owed") {
		t.Errorf("reason = %q, want it to say the notes landed and the tag is owed", tracker.ReopenReason)
	}
	if !outcome.OwesReleaseTag() || outcome.Discharges() ||
		!strings.Contains(outcome.UndischargedDisposition(), "until the v0.7.0 tag is on the remote") {
		t.Errorf("outcome: owes = %t, discharges = %t, disposition = %q", outcome.OwesReleaseTag(), outcome.Discharges(), outcome.UndischargedDisposition())
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReleaseTagOwed != "v0.7.0" || state.Discharges() || !state.Parks() || !state.OwesReleaseTag() {
		t.Errorf("record: owed = %q, discharges = %t, parks = %t", state.ReleaseTagOwed, state.Discharges(), state.Parks())
	}
}

// Only a release the Lead Product Manager decided is held: an item titled the
// same that she did not admit, or one that is not a release, closes as before.
func TestOnlyADecidedReleaseOwesATag(t *testing.T) {
	t.Parallel()

	for _, item := range []beads.WorkItem{
		{ID: "yoyodyne-task", Title: "Task", Status: "open"},
		{ID: "yoyodyne-rel", Title: "Cut v0.7.0: the release since v0.6.0", Status: "open"},
	} {
		run := &activeRun{item: item}
		run.claimLanding(context.Background(), "the work is done")
		if run.state.ReleaseTagOwed != "" || !run.state.Discharges() {
			t.Errorf("%s: owed = %q, discharges = %t; want it closed as before", item.Title, run.state.ReleaseTagOwed, run.state.Discharges())
		}
	}
	run := &activeRun{item: decidedRelease()}
	run.claimLanding(context.Background(), "the notes are written")
	if run.state.ReleaseTagOwed != "v0.7.0" || run.state.Discharges() {
		t.Errorf("a decided release: owed = %q, discharges = %t", run.state.ReleaseTagOwed, run.state.Discharges())
	}
	if !strings.Contains(describeLanding(run.state), "stays open after the change lands until the v0.7.0 tag is on the remote") {
		t.Errorf("the reviewer is not told the release stays open: %q", describeLanding(run.state))
	}
	// A developer that said its change is evidence is answered for in its own
	// words, not as a release owing its tag.
	evidence := run.state
	evidence.LandingOutcome, evidence.LandingReason = runstate.LandingEvidence, "the notes cannot be written yet"
	if evidence.OwesReleaseTag() || strings.Contains(undischargedLandingReason(evidence), "tag is still owed") {
		t.Errorf("an evidence landing on a release reads as a tag owed: %q", undischargedLandingReason(evidence))
	}
}
