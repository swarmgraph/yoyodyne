package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

var moment = time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)

// What is worth saying is the notifier's to decide, and the feed's whole job is
// to hand it the two readings it compares and to remember which crossings have
// been said. Read the same record twice and the second reading says nothing: a
// thread is a narrative rather than an event log scrolling sideways.
func TestARunsCrossingsAreSaidOnceHoweverOftenTheRecordIsRead(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	state := harness.run(t, runstate.StatusRunning)
	harness.record(t, state)

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted)
	harness.poll(t, cursors)

	// The record moves on, and only what it crossed since is said.
	state.Phase = runstate.PhaseReviewing
	state.UpdatedAt = moment.Add(time.Minute)
	harness.save(t, state)
	harness.poll(t, cursors, notify.KindChecksPassed)
}

// A check that fails, is repaired, and fails differently has crossed the same
// kind twice with two different things to say. A cursor that could not tell
// those apart would swallow the second, which is the repair loop going quiet at
// exactly the point somebody is watching it.
func TestTheSameKindCrossedTwiceDifferentlyIsSaidTwice(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	state := harness.run(t, runstate.StatusRunning)
	state.Phase = runstate.PhaseChecking
	state.CheckFailure = &runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}
	harness.record(t, state)

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindChecksFailed)

	state.CheckFailure = &runstate.CheckFailure{Command: "go vet ./...", ExitCode: 2}
	state.UpdatedAt = moment.Add(time.Minute)
	harness.save(t, state)
	harness.poll(t, cursors, notify.KindChecksFailed)
}

// The reading a crossing was said against advances only once the whole of it has
// been posted. A sink killed halfway therefore repeats what it had already said
// rather than losing what it had not: the durable record is authoritative, and a
// repetition is the right side of that trade.
func TestACrossingInterruptedHalfwayRepeatsRatherThanLosesTheRest(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	state := harness.run(t, runstate.StatusRunning)
	state.Phase = runstate.PhaseChecking
	state.CheckFailure = &runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}
	harness.record(t, state)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Deliveries) != 2 {
		t.Fatalf("deliveries = %d, want the run started and its checks failing", len(batch.Deliveries))
	}
	// Only the first was posted before the process died.
	interrupted := Cursors{Streams: map[string]Cursor{
		batch.Deliveries[0].Stream: batch.Deliveries[0].Cursor,
	}}
	harness.poll(t, interrupted, notify.KindChecksFailed)
}

// A sink started today does not want a month of finished work arriving at once.
// A run that was already over before it started is read past without a word, and
// its cursor closes so it is not carried for as long as the product exists.
func TestARunThatWasOverBeforeTheSinkStartedIsReadPastSilently(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	state := harness.run(t, runstate.StatusSucceeded)
	harness.record(t, state)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Deliveries) != 1 || !batch.Deliveries[0].Silent() {
		t.Fatalf("deliveries = %#v, want one silent advance and nothing said", batch.Deliveries)
	}
	if !batch.Deliveries[0].Cursor.Closed {
		t.Fatalf("cursor = %#v, want history closed rather than carried", batch.Deliveries[0].Cursor)
	}
}

// A status is a reading rather than a crossing, and it is the reading of the
// item's latest run: a second attempt is what is happening to the item now, and
// what the first one did is in the thread rather than on it.
func TestAnItemsStatusIsReadFromItsLatestRun(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	failed := harness.run(t, runstate.StatusFailed)
	failed.Failure = "the repair budget was spent"
	harness.record(t, failed)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if got := batch.Statuses["work-item:yoyodyne-ifd.68.3"]; got != notify.StatusBlocked {
		t.Fatalf("status = %q, want a run that stopped and stayed stopped read as blocked", got)
	}

	retried := harness.run(t, runstate.StatusRunning)
	retried.Phase = runstate.PhaseReviewing
	retried.StartedAt = moment.Add(time.Hour)
	retried.UpdatedAt = moment.Add(time.Hour)
	harness.record(t, retried)

	batch, err = harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("second Poll() error = %v", err)
	}
	if got := batch.Statuses["work-item:yoyodyne-ifd.68.3"]; got != notify.StatusInReview {
		t.Fatalf("status = %q, want the latest attempt rather than the one before it", got)
	}
}

// A run that was over before the sink was ever pointed at this product is
// history the channel was never told about, so it marks nothing: a thread opened
// today by something else must not acquire a status from a run nobody here has
// said a word about.
func TestARunThatWasOverBeforeTheSinkStartedMarksNothing(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	harness.record(t, harness.run(t, runstate.StatusSucceeded))

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Statuses) != 0 {
		t.Fatalf("statuses = %#v, want history to mark nothing", batch.Statuses)
	}
}

// A run that is over and owes nothing has nothing left to cross, so the reading
// it was compared against is dropped. Keeping it would make the sink's own
// record grow with the product's whole history.
func TestARunThatIsOverAndOwesNothingStopsBeingCarried(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	state := harness.run(t, runstate.StatusRunning)
	harness.record(t, state)
	cursors := harness.poll(t, harness.start(), notify.KindRunStarted)
	if cursors.Streams[runStream(state.RunID)].Reported == nil {
		t.Fatal("a run still in flight must keep the reading it is compared against")
	}

	completed := moment.Add(time.Minute)
	state.Status = runstate.StatusSucceeded
	state.StopClass = runstate.StopIntegrationPolicy
	state.Phase = runstate.PhaseComplete
	state.CompletedAt = &completed
	state.UpdatedAt = completed
	harness.save(t, state)

	// The checks passed and the policy ended the run without promotion, so both
	// are said. The pass after says nothing and closes the run.
	cursors = harness.poll(t, cursors, notify.KindChecksPassed, notify.KindRunEnded)
	cursors = harness.poll(t, cursors)
	closed := cursors.Streams[runStream(state.RunID)]
	if !closed.Closed || closed.Reported != nil {
		t.Fatalf("cursor = %#v, want a settled run closed and its reading dropped", closed)
	}
}

// A report is the agent's own words and a proposal is its argument about a
// document it does not own. Both are logs, so both advance by position, and both
// are said once.
func TestReportsAndProposalsAreSaidOnceInTheOrderTheyWereRecorded(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityWarning, moment)
	harness.propose(t, "amendment-0123456789abcdef0123456789abcde0", moment)

	cursors := harness.poll(t, harness.start(),
		notify.KindReportFiled, notify.KindProposalRaised)
	harness.poll(t, cursors)

	// A critical report is also a finding for the operator until somebody handles
	// it, said to him beside the report.
	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityCritical, moment.Add(time.Minute))
	harness.poll(t, cursors, notify.KindReportFiled, notify.KindOperatorAction)
}

