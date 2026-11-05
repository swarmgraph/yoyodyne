package maintain

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// withCalls wires a log of the harness's tracker calls, with the calls given
// already in it, and a report pile.
func withCalls(t *testing.T, f *fixture, calls ...beads.LoggedCall) (*runstate.TrackerCallLogStore, *runstate.ReportStore) {
	t.Helper()
	root := t.TempDir()
	log, err := runstate.NewTrackerCallLogStore(root, "calc", "yoyo watch")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		log.LogCall(call)
	}
	if err := log.Failed(); err != nil {
		t.Fatal(err)
	}
	reports, err := runstate.NewReportStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	f.pass.Calls, f.pass.Reports, f.pass.RepositoryID = log, reports, "calc"
	return log, reports
}

func listing(at time.Time, who string, took time.Duration) beads.LoggedCall {
	return beads.LoggedCall{At: at, For: who, Call: "listing the work items ready to start", Verb: "ready", Took: took}
}

// Within both bounds, the step says the figures and warns nobody.
func TestATrackerWithinItsLimitsIsSaidSo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	withTrackerStore(t, f, &fakeStore{size: 500 * megabyte, after: 500 * megabyte, items: 900})
	_, reports := withCalls(t, f,
		listing(f.now.Add(-30*time.Minute), "yoyo watch", 4*time.Second),
		// Older than the hour read, and slow.
		listing(f.now.Add(-2*time.Hour), "yoyo watch", time.Minute),
		// Not a listing of open or ready work.
		beads.LoggedCall{At: f.now.Add(-10 * time.Minute), Call: "listing the work items of every status", Verb: "list", Took: time.Minute},
	)

	step := stepNamed(t, f.pass1(t), StepTrackerLimits)
	if step.Outcome != runstate.StepRan || !strings.HasPrefix(step.Detail, "the tracker is within its limits") {
		t.Fatalf("limits step = %+v, want it within its limits", step)
	}
	for _, want := range []string{"the store is 500 MB on disk, within its bound of 1024 MB", "every listing of open or ready work in the hour before (1 of them) took at most 10 seconds, the slowest 4.0 seconds"} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("limits step = %q, want %q in it", step.Detail, want)
		}
	}
	if filed, _ := reports.List(); len(filed) != 0 {
		t.Errorf("filed %+v, want no warning", filed)
	}
}

// A store past its size bound and listings past their time bound are each
// said in ordinary words with the setting that bounds them, and the
// development manager is warned once; a pass that finds the same again does
// not warn again, and one that finds a different bound passed does.
func TestATrackerPastItsLimitsWarnsTheDevelopmentManagerOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := &fakeStore{size: 1200 * megabyte, after: 1200 * megabyte, items: 900}
	withTrackerStore(t, f, store)
	f.pass.TrackerLimits = config.TrackerLimits{StoreMegabytes: 1000, ListingSeconds: 10}
	log, reports := withCalls(t, f,
		listing(f.now.Add(-20*time.Minute), "developer run run-1 for calc-1", 25*time.Second),
		listing(f.now.Add(-10*time.Minute), "yoyo watch", 3*time.Second),
	)

	step := stepNamed(t, f.pass1(t), StepTrackerLimits)
	for _, want := range []string{
		"THE TRACKER IS PAST ITS LIMITS",
		"the tracker's store is 1200 MB on disk, past its bound of 1000 MB (services.maintenance.tracker_limits.store_megabytes)",
		"1 of the 2 listings of open or ready work the harness made in the hour before took longer than their bound of 10 seconds (services.maintenance.tracker_limits.listing_seconds); the slowest, listing the work items ready to start for developer runs, took 25.0 seconds",
		"the development manager was warned",
	} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("limits step = %q\nwant %q in it", step.Detail, want)
		}
	}
	filed, err := reports.List()
	if err != nil || len(filed) != 1 || filed[0].For != domain.RoleDevelopmentManager || !strings.Contains(filed[0].Message, "past its bound of 1000 MB") {
		t.Fatalf("filed %+v, %v, want one warning for the development manager", filed, err)
	}

	f.now = f.now.Add(15 * time.Minute)
	again := stepNamed(t, f.pass1(t), StepTrackerLimits)
	if !strings.Contains(again.Detail, "the development manager was already warned") {
		t.Errorf("second limits step = %q, want no second warning", again.Detail)
	}
	if filed, _ := reports.List(); len(filed) != 1 {
		t.Errorf("filed %d warnings, want still one", len(filed))
	}

	// The listings are now within their bound, the store still is not.
	f.now = f.now.Add(2 * time.Hour)
	log.LogCall(listing(f.now.Add(-time.Minute), "yoyo watch", time.Second))
	changed := stepNamed(t, f.pass1(t), StepTrackerLimits)
	if !strings.Contains(changed.Detail, "the development manager was warned") || strings.Contains(changed.Detail, "listing_seconds") {
		t.Errorf("third limits step = %q, want a new warning for the store alone", changed.Detail)
	}
	if filed, _ := reports.List(); len(filed) != 2 {
		t.Errorf("filed %d warnings, want a second for what changed", len(filed))
	}
}

