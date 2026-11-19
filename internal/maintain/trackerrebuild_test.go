package maintain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeRebuilder answers a rehearsal and a live rebuild as scripted, and keeps
// the order they were asked in.
type fakeRebuilder struct {
	rehearsal, live beads.StoreRebuild
	rehearsalErr    error
	liveErr         error
	rebuilds        *runstate.TrackerStoreRebuildStore
	events          []string
}

func (r *fakeRebuilder) StoreSize() (string, int64, error) { return "", 0, errors.New("not used") }

func (r *fakeRebuilder) RehearseRebuild(context.Context, *repowrite.PinnedRoot) (beads.StoreRebuild, error) {
	r.events = append(r.events, "rehearse")
	return r.rehearsal, r.rehearsalErr
}

func (r *fakeRebuilder) RebuildStore(_ context.Context, _, kept *repowrite.PinnedRoot, name string, recorded func(beads.StoreRebuild) error) (beads.StoreRebuild, error) {
	r.events = append(r.events, "rebuild")
	live := r.live
	if len(live.Busy) == 0 {
		live.Kept = filepath.Join(kept.Path(), name)
		live.KeptAt = time.Now()
		if err := recorded(live); err != nil {
			return live, err
		}
		if r.live.Swapped {
			return live, r.liveErr
		}
	}
	return r.live, r.liveErr
}

type rebuildFixture struct {
	*fixture
	rebuilder *fakeRebuilder
	rebuilds  *runstate.TrackerStoreRebuildStore
	reports   *runstate.ReportStore
}

// newRebuildFixture is a pass whose live store is size bytes on disk, with a
// rehearsal that passes in 20 seconds and a live rebuild that takes it to 14 MB.
func newRebuildFixture(t *testing.T, size int64) *rebuildFixture {
	t.Helper()
	f := newFixture(t)
	root := t.TempDir()
	rebuilds, err := runstate.NewTrackerStoreRebuildStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	reports, err := runstate.NewReportStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	rebuilder := &fakeRebuilder{
		rehearsal: beads.StoreRebuild{Records: 1881, Took: 20 * time.Second, SizeBefore: size, SizeAfter: 14 << 20},
		live:      beads.StoreRebuild{Records: 1881, Took: 20 * time.Second, HeldFor: 22 * time.Second, SizeBefore: size, SizeAfter: 14 << 20, Swapped: true},
		rebuilds:  rebuilds,
	}
	f.pass.Tracker = &fakeStore{size: size, after: size, items: 1900}
	f.pass.TrackerLimits = config.DefaultTrackerLimits()
	f.pass.Rebuilder, f.pass.Rebuilds, f.pass.Reports, f.pass.RepositoryID = rebuilder, rebuilds, reports, "calc"
	return &rebuildFixture{fixture: f, rebuilder: rebuilder, rebuilds: rebuilds, reports: reports}
}

func (f *rebuildFixture) filed(t *testing.T) int {
	t.Helper()
	filed, err := f.reports.List()
	if err != nil {
		t.Fatal(err)
	}
	// The limits step warns about the store's size beside this one, so only the
	// rebuild's own warnings are counted.
	count := 0
	for _, each := range filed {
		if strings.HasPrefix(each.RunID, "tracker-store-rebuild:") {
			count++
		}
	}
	return count
}

// A store past its size limit is rebuilt on a copy and then, the copy having
// passed, live: the old store's place is recorded before it is moved, and the
// step says how long every other call waited and where the old store is kept.
func TestAStorePastItsSizeLimitIsRebuiltFromItsExport(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepRan {
		t.Fatalf("rebuild step = %+v, want one that ran", step)
	}
	if got := strings.Join(f.rebuilder.events, ","); got != "rehearse,rebuild" {
		t.Errorf("the rebuild asked %s, want a rehearsal and then the live rebuild", got)
	}
	for _, want := range []string{"from 1500 MB to 14 MB", "waited behind the rebuild for 22", "the old store is kept until"} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("rebuild step = %q, want %q in it", step.Detail, want)
		}
	}
	read, err := f.rebuilds.Read()
	if err != nil || len(read) != 1 || !read[0].Ended || !read[0].Replaced() || !strings.Contains(read[0].Reason, "past its limit of 1024 MB") {
		t.Fatalf("recorded %+v, %v, want one ended rebuild with its reason", read, err)
	}
	if f.filed(t) != 0 {
		t.Errorf("filed a warning for a rebuild that brought the store under its limit")
	}
}

// A store within its limits is not copied or rebuilt.
func TestAStoreWithinItsLimitsIsNotRebuilt(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 500*megabyte)

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "within their limit") {
		t.Fatalf("rebuild step = %+v, want it skipped as within its limits", step)
	}
	if len(f.rebuilder.events) != 0 {
		t.Errorf("the rebuild asked %v for a store within its limits", f.rebuilder.events)
	}
}