// A finding that needs the operator's own hand is said to him once — directly,
// and tagged by member id — the pass after it is recorded, and never again while
// it stands: `yoyo status` names it. Two records make one: a report handled as
// needing him, and a critical report nobody has handled. A later handling of the
// same report that says nothing of the kind ends it, silently; and the same
// report handled as needing him again is a second finding, said once more.
func TestAFindingForTheOperatorIsSaidToHimOnceDirectly(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityWarning, moment)
	cursors := harness.poll(t, harness.start(), notify.KindReportFiled)

	// The product manager handles it as needing the operator: the finding is
	// recorded the moment the handling is, and said on the next pass.
	harness.handle(t, "report-0123456789abcdef0123456789abcde0", "add the PreToolUse hook to .claude/settings.json by hand", true, moment.Add(time.Hour))
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	var findings []Delivery
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == operatorActionStream && delivery.Posts() {
			findings = append(findings, delivery)
		}
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %#v, want the handling said once", findings)
	}
	finding := findings[0]
	if !finding.Direct || !finding.Tag {
		t.Fatalf("finding = %#v, want it said to the operator directly and tagged by member id", finding)
	}
	if finding.Notification.Event.Kind != notify.KindOperatorAction || finding.Notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("finding = %#v, want a warning of the operator-action kind", finding.Notification.Event)
	}
	message, err := notify.Render(finding.Notification.Topic, finding.Notification.Speaker, finding.Notification.Event)
	if err != nil {
		t.Fatalf("render the finding: %v", err)
	}
	for _, want := range []string{"add the PreToolUse hook to .claude/settings.json by hand", "report-0123456789abcdef0123456789abcde0", "the Lead Product Manager, handling the report"} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("finding reads as %q, which does not say %q", message.Body, want)
		}
	}
	cursors = harness.poll(t, cursors, notify.KindOperatorAction)
	if !cursors.Streams[operatorActionStream].Has(findingMark + "report:report-0123456789abcdef0123456789abcde0") {
		t.Fatalf("cursor = %#v, want the finding marked as said", cursors.Streams[operatorActionStream])
	}

	// A second pass sends nothing more, however many times the record is read.
	cursors = harness.poll(t, cursors)
	cursors = harness.poll(t, cursors)

	// The operator makes the change and the product manager records it: the
	// finding ends, and its mark goes with it. Nothing is said about that — the
	// status line stops naming it, which is the ending.
	harness.handle(t, "report-0123456789abcdef0123456789abcde0", "the hook is in place; the operator added it", false, moment.Add(2*time.Hour))
	cursors = harness.poll(t, cursors)
	if len(cursors.Streams[operatorActionStream].Delivered) != 0 {
		t.Fatalf("cursor = %#v, want a finding that ended forgotten", cursors.Streams[operatorActionStream])
	}

	// Handled as needing him again, it is a finding again, said once more.
	harness.handle(t, "report-0123456789abcdef0123456789abcde0", "the hook was removed by a settings sync; it has to go back", true, moment.Add(3*time.Hour))
	cursors = harness.poll(t, cursors, notify.KindOperatorAction)
	harness.poll(t, cursors)
}

// A run stopping on a condition only a person can clear is recorded as the
// development manager's escalation of that stoppage, and it is said to the
// operator once, directly and tagged, the pass after the decision is recorded.
// A later decision on the same run ends it; the mark goes with it.
func TestAnEscalatedStoppageIsSaidToTheOperatorOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stopped := harness.run(t, runstate.StatusFailed)
	stopped.Phase = runstate.PhaseIntegrating
	stopped.Blocker = "Yoyodyne stopped this item: main on origin does not contain the local main, and only a person can say which history is right."
	harness.record(t, stopped)
	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindChecksPassed, notify.KindBlockerRecorded)

	decision := runstate.TriageDecision{
		Decision:     runstate.TriageDecisionEscalate,
		RunID:        stopped.RunID,
		Reason:       "the target branch diverged from the forge; only the operator can say which history is right",
		DecidedBy:    "development-manager",
		Conversation: "chat-0123456789abcdef0123456789abcdef",
		Turn:         7,
		DecidedAt:    moment.Add(time.Hour),
	}
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), stopped.WorkItemID, decision, decision.DecidedAt); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	var findings []Delivery
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == operatorActionStream && delivery.Posts() {
			findings = append(findings, delivery)
		}
	}
	if len(findings) != 1 || !findings[0].Direct || !findings[0].Tag {
		t.Fatalf("findings = %#v, want the escalation said once, directly and tagged", findings)
	}
	message, err := notify.Render(findings[0].Notification.Topic, findings[0].Notification.Speaker, findings[0].Notification.Event)
	if err != nil {
		t.Fatalf("render the finding: %v", err)
	}
	for _, want := range []string{
		"the target branch diverged from the forge; only the operator can say which history is right",
		"the development manager, escalating the stopped run to the operator",
		"escalation of run " + stopped.RunID,
		"a later triage decision on the run, the item being run again, parked, retired, or closed, or the run's branch and worktree both being gone ends it",
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("finding reads as %q, which does not say %q", message.Body, want)
		}
	}
	cursors = harness.poll(t, cursors, notify.KindOperatorAction)
	cursors = harness.poll(t, cursors)

	// The development manager decides the run again: the escalation is
	// superseded, the finding ends, and its mark is forgotten.
	later := decision
	later.Decision, later.DecidedAt = runstate.TriageDecisionWait, moment.Add(2*time.Hour)
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), stopped.WorkItemID, later, later.DecidedAt); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	cursors = harness.poll(t, cursors)
	if len(cursors.Streams[operatorActionStream].Delivered) != 0 {
		t.Fatalf("cursor = %#v, want an ended escalation forgotten", cursors.Streams[operatorActionStream])
	}
}

// The batch an owning role argued on a recurring pass is one decision list, and
// it reaches the operator the way every finding does: once, directly and tagged,
// through the operator-action message, naming each proposal and what the owner
// recommends. It ends, silently, once the operator has decided every proposal
// in it.
func TestAnOwnersArguedBatchIsSaidToTheOperatorOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	first := "amendment-0123456789abcdef0123456789abcde0"
	second := "amendment-0123456789abcdef0123456789abcde1"
	harness.propose(t, first, moment)
	harness.propose(t, second, moment.Add(time.Minute))
	cursors := harness.poll(t, harness.start(), notify.KindProposalRaised, notify.KindProposalRaised)

	pass := moment.Add(time.Hour)
	if err := harness.runs.Sweeps().Append(runstate.Sweep{
		Task: "architect-amendments", Role: domain.RoleArchitect,
		StartedAt: pass, EndedAt: pass.Add(time.Minute), Turns: 1,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "argued two", Recommendations: []sweep.Recommendation{
			{Proposal: first, Verdict: sweep.RecommendApprove, Reason: "the design is silent on it"},
			{Proposal: second, Verdict: sweep.RecommendMerge, Reason: "the same change", Into: first},
		}},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	var findings []Delivery
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == operatorActionStream && delivery.Posts() {
			findings = append(findings, delivery)
		}
	}
	if len(findings) != 1 || !findings[0].Direct || !findings[0].Tag {
		t.Fatalf("findings = %#v, want the batch said once, directly and tagged", findings)
	}
	message, err := notify.Render(findings[0].Notification.Topic, findings[0].Notification.Speaker, findings[0].Notification.Event)
	if err != nil {
		t.Fatalf("render the finding: %v", err)
	}
	for _, want := range []string{
		first + " approve; " + second + " merge into " + first,
		"the design is silent on it",
		"the architect, arguing the undecided changes proposed to its documents",
		"yoyo sweeps --task architect-amendments",
		"every proposal in it is decided",
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("finding reads as %q, which does not say %q", message.Body, want)
		}
	}
	cursors = harness.poll(t, cursors, notify.KindOperatorAction)
	cursors = harness.poll(t, cursors)

	// Deciding one leaves the batch standing and says nothing more; deciding the
	// other ends it, and its mark goes with it.
	for index, id := range []string{first, second} {
		if err := harness.amend.Decide(amendment.Decision{
			SchemaVersion: amendment.SchemaVersion, ProposalID: id, Verdict: amendment.VerdictApproved,
			Authority: domain.RoleArchitect, Decider: amendment.DeciderOperator,
			DecidedAt: pass.Add(time.Duration(index+1) * time.Hour),
		}); err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		cursors = harness.poll(t, cursors)
	}
	if len(cursors.Streams[operatorActionStream].Delivered) != 0 {
		t.Fatalf("cursor = %#v, want a batch whose every proposal is decided forgotten", cursors.Streams[operatorActionStream])
	}
}

// A finding from before the watermark is history, exactly as the record it came
// from is: it is marked and not said. Its moment is the record that made it, so
// a handling made today of a report filed before the channel existed is news
// today.
func TestAFindingFromBeforeTheWatermarkIsReadPast(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityCritical, moment.Add(-2*time.Hour))
	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityNote, moment.Add(-time.Hour))
	cursors := harness.poll(t, harness.start())
	if !cursors.Streams[operatorActionStream].Has(findingMark + "report:report-0123456789abcdef0123456789abcde0") {
		t.Fatalf("cursor = %#v, want the old critical marked without being said", cursors.Streams[operatorActionStream])
	}
	harness.handle(t, "report-0123456789abcdef0123456789abcde1", "only the operator can rotate that token", true, moment.Add(time.Hour))
	harness.poll(t, cursors, notify.KindOperatorAction)
}

