package maintain

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeMeasurer is the tracker's store as the measuring step sees it: a
// measurement that answers what it is told to, remembering that it was asked,
// into which directory, and under what bound.
type fakeMeasurer struct {
	mu       sync.Mutex
	answer   beads.StoreMeasurement
	err      error
	asked    int
	work     string
	deadline bool
	order    *[]string
}

func (m *fakeMeasurer) MeasureStore(ctx context.Context, work *repowrite.PinnedRoot) (beads.StoreMeasurement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked++
	m.work = work.Path()
	_, m.deadline = ctx.Deadline()
	if m.order != nil {
		*m.order = append(*m.order, "measure")
	}
	return m.answer, m.err
}

func (m *fakeMeasurer) times() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.asked
}

// measuredStore is a measurement of every stage, each export unchanged.
func measuredStore(at time.Time) beads.StoreMeasurement {
	stage := func(name string, size int64, took time.Duration) beads.StoreStage {
		return beads.StoreStage{Name: name, Size: size, Reads: []beads.StoreRead{
			{Name: beads.ReadOpenListing, Took: took, Items: 40},
			{Name: beads.ReadReadyListing, Took: took, Items: 12},
			{Name: beads.ReadOneItem, Took: time.Second, Items: 1},
		}, Export: beads.StoreExport{Records: 900, Compared: name != beads.StageAsItIs}}
	}
	return beads.StoreMeasurement{
		Store:     "/opt/calc/.beads",
		StartedAt: at,
		CopyTook:  4 * time.Second,
		Took:      9 * time.Minute,
		Stages: []beads.StoreStage{
			stage(beads.StageAsItIs, 930*megabyte, 33*time.Second),
			stage(beads.StageCleanedUp, 900*megabyte, 31*time.Second),
			stage(beads.StageOldSquashed, 200*megabyte, 6*time.Second),
			stage(beads.StageAllSquashed, 40*megabyte, 2*time.Second),
		},
	}
}

func withMeasurer(t *testing.T, f *fixture, measurer *fakeMeasurer) *runstate.TrackerStoreMeasurementStore {
	t.Helper()
	record, err := runstate.NewTrackerStoreMeasurementStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	f.pass.Measurer, f.pass.Measurements = measurer, record
	return record
}

// A store never measured is measured at the first pass, after the cleanup,
// under a bound, into the record's own directory; the measurement is recorded
// and its figures are said on the step.
func TestAStoreNeverMeasuredIsMeasuredAfterTheCleanupAndRecorded(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var order []string
	store := &fakeStore{size: 930 * megabyte, after: 930 * megabyte, items: 900, order: &order}
	withTrackerStore(t, f, store)
	measurer := &fakeMeasurer{answer: measuredStore(f.now.Add(time.Minute)), order: &order}
	record := withMeasurer(t, f, measurer)

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerMeasure)
	if step.Outcome != runstate.StepRan {
		t.Fatalf("measuring step = %+v, want a measurement", step)
	}
	for _, want := range []string{
		"30 MB leftover data a cleanup takes back, 700 MB history older than 30 days, 160 MB history of the last 30 days, 40 MB current data",
		"as it is: 930 MB, listing open items 33.0 seconds",
		"after squashing all history: 40 MB, listing open items 2.0 seconds",
		"recorded in " + record.Path(),
	} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("measuring step = %q, want it to say %q", step.Detail, want)
		}
	}
	if len(order) != 2 || order[0] != "cleanup" || order[1] != "measure" {
		t.Errorf("order = %v, want the cleanup and then the measurement", order)
	}
	if !measurer.deadline || !strings.HasSuffix(measurer.work, "tracker-store-measurement-copy") {
		t.Errorf("measured into %q, bounded %t, want the record's copy directory under a bound", measurer.work, measurer.deadline)
	}
	measurements, err := record.Read()
	if err != nil || len(measurements) != 1 || measurements[0].Breakdown().Current != 40*megabyte {
		t.Errorf("measurements = %+v, %v, want this one recorded", measurements, err)
	}
	if recorded.Problem != "" {
		t.Errorf("pass problem = %q, want none", recorded.Problem)
	}
}