// With no log of calls wired the size is still checked, and the step says the
// listings were not.
func TestTheLimitsAreCheckedWithoutACallLog(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	withTrackerStore(t, f, &fakeStore{size: 100 * megabyte, after: 100 * megabyte, items: 900})

	step := stepNamed(t, f.pass1(t), StepTrackerLimits)
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "within its bound") || !strings.Contains(step.Detail, "the listings' times are not checked") {
		t.Errorf("limits step = %+v, want the size checked and the listings said to be unchecked", step)
	}
}

// A measurement counts the harness's own calls over the hour before it by
// caller, and says how much the store grew since the measurement before.
func TestAMeasurementCountsTheHarnesssCallsByCaller(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	withTrackerStore(t, f, &fakeStore{size: 930 * megabyte, after: 930 * megabyte, items: 900})
	measuredAt := f.now.Add(time.Minute)
	withCalls(t, f,
		listing(measuredAt.Add(-50*time.Minute), "developer run run-1 for calc-1", 2*time.Second),
		listing(measuredAt.Add(-40*time.Minute), "developer run run-2 for calc-2", 3*time.Second),
		listing(measuredAt.Add(-30*time.Minute), "", time.Second),
	)
	record := withMeasurer(t, f, &fakeMeasurer{answer: measuredStore(measuredAt)})
	earlier := measuredStore(measuredAt.Add(-8 * 24 * time.Hour))
	earlier.Stages[0].Size = 730 * megabyte
	if err := record.Record(runstate.TrackerStoreMeasurement{Measurement: earlier}); err != nil {
		t.Fatal(err)
	}

	step := stepNamed(t, f.pass1(t), StepTrackerMeasure)
	measurements, err := record.Read()
	if err != nil || len(measurements) != 2 {
		t.Fatalf("measurements = %d, %v", len(measurements), err)
	}
	latest, _ := measurements.Latest()
	if latest.Callers == nil || latest.Callers.Calls != 3 || len(latest.Callers.Callers) != 2 || latest.Callers.Callers[0].Caller != "developer runs" || latest.Callers.Callers[1].Caller != "yoyo watch" {
		t.Errorf("callers = %+v, want both developer runs together and the watch", latest.Callers)
	}
	if latest.Grew == nil || latest.Grew.From != 730*megabyte || latest.Grew.To != 930*megabyte {
		t.Errorf("grew = %+v, want 730 MB to 930 MB", latest.Grew)
	}
	if !strings.Contains(step.Detail, "the harness's own processes asked the tracker 3 times") {
		t.Errorf("measuring step = %q, want the calls by caller said", step.Detail)
	}
}