// The 17:56Z shape, replayed: the brake trips on three runs, and within one
// poll the trip is said to the operator directly and tagged by member id,
// naming each run with its item and what stopped it, and the verb that lifts
// the hold — once, however many passes follow. The development manager
// escalating it to him is the moment it becomes his, and that is said to him
// once more. The release is said once, by whom. His own hold is said to the
// channel alone, because he placed it.
func TestABrakeTripIsSaidToTheOperatorDirectlyOnceAndItsEscalationOnceMore(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	trip := runstate.IntakeBrake{
		Blocked: []runstate.BrakeBlockedRun{
			{RunID: "run-0000000000000000000000000000398a", WorkItemID: "yoyodyne-ifd.398", Reason: "its reviewer still required repair after 2 repair attempt(s)"},
			{RunID: "run-0000000000000000000000000000401a", WorkItemID: "yoyodyne-ifd.401", Reason: "check `make test` failed (exit 1) after 2 repair attempt(s)"},
			{RunID: "run-0000000000000000000000000000402a", WorkItemID: "yoyodyne-ifd.402", Reason: "its reviewer still required repair after 2 repair attempt(s)"},
		},
		CooldownEndsAt: moment.Add(30 * time.Minute),
	}
	if _, err := harness.intake.Brake(trip, "3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3", moment); err != nil {
		t.Fatalf("Brake() error = %v", err)
	}
	kinded := func(batch Batch, kind notify.Kind) []Delivery {
		var found []Delivery
		for _, delivery := range batch.Deliveries {
			if delivery.Notification.Event.Kind == kind {
				found = append(found, delivery)
			}
		}
		return found
	}
	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	held := kinded(batch, notify.KindIntakeHeld)
	if len(held) != 1 {
		t.Fatalf("deliveries = %#v, want the brake trip said once", batch.Deliveries)
	}
	if !held[0].Direct || !held[0].Tag {
		t.Fatalf("trip = %#v, want it said to the operator directly and tagged by member id", held[0])
	}
	message, err := notify.Render(held[0].Notification.Topic, held[0].Notification.Speaker, held[0].Notification.Event)
	if err != nil {
		t.Fatalf("render the trip: %v", err)
	}
	for _, want := range []string{
		"run run-0000000000000000000000000000398a of yoyodyne-ifd.398: its reviewer still required repair",
		"run run-0000000000000000000000000000401a of yoyodyne-ifd.401: check `make test` failed (exit 1)",
		"run run-0000000000000000000000000000402a of yoyodyne-ifd.402",
		"`yoyo release`, or `/release` in the conversation, lifts it sooner",
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("trip reads as %q, which does not say %q", message.Body, want)
		}
	}
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	cursors = harness.poll(t, cursors)

	// She escalates it to him: said to him once, directly and tagged, naming
	// that she did.
	if _, err := harness.intake.DecideBrake(runstate.BrakeDecisionEscalate, "the checks fail on main and only the operator can say why", "development-manager conversation chat-1, turn 4", moment.Add(10*time.Minute)); err != nil {
		t.Fatalf("DecideBrake() error = %v", err)
	}
	batch, err = harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	escalated := kinded(batch, notify.KindIntakeHeld)
	if len(escalated) != 1 || !escalated[0].Direct || !escalated[0].Tag {
		t.Fatalf("deliveries = %#v, want her escalation said to the operator once, directly and tagged", batch.Deliveries)
	}
	message, err = notify.Render(escalated[0].Notification.Topic, escalated[0].Notification.Speaker, escalated[0].Notification.Event)
	if err != nil {
		t.Fatalf("render the escalation: %v", err)
	}
	if !strings.Contains(message.Body, "the development manager escalated it to the operator") {
		t.Fatalf("escalation reads as %q, which does not say she escalated it", message.Body)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeHeld)
	cursors = harness.poll(t, cursors)

	if _, _, err := harness.intake.ReleaseBy("the operator, at a terminal (`yoyo release`)", moment.Add(2*time.Hour)); err != nil {
		t.Fatalf("ReleaseBy() error = %v", err)
	}
	batch, err = harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	released := kinded(batch, notify.KindIntakeReleased)
	if len(released) != 1 {
		t.Fatalf("deliveries = %#v, want the release said", batch.Deliveries)
	}
	message, err = notify.Render(released[0].Notification.Topic, released[0].Notification.Speaker, released[0].Notification.Event)
	if err != nil {
		t.Fatalf("render the release: %v", err)
	}
	if !strings.Contains(message.Body, "released by the operator, at a terminal (`yoyo release`)") {
		t.Fatalf("release reads as %q, which does not say who lifted it", message.Body)
	}
	if !released[0].Notification.Event.At.Equal(moment.Add(2 * time.Hour)) {
		t.Fatalf("release is dated %s, want the moment it was recorded", released[0].Notification.Event.At)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	if marks := cursors.Streams[productStream].Delivered; len(marks) != 0 {
		t.Fatalf("product cursor = %#v, want the hold's marks forgotten with it", marks)
	}
	harness.poll(t, cursors)

	// The operator's own hold is his decision, and is said to the channel alone.
	if _, err := harness.intake.Hold(runstate.IntakeHolderOperator, "reordering the backlog first", moment.Add(3*time.Hour)); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	batch, err = harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range kinded(batch, notify.KindIntakeHeld) {
		if delivery.Direct || delivery.Tag {
			t.Fatalf("the operator's own hold was said to him directly: %#v", delivery)
		}
	}
}

// Two findings ending in one pass both have their marks dropped: the cursor
// holds only what is standing, and neither is kept for a finding that ended.
func TestTwoFindingsEndingInOnePassAreBothForgotten(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityWarning, moment)
	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityWarning, moment.Add(time.Minute))
	harness.file(t, "report-0123456789abcdef0123456789abcde2", report.SeverityWarning, moment.Add(2*time.Minute))
	cursors := harness.poll(t, harness.start(), notify.KindReportFiled, notify.KindReportFiled, notify.KindReportFiled)
	for _, id := range []string{"report-0123456789abcdef0123456789abcde0", "report-0123456789abcdef0123456789abcde1", "report-0123456789abcdef0123456789abcde2"} {
		harness.handle(t, id, "only the operator can rotate that token", true, moment.Add(time.Hour))
	}
	cursors = harness.poll(t, cursors, notify.KindOperatorAction, notify.KindOperatorAction, notify.KindOperatorAction)

	harness.handle(t, "report-0123456789abcdef0123456789abcde0", "rotated", false, moment.Add(2*time.Hour))
	harness.handle(t, "report-0123456789abcdef0123456789abcde1", "rotated", false, moment.Add(2*time.Hour))
	cursors = harness.poll(t, cursors)
	marks := cursors.Streams[operatorActionStream].Delivered
	if len(marks) != 1 || marks[0] != findingMark+"report:report-0123456789abcdef0123456789abcde2" {
		t.Fatalf("cursor = %#v, want only the standing finding marked", marks)
	}
}

// What a product recorded before this sink started is history. It is read past
// in one silent advance rather than rescanned on every pass for as long as the
// process runs.
func TestRecordsOlderThanTheSinkAreReadPastInOneAdvance(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityNote, moment)
	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityNote, moment.Add(time.Minute))

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	var reports []Delivery
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == reportStream {
			reports = append(reports, delivery)
		}
	}
	if len(reports) != 1 || !reports[0].Silent() {
		t.Fatalf("deliveries = %#v, want one silent advance past both", reports)
	}
	if reports[0].Cursor.Position != 2 {
		t.Fatalf("cursor = %#v, want the log read past rather than rescanned", reports[0].Cursor)
	}
}

