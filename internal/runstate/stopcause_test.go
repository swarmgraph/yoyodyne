package runstate

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/recovery"
)

var stopCauseNow = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

func newStopCauseStore(t *testing.T) *StopCauseStore {
	t.Helper()
	store, err := NewStopCauseStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewStopCauseStore() error = %v", err)
	}
	return store
}

func checkTimeStop(run string, at time.Time) StopEvent {
	return StopEvent{Run: run, StoppedAt: at, Item: "item-" + run, Cause: StopCauseKey{Cause: CauseCheckTimeLimit.StopClass(), Side: StopSideBound, Step: PhaseChecking}}
}

func reviewStop(run string, at time.Time) StopEvent {
	return StopEvent{Run: run, StoppedAt: at, Item: "item-" + run, Cause: StopCauseKey{Cause: StopReview, Side: StopSideWork, Step: PhaseReviewing}}
}

func recordStops(t *testing.T, store *StopCauseStore, stops ...StopEvent) StopCauseAccount {
	t.Helper()
	account, err := store.Record(context.Background(), "factory-flow-pm", stopCauseNow, func(account StopCauseAccount) ([]StopCauseEntry, error) {
		return account.StopEntries(stops), nil
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return account
}

func attachReport(t *testing.T, store *StopCauseStore, cause StopCauseKey, report string, at time.Time) StopCauseAccount {
	t.Helper()
	account, err := store.Record(context.Background(), "factory-flow-pm", at, func(StopCauseAccount) ([]StopCauseEntry, error) {
		return []StopCauseEntry{{Kind: StopCauseReport, Cause: cause, Report: report, Fault: StopFaultBound, Remedy: "raise the limit"}}, nil
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return account
}

// A cause is read from the run record: the stop cause it prints, the class
// that cause falls in, and the step it stopped at. A record naming no cause
// or no step is counted under unknown or unrecorded rather than guessed.
func TestAStopCauseIsReadFromTheRunRecord(t *testing.T) {
	t.Parallel()

	state := State{RunID: "run-a", WorkItemID: "item-a", Status: StatusFailed, Phase: PhaseChecking, StopClass: CauseCheckTimeLimit.StopClass()}
	key := StopCauseKeyOf(state)
	if key != (StopCauseKey{Cause: CauseCheckTimeLimit.StopClass(), Side: StopSideBound, Step: PhaseChecking}) {
		t.Fatalf("StopCauseKeyOf() = %+v", key)
	}
	if key.ID() != "check-time-limit/bound/checking" {
		t.Errorf("ID() = %q", key.ID())
	}
	if err := key.Validate(); err != nil {
		t.Errorf("Validate() = %v", err)
	}

	unknown := StopCauseKeyOf(State{RunID: "run-b", Status: StatusFailed})
	if unknown.Cause != StopUnknown || unknown.Side != StopSideOutside || unknown.Step != unrecordedStep {
		t.Errorf("StopCauseKeyOf(old record) = %+v, want unknown, outside, unrecorded", unknown)
	}
	if err := (StopCauseKey{Cause: StopReview, Side: StopSideBound, Step: PhaseReviewing}).Validate(); err == nil {
		t.Error("Validate() accepted a class that is not the cause's own")
	}
	for class, want := range map[StopClass]StopSide{
		StopChecks: StopSideWork, StopEscalated: StopSideWork, StopRepairBudget: StopSideBound,
		StopProviderIdle: StopSideOutside, StopRedeploy: StopSideOutside, CauseUsageWindow.StopClass(): StopSideOutside,
	} {
		if got := StopSideOf(class); got != want {
			t.Errorf("StopSideOf(%s) = %s, want %s", class, got, want)
		}
	}
}

// Every cause the run record can print is sorted by name, so none of them
// reaches the default; a cause this build has never heard of is sorted as about
// the work and said to be unrecognised.
func TestEveryStopCauseIsSortedByNameAndAnUnrecognisedOneIsAboutTheWork(t *testing.T) {
	t.Parallel()

	for _, class := range StopClasses() {
		if !StopRecognised(class) {
			t.Errorf("stop cause %q is sorted by the default rather than by name", class)
		}
	}
	invented := StopClass("a-cause-from-a-newer-build")
	if StopRecognised(invented) || StopSideOf(invented) != StopSideWork {
		t.Errorf("an invented cause is recognised = %t, sorted %s, want unrecognised and about the work", StopRecognised(invented), StopSideOf(invented))
	}
}

// The hold on new work leaves out only the causes outside the work, the bounds
// step 1 of stopped-run-recovery places outside it, and the stops the operator
// or the development manager asked for. The harness's own
// failures around a run, a dead process, a cancellation naming no origin, a
// publishing failure, a record that names no cause, and an unrecognised cause
// are counted (stopped-run-recovery, step 1, as ruled on October 19).
func TestTheHoldKeepsOutOnlyOutsideCausesAndStopsSomebodyAskedFor(t *testing.T) {
	t.Parallel()

	for class, want := range map[StopClass]bool{
		StopOperator: true, StopManager: true, StopRedeploy: true,
		StopProvider: true, StopProviderIdle: true, StopRelaunchBudget: true,
		StopUsagePause: true, CauseUsageWindow.StopClass(): true,
		StopDeveloperAccount: true, StopReviewAccount: true,
		StopOutside: true, CauseTransportFailure.StopClass(): true,
		CauseMergeQueueHeld.StopClass(): true,
		StopDeadClaim:                   false, StopCancelled: false, StopPublish: false,
		StopHarness: false, StopRecording: false, StopCleanup: false, StopUnknown: false,
		StopChecks: false, StopReview: false, StopRepairBudget: false, StopRecoveryWindow: false,
		"a-cause-from-a-newer-build": false,
		// Step 1 of stopped-run recovery: a check stopped on time after its
		// reruns, a merge race lost as many times as a run may, no configured
		// check running, the reviewer's session out of budget, tests that also
		// fail on the base commit, and a merge the forge's required merge queue
		// refused say nothing about the work.
		StopCheckTimeout: true, CauseCheckTimeLimit.StopClass(): true, CauseCheckStageBound.StopClass(): true,
		StopLostRaces: true, CauseNoCheckRan.StopClass(): true, StopReviewBudget: true,
		StopBaseFailing: true, StopForgeQueueRequired: true,
		// Close to those, and still counted: a replay that stopped on the change
		// past the integration budget, the developer's session out of budget, a
		// replay conflict or other promotion failure, and the wait for a turn to
		// merge.
		StopIntegrationBudget: false, StopProviderBudget: false, StopIntegration: false,
		StopPromotionWait: false, StopReviewBound: false, StopContextBound: false,
	} {
		if got := StopKeptOutOfHold(class); got != want {
			t.Errorf("StopKeptOutOfHold(%s) = %t, want %t", class, got, want)
		}
		if got := StopKeptOutOfHoldAfter(class, nil); got != want {
			t.Errorf("StopKeptOutOfHoldAfter(%s, no waits) = %t, want %t", class, got, want)
		}
	}
}

// spentWindow is the waits a run records at one boundary up to the point its
// recovery window has no wait left, as recoverFrom would have recorded them.
func spentWindow(boundary string) []Retry {
	var retries []Retry
	var waited time.Duration
	for attempt := 1; waited+recovery.Interval(attempt) <= recovery.Window; attempt++ {
		delay := recovery.Interval(attempt)
		retries = append(retries, Retry{Boundary: boundary, Attempt: attempt, DelaySeconds: int64(delay / time.Second)})
		waited += delay
	}
	return retries
}

// The reviewer's session running out of budget is its own cause, kept apart from
// the developer's, which still counts: by the invocation that stopped, and by the
// step a run whose process vanished was parked at.
func TestTheReviewersBudgetIsItsOwnCause(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		got  StopClass
		want StopClass
	}{
		{"the reviewer out of budget", ReviewStopClass(ProviderStopBudgetExhausted), StopReviewBudget},
		{"the reviewer gone silent", ReviewStopClass(ProviderStopStalled), StopProviderIdle},
		{"a vanished process parked at review", ProviderStopClassAt(ProviderStopBudgetExhausted, PhaseReviewing), StopReviewBudget},
		{"a vanished process parked in development", ProviderStopClassAt(ProviderStopBudgetExhausted, PhaseDeveloping), StopProviderBudget},
	} {
		if test.got != test.want {
			t.Errorf("%s: class = %s, want %s", test.name, test.got, test.want)
		}
	}
	if !StopKeptOutOfHold(StopReviewBudget) || StopKeptOutOfHold(StopProviderBudget) {
		t.Error("want the reviewer's budget left out of the hold and the developer's counted")
	}
}

// A publishing failure counts unless the forge's retries ran out, and a spent
// recovery window is left out only at the forge or the provider. A window that
// still had room, and one spent at the tracker, count.
func TestThePublishingFailureTheHoldLeavesOutIsTheForgesRetriesRunningOut(t *testing.T) {
	t.Parallel()

	oneWait := []Retry{{Boundary: RetryMerge, Attempt: 1, DelaySeconds: 1}}
	forgeThenTracker := append(spentWindow(RetryMerge), Retry{Boundary: RetryTrackerWrite, Attempt: 1, DelaySeconds: 1})
	for _, test := range []struct {
		name    string
		class   StopClass
		retries []Retry
		want    bool
	}{
		{"publishing failure with no waits", StopPublish, nil, false},
		{"publishing failure after one wait at the merge", StopPublish, oneWait, false},
		{"publishing failure after the merge's retries ran out", StopPublish, spentWindow(RetryMerge), true},
		{"publishing failure after the merge confirmation's retries ran out", StopPublish, spentWindow(RetryMergeConfirmation), true},
		{"publishing failure after pushing the branch ran out of retries", StopPublish, spentWindow(RetryPublishBranch), true},
		{"publishing failure whose last wait was the tracker's", StopPublish, forgeThenTracker, false},
		{"publishing failure after catching the target up ran out of retries", StopPublish, spentWindow(RetryCatchUpTarget), false},
		{"recovery window spent at the forge", StopRecoveryWindow, spentWindow(RetryOpenPullRequest), true},
		{"recovery window spent at the provider", StopRecoveryWindow, spentWindow(RetryProviderInvocation), true},
		{"recovery window spent at the tracker", StopRecoveryWindow, spentWindow(RetryTrackerWrite), false},
		{"publishing failure after the provider's retries ran out", StopPublish, spentWindow(RetryProviderInvocation), false},
		{"checks failing after the forge's retries ran out", StopChecks, spentWindow(RetryMerge), false},
	} {
		if got := StopKeptOutOfHoldAfter(test.class, test.retries); got != test.want {
			t.Errorf("%s: StopKeptOutOfHoldAfter(%s) = %t, want %t", test.name, test.class, got, test.want)
		}
	}
}

// Every stop cause the run record can print is said in ordinary words, so a
// post-mortem never names a cause by the harness's identifier alone.
func TestEveryStopCauseIsSaidInOrdinaryWords(t *testing.T) {
	t.Parallel()

	for _, class := range StopClasses() {
		if says := StopSays(class); says == string(class) || strings.TrimSpace(says) == "" {
			t.Errorf("stop cause %q has no ordinary words", class)
		}
	}
	if got := (StopCauseKey{Cause: StopRepairBudget, Side: StopSideBound, Step: PhaseCleaningUp}).Describe(); got != "the repair attempts ran out, at the cleanup step, which is a bound on the work" {
		t.Errorf("Describe() = %q", got)
	}
}

// The design's three examples: one cause across two passes, a replayed pass,
// and a separate cause.
func TestStopsAreGroupedByCauseAndCountedOncePerRunAndTime(t *testing.T) {
	t.Parallel()

	store := newStopCauseStore(t)
	a, b, c := checkTimeStop("run-a", stopCauseNow.Add(-3*time.Hour)), checkTimeStop("run-b", stopCauseNow.Add(-2*time.Hour)), checkTimeStop("run-c", stopCauseNow.Add(-time.Hour))

	// Pass one is handed A and B: one cause, count two.
	account := recordStops(t, store, a, b)
	if len(account.Causes) != 1 || account.Causes[0].Count() != 2 {
		t.Fatalf("after pass one: %+v, want one cause counted twice", account.Causes)
	}
	account = attachReport(t, store, a.Cause, "report-one", stopCauseNow)

	// Pass two is handed C: count three, and the report stands.
	account = recordStops(t, store, c)
	tally, _ := account.Cause(a.Cause.ID())
	if tally.Count() != 3 {
		t.Fatalf("after pass two: count %d, want 3", tally.Count())
	}
	if current, found := tally.Current(); !found || current.Report != "report-one" {
		t.Errorf("Current() = %+v, %v, want the first report", current, found)
	}

	// Pass two run again, handed C once more, and also handed it twice in one
	// list: nothing is counted twice.
	before, _ := store.List("factory-flow-pm")
	account = recordStops(t, store, c, c)
	after, _ := store.List("factory-flow-pm")
	tally, _ = account.Cause(a.Cause.ID())
	if tally.Count() != 3 || len(after) != len(before) {
		t.Fatalf("replay: count %d and %d entries (was %d), want 3 and nothing appended", tally.Count(), len(after), len(before))
	}

	// The same run stopped again after a re-run is a new stop time.
	account = recordStops(t, store, checkTimeStop("run-c", stopCauseNow))
	tally, _ = account.Cause(a.Cause.ID())
	if tally.Count() != 4 {
		t.Errorf("re-run stop: count %d, want 4", tally.Count())
	}

	// D stopped at review in the work: a second cause, and the first unchanged.
	account = recordStops(t, store, reviewStop("run-d", stopCauseNow))
	if len(account.Causes) != 2 {
		t.Fatalf("causes = %d, want 2", len(account.Causes))
	}
	review, _ := account.Cause("review/work/reviewing")
	first, _ := account.Cause(a.Cause.ID())
	if review.Count() != 1 || first.Count() != 4 {
		t.Errorf("counts = %d and %d, want 1 and 4", review.Count(), first.Count())
	}
	if _, reported := review.Current(); reported {
		t.Error("the second cause carries the first cause's report")
	}
}

// A stop whose run record has come to name another cause is moved by an
// appended entry, and the entry that first counted it is left as it was.
func TestAMisclassifiedStopIsMovedByAnAppendedEntry(t *testing.T) {
	t.Parallel()

	store := newStopCauseStore(t)
	stop := checkTimeStop("run-a", stopCauseNow.Add(-time.Hour))
	recordStops(t, store, stop)
	original, err := os.ReadFile(store.Path("factory-flow-pm"))
	if err != nil {
		t.Fatal(err)
	}

	moved := stop
	moved.Cause = StopCauseKey{Cause: StopChecks, Side: StopSideWork, Step: PhaseChecking}
	account := recordStops(t, store, moved)
	if old, _ := account.Cause(stop.Cause.ID()); old.Count() != 0 {
		t.Errorf("the old cause still counts %d", old.Count())
	}
	if now, _ := account.Cause(moved.Cause.ID()); now.Count() != 1 {
		t.Errorf("the new cause counts %d, want 1", now.Count())
	}
	rewritten, err := os.ReadFile(store.Path("factory-flow-pm"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(rewritten), string(original)) {
		t.Error("the record was rewritten rather than appended to")
	}
	entries, _ := store.List("factory-flow-pm")
	if last := entries[len(entries)-1]; last.Kind != StopCauseMove || last.Reading == "" {
		t.Errorf("last entry = %+v, want a move saying why", last)
	}
}

// A correction to a report is a note appended against it; the report entry
// stands as it was written.
func TestACorrectionIsANoteAppendedAgainstTheReport(t *testing.T) {
	t.Parallel()

	store := newStopCauseStore(t)
	stop := checkTimeStop("run-a", stopCauseNow.Add(-time.Hour))
	recordStops(t, store, stop)
	attachReport(t, store, stop.Cause, "report-one", stopCauseNow)
	account, err := store.Record(context.Background(), "factory-flow-pm", stopCauseNow, func(StopCauseAccount) ([]StopCauseEntry, error) {
		return []StopCauseEntry{{Kind: StopCauseNote, Cause: stop.Cause, Report: "report-one", Reading: "the limit was not the cause; the tracker was slow"}}, nil
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	tally, _ := account.Cause(stop.Cause.ID())
	current, _ := tally.Current()
	if current.Remedy != "raise the limit" || len(current.Notes) != 1 {
		t.Errorf("Current() = %+v, want the report unchanged with one note", current)
	}
}

// Once a cause's report is handled, a new report is owed only at the set count
// of stops since the handling, or at once on a stop of priority 0 work; until
// it is handled, recurrences are carried in the digest.
func TestANewReportIsOwedAtTheRecurrenceCountOrOnTopPriorityWork(t *testing.T) {
	t.Parallel()

	handledAt := stopCauseNow
	handled := func(report string) (time.Time, bool) { return handledAt, report == "report-one" }
	unhandled := func(string) (time.Time, bool) { return time.Time{}, false }
	cause := checkTimeStop("x", time.Time{}).Cause

	tally := StopCauseTally{Cause: cause, Stops: []StopEvent{checkTimeStop("run-a", stopCauseNow.Add(-time.Hour))}}
	if due := tally.Standing(handled, 0).Due; due != StopCauseDueOriginal {
		t.Fatalf("no report: due %s, want original", due)
	}
	tally.Reports = []StopCauseReportRef{{Report: "report-one"}}
	tally.Stops = append(tally.Stops, checkTimeStop("run-b", stopCauseNow.Add(time.Minute)), checkTimeStop("run-c", stopCauseNow.Add(2*time.Minute)), checkTimeStop("run-d", stopCauseNow.Add(3*time.Minute)))
	if due := tally.Standing(unhandled, 0).Due; due != StopCauseDueCarried {
		t.Errorf("unhandled report: due %s, want carried", due)
	}

	two := tally
	two.Stops = tally.Stops[:3]
	if standing := two.Standing(handled, 0); standing.Due != StopCauseDueCarried || standing.SinceHandled != 2 {
		t.Errorf("two since handling: %+v, want carried with two", standing)
	}
	if standing := tally.Standing(handled, 0); standing.Due != StopCauseDueRecurred || standing.SinceHandled != 3 {
		t.Errorf("three since handling: %+v, want recurred with three", standing)
	}
	if due := tally.Standing(handled, 5).Due; due != StopCauseDueCarried {
		t.Errorf("three since handling against a threshold of five: due %s, want carried", due)
	}

	zero := 0
	top := checkTimeStop("run-e", stopCauseNow.Add(time.Minute))
	top.Priority = &zero
	urgent := StopCauseTally{Cause: cause, Reports: tally.Reports, Stops: []StopEvent{top}}
	if due := urgent.Standing(handled, 0).Due; due != StopCauseDueTopPriority {
		t.Errorf("a priority 0 stop since handling: due %s, want top-priority", due)
	}
	before := urgent
	before.Stops = []StopEvent{top}
	before.Stops[0].StoppedAt = stopCauseNow.Add(-time.Minute)
	if due := before.Standing(handled, 0).Due; due != StopCauseDueCarried {
		t.Errorf("a priority 0 stop before the handling: due %s, want carried", due)
	}
}

// What another product or instance wrote is not read as this one's.
func TestAStopCauseRecordForAnotherInstanceIsRefused(t *testing.T) {
	t.Parallel()

	store := newStopCauseStore(t)
	recordStops(t, store, checkTimeStop("run-a", stopCauseNow))
	other, err := os.ReadFile(store.Path("factory-flow-pm"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(strings.TrimSuffix(store.Path("writing-pm"), stopCauseFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path("writing-pm"), other, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List("writing-pm"); err == nil {
		t.Error("List() read another instance's record as this one's")
	}
}
