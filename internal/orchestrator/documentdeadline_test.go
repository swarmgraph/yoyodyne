package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// slowPublicationFixture is a conversation whose architect writes one design
// that publishes automatically, with a check that marks when it has started and
// then runs until whenStarted has acted on that, under a budget it never
// reaches. The returned path exists once the check is running.
//
// The check waits for the test rather than for a fixed time because a fixed
// time is a race the test can lose: under the race detector on a loaded machine
// the check finished before the test had recorded what it meant to do while the
// check ran, and the run succeeded under a stop nobody had managed to ask for.
// The wait has an end of its own, inside the run's one-minute check limit, so a
// test that never acts still lets the check finish.
func slowPublicationFixture(t *testing.T) (*orchestratortest.Backend, *runstate.Store, chat.Options, string) {
	t.Helper()
	_, _, provider, pipeline, runs := automaticFixture(t)
	retryDesign(provider, "slow-design")
	started := filepath.Join(t.TempDir(), "started")
	pipeline.Config.Execution.CheckTimeout = config.Duration(time.Minute)
	pipeline.Checks = checks.Runner{Process: execution.OSProcessRunner{}, Timeout: time.Minute}
	pipeline.Config.Checks = []string{fmt.Sprintf(
		"touch %q; end=$(( $(date +%%s) + 50 )); while [ ! -e %q ] && [ \"$(date +%%s)\" -lt \"$end\" ]; do sleep 0.05; done",
		started, released(started))}
	options, _ := publicationConversation(t, pipeline, provider)
	return provider, runs, options, started
}

// startWatchBound is how long whenStarted waits for the slow check to start.
// Reaching the check takes a design, a document, and the steps between, which
// under the race detector and a loaded machine can take far longer than when
// the test runs alone, so the bound is set well past the run's own one-minute
// check limit rather than near how long the test usually takes.
const startWatchBound = 5 * time.Minute

// whenStarted calls act once the slow check is running, then lets the check
// finish, and gives up after a bound of its own so nothing it starts outlives
// the test.
func whenStarted(started string, act func()) {
	go func() {
		for end := time.Now().Add(startWatchBound); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(started); err == nil {
				act()
				_ = os.WriteFile(released(started), nil, 0o600)
				return
			}
		}
	}()
}

// released is the file whose appearance lets the slow check finish.
func released(started string) string { return started + ".released" }

// passingDeadline is a message context whose deadline passes when the test says
// so, which lets it pass while the run's check is running rather than at
// whatever step a wall-clock deadline happens to land on.
type passingDeadline struct {
	context.Context
	at time.Time
}

func newPassingDeadline() (passingDeadline, context.CancelFunc) {
	ctx, pass := context.WithCancel(context.Background())
	return passingDeadline{Context: ctx, at: time.Now().Add(time.Minute)}, pass
}

func (d passingDeadline) Deadline() (time.Time, bool) { return d.at, true }

func (d passingDeadline) Err() error {
	if d.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func onlyDocumentRun(t *testing.T, runs *runstate.Store) runstate.State {
	t.Helper()
	recorded := documentRuns(t, runs)
	if len(recorded) != 1 {
		t.Fatalf("runs = %d, want the one run the message started", len(recorded))
	}
	return recorded[0]
}

// A message is bounded by how long its sender waits for an answer. A document
// run it starts outlives that deadline and is bounded by its own limits, so its
// checks finish and the document lands.
func TestADocumentRunOutlivesTheDeadlineOfTheMessageThatStartedIt(t *testing.T) {
	_, runs, options, started := slowPublicationFixture(t)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, pass := newPassingDeadline()
	defer pass()
	whenStarted(started, pass)
	// What the message does after its deadline is not this test's subject; the
	// run it started is.
	_, _ = session.Send(ctx, "Write it.")
	if ctx.Err() == nil {
		t.Fatal("the message's deadline had not passed when the run ended, so the test proves nothing")
	}
	run := onlyDocumentRun(t, runs)
	if run.Status != runstate.StatusSucceeded || run.Integration == nil {
		t.Fatalf("run = %s %s %q, want its checks finished and the document landed", run.Status, run.RecordedStopClass(), run.Failure)
	}
	if strings.Contains(run.Failure, "deadline of the work that started this run") {
		t.Fatalf("run = %q, want no stop over the message's deadline", run.Failure)
	}
}

// Cancelling the message — a person ending the conversation's process — still
// stops the run's checks.
func TestCancellingTheMessageStillStopsItsDocumentRun(t *testing.T) {
	_, runs, options, started := slowPublicationFixture(t)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	whenStarted(started, cancel)
	_, _ = session.Send(ctx, "Write it.")
	run := onlyDocumentRun(t, runs)
	if run.Status == runstate.StatusSucceeded || run.Integration != nil {
		t.Fatalf("run = %s, want it stopped", run.Status)
	}
	if !strings.Contains(run.Failure, "was cancelled") {
		t.Fatalf("run's reason = %q, want its check cancelled", run.Failure)
	}
}

// A stop the development manager asks for still ends a document run whose
// message's deadline has passed, at the run's next boundary.
func TestAStopRequestStillEndsADocumentRunPastItsMessageDeadline(t *testing.T) {
	provider, runs, options, started := slowPublicationFixture(t)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, pass := newPassingDeadline()
	defer pass()
	asked := make(chan error, 1)
	whenStarted(started, func() {
		pass()
		recorded, err := runs.Recorded()
		if err != nil || len(recorded) != 1 {
			asked <- fmt.Errorf("runs = %d, %v", len(recorded), err)
			return
		}
		run := recorded[0]
		asked <- runs.RecordStop(runstate.StopRequest{SchemaVersion: runstate.StopSchemaVersion, ProductID: run.ProductID, RunID: run.RunID, WorkItemID: run.WorkItemID, RequestedAt: time.Now(), RequestedBy: "the development manager", Decision: runstate.TriageDecisionStop, Reason: "superseded"})
	})
	_, _ = session.Send(ctx, "Write it.")
	select {
	case err := <-asked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		// Send has returned, so the run is over and the stop, if it was asked,
		// was recorded moments ago; this only waits out its being passed back.
		t.Fatal("the stop was never asked: the run ended before its check was seen to start")
	}
	run := onlyDocumentRun(t, runs)
	if run.Status != runstate.StatusCancelled || run.Integration != nil || run.RecordedStopClass() != runstate.StopManager {
		t.Fatalf("run = %s %s %q, want it stopped by the development manager's request", run.Status, run.RecordedStopClass(), run.Failure)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 0 {
		t.Fatal("the reviewer was asked about a run somebody stopped")
	}
}