// An outage delays messages rather than losing them, and the record filed while
// the sink was down is exactly the one that would be lost. The stream it arrives
// on has never advanced — the normal state of a product that has not needed a
// report for weeks — so "has this cursor moved" is no answer to "has this sink
// ever run". Only the watermark answers that, and it does not move.
func TestARecordFiledWhileTheSinkWasDownIsStillPosted(t *testing.T) {
	t.Parallel()

	// Somebody turned reporting on, nothing was filed, and the sink stopped with
	// its report cursor still at zero.
	harness := newTestHarness(t, moment)
	cursors := harness.poll(t, harness.start())
	if cursors.Streams[reportStream].Position != 0 {
		t.Fatalf("cursor = %#v, want a log nothing has been filed on left where it was", cursors.Streams[reportStream])
	}

	// An hour of downtime, and a critical filed in the middle of it.
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityCritical, moment.Add(time.Hour))

	// The restart reads the same watermark it wrote, so the report is news — and
	// so is the finding a critical report is until somebody handles it.
	harness.poll(t, cursors, notify.KindReportFiled, notify.KindOperatorAction)
}

// The same thing for a run: one that both started and finished while the sink
// was down has no cursor at all, so nothing but the watermark distinguishes it
// from work that was over before reporting was ever turned on.
func TestARunThatRanEntirelyWhileTheSinkWasDownIsStillReported(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		class runstate.StopClass
		want  string
	}{
		{name: "a historical ending", want: "unknown"},
		{name: "a policy ending", class: runstate.StopIntegrationPolicy, want: "integration-policy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newTestHarness(t, moment)
			cursors := harness.poll(t, harness.start())

			completed := moment.Add(time.Hour)
			state := harness.run(t, runstate.StatusSucceeded)
			state.StopClass = test.class
			state.StartedAt = moment.Add(30 * time.Minute)
			state.UpdatedAt = completed
			state.CompletedAt = &completed
			harness.record(t, state)

			batch, err := harness.feed.Poll(context.Background(), cursors)
			if err != nil {
				t.Fatal(err)
			}
			for _, delivery := range batch.Deliveries {
				if delivery.Notification.Event.Kind == notify.KindRunEnded {
					message, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
					if err != nil || !strings.Contains(message.Body, test.want) {
						t.Fatalf("ending message = %+v, error %v; want cause %q", message, err, test.want)
					}
				}
			}
			cursors = harness.poll(t, cursors, notify.KindRunStarted, notify.KindChecksPassed, notify.KindRunEnded)
			harness.poll(t, cursors)
		})
	}
}

// One record nobody can address must not hold up every record behind it for as
// long as the process runs. It is said once in the sink's own log and read past,
// because a channel that goes silent over one malformed line is worse than one
// missing that line.
func TestARecordThatCannotBeAddressedIsReadPastRatherThanRetriedForever(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	var logged []string
	harness.feed.Log = func(format string, _ ...any) { logged = append(logged, format) }
	// A work item identifier with a separator in it names no thread: the key it
	// would make could not be read back into the topic it came from.
	harness.fileOn(t, "report-0123456789abcdef0123456789abcde0", "not: an item", moment)
	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityNote, moment.Add(time.Minute))

	cursors := harness.poll(t, harness.start(), notify.KindReportFiled)
	if len(logged) != 1 {
		t.Fatalf("logged %v, want the record nobody can address said once", logged)
	}
	if cursors.Streams[reportStream].Position != 2 {
		t.Fatalf("cursor = %#v, want the log read past both", cursors.Streams[reportStream])
	}
	harness.poll(t, cursors)
	if len(logged) != 1 {
		t.Fatalf("logged %v, want it said once rather than on every pass", logged)
	}
}

// The operator's two switches are the awkward pair: a hold is a record, and what
// lifts it is only its absence. Both halves are said, because a queue that goes
// quiet is indistinguishable from a broken one until something says which.
// What a watch session is doing is read like every other log — each transition
// once, in the order it happened — and almost none of it is posted. Started,
// idle and stopped are the poll-by-poll narration of a process that spends most
// of its life saying nothing, and they were 473 of the 2,250 measured posts: they
// advance the cursor and stay in the watch log, which is where `yoyo status`
// reads them. Braked is the one an operator has to act on, and it still reaches
// the channel.
func TestWhatAWatchSessionIsDoingStaysInTheLogExceptWhenItStops(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.watched(t, runstate.WatchIdle, "the backlog is empty", moment.Add(time.Minute))
	cursors := harness.poll(t, harness.start())
	if cursors.Streams[watchStream].Position != 2 {
		t.Fatalf("cursor = %#v, want what posts nowhere advanced rather than re-read every pass", cursors.Streams[watchStream])
	}
	cursors = harness.poll(t, cursors)

	harness.watched(t, runstate.WatchBraked, "the operator placed it — the queue is being reordered", moment.Add(2*time.Minute))
	harness.poll(t, cursors, notify.KindWatchBraked)
}

// A session that ran before anybody pointed a channel at this product is
// history: the watermark is read past in one silent advance rather than a
// night's worth of idling arriving at once.
func TestAWatchSessionFromBeforeTheWatermarkIsReadPast(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment.Add(-time.Hour))
	harness.watched(t, runstate.WatchStopped, "the scheduler was cancelled", moment.Add(-time.Minute))
	cursors := harness.poll(t, harness.start())
	if cursors.Streams[watchStream].Position != 2 {
		t.Fatalf("cursor = %#v, want what was read past advanced rather than re-read every pass", cursors.Streams[watchStream])
	}
	// What happens after the watermark is news, whatever came before it — and for
	// this log that means the one transition somebody has to act on.
	harness.watched(t, runstate.WatchBraked, "the operator is holding intake", moment.Add(time.Hour))
	harness.poll(t, cursors, notify.KindWatchBraked)
}

// A provider refusing something that is not a run reaches the channel from the
// log the process that met it wrote, at the weight an exhausted limit deserves.
// Nothing else in the record says it happened: the conversation failed at
// somebody's terminal and there is no run to have parked.
func TestAProviderRefusalOutsideARunReachesTheChannelAtWarning(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	reset := moment.Add(3 * time.Hour)
	harness.refused(t, "the product manager conversation chat-91253e0e", &reset, moment.Add(time.Minute))
	cursors := harness.poll(t, harness.start(), notify.KindUsageLimitExhausted)
	// Read again with nothing new: one refusal is one thing to say, however
	// often the log is read.
	cursors = harness.poll(t, cursors)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	said := batch.Deliveries[0].Notification
	if said.Event.Severity != report.SeverityWarning {
		t.Fatalf("a refusal is said at %q, want %q", said.Event.Severity, report.SeverityWarning)
	}
	message, err := notify.Render(said.Topic, said.Speaker, said.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"the product manager conversation chat-91253e0e", reset.UTC().Format(time.RFC3339)} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("a refusal reads as %q, which does not say %q", message.Body, want)
		}
	}
	// A second refusal is a second thing to say: an operator who released
	// capacity and ran into it again has learned something.
	harness.refused(t, "the independent review review-4d1f of main", nil, moment.Add(2*time.Minute))
	harness.poll(t, cursors, notify.KindUsageLimitExhausted)
}

// A refusal from before anybody pointed a channel at this product is history,
// and is read past in one silent advance like every other log's.
func TestARefusalFromBeforeTheWatermarkIsReadPast(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	harness.refused(t, "the product manager conversation chat-91253e0e", nil, moment.Add(-time.Hour))
	cursors := harness.poll(t, harness.start())
	if cursors.Streams[usageLimitStream].Position != 1 {
		t.Fatalf("cursor = %#v, want what was read past advanced rather than re-read every pass", cursors.Streams[usageLimitStream])
	}
	harness.refused(t, "the product manager conversation chat-91253e0e", nil, moment.Add(time.Hour))
	harness.poll(t, cursors, notify.KindUsageLimitExhausted)
}

func TestAHoldIsSaidWhenItIsPlacedAndAgainWhenItIsLifted(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	if _, err := harness.intake.Hold(runstate.IntakeHolderOperator, "reordering the backlog first", moment); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	// Held twice is the same hold, and it is said once.
	cursors = harness.poll(t, cursors)

	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	// The pair has been said in full and is forgotten, so the product's cursor
	// does not grow a line for every afternoon somebody was away.
	if len(cursors.Streams[productStream].Delivered) != 0 {
		t.Fatalf("cursor = %#v, want a said pair forgotten", cursors.Streams[productStream])
	}
	harness.poll(t, cursors)
}

