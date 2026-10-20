package slack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The overnight this exists for: intake held at two minutes past midnight, a
// watch session that reached its budget and stopped, and ten hours in which the
// channel could not be told from a broken one. Both of those said themselves
// once, as they happened. What was missing is the hour after that.
func TestAHeldLineWithReadyWorkSaysSoAgainWhileItStands(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.hold(t, "the harness held intake after runs kept blocking", moment)

	// The first sighting arms the clock and says nothing: the hold said itself
	// when it was placed, and repeating it a poll later would be the sink saying
	// what the channel already has.
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	harness.now = harness.now.Add(59 * time.Minute)
	cursors = harness.poll(t, cursors)

	harness.now = harness.now.Add(2 * time.Minute)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)
	// And again the hour after that, for as long as it stands.
	harness.now = harness.now.Add(time.Hour)
	harness.poll(t, cursors, notify.KindLineWaiting)
}

// The hold of 2026-09-19, replayed: the brake tripped at 17:56Z and held intake
// for nearly two hours with a free developer slot idle, and nothing reached the
// operator. A brake hold that waits on a person is a stopped line, and the line
// gets louder as it stands rather than repeating at the same pitch: tagged to
// the operators every hour, a warning while it is young, and critical and taken
// to them directly once it has stood past the bar — until intake is released.
func TestABrakeHoldWaitingOnAPersonGetsLouderAndTagsTheOperator(t *testing.T) {
	t.Parallel()

	tripped := time.Date(2026, 9, 19, 17, 56, 0, 0, time.UTC)
	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", tripped)
	// A brake hold written before the brake summoned anybody, which waits on a
	// person exactly as it always did.
	if _, err := harness.intake.Hold(runstate.IntakeHolderBrake, "3 runs blocked in a row", tripped); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	harness.now = tripped
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)

	harness.now = tripped.Add(time.Hour)
	first := harness.line(t, cursors)
	if !first.Tag {
		t.Fatal("the held line was posted without naming the operators, want them tagged")
	}
	if first.Direct {
		t.Fatal("a hold an hour old was taken to the operators directly, want the channel with them tagged")
	}
	if first.Notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a young brake hold said as a warning", first.Notification.Event.Severity)
	}
	said, err := notify.Render(first.Notification.Topic, first.Notification.Speaker, first.Notification.Event)
	if err != nil {
		t.Fatalf("the line could not be said: %v", err)
	}
	if !strings.Contains(said.Body, "Next: the development manager's") || !strings.Contains(said.Body, "yoyo release") {
		t.Fatalf("body %q does not name the development manager's move and what lifts it", said.Body)
	}
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)

	// The second hour, and every hour after it: critical, tagged, and to them
	// directly as well.
	for hour := 2; hour <= 4; hour++ {
		harness.now = tripped.Add(time.Duration(hour) * time.Hour)
		again := harness.line(t, cursors)
		if !again.Tag || !again.Direct {
			t.Fatalf("hour %d: tag = %t, direct = %t, want the operators tagged and told directly", hour, again.Tag, again.Direct)
		}
		if again.Notification.Event.Severity != report.SeverityCritical {
			t.Fatalf("hour %d: severity = %q, want a standing brake hold said as critical", hour, again.Notification.Event.Severity)
		}
		cursors = harness.poll(t, cursors, notify.KindLineWaiting)
	}

	// Released, and the hold is no longer what the line says. What it says next —
	// the stopped session over ready work — is the ordinary hourly note, tagged
	// to nobody: the tagging was the brake hold's and went with it.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	harness.now = harness.now.Add(3 * time.Hour)
	after := harness.line(t, cursors)
	if after.Tag || after.Direct || after.Notification.Event.Severity != report.SeverityNote {
		t.Fatalf("tag = %t, direct = %t, severity = %q after the release, want the ordinary note", after.Tag, after.Direct, after.Notification.Event.Severity)
	}
}

// A brake hold the development manager has escalated is the other hold that
// waits on the operator, and it is said the same way, naming her escalation as
// what makes it his.
func TestAnEscalatedBrakeHoldIsTaggedToTheOperator(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.braked(t, moment)
	if _, err := harness.intake.DecideBrake(runstate.BrakeDecisionEscalate, "the checks fail on main", "chat-1", moment.Add(5*time.Minute)); err != nil {
		t.Fatalf("DecideBrake() error = %v", err)
	}

	harness.now = moment.Add(10 * time.Minute)
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	harness.now = moment.Add(time.Hour + 10*time.Minute)
	delivery := harness.line(t, cursors)
	if !delivery.Tag || delivery.Notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("tag = %t, severity = %q, want the operators tagged at warning", delivery.Tag, delivery.Notification.Event.Severity)
	}
	said, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
	if err != nil {
		t.Fatalf("the line could not be said: %v", err)
	}
	if !strings.Contains(said.Body, "Next: the Lead Product Manager's — the development manager escalated it") {
		t.Fatalf("body %q does not say the hold is the Lead Product Manager's by her escalation", said.Body)
	}
}

