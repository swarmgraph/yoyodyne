package triage

import (
	"fmt"
	"testing"
	"time"
)

// windowEntry is one stopped run on the docket, recorded a number of days
// before the moment the tests read it at.
func windowEntry(class Class, run, item string, daysAgo int) Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(class, run),
		Class:         class,
		ProductID:     "yoyodyne",
		RunID:         run,
		WorkItemID:    item,
		RecordedAt:    windowNow.AddDate(0, 0, -daysAgo),
	}
}

var windowNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func closedAmong(items ...string) func(string) bool {
	closed := map[string]bool{}
	for _, item := range items {
		closed[item] = true
	}
	return func(item string) bool { return closed[item] }
}

func stoppageKeys(stoppages []Stoppage) []string {
	keys := make([]string, 0, len(stoppages))
	for _, stoppage := range stoppages {
		keys = append(keys, stoppage.Entry.Key)
	}
	return keys
}

// The docket of 2026-09-25 in miniature: entries on closed work, a run that
// left two entries, a settled entry, and live stoppages recorded out of order.
// What the window reads is the live ones, once each, oldest stoppage first.
func TestLiveKeepsOneEntryPerLiveStoppedRunOldestFirst(t *testing.T) {
	t.Parallel()

	settled := windowEntry(ClassStoppedRun, "run-settled", "item-settled", 40)
	settled.Closed = &Closure{Decision: "rescope", ClosedAt: windowNow.AddDate(0, 0, -1)}
	repeatFirst := windowEntry(ClassStoppedRun, "run-repeated", "item-repeated", 20)
	repeatLater := windowEntry(ClassPublication, "run-repeated", "item-repeated", 3)
	entries := []Entry{
		windowEntry(ClassStoppedRun, "run-dead-1", "item-closed", 50),
		windowEntry(ClassStoppedRun, "run-new", "item-new", 7),
		repeatLater,
		settled,
		windowEntry(ClassStoppedRun, "run-oldest", "item-oldest", 36),
		repeatFirst,
		windowEntry(ClassStoppedRun, "run-dead-2", "item-closed", 2),
	}

	live := Live(entries, closedAmong("item-closed"), windowNow)

	if live.Dead != 2 || live.Settled != 1 || live.Folded != 1 {
		t.Fatalf("dead, settled, folded = %d, %d, %d, want 2, 1, 1", live.Dead, live.Settled, live.Folded)
	}
	got := stoppageKeys(live.Stoppages)
	want := []string{Key(ClassStoppedRun, "run-oldest"), repeatLater.Key, Key(ClassStoppedRun, "run-new")}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("live stoppages = %v, want %v", got, want)
	}
	// The repeated run is listed once, by the entry it stands at now, and waits
	// from when it first stopped rather than from its later entry.
	repeated := live.Stoppages[1]
	if repeated.Folded != 1 || !repeated.Since.Equal(repeatFirst.RecordedAt) {
		t.Fatalf("repeated run = folded %d since %s, want folded 1 since %s", repeated.Folded, repeated.Since, repeatFirst.RecordedAt)
	}
}

// A tracker nobody could read closes nothing: every entry stays live rather than
// being hidden on the strength of a listing that was never read.
func TestLiveTakesNothingForDeadWithoutATrackerAnswer(t *testing.T) {
	t.Parallel()

	entries := []Entry{windowEntry(ClassStoppedRun, "run-a", "item-a", 3)}
	if live := Live(entries, nil, windowNow); live.Dead != 0 || len(live.Stoppages) != 1 {
		t.Fatalf("Live(nil) = %+v, want the entry live", live)
	}
}

