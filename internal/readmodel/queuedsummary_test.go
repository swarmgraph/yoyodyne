package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// queueWaitingRun is a run that ended succeeded by handing its approved change to
// the merge queue as the given entry, which has neither landed it nor handed it
// back: the record names no stop cause, because nothing stopped it.
func queueWaitingRun(id string, order uint64, completed time.Duration) runstate.State {
	state := terminal(id, runstate.StatusSucceeded, -2*time.Hour, completed, false, "")
	state.Phase = runstate.PhaseCleaningUp
	state.MergeQueue = &runstate.MergeQueueAdmissionRecord{
		EntryID: "mqe-" + id, Order: order, Repository: "yoyodyne", TargetBranch: "main",
		Mode: runstate.MergeQueueHarness, AdmittedAt: noon.Add(completed),
	}
	return state
}

// Succeeded runs waiting in the merge queue beside one run that really failed:
// the failures line counts the one failure, the queued changes are in progress
// as waiting to land with their place in the queue, and neither the count nor
// the failure group's latest time comes from a queued run.
func TestQueuedRunsAreWaitingToLandAndTheFailuresLineCountsOnlyRealStops(t *testing.T) {
	t.Parallel()
	first := queueWaitingRun("queued-1", 34, -50*time.Minute)
	second := queueWaitingRun("queued-2", 35, -20*time.Minute)
	failed := terminal("failed", runstate.StatusFailed, -3*time.Hour, -2*time.Hour, false, "the checks kept failing")
	failed.StopClass = runstate.StopChecks
	// Its change left the queue without landing, so it stopped after all.
	lost := queueWaitingRun("lost", 33, -3*time.Hour)
	lost.Status, lost.StopClass, lost.Blocker = runstate.StatusFailed, runstate.StopIntegration, "the queue lost the entry"
	// Still in the queue, but the item was handed to somebody with a blocker,
	// which the run history prints as stopped: a stop whose cause the record
	// does not name.
	blocked := queueWaitingRun("blocked", 32, -4*time.Hour)
	blocked.Blocker = "handed back to the development manager"

	reading := ReadThroughput(context.Background(), ThroughputSources{
		Runs: fakeRuns{recorded: []runstate.State{first, second, failed, lost, blocked}},
		Now:  func() time.Time { return noon },
	})
	today := window(t, reading, "today")
	if today.Queued != 2 || today.Succeeded != 0 || today.Landed != 0 || today.Stopped != 3 {
		t.Fatalf("today counted %+v; want the two queued runs as queued, neither succeeded without landing nor landed", today)
	}
	if len(today.StopsByCause) != 3 || today.StopsByCause[runstate.StopChecks] != 1 || today.StopsByCause[runstate.StopIntegration] != 1 || today.StopsByCause[runstate.StopUnknown] != 1 {
		t.Fatalf("stops = %v; want the checks failure, the lost entry under the merge, and the blocked run as unnamed", today.StopsByCause)
	}
	if latest := today.LatestStopByCause[runstate.StopUnknown]; !latest.Equal(noon.Add(-4 * time.Hour)) {
		t.Fatalf("the unnamed stop's latest time is %s; want the blocked run's ending, not a queued run's", latest)
	}
	for _, queued := range []runstate.State{first, second} {
		if class := queued.RecordedStopClass(); class != "" {
			t.Fatalf("run %s waiting in the merge queue records stop class %q; it has not stopped", queued.RunID, class)
		}
	}

	// The real failure alone, beside the queued runs: a failures count of one.
	only := ReadThroughput(context.Background(), ThroughputSources{
		Runs: fakeRuns{recorded: []runstate.State{first, second, failed}},
		Now:  func() time.Time { return noon },
	})
	standing := Standing{ObservedAt: noon, Running: []RunningRun{}, NotStartable: []Refused{}, MergeQueues: []MergeQueueStanding{{
		Repository: "yoyodyne", TargetBranch: "main", Open: 3, Finished: 1,
		Entries: []QueueEntryStanding{
			{EntryID: "mqe-handed", Order: 31, Place: 31, WorkItemID: "ex-31", Title: "Handed back", RunID: "run-handed", State: QueueEntryHandedBack, Label: QueueEntryHandedBack.Label()},
			{EntryID: "mqe-queued-1", Order: 34, Place: 34, WorkItemID: "ex-34", Title: "Integrate to a local branch", RunID: "queued-1", State: QueueEntryWaiting, Label: QueueEntryWaiting.Label(), Why: "waiting for its turn"},
			{EntryID: "mqe-queued-2", Order: 35, Place: 35, WorkItemID: "ex-35", Title: "Publish the machine home", RunID: "queued-2", State: QueueEntryVerifying, Label: QueueEntryVerifying.Label()},
			{EntryID: "mqe-landed", Order: 30, Place: 30, WorkItemID: "ex-30", Title: "Already landed", RunID: "run-landed", State: QueueEntryLanded, Label: QueueEntryLanded.Label()},
		},
	}}}
	summary := Summarize(SummaryInputs{Standing: &standing, Throughput: &only, Period: SummaryToday, Now: noon})

	if summary.Failures.Says != "1 run" || len(summary.Failures.Entries) != 1 {
		t.Fatalf("failures = %+v; want the one run that failed", summary.Failures)
	}
	if failure := summary.Failures.Entries[0]; failure.Says != "1 run: "+runstate.StopSays(runstate.StopChecks) || !failure.Latest.Equal(noon.Add(-2*time.Hour)) {
		t.Fatalf("failure line = %+v", failure)
	}
	if summary.Moving.Says != "no runs, and 2 approved changes waiting to land" || len(summary.Moving.Entries) != 2 {
		t.Fatalf("in progress = %+v; want the two queued changes and not the handed-back or landed entries", summary.Moving)
	}
	waiting := entryWith(t, summary.Moving, "Integrate to a local branch (ex-34)")
	if !strings.HasSuffix(waiting.Says, " — waiting to land: number 2 of 3 in the merge queue for main") || waiting.WorkItemID != "ex-34" {
		t.Fatalf("waiting change = %+v", waiting)
	}
	if strings.Join(waiting.Detail, "|") != "run queued-1|merge queue entry 34|waiting for its turn" {
		t.Fatalf("waiting change's detail = %q", waiting.Detail)
	}
	verifying := entryWith(t, summary.Moving, "Publish the machine home (ex-35)")
	if !strings.HasSuffix(verifying.Says, " — waiting to land: number 3 of 3 in the merge queue for main (being verified)") {
		t.Fatalf("verifying change = %+v", verifying)
	}
	rendered := summary.Render(false)
	for _, want := range []string{
		"  In progress: no runs, and 2 approved changes waiting to land\n",
		"    - Integrate to a local branch (ex-34) — waiting to land: number 2 of 3 in the merge queue for main\n",
		"  Ended without landing: 1 run\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the summary does not say %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, runstate.StopSays(runstate.StopUnknown)) {
		t.Fatalf("the summary names a stop the record does not name, where only queued runs lack a cause:\n%s", rendered)
	}
}

