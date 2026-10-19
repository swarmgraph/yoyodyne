package orchestrator

// A developer turn that ended while work it had started in the background was
// still running used to be taken as the attempt's answer: its change went to
// the checks unfinished, and what they found was charged to a repair attempt.
// The regression case is run-790a9076 (yoyodyne-ifd.429.14.1), two of whose
// three attempts ended "waiting for the timing runs".

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// endedAtBound is what the runner reports for a session whose background work
// was still running when the wait after its reply ran out.
func endedAtBound() *execution.AfterReply {
	return &execution.AfterReply{
		RepliedAt:     time.Date(2026, 10, 17, 21, 0, 0, 0, time.UTC),
		BoundSeconds:  300,
		Outcome:       execution.AfterReplyEnded,
		WaitedSeconds: 300,
	}
}

// harnessSaid is every sentence the harness wrote into a run's event stream
// about the developer's background work.
func harnessSaid(t *testing.T, store *runstate.Store, runID string) []string {
	t.Helper()
	events, err := store.LoadEvents(runID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	var said []string
	for _, event := range events {
		if event.Source == "harness" && strings.Contains(string(event.Payload), "started in the background") {
			said = append(said, string(event.Payload))
		}
	}
	return said
}

// The developer ends its first turn with its background work still running. The
// turn is sent back in the same session, within the same attempt, before
// anything is checked or reviewed; the developer finishes, and the run goes on
// exactly as one whose developer finished the first time, with no repair
// attempt spent and the record saying why the developer was asked twice.
func TestATurnEndingOnItsOwnBackgroundWorkIsSentBackWithoutSpendingARepair(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{
		"Added feature.txt. Waiting for the timing runs to finish before I report the numbers.",
		"Added feature.txt and ran the configured check in the foreground, which passes. No risk outstanding.",
	}
	provider.DeveloperAfterReplyByAttempt = []*execution.AfterReply{endedAtBound()}
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("a developer sent back for its background work did not finish its run: %#v", outcome)
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want a send-back that spends none", outcome.RepairAttempts)
	}
	if outcome.Summary != "Added feature.txt and ran the configured check in the foreground, which passes. No risk outstanding." {
		t.Fatalf("summary = %q, want the account the finished turn gave", outcome.Summary)
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 2 {
		t.Fatalf("the developer was invoked %d time(s), want the first turn and one send-back", len(requests))
	}
	// Only the finished turn reaches the reviewer.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("the change was reviewed %d time(s), want once, after the send-back", reviews)
	}
	sentBack := requests[1]
	if sentBack.SessionID != provider.DeveloperSession {
		t.Fatalf("the send-back ran in session %q, want the session that did the work", sentBack.SessionID)
	}
	for _, want := range []string{
		"Your turn ended while your own background work was still running",
		"the harness ended them",
		"in the foreground",
		"spends no repair attempt",
	} {
		if !strings.Contains(sentBack.Prompt, want) {
			t.Fatalf("the send-back does not say %q:\n%s", want, sentBack.Prompt)
		}
	}
	said := harnessSaid(t, store, outcome.RunID)
	if len(said) != 1 || !strings.Contains(said[0], "sent back to the developer") || !strings.Contains(said[0], "no repair attempt is spent") {
		t.Fatalf("the run's record does not say why the developer was asked again: %q", said)
	}
}

// The developer is sent back once and ends the same way again. The harness has
// already ended what it left, and asking a third time would spend another
// invocation on the same question, so the reply is taken as it stands and the
// record says so.
func TestATurnSentBackForBackgroundWorkIsTakenAsItStandsTheSecondTime(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	provider.DeveloperFinalText = "Added feature.txt; the configured check passes. The timing runs are left running."
	provider.DeveloperAfterReplyByAttempt = []*execution.AfterReply{endedAtBound(), endedAtBound()}
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.RepairAttempts != 0 {
		t.Fatalf("outcome = %#v, want a run that went on with no repair attempt spent", outcome)
	}
	if invocations := len(provider.RequestsForRole(domain.RoleDeveloper)); invocations != 2 {
		t.Fatalf("the developer was invoked %d time(s), want the turn and one send-back", invocations)
	}
	said := harnessSaid(t, store, outcome.RunID)
	if len(said) != 2 || !strings.Contains(said[1], "taken as it stands") || !strings.Contains(said[1], "the harness ended them") {
		t.Fatalf("the run's record does not say what was ended and why the reply was taken: %q", said)
	}
}

// A turn whose session exited with its reply is the ordinary case and is
// untouched: one invocation, and nothing said about background work.
func TestAnOrdinaryTurnIsNotSentBackForBackgroundWork(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want a succeeded run", outcome)
	}
	if invocations := len(provider.RequestsForRole(domain.RoleDeveloper)); invocations != 1 {
		t.Fatalf("the developer was invoked %d time(s), want one", invocations)
	}
	if said := harnessSaid(t, store, outcome.RunID); len(said) != 0 {
		t.Fatalf("an ordinary turn was recorded as leaving background work: %q", said)
	}
}

// What the record and the send-back say about the work left running, for both
// ways it can end.
func TestBackgroundWorkEndingSaysWhatBecameOfTheWork(t *testing.T) {
	t.Parallel()

	if got := backgroundWorkEnding(*endedAtBound()); !strings.Contains(got, "still running 5 minutes after the reply, so the harness ended them") {
		t.Fatalf("ended account = %q", got)
	}
	exited := execution.AfterReply{Outcome: execution.AfterReplyExited, WaitedSeconds: 120, BoundSeconds: 300}
	if got := backgroundWorkEnding(exited); !strings.Contains(got, "finished on their own 2 minutes after the reply") {
		t.Fatalf("exited account = %q", got)
	}
}