// The wider hold is the same shape and is said the same way, and the two must
// not be confused for each other: one stops choosing work and the other stops
// everything.
func TestTheOperatorHoldIsSaidSeparatelyFromTheIntakeHold(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	if _, err := harness.holds.Hold(moment); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	cursors := harness.poll(t, harness.start(), notify.KindHoldPlaced)

	if _, _, err := harness.holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	harness.poll(t, cursors, notify.KindHoldLifted)
}

// Somebody who steers from a thread is told what was recorded and then hears
// nothing more, because what becomes of a directive is settled at a terminal
// they are not at. So the record is read for it, and it is said where they asked
// and addressed to them — once, however often the record is read afterwards —
// carrying the message that asked, whose mark stops saying the directive is open
// at the same moment.
func TestWhatBecomesOfADirectiveSaidInAThreadIsSaidInThatThread(t *testing.T) {
	t.Parallel()

	const member = "U0OPERATOR"
	const askTS = "1750000001.000200"
	harness := newTestHarness(t, time.Time{})
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", member, askTS)

	// Nothing has become of it yet. The thread already carries what was recorded,
	// and a directive nobody has settled is not news a second time.
	cursors := harness.poll(t, harness.start())

	if _, err := harness.directives.Resolve(recorded.ID, "the second one, and the design says so", moment); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	said := 0
	for _, delivery := range batch.Deliveries {
		if delivery.Stream != directiveStream {
			continue
		}
		said++
		cursors.Streams[delivery.Stream] = delivery.Cursor
		if delivery.Notification.Event.Kind != notify.KindDirectiveResolved {
			t.Fatalf("said %q, want what became of the directive", delivery.Notification.Event.Kind)
		}
		if delivery.Notification.Topic.Key() != "work-item:yoyodyne-ifd.68.3" {
			t.Fatalf("said in %q, want the thread the directive was asked for in", delivery.Notification.Topic.Key())
		}
		if delivery.Mention != member {
			t.Fatalf("mention = %q, want the human who asked for it tagged", delivery.Mention)
		}
		if delivery.Reply != askTS {
			t.Fatalf("reply = %q, want the message that asked, so its mark can move to settled", delivery.Reply)
		}
		if delivery.Notification.Event.Text != "the second one, and the design says so" {
			t.Fatalf("said %q, want what settled it", delivery.Notification.Event.Text)
		}
		if delivery.Notification.Event.Refs.DirectiveID != recorded.ID {
			t.Fatalf("refs = %#v, want the directive it is about", delivery.Notification.Event.Refs)
		}
	}
	if said != 1 {
		t.Fatalf("said %d outcomes, want exactly one", said)
	}

	// Read again, and it says nothing: a settlement is said once, like every
	// other crossing.
	harness.poll(t, cursors)
}

// The founding case, replayed to its end. A plain reply records an operational
// directive, which pauses nothing and so has nothing to resolve: before the
// harness could record what came of one, that reply was acknowledged, the work
// it prompted was admitted, and the thread was never told the two were the same
// thing. Carrying it out is what closes that, and the thread hears which item
// its directive became.
//
// It is said as carried out rather than as resolved, because the person reading
// it was never waiting for work to resume — nothing had stopped — and a message
// about a lifted pause would describe something that did not happen.
func TestWhatBecameOfAnOperationalDirectiveIsSaidInTheThreadThatAskedForIt(t *testing.T) {
	t.Parallel()

	const member = "U0OPERATOR"
	const askTS = "1750000001.000200"
	const outcome = "admitted yoyodyne-ifd.171 to the backlog: Make the integration retry budget configurable"
	harness := newTestHarness(t, time.Time{})
	recorded := harness.operationalDirective(t, "yoyodyne-ifd.68.3", member, askTS)

	// Nothing has become of it yet, and a directive in force is not news a second
	// time.
	cursors := harness.poll(t, harness.start())

	if _, err := harness.directives.CarryOut(recorded.ID, outcome, moment); err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	said := 0
	for _, delivery := range batch.Deliveries {
		if delivery.Stream != directiveStream {
			continue
		}
		said++
		cursors.Streams[delivery.Stream] = delivery.Cursor
		if delivery.Notification.Event.Kind != notify.KindDirectiveCarriedOut {
			t.Fatalf("said %q, want a directive that paused nothing said as carried out", delivery.Notification.Event.Kind)
		}
		if delivery.Notification.Topic.Key() != "work-item:yoyodyne-ifd.68.3" {
			t.Fatalf("said in %q, want the thread the directive was asked for in", delivery.Notification.Topic.Key())
		}
		if delivery.Mention != member || delivery.Reply != askTS {
			t.Fatalf("delivery = %#v, want the person who asked tagged and their message carried", delivery)
		}
		// The identifier of the work is the whole point: a thread told its
		// directive was acted on is told nothing it can go and read.
		if delivery.Notification.Event.Text != outcome {
			t.Fatalf("said %q, want the item the directive became", delivery.Notification.Event.Text)
		}
		if delivery.Notification.Event.Refs.DirectiveID != recorded.ID {
			t.Fatalf("refs = %#v, want the directive it is about", delivery.Notification.Event.Refs)
		}
	}
	if said != 1 {
		t.Fatalf("said %d outcomes, want exactly one", said)
	}
	harness.poll(t, cursors)
}

// A directive asked for in a thread and later taken back was never answered
// there, because withdrawing is deliberately not a settlement and the outcome
// reading reads settlements. The reply sat wearing the thinking face for as long
// as the record stood, in a thread told the directive was heard and never told
// it was taken back. So the withdrawal is said where they asked, in the voice of
// the role it was taken back under, carrying the reply so its mark moves — once.
func TestAWithdrawnDirectiveSaidInAThreadIsAnsweredInThatThread(t *testing.T) {
	t.Parallel()

	const member = "U0OPERATOR"
	const askTS = "1750000001.000200"
	harness := newTestHarness(t, time.Time{})
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", member, askTS)
	cursors := harness.poll(t, harness.start())

	if _, err := harness.directives.Withdraw(recorded.ID, "the operator, from conversation chat-91253e0e, after turn 557",
		domain.RoleProductManager, "never mind: the work went the other way and the question no longer arises", moment); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	said := 0
	for _, delivery := range batch.Deliveries {
		if delivery.Stream != directiveStream {
			continue
		}
		said++
		cursors.Streams[delivery.Stream] = delivery.Cursor
		if delivery.Notification.Event.Kind != notify.KindDirectiveWithdrawn {
			t.Fatalf("said %q, want the directive said as withdrawn rather than as settled", delivery.Notification.Event.Kind)
		}
		if delivery.Notification.Topic.Key() != "work-item:yoyodyne-ifd.68.3" {
			t.Fatalf("said in %q, want the thread the directive was asked for in", delivery.Notification.Topic.Key())
		}
		// In the voice of the conversation that took it back, not the harness's.
		if delivery.Notification.Speaker.Role != domain.RoleProductManager {
			t.Fatalf("speaker = %#v, want the role the withdrawal was made under", delivery.Notification.Speaker)
		}
		if delivery.Mention != member || delivery.Reply != askTS {
			t.Fatalf("delivery = %#v, want the person who asked tagged and their message carried, so its mark stops saying the directive is open", delivery)
		}
		if !strings.HasPrefix(delivery.Notification.Event.Text, "never mind: the work went the other way") {
			t.Fatalf("said %q, want why it was withdrawn", delivery.Notification.Event.Text)
		}
		if delivery.Notification.Event.Refs.DirectiveID != recorded.ID {
			t.Fatalf("refs = %#v, want the directive it is about", delivery.Notification.Event.Refs)
		}
		if !delivery.Posts() {
			t.Fatalf("delivery = %#v, want a withdrawal posted to the person who asked", delivery)
		}
	}
	if said != 1 {
		t.Fatalf("said %d withdrawals, want exactly one", said)
	}
	// Said once, like every other crossing.
	harness.poll(t, cursors)
}

