package maintain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
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
	// sizeFailed is a size on disk that cannot be read; holds, where set, is
	// asked whether new runs were held when the squash ran, and heldDuring
	// says each answer; events is shared with the hold, so the order of what
	// the step did can be read.
	sizeFailed error
	holds      *recordingHolds
	heldDuring []bool
	events     *[]string
	now        func() time.Time
}

func (s *fakeSquasher) StoreSize() (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("size")
	if s.sizeFailed != nil {
		return "", 0, s.sizeFailed
	}
	return "/opt/calc/.beads/embeddeddolt", 1033 * megabyte, nil
}

func (s *fakeSquasher) note(event string) {
	if s.events != nil {
		*s.events = append(*s.events, event)
	}
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
	s.note("squash")
	if s.holds != nil {
		_, held, _ := s.holds.Standing(s.now())
		s.heldDuring = append(s.heldDuring, held)
	}
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

// recordingHolds is the hold on new runs, kept in a real store, noting each
// place and lift in the order they came, and refusing either where told to.
type recordingHolds struct {
	*runstate.TrackerSquashHoldStore
	events      *[]string
	placed      []runstate.TrackerSquashHold
	placeFailed error
	liftFailed  error
}

func (h *recordingHolds) Place(hold runstate.TrackerSquashHold) error {
	*h.events = append(*h.events, "hold")
	if h.placeFailed != nil {
		return h.placeFailed
	}
	h.placed = append(h.placed, hold)
	return h.TrackerSquashHoldStore.Place(hold)
}

func (h *recordingHolds) Lift() error {
	*h.events = append(*h.events, "lift")
	if h.liftFailed != nil {
		return h.liftFailed
	}
	return h.TrackerSquashHoldStore.Lift()
}

// fakeRuns is the product's runs as the squash counts them: at each look,
// the next of inFlight developer runs, the last repeating, beside one run
// stopped for a redeploy that is never counted.
type fakeRuns struct {
	mu       sync.Mutex
	inFlight []int
	looks    int
	failed   error
}

func (r *fakeRuns) Incomplete() ([]runstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed != nil {
		return nil, r.failed
	}
	count := 0
	if len(r.inFlight) > 0 {
		count = r.inFlight[min(r.looks, len(r.inFlight)-1)]
	}
	r.looks++
	states := []runstate.State{{RunID: "run-stopped", WorkItemID: "calc-9", RedeployStop: &runstate.RedeployStop{}}}
	for index := range count {
		states = append(states, runstate.State{RunID: fmt.Sprintf("run-%d", index), WorkItemID: fmt.Sprintf("calc-%d", index)})
	}
	return states, nil
}

func (r *fakeRuns) HoldsDeveloperSlotNow(runstate.State, time.Time) bool { return true }

// squashFixture is a pass with a measurement already recorded, nothing wired
// to measure again, the squash wired to fake, and every wait the squash makes
// moving the fixture's clock on rather than sleeping.
type squashFixture struct {
	*fixture
	squasher *fakeSquasher
	squashes *runstate.TrackerStoreSquashStore
	reports  *runstate.ReportStore
	holds    *recordingHolds
	runs     *fakeRuns
	events   *[]string
	// waits is every wait the squash made, and stop a wait the pass is stopped
	// in.
	waits []time.Duration
	stop  bool
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
	holdStore, err := runstate.NewTrackerSquashHoldStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	events := &[]string{}
	holds := &recordingHolds{TrackerSquashHoldStore: holdStore, events: events}
	now := func() time.Time { return f.now }
	squasher := &fakeSquasher{squashes: squashes, holds: holds, events: events, now: now}
	runs := &fakeRuns{}
	f.pass.Measurements, f.pass.Squasher, f.pass.Squashes, f.pass.Reports = measurements, squasher, squashes, reports
	f.pass.SquashHolds, f.pass.Runs = holds, runs
	f.pass.RepositoryID = "calc"
	sf := &squashFixture{fixture: f, squasher: squasher, squashes: squashes, reports: reports, holds: holds, runs: runs, events: events}
	f.pass.Sleep = func(_ context.Context, d time.Duration) bool {
		sf.waits = append(sf.waits, d)
		if sf.stop {
			return false
		}
		f.now = f.now.Add(d)
		return true
	}
	return sf
}

// reset is the squash as it was before a pass, for a test that takes another.
func (f *squashFixture) reset() {
	*f.squasher = fakeSquasher{squashes: f.squashes, holds: f.holds, events: f.events, now: f.squasher.now}
}

// held reports whether new runs are held now, as a watching session reads it.
func (f *squashFixture) held(t *testing.T) bool {
	t.Helper()
	_, standing, err := f.holds.Standing(f.now)
	if err != nil {
		t.Fatal(err)
	}
	return standing
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
// history only where squashing old history alone is not enough, and history
// older than 30 days where nothing brings them within it, because they are
// past the limit.
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
		"not even after everything": {slow: map[string]time.Duration{beads.StageOldSquashed: 12 * time.Second, beads.StageAllSquashed: 11 * time.Second}, want: beads.StageOldSquashed, say: "no squash that can be run brought them within it"},
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

			f.reset()
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

// Once a squash is due, new runs are held before anything else is done to the
// store, and the squash starts only once no developer run is in flight: the
// runs already going are counted after the hold has stood long enough for a
// pull that missed it to have started its run, and again until none is left.
// The hold stands while bd runs, ends on its own at the drain bound and the
// squash's measured time after it was placed, and is lifted once the squash
// has ended; the record says how long the store was held and how long the
// squash waited for the runs.
func TestASquashHoldsNewRunsAndStartsOnlyOnceNoneIsInFlight(t *testing.T) {
	t.Parallel()
	measured := slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC))
	f := newSquashFixture(t, measured)
	f.runs.inFlight = []int{2, 1, 0}
	start := f.now

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if step.Outcome != runstate.StepRan {
		t.Fatalf("squashing step = %+v, want a squash", step)
	}
	if got := strings.Join(*f.events, ", "); got != "size, hold, squash, lift" {
		t.Errorf("the step did %s, want the size read, then new runs held, then the squash, then the hold lifted", got)
	}
	if len(f.squasher.heldDuring) != 1 || !f.squasher.heldDuring[0] {
		t.Errorf("new runs held while bd ran = %v, want held", f.squasher.heldDuring)
	}
	if f.held(t) {
		t.Errorf("new runs are still held after the squash ended")
	}
	if want := []time.Duration{squashSettle, squashRunsPoll, squashRunsPoll}; fmt.Sprint(f.waits) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v: the settle, then a look every %s until none was in flight", f.waits, want, squashRunsPoll)
	}
	if len(f.holds.placed) != 1 {
		t.Fatalf("holds placed = %+v, want one", f.holds.placed)
	}
	stage, _ := measured.Stage(beads.StageOldSquashed)
	if hold := f.holds.placed[0]; !hold.Since.Equal(start) || !hold.Until.Equal(start.Add(config.DefaultRedeployDrainLimit()+measured.CopyTook+stage.Took)) {
		t.Errorf("hold = %+v, want it from %s, ending on its own at the drain bound and the measured copy and squash after it", hold, start)
	}
	squashes, err := f.squashes.Read()
	if err != nil || len(squashes) != 1 {
		t.Fatalf("squashes = %+v, %v, want one", squashes, err)
	}
	record := squashes[0]
	if record.Squash.Took != 42*time.Second || !record.HeldRunsAt.Equal(start) || record.WaitedForRuns != squashSettle+2*squashRunsPoll || record.SizeOnDisk != 1033*megabyte {
		t.Errorf("record = %+v, want how long the store was held, when new runs were held, the wait, and the size on disk", record)
	}
	for _, want := range []string{"held the store 42.0 seconds", "the store was 1033 MB on disk", "the squash waited 2m30s for the runs already going to finish"} {
		if !strings.Contains(record.Says(), want) || !strings.Contains(step.Detail, want) {
			t.Errorf("record says %q and step %q, want both to say %q", record.Says(), step.Detail, want)
		}
	}
}

