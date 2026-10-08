package watchdog

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var moment = time.Date(2026, 9, 1, 6, 5, 0, 0, time.UTC)

// The window this whole instrument exists for, replayed: on 2026-09-01 the watch
// session died on a transient tracker read at 06:05, its last word was that it
// was watching, and for seven and a half hours nothing started while the tracker
// went on reporting work ready. No hold, no stop, no idle poll, no run — nothing
// any surface reads. It was found by a person noticing.
//
// It is checked here rather than in the Slack sink because that is the change
// this package is: the checker runs from machinery that is always running, so a
// product reporting nowhere records the same stall.
func TestTheDeadWindowIsNoticedAndRecordedOnce(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	// Inside the threshold this is a gap between runs, and nothing is recorded.
	harness.now = moment.Add(readmodel.DefaultStallThreshold - time.Minute)
	if reading := harness.check(t); reading.Stalled() {
		t.Fatalf("Check() = %+v, want nothing recorded inside the threshold", reading)
	}

	// Past it, it is a stall, and the record carries what somebody woken by it has
	// to act on: how long nothing has happened, how much was waiting, and whether
	// the thing that chooses work is dead or still claiming to be watching.
	harness.now = moment.Add(readmodel.DefaultStallThreshold + time.Minute)
	reading := harness.check(t)
	if reading.Opened == nil || reading.Standing == nil {
		t.Fatalf("Check() = %+v, want the stall opened and standing", reading)
	}
	if !reading.Standing.Since.Equal(moment) || reading.Standing.Ready != 3 {
		t.Fatalf("the record says since %s over %d ready, want %s and 3",
			reading.Standing.Since, reading.Standing.Ready, moment)
	}
	if !strings.Contains(reading.Standing.Chooser, "watching") {
		t.Fatalf("Chooser = %q, want what the session choosing work last said", reading.Standing.Chooser)
	}

	// And then the seven and a half hours, checked as often as an unattended pass
	// would. One stall stays one stall: saying it once is a property of the record
	// rather than of any one process's memory.
	for check := 0; check < 90; check++ {
		harness.now = harness.now.Add(5 * time.Minute)
		if reading := harness.check(t); reading.Opened != nil {
			t.Fatalf("check %d opened a second stall: %+v", check, reading.Opened)
		}
	}
	events := harness.recorded(t)
	if len(events) != 1 {
		t.Fatalf("List() = %d stalls, want one across the whole window", len(events))
	}
}

// The case the harness's own loop is wired for: a session that is alive, still
// writing a transition on every poll, and no longer starting anything.
//
// It is the other half of the pair. The dead session above leaves a log that
// stops, and `yoyo reconcile` is what reads it; this one goes on saying it is
// polling, which is what a queue whose ready items are all claimed by runs that
// died looks like from every surface — and no sweep is needed to see it, because
// the loop writing those polls is the loop taking this reading.
//
// What makes it visible is that the silence is dated from the runs rather than
// from the session's account of itself: a poll that started nothing is not a
// start, so a session can say it is watching all night without moving the moment
// this is measured from. That is `readmodel.LastHeld`, and it is pinned here
// because the whole justification for taking the reading from inside the loop
// rests on it.
func TestALiveSessionStillPollingAndStartingNothingIsAStall(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(3)
	// A session that has been up for three hours and last started something two
	// hours ago. The run is over, so it accounts for nothing now.
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.record(t, runstate.StatusSucceeded, moment.Add(time.Hour))

	// And then thirty polls, one a minute, right up to the moment this reading is
	// taken: the session is demonstrably alive and demonstrably starting nothing.
	harness.now = moment.Add(3 * time.Hour)
	for poll := 30; poll >= 1; poll-- {
		harness.watched(t, runstate.WatchIdle, "nothing pullable this poll",
			harness.now.Add(-time.Duration(poll)*time.Minute))
	}

	reading := harness.check(t)
	if reading.Opened == nil {
		t.Fatalf("Check() = %+v, want a live session that has stopped starting anything read as a stall", reading)
	}
	// Dated from the last run rather than from the last poll. A session whose own
	// polls moved this would never be reported as stalled while it kept polling,
	// which is exactly the state this loop is here to catch.
	if !reading.Opened.Since.Equal(moment.Add(time.Hour)) {
		t.Fatalf("the record says since %s, want %s — when the last run started rather than when the session last polled",
			reading.Opened.Since, moment.Add(time.Hour))
	}
	// And the record says the session is alive, which is what tells whoever reads
	// it to kill this one rather than start one.
	if !strings.Contains(reading.Opened.Chooser, "has said nothing since") ||
		!strings.Contains(reading.Opened.Chooser, string(runstate.WatchIdle)) {
		t.Fatalf("Chooser = %q, want the live session's own last word", reading.Opened.Chooser)
	}
}

