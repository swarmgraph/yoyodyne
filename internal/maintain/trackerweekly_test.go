package maintain

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// listings sets how long a stage's listings of open and ready items took.
func listings(measured *beads.StoreMeasurement, stage string, took time.Duration) {
	for index := range measured.Stages {
		if measured.Stages[index].Name != stage {
			continue
		}
		for read := range measured.Stages[index].Reads {
			if name := measured.Stages[index].Reads[read].Name; name == beads.ReadOpenListing || name == beads.ReadReadyListing {
				measured.Stages[index].Reads[read].Took = took
			}
		}
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
