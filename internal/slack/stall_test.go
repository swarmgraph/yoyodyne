package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The night this exists for, replayed: on 2026-09-07 the alarm fired at 02:48Z
// and by design said nothing more while the stall stood, so a line fully
// stopped for over four hours had produced one message, four hours old. What
// notices a stall is the checker, wherever it runs; what this does is take the
// record of it to somebody's phone — and keep taking it, louder, until it
// clears. Four silent hours replayed produce a rising sequence rather than one
// message.
func TestAStandingStallIsSaidAgainAndLouderUntilItClears(t *testing.T) {
	t.Parallel()

	fired := time.Date(2026, 9, 7, 2, 48, 0, 0, time.UTC)
	since := fired.Add(-readmodel.DefaultStallThreshold)
	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", since)

	// Nothing is in the record yet, so this surface says nothing about it however
	// quiet the product looks from here. Deciding that a quiet product has stalled
	// is not the sink's, and after yoyodyne-ifd.295 it is not the sink's to record
	// either.
	harness.now = fired
	cursors := harness.quietPass(t, harness.start())
	if events := harness.stallsRecorded(t, stalls); len(events) != 0 {
		t.Fatalf("List() = %+v, want a sink that records nothing", events)
	}

	// The checker records one, and this says it: to the operators directly and
	// tagged to them by member id, because a harness that has stopped doing
	// anything is the sharpest case there is of a degraded harness, and it is
	// theirs to act on. Ten minutes in it is a warning.
	harness.stalled(t, stalls, since, 3, harness.now)
	first := harness.stallDelivery(t, cursors)
	if !first.Direct || !first.Tag {
		t.Fatalf("direct = %t, tag = %t, want the operators told directly and tagged", first.Direct, first.Tag)
	}
	if first.Notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a young stall said as a warning", first.Notification.Event.Severity)
	}
	cursors = harness.poll(t, cursors, notify.KindStallNoticed)

	// And then the four hours, at the sink's own poll: quiet between beats, and at
	// every beat said again to the operators directly and tagged, still naming
	// whose move it is. The first repetition is the warning once more; from the
	// one after that, with two hours stood and nobody having acted on it, the
	// stall is critical.
	repetitions := 0
	var sequence []report.Severity
	for harness.now.Before(fired.Add(4 * time.Hour)) {
		for check := 0; check < 8; check++ {
			harness.now = harness.now.Add(7 * time.Minute)
			cursors = harness.quietPass(t, cursors)
		}
		harness.now = harness.now.Add(4 * time.Minute)
		again := harness.stallDelivery(t, cursors)
		if !again.Direct || !again.Tag {
			t.Fatalf("repetition %d: direct = %t, tag = %t, want the operators told directly and tagged every time", repetitions, again.Direct, again.Tag)
		}
		want := report.SeverityCritical
		if stood := harness.now.Sub(since); stood < DefaultStallEscalation {
			want = report.SeverityWarning
		}
		if again.Notification.Event.Severity != want {
			t.Fatalf("repetition %d: severity = %q, want a stall that has stood %s said as %q", repetitions, again.Notification.Event.Severity, harness.now.Sub(since), want)
		}
		said, err := notify.Render(again.Notification.Topic, again.Notification.Speaker, again.Notification.Event)
		if err != nil {
			t.Fatalf("the stall could not be said: %v", err)
		}
		if !strings.Contains(said.Body, "Next: the operator's") {
			t.Fatalf("repetition %d: body %q does not say whose move follows it", repetitions, said.Body)
		}
		cursors = harness.poll(t, cursors, notify.KindStallNoticed)
		sequence = append(sequence, again.Notification.Event.Severity)
		repetitions++
	}
	if repetitions != 4 {
		t.Fatalf("the stall was said again %d times over four hours, want once an hour", repetitions)
	}
	// The rising sequence the four silent hours produce, after the warning that
	// opened it: warning, critical, critical, critical.
	if want := []report.Severity{report.SeverityWarning, report.SeverityCritical, report.SeverityCritical, report.SeverityCritical}; !equalSeverities(sequence, want) {
		t.Fatalf("the four hours were said as %v, want %v", sequence, want)
	}
	if events := harness.stallsRecorded(t, stalls); len(events) != 1 {
		t.Fatalf("List() = %d stalls, want the one the checker recorded", len(events))
	}

	// The checker closes it, and the line goes quiet. What cleared it said so
	// itself, as the run that started.
	harness.cleared(t, stalls, "1 developer run(s) are in flight and still moving", harness.now)
	cursors = harness.quietPass(t, cursors)
	if standing := cursors.Streams[stallStream].Standing; standing != "" {
		t.Fatalf("the stall stream is still standing on %q after the stall cleared", standing)
	}
	harness.now = harness.now.Add(3 * time.Hour)
	harness.quietPass(t, cursors)
}