// A run that actually stopped with no cause on its record is still said as
// such, beside a queued run that names none either.
func TestAStopWithNoRecordedCauseIsStillSaidForARunThatStopped(t *testing.T) {
	t.Parallel()
	unnamed := terminal("unnamed", runstate.StatusFailed, -2*time.Hour, -time.Hour, false, "")
	reading := ReadThroughput(context.Background(), ThroughputSources{
		Runs: fakeRuns{recorded: []runstate.State{unnamed, queueWaitingRun("queued", 7, -30*time.Minute)}},
		Now:  func() time.Time { return noon },
	})
	summary := Summarize(SummaryInputs{Standing: &Standing{ObservedAt: noon, Running: []RunningRun{}}, Throughput: &reading, Period: SummaryToday, Now: noon})
	if summary.Failures.Says != "1 run" || len(summary.Failures.Entries) != 1 {
		t.Fatalf("failures = %+v", summary.Failures)
	}
	if line := summary.Failures.Entries[0]; line.Says != "1 run: "+runstate.StopSays(runstate.StopUnknown) || !line.Latest.Equal(noon.Add(-time.Hour)) {
		t.Fatalf("failure line = %+v; want the stopped run's own ending", line)
	}
}

// Where a merge queue could not be read, the answer says so rather than
// leaving the changes waiting there out unremarked.
func TestTheInProgressAnswerSaysAMergeQueueItCouldNotRead(t *testing.T) {
	t.Parallel()
	standing := Standing{ObservedAt: noon, Running: []RunningRun{}, MergeQueuesProblem: "the queue for main could not be read: permission denied"}
	summary := Summarize(SummaryInputs{Standing: &standing, Period: SummaryToday, Now: noon})
	if summary.Moving.Says != "no runs; not every merge queue could be read" || len(summary.Moving.Detail) != 1 || !strings.Contains(summary.Moving.Detail[0], "permission denied") {
		t.Fatalf("in progress = %+v", summary.Moving)
	}
}
