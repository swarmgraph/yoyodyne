package maintain

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The weekly measurement holds the store to the design's limit: listings past
// it, or a store doubled since its last squash, start the 30 day squash; a
// squash that leaves the listings past it warns once; and two measurements in
// a row within it are recorded. Every tracker call here is a fake.

// pastTheLimit is a measurement whose listings are past the limit at every
// stage, every export unchanged.
func pastTheLimit(at time.Time, afterSquash time.Duration) beads.StoreMeasurement {
	measured := slowUntilSquashed(at)
	listings(&measured, beads.StageOldSquashed, afterSquash)
	listings(&measured, beads.StageAllSquashed, afterSquash)
	return measured
}

// recordSquashed records a squash of the live store that finished at, leaving
// it sizeAfter on disk.
func recordSquashed(t *testing.T, f *squashFixture, at time.Time, sizeAfter int64) {
	t.Helper()
	if err := f.squashes.Record(runstate.TrackerStoreSquash{
		Name: f.squashes.NewName(at), MeasuredAt: at.Add(-time.Hour), Ended: true,
		Squash: beads.StoreSquash{Stage: beads.StageOldSquashed, StartedAt: at, SizeBefore: 900 * megabyte, SizeAfter: sizeAfter},
	}); err != nil {
		t.Fatal(err)
	}
}

// Listings past the limit after the cleanup, which no squash on the copy brings
// within it, still start the 30 day squash, and never all history.
func TestListingsPastTheLimitStartTheThirtyDaySquash(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, pastTheLimit(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC), 14*time.Second))

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 1 || got[0] != beads.StageOldSquashed {
		t.Fatalf("squashed %v, want history older than 30 days", got)
	}
	for _, want := range []string{"listed open and ready items past 10 seconds after the cleanup", "squashed the tracker store's history older than 30 days"} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("squashing step = %q, want it to say %q", step.Detail, want)
		}
	}
}

// A store that has doubled on disk since its last squash is due the 30 day
// squash although its listings are within the limit; one that has grown less,
// or that was never squashed, is not.
func TestAStoreDoubledSinceItsLastSquashIsSquashed(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		sizeAfter int64
		want      bool
	}{
		"doubled":         {sizeAfter: 200 * megabyte, want: true},
		"exactly doubled": {sizeAfter: 465 * megabyte, want: true},
		"not doubled":     {sizeAfter: 500 * megabyte},
		"never squashed":  {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			measured := slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC))
			listings(&measured, beads.StageAsItIs, 8*time.Second)
			f := newSquashFixture(t, measured)
			if tc.sizeAfter > 0 {
				recordSquashed(t, f, time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC), tc.sizeAfter)
			}

			step := stepNamed(t, f.pass1(t), StepTrackerSquash)
			got := f.squasher.stages()
			if !tc.want {
				if len(got) != 0 || !strings.Contains(step.Detail, "so no squash is needed") {
					t.Errorf("squashed %v, step %q, want nothing squashed", got, step.Detail)
				}
				return
			}
			if len(got) != 1 || got[0] != beads.StageOldSquashed {
				t.Fatalf("squashed %v, want history older than 30 days", got)
			}
			if !strings.Contains(step.Detail, "the store has doubled in size since its last squash") || !strings.Contains(step.Detail, "found it 930 MB") {
				t.Errorf("squashing step = %q, want it to say the store doubled, with the figures", step.Detail)
			}
			if filed, _ := f.reports.List(); len(filed) != 0 {
				t.Errorf("reports = %+v, want none: the listings were within the limit", filed)
			}
		})
	}
}