// Runs still in flight once new runs have been held for the redeploy drain
// bound mean no squash: the hold is lifted so new runs start again, the
// development manager is warned once, naming the step and how many runs were
// still going, and a later pass on the same measurement neither squashes nor
// warns again.
func TestRunsStillInFlightAtTheDrainBoundSkipTheSquashWithOneWarning(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	f.pass.DrainBound = 5 * time.Minute
	f.runs.inFlight = []int{3}
	start := f.now

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 {
		t.Fatalf("squashed %v, want nothing squashed while runs are in flight", got)
	}
	if f.now.Sub(start) != 5*time.Minute {
		t.Errorf("waited %s for the runs, want the 5m0s drain bound", f.now.Sub(start))
	}
	if f.held(t) || (*f.events)[len(*f.events)-1] != "lift" {
		t.Errorf("events = %v, want new runs started again", *f.events)
	}
	filed, err := f.reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("reports = %+v, %v, want one warning", filed, err)
	}
	warning := filed[0]
	if warning.Severity != report.SeverityWarning || warning.Addressee() != domain.RoleDevelopmentManager {
		t.Errorf("warning = %+v, want a warning for the development manager", warning)
	}
	for _, want := range []string{"(the squash step)", "3 developer run(s) were still in flight", "5m0s"} {
		if !strings.Contains(warning.Message, want) {
			t.Errorf("warning = %q, want it to say %q", warning.Message, want)
		}
	}
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "3 developer run(s) were still in flight") || !strings.Contains(step.Detail, warning.ID) {
		t.Errorf("squashing step = %+v, want it skipped naming the runs and the warning", step)
	}

	f.reset()
	f.now = f.now.Add(time.Hour)
	again := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || !strings.Contains(again.Detail, "already decided on the latest measurement") {
		t.Errorf("second pass squashed %v and said %q, want nothing done on a measurement already decided", got, again.Detail)
	}
	if filed, _ := f.reports.List(); len(filed) != 1 {
		t.Errorf("reports after a second pass = %d, want the one warning still", len(filed))
	}
}

