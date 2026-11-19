package recoverytests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// downTracker is a stand-in tracker whose reads go unanswered while readsDown
// is set, as a `bd` killed at its time limit does, and that answers as the
// tracker under it does otherwise.
type downTracker struct {
	*orchestratortest.Tracker
	readsDown atomic.Bool
}

func (d *downTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	if d.readsDown.Load() {
		return beads.WorkItem{}, fmt.Errorf("bd show %s failed with status timed_out: %w", id, beads.ErrUnanswered)
	}
	return d.Tracker.Show(ctx, id)
}

// An approved change whose promotion stops because the tracker did not answer
// is resumed by the harness's own carry-out once the tracker answers again, and
// lands, with nobody typing a command and nothing spent. While the tracker is
// still down, the carry-out's try writes what it waits for and when onto the
// run, and every surface says the harness resumes it rather than naming a
// command for a person.
func TestAnApprovedChangeTheTrackerStoppedIsResumedByTheHarnessOnceItAnswers(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	flaky := &downTracker{Tracker: tracker}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	// The tracker stops answering once the reviewer has approved the change, so
	// the read the promotion makes of the item is what goes unanswered.
	serve := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := serve(request)
		if request.Role == domain.RoleReviewer {
			flaky.readsDown.Store(true)
		}
		return result, err
	}
	build := func() orchestrator.Pipeline {
		pipeline := testharness.Publishing(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, flaky, provider, []string{"test -f feature.txt"}), provider), forge)
		// The waits are taken without taking the time, so the read's window is
		// spent at once and the run stops as it would after two hours.
		pipeline.Sleep = func(context.Context, time.Duration) error { return nil }
		return pipeline
	}

	outcome, err := build().Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() succeeded; want the promotion stopped by the tracker not answering")
	}
	if outcome.Integration != nil || outcome.ReviewDecision != "approve" {
		t.Fatalf("outcome = integration %#v, review %q; want an approved, unpromoted change", outcome.Integration, outcome.ReviewDecision)
	}
	if outcome.IntegrationStop == nil || outcome.IntegrationStop.Cause != runstate.CauseTransportFailure {
		t.Fatalf("integration stop = %#v (run error %v), want the tracker not answering recorded as the cause", outcome.IntegrationStop, err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.HarnessResumesIntegration() {
		t.Fatalf("the stopped run is not one the harness resumes by itself: %#v", stopped.IntegrationStop)
	}
	left, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	docket := &testharness.MemoryDocket{}
	if _, err := testharness.DocketerOverStore(docket, store, build().Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	if rendered := docket.Entries[0].Render(); !strings.Contains(rendered, "Next mover: the harness") || strings.Contains(rendered, "yoyo triage resume") {
		t.Fatalf("docket entry does not say the harness resumes it by itself:\n%s", rendered)
	}

	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testharness.TestGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	pipeline := build()
	carry := orchestrator.CarryOut{
		Docket: docket, Decisions: store.Triage(), Reruns: store.Reruns(), Runs: store,
		Resumes: orchestrator.HarnessResumedIntegrations{
			Runs: store, Items: tracker,
			Resumer: orchestrator.IntegrationResumer{
				Docket: docket, Runs: store, Intake: testharness.NewIntakeHoldStore(t), Items: flaky, Worktrees: worktrees,
				Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
				Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
					return pipeline.Continue(ctx, workItemID, runID)
				},
			},
		},
	}
	pull := func() (orchestrator.CarriedOut, orchestrator.Outcome, error) {
		t.Helper()
		tasks, err := carry.Outstanding()
		if err != nil {
			t.Fatalf("Outstanding() error = %v", err)
		}
		if len(tasks) != 1 || tasks[0].Decision != orchestrator.DecisionResumeIntegration || tasks[0].RunID != outcome.RunID {
			t.Fatalf("outstanding = %#v, want the harness's own resumption of the stopped run", tasks)
		}
		return carry.Carry(context.Background(), tasks[0])
	}

	// A pull while the tracker is still down tries, writes nothing but what it
	// waits for and when, and leaves the run as it stopped.
	account, _, err := pull()
	if err != nil || account.Carried || !account.Waiting {
		t.Fatalf("Carry() = %#v, %v; want a wait on the tracker", account, err)
	}
	waiting, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if waiting.Status != runstate.StatusFailed || len(waiting.IntegrationResumptions) != 0 {
		t.Fatalf("a try that waited changed the run: %s, resumptions %d", waiting.Status, len(waiting.IntegrationResumptions))
	}
	says := waiting.IntegrationStop.WaitingSays()
	if !strings.Contains(says, "waiting for the tracker, the forge, or the network to answer") || !strings.Contains(says, "last tried at") || waiting.IntegrationStop.LastTriedAt == nil {
		t.Fatalf("the run record says %q, want what the resumption waits for and when it last tried", says)
	}

	// The tracker answers again, and the next pull resumes the run, which lands.
	flaky.readsDown.Store(false)
	account, landed, err := pull()
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !account.Carried || landed.Integration == nil || landed.PullRequest == nil || !landed.PullRequest.Merged {
		t.Fatalf("Carry() = %#v, outcome %#v; want the approved change resumed and merged", account, landed)
	}
	if len(forge.Merges) != 1 || !tracker.Closed {
		t.Fatalf("forge merges = %d, item closed = %t; want one merge and the item closed on it", len(forge.Merges), tracker.Closed)
	}
	if developer, reviewer := provider.RequestsForRole(domain.RoleDeveloper), provider.RequestsForRole(domain.RoleReviewer); len(developer) != 1 || len(reviewer) != 1 {
		t.Fatalf("invocations = %d developer, %d reviewer; want only the first run's", len(developer), len(reviewer))
	}
	after, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if !reflect.DeepEqual(after, left) {
		t.Fatalf("the item's triage record moved across the resumption:\nleft  %#v\nafter %#v", left, after)
	}
	final, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.Status != runstate.StatusSucceeded || final.RepairAttempts != 0 || final.ReviewRounds != 1 || len(final.IntegrationResumptions) != 1 ||
		final.IntegrationResumptions[0].Cause != runstate.CauseTransportFailure {
		t.Fatalf("final run = %s, attempts %d, rounds %d, resumptions %#v; want it landed with nothing charged", final.Status, final.RepairAttempts, final.ReviewRounds, final.IntegrationResumptions)
	}
	// Nothing is left for a later pull to do.
	if tasks, err := carry.Outstanding(); err != nil || len(tasks) != 0 {
		t.Fatalf("Outstanding() = %#v, %v; want nothing left once the change landed", tasks, err)
	}
}

// A stop with any other cause is not resumed by the harness by itself: a
// primary checkout carrying somebody's edit is still the stop `yoyo triage
// resume` is asked for, and the carry-out offers nothing for it.
func TestAStopWithAnotherCauseIsNotResumedByTheHarnessItself(t *testing.T) {
	t.Parallel()

	state := testharness.ApprovedStoppedState()
	if state.IntegrationStop == nil || state.IntegrationStop.Cause.HarnessResumes() {
		t.Fatalf("fixture stop = %#v, want one the harness does not resume by itself", state.IntegrationStop)
	}
	if state.HarnessResumesIntegration() {
		t.Fatal("a dirty-checkout stop reads as one the harness resumes by itself")
	}
	if says := state.IntegrationStop.ResumeSays(state.RunID); !strings.Contains(says, "`yoyo triage resume "+state.RunID+"`") {
		t.Fatalf("ResumeSays() = %q, want the command named as before", says)
	}
	transport := state
	stop := *state.IntegrationStop
	stop.Cause = runstate.CauseTransportFailure
	transport.IntegrationStop = &stop
	if !transport.HarnessResumesIntegration() || strings.Contains(transport.IntegrationStop.ResumeSays(state.RunID), "triage resume") {
		t.Fatal("a stop the tracker caused does not read as one the harness resumes by itself")
	}
	// Once a try met something else, the harness stops trying, and the stop is
	// said as every other one is.
	transport.NoteHarnessResumeRefused("the worktree is not as the harness left it", testharness.DocketedNow)
	if transport.HarnessResumesIntegration() || !strings.Contains(transport.IntegrationStop.ResumeSays(state.RunID), "`yoyo triage resume") {
		t.Fatal("a stop the harness stopped trying to resume still reads as its own")
	}
}