func equalSeverities(got, want []report.Severity) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// The repetition is on the heartbeat's clock, so an operator who set the cadence
// for the waiting line set it for this too, and a stall said as critical is
// critical from the bar this surface holds rather than from the hour.
func TestAStallEscalatesOnTheConfiguredIntervals(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.feed.Heartbeat = 20 * time.Minute
	harness.feed.StallEscalation = 50 * time.Minute
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(readmodel.DefaultStallThreshold)
	harness.stalled(t, stalls, moment, 2, harness.now)
	cursors := harness.poll(t, harness.start(), notify.KindStallNoticed)

	for _, beat := range []struct {
		at   time.Duration
		want report.Severity
	}{
		{30 * time.Minute, report.SeverityWarning},
		{50 * time.Minute, report.SeverityCritical},
		{70 * time.Minute, report.SeverityCritical},
	} {
		harness.now = moment.Add(beat.at - time.Minute)
		cursors = harness.quietPass(t, cursors)
		harness.now = moment.Add(beat.at)
		delivery := harness.stallDelivery(t, cursors)
		if delivery.Notification.Event.Severity != beat.want {
			t.Fatalf("at %s: severity = %q, want %q", beat.at, delivery.Notification.Event.Severity, beat.want)
		}
		cursors = harness.poll(t, cursors, notify.KindStallNoticed)
	}
}

// A stall that is already old when this sink first sees it is critical from the
// first word. The age is the stall's, not the sink's: a four-hour stop is not
// the same message as a twenty-minute one whoever has been listening.
func TestAStallFirstSeenOldIsCriticalAtOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(7*time.Hour + 30*time.Minute)
	harness.stalled(t, stalls, moment, 3, harness.now)
	delivery := harness.stallDelivery(t, harness.start())
	if delivery.Notification.Event.Severity != report.SeverityCritical {
		t.Fatalf("severity = %q, want a stall that has stood seven hours said as critical", delivery.Notification.Event.Severity)
	}
}

// A fresh sink over a stall that is already open says it again. This is the
// crash and the restart: the stall is the present state of the line rather than
// history the watermark reads past, and a sink that came back silent over it
// would be silent for exactly the thing it exists to say. Repetition is the
// point now, and a restart is one more.
func TestARestartedSinkSaysAStandingStallAgain(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(time.Hour)
	harness.stalled(t, stalls, moment, 2, harness.now)
	harness.poll(t, harness.start(), notify.KindStallNoticed)

	// A sink that comes back with no memory at all, on a watermark taken when it
	// came back, over the same stall still standing.
	harness.now = harness.now.Add(time.Hour)
	restarted := Cursors{SchemaVersion: CursorsSchemaVersion, Since: harness.now, Streams: map[string]Cursor{}}
	harness.now = harness.now.Add(2 * time.Hour)
	delivery := harness.stallDelivery(t, restarted)
	if !delivery.Direct || !delivery.Tag {
		t.Fatalf("direct = %t, tag = %t, want the restarted sink to take the standing stall to the operators", delivery.Direct, delivery.Tag)
	}
	events := harness.stallsRecorded(t, stalls)
	if len(events) != 1 || !events[0].Open() {
		t.Fatalf("List() = %+v, want the one stall still open and untouched", events)
	}
}

