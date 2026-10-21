package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// slowTrackerRead is how the tracker reports a read of the item it did not
// answer within its bound on any attempt.
var slowTrackerRead = fmt.Errorf("bd show %s did not answer within its 30s bound on any of 3 attempts over 1m40s: %w",
	docketedItem, beads.ErrUnanswered)

// A decided re-run whose read of the item timed out is left waiting, not
// refused, and the next pass carries it out once the tracker answers: well
// inside the pacing a refusal would have been given, because a slow tracker is
// not an answer about the decision.
func TestADecidedRerunStoppedByASlowTrackerIsTriedAgainAndCarriedOut(t *testing.T) {
	t.Parallel()
	harness := newRerunHarness(t, stoppedState())
	tracker := &orchestratortest.Tracker{Item: harness.item}
	harness.itemErr = slowTrackerRead
	watch := harness.carryOut()
	watch.Notes = tracker

	first, _, err := watch.Carry(context.Background(), theOneOutstanding(t, watch))
	if err != nil || first.Carried || !first.Waiting || first.Cause != "" || first.Gate != runstate.TriageGateHarness {
		t.Fatalf("Carry() = %+v, %v; want a wait on the slow tracker, not a refusal", first, err)
	}
	for _, want := range []string{"is waiting on", "tracker was too slow to answer", "will try it again at a later pass", "did not answer within its 30s bound"} {
		if !strings.Contains(first.Problem, want) {
			t.Fatalf("Problem = %q, want it to say %q", first.Problem, want)
		}
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	finding, found := counters.CarryOutOf(docketedRunID)
	if !found || !finding.Waiting || finding.Cause != "" || finding.Attempts != 1 {
		t.Fatalf("finding = %+v, %v; want one waiting attempt on the item's record", finding, found)
	}
	if refused, found := counters.RefusedCarryOut(docketedRunID); found {
		t.Fatalf("RefusedCarryOut() = %+v; a slow tracker must not hand the decision back as refused", refused)
	}
	wait := tracker.Item.SchedulingWait
	if wait == nil || !strings.Contains(wait.Reason, "tracker was too slow to answer") || !strings.Contains(wait.Reason, "try it again") {
		t.Fatalf("scheduling wait = %+v; want it to say the tracker was slow and it will be tried again", wait)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed || len(harness.started) != 0 {
		t.Fatalf("a re-run stopped by a slow tracker claimed its stoppage or started %+v", harness.started)
	}

	// A minute later the tracker answers, and the same decision is offered and
	// carried out.
	harness.itemErr = nil
	next := harness.carryOutAt(time.Minute)
	next.Notes = tracker
	second, _, err := next.Carry(context.Background(), theOneOutstanding(t, next))
	if err != nil || !second.Carried {
		t.Fatalf("second Carry() = %+v, %v; want the decision carried out", second, err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %+v, want exactly the one fresh run", harness.started)
	}
	counters, err = harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	if finding, found := counters.CarryOutOf(docketedRunID); found {
		t.Fatalf("finding = %+v; want the wait cleared once the decision was carried out", finding)
	}
}

// A re-run refused for anything other than a slow tracker is refused as before:
// paced, and handed back to the development manager.
func TestADecidedRerunRefusedForAnotherReasonIsStillRefused(t *testing.T) {
	t.Parallel()
	harness := newRerunHarness(t, stoppedState())
	harness.itemErr = errors.New("bd show failed with status failed and exit code 1: the store's schema is newer than this bd")
	watch := harness.carryOut()

	carried, _, err := watch.Carry(context.Background(), theOneOutstanding(t, watch))
	if err != nil || carried.Carried || carried.Waiting || !strings.Contains(carried.Problem, "was refused by") {
		t.Fatalf("Carry() = %+v, %v; want a refusal", carried, err)
	}
	if strings.Contains(carried.Problem, "too slow") {
		t.Fatalf("Problem = %q; a refusal that is not about a slow tracker must not say it is", carried.Problem)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	if refused, found := counters.RefusedCarryOut(docketedRunID); !found || refused.Waiting {
		t.Fatalf("RefusedCarryOut() = %+v, %v; want the refusal standing", refused, found)
	}
	harness.itemErr = nil
	if outstanding, err := harness.carryOutAt(time.Minute).Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("Outstanding() a minute later = %+v, %v; want the refusal paced", outstanding, err)
	}
}

// Only a failure that says the tracker did not answer is a wait; a refusal the
// action classed as permanent stays permanent whatever it wraps.
func TestOnlyAnUnansweredTrackerCallIsASlowTracker(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"timed-out read", slowTrackerRead, true},
		{"export behind another export", fmt.Errorf("refresh the export: %w", beads.ErrExportBusy), true},
		{"tracker refused", errors.New("bd show failed with status failed and exit code 1: refused"), false},
		{"no such item", fmt.Errorf("read: %w", beads.ErrNoSuchWorkItem), false},
		{"permanent", permanentCarryOut(triage.CarryOutDecisionSuperseded, slowTrackerRead), false},
		{"nothing", nil, false},
	} {
		if got := trackerWasSlow(test.err); got != test.want {
			t.Errorf("%s: trackerWasSlow(%v) = %v, want %v", test.name, test.err, got, test.want)
		}
	}
}