// The walk resumes past the position, carries on from the oldest end once it
// reaches the newest, and lets a critical jump it wherever it sits.
func TestWalkResumesPastThePositionWithCriticalsAhead(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		windowEntry(ClassStoppedRun, "run-1", "item-1", 30),
		windowEntry(ClassStoppedRun, "run-2", "item-2", 20),
		windowEntry(ClassStoppedRun, "run-3", "item-3", 10),
		windowEntry(ClassEscalation, "run-4", "item-4", 1),
		windowEntry(ClassStoppedRun, "run-5", "item-5", 5),
	}
	live := Live(entries, closedAmong(), windowNow)

	first := Walk(live.Stoppages, WindowPosition{})
	if got := stoppageKeys(first.Urgent); fmt.Sprint(got) != fmt.Sprint([]string{Key(ClassEscalation, "run-4")}) {
		t.Fatalf("urgent = %v, want the escalation", got)
	}
	if got, want := stoppageKeys(first.Next), []string{Key(ClassStoppedRun, "run-1"), Key(ClassStoppedRun, "run-2"), Key(ClassStoppedRun, "run-3"), Key(ClassStoppedRun, "run-5")}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("first walk = %v, want %v", got, want)
	}

	// A pass that showed the first two leaves the position at the second, and the
	// next pass starts with what that one did not reach.
	second := Walk(live.Stoppages, first.Next[1].At())
	if got, want := stoppageKeys(second.Next), []string{Key(ClassStoppedRun, "run-3"), Key(ClassStoppedRun, "run-5"), Key(ClassStoppedRun, "run-1"), Key(ClassStoppedRun, "run-2")}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("resumed walk = %v, want %v", got, want)
	}
}

// A decision the harness was stopped carrying out by a gate that will not clear
// on its own is critical; one waiting on a gate that clears by itself is not.
func TestCriticalIsAnEscalationOrACarryOutStoppedForGood(t *testing.T) {
	t.Parallel()

	decided := windowEntry(ClassStoppedRun, "run-a", "item-a", 3)
	decided.Closed = &Closure{Decision: "repair", ClosedAt: windowNow.AddDate(0, 0, -2)}
	decided.CarryOut = &CarryOut{Decision: "repair", Gate: "worktree", RefusedAt: windowNow.AddDate(0, 0, -1)}
	if !decided.Critical() || !decided.Undecided(windowNow) {
		t.Fatalf("a refused carry-out: critical %v undecided %v, want both", decided.Critical(), decided.Undecided(windowNow))
	}
	decided.CarryOut.Waiting = true
	if decided.Critical() {
		t.Fatal("a carry-out waiting on a gate that clears by itself was read as critical")
	}
	if plain := windowEntry(ClassStoppedRun, "run-b", "item-b", 3); plain.Critical() {
		t.Fatal("an ordinary stopped run was read as critical")
	}
}

// A stoppage whose decision is recorded comes after every one nobody has
// decided, however the decision is gated: on 2026-09-26 four such entries, each
// critical for a gate that would not clear on its own, led the window ahead of
// 29 undecided ones. Among the decided, the critical still lead.
func TestWalkListsEveryUndecidedStoppageBeforeAnyDecidedOne(t *testing.T) {
	t.Parallel()

	gated := func(run string, daysAgo int, waiting bool) Entry {
		entry := windowEntry(ClassStoppedRun, run, "item-"+run, daysAgo)
		entry.Closed = &Closure{Decision: "rerun", ClosedAt: windowNow.AddDate(0, 0, -daysAgo+1)}
		entry.CarryOut = &CarryOut{Decision: "rerun", Gate: "dependency", Waiting: waiting, RefusedAt: windowNow.Add(-time.Hour)}
		return entry
	}
	waitingOldest := gated("run-waiting", 40, true)
	refusedOld := gated("run-refused", 30, false)
	escalation := windowEntry(ClassEscalation, "run-raised", "item-raised", 1)
	older := windowEntry(ClassStoppedRun, "run-older", "item-older", 20)
	newer := windowEntry(ClassStoppedRun, "run-newer", "item-newer", 2)
	// A lapsed decision is a question again, so it is undecided.
	lapsed := windowEntry(ClassPublication, "run-lapsed", "item-lapsed", 10)
	lapsed.Closed = &Closure{Decision: "wait", ClosedAt: windowNow.AddDate(0, 0, -9), RevisitAfter: windowNow.AddDate(0, 0, -1)}

	live := Live([]Entry{waitingOldest, refusedOld, escalation, older, newer, lapsed}, closedAmong(), windowNow)
	window := Walk(live.Stoppages, WindowPosition{})

	if got, want := stoppageKeys(window.Urgent), []string{escalation.Key}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("urgent = %v, want %v", got, want)
	}
	if got, want := stoppageKeys(window.Next), []string{older.Key, lapsed.Key, newer.Key}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("walk = %v, want %v", got, want)
	}
	if got, want := stoppageKeys(window.Decided), []string{refusedOld.Key, waitingOldest.Key}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("decided = %v, want the refused carry-out ahead of the waiting one: %v", got, want)
	}
}