// A cursor from before the stall was said again while it stood carries the
// stall as a delivered mark meaning said once. It is read as a stall this sink
// has not yet said, so a stall standing when the harness is deployed is said
// rather than left to the mark, and the mark is dropped rather than carried for
// the life of the product.
func TestACursorFromBeforeTheRepetitionSaysTheStandingStall(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(time.Hour)
	harness.stalled(t, stalls, moment, 2, harness.now)
	events := harness.stallsRecorded(t, stalls)
	cursors := harness.start()
	cursors.Streams[stallStream] = Cursor{Delivered: []string{stallMark + events[0].EventID}}

	harness.now = harness.now.Add(time.Hour)
	delivery := harness.stallDelivery(t, cursors)
	if delivery.Cursor.Standing != stallMark+events[0].EventID {
		t.Fatalf("standing = %q, want the cursor standing on the stall", delivery.Cursor.Standing)
	}
	if mark, said := delivery.Cursor.Marked(stallMark); said {
		t.Fatalf("the cursor still carries the delivered mark %q", mark)
	}
}

// What it says is what somebody woken at three in the morning has to act on:
// how long nothing has happened, how much was waiting, and — the fact that
// decides what they do — whether the thing that chooses work is dead or is still
// claiming to be watching. Every one of those is the record's rather than this
// surface's, which is what keeps a channel and `yoyo status` telling one story.
func TestAStallSaysItsAgeItsQueueAndWhatTheChooserLastSaid(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(4)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	// The watch transition is read past first, so what the next pass says is the
	// stall and nothing else.
	harness.now = moment.Add(readmodel.DefaultStallThreshold)
	cursors := harness.poll(t, harness.start())
	harness.now = moment.Add(7*time.Hour + 30*time.Minute)
	harness.stalled(t, stalls, moment, 4, harness.now)
	said := harness.say(t, cursors, notify.KindStallNoticed)
	for _, fact := range []string{"7 hours", "4 items", "watching", "said nothing since"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
	if !strings.Contains(said.Body, "Next: the operator's") {
		t.Fatalf("body %q does not say whose move follows it", said.Body)
	}
}

// A stall that clears is not said again, and the next one is said afresh. What
// cleared it said so itself, as the run that started, so the clearing is silent
// here — and a second stall a week later is a second thing to say rather than the
// same one repeated.
func TestAClearedStallGoesQuietAndTheNextIsSaidAfresh(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(time.Hour)
	harness.stalled(t, stalls, moment, 2, harness.now)
	cursors := harness.poll(t, harness.start(), notify.KindStallNoticed)

	// The checker closes it. Nothing is said about that.
	harness.now = harness.now.Add(time.Hour)
	harness.cleared(t, stalls, "1 developer run(s) are in flight and still moving", harness.now)
	cursors = harness.quietPass(t, cursors)

	// A second stall afterwards is a second thing to say.
	harness.now = harness.now.Add(3 * time.Hour)
	harness.stalled(t, stalls, harness.now.Add(-time.Hour), 5, harness.now)
	harness.poll(t, cursors, notify.KindStallNoticed)
}

// The other window, replayed: 2026-09-05, 12:13Z to 13:43Z. The session was
// alive and polling, nothing started because the provider would not serve any
// more, and there is no stall in the record because the pause was the whole of
// the accounting. What this surface says is the cause, once, in the channel.
//
// The window is derived here rather than read from the record because the record
// holds no window: it is precisely the case where there is nothing to record.
func TestAProviderWindowIsSaidOnceAsANoteAndNeverAsAStall(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 9, 5, 12, 13, 0, 0, time.UTC)
	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", opened.Add(-time.Hour))
	harness.waitingOnProvider(t, "Paused on the provider's usage limit until 13:43Z", opened, &lifts)

	// Half an hour in — past the threshold that fired the alarm on the day — and it
	// is a note in the channel rather than a warning on somebody's phone.
	harness.now = opened.Add(30 * time.Minute)
	start := harness.start()
	delivery := harness.stallDelivery(t, start)
	if delivery.Notification.Event.Kind != notify.KindProviderWindow {
		t.Fatalf("kind = %q, want the provider's window rather than a stall", delivery.Notification.Event.Kind)
	}
	if delivery.Notification.Event.Severity != report.SeverityNote {
		t.Fatalf("severity = %q, want a note", delivery.Notification.Event.Severity)
	}
	if delivery.Direct {
		t.Fatal("the provider's window was taken to somebody directly, want it said in the channel")
	}
	said, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
	if err != nil {
		t.Fatalf("the provider's window could not be said: %v", err)
	}
	// The operator's own acceptance, stated as he stated it: the cause is the
	// first words of the message, with the time the provider named.
	if !strings.HasPrefix(said.Body, "Paused on the provider's usage limit until 13:43Z") {
		t.Fatalf("body %q does not open with the cause and the reset time", said.Body)
	}
	if !strings.Contains(said.Body, "Next: nobody's") {
		t.Fatalf("body %q does not say the window is nobody's move", said.Body)
	}
	// The session's own account of the poll stays in the watch log, so the window
	// is the whole of what the channel is told about this silence.
	cursors := harness.poll(t, start, notify.KindProviderWindow)

	// And then the ninety minutes, at the sink's own fifteen-second poll. The note
	// is not said twice and nothing is recorded.
	for harness.now.Add(5 * time.Minute).Before(lifts) {
		harness.now = harness.now.Add(5 * time.Minute)
		cursors = harness.quietPass(t, cursors)
	}
	if events := harness.stallsRecorded(t, stalls); len(events) != 0 {
		t.Fatalf("List() = %+v, want nothing recorded: the pause was the accounting", events)
	}
}