// The same live session on a product where no run has ever started. The silence
// is dated from the earliest thing the watch log holds rather than from the
// latest, so a session that has been polling since it started and has never
// pulled anything is a stall rather than a machine that looks busy.
func TestALiveSessionThatHasNeverStartedAnythingIsAStall(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.now = moment.Add(2 * time.Hour)
	for poll := 20; poll >= 1; poll-- {
		harness.watched(t, runstate.WatchIdle, "nothing pullable this poll",
			harness.now.Add(-time.Duration(poll)*time.Minute))
	}

	reading := harness.check(t)
	if reading.Opened == nil {
		t.Fatalf("Check() = %+v, want a session that has never started anything read as a stall", reading)
	}
	if !reading.Opened.Since.Equal(moment) {
		t.Fatalf("the record says since %s, want %s — when this session first said it was watching",
			reading.Opened.Since, moment)
	}
}

// Everything that legitimately accounts for nothing having started, and none of
// them is a stall. Each is either a decision somebody made or the harness
// visibly working, and recording one would put a stall in the history an
// operator reads and a message on somebody's phone for a machine behaving
// exactly as it should.
func TestNothingAccountedForIsEverRecordedAsAStall(t *testing.T) {
	t.Parallel()

	for name, arrange := range map[string]func(*testing.T, *harness){
		"the operator held everything": func(t *testing.T, h *harness) {
			h.ready(3)
			h.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
			if _, err := h.holds.Hold(moment); err != nil {
				t.Fatalf("Hold() error = %v", err)
			}
		},
		"intake is held": func(t *testing.T, h *harness) {
			h.ready(3)
			h.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
			if _, err := h.intake.Hold(runstate.IntakeHolderOperator, "reordering the backlog first", moment); err != nil {
				t.Fatalf("Hold() error = %v", err)
			}
		},
		"a run is in flight and still moving": func(t *testing.T, h *harness) {
			h.ready(3)
			h.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
			h.record(t, runstate.StatusRunning, moment)
			// The run's record is stamped at `moment` and these checks are hours
			// later, so the window is what decides whether it still counts as moving.
			// It is widened past them because this case is a run that is
			// demonstrably working; the phantom is its own test below.
			h.checker.RunActivityWindow = 24 * time.Hour
		},
		"the queue is drained": func(t *testing.T, h *harness) {
			h.ready(0)
			h.watched(t, runstate.WatchIdle, "the backlog is empty", moment)
		},
		"nobody has ever watched this product": func(t *testing.T, h *harness) {
			h.ready(6)
		},
		"the provider will not serve any more work": func(t *testing.T, h *harness) {
			h.ready(3)
			h.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
			lifts := moment.Add(12 * time.Hour)
			h.waitingOnProvider(t, "Paused on the provider's usage limit until 18:05Z", moment.Add(time.Minute), &lifts)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := newHarness(t)
			arrange(t, harness)

			harness.now = moment.Add(9 * time.Hour)
			if reading := harness.check(t); reading.Stalled() {
				t.Fatalf("Check() = %+v, want nothing recorded", reading)
			}
			if events := harness.recorded(t); len(events) != 0 {
				t.Fatalf("List() = %+v, want nothing recorded", events)
			}
		})
	}
}

