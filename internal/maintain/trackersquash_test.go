package maintain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeSquasher is the live store as the squashing step sees it. Like the real
// squash it makes its copy in the directory it is given, hands it to be
// recorded, and runs nothing where that fails; busy and moved stop it before
// any of that. When it runs it notes whether the copy's record was already
// there to read.
type fakeSquasher struct {
	mu       sync.Mutex
	busy     []beads.StoreHolder
	unknown  string
	moved    string
	failed   error
	squashes *runstate.TrackerStoreSquashStore
	// ran is each stage squashed, and recordedFirst whether, when it ran, the
	// record carried the copy's place.
	ran           []string
	recordedFirst []bool
}

func (s *fakeSquasher) SquashStore(_ context.Context, stage string, copies *repowrite.PinnedRoot, name string, limit time.Duration, copied func(beads.StoreSquash) error) (beads.StoreSquash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	squash := beads.StoreSquash{Store: "/opt/calc/.beads", Stage: stage}
	switch {
	case len(s.busy) > 0:
		squash.Busy = s.busy
		return squash, nil
	case s.unknown != "":
		squash.Unknown = s.unknown
		return squash, nil
	case s.moved != "":
		squash.Moved = s.moved
		return squash, nil
	}
	if err := copies.MakeDirectory(name, 0o700); err != nil {
		return squash, err
	}
	squash.Copy, squash.CopiedAt, squash.CopyTook = filepath.Join(copies.Path(), name), time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC), 3*time.Second
	if err := copied(squash); err != nil {
		return beads.StoreSquash{Store: squash.Store, Stage: stage}, errors.Join(err, copies.RemoveAll(name))
	}
	first := false
	if s.squashes != nil {
		recorded, _ := s.squashes.Read()
		for _, record := range recorded {
			first = first || (record.Name == name && record.Squash.Copy == squash.Copy && !record.Ended)
		}
	}
	s.ran = append(s.ran, stage)
	s.recordedFirst = append(s.recordedFirst, first)
	command, _ := beads.SquashCommand(stage)
	squash.Command, squash.StartedAt, squash.Took = command, squash.CopiedAt.Add(time.Minute), 42*time.Second
	squash.SizeBefore, squash.SizeAfter = 930*megabyte, 200*megabyte
	if s.failed != nil {
		squash.Failed = s.failed.Error()
		return squash, s.failed
	}
	return squash, nil
}

func (s *fakeSquasher) stages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ran...)
}

// refusingSquashes is a squash record that cannot be written.
type refusingSquashes struct {
	*runstate.TrackerStoreSquashStore
}

func (refusingSquashes) Record(runstate.TrackerStoreSquash) error {
	return errors.New("the state directory is read-only")
}

// squashFixture is a pass with a measurement already recorded, nothing wired
// to measure again, and the squash wired to fake.
type squashFixture struct {
	*fixture
	squasher *fakeSquasher
	squashes *runstate.TrackerStoreSquashStore
	reports  *runstate.ReportStore
}

func newSquashFixture(t *testing.T, measured beads.StoreMeasurement) *squashFixture {
	t.Helper()
	f := newFixture(t)
	root := t.TempDir()
	measurements, err := runstate.NewTrackerStoreMeasurementStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	if err := measurements.Record(runstate.TrackerStoreMeasurement{Measurement: measured}); err != nil {
		t.Fatal(err)
	}
	squashes, err := runstate.NewTrackerStoreSquashStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	reports, err := runstate.NewReportStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	squasher := &fakeSquasher{squashes: squashes}
	f.pass.Measurements, f.pass.Squasher, f.pass.Squashes, f.pass.Reports = measurements, squasher, squashes, reports
	f.pass.RepositoryID = "calc"
	return &squashFixture{fixture: f, squasher: squasher, squashes: squashes, reports: reports}
}

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