// The page of 2026-09-06, replayed. Nothing had started for an hour over a queue
// the tracker called ready, and what this message said was that nothing
// accounted for it — while the session's own idle line, one surface over, held
// the whole accounting: a third of the queue waiting on triage decisions.
//
// The operator's words are the acceptance: that message should have pointed out
// the cause. So it reads the account the poll wrote down rather than deriving
// its own from the silence, and closes on the person who releases what it names.
func TestAStallNamesTheDominantCauseAndTheNextMover(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(47)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment.Add(-time.Hour))
	harness.passedOver(t, moment, runstate.PassedOver{Admitted: 47, Groups: []runstate.PassedOverGroup{
		{Class: runstate.PassedOverHeldForAPerson, Count: 33, Items: []string{"yoyodyne-ifd.212"}},
		{Class: runstate.PassedOverCarriedInConversation, Role: domain.RoleArchitect, Count: 9},
		{Class: runstate.PassedOverSequencedBehindWork, Count: 5},
	}})

	// The two watch transitions are read past first, so what the next pass says is
	// the stall and nothing else.
	harness.now = moment.Add(readmodel.DefaultStallThreshold)
	cursors := harness.poll(t, harness.start())
	harness.now = moment.Add(time.Hour)
	harness.stalled(t, stalls, moment.Add(-time.Hour), 47, harness.now)

	said := harness.say(t, cursors, notify.KindStallNoticed)
	if !strings.Contains(said.Body, "33 of the 47 admitted items are stopped, waiting on the development manager's decision or the harness carrying it out") {
		t.Fatalf("body %q does not point out the cause", said.Body)
	}
	if !strings.Contains(said.Body, "Next: the development manager's") {
		t.Fatalf("body %q does not name the person who releases what it named", said.Body)
	}
	// And the clause it used to close on, which sent the operator to restart a
	// chooser that was running and doing exactly what it should.
	if strings.Contains(said.Body, "started again if it has died") {
		t.Fatalf("body %q still sends the reader after the chooser", said.Body)
	}
}