// A squash that leaves the listings past the limit on the measured copy files
// one warning for the development manager with the figures. A later squash
// with the same figures files nothing more; one whose figures changed warns
// again.
func TestOneWarningIsFiledWhenStillPastTheLimitAfterTheSquash(t *testing.T) {
	t.Parallel()
	first := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	f := newSquashFixture(t, pastTheLimit(first, 14*time.Second))

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if step.Outcome != runstate.StepRan {
		t.Fatalf("squashing step = %+v, want a squash", step)
	}
	filed, err := f.reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("reports = %+v, %v, want one warning", filed, err)
	}
	warning := filed[0]
	if warning.Severity != report.SeverityWarning || warning.Addressee() != domain.RoleDevelopmentManager || warning.Role != report.HarnessReporter {
		t.Errorf("warning = %+v, want a warning from the harness for the development manager", warning)
	}
	for _, want := range []string{
		"(the squash step)",
		"still past their 10 second limit",
		"as the store was, listing open items 33.0 seconds",
		"after the same squash, listing open items 14.0 seconds, listing ready items 14.0 seconds",
		"from 930 MB to 200 MB",
		"not filed again until these figures change",
	} {
		if !strings.Contains(warning.Message, want) {
			t.Errorf("warning = %q, want it to say %q", warning.Message, want)
		}
	}
	if !strings.Contains(step.Detail, "STILL PAST their limit") || !strings.Contains(step.Detail, warning.ID) {
		t.Errorf("squashing step = %q, want it to say the listings are still past the limit and name %s", step.Detail, warning.ID)
	}
	squashes, err := f.squashes.Read()
	if err != nil || len(squashes) != 1 || squashes[0].StillOverReported != warning.ID || squashes[0].StillOver == "" {
		t.Fatalf("squashes = %+v, %v, want the warning recorded on the squash", squashes, err)
	}

	// The same pass again, on the same measurement, warns no more.
	f.reset()
	f.now = f.now.Add(time.Hour)
	stepNamed(t, f.pass1(t), StepTrackerSquash)
	if filed, _ := f.reports.List(); len(filed) != 1 {
		t.Errorf("reports after a second pass = %d, want the one warning still", len(filed))
	}

	// A week later the figures are the same in whole seconds: squashed, no warning.
	second := first.Add(7 * 24 * time.Hour)
	if err := f.pass.Measurements.Record(runstate.TrackerStoreMeasurement{Measurement: pastTheLimit(second, 14*time.Second+300*time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	f.reset()
	f.now = second.Add(2 * time.Hour)
	step = stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 1 {
		t.Fatalf("squashed %v on the second measurement, want the 30 day squash", got)
	}
	if filed, _ := f.reports.List(); len(filed) != 1 {
		t.Errorf("reports after a squash with the same figures = %d, want the one warning still", len(filed))
	}
	if !strings.Contains(step.Detail, "same figures the development manager was already warned of in "+warning.ID) {
		t.Errorf("squashing step = %q, want it to say the figures were already warned of", step.Detail)
	}

	// Another week, and the figures have changed: a second warning.
	third := second.Add(7 * 24 * time.Hour)
	if err := f.pass.Measurements.Record(runstate.TrackerStoreMeasurement{Measurement: pastTheLimit(third, 19*time.Second)}); err != nil {
		t.Fatal(err)
	}
	f.reset()
	f.now = third.Add(2 * time.Hour)
	stepNamed(t, f.pass1(t), StepTrackerSquash)
	filed, _ = f.reports.List()
	if len(filed) != 2 || !strings.Contains(filed[1].Message, "listing open items 19.0 seconds") {
		t.Errorf("reports after a squash with new figures = %+v, want a second warning carrying them", filed)
	}
}

// A squash whose listings came within the limit on the copy files no warning.
func TestNoWarningIsFiledWhenTheSquashBringsTheListingsWithinTheLimit(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if step.Outcome != runstate.StepRan || strings.Contains(step.Detail, "STILL PAST") {
		t.Errorf("squashing step = %+v, want a squash saying nothing is still past the limit", step)
	}
	if filed, _ := f.reports.List(); len(filed) != 0 {
		t.Errorf("reports = %+v, want none", filed)
	}
}

// failingReports refuses the first append and takes every one after it.
type failingReports struct {
	*runstate.ReportStore
	mu      sync.Mutex
	refused bool
}

func (r *failingReports) Append(filed report.Report) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.refused {
		r.refused = true
		return errors.New("the report pile is read-only")
	}
	return r.ReportStore.Append(filed)
}

// A warning that could not be filed fails the step and is filed by the next
// pass, without squashing again.
func TestAStillPastWarningThatCouldNotBeFiledIsFiledByTheNextPass(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, pastTheLimit(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC), 14*time.Second))
	f.pass.Reports = &failingReports{ReportStore: f.reports}

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "the report pile is read-only") {
		t.Fatalf("squashing step = %+v, want it failed on the warning", step)
	}
	if squashes, _ := f.squashes.Read(); len(squashes) != 1 || !squashes[0].StillOverUnwarned() {
		t.Fatalf("squashes = %+v, want the squash recorded as not yet warned", squashes)
	}

	f.reset()
	f.now = f.now.Add(time.Hour)
	step = stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 {
		t.Errorf("squashed %v on the next pass, want no second squash", got)
	}
	filed, _ := f.reports.List()
	if len(filed) != 1 || !strings.Contains(step.Detail, filed[0].ID) {
		t.Errorf("reports = %+v, step %q, want the warning filed and named", filed, step.Detail)
	}
	if squashes, _ := f.squashes.Read(); len(squashes) != 1 || squashes[0].StillOverReported == "" {
		t.Errorf("squashes = %+v, want the warning recorded", squashes)
	}
}