// The other way a brake hold becomes the operator's: the harness's own
// summons-and-probe loop went round its configured number of times with the
// development manager not escalating it, and the harness escalated it itself.
// That is said once, the moment the record shows it, to the operators directly
// and tagged by member id, naming the cycles spent and what stopped the last
// probe — and never again on a later pass, because the hourly line carries the
// hold from there, tagged as any hold that waits on him is.
func TestTheHarnessEscalatingABrakeHoldIsSaidOnceDirectlyToTheOperator(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.braked(t, moment)
	harness.now = moment.Add(10 * time.Minute)
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)

	// The loop going round, as the scheduler records it: two cycles spent of a
	// bound of two, and the last probe blocked.
	escalated := moment.Add(2 * time.Hour)
	if _, err := harness.intake.ReviseBrake(func(trip *runstate.IntakeBrake) error {
		ended := escalated
		trip.Probe = &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.405", RunID: "run-5", StartedAt: escalated.Add(-20 * time.Minute), EndedAt: &ended, Blocked: true, Reason: "the checks failed on main"}
		trip.Probes, trip.Cycles, trip.CycleBound = 2, 2, 2
		trip.Escalation = &runstate.BrakeEscalation{At: escalated, Cycles: 2, Probe: "yoyodyne-ifd.405", Reason: "the checks failed on main"}
		return nil
	}); err != nil {
		t.Fatalf("ReviseBrake() error = %v", err)
	}

	harness.now = escalated.Add(time.Minute)
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	var said []Delivery
	for _, delivery := range batch.Deliveries {
		cursors.Streams[delivery.Stream] = delivery.Cursor
		if delivery.Posts() && delivery.Notification.Event.Kind == notify.KindIntakeEscalated {
			said = append(said, delivery)
		}
	}
	if len(said) != 1 {
		t.Fatalf("said the escalation %d time(s) on the pass that first saw it, want once", len(said))
	}
	message := said[0]
	if !message.Direct || !message.Tag {
		t.Fatalf("direct = %t, tag = %t, want the escalation sent to the operators directly and tagged to them", message.Direct, message.Tag)
	}
	if message.Notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a hold handed to a person said as a warning", message.Notification.Event.Severity)
	}
	if !message.Notification.Event.At.Equal(escalated) {
		t.Fatalf("at = %s, want the moment of the escalation %s", message.Notification.Event.At, escalated)
	}
	rendered, err := notify.Render(message.Notification.Topic, message.Notification.Speaker, message.Notification.Event)
	if err != nil {
		t.Fatalf("the escalation could not be said: %v", err)
	}
	for _, fact := range []string{"escalated to the Lead Product Manager by the harness, and you are told", "2 summons-and-probe cycles", "yoyodyne-ifd.405", "the checks failed on main", "Next: the Lead Product Manager's — the harness has stopped probing", "yoyo release"} {
		if !strings.Contains(rendered.Body, fact) {
			t.Fatalf("body %q does not carry %q", rendered.Body, fact)
		}
	}

	// A later pass says nothing more about the escalation: the hourly line is
	// what carries the hold now, tagged to him as any hold that waits on him is.
	harness.now = escalated.Add(time.Hour + 2*time.Minute)
	line := harness.line(t, cursors)
	if line.Notification.Event.Kind != notify.KindLineWaiting || !line.Tag {
		t.Fatalf("kind = %q, tag = %t, want the hourly line tagged to the operators", line.Notification.Event.Kind, line.Tag)
	}
	body, err := notify.Render(line.Notification.Topic, line.Notification.Speaker, line.Notification.Event)
	if err != nil {
		t.Fatalf("the line could not be said: %v", err)
	}
	if !strings.Contains(body.Body, "Next: the Lead Product Manager's — the harness escalated it after 2 summons-and-probe cycles") {
		t.Fatalf("body %q does not say the hold is the Lead Product Manager's by the harness's escalation", body.Body)
	}
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)
	harness.now = harness.now.Add(time.Hour)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)

	// Released, and the mark goes with the hold: nothing about the escalation is
	// left in the cursor to be said over the next hold.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	if mark, said := cursors.Streams[productStream].Marked(brakeEscalationMark); said {
		t.Fatalf("the cursor still carries %q after the hold lifted", mark)
	}
}

// While the harness is still working the hold the hourly line names the loop it
// is in — which cycle, and at what cycle the harness stops asking — so a note
// that repeats every hour through a night says how much longer it goes on.
func TestTheHourlyLineNamesTheLoopWhileTheHarnessWorksTheHold(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.braked(t, moment)
	if _, err := harness.intake.ReviseBrake(func(trip *runstate.IntakeBrake) error {
		trip.Cycles, trip.CycleBound = 1, 4
		return nil
	}); err != nil {
		t.Fatalf("ReviseBrake() error = %v", err)
	}

	harness.now = moment.Add(10 * time.Minute)
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	harness.now = moment.Add(2 * time.Hour)
	delivery := harness.line(t, cursors)
	if delivery.Tag || delivery.Direct {
		t.Fatalf("tag = %t, direct = %t, want a hold the harness is still working said in the channel alone", delivery.Tag, delivery.Direct)
	}
	said, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
	if err != nil {
		t.Fatalf("the line could not be said: %v", err)
	}
	if !strings.Contains(said.Body, "summons-and-probe cycle 2 of at most 4") || !strings.Contains(said.Body, "escalates it to the Lead Product Manager itself after 4 probes blocked") {
		t.Fatalf("body %q does not name the loop and its bound", said.Body)
	}
}

// A brake hold the harness is working itself asks a person for nothing, and the
// line says so at the pitch it always had: an hourly note, tagged to nobody,
// naming the development manager's move. She is summoned about it separately,
// and tagging the operator over a hold that is not his is the nagging that gets
// a channel muted. The operator's own hold is a state he chose to sit with, and
// is said the same way.
func TestAHoldThatIsNotTheOperatorsIsNotTaggedToHim(t *testing.T) {
	t.Parallel()

	for _, held := range []struct {
		name  string
		place func(t *testing.T, harness *testHarness)
		mover string
	}{
		{"the brake's, with the development manager deciding", func(t *testing.T, harness *testHarness) {
			harness.braked(t, moment)
		}, "Next: the development manager's"},
		{"the operator's own", func(t *testing.T, harness *testHarness) {
			harness.hold(t, "reordering the backlog first", moment)
		}, "Next: the operator's — nothing new is chosen until `yoyo release` lifts it"},
	} {
		t.Run(held.name, func(t *testing.T) {
			t.Parallel()

			harness := newTestHarness(t, time.Time{})
			harness.ready(2)
			harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
			held.place(t, harness)

			cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
			harness.now = moment.Add(5 * time.Hour)
			delivery := harness.line(t, cursors)
			if delivery.Tag || delivery.Direct {
				t.Fatalf("tag = %t, direct = %t, want a hold that is not the operator's said in the channel alone", delivery.Tag, delivery.Direct)
			}
			if delivery.Notification.Event.Severity != report.SeverityNote {
				t.Fatalf("severity = %q, want a note", delivery.Notification.Event.Severity)
			}
			said, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
			if err != nil {
				t.Fatalf("the line could not be said: %v", err)
			}
			if !strings.Contains(said.Body, held.mover) {
				t.Fatalf("body %q does not name the hold's own move %q", said.Body, held.mover)
			}
		})
	}
}