// An account a start overtook, which is what a session that died carrying a run
// leaves behind. Its last poll is still the newest thing a live session said, and
// the harness started something after it — so the queue it describes has not been
// read since it moved, and the message says nothing accounts for the silence and
// sends the reader to the chooser.
func TestAStallOverAnAccountAStartOvertookNamesNoCause(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(47)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment.Add(-time.Hour))
	harness.passedOver(t, moment, runstate.PassedOver{Admitted: 47, Groups: []runstate.PassedOverGroup{
		{Class: runstate.PassedOverHeldForAPerson, Count: 33, Items: []string{"yoyodyne-ifd.212"}},
	}})

	harness.now = moment.Add(readmodel.DefaultStallThreshold)
	cursors := harness.poll(t, harness.start())
	// A run started after that poll, and nothing since: the silence being reported
	// begins after the account, so the account is from before it.
	harness.now = moment.Add(2 * time.Hour)
	harness.stalled(t, stalls, moment.Add(time.Hour), 47, harness.now)

	// The hourly line has come due by now as well, so the stall is read off its own
	// stream rather than off whatever the pass said first.
	delivery := harness.stallDelivery(t, cursors)
	said, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
	if err != nil {
		t.Fatalf("the stall could not be said: %v", err)
	}
	if delivery.Notification.Event.Kind != notify.KindStallNoticed {
		t.Fatalf("kind = %q, want the stall", delivery.Notification.Event.Kind)
	}
	if strings.Contains(said.Body, "waiting on the development manager's decision or the harness carrying it out") {
		t.Fatalf("body %q states an account taken before the silence it is reporting", said.Body)
	}
	if !strings.Contains(said.Body, "Next: the operator's") {
		t.Fatalf("body %q does not send the reader to the chooser", said.Body)
	}
}

// A stall with no poll to read a cause from says what it always said. A session
// that stopped cleanly left no account of a queue, and inventing one would be
// the confident emptiness the accounting exists to remove.
func TestAStallWithNoPollToReadSaysNothingAccountsForIt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(4)
	harness.watched(t, runstate.WatchStopped, "the session was stopped", moment)

	harness.now = moment.Add(readmodel.DefaultStallThreshold)
	cursors := harness.poll(t, harness.start())
	harness.now = moment.Add(time.Hour)
	harness.stalled(t, stalls, moment, 4, harness.now)

	said := harness.say(t, cursors, notify.KindStallNoticed)
	if !strings.Contains(said.Body, "something the record does not name") {
		t.Fatalf("body %q does not state the absence of an accounting", said.Body)
	}
	if !strings.Contains(said.Body, "Next: the operator's") {
		t.Fatalf("body %q does not fall back to the chooser being looked at", said.Body)
	}
}

// One silence, one message. The heartbeat's waiting line is derived from the
// same reading and would otherwise say the window too — hourly, and with the
// cause after the fact that choosing stopped, which is the shape the operator
// asked not to be told in. So the line yields to the note above.
func TestTheHeartbeatLeavesTheProvidersWindowToTheMessageThatLeadsWithIt(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 9, 5, 12, 13, 0, 0, time.UTC)
	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	sessions := []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchWatching, At: opened.Add(-time.Hour)},
		{
			SessionID: "watch-1", State: runstate.WatchIdle, At: opened,
			ProviderWindow: true, ProviderWindowResetsAt: &lifts,
		},
	}
	if line := waitingLine(switches{}, sessions, 0, opened.Add(30*time.Minute)); line.Stopped() {
		t.Fatalf("the heartbeat line is %+v, want the window left to the note that opens with it", line)
	}
	// Once the window has lifted the session is idle for no recorded reason, and
	// that is a line the heartbeat does say.
	line := waitingLine(switches{}, sessions, 0, lifts.Add(time.Minute))
	if line.Reason != readmodel.ReasonSessionIdle {
		t.Fatalf("the heartbeat line is %+v, want an idle session once the window has lifted", line)
	}
}