// A rehearsal that finds a record different leaves the live store alone and
// warns the development manager once; the next pass does not copy the store
// again until a day has passed.
func TestARehearsalThatFindsARecordDifferentStopsTheRebuildAndWarnsOnce(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.rehearsal.Step, f.rebuilder.rehearsal.Differing, f.rebuilder.rehearsal.Changed = beads.RebuildCompare, 1, []string{"calc-abc"}

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "calc-abc") {
		t.Fatalf("rebuild step = %+v, want one stopped by the record that differs", step)
	}
	if got := strings.Join(f.rebuilder.events, ","); got != "rehearse" {
		t.Errorf("the rebuild asked %s, want only the rehearsal", got)
	}
	if f.filed(t) != 1 {
		t.Fatalf("filed %d warnings, want one", f.filed(t))
	}
	f.now = f.now.Add(2 * time.Hour)
	again := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if len(f.rebuilder.events) != 1 || f.filed(t) != 1 || !strings.Contains(again.Detail, "the next is tried after") {
		t.Errorf("second pass = %+v after %v, %d warnings, want no second rehearsal or warning", again, f.rebuilder.events, f.filed(t))
	}
	f.now = f.now.Add(25 * time.Hour)
	stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if len(f.rebuilder.events) != 2 {
		t.Errorf("after a day the rebuild asked %v, want the rehearsal tried again", f.rebuilder.events)
	}
}

// A rehearsal longer than the bound is not followed by a live rebuild.
func TestARehearsalLongerThanTheBoundIsNotFollowedByALiveRebuild(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.rehearsal.Took = 90 * time.Second

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "longer than the") {
		t.Fatalf("rebuild step = %+v, want it skipped for its time", step)
	}
	if got := strings.Join(f.rebuilder.events, ","); got != "rehearse" || f.filed(t) != 1 {
		t.Errorf("events %s, %d warnings, want a rehearsal and one warning", got, f.filed(t))
	}
}

// A live rebuild that finds a record different leaves the old store in place
// and warns.
func TestALiveRebuildThatFindsARecordDifferentLeavesTheOldStoreAndWarns(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.live = beads.StoreRebuild{Records: 1881, Step: beads.RebuildCompare, Differing: 2, Missing: []string{"calc-gone"}}

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "calc-gone") || f.filed(t) != 1 {
		t.Fatalf("rebuild step = %+v, %d warnings, want it refused naming calc-gone and one warning", step, f.filed(t))
	}
}

// A live rebuild that could not put its new store in place and could not put
// the old one back is a failure, not a skip.
func TestAStoreLeftMissingIsAFailure(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.live = beads.StoreRebuild{Records: 1881, Step: beads.RebuildMove, Failed: "no way back", Kept: "/state/kept/x"}
	f.rebuilder.liveErr = errors.New("the tracker's store is missing")

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "/state/kept/x") {
		t.Fatalf("rebuild step = %+v, want a failure naming where the old store is", step)
	}
}

// A process outside the harness that has the store open once the rebuild holds
// the store lock stops it: the step names the process, the pass is recorded
// with that reason, and the next pass tries again rather than waiting a day.
func TestARebuildStoppedByAnOutsideProcessIsRecordedAndTriedAgain(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.live = beads.StoreRebuild{Busy: []beads.StoreHolder{{PID: 9, Command: "bd list"}}}

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "outside the harness") || !strings.Contains(step.Detail, "process 9 (bd list)") {
		t.Fatalf("rebuild step = %+v, want it skipped naming the outside process", step)
	}
	read, err := f.rebuilds.Read()
	if err != nil || len(read) != 1 || !strings.Contains(read[0].HeldBack, "process 9") || read[0].Refused() || read[0].Replaced() {
		t.Fatalf("recorded %+v, %v, want one pass held back naming the process", read, err)
	}
	if f.filed(t) != 0 {
		t.Errorf("filed %d warnings after one pass, want none", f.filed(t))
	}
	f.now = f.now.Add(time.Hour)
	stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if got := strings.Join(f.rebuilder.events, ","); got != "rehearse,rebuild,rehearse,rebuild" {
		t.Errorf("the rebuild asked %s, want it tried again at the next pass", got)
	}
}

// The rehearsal works on a copy, so the live store being open elsewhere does
// not stop it; only the store changing while it was copied does, and that is
// recorded.
func TestARehearsalOnACopyThatSawTheStoreWrittenIsRecorded(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.rehearsal = beads.StoreRebuild{Moved: "another process wrote to the store while it was copied: embeddeddolt/x was written to"}

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "while it was copied") {
		t.Fatalf("rebuild step = %+v, want it skipped for the write during the copy", step)
	}
	if read, _ := f.rebuilds.Read(); len(read) != 1 || read[0].HeldBackBy != heldByStoreWritten {
		t.Errorf("recorded %+v, want the pass held back for the write during the copy", read)
	}
}