// A directive that was carried out and later taken back is both things, and the
// thread hears both: what came of it when it did, and that it no longer applies
// when the operator says so. Each is said once, and the second is not swallowed
// by the mark that says the first was said. One taken back by the operator at a
// terminal was taken back under nobody's persona, and the harness says so.
func TestADirectiveCarriedOutAndThenWithdrawnIsAnsweredTwice(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	recorded := harness.operationalDirective(t, "yoyodyne-ifd.68.3", "U0OPERATOR", "1750000001.000200")
	if _, err := harness.directives.CarryOut(recorded.ID, "admitted yoyodyne-ifd.171 to the backlog", moment); err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	cursors := harness.poll(t, harness.start(), notify.KindDirectiveCarriedOut)

	if _, err := harness.directives.Withdraw(recorded.ID, "Mason, at a terminal", "",
		"we open small documentation pull requests again", moment.Add(time.Hour)); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	said := 0
	for _, delivery := range batch.Deliveries {
		if delivery.Stream != directiveStream {
			continue
		}
		said++
		cursors.Streams[delivery.Stream] = delivery.Cursor
		if delivery.Notification.Event.Kind != notify.KindDirectiveWithdrawn {
			t.Fatalf("said %q, want the withdrawal and not the outcome again", delivery.Notification.Event.Kind)
		}
		if !delivery.Notification.Speaker.IsHarness() {
			t.Fatalf("speaker = %#v, want the harness for a withdrawal made under nobody's persona", delivery.Notification.Speaker)
		}
	}
	if said != 1 {
		t.Fatalf("said %d withdrawals, want exactly one", said)
	}
	harness.poll(t, cursors)
}

// A withdrawal from before the watermark is history, exactly as a settlement is.
func TestAWithdrawalFromBeforeTheWatermarkIsNotSaid(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", "U0OPERATOR", "1750000001.000200")
	if _, err := harness.directives.Withdraw(recorded.ID, "Mason, at a terminal", "",
		"withdrawn long before the channel existed", moment); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	harness.poll(t, harness.start())
}

// A settlement that happened before this product's reporting began is history,
// exactly as everything else read from a record is. The per-directive marks live
// in the cursors and the steer map does not, so an operator who starts the
// channel over by deleting one and keeping the other must not be answered by
// name for every directive they ever steered and settled: a flood of mentions
// about work that is long over is the same trust erosion as silence.
func TestASettlementFromBeforeTheWatermarkIsNotSaidAgain(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", "U0OPERATOR", "1750000001.000200")
	if _, err := harness.directives.Resolve(recorded.ID, "settled long before the channel existed", moment); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	// The cursors a sink has when it has read nothing at all, which is exactly
	// what deleting them leaves behind.
	harness.poll(t, harness.start())
}

// A settlement the connection already said in the thread is not said a second
// time by the delivery pass. The two halves post from different goroutines, so
// what the connection wrote down is what stops the pass repeating it.
func TestASettlementTheThreadWasAlreadyToldIsNotSaidByThePass(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", "U0OPERATOR", "1750000001.000200")
	if _, err := harness.directives.Resolve(recorded.ID, "the one already on the target branch", moment); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	steers, err := harness.steers.LoadSteers()
	if err != nil {
		t.Fatalf("LoadSteers() error = %v", err)
	}
	steer, found := steers.Lookup(recorded.ID)
	if !found {
		t.Fatalf("steer for %s is missing, want the one the harness recorded", recorded.ID)
	}
	// What a reply that resolved it in its own thread leaves behind.
	steer.Said = true
	steers.Record(recorded.ID, steer)
	if err := harness.steers.SaveSteers(steers); err != nil {
		t.Fatalf("SaveSteers() error = %v", err)
	}

	harness.poll(t, harness.start())
}

// A directive recorded at a terminal has no thread to answer in and nobody to
// tag, so nothing is said about it here. Reporting on every directive the
// product has would be a channel narrating a record nobody asked it to.
func TestADirectiveNobodySaidInAThreadIsNotAnsweredInOne(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", "U0OPERATOR", "1750000001.000200")
	// The sink's note of where it came from is what makes it answerable, and a
	// directive typed at a terminal never has one.
	if err := harness.steers.SaveSteers(SteerMap{}); err != nil {
		t.Fatalf("SaveSteers() error = %v", err)
	}
	if _, err := harness.directives.Resolve(recorded.ID, "settled at the terminal it was typed at", moment); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	harness.poll(t, harness.start())
}

// testHarness is a product's durable records and a feed reading them, so what a
// test exercises is the reading rather than a stand-in for it.
type testHarness struct {
	// since is the product's watermark, which rides on the cursors rather than on
	// the feed: it is one durable moment for the product rather than one per
	// process, which is what makes downtime a gap the sink reads across.
	since time.Time
	// now is when the feed thinks it is. It moves, because what the sink says
	// about a state rather than an event depends on how long that state has stood.
	now  time.Time
	feed *HarnessFeed
	// root is the state root every store below is rooted at, kept so a test that
	// needs a store the harness does not build by default can open one beside
	// them rather than in a directory of its own.
	root    string
	runs    *runstate.Store
	chats   *runstate.ConversationStore
	reports *runstate.ReportStore
	amend   *runstate.AmendmentStore
	intake  *runstate.IntakeHoldStore
	holds   *runstate.OperatorHoldStore
	watch   *runstate.WatchStore
	limits  *runstate.UsageLimitStore
	// directives is the product's directive record, and steers is the sink's own
	// note of which of those were said into a thread. The outcome half reads both:
	// one says what became of a directive, the other says whose thread to say it
	// in, whom to tag, and which message stops saying it is open.
	directives *runstate.DirectiveStore
	steers     *Store
}

func newTestHarness(t *testing.T, since time.Time) *testHarness {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	chats, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	reports, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	amend, err := runstate.NewAmendmentStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	watch, err := runstate.NewWatchStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	limits, err := runstate.NewUsageLimitStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	directives, err := runstate.NewDirectiveStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewDirectiveStore() error = %v", err)
	}
	steers, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	harness := &testHarness{
		since:      since,
		root:       root,
		now:        moment.Add(time.Hour),
		runs:       runs,
		chats:      chats,
		reports:    reports,
		amend:      amend,
		intake:     intake,
		holds:      holds,
		watch:      watch,
		limits:     limits,
		directives: directives,
		steers:     steers,
	}
	harness.feed = &HarnessFeed{
		Runs:          runs,
		Conversations: chats,
		Reports:       reports,
		Decisions:     runs.Triage(),
		Proposals:     amend,
		Intake:        intake,
		Holds:         holds,
		Watch:         watch,
		UsageLimits:   limits,
		Directives:    directives,
		Steers:        steers,
		Now:           func() time.Time { return harness.now },
	}
	return harness
}