// The stall record is read here and never written, which is the whole of what
// yoyodyne-ifd.295 changed about this surface. A sink that went on producing it
// would leave every product reporting nowhere with no history at all, because
// Slack reporting is optional and this process may never be started.
func TestTheSinkNeverWritesToTheStallRecord(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	// Everything a stall is made of: ready work, a session whose last word was
	// that it was watching, and hours of nothing since.
	harness.ready(5)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(9 * time.Hour)
	cursors := harness.quietPass(t, harness.start())
	harness.now = harness.now.Add(9 * time.Hour)
	harness.quietPass(t, cursors)
	if events := harness.stallsRecorded(t, stalls); len(events) != 0 {
		t.Fatalf("List() = %+v, want a surface that records nothing", events)
	}
}

// watchesForStalls gives the feed the product's own stall record, which is what
// every sink the harness builds is given, and hands it back so a test can put a
// stall in it and read what was there afterwards.
func (h *testHarness) watchesForStalls(t *testing.T) *runstate.StallStore {
	t.Helper()
	stalls, err := runstate.NewStallStore(h.root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	h.feed.Stalls = stalls
	return stalls
}

// passedOver records the idle poll that found the queue and left it where it
// was, which is the account both the session's own line and the alarm are
// rendered from.
func (h *testHarness) passedOver(t *testing.T, at time.Time, account runstate.PassedOver) {
	t.Helper()
	if err := h.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         runstate.WatchIdle,
		At:            at,
		Reason:        readmodel.IdleLine(account, 0),
		Executor:      readmodel.Carrier(account),
		PassedOver:    account,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

// stalled opens a stall in the record the way the checker does, which is the
// only way one is opened at all.
func (h *testHarness) stalled(t *testing.T, stalls *runstate.StallStore, since time.Time, ready int, at time.Time) {
	t.Helper()
	if _, err := stalls.Reconcile(runstate.StallObservation{
		Stalled: true,
		Since:   since,
		Ready:   ready,
		Chooser: "the session choosing work last recorded watching at " +
			since.UTC().Format(time.RFC3339) + ", and has said nothing since",
		At: at,
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}

// cleared closes the standing stall, saying what accounted for it.
func (h *testHarness) cleared(t *testing.T, stalls *runstate.StallStore, explains string, at time.Time) {
	t.Helper()
	if _, err := stalls.Reconcile(runstate.StallObservation{Explains: explains, At: at}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}

func (h *testHarness) stallsRecorded(t *testing.T, stalls *runstate.StallStore) []runstate.StallEvent {
	t.Helper()
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	return events
}

// quietPass makes one pass, allows it to say whatever else the records call for,
// and fails only if it said this product had gone quiet.
func (h *testHarness) quietPass(t *testing.T, cursors Cursors) Cursors {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	advanced := Cursors{SchemaVersion: CursorsSchemaVersion, Since: cursors.Since, Streams: map[string]Cursor{}}
	for stream, cursor := range cursors.Streams {
		advanced.Streams[stream] = cursor
	}
	for _, delivery := range batch.Deliveries {
		advanced.Streams[delivery.Stream] = delivery.Cursor
		if !delivery.Silent() && delivery.Notification.Event.Kind == notify.KindStallNoticed {
			t.Fatal("this product was said to have gone quiet, want nothing said about it")
		}
	}
	return advanced
}

// stallDelivery makes one pass and returns the delivery the stall stream
// produced, which is where a test reads the parts a rendered message does not
// carry: whether the operators were told directly at all.
func (h *testHarness) stallDelivery(t *testing.T, cursors Cursors) Delivery {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == stallStream && !delivery.Silent() {
			return delivery
		}
	}
	t.Fatal("nothing was said about this product having gone quiet")
	return Delivery{}
}