// The hold on new runs is lifted on every way the step ends once it was
// placed: runs that cannot be counted, the pass stopped while it waits, bd
// failing, another process holding the store, the copy not recorded, and the
// squash running out of runs in flight.
func TestTheHoldOnNewRunsIsLiftedHoweverTheStepEnds(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		set     func(*squashFixture)
		outcome runstate.SweepStepOutcome
		say     string
	}{
		"the runs cannot be counted": {set: func(f *squashFixture) { f.runs.failed = errors.New("the run index is unreadable") }, outcome: runstate.StepFailed, say: "the run index is unreadable"},
		"the pass is stopped":        {set: func(f *squashFixture) { f.stop = true }, outcome: runstate.StepFailed, say: "the pass was stopped while it waited"},
		"bd fails":                   {set: func(f *squashFixture) { f.squasher.failed = errors.New("bd compact: out of disk") }, outcome: runstate.StepFailed, say: "out of disk"},
		"the store is held open": {set: func(f *squashFixture) {
			f.squasher.busy = []beads.StoreHolder{{PID: 4242, Command: "bd ready --json"}}
		}, outcome: runstate.StepSkipped, say: "another process had it open"},
		"the copy is not recorded": {set: func(f *squashFixture) { f.pass.Squashes = refusingSquashes{f.squashes} }, outcome: runstate.StepFailed, say: "the state directory is read-only"},
		"it squashed":              {set: func(*squashFixture) {}, outcome: runstate.StepRan, say: "squashed the tracker store's"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
			tc.set(f)

			step := stepNamed(t, f.pass1(t), StepTrackerSquash)
			if step.Outcome != tc.outcome || !strings.Contains(step.Detail, tc.say) {
				t.Errorf("squashing step = %+v, want %s saying %q", step, tc.outcome, tc.say)
			}
			events := *f.events
			if len(events) < 2 || events[1] != "hold" || events[len(events)-1] != "lift" || f.held(t) {
				t.Errorf("events = %v, held now %t, want new runs held and then the hold lifted", events, f.held(t))
			}
		})
	}
}

// A hold that cannot be lifted fails the step and says when it ends on its
// own, so nobody has to lift it by hand; one that cannot be placed stops the
// squash before it starts.
func TestAHoldThatCannotBePlacedOrLiftedIsSaid(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	f.holds.liftFailed = errors.New("the state directory is read-only")
	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "could not be lifted, so no run starts until it ends on its own at") {
		t.Errorf("squashing step = %+v, want it failed saying when the hold ends on its own", step)
	}

	g := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	g.holds.placeFailed = errors.New("the state directory is read-only")
	step = stepNamed(t, g.pass1(t), StepTrackerSquash)
	if got := g.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "new runs could not be held, so it was not squashed") {
		t.Errorf("squashed %v, step %+v, want nothing squashed when new runs cannot be held", got, step)
	}
	if squashes, _ := g.squashes.Read(); len(squashes) != 0 {
		t.Errorf("squashes = %+v, want none recorded, so the next pass tries again", squashes)
	}
}