// Three passes in a row that did not rebuild the store file exactly one
// warning naming what stopped them; more passes for the same reason file none,
// and a different reason files one more.
func TestThreePassesThatDidNotRebuildWarnOnce(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.live = beads.StoreRebuild{Busy: []beads.StoreHolder{{PID: 9, Command: "bd list"}}}

	for pass := 1; pass <= 5; pass++ {
		step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
		want := 0
		if pass >= 3 {
			want = 1
		}
		if f.filed(t) != want {
			t.Fatalf("after pass %d filed %d warnings, want %d (step %q)", pass, f.filed(t), want, step.Detail)
		}
		if pass >= 3 && !strings.Contains(step.Detail, "the development manager was warned in") {
			t.Errorf("pass %d step = %q, want it to name the warning", pass, step.Detail)
		}
		f.now = f.now.Add(time.Hour)
	}
	filed, err := f.reports.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, each := range filed {
		if strings.HasPrefix(each.RunID, "tracker-store-rebuild:") && (!strings.Contains(each.Message, "3 passes in a row") || !strings.Contains(each.Message, "process 9 (bd list)")) {
			t.Errorf("warning = %q, want it to count the passes and name the process", each.Message)
		}
	}
	f.rebuilder.live = beads.StoreRebuild{Waited: "the tracker calls already running did not finish within 2m0s"}
	stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if f.filed(t) != 2 {
		t.Errorf("filed %d warnings once the reason changed, want 2", f.filed(t))
	}
}

// A rebuild that goes ahead ends the run of passes that did not, so the next
// pass held back starts counting again.
func TestARebuildThatGoesAheadEndsTheCount(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.live = beads.StoreRebuild{Busy: []beads.StoreHolder{{PID: 9, Command: "bd list"}}}
	for range 2 {
		stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
		f.now = f.now.Add(time.Hour)
	}
	f.rebuilder.live = beads.StoreRebuild{Records: 1881, Took: 20 * time.Second, HeldFor: 22 * time.Second, SizeBefore: 1500 * megabyte, SizeAfter: 1200 * megabyte, Swapped: true}
	stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	before := f.filed(t)
	f.now = f.now.Add(time.Hour)
	f.rebuilder.live = beads.StoreRebuild{Busy: []beads.StoreHolder{{PID: 9, Command: "bd list"}}}
	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if f.filed(t) != before || !strings.Contains(step.Detail, "1 pass(es) in a row") {
		t.Errorf("after a rebuild went ahead, a pass held back = %q with %d new warnings, want it counted as the first and no warning", step.Detail, f.filed(t)-before)
	}
}

// A rebuilt store still past its limit warns once, and not again while its
// figures are the same.
func TestAStoreStillPastItsLimitAfterTheRebuildWarnsOnce(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 1500*megabyte)
	f.rebuilder.live.SizeAfter = 1200 * megabyte

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "STILL PAST") || f.filed(t) != 1 {
		t.Fatalf("rebuild step = %+v, %d warnings, want one warning that it is still past", step, f.filed(t))
	}
	f.now = f.now.Add(8 * time.Hour)
	stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if f.filed(t) != 1 {
		t.Errorf("filed %d warnings for the same figures, want one", f.filed(t))
	}
}

// Stores kept for more than 14 days are removed, and the step says which.
func TestStoresKeptMoreThanFourteenDaysAreRemoved(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t, 500*megabyte)
	kept, err := f.rebuilds.KeptDirectory()
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	old := f.rebuilds.NewName(f.now.Add(-15 * 24 * time.Hour))
	recent := f.rebuilds.NewName(f.now.Add(-13 * 24 * time.Hour))
	for _, name := range []string{old, recent} {
		if _, err := kept.CreateDirectory(name); err != nil {
			t.Fatal(err)
		}
	}

	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if !strings.Contains(step.Detail, old) || strings.Contains(step.Detail, recent) {
		t.Errorf("rebuild step = %q, want only the old store removed", step.Detail)
	}
	if _, err := os.Stat(filepath.Join(kept.Path(), recent)); err != nil {
		t.Errorf("the recent kept store is gone: %v", err)
	}
}

// A pass with nothing wired says so.
func TestAPassWithNoRebuildWiredSaysSo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	step := stepNamed(t, f.pass1(t), StepTrackerStoreRebuild)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "no rebuild of the tracker's store is wired") {
		t.Errorf("rebuild step = %+v, want it skipped as not wired", step)
	}
}