// A brake hold escalated to the operator is said to him directly once, whoever
// escalated it and however many times the record is escalated again: the
// harness at its bound and then the development manager on top of it, her and
// then the harness, her deciding it twice, a hold first read already
// escalated, and a cursor written before the two escalations were one message.
// Each is one hold waiting on one person, and the hourly line carries it after
// the first message.
func TestAnEscalatedBrakeHoldIsSaidToTheOperatorDirectlyOnce(t *testing.T) {
	t.Parallel()

	escalatedAt := moment.Add(2 * time.Hour)
	byHarness := func(t *testing.T, harness *testHarness) {
		t.Helper()
		if _, err := harness.intake.ReviseBrake(func(trip *runstate.IntakeBrake) error {
			ended := escalatedAt
			trip.Probe = &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.405", RunID: "run-5", StartedAt: escalatedAt.Add(-20 * time.Minute), EndedAt: &ended, Blocked: true, Reason: "the checks failed on main"}
			trip.Probes, trip.Cycles, trip.CycleBound = 2, 2, 2
			trip.Escalation = &runstate.BrakeEscalation{At: escalatedAt, Cycles: 2, Probe: "yoyodyne-ifd.405", Reason: "the checks failed on main"}
			return nil
		}); err != nil {
			t.Fatalf("ReviseBrake() error = %v", err)
		}
	}
	byHer := func(t *testing.T, harness *testHarness, at time.Time) {
		t.Helper()
		if _, err := harness.intake.DecideBrake(runstate.BrakeDecisionEscalate, "the checks fail on main and only the operator can say why", "development-manager conversation chat-1, turn 4", at); err != nil {
			t.Fatalf("DecideBrake() error = %v", err)
		}
	}
	// pass makes one poll and returns the cursors after it and every message it
	// sent the operator directly.
	pass := func(t *testing.T, harness *testHarness, cursors Cursors) (Cursors, []Delivery) {
		t.Helper()
		batch, err := harness.feed.Poll(context.Background(), cursors)
		if err != nil {
			t.Fatalf("Poll() error = %v", err)
		}
		var direct []Delivery
		for _, delivery := range batch.Deliveries {
			cursors.Streams[delivery.Stream] = delivery.Cursor
			if delivery.Posts() && delivery.Direct {
				direct = append(direct, delivery)
			}
		}
		return cursors, direct
	}
	// trip places the brake's hold and takes the pass that says the trip.
	trip := func(t *testing.T, harness *testHarness) Cursors {
		t.Helper()
		harness.braked(t, moment)
		cursors, direct := pass(t, harness, harness.start())
		if len(direct) != 1 || direct[0].Notification.Event.Kind != notify.KindIntakeHeld {
			t.Fatalf("direct = %#v, want the trip said to the operator once", direct)
		}
		return cursors
	}

	for _, scenario := range []struct {
		name string
		// escalate makes each escalation in turn; the pass after the first says
		// the escalation, and every pass after that says nothing to him.
		escalate []func(*testing.T, *testHarness)
		kind     notify.Kind
	}{
		{
			name: "the harness and then her",
			escalate: []func(*testing.T, *testHarness){
				byHarness,
				func(t *testing.T, h *testHarness) { byHer(t, h, escalatedAt.Add(10*time.Minute)) },
				func(t *testing.T, h *testHarness) { byHer(t, h, escalatedAt.Add(20*time.Minute)) },
			},
			kind: notify.KindIntakeEscalated,
		},
		{
			name: "her and then the harness",
			escalate: []func(*testing.T, *testHarness){
				func(t *testing.T, h *testHarness) { byHer(t, h, moment.Add(10*time.Minute)) },
				func(t *testing.T, h *testHarness) { byHer(t, h, moment.Add(20*time.Minute)) },
				byHarness,
			},
			kind: notify.KindIntakeHeld,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			harness := newTestHarness(t, time.Time{})
			cursors := trip(t, harness)
			said := 0
			for _, escalate := range scenario.escalate {
				escalate(t, harness)
				var direct []Delivery
				cursors, direct = pass(t, harness, cursors)
				for _, delivery := range direct {
					if delivery.Notification.Event.Kind != scenario.kind || !delivery.Tag {
						t.Fatalf("direct = %#v, want the escalation said as %q and tagged", delivery, scenario.kind)
					}
				}
				said += len(direct)
			}
			if said != 1 {
				t.Fatalf("the escalated hold was said to the operator directly %d time(s), want once", said)
			}
			cursors = harness.poll(t, cursors)
			if _, _, err := harness.intake.Release(); err != nil {
				t.Fatalf("Release() error = %v", err)
			}
			cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
			if marks := cursors.Streams[productStream].Delivered; len(marks) != 0 {
				t.Fatalf("product cursor = %#v, want the hold's marks forgotten with it", marks)
			}
		})
	}

	// A hold first read already escalated is said once, by the trip's own
	// message, which already names the operator as the one to move.
	t.Run("first read already escalated", func(t *testing.T) {
		t.Parallel()
		harness := newTestHarness(t, time.Time{})
		harness.braked(t, moment)
		byHarness(t, harness)
		cursors, direct := pass(t, harness, harness.start())
		if len(direct) != 1 || direct[0].Notification.Event.Kind != notify.KindIntakeHeld {
			t.Fatalf("direct = %#v, want the hold said to the operator once", direct)
		}
		rendered, err := notify.Render(direct[0].Notification.Topic, direct[0].Notification.Speaker, direct[0].Notification.Event)
		if err != nil {
			t.Fatalf("render the hold: %v", err)
		}
		if !strings.Contains(rendered.Body, "Next: the operator's — the harness escalated it") {
			t.Fatalf("hold reads as %q, which does not say the harness escalated it to the operator", rendered.Body)
		}
		byHer(t, harness, escalatedAt.Add(10*time.Minute))
		harness.poll(t, cursors)
	})

	// A sink upgraded over a standing escalation it already said, under either
	// of the marks it used to keep, says nothing more about it.
	t.Run("said before the two were one message", func(t *testing.T) {
		t.Parallel()
		for name, legacy := range map[string]func(runstate.IntakeHold) string{
			"by the harness": func(runstate.IntakeHold) string { return brakeEscalationMark + stamp(escalatedAt) },
			"by her": func(hold runstate.IntakeHold) string {
				return legacyBrakeDecisionMark + stamp(*hold.Brake.DecidedAt)
			},
		} {
			harness := newTestHarness(t, time.Time{})
			harness.braked(t, moment)
			byHarness(t, harness)
			if name == "by her" {
				byHer(t, harness, escalatedAt.Add(10*time.Minute))
			}
			hold, _, err := harness.intake.Held()
			if err != nil {
				t.Fatalf("%s: Held() error = %v", name, err)
			}
			cursors := harness.start()
			cursors.Streams[productStream] = Cursor{}.With(intakeMark + stamp(hold.HeldAt)).With(legacy(hold))
			cursors = harness.poll(t, cursors)
			if _, _, err := harness.intake.Release(); err != nil {
				t.Fatalf("%s: Release() error = %v", name, err)
			}
			cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
			if marks := cursors.Streams[productStream].Delivered; len(marks) != 0 {
				t.Fatalf("%s: product cursor = %#v, want the hold's marks forgotten with it", name, marks)
			}
		}
	})
}

// poll makes one pass, checks it said exactly what was expected, and returns the
// cursors as they stand once every delivery has been taken — which is what the
// sink writes as it posts.
func (h *testHarness) poll(t *testing.T, cursors Cursors, want ...notify.Kind) Cursors {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	advanced := Cursors{SchemaVersion: CursorsSchemaVersion, Since: cursors.Since, Streams: map[string]Cursor{}}
	for stream, cursor := range cursors.Streams {
		advanced.Streams[stream] = cursor
	}
	var said []notify.Kind
	for _, delivery := range batch.Deliveries {
		advanced.Streams[delivery.Stream] = delivery.Cursor
		// A delivery that posts nowhere is not something the pass said, whether
		// selection had nothing to say about it or its reach is the record it came
		// from. Both advance a cursor and neither reaches a reader.
		if !delivery.Posts() {
			continue
		}
		if _, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event); err != nil {
			t.Fatalf("a selected notification could not be said: %v", err)
		}
		said = append(said, delivery.Notification.Event.Kind)
	}
	if len(said) != len(want) {
		t.Fatalf("said %v, want %v", said, want)
	}
	for index, kind := range want {
		if said[index] != kind {
			t.Fatalf("said %v, want %v", said, want)
		}
	}
	return advanced
}

// start is the cursors a sink has on the first pass it ever makes over this
// product: nothing read, and the watermark already taken.
func (h *testHarness) start() Cursors {
	return Cursors{SchemaVersion: CursorsSchemaVersion, Since: h.since, Streams: map[string]Cursor{}}
}

// directive records one directive the way a reply in a thread records it: in
// the product's own directive record, with the sink's note of which thread it
// was said in, by whom, and in which message beside it.
func (h *testHarness) directive(t *testing.T, workItemID, member, messageTS string) directive.Directive {
	t.Helper()
	return h.steered(t, directive.Directive{
		Kind:       directive.KindAmbiguous,
		Text:       "ambiguous: which of the two branches did you mean",
		Unresolved: "which of the two branches did you mean",
	}, workItemID, member, messageTS)
}