// A stall that clears closes its event, saying what accounted for it, and a
// second stall afterwards is a second thing rather than the same one again. A
// stall that simply stopped being reported would leave whoever reads the history
// deciding for themselves whether it was fixed or merely stopped being looked
// for.
func TestAClearingStallClosesItsEventAndTheNextIsOpenedAfresh(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(time.Hour)
	if reading := harness.check(t); reading.Opened == nil {
		t.Fatalf("Check() = %+v, want the stall opened", reading)
	}

	// The queue drains, so nothing is waiting on anybody any more.
	harness.ready(0)
	harness.now = harness.now.Add(time.Hour)
	closed := harness.check(t)
	if closed.Closed == nil || closed.Stalled() {
		t.Fatalf("Check() = %+v, want the stall closed", closed)
	}
	if !strings.Contains(closed.Closed.Cleared, "nothing ready") {
		t.Fatalf("Cleared = %q, want what cleared it recorded", closed.Closed.Cleared)
	}

	// Work is admitted again and still nothing starts. That is a second stall.
	harness.ready(5)
	harness.now = harness.now.Add(3 * time.Hour)
	if reading := harness.check(t); reading.Opened == nil {
		t.Fatalf("Check() = %+v, want a second stall opened afresh", reading)
	}
	events := harness.recorded(t)
	if len(events) != 2 || events[0].Open() || !events[1].Open() {
		t.Fatalf("List() = %+v, want the first closed and a second standing", events)
	}
}

// A run whose process is gone does not account for the quiet.
//
// This is the case the whole instrument exists for. A killed run leaves durable
// state saying it is in flight until `yoyo reconcile` settles it, so reading "in
// flight" as "working" would silence the checker for exactly the crash it is
// watching for — and the settling and this check are now steps of the same
// sweep, in that order.
func TestARunWhoseProcessIsGoneDoesNotSilenceTheCheck(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	// A run in flight whose record stops moving at `moment`, because the process
	// carrying it was killed. Nothing settles it and nothing rewrites it.
	harness.record(t, runstate.StatusRunning, moment)

	// While the record is still fresh the run accounts for the quiet, which is the
	// behaviour a working run relies on.
	harness.now = moment.Add(30 * time.Minute)
	if reading := harness.check(t); reading.Stalled() {
		t.Fatalf("Check() = %+v, want a moving run to account for the quiet", reading)
	}

	// Past the window the record says nothing about a live process.
	harness.now = moment.Add(readmodel.DefaultRunActivityWindow + time.Hour)
	reading := harness.check(t)
	if reading.Standing == nil {
		t.Fatalf("Check() = %+v, want the stall recorded over a phantom run", reading)
	}
	if !reading.Standing.Since.Equal(moment) {
		t.Fatalf("the record says since %s, want %s — the moment the last run started",
			reading.Standing.Since, moment)
	}
}

// A tracker that will not answer leaves this unable to tell a stalled machine
// from a drained queue, and it must not guess in either direction: inventing
// ready work wakes somebody for nothing, and assuming none is the silence this
// exists to end. So the check fails, records nothing, and its caller says so.
func TestATrackerThatCannotBeReadRecordsNothingAndSaysWhy(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.checker.Backlog = brokenBacklog{}
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(4 * time.Hour)
	_, err := harness.checker.Check(context.Background())
	if err == nil {
		t.Fatal("Check() error = nil, want a tracker nobody could read reported")
	}
	if !strings.Contains(err.Error(), "ready to pull") {
		t.Fatalf("Check() error = %v, want it to name what could not be read", err)
	}
	if events := harness.recorded(t); len(events) != 0 {
		t.Fatalf("List() = %+v, want nothing recorded over a tracker nobody could read", events)
	}
}