// The docket's build hands the window entries already folded, one per run with
// the rest beneath (Fold). Such a run waits from its first docketing, and an
// escalation folded beneath a later account of the run still jumps the walk.
func TestLiveReadsAFoldedRunFromItsFirstDocketingAndKeepsItsCritical(t *testing.T) {
	t.Parallel()

	escalation := windowEntry(ClassEscalation, "run-folded", "item-folded", 30)
	standing := windowEntry(ClassPublication, "run-folded", "item-folded", 1)
	standing.Earlier = []Entry{escalation}
	other := windowEntry(ClassStoppedRun, "run-other", "item-other", 10)

	live := Live([]Entry{other, standing}, closedAmong(), windowNow)
	if live.Folded != 0 {
		t.Fatalf("folded = %d, want 0 for a docket already folded", live.Folded)
	}
	if got, want := stoppageKeys(live.Stoppages), []string{standing.Key, other.Key}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("live stoppages = %v, want %v", got, want)
	}
	folded := live.Stoppages[0]
	if folded.Folded != 1 || !folded.Since.Equal(escalation.RecordedAt) {
		t.Fatalf("folded run = folded %d since %s, want folded 1 since %s", folded.Folded, folded.Since, escalation.RecordedAt)
	}
	if got := stoppageKeys(Walk(live.Stoppages, WindowPosition{}).Urgent); fmt.Sprint(got) != fmt.Sprint([]string{standing.Key}) {
		t.Fatalf("urgent = %v, want the run an escalation is folded beneath", got)
	}
}

// The docket of 2026-10-01: eleven stopped runs the development manager decided
// to wait on, each stopped longer ago than anything new, and three new
// stoppages. The three come first and the waits after them; once one wait runs
// out its run is undecided again, at the age it had when it first stopped.
func TestAWaitedStoppageWaitsBehindEveryUndecidedOneUntilTheWaitRunsOut(t *testing.T) {
	t.Parallel()

	var entries []Entry
	for index := range 11 {
		waited := windowEntry(ClassStoppedRun, fmt.Sprintf("run-waited-%02d", index), fmt.Sprintf("item-waited-%02d", index), 30-index)
		revisit := windowNow.AddDate(0, 0, 3)
		if index == 4 {
			revisit = windowNow.Add(time.Hour)
		}
		waited.Closed = &Closure{Decision: "wait", ClosedAt: windowNow.AddDate(0, 0, -1), RevisitAfter: revisit}
		entries = append(entries, waited)
	}
	fresh := []Entry{
		windowEntry(ClassStoppedRun, "run-new-a", "item-new-a", 3),
		windowEntry(ClassStoppedRun, "run-new-b", "item-new-b", 2),
		windowEntry(ClassStoppedRun, "run-new-c", "item-new-c", 1),
	}
	entries = append(entries, fresh...)

	live := Live(entries, closedAmong(), windowNow)
	if live.Settled != 0 || len(live.Stoppages) != 14 {
		t.Fatalf("settled %d, live %d, want 0 and every stoppage listed", live.Settled, len(live.Stoppages))
	}
	window := Walk(live.Stoppages, WindowPosition{})
	if len(window.Urgent) != 0 || len(window.Decided) != 0 {
		t.Fatalf("urgent %v, decided %v, want neither", stoppageKeys(window.Urgent), stoppageKeys(window.Decided))
	}
	if got, want := stoppageKeys(window.Next), stoppageKeys([]Stoppage{{Entry: fresh[0]}, {Entry: fresh[1]}, {Entry: fresh[2]}}); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("undecided = %v, want the three new stoppages first: %v", got, want)
	}
	if len(window.Waiting) != 11 || window.Waiting[0].Entry.Key != entries[0].Key {
		t.Fatalf("waiting = %v, want the eleven waited runs, oldest first", stoppageKeys(window.Waiting))
	}
	for _, waited := range window.Waiting {
		if !waited.Waiting || waited.Decided {
			t.Fatalf("%s: waiting %v decided %v, want a wait and not a decision waiting on the harness", waited.Entry.Key, waited.Waiting, waited.Decided)
		}
	}

	// Two hours later the fifth wait has run out, and that run is a question again
	// at the age it had: older than every new stoppage, so first of the undecided.
	later := windowNow.Add(2 * time.Hour)
	rejoined := Walk(Live(entries, closedAmong(), later).Stoppages, WindowPosition{})
	if got, want := stoppageKeys(rejoined.Next), []string{entries[4].Key, fresh[0].Key, fresh[1].Key, fresh[2].Key}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("undecided after the wait ran out = %v, want %v", got, want)
	}
	if len(rejoined.Waiting) != 10 {
		t.Fatalf("waiting after one wait ran out = %v, want the other ten", stoppageKeys(rejoined.Waiting))
	}
}