// braked places the brake's hold with its own record attached, the way the
// poll that trips it does: the runs that blocked, and a cooldown after which a
// probe starts by itself.
func (h *testHarness) braked(t *testing.T, at time.Time) {
	t.Helper()
	if _, err := h.intake.Brake(runstate.IntakeBrake{
		Blocked: []runstate.BrakeBlockedRun{
			{WorkItemID: "yoyodyne-ifd.398", RunID: "run-1", Reason: "the reviewer returned it"},
			{WorkItemID: "yoyodyne-ifd.401", RunID: "run-2", Reason: "the checks failed"},
			{WorkItemID: "yoyodyne-ifd.402", RunID: "run-3", Reason: "the merge was refused"},
		},
		CooldownEndsAt: at.Add(30 * time.Minute),
	}, "3 runs blocked in a row", at); err != nil {
		t.Fatalf("Brake() error = %v", err)
	}
}

// line makes one pass and returns the delivery the heartbeat stream produced,
// which is where a test reads the parts a rendered message does not carry:
// whether the operators were tagged or told directly.
func (h *testHarness) line(t *testing.T, cursors Cursors) Delivery {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == heartbeatStream && !delivery.Silent() {
			return delivery
		}
	}
	t.Fatal("nothing was said about the line")
	return Delivery{}
}

// What it says is what somebody woken by it has to act on: which state, how long
// it has stood, and how much work is waiting behind it.
func TestTheHeartbeatNamesTheStateItsAgeAndWhatIsWaiting(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(4)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	held := harness.now
	harness.hold(t, "reordering the backlog first", held)

	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	harness.now = held.Add(10 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	for _, fact := range []string{"intake is held", "reordering the backlog first", "10 hours", "4 items"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
}

// A line nobody is holding, with a session choosing work, is not waiting on
// anybody. The heartbeat stops the moment the state clears, and it says nothing
// about the clearing: what cleared it said so itself.
func TestTheHeartbeatStopsWhenTheStateClears(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.hold(t, "looking at something first", moment)

	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	harness.now = harness.now.Add(time.Hour)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)

	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	if standing := cursors.Streams[heartbeatStream].Standing; standing != "" {
		t.Fatalf("the heartbeat is still standing on %q after the state cleared", standing)
	}
	harness.now = harness.now.Add(4 * time.Hour)
	harness.poll(t, cursors)
}

// The other half of the rule, and the half that makes the channel readable: an
// idle line with nothing ready is not waiting on anybody, so it stays exactly as
// silent as it was.
func TestAnIdleLineWithNothingReadyStaysSilent(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(0)
	harness.watched(t, runstate.WatchIdle, "the backlog is empty", moment)

	cursors := harness.poll(t, harness.start())
	for hour := 0; hour < 5; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
}

// The other thing a quiet line is worth saying over. A promotion the forge has
// not published is work already paid for that is sitting nowhere a person would
// see it, and the message that said it — the drop — is said once as it happens.
// A reader who was away for that one has nothing else that would ever tell them,
// so the count comes back on the next quiet tick and every one after it.
func TestAPromotionWaitingOnTheForgeIsCountedOnEveryQuietTick(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	// Nothing at all is ready to pull: before this, that alone kept the line
	// silent, and the outstanding publication with it.
	harness.ready(0)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.record(t, harness.awaitingForge(t))

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindChecksPassed,
		notify.KindReviewApproved, notify.KindPromoted, notify.KindPublished,
		notify.KindMergeQueued)
	harness.now = harness.now.Add(time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	for _, fact := range []string{"no items ready to pull", "one promotion awaiting the forge"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
}

// And it stops the moment the publication is settled, because silence has to go
// on meaning nothing to do. A merged publication is nobody's move.
func TestTheCountStopsWhenThePublicationIsSettled(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(0)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	waiting := harness.awaitingForge(t)
	harness.record(t, waiting)

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindChecksPassed,
		notify.KindReviewApproved, notify.KindPromoted, notify.KindPublished,
		notify.KindMergeQueued)
	harness.now = harness.now.Add(time.Hour)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)

	merged := *waiting.PullRequest
	merged.MergeQueued = false
	merged.Merged = true
	waiting.PullRequest = &merged
	harness.save(t, waiting)

	cursors = harness.poll(t, cursors, notify.KindMergeCompleted)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// The case this was written for, replayed end to end: a run whose merge the
// forge queued, a drop nothing was watching for, and a channel that said
// nothing about either for four hours. Now the drop is a warning as it happens,
// and the count says it again while the publication stands.
func TestADroppedMergeIsAnnouncedAtDropTimeAndCountedUntilItIsSettled(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(0)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	queued := harness.awaitingForge(t)
	harness.record(t, queued)
	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindChecksPassed,
		notify.KindReviewApproved, notify.KindPromoted, notify.KindPublished,
		notify.KindMergeQueued)

	// The forge gives up on it, and reconciliation records the moment.
	dropped := queued
	request := *queued.PullRequest
	request.MergeQueued = false
	dropped.PullRequest = &request
	dropped.PublishFailure = "the forge dropped the queued merge of pull request 84: it is open and has no merge queued for it"
	dropped.MergeDrop = &runstate.MergeDrop{At: harness.now, Reason: dropped.PublishFailure}
	harness.save(t, dropped)

	said := harness.say(t, cursors, notify.KindMergeDropped)
	if said.Severity != report.SeverityWarning {
		t.Fatalf("the drop is said at %q, want a warning: nobody chose it", said.Severity)
	}
	if !broadcast(said.Reach) {
		t.Fatalf("a drop that reaches %q does not reach the channel", said.Reach)
	}
	cursors = harness.poll(t, cursors, notify.KindMergeDropped)

	// And for as long as nobody settles it, the hourly line carries the count —
	// which is what a reader who was not looking at four in the morning gets.
	for hour := 0; hour < 2; hour++ {
		harness.now = harness.now.Add(time.Hour)
		beat := harness.say(t, cursors, notify.KindLineWaiting)
		if !strings.Contains(beat.Body, "one promotion awaiting the forge") {
			t.Fatalf("body %q does not carry the outstanding publication", beat.Body)
		}
		cursors = harness.poll(t, cursors, notify.KindLineWaiting)
	}
}