// operationalDirective records the directive a plain reply actually makes: in
// force from the moment it arrives, pausing nothing, and settled only by
// somebody carrying it out. It is what most replies are, which is why what
// becomes of one is the case that matters most in this thread.
func (h *testHarness) operationalDirective(t *testing.T, workItemID, member, messageTS string) directive.Directive {
	t.Helper()
	return h.steered(t, directive.Directive{
		Kind: directive.KindOperational,
		Text: "also make the integration retry budget configurable",
	}, workItemID, member, messageTS)
}

// steered records one directive and the sink's note of where it was said, which
// is what makes it answerable later.
func (h *testHarness) steered(t *testing.T, said directive.Directive, workItemID, member, messageTS string) directive.Directive {
	t.Helper()
	id, err := directive.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	recorded := said
	recorded.SchemaVersion = directive.SchemaVersion
	recorded.ID = id
	recorded.ProductID = "yoyodyne"
	recorded.ReceivedBy = domain.RoleProductManager
	recorded.ReceivedAt = moment
	recorded.Scope = []string{workItemID}
	if err := h.directives.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	topic, err := notify.WorkItem(workItemID)
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	steers, err := h.steers.LoadSteers()
	if err != nil {
		t.Fatalf("LoadSteers() error = %v", err)
	}
	steers.Record(id, Steer{Member: member, Topic: topic.Key(), Message: messageTS, RecordedAt: moment})
	if err := h.steers.SaveSteers(steers); err != nil {
		t.Fatalf("SaveSteers() error = %v", err)
	}
	return recorded
}

func (h *testHarness) run(t *testing.T, status runstate.Status) runstate.State {
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
		WorkItemID:    "yoyodyne-ifd.68.3",
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     moment,
		UpdatedAt:     moment,
		Selection: &runstate.Selection{
			By:     runstate.SelectedByDevelopmentManager,
			Reason: "the only ready child of the reporting epic",
			At:     moment,
		},
	}
	if status.Terminal() {
		completed := moment
		state.CompletedAt = &completed
		state.Phase = runstate.PhaseComplete
	}
	return state
}

func (h *testHarness) record(t *testing.T, state runstate.State) {
	t.Helper()
	if err := h.runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

func (h *testHarness) save(t *testing.T, state runstate.State) {
	t.Helper()
	if err := h.runs.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func (h *testHarness) file(t *testing.T, id string, severity report.Severity, at time.Time) {
	t.Helper()
	h.fileAs(t, id, "yoyodyne-ifd.68.3", severity, at)
}

// fileOn files a report against an item named in a way no thread can be keyed
// by, which is the record the sink has to read past rather than wedge on.
func (h *testHarness) fileOn(t *testing.T, id, workItemID string, at time.Time) {
	t.Helper()
	h.fileAs(t, id, workItemID, report.SeverityNote, at)
}

func (h *testHarness) fileAs(t *testing.T, id, workItemID string, severity report.Severity, at time.Time) {
	t.Helper()
	if err := h.reports.Append(report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    workItemID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      severity,
		Message:       "the preserved branch holds work worth cherry-picking",
		RecordedAt:    at,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
}

// handle records what the product manager decided about one report, and
// whether that decision is a finding for the operator.
func (h *testHarness) handle(t *testing.T, id, reason string, needsOperator bool, at time.Time) {
	t.Helper()
	if err := h.reports.Handle(report.Handling{
		SchemaVersion: report.HandlingSchemaVersion,
		ReportID:      id,
		Role:          domain.RoleProductManager,
		Agent:         "product-manager",
		RunID:         "chat-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Reason:        reason,
		RecordedAt:    at,
		NeedsOperator: needsOperator,
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

func (h *testHarness) watched(t *testing.T, state runstate.WatchState, reason string, at time.Time) {
	t.Helper()
	h.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", state, reason, at)
}

// watchedAs records a transition of one named session, so a test can put two
// sessions in one log — which is what the product's log actually holds.
func (h *testHarness) watchedAs(t *testing.T, sessionID string, state runstate.WatchState, reason string, at time.Time) {
	t.Helper()
	if err := h.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         state,
		At:            at,
		Reason:        reason,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

// waitingOnProvider records the poll a session made inside the provider's usage
// window, which is the entry the surfaces read to tell that silence from one
// nothing accounts for.
func (h *testHarness) waitingOnProvider(t *testing.T, reason string, at time.Time, resetsAt *time.Time) {
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

func (h *testHarness) refused(t *testing.T, waiting string, resetsAt *time.Time, at time.Time) {
	t.Helper()
	if err := h.limits.Record(runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            at,
		Waiting:       waiting,
		Kind:          "five-hour",
		ResetsAt:      resetsAt,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func (h *testHarness) propose(t *testing.T, id string, at time.Time) {
	t.Helper()
	if err := h.amend.Append(amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.68.3",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "slack-reporting-design",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "say which persona opens a topic's thread",
		Why:           "opening a thread is nobody's account of anything",
		RaisedAt:      at,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
}

// A conversation is the second producer, and it arrives the way the design said
// one would: the feed reads its log by position and the notifier decides what
// any of it means. Most of the log is the turn itself, and what is said is the
// few records where the queue actually moved.
func TestAConversationSaysWhatItDidToTheBacklogAndNothingElse(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	conversation := harness.converse(t, domain.RoleProductManager)
	harness.chatted(t, conversation, 1, execution.EventAgentMessage, map[string]any{"text": "what was said in the turn"})
	harness.chatted(t, conversation, 2, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":      "create",
			"title":       "Conversation milestones reach Slack",
			"description": "the item's own words",
			"goal":        "Work the harness runs on its own is visible while it runs",
			"reason":      "the backlog moves invisibly today",
		},
		"work_item_id": "yoyodyne-ifd.114",
		"summary":      "admitted yoyodyne-ifd.114 to the backlog",
	})
	harness.chatted(t, conversation, 3, execution.EventProcessOutput, map[string]any{"provider_subtype": "api_retry"})

	cursors := harness.poll(t, harness.start(), notify.KindItemAdmitted)
	// The position moved past the turn as well as past the milestone, so the log
	// is not read from its beginning again on the next pass.
	if position := cursors.Streams[conversationStream(conversation.ConversationID)].Position; position != 3 {
		t.Fatalf("position = %d, want the whole log read", position)
	}
	harness.poll(t, cursors)
}

// What a conversation did before somebody pointed a sink at this product is
// history nobody turned reporting on to read, exactly as a finished run is.
func TestWhatAConversationDidBeforeTheWatermarkIsReadPastSilently(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	conversation := harness.converse(t, domain.RoleProductManager)
	harness.chatted(t, conversation, 1, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":   "reprioritize",
			"id":       "yoyodyne-ifd.99",
			"priority": 1,
			"reason":   "it waits on the epic above it",
		},
		"work_item_id": "yoyodyne-ifd.99",
		"summary":      "set yoyodyne-ifd.99 to priority 1",
	})

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if !delivery.Silent() {
			t.Fatalf("said %s about work that predates reporting", delivery.Notification.Event.Kind)
		}
	}
}

// converse records a conversation for one role, which is what makes its log
// discoverable and tells the notifier whose account the milestones in it are.
func (h *testHarness) converse(t *testing.T, role domain.AgentRole) runstate.Conversation {
	t.Helper()
	id, err := runstate.NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	conversation := runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: id,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Agent:          string(role),
		Role:           role,
		Backend:        domain.BackendClaudeCode,
		StartedAt:      moment,
		UpdatedAt:      moment,
	}
	if err := h.chats.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return conversation
}

func (h *testHarness) chatted(t *testing.T, conversation runstate.Conversation, sequence uint64, eventType execution.EventType, payload any) {
	t.Helper()
	event, err := execution.NewEvent(conversation.ConversationID, sequence, moment, eventType, "harness.chat", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := h.chats.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}