// slowUntilSquashed is a measurement whose listings take 33 and 31 seconds as
// the store is and after the cleanup, 6 seconds after squashing old history,
// and 2 after squashing all of it, every export unchanged.
func slowUntilSquashed(at time.Time) beads.StoreMeasurement {
	measured := measuredStore(at)
	command := map[string][]string{
		beads.StageCleanedUp:   {"gc", "--skip-decay", "--force"},
		beads.StageOldSquashed: {"compact", "--days", "30", "--force", "--json"},
		beads.StageAllSquashed: {"flatten", "--force", "--json"},
	}
	for index := range measured.Stages {
		measured.Stages[index].Command = command[measured.Stages[index].Name]
		measured.Stages[index].Took = 4 * time.Minute
	}
	return measured
}

// The measurement that shows listings within the limit only after squashing
// history older than 30 days, every export unchanged, chooses that squash; the
// copy's place is recorded before bd runs, and the step says how long the
// store was held and where the copy is kept until.
func TestASquashOfOldHistoryRunsWhenItAloneBringsTheListingsWithinTheLimit(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerSquash)
	if step.Outcome != runstate.StepRan {
		t.Fatalf("squashing step = %+v, want a squash", step)
	}
	if got := f.squasher.stages(); len(got) != 1 || got[0] != beads.StageOldSquashed {
		t.Fatalf("squashed %v, want history older than 30 days alone", got)
	}
	if !f.squasher.recordedFirst[0] {
		t.Errorf("the copy's place was not recorded before bd ran")
	}
	for _, want := range []string{
		"squashed the tracker store's history older than 30 days",
		"within 10 seconds only after squashing history older than 30 days",
		"bd compact --days 30 --force --json held the store 42.0 seconds",
		"on the copy, bd compact --days 30 --force --json held the store 4m0s",
		"the copy is kept until 2026-10-13",
		"every other tracker call waited behind bd while it held the store",
		"recorded in " + f.squashes.Path(),
	} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("squashing step = %q, want it to say %q", step.Detail, want)
		}
	}
	squashes, err := f.squashes.Read()
	if err != nil || len(squashes) != 1 || !squashes[0].Ended || squashes[0].Squash.Copy == "" || squashes[0].Squash.Took != 42*time.Second {
		t.Errorf("squashes = %+v, %v, want this one recorded as ended with its copy", squashes, err)
	}
	if recorded.Problem != "" {
		t.Errorf("pass problem = %q, want none", recorded.Problem)
	}
}

// Each measurement chooses its branch of the design: no squash where the
// listings are within the limit as the store is or after the cleanup, all
// history only where squashing old history alone is not enough, and none where
// nothing brings them within it.
func TestTheMeasurementChoosesWhichSquashRuns(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		slow map[string]time.Duration
		want string
		say  string
	}{
		"within as it is":           {slow: map[string]time.Duration{beads.StageAsItIs: 8 * time.Second}, say: "within 10 seconds as it is"},
		"within after the cleanup":  {slow: map[string]time.Duration{beads.StageCleanedUp: 9 * time.Second}, say: "within 10 seconds after the cleanup"},
		"only after all history":    {slow: map[string]time.Duration{beads.StageOldSquashed: 12 * time.Second}, want: beads.StageAllSquashed, say: "squashed the tracker store's all history"},
		"not even after everything": {slow: map[string]time.Duration{beads.StageOldSquashed: 12 * time.Second, beads.StageAllSquashed: 11 * time.Second}, say: "no squash brings them within it"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			measured := slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC))
			for stage, took := range tc.slow {
				listings(&measured, stage, took)
			}
			f := newSquashFixture(t, measured)

			step := stepNamed(t, f.pass1(t), StepTrackerSquash)
			got := f.squasher.stages()
			if tc.want == "" {
				if len(got) != 0 || step.Outcome != runstate.StepSkipped {
					t.Errorf("squashed %v, step %+v, want nothing squashed", got, step)
				}
			} else if len(got) != 1 || got[0] != tc.want {
				t.Errorf("squashed %v, want %q", got, tc.want)
			}
			if !strings.Contains(step.Detail, tc.say) {
				t.Errorf("squashing step = %q, want it to say %q", step.Detail, tc.say)
			}
		})
	}
}