// A measurement is taken a week after the last that finished, and a day after
// the last that failed; until then the step says when the next is due.
func TestAMeasurementIsDueAWeekAfterOneFinishedAndADayAfterOneFailed(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		ago    time.Duration
		failed bool
		due    bool
	}{
		"finished six days ago":   {ago: 6 * 24 * time.Hour},
		"finished eight days ago": {ago: 8 * 24 * time.Hour, due: true},
		"failed twenty hours ago": {ago: 20 * time.Hour, failed: true},
		"failed two days ago":     {ago: 48 * time.Hour, failed: true, due: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			measurer := &fakeMeasurer{answer: measuredStore(f.now)}
			record := withMeasurer(t, f, measurer)
			earlier := runstate.TrackerStoreMeasurement{Measurement: measuredStore(f.now.Add(-tc.ago))}
			if tc.failed {
				earlier.Failed = "context deadline exceeded"
			}
			if err := record.Record(earlier); err != nil {
				t.Fatal(err)
			}

			step := stepNamed(t, f.pass1(t), StepTrackerMeasure)
			if tc.due {
				if measurer.times() != 1 || step.Outcome != runstate.StepRan {
					t.Errorf("measuring step = %+v after %d measurements, want one taken", step, measurer.times())
				}
				return
			}
			if measurer.times() != 0 || step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "the tracker's store was last measured at") || !strings.Contains(step.Detail, "the next is due at") {
				t.Errorf("measuring step = %+v after %d measurements, want it skipped saying when the next is due", step, measurer.times())
			}
		})
	}
}

// A store another process has open, or one that changed while it was copied,
// is not measured: the step is skipped saying why, nothing is recorded, and the
// next pass tries again.
func TestAStoreThatCouldNotBeCopiedCleanlyIsTriedAgainAtTheNextPass(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		answer beads.StoreMeasurement
		want   string
	}{
		"held open": {answer: beads.StoreMeasurement{Busy: []beads.StoreHolder{{PID: 4242, Command: "bd ready --json"}}}, want: "it was not copied because another process had it open: process 4242 (bd ready --json)"},
		"changed":   {answer: beads.StoreMeasurement{Moved: "another process wrote to the store while it was copied: embeddeddolt/calc/data was written to"}, want: "its copy was discarded because another process wrote to the store while it was copied"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			record := withMeasurer(t, f, &fakeMeasurer{answer: tc.answer})

			step := stepNamed(t, f.pass1(t), StepTrackerMeasure)
			if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, tc.want) || !strings.Contains(step.Detail, "the next pass tries again") {
				t.Errorf("measuring step = %+v, want it skipped saying %q", step, tc.want)
			}
			if measurements, err := record.Read(); err != nil || len(measurements) != 0 {
				t.Errorf("measurements = %+v, %v, want none recorded", measurements, err)
			}
		})
	}
}

// A measurement whose export changed, or one of whose stages failed, or that
// did not finish, is recorded and fails the step, so that it is seen; one that
// could not even copy the store fails the step and is not recorded.
func TestAMeasurementThatFoundOrMetTroubleFailsTheStep(t *testing.T) {
	t.Parallel()
	changed := measuredStore(time.Time{})
	changed.Stages[2].Export = beads.StoreExport{Records: 899, Compared: true, Missing: []string{"calc-7"}, Differing: 1}
	failedStage := measuredStore(time.Time{})
	failedStage.Stages = failedStage.Stages[:3]
	failedStage.Stages[2] = beads.StoreStage{Name: beads.StageOldSquashed, Command: []string{"compact", "--days", "30", "--force", "--json"}, Failed: "bd compact failed: storage backend does not support compact"}
	for name, tc := range map[string]struct {
		answer   beads.StoreMeasurement
		err      error
		recorded bool
		want     string
	}{
		"an export changed":  {answer: changed, recorded: true, want: "the export's records CHANGED: 1 differ (missing calc-7)"},
		"a stage failed":     {answer: failedStage, recorded: true, want: "failed: bd compact failed: storage backend does not support compact"},
		"it did not finish":  {answer: measuredStore(time.Time{}), err: context.DeadlineExceeded, recorded: true, want: "it did not finish: context deadline exceeded"},
		"it was never begun": {answer: beads.StoreMeasurement{Store: "/opt/calc/.beads"}, err: errors.New("the tracker's store holds a symbolic link"), want: "it could not be copied: the tracker's store holds a symbolic link"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			answer := tc.answer
			if tc.recorded {
				answer.StartedAt = f.now.Add(time.Minute)
			}
			record := withMeasurer(t, f, &fakeMeasurer{answer: answer, err: tc.err})

			recorded := f.pass1(t)
			step := stepNamed(t, recorded, StepTrackerMeasure)
			if step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, tc.want) {
				t.Errorf("measuring step = %+v, want it failed saying %q", step, tc.want)
			}
			if !strings.Contains(recorded.Problem, StepTrackerMeasure+": ") {
				t.Errorf("pass problem = %q, want the measuring step named", recorded.Problem)
			}
			measurements, err := record.Read()
			if err != nil || (len(measurements) == 1) != tc.recorded {
				t.Errorf("measurements = %+v, %v, want recorded %t", measurements, err, tc.recorded)
			}
		})
	}
}

// A pass with nothing wired to measure says so and does nothing.
func TestAPassWithNoMeasurementWiredSaysSo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	step := stepNamed(t, f.pass1(t), StepTrackerMeasure)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "no measurement of the tracker's store is wired") {
		t.Errorf("measuring step = %+v, want it skipped saying nothing is wired", step)
	}
}