// awaitingForge is a run that did everything right and is waiting on the forge:
// promoted, published, and with the merge the forge accepted still queued. It is
// the state the four silent hours were spent in.
func (h *testHarness) awaitingForge(t *testing.T) runstate.State {
	t.Helper()
	state := h.run(t, runstate.StatusSucceeded)
	state.Phase = runstate.PhaseCleaningUp
	state.WorktreePath = "/tmp/worktrees/" + state.RunID
	state.Branch = "yoyodyne/ifd-68-3"
	state.BaseCommit = strings.Repeat("d", 40)
	state.ReviewDecision = runstate.ReviewApprove
	state.ReviewRounds = 1
	state.ProviderSessionID = "developer-session"
	state.ProviderModel = "opus"
	state.ReviewSessionID = "reviewer-session"
	state.ReviewModel = "opus"
	state.Integration = &runstate.Integration{
		TargetBranch:         "main",
		SourceCommit:         strings.Repeat("c", 40),
		TargetCommit:         strings.Repeat("c", 40),
		PreviousTargetCommit: strings.Repeat("d", 40),
	}
	state.PullRequest = &runstate.PullRequest{
		Remote:      "origin",
		Branch:      state.Branch,
		Number:      84,
		URL:         "https://example.test/pull/84",
		HeadCommit:  strings.Repeat("c", 40),
		MergeMethod: "merge",
		MergeQueued: true,
	}
	return state
}

// A product nobody has ever watched has no line that stopped. An operator who
// runs items by name has a queue by choice, and an hourly message about it is
// the nagging this is written not to be.
func TestAProductNobodyHasWatchedIsNotAStalledLine(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(6)

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(3 * time.Hour)
	harness.poll(t, cursors)
}

// Work visibly moving is not a stalled line whatever else is true, and a message
// saying nothing is being chosen while a run posts its way through a review is
// false in the way that teaches people to stop reading.
func TestALineWithARunInFlightIsNotWaiting(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.record(t, harness.run(t, runstate.StatusRunning))

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// A held intake is the one state said over a run in flight. The run was already
// going when the line stopped; nothing new is chosen behind it, and a free slot
// idle beside it is exactly the shape the brake trip of 2026-09-19 stood in for
// two hours with the heartbeat silent. So the heartbeat says intake is held
// while it is, whatever is in flight.
func TestAHeldIntakeIsSaidAgainOverARunInFlight(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchBraked, "the harness's own brake placed it after 3 run(s) blocked in a row", moment)
	harness.record(t, harness.run(t, runstate.StatusRunning))
	harness.hold(t, "3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3", moment)

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindWatchBraked, notify.KindIntakeHeld)
	harness.now = harness.now.Add(2 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	if !strings.Contains(said.Body, "intake is held") {
		t.Fatalf("body %q does not say intake is held", said.Body)
	}
}

// A tracker that will not answer leaves the sink unable to tell a line waiting on
// somebody from an honestly quiet one. It must not guess in either direction: it
// says so where it says everything else about itself, and asks again at the next
// interval rather than at the next poll.
func TestATrackerThatCannotBeReadIsSaidRatherThanGuessedAt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.feed.Backlog = brokenBacklog{}
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(time.Hour)
	cursors = harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v about a tracker it could not read, want it said once", said)
	}
	// The clock is reset rather than left where it was, so the next attempt is at
	// the next interval rather than fifteen seconds later.
	harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v, want the retry held to the heartbeat interval", said)
	}
}

// The ask put to the operators is the same reading as the line said in the
// channel: it is produced beside the message and never without it, it names the
// state by the mark the cursor stands on, and what stopped the line is the read
// model's own sentence rather than one this surface composed. The options are
// this surface's, keyed by the read model's reason.
func TestTheAskIsDerivedBesideTheChannelLineFromTheReadModelsState(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(4)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.hold(t, "reordering the backlog first", moment)

	// Arming says nothing and asks nobody: the hold said itself when it was
	// placed.
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	if batch := harness.batch(t, cursors); batch.Asking != nil {
		t.Fatalf("asking = %+v on the pass that armed the state, want nobody asked before the line is said", batch.Asking)
	}

	harness.now = harness.now.Add(2 * time.Hour)
	batch := harness.batch(t, cursors)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	if batch.Asking == nil {
		t.Fatal("asking = nil on the pass that said the line is waiting, want the operators asked beside it")
	}
	if batch.Asking.Mark != cursors.Streams[heartbeatStream].Standing {
		t.Fatalf("asking about %q, want the mark the cursor stands on, %q", batch.Asking.Mark, cursors.Streams[heartbeatStream].Standing)
	}
	if !strings.Contains(said.Body, batch.Asking.Stopped) {
		t.Fatalf("the channel said %q, want it to carry what the ask says stopped the line, %q", said.Body, batch.Asking.Stopped)
	}
	if !strings.Contains(batch.Asking.Stopped, "reordering the backlog first") {
		t.Fatalf("asking.Stopped = %q, want the read model's own account of the hold", batch.Asking.Stopped)
	}
	if batch.Asking.Ready != 4 || !batch.Asking.Since.Equal(moment) {
		t.Fatalf("asking = %+v, want what is waiting and since when carried as the line says them", batch.Asking)
	}
	if want := options(readmodel.ReasonIntakeHold); len(batch.Asking.Options) != len(want) || batch.Asking.Options[0] != want[0] {
		t.Fatalf("asking.Options = %v, want the answers for a held intake", batch.Asking.Options)
	}

	// The state clears, and with it the ask: a line that is moving is not
	// waiting on anybody.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if batch := harness.batch(t, cursors); batch.Asking != nil {
		t.Fatalf("asking = %+v after the state cleared, want nobody asked about a line that is moving", batch.Asking)
	}
}