// A run is waited on only while every entry it has is: one docketed again
// beside the wait is a question.
func TestAFoldedRunIsWaitingOnlyWhileEveryEntryOfItIs(t *testing.T) {
	t.Parallel()

	waited := windowEntry(ClassPublication, "run-mixed", "item-mixed", 10)
	waited.Closed = &Closure{Decision: "wait", ClosedAt: windowNow.AddDate(0, 0, -1), RevisitAfter: windowNow.AddDate(0, 0, 2)}
	standing := windowEntry(ClassStoppedRun, "run-mixed", "item-mixed", 1)
	standing.Earlier = []Entry{waited}

	live := Live([]Entry{standing}, closedAmong(), windowNow)
	if len(live.Stoppages) != 1 || live.Stoppages[0].Waiting {
		t.Fatalf("live = %+v, want the run listed as undecided", live.Stoppages)
	}
}

// A wait on named work ends when the tracker holds that work closed, whether or
// not anything has recorded the wait's end: the stoppage is a question again,
// says which item closed, and is never read as a decision waiting on the
// harness. A wait on work still open, or on work no tracker answer covers,
// stands as it did.
func TestAWaitOnWorkTheTrackerHoldsClosedIsUndecidedAgain(t *testing.T) {
	t.Parallel()

	waitOn := func(run, item, waitsOn string) Entry {
		entry := windowEntry(ClassStoppedRun, run, item, 15)
		entry.Closed = &Closure{Decision: "wait", ClosedAt: windowNow.AddDate(0, 0, -14), WaitsOn: waitsOn}
		return entry
	}
	ended := waitOn("run-ended", "item-ended", "item-landed")
	standing := waitOn("run-standing", "item-standing", "item-still-open")
	entries := []Entry{ended, standing}

	live := Live(entries, closedAmong("item-landed"), windowNow)
	if live.Settled != 0 || live.Dead != 0 || len(live.Stoppages) != 2 {
		t.Fatalf("settled %d, dead %d, live %d; want both stoppages listed", live.Settled, live.Dead, len(live.Stoppages))
	}
	window := Walk(live.Stoppages, WindowPosition{})
	if len(window.Next) != 1 || window.Next[0].Entry.Key != ended.Key {
		t.Fatalf("undecided = %v, want the stoppage whose wait ended", stoppageKeys(window.Next))
	}
	if got := window.Next[0]; got.Waiting || got.Decided || got.WaitEnded != "item-landed" {
		t.Fatalf("ended wait: waiting %v, decided %v, wait ended on %q; want an undecided stoppage naming item-landed", got.Waiting, got.Decided, got.WaitEnded)
	}
	if len(window.Waiting) != 1 || window.Waiting[0].Entry.Key != standing.Key || window.Waiting[0].WaitEnded != "" {
		t.Fatalf("waiting = %+v, want the wait on open work still standing", window.Waiting)
	}

	// No tracker answer ends no wait.
	unasked := Walk(Live(entries, nil, windowNow).Stoppages, WindowPosition{})
	if len(unasked.Next) != 0 || len(unasked.Waiting) != 2 {
		t.Fatalf("without a tracker answer: undecided %v, waiting %v; want both still waiting", stoppageKeys(unasked.Next), stoppageKeys(unasked.Waiting))
	}
}
