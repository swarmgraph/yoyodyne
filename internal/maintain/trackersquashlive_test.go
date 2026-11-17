package maintain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// sizedStore is the live store as the squash's due check sees it: only its
// size on disk, which it reports or fails to.
type sizedStore struct {
	size   int64
	failed error
}

func (s sizedStore) StoreSize() (string, int64, error) {
	if s.failed != nil {
		return "", 0, s.failed
	}
	return "/opt/calc/.beads/embeddeddolt", s.size, nil
}

func (sizedStore) CleanUpStore(context.Context, time.Duration) (beads.StoreCleanup, error) {
	return beads.StoreCleanup{}, errors.New("not used")
}

func (sizedStore) CleanUpStoreBeside(context.Context, time.Duration) (beads.StoreCleanup, error) {
	return beads.StoreCleanup{}, errors.New("not used")
}

func (sizedStore) MissingItems(context.Context, []string) ([]string, error) {
	return nil, errors.New("not used")
}

// fastMeasurement is a measurement whose listings are fast as the store is
// and after the cleanup, as they are on an idle copy of a large live store.
func fastMeasurement() beads.StoreMeasurement {
	measured := slowUntilSquashed(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC))
	listings(&measured, beads.StageAsItIs, 2*time.Second)
	listings(&measured, beads.StageCleanedUp, 2*time.Second)
	return measured
}

// withLiveCalls wires a log of the harness's tracker calls holding calls.
func withLiveCalls(t *testing.T, f *squashFixture, calls ...beads.LoggedCall) {
	t.Helper()
	log, err := runstate.NewTrackerCallLogStore(t.TempDir(), "calc", "yoyo watch")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		log.LogCall(call)
	}
	if err := log.Failed(); err != nil {
		t.Fatal(err)
	}
	f.pass.Calls = log
}

// A live store past its size limit is due the 30 day squash although the
// copy's listings were fast, and the step and the record say why.
func TestALiveStorePastItsSizeLimitIsSquashedWhateverTheCopyListed(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, fastMeasurement())
	f.pass.Tracker = sizedStore{size: 1419 * megabyte}

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 1 || got[0] != beads.StageOldSquashed {
		t.Fatalf("squashed %v, want history older than 30 days", got)
	}
	if step.Outcome != runstate.StepRan {
		t.Fatalf("squashing step = %+v, want a squash", step)
	}
	for _, want := range []string{runstate.SayBytes(1419*megabyte) + " on disk", "past its limit of 1024 MB"} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("squashing step = %q, want it to say %q", step.Detail, want)
		}
	}
	squashes, err := f.squashes.Read()
	if err != nil || len(squashes) != 1 || !strings.Contains(squashes[0].LiveDue, "past its limit of 1024 MB") {
		t.Errorf("squashes = %+v, %v, want one recorded with the live reason", squashes, err)
	}
}

// A live store under its size limit with fast listings needs no squash.
func TestALiveStoreUnderItsSizeLimitWithFastListingsIsNotSquashed(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, fastMeasurement())
	f.pass.Tracker = sizedStore{size: 900 * megabyte}

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 {
		t.Fatalf("squashed %v, want nothing", got)
	}
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "so no squash is needed") {
		t.Errorf("squashing step = %+v, want it skipped, no squash needed", step)
	}
}

// A live store under its size limit is still due the squash where the
// harness's own listing in the last hour was slower than listing_seconds.
func TestASlowLiveListingMakesTheStoreDueTheSquash(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, fastMeasurement())
	f.pass.Tracker = sizedStore{size: 900 * megabyte}
	withLiveCalls(t, f, listing(f.now.Add(-20*time.Minute), "yoyo watch", 25*time.Second))

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 1 || got[0] != beads.StageOldSquashed {
		t.Fatalf("squashed %v, want history older than 30 days", got)
	}
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "listing_seconds") {
		t.Errorf("squashing step = %+v, want a squash naming listing_seconds", step)
	}
}

// A listing that was slow more than an hour ago, or fast, does not make the
// store due.
func TestOldOrFastLiveListingsDoNotMakeTheStoreDue(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, fastMeasurement())
	f.pass.Tracker = sizedStore{size: 900 * megabyte}
	withLiveCalls(t, f,
		listing(f.now.Add(-3*time.Hour), "yoyo watch", time.Minute),
		listing(f.now.Add(-10*time.Minute), "yoyo watch", 3*time.Second),
	)

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepSkipped {
		t.Errorf("squashed %v, step %+v, want nothing squashed", got, step)
	}
}

// A store due by its size whose squash held the copy longer than the drain
// bound is not squashed, and the one warning says why it is due, with the
// size, and the time measured.
func TestADueStoreWhoseSquashIsTooLongOnTheCopyIsWarnedAboutWithItsSize(t *testing.T) {
	t.Parallel()
	measured := fastMeasurement()
	measured.Stages[2].Took = 20 * time.Minute
	f := newSquashFixture(t, measured)
	f.pass.Tracker = sizedStore{size: 1419 * megabyte}
	f.pass.DrainBound = 15 * time.Minute

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepSkipped {
		t.Fatalf("squashed %v, step %+v, want nothing squashed", got, step)
	}
	// The limits step files its own warning about the size; only the squash
	// step's is read here.
	all, err := f.reports.List()
	if err != nil {
		t.Fatal(err)
	}
	var filed []report.Report
	for _, one := range all {
		if strings.HasPrefix(one.RunID, "tracker-store-squash:") {
			filed = append(filed, one)
		}
	}
	if len(filed) != 1 {
		t.Fatalf("squash warnings = %+v, want exactly one", filed)
	}
	for _, want := range []string{
		"The store is due one, because the live store is " + runstate.SayBytes(1419*megabyte) + " on disk, past its limit of 1024 MB",
		"20m0s",
		"15m0s",
	} {
		if !strings.Contains(filed[0].Message, want) {
			t.Errorf("warning = %q, want it to say %q", filed[0].Message, want)
		}
	}
}

// A store due by its size is still not squashed where the copy cannot show
// every export unchanged through the squash: the conditions on the copy hold.
func TestADueLiveStoreIsNotSquashedWhereTheCopyCannotShowTheExportsUnchanged(t *testing.T) {
	t.Parallel()
	measured := fastMeasurement()
	measured.Stages[2].Export.Compared = false
	f := newSquashFixture(t, measured)
	f.pass.Tracker = sizedStore{size: 1419 * megabyte}

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepSkipped {
		t.Fatalf("squashed %v, step %+v, want nothing squashed", got, step)
	}
	for _, want := range []string{"cannot show every item and note unchanged", "due one because the live store is"} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("squashing step = %q, want it to say %q", step.Detail, want)
		}
	}
}

// A size on disk that cannot be read does not make the store due, and the
// step says the size could not be read.
func TestALiveStoreSizeThatCannotBeReadIsSaidAndSquashesNothing(t *testing.T) {
	t.Parallel()
	f := newSquashFixture(t, fastMeasurement())
	f.pass.Tracker = sizedStore{failed: errors.New("disk gone")}

	step := stepNamed(t, f.pass1(t), StepTrackerSquash)
	if got := f.squasher.stages(); len(got) != 0 || step.Outcome != runstate.StepSkipped {
		t.Fatalf("squashed %v, step %+v, want nothing squashed", got, step)
	}
	for _, want := range []string{"could not be read", "disk gone"} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("squashing step = %q, want it to say %q", step.Detail, want)
		}
	}
}