// The tracker is a process this spawns, so it is asked only where the answer
// still turns on it: everything else is derived from records already in hand,
// and a machine that is held, busy, or unwatched costs nothing at all to check.
func TestTheTrackerIsAskedOnlyWhereNothingElseAccountsForTheQuiet(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	backlog := harness.tallies(0)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	if _, err := harness.intake.Hold(runstate.IntakeHolderOperator, "reordering the backlog first", moment); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	harness.now = moment.Add(time.Hour)
	for check := 0; check < 20; check++ {
		harness.now = harness.now.Add(time.Minute)
		harness.check(t)
	}
	if backlog.asked != 0 {
		t.Fatalf("the tracker was read %d time(s) over a held line, want none", backlog.asked)
	}

	// Once nothing accounts for the quiet it is asked, because nothing but the
	// tracker can say whether the queue is drained. A checker that never asked
	// would notice nothing at all.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	harness.check(t)
	if backlog.asked != 1 {
		t.Fatalf("the tracker was read %d time(s) once nothing accounted for the quiet, want once", backlog.asked)
	}
}

// A window the session recorded and then never came out of is not an account of
// anything. The record is the last word of a session that has said nothing
// since, which is exactly the shape of a session that died — so past the
// deadline it accounts for nothing and the check says what it always said.
func TestAWindowThatHasLiftedStopsAccountingForTheQuiet(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 9, 5, 12, 13, 0, 0, time.UTC)
	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	harness := newHarness(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", opened.Add(-time.Hour))
	harness.waitingOnProvider(t, "Paused on the provider's usage limit until 13:43Z", opened, &lifts)

	// Inside the window there is nothing to record, and the window itself is
	// carried back so a surface can say the cause rather than only staying silent.
	harness.now = lifts.Add(-time.Minute)
	inside := harness.check(t)
	if inside.Stalled() {
		t.Fatalf("Check() = %+v, want the window to account for the quiet", inside)
	}
	if !inside.Window.Waiting || !inside.Window.ResetsAt.Equal(lifts) {
		t.Fatalf("Window = %+v, want the window the session recorded", inside.Window)
	}

	// The window lifted and the session went on saying nothing. That is a stall.
	harness.now = lifts.Add(readmodel.DefaultStallThreshold + time.Minute)
	if reading := harness.check(t); reading.Standing == nil {
		t.Fatalf("Check() = %+v, want a stall once the window it was waiting on has lifted", reading)
	}
}

// A dispatch waiting out the tracker before it has claimed anything holds its
// slot with no run record, for up to the recovery window, and the quiet it makes
// is accounted for by the wait its session recorded rather than paged as a hang
// — until the wait has lapsed with nothing after it, which is what a dispatch
// that died mid-wait leaves, and is a stall again.
func TestADispatchWaitingOutTheTrackerAccountsForTheQuietUntilItLapses(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.watched(t, runstate.WatchIdle, "nothing pullable this poll", moment.Add(time.Minute))
	waited := moment.Add(time.Hour)
	wait := runstate.DispatchWait{
		WorkItemID:   "yoyodyne-task",
		Boundary:     runstate.RetryDependencyRead,
		Attempt:      7,
		DelaySeconds: int64((13 * time.Minute) / time.Second),
		At:           waited,
		Failure:      "bd show failed with status timed_out and exit code -1: signal: killed",
	}
	if err := harness.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         runstate.WatchWatching,
		At:            waited,
		Reason:        "the dispatch for yoyodyne-task is waiting out a tracker failure",
		DispatchWait:  &wait,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	harness.now = waited.Add(10 * time.Minute)
	inside := harness.check(t)
	if inside.Stalled() {
		t.Fatalf("Check() = %+v, want the dispatch's wait to account for the quiet", inside)
	}
	if !strings.Contains(inside.Silence.Explains, "yoyodyne-task") || !strings.Contains(inside.Silence.Explains, "tracker failure") {
		t.Fatalf("Explains = %q, want it to name the dispatch waiting out the tracker", inside.Silence.Explains)
	}

	harness.now = wait.Until().Add(readmodel.DefaultStallThreshold)
	lapsed := harness.check(t)
	if lapsed.Opened == nil {
		t.Fatalf("Check() = %+v, want a stall once the wait lapsed with nothing written after it", lapsed)
	}
	// The note is not the session's last word: that is still the idle poll.
	if !strings.Contains(lapsed.Opened.Chooser, string(runstate.WatchIdle)) {
		t.Fatalf("Chooser = %q, want the session's own last transition rather than its dispatch's note", lapsed.Opened.Chooser)
	}
}