// The first condition: a measurement that cannot show every item and note
// unchanged across the squash — its export at that stage, or at a stage before
// it, failed or was never compared — squashes nothing and warns nobody.
func TestNoSquashRunsWhereTheMeasurementCannotShowEveryRecordUnchanged(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		stage  int
		export beads.StoreExport
	}{
		"its export failed":           {stage: 2, export: beads.StoreExport{Failed: "bd export: database is locked"}},
		"its export was not compared": {stage: 2, export: beads.StoreExport{Records: 900}},
		"an earlier export failed":    {stage: 1, export: beads.StoreExport{Failed: "bd export: database is locked"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			measured := slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC))
			measured.Stages[tc.stage].Export = tc.export
			f := newSquashFixture(t, measured)

			step := stepNamed(t, f.pass1(t), StepTrackerSquash)
			if got := f.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "cannot show every item and note unchanged") {
				t.Errorf("squashed %v, step %+v, want nothing squashed and the reason said", got, step)
			}
			if filed, err := f.reports.List(); err != nil || len(filed) != 0 {
				t.Errorf("reports = %+v, %v, want none", filed, err)
			}
		})
	}
}

// A measurement on which any stage changed an item or a note squashes nothing,
// and the development manager is warned once, the warning naming the stage
// and the item; later passes on the same measurement neither squash nor warn
// again.
func TestAChangedRecordStopsTheSquashAndWarnsTheDevelopmentManagerOnce(t *testing.T) {
	t.Parallel()
	measured := slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC))
	measured.Stages[3].Export = beads.StoreExport{Records: 900, Compared: true, Changed: []string{"calc-7"}, Differing: 1}
	f := newSquashFixture(t, measured)

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 {
		t.Fatalf("squashed %v, want nothing squashed", got)
	}
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "after squashing all history (bd flatten --force --json): 1 record(s) differ (changed calc-7)") {
		t.Errorf("squashing step = %+v, want it to name the stage and the item", step)
	}
	filed, err := f.reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("reports = %+v, %v, want one warning", filed, err)
	}
	warning := filed[0]
	if warning.Severity != report.SeverityWarning || warning.Addressee() != domain.RoleDevelopmentManager || warning.Role != report.HarnessReporter {
		t.Errorf("warning = %+v, want a warning from the harness for the development manager", warning)
	}
	for _, want := range []string{"after squashing all history (bd flatten --force --json)", "calc-7", "did not squash"} {
		if !strings.Contains(warning.Message, want) {
			t.Errorf("warning = %q, want it to name %q", warning.Message, want)
		}
	}
	if !strings.Contains(step.Detail, warning.ID) {
		t.Errorf("squashing step = %q, want it to name the warning %s", step.Detail, warning.ID)
	}

	f.now = f.now.Add(time.Hour)
	again := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || !strings.Contains(again.Detail, "already decided on the latest measurement") {
		t.Errorf("second pass squashed %v and said %q, want nothing done on a measurement already decided", got, again.Detail)
	}
	if filed, _ := f.reports.List(); len(filed) != 1 {
		t.Errorf("reports after a second pass = %d, want the one warning still", len(filed))
	}
}