// A line that is idle with nothing ready is not waiting on anybody, so nobody is
// asked about it — the healthy quiet is quiet in the direct messages too.
func TestAnIdleLineWithNothingReadyAsksNobody(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(0)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(2 * time.Hour)
	if batch := harness.batch(t, cursors); batch.Asking != nil {
		t.Fatalf("asking = %+v over an idle line with nothing ready, want nobody asked", batch.Asking)
	}
}

// A promotion waiting on the forge makes the channel line worth saying over an
// empty queue, and it is the channel's alone: nothing is choosing nothing over
// ready work, so there is no decision to put to anybody, and an ask offering to
// release intake over an empty queue would be a question about the wrong thing.
func TestAPromotionWaitingOnTheForgeOverAnEmptyQueueAsksNobody(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(0)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.record(t, harness.awaitingForge(t))

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindChecksPassed,
		notify.KindReviewApproved, notify.KindPromoted, notify.KindPublished,
		notify.KindMergeQueued)
	harness.now = harness.now.Add(time.Hour)
	batch := harness.batch(t, cursors)
	harness.say(t, cursors, notify.KindLineWaiting)
	if batch.Asking != nil {
		t.Fatalf("asking = %+v beside a line said for the forge alone, want nobody asked over an empty queue", batch.Asking)
	}
}

// Every reason the heartbeat repeats has answers to offer, so an ask is never a
// question with nothing numbered under it for want of somebody adding a case.
func TestEveryStateTheHeartbeatSaysOffersOptions(t *testing.T) {
	t.Parallel()

	for _, reason := range readmodel.Reasons() {
		// A run in flight and the provider's usage window are never said by this
		// surface, a provider answering nobody is said once in its own message
		// rather than by the heartbeat, a product nobody watched is not a
		// stopped line, and a session restarting into a deployed build is said by
		// its own restart message rather than repeated here; the rest are.
		switch reason {
		case readmodel.ReasonNoCapacity, readmodel.ReasonProviderWindow, readmodel.ReasonProviderAway, readmodel.ReasonUnwatched, readmodel.ReasonRedeploying:
			continue
		}
		if len(options(reason)) < 2 {
			t.Fatalf("options(%q) = %v, want at least two answers to offer", reason, options(reason))
		}
	}
}

// batch makes one pass and returns the whole of what the feed produced, which is
// where a test reads the ask beside the deliveries.
func (h *testHarness) batch(t *testing.T, cursors Cursors) Batch {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	return batch
}

// A different state is a different thing to do something about, so it is armed
// afresh rather than inheriting a clock that has already run out — the sink says
// the state that stands now, once it has stood for an interval, rather than the
// moment it replaces one that had.
func TestANewStateIsArmedRatherThanInheritingTheLastOnesClock(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(2 * time.Hour)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)

	// The operator holds intake, which is now what has stopped the line.
	harness.hold(t, "looking at the queue", harness.now)
	cursors = harness.poll(t, cursors, notify.KindIntakeHeld)
	harness.now = harness.now.Add(30 * time.Minute)
	cursors = harness.poll(t, cursors)
	harness.now = harness.now.Add(31 * time.Minute)
	harness.poll(t, cursors, notify.KindLineWaiting)
}

// One log holds every session a product has had, and nothing stops two running
// at once. A session ending while another carries on watching is the last line of
// the log saying "stopped" over a line that is still being pulled from, so the
// sessions are read one at a time rather than off the end of the log.
func TestASessionEndingBesideOneStillWatchingIsNotAStoppedLine(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.watchedAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchWatching, "watching the backlog until stopped", moment.Add(time.Minute))
	harness.watchedAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchStopped, "the session spent the budget it was given", moment.Add(2*time.Minute))

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(3 * time.Hour)
	cursors = harness.poll(t, cursors)

	// Once the session that was still watching ends too, nobody is choosing.
	harness.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", runstate.WatchStopped, "the scheduler was cancelled", harness.now)
	cursors = harness.poll(t, cursors)
	harness.now = harness.now.Add(time.Hour)
	harness.poll(t, cursors, notify.KindLineWaiting)
}

// The night this exists for, replayed. The repair-handback fix merged the day
// before, the watch session's binary predated it, and three granted repair rounds
// executed against clean worktrees and delivered empty diffs. Every one of those
// rounds was a run in flight, so the waiting heartbeat above was correctly silent
// through the whole of it: the session's age is what nothing was saying.
//
// It is said the first time it is seen rather than armed silently, because unlike
// a hold there is no record that said it as it happened — and being told after
// the first round has been spent is being told too late.
func TestAStaleResidentSaysSoWhileTheRoundsAreBeingSpent(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.deployed(3)
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)
	harness.record(t, harness.run(t, runstate.StatusRunning))

	cursors := harness.poll(t, harness.start(),
		notify.KindRunStarted, notify.KindResidentStale)
	// The repository is asked at the cadence rather than at every poll, so a
	// fifteen-second pass a moment later spawns nothing and says nothing.
	harness.now = harness.now.Add(30 * time.Minute)
	cursors = harness.poll(t, cursors)
	// And again the hour after that, for as long as the session runs that binary.
	harness.now = harness.now.Add(31 * time.Minute)
	harness.poll(t, cursors, notify.KindResidentStale)
}

// What it says is what somebody has to act on: how far behind the session is,
// which build it is on, and what closes the gap — installing the build, which
// the session then takes up itself rather than waiting to be restarted.
func TestTheResidentLineNamesTheCountAndTheWayOutOfIt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(7)
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start(), notify.KindResidentStale)
	harness.now = harness.now.Add(time.Hour)
	said := harness.say(t, cursors, notify.KindResidentStale)
	for _, fact := range []string{"7 harness changes", staleResidentBuild[:12], "restarts itself", "installing the build"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
}

// A session running what is deployed is the ordinary state, and silence has to
// keep meaning nothing to do. The clock is still reset, so the repository is read
// once an hour rather than every fifteen seconds on a machine that is behaving.
func TestASessionRunningWhatIsDeployedStaysSilent(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.deployed(0)
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start())
	for hour := 0; hour < 4; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
}