// A squash that held the store longer on the measurement's copy than the
// redeploy drain bound is not run: nothing holds new runs, and the
// development manager is warned once, naming the step and the measured time.
func TestASquashLongerOnTheCopyThanTheDrainBoundIsNotRun(t *testing.T) {
	t.Parallel()
	measured := slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC))
	for index := range measured.Stages {
		if measured.Stages[index].Name == beads.StageOldSquashed {
			measured.Stages[index].Took = 16 * time.Minute
		}
	}
	f := newSquashFixture(t, measured)

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || len(*f.events) != 0 {
		t.Fatalf("squashed %v and did %v, want nothing squashed and new runs never held", got, *f.events)
	}
	filed, err := f.reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("reports = %+v, %v, want one warning", filed, err)
	}
	for _, want := range []string{"(the squash step)", "held the store 16m0s", "15m0s"} {
		if !strings.Contains(filed[0].Message, want) {
			t.Errorf("warning = %q, want it to say %q", filed[0].Message, want)
		}
	}
	if filed[0].Addressee() != domain.RoleDevelopmentManager {
		t.Errorf("warning is for %s, want the development manager", filed[0].Addressee())
	}
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "longer than the 15m0s") {
		t.Errorf("squashing step = %+v, want it skipped naming the measured time and the bound", step)
	}
	f.now = f.now.Add(time.Hour)
	stepNamed(t, f.pass1(t), StepTrackerSquash)
	if filed, _ := f.reports.List(); len(filed) != 1 {
		t.Errorf("reports after a second pass = %d, want the one warning still", len(filed))
	}
}

// timingOutTracker is the live store as the cleanup step meets it at 1 GB:
// its size answers, and anything that lists or counts work items runs out of
// time.
type timingOutTracker struct {
	mu     sync.Mutex
	listed int
}

func (s *timingOutTracker) StoreSize() (string, int64, error) {
	return "/opt/calc/.beads/embeddeddolt", 1033 * megabyte, nil
}

func (s *timingOutTracker) CleanUpStore(context.Context, time.Duration) (beads.StoreCleanup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listed++
	return beads.StoreCleanup{Store: "/opt/calc/.beads/embeddeddolt", SizeBefore: 1033 * megabyte}, errors.New("count the work items: bd list --all: timed out after 30s")
}

func (s *timingOutTracker) CleanUpStoreBeside(ctx context.Context, hold time.Duration) (beads.StoreCleanup, error) {
	return s.CleanUpStore(ctx, hold)
}

func (s *timingOutTracker) MissingItems(context.Context, []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listed++
	return nil, errors.New("bd show: timed out after 30s")
}

// Deciding the squash lists and counts no work items, so a store too large to
// list in time is still squashed: the cleanup before it fails on its count,
// and the squash goes ahead on the recorded measurement and the store's size
// on disk, read before new runs are held.
func TestASquashIsDecidedWhereListingWorkItemsWouldTimeOut(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	cleanups, err := runstate.NewTrackerStoreCleanupStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	tracker := &timingOutTracker{}
	f.pass.Tracker, f.pass.Cleanups = tracker, cleanups

	recorded := f.pass1(t)
	if cleaned := stepNamed(t, recorded, StepTrackerCleanup); cleaned.Outcome != runstate.StepFailed || !strings.Contains(cleaned.Detail, "timed out") {
		t.Fatalf("cleanup step = %+v, want it failed on the count timing out", cleaned)
	}
	listedByCleanup := tracker.listed
	step := stepNamed(t, recorded, StepTrackerSquash)
	if step.Outcome != runstate.StepRan {
		t.Fatalf("squashing step = %+v, want the squash run although the store cannot be listed in time", step)
	}
	if tracker.listed != listedByCleanup {
		t.Errorf("the squash listed the tracker %d time(s), want none", tracker.listed-listedByCleanup)
	}
	if events := *f.events; len(events) < 2 || events[0] != "size" || events[1] != "hold" {
		t.Errorf("events = %v, want the size on disk read before new runs were held", events)
	}
}

// Without a hold on new runs or a count of the runs in flight, nothing is
// squashed, because a squash started under a run makes its reads wait.
func TestNoSquashRunsWithoutAHoldOnNewRuns(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)))
	f.pass.SquashHolds = nil
	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "nothing is wired to hold new runs") {
		t.Errorf("squashed %v, step %+v, want nothing squashed and the reason said", got, step)
	}
}