// A check assembled without one of its sources refuses rather than deciding.
// Every one of them is a reason nothing has started, and a check that treated an
// unread hold as absent would report a deliberate stop as a machine that died.
func TestACheckMissingASourceRefusesRatherThanDeciding(t *testing.T) {
	t.Parallel()

	_, err := Checker{}.Check(context.Background())
	if err == nil {
		t.Fatal("Check() error = nil, want a checker with no sources refused")
	}
	for _, missing := range []string{"recorded runs", "sessions", "operator's pause", "intake hold", "ready to pull", "stall record"} {
		if !strings.Contains(err.Error(), missing) {
			t.Fatalf("Check() error = %v, want %q named", err, missing)
		}
	}
}

// The false alarm from stalls.jsonl on 2026-09-24, replayed. The watch filled
// every free slot in one pull and the runs took more than an hour, so when the
// batch ended there was an instant with nothing in flight and a last start over
// an hour old. The watch read a stall at that instant, just before the pull
// that filled the slots, twenty times running. One example: opened 13:33:31
// after a last start at 11:39:45. A slot was free for seconds, not an hour.
func TestABatchEndingJustBeforeThePullThatRefillsItOpensNoStall(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(27)
	lastStart := time.Date(2026, 9, 24, 11, 39, 45, 0, time.UTC)
	reading := time.Date(2026, 9, 24, 13, 33, 31, 0, time.UTC)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", lastStart.Add(-time.Minute))
	// Three runs started in one pull, and all of them ended seconds before the
	// reading.
	for slot := 0; slot < 3; slot++ {
		harness.recordEnded(t, runstate.StatusSucceeded, lastStart, reading.Add(-time.Duration(6+slot)*time.Second))
	}

	// The reading at the instant nothing is in flight: the last start is nearly
	// two hours old, and the last end is seconds ago.
	harness.now = reading
	got := harness.check(t)
	if got.Stalled() || got.Opened != nil {
		t.Fatalf("Check() = %+v, want no stall from slots that were free for seconds", got)
	}
	if !got.Silence.Since.Equal(reading.Add(-6 * time.Second)) {
		t.Fatalf("Silence.Since = %s, want the last run end %s rather than the last start",
			got.Silence.Since, reading.Add(-6*time.Second))
	}

	// And the pull that follows, starting a run at the same second.
	harness.record(t, runstate.StatusRunning, reading)
	if got := harness.check(t); got.Stalled() {
		t.Fatalf("Check() = %+v, want no stall once the pull has started a run", got)
	}
	if events := harness.recorded(t); len(events) != 0 {
		t.Fatalf("List() = %+v, want no stall recorded across the whole replay", events)
	}
}

// The other half: a line whose slots stayed free for the whole threshold after
// its last run ended, over ready work and with nothing accounting for it, is
// still a stall, and it is dated from that end.
func TestSlotsFreeForTheWholeThresholdAfterTheLastEndAreAStall(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(4)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	ended := moment.Add(90 * time.Minute)
	harness.recordEnded(t, runstate.StatusSucceeded, moment, ended)

	harness.now = ended.Add(readmodel.DefaultStallThreshold - time.Minute)
	if got := harness.check(t); got.Stalled() {
		t.Fatalf("Check() = %+v, want nothing inside the threshold after the last end", got)
	}
	harness.now = ended.Add(readmodel.DefaultStallThreshold + time.Minute)
	got := harness.check(t)
	if got.Opened == nil {
		t.Fatalf("Check() = %+v, want slots free for the whole threshold read as a stall", got)
	}
	if !got.Opened.Since.Equal(ended) || got.Opened.Ready != 4 {
		t.Fatalf("the record says since %s over %d ready, want %s and 4", got.Opened.Since, got.Opened.Ready, ended)
	}
}