// Far enough past the threshold the session has missed too much for what it is
// doing to be trusted to be about the work, so the operators are told where they
// will see it rather than in a channel nobody is reading at three in the morning.
// Once, because the hourly line is already carrying the state and a second
// channel repeating it is a channel somebody mutes.
func TestCrossingTheThresholdReachesTheOperatorsOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(4)
	harness.feed.StaleBuildThreshold = 4
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	crossed := harness.resident(t, harness.start())
	if !crossed.Direct {
		t.Fatal("the threshold was crossed and nothing was said to the operators directly")
	}
	if severity := crossed.Notification.Event.Severity; severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a degraded harness said louder than a note", severity)
	}
	cursors := harness.start()
	cursors.Streams[residentStream] = crossed.Cursor

	harness.now = harness.now.Add(time.Hour)
	again := harness.resident(t, cursors)
	if again.Direct {
		t.Fatal("the operators were told a second time about the same build")
	}
	if again.Notification.Event.Kind != notify.KindResidentStale {
		t.Fatalf("said %q, want the channel to keep carrying the state", again.Notification.Event.Kind)
	}
}

// Below the threshold this is worth reading beside the heartbeat and not worth
// interrupting somebody for.
func TestAResidentShortOfTheThresholdIsSaidInTheChannelAlone(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(3)
	harness.feed.StaleBuildThreshold = 4
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	said := harness.resident(t, harness.start())
	if said.Direct {
		t.Fatal("the operators were interrupted for a session that is three changes behind")
	}
	if severity := said.Notification.Event.Severity; severity != report.SeverityNote {
		t.Fatalf("severity = %q, want a note", severity)
	}
}

// A session restarted onto a binary that is still behind is a different build,
// and being told about the last one is not being told about this one. The
// escalation is remembered per build so it re-arms rather than being swallowed by
// a mark about a binary nobody is running any more.
func TestARestartOntoAStillStaleBuildIsEscalatedAfresh(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(9)
	harness.feed.StaleBuildThreshold = 2
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.start()
	cursors.Streams[residentStream] = harness.resident(t, cursors).Cursor

	// The operator restarts, and what comes back is newer and still not current.
	harness.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", runstate.WatchStopped, "the operator stopped it", harness.now)
	harness.now = harness.now.Add(time.Minute)
	harness.watchedBuildAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchWatching,
		"watching the backlog until stopped", harness.now, newerResidentBuild)

	restarted := harness.resident(t, cursors)
	if !restarted.Direct {
		t.Fatal("a restart onto a binary that is still behind said nothing to the operators")
	}
}

// A session that has stopped is not a resident, and one whose binary recorded no
// revision is a comparison nobody can make. Neither is worth an hourly message:
// the first is already said where sessions are said, and the second is a fact
// about how somebody built the binary rather than news about this product.
func TestASessionWithNothingToCompareIsNotReportedAsStale(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.deployed(40)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(2 * time.Hour)
	cursors = harness.poll(t, cursors)

	// A session that did record one, and then ended.
	harness.watchedBuildAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchStopped,
		"the session spent the budget it was given", harness.now, staleResidentBuild)
	cursors = harness.poll(t, cursors)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// The runs in flight are the second record that says which harness is
// dispatching work, and they are read when the watch log does not say. That is
// the shape the field cases had: the session choosing work stamped nothing at
// all — watch.jsonl only began carrying a build on 2026-08-30 — while every run
// it reserved was made by that same binary and now says so. Without this the
// stale resident is silent exactly where it is spending rounds.
func TestARunInFlightNamesTheBuildWhenTheSessionDoesNot(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.deployed(31)
	// A session that is alive and says nothing about its binary, which is every
	// session recorded before the stamping existed.
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	dispatched := harness.run(t, runstate.StatusRunning)
	dispatched.Build = staleResidentBuild
	harness.record(t, dispatched)

	harness.poll(t, harness.start(),
		notify.KindRunStarted, notify.KindResidentStale)
}

// A live session that says which binary it is is believed over the runs beside
// it, even where a run was reserved later by a different one. That is the
// precedence rather than a contest of which record is newer: the session is the
// resident, and a run reserved by another binary is an operator's `yoyo run` or a
// triage carry-out — a process that is already ending, whose build is not the one
// that will go on choosing work.
func TestALiveSessionsOwnStampIsBelievedOverTheRunsBesideIt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.feed.Deployments = perBuildDeployments{currentResidentBuild: 0, staleResidentBuild: 31}
	// The session is on what is deployed and said so ten minutes ago; a run
	// started since was reserved by a binary thirty-one changes behind.
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, currentResidentBuild)
	dispatched := harness.run(t, runstate.StatusRunning)
	dispatched.Build = staleResidentBuild
	dispatched.StartedAt = moment.Add(10 * time.Minute)
	dispatched.UpdatedAt = dispatched.StartedAt
	harness.record(t, dispatched)

	// The resident is current, so nothing is said about a stale one however
	// recently the run beside it started.
	cursors := harness.poll(t, harness.start(), notify.KindRunStarted)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// A run that has ended says which build made it and is not evidence about what is
// running now. The record is still the answer to "which build did this" long
// afterwards; what it stops being is an account of the resident.
func TestAFinishedRunIsNotReadAsTheResident(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.deployed(31)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	ended := harness.run(t, runstate.StatusSucceeded)
	ended.StopClass = runstate.StopIntegrationPolicy
	ended.Build = staleResidentBuild
	harness.record(t, ended)

	cursors := harness.poll(t, harness.start(),
		notify.KindRunStarted, notify.KindChecksPassed, notify.KindRunEnded)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// The build revision is the yoyodyne binary's and the repository is the
// product's, and those are one history only where the product is the harness's
// own source. Everywhere else the repository has never held that revision, there
// is no count to say, and nothing is wrong — so the channel hears nothing at all
// and the sink's log says why once per build rather than every hour for the life
// of the process.
func TestABuildFromAnotherRepositoryIsSilentRatherThanCountedOrNagged(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.feed.Deployments = unrelatedDeployments{}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start())
	if len(said) != 1 {
		t.Fatalf("the sink said %v about a build from another repository, want it said once", said)
	}
	for hour := 0; hour < 4; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
	if len(said) != 1 {
		t.Fatalf("the sink said %v, want an installation that is behaving told about once", said)
	}
}