// Each measurement records whether its listings as the store was were within
// the limit, and for how many measurements in a row; the second in a row says
// the tracker read wait may be revisited. One past the limit starts the count
// again, and one that timed nothing is passed over.
func TestTwoMeasurementsInARowWithinTheLimitAreRecorded(t *testing.T) {
	t.Parallel()
	within := func(at time.Time) beads.StoreMeasurement {
		measured := measuredStore(at)
		listings(&measured, beads.StageAsItIs, 7*time.Second)
		return measured
	}
	untimed := func(at time.Time) beads.StoreMeasurement {
		measured := measuredStore(at)
		measured.Stages[0].Failed = "bd list: timed out"
		return measured
	}
	for name, tc := range map[string]struct {
		earlier []func(time.Time) beads.StoreMeasurement
		now     func(time.Time) beads.StoreMeasurement
		within  bool
		inARow  int
		say     string
	}{
		"the first within":            {now: within, within: true, inARow: 1, say: "within their 10 second limit, for 1 measurement(s) in a row"},
		"the second within":           {earlier: []func(time.Time) beads.StoreMeasurement{within}, now: within, within: true, inARow: 2, say: "the limit has held for two measurements in a row and the 120 second wait on every tracker read may be revisited"},
		"within after one past":       {earlier: []func(time.Time) beads.StoreMeasurement{within, measuredStore}, now: within, within: true, inARow: 1, say: "for 1 measurement(s) in a row"},
		"within past an untimed one":  {earlier: []func(time.Time) beads.StoreMeasurement{within, untimed}, now: within, within: true, inARow: 2, say: "may be revisited"},
		"past the limit after within": {earlier: []func(time.Time) beads.StoreMeasurement{within}, now: measuredStore, say: "were PAST their 10 second limit"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			measurer := &fakeMeasurer{answer: tc.now(f.now)}
			record := withMeasurer(t, f, measurer)
			for index, earlier := range tc.earlier {
				at := f.now.Add(-time.Duration(len(tc.earlier)-index) * 7 * 24 * time.Hour)
				if err := record.Record(runstate.TrackerStoreMeasurement{Measurement: earlier(at)}); err != nil {
					t.Fatal(err)
				}
			}

			step := stepNamed(t, f.pass1(t), StepTrackerMeasure)
			if !strings.Contains(step.Detail, tc.say) {
				t.Errorf("measuring step = %q, want it to say %q", step.Detail, tc.say)
			}
			measurements, err := record.Read()
			if err != nil {
				t.Fatal(err)
			}
			latest, _ := measurements.Latest()
			limit := latest.Listings
			if limit == nil || limit.Within != tc.within || limit.InARow != tc.inARow || limit.Limit != 10*time.Second || limit.HeldTwiceInARow() != (tc.inARow >= 2) {
				t.Errorf("recorded listings = %+v, want within %t, %d in a row", limit, tc.within, tc.inARow)
			}
		})
	}
}