// The crash this alarm exists for, seen from the sweep that settles it. A run's
// process died at T with work ready, its record went still, and two hours later
// `yoyo reconcile` settled it, writing an end at the settlement. The sweep reads
// the stall straight after it settles. Taking that end as activity would read a
// line dead for two hours as one that ended a run a moment ago, open nothing, and
// date the stall from the settlement when a later pass did open it. The run held
// its slot until its record last moved, and the stall is dated from there.
func TestASettlementOfADeadRunIsNotReadAsActivity(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment.Add(-time.Hour))
	quiet := moment
	settled := moment.Add(2 * time.Hour)
	harness.recordSettled(t, moment.Add(-30*time.Minute), quiet, settled)

	harness.now = settled.Add(time.Second)
	got := harness.check(t)
	if got.Opened == nil {
		t.Fatalf("Check() = %+v, want the reading right after the settlement to open the stall", got)
	}
	if !got.Opened.Since.Equal(quiet) {
		t.Fatalf("the record says since %s, want %s — when the dead run last moved rather than when it was settled",
			got.Opened.Since, quiet)
	}
}

// Two readers take this reading, the watching session and the reconcile sweep,
// each in its own process with its own handle on the stall log. Over one
// standing stall, their readings together write one record, whichever reads
// first and however closely together they read.
func TestTheWatchAndTheSweepOverOneStandingStallWriteOneRecord(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.now = moment.Add(readmodel.DefaultStallThreshold + time.Minute)

	// The sweep's checker: the same records, opened separately, as a second
	// process opens them.
	sweep := *harness.checker
	stalls, err := runstate.NewStallStore(harness.root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	sweep.Stalls = stalls

	// Both at once, repeatedly, which is the ordering a check-then-append with
	// nothing between them loses. The two goroutines contend as two processes
	// would: the stall log's lock is flock(2) (runstate's lockStateFile), which
	// is held per open file description, and each Reconcile opens the lock file
	// afresh. POSIX fcntl record locks are per process and would not contend here.
	const rounds = 20
	errs := make(chan error, 2*rounds)
	var wait sync.WaitGroup
	for round := 0; round < rounds; round++ {
		for _, checker := range []Checker{*harness.checker, sweep} {
			wait.Add(1)
			go func(checker Checker) {
				defer wait.Done()
				_, err := checker.Check(context.Background())
				errs <- err
			}(checker)
		}
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Check() error = %v", err)
		}
	}

	events := harness.recorded(t)
	if len(events) != 1 || !events[0].Open() {
		t.Fatalf("List() = %+v, want one standing stall", events)
	}
	raw, err := os.ReadFile(harness.stalls.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 1 {
		t.Fatalf("the stall log holds %d records, want exactly one for one opening:\n%s", lines, raw)
	}
}

// harness is one product's durable records and a checker over them.
type harness struct {
	now     time.Time
	root    string
	runs    *runstate.Store
	watch   *runstate.WatchStore
	holds   *runstate.OperatorHoldStore
	intake  *runstate.IntakeHoldStore
	stalls  *runstate.StallStore
	checker *Checker
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	watch, err := runstate.NewWatchStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	stalls, err := runstate.NewStallStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	h := &harness{
		now:    moment,
		root:   root,
		runs:   runs,
		watch:  watch,
		holds:  holds,
		intake: intake,
		stalls: stalls,
	}
	h.checker = &Checker{
		Runs:     runs,
		Sessions: watch,
		Holds:    holds,
		Intake:   intake,
		Backlog:  countedBacklog{},
		Stalls:   stalls,
		Now:      func() time.Time { return h.now },
	}
	return h
}

func (h *harness) check(t *testing.T) Reading {
	t.Helper()
	reading, err := h.checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	return reading
}

func (h *harness) recorded(t *testing.T) []runstate.StallEvent {
	t.Helper()
	events, err := h.stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	return events
}

// ready gives the checker a tracker that reports this much pullable work.
func (h *harness) ready(count int) {
	h.checker.Backlog = countedBacklog{count: count}
}

// tallies gives the checker a tracker that counts the reads made of it, which is
// the whole of what the cost rule is about: `bd` is a process this spawns, and
// how often it spawns one is the thing under test.
func (h *harness) tallies(count int) *tallyBacklog {
	backlog := &tallyBacklog{count: count}
	h.checker.Backlog = backlog
	return backlog
}

func (h *harness) watched(t *testing.T, state runstate.WatchState, reason string, at time.Time) {
	t.Helper()
	if err := h.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         state,
		At:            at,
		Reason:        reason,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

// waitingOnProvider records the poll a session made inside the provider's usage
// window, which is the entry that tells that silence from one nothing accounts
// for.
func (h *harness) waitingOnProvider(t *testing.T, reason string, at time.Time, resetsAt *time.Time) {
	t.Helper()
	if err := h.watch.Record(runstate.WatchTransition{
		SchemaVersion:          runstate.WatchSchemaVersion,
		ProductID:              "yoyodyne",
		SessionID:              "watch-0123456789abcdef0123456789abcdef",
		State:                  runstate.WatchIdle,
		At:                     at,
		Reason:                 reason,
		ProviderWindow:         true,
		ProviderWindowResetsAt: resetsAt,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func (h *harness) record(t *testing.T, status runstate.Status, at time.Time) {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.295",
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     at,
		UpdatedAt:     at,
	}
	// A run that ended is recorded as one: what dates the silence is when it
	// started, and what stops it accounting for the quiet is that it is over.
	if status.Terminal() {
		completed := at
		state.CompletedAt = &completed
		state.Phase = runstate.PhaseComplete
	}
	if err := h.runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

// recordEnded records a run that ended at a moment of its own, later than it
// started, which is what a run that held its slot for a while looks like.
func (h *harness) recordEnded(t *testing.T, status runstate.Status, started, ended time.Time) {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	if err := h.runs.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.295",
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		Phase:         runstate.PhaseComplete,
		StartedAt:     started,
		UpdatedAt:     ended,
		CompletedAt:   &ended,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

// recordSettled records a run whose process died with its record last moving at
// quiet, ended by the harness at settled, as `yoyo reconcile` and the claim
// audit record one.
func (h *harness) recordSettled(t *testing.T, started, quiet, settled time.Time) {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	if err := h.runs.Create(runstate.State{
		SchemaVersion:     runstate.StateSchemaVersion,
		RunID:             runID,
		ProductID:         "yoyodyne",
		RepositoryID:      "yoyodyne",
		WorkItemID:        "yoyodyne-ifd.295",
		Backend:           domain.BackendClaudeCode,
		Status:            runstate.StatusFailed,
		Phase:             runstate.PhaseDeveloping,
		StartedAt:         started,
		UpdatedAt:         settled,
		CompletedAt:       &settled,
		SettledQuietSince: &quiet,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

// countedBacklog is a tracker that reports a fixed count of pullable work.
type countedBacklog struct {
	count int
}

func (b countedBacklog) Ready(context.Context) (int, error) { return b.count, nil }

// tallyBacklog answers what is ready and counts how often it was asked.
type tallyBacklog struct {
	count int
	asked int
}

func (b *tallyBacklog) Ready(context.Context) (int, error) {
	b.asked++
	return b.count, nil
}

// brokenBacklog is the tracker that will not answer at all, which is how the
// 2026-09-01 window began.
type brokenBacklog struct{}

func (brokenBacklog) Ready(context.Context) (int, error) {
	return 0, errors.New("bd: executable file not found in $PATH")
}