// A repository that cannot be read leaves the sink unable to tell a session
// running what is deployed from one running a binary from before the fix. It must
// not guess in either direction, so it is said once where the sink says everything
// about itself, and asked again at the next interval rather than at the next poll.
func TestARepositoryThatCannotBeReadIsSaidRatherThanGuessedAt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.feed.Deployments = brokenDeployments{}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start())
	if len(said) != 1 {
		t.Fatalf("the sink said %v about a repository it could not read, want it said once", said)
	}
	harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v, want the retry held to the heartbeat interval", said)
	}
}

// The builds a session can be running: one the harness has moved past, the newer
// one a restart puts it on that is still not current, and the one that is
// actually deployed.
const (
	staleResidentBuild   = "4c1f2b3a9d8e7f6a5b4c3d2e1f0099887766554433221100aabbccddeeff0011"
	newerResidentBuild   = "aa11bb22cc33dd44ee55ff6677889900112233445566778899aabbccddeeff00"
	currentResidentBuild = "ff00ee11dd22cc33bb44aa5566778899001122334455667788990011223344ff"
)

// ready gives the feed a tracker that reports this many items ready to pull, and
// the hourly cadence the sink ships with.
func (h *testHarness) ready(count int) {
	h.feed.Backlog = countedBacklog{count: count}
}

// deployed gives the feed a repository that has taken on this many changes since
// whatever build it is asked about.
func (h *testHarness) deployed(behind int) {
	h.feed.Deployments = countedDeployments{behind: behind}
}

// watchedBuild records a transition of a session that says which binary it is
// running, which is what every session started by a build that stamped a revision
// records.
func (h *testHarness) watchedBuild(t *testing.T, state runstate.WatchState, reason string, at time.Time, build string) {
	t.Helper()
	h.watchedBuildAs(t, "watch-0123456789abcdef0123456789abcdef", state, reason, at, build)
}

func (h *testHarness) watchedBuildAs(t *testing.T, sessionID string, state runstate.WatchState, reason string, at time.Time, build string) {
	t.Helper()
	if err := h.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         state,
		At:            at,
		Reason:        reason,
		Build:         build,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

// resident makes one pass and returns the delivery the resident stream produced,
// which is where a test reads the parts of it a rendered message does not carry:
// whether the operators were told directly, and the cursor that records it.
func (h *testHarness) resident(t *testing.T, cursors Cursors) Delivery {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == residentStream && !delivery.Silent() {
			return delivery
		}
	}
	t.Fatal("nothing was said about the session's build")
	return Delivery{}
}

func (h *testHarness) hold(t *testing.T, reason string, at time.Time) {
	t.Helper()
	if _, err := h.intake.Hold(runstate.IntakeHolderOperator, reason, at); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
}

// say makes one pass, checks it said exactly what was expected, and returns the
// message as the channel would have it — which is where a test reads what a
// persona actually says rather than only which kind was selected.
func (h *testHarness) say(t *testing.T, cursors Cursors, want notify.Kind) notify.Message {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if !delivery.Posts() {
			continue
		}
		if delivery.Notification.Event.Kind != want {
			t.Fatalf("said %q, want %q", delivery.Notification.Event.Kind, want)
		}
		message, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
		if err != nil {
			t.Fatalf("a selected notification could not be said: %v", err)
		}
		return message
	}
	t.Fatalf("nothing was said, want %q", want)
	return notify.Message{}
}

type countedBacklog struct {
	count int
}

func (b countedBacklog) Ready(context.Context) (int, error) { return b.count, nil }

type brokenBacklog struct{}

func (brokenBacklog) Ready(context.Context) (int, error) {
	return 0, errors.New("bd: executable file not found in $PATH")
}

type countedDeployments struct {
	behind int
}

func (d countedDeployments) Behind(context.Context, string) (int, error) { return d.behind, nil }

// perBuildDeployments answers differently for each build, which is what a real
// repository does and what separating two candidate residents needs: a session on
// what is deployed and a run reserved by something older are one reading only if
// the repository is asked about each of them.
type perBuildDeployments map[string]int

func (d perBuildDeployments) Behind(_ context.Context, build string) (int, error) {
	return d[build], nil
}

type brokenDeployments struct{}

func (brokenDeployments) Behind(context.Context, string) (int, error) {
	return 0, errors.New("git rev-list failed: bad revision")
}

// unrelatedDeployments is the ordinary answer for every product that is not the
// harness's own source: the repository this sink is pointed at has never held the
// revision the session's binary was built from.
type unrelatedDeployments struct{}

func (unrelatedDeployments) Behind(context.Context, string) (int, error) {
	return 0, fmt.Errorf("%w: not in /some-other-product", ErrUnrelatedBuild)
}

// standingTracker is a tracker with an empty admitted queue, which is what the
// four lines need to be readable at all: a nil tracker would make the queue's
// line report a wiring gap rather than an empty backlog.
type standingTracker struct{}

func (standingTracker) List(context.Context, string) ([]beads.WorkItem, error) { return nil, nil }
func (standingTracker) Ready(context.Context) ([]beads.WorkItem, error)        { return nil, nil }