// The second condition: where another process has the store open, or whether
// one does cannot be told, or the store changed while it was copied, nothing
// is squashed, nothing is recorded, and the next pass tries again.
func TestNoSquashRunsUnlessMaintenanceAloneHoldsTheStore(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		set  func(*fakeSquasher)
		want string
	}{
		"held open":      {set: func(s *fakeSquasher) { s.busy = []beads.StoreHolder{{PID: 4242, Command: "bd ready --json"}} }, want: "another process had it open: process 4242 (bd ready --json)"},
		"cannot be told": {set: func(s *fakeSquasher) { s.unknown = "lsof is not installed" }, want: "whether another process had it open could not be told: lsof is not installed"},
		"changed": {set: func(s *fakeSquasher) {
			s.moved = "another process wrote to the store while it was copied: embeddeddolt/calc/data was written to"
		}, want: "embeddeddolt/calc/data was written to"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
			tc.set(f.squasher)

			step := stepNamed(t, f.pass1(t), StepTrackerSquash)
			if got := f.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, tc.want) || !strings.Contains(step.Detail, "the next pass tries again") {
				t.Errorf("squashed %v, step %+v, want nothing squashed saying %q", got, step, tc.want)
			}
			if squashes, err := f.squashes.Read(); err != nil || len(squashes) != 0 {
				t.Errorf("squashes = %+v, %v, want none recorded, so the next pass tries again", squashes, err)
			}

			*f.squasher = fakeSquasher{squashes: f.squashes}
			f.now = f.now.Add(time.Hour)
			if step := stepNamed(t, f.pass1(t), StepTrackerSquash); step.Outcome != runstate.StepRan {
				t.Errorf("the next pass's squashing step = %+v, want the squash it was due", step)
			}
		})
	}
}

// The third condition: a copy whose place cannot be recorded is removed and
// the store is not squashed.
func TestNoSquashRunsWhereTheCopyCannotBeRecorded(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	f.pass.Squashes = refusingSquashes{f.squashes}

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "the state directory is read-only") {
		t.Errorf("squashed %v, step %+v, want nothing squashed and the step failed", got, step)
	}
	copies, err := f.squashes.CopyDirectory()
	if err != nil {
		t.Fatal(err)
	}
	defer copies.Close()
	if entries, _ := os.ReadDir(copies.Path()); len(entries) != 0 {
		t.Errorf("copies = %v, want the unrecorded copy removed", entries)
	}
}

// A squash bd failed is recorded with its copy and fails the step, and the
// next is decided on the next measurement.
func TestASquashThatFailedIsRecordedAndNotRepeatedOnTheSameMeasurement(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	f.squasher.failed = errors.New("bd compact: storage backend does not support compact")

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerSquash)
	if step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "storage backend does not support compact") || !strings.Contains(step.Detail, "is what the store is restored from") {
		t.Errorf("squashing step = %+v, want it failed naming the copy", step)
	}
	squashes, err := f.squashes.Read()
	if err != nil || len(squashes) != 1 || squashes[0].Failed == "" || squashes[0].Squash.Copy == "" {
		t.Errorf("squashes = %+v, %v, want the failure recorded with its copy", squashes, err)
	}
	f.now = f.now.Add(time.Hour)
	stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 1 {
		t.Errorf("squashed %v, want one attempt on the one measurement", got)
	}
}

// Copies made more than 14 days ago are removed at each pass, and newer ones
// and anything else in the directory are kept.
func TestCopiesOlderThanFourteenDaysAreRemoved(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	copies, err := f.squashes.CopyDirectory()
	if err != nil {
		t.Fatal(err)
	}
	defer copies.Close()
	old := f.squashes.NewName(f.now.Add(-15 * 24 * time.Hour))
	recent := f.squashes.NewName(f.now.Add(-13 * 24 * time.Hour))
	for _, name := range []string{old, recent, "kept-by-hand"} {
		if err := copies.MakeDirectory(filepath.Join(name, ".beads"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.squasher.busy = []beads.StoreHolder{{PID: 4242, Command: "bd ready --json"}}

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if !strings.Contains(step.Detail, "removed the copies of the store made more than 14 days ago: "+filepath.Join(copies.Path(), old)) {
		t.Errorf("squashing step = %q, want the old copy named as removed", step.Detail)
	}
	for name, want := range map[string]bool{old: false, recent: true, "kept-by-hand": true} {
		if _, err := os.Stat(filepath.Join(copies.Path(), name)); (err == nil) != want {
			t.Errorf("%s still there = %t, want %t", name, err == nil, want)
		}
	}
}

// A pass with nothing wired to squash says so and does nothing.
func TestAPassWithNoSquashWiredSaysSo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "no squash of the tracker's store is wired") {
		t.Errorf("squashing step = %+v, want it skipped saying nothing is wired", step)
	}
}