// The heartbeat says the same four lines the terminal prints. Before this it
// said that choosing had stopped and nothing whatever about what the machine was
// doing instead, which is exactly what somebody woken by it at three in the
// morning then has to go and reconstruct.
func TestTheHeartbeatCarriesTheFourLines(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	held := harness.now
	harness.hold(t, "reordering the backlog first", held)
	harness.feed.Standing = &readmodel.Sources{
		Runs:          harness.runs,
		Conversations: harness.chats,
		Tracker:       standingTracker{},
		Directives:    harness.directives,
		Amendments:    harness.amend,
		OperatorHolds: harness.holds,
		IntakeHolds:   harness.intake,
		Sessions:      harness.watch,
		Capacity:      1,
		Now:           func() time.Time { return harness.now },
	}

	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	harness.now = held.Add(2 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	for _, line := range []string{
		"Running: nothing",
		"Working: nothing",
		"Not startable: nothing, of no admitted items",
		"Needs a human (1)",
	} {
		if !strings.Contains(said.Body, line) {
			t.Fatalf("body %q does not carry %q", said.Body, line)
		}
	}
	// Counted and not listed. Nobody asked for this message, and an enumerated
	// queue under every line is a screen of detail repeated every hour in front of
	// the one sentence that says the line has stopped. What was waiting on a person
	// here is a resolvable directive, and its own words stay in `yoyo status`.
	if strings.Contains(said.Body, "is unresolved") {
		t.Fatalf("body %q enumerates a queue, want the four lines counted", said.Body)
	}
}

// What the channel says a state is, is what the read model says it is. This sink
// used to derive it a second time, and the two derivations disagreed exactly
// where it costs somebody a trip: a live session sitting idle over a queue it
// would not touch was reported here as no session running at all, which sends an
// operator to start the session they already have.
func TestTheHeartbeatSaysTheStateTheReadModelDerived(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	idle := harness.now
	harness.watched(t, runstate.WatchIdle, "nothing it could start", idle)

	cursors := harness.poll(t, harness.start())
	harness.now = idle.Add(2 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)

	stall := readmodel.WhyNothingStarts(readmodel.Conditions{
		Sessions: func() ([]runstate.WatchTransition, error) {
			return []runstate.WatchTransition{{
				SessionID: "watch-0123456789abcdef0123456789abcdef",
				State:     runstate.WatchIdle,
				At:        idle,
			}}, nil
		},
	})
	if !strings.Contains(said.Body, stall.Says) {
		t.Fatalf("body %q does not say the read model's %q", said.Body, stall.Says)
	}
	if strings.Contains(said.Body, "yoyo work --watch") {
		t.Fatalf("an idle session was told to start a session: %q", said.Body)
	}
}

// A live session whose last poll could not read the store is not one that found
// nothing to start. The waiting line said it as idle for the whole of a store
// outage, closing on somebody else's move; it now says the read is failing and
// being retried, and closes on the harness's.
func TestTheWaitingLineSaysAFailedReadIsBeingRetriedInTheHarnesssVoice(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	outage := harness.now
	if err := harness.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         runstate.WatchIdle,
		At:            outage,
		Reason:        "the harness could not be read and is being read again for up to 2h0m0s before the session gives up on it: database is locked",
		Unreadable:    true,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	cursors := harness.poll(t, harness.start())
	harness.now = outage.Add(2 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)

	if !strings.Contains(said.Body, "could not read the harness's store and is reading it again") {
		t.Fatalf("body %q does not say the store read is failing and being retried", said.Body)
	}
	if strings.Contains(said.Body, "found nothing") || strings.Contains(strings.ToLower(said.Body), "product manager") {
		t.Fatalf("body %q says the session found nothing, or names the product manager", said.Body)
	}
	if strings.Contains(said.Body, "the operator's") {
		t.Fatalf("body %q hands the operator a move over a read the harness is retrying", said.Body)
	}
	if !strings.Contains(said.Body, readmodel.Stall{Reason: readmodel.ReasonStoreUnreadable}.Whose()) {
		t.Fatalf("body %q does not close on the harness's move, %q", said.Body, readmodel.Stall{Reason: readmodel.ReasonStoreUnreadable}.Whose())
	}
}

// The mark a standing state is remembered by still renders exactly as this
// package stamped it before the derivation moved to the read model.
//
// Both halves of the cursor value matter and for one reason. A sink names the
// state it is standing on by this, and a mark it does not recognise re-arms the
// clock silently — so a state standing when the harness is deployed would go
// quiet for a whole interval, which is precisely the silence the heartbeat
// exists to end. The reason tokens were kept as they were found for that, and
// the timestamp is the other half: a format that drifted from stamp's would buy
// the same silence by the other route.
func TestTheStandingMarkRendersAsThisPackageStampedIt(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 8, 30, 12, 0, 0, 123456789, time.UTC)
	for _, testCase := range []struct {
		reason readmodel.Reason
		legacy string
	}{
		{readmodel.ReasonOperatorHold, "hold:"},
		{readmodel.ReasonIntakeHold, "intake:"},
		{readmodel.ReasonSessionIdle, "idle:"},
		{readmodel.ReasonNoWatchSession, "stopped:"},
	} {
		stall := readmodel.Stall{Reason: testCase.reason, Since: since}
		if want := testCase.legacy + stamp(since); stall.Mark() != want {
			t.Fatalf("mark = %q, want the cursor value this package already holds, %q", stall.Mark(), want)
		}
	}
}

// A sink assembled without a way to read the four lines says so, rather than
// leaving them out. A message that simply lacked them would be indistinguishable
// from a harness with nothing in any of them.
func TestAHeartbeatWithNoStandingSaysSoRatherThanOmittingIt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	held := harness.now
	harness.hold(t, "reordering the backlog first", held)

	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	harness.now = held.Add(2 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	if !strings.Contains(said.Body, "where the harness stands could not be read here") {
		t.Fatalf("body %q does not say the four lines were unavailable", said.Body)
	}
}

// A line that cannot start work is not an idle one, and the difference is the
// whole of what the reader can act on. The session's own words carry the file
// and the move that ends it, and they are repeated verbatim rather than
// generalized back to a sentence written here — which would leave the reader
// exactly where the three silences left them.
func TestABlockedLineSaysWhatIsStoppingItAndWhatEndsIt(t *testing.T) {
	t.Parallel()

	const stopped = "runs cannot start: uncommitted changes in the primary checkout (docs/notes.md); commit or stash to release"
	harness := newTestHarness(t, time.Time{})
	harness.ready(5)
	stalled := harness.now
	harness.watched(t, runstate.WatchBlocked, stopped, stalled)

	cursors := harness.poll(t, harness.start(), notify.KindWatchBlocked)
	harness.now = stalled.Add(9 * time.Hour)
	batch := harness.batch(t, cursors)
	if batch.Asking == nil || batch.Asking.Stopped != stopped || len(batch.Asking.Options) < 2 {
		t.Fatalf("asking = %+v, want the recorded refusal with answers to offer", batch.Asking)
	}
	said := harness.say(t, cursors, notify.KindLineWaiting)
	for _, fact := range []string{stopped, "9 hours", "5 items"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
}
