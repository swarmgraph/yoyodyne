package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// exportTimedOut is a checkout that could not be cut because the tracker's
// export was stopped at its limit: nothing about the work.
func exportTimedOut() error {
	return fmt.Errorf("refresh the current exports in the created worktree: %w", errors.Join(gitworktree.ErrExportsUnprepared, errors.New("prepare the primary exports: bd export failed with status timed_out and exit code -1: exporting every work item was stopped after 30s, at the 30s limit each tracker call is given")))
}

// createFailing answers the first Create calls with the errors it holds, one
// each, and passes every later call to the real manager. It counts the calls of the first
// run that asked.
type createFailing struct {
	WorktreeManager
	mu         sync.Mutex
	errs       []error
	firstRun   string
	firstCalls int
}

func (w *createFailing) Create(ctx context.Context, request gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	w.mu.Lock()
	if w.firstRun == "" {
		w.firstRun = request.RunID
	}
	if request.RunID == w.firstRun {
		w.firstCalls++
	}
	var failure error
	if len(w.errs) > 0 {
		failure, w.errs = w.errs[0], w.errs[1:]
	}
	w.mu.Unlock()
	if failure != nil {
		return gitworktree.Worktree{}, failure
	}
	return w.WorktreeManager.Create(ctx, request)
}

// count is how many times the first run to ask for a checkout asked: a stopped
// document run may be run again as a new run, which asks for its own.
func (w *createFailing) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.firstCalls
}

// publishWithCheckoutFailures publishes one document through a manager whose
// first Creates fail as given, with the pause between tries made instant, and
// returns the manager and the recorded runs.
func publishWithCheckoutFailures(t *testing.T, failures ...error) (*createFailing, []runstate.State, []time.Duration) {
	t.Helper()
	_, _, provider, pipeline, runs := automaticFixture(t)
	retryDesign(provider, "once-more-design")
	var mu sync.Mutex
	var waits []time.Duration
	pipeline.Sleep = func(_ context.Context, delay time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		waits = append(waits, delay)
		return nil
	}
	worktrees := &createFailing{WorktreeManager: pipeline.Worktrees, errs: failures}
	pipeline.Worktrees = worktrees
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	// The run that met the failures, which a document stopped without judging is
	// run again behind as a new run of its own.
	var first []runstate.State
	for _, state := range documentRuns(t, runs) {
		if state.RunID == worktrees.firstRun {
			first = append(first, state)
		}
	}
	return worktrees, first, waits
}

func checkoutRetries(state runstate.State) []runstate.Retry {
	var found []runstate.Retry
	for _, retry := range state.Retries {
		if retry.Boundary == runstate.RetryCheckout {
			found = append(found, retry)
		}
	}
	return found
}

// A checkout that failed on the tracker's export timing out is cut again once,
// and the run goes on from there with the wait on its record.
func TestOnceMoreCutsADocumentCheckoutAgainAfterAnExportTimeout(t *testing.T) {
	t.Parallel()

	worktrees, recorded, waits := publishWithCheckoutFailures(t, exportTimedOut())
	if got := worktrees.count(); got != 2 {
		t.Fatalf("Create calls = %d, want the failed one and one more", got)
	}
	if len(recorded) != 1 {
		t.Fatalf("runs = %d, want one", len(recorded))
	}
	state := recorded[0]
	if state.Status != runstate.StatusSucceeded {
		t.Fatalf("status = %s (%q), want the run to go on past the checkout", state.Status, state.Failure)
	}
	retries := checkoutRetries(state)
	if len(state.Retries) != 1 || len(retries) != 1 || retries[0].Attempt != 1 {
		t.Fatalf("retries = %+v, want exactly one at the checkout", state.Retries)
	}
	if !strings.Contains(retries[0].Failure, "bd export failed") {
		t.Errorf("retry failure = %q, want the export text", retries[0].Failure)
	}
	if len(waits) != 1 || waits[0] != onceMoreWait {
		t.Errorf("waits = %v, want one of %s", waits, onceMoreWait)
	}
}

// A second failure of the same kind stops the run, saying what was tried and
// keeping the tracker's words.
func TestOnceMoreStopsADocumentRunWhoseCheckoutFailsTwice(t *testing.T) {
	t.Parallel()

	worktrees, recorded, _ := publishWithCheckoutFailures(t, exportTimedOut(), exportTimedOut())
	if got := worktrees.count(); got != 2 {
		t.Fatalf("Create calls = %d, want exactly two", got)
	}
	if len(recorded) == 0 {
		t.Fatal("no run recorded")
	}
	state := recorded[0]
	if state.Status == runstate.StatusSucceeded {
		t.Fatalf("status = %s, want the run stopped", state.Status)
	}
	for _, want := range []string{"preparing the run's checkout", "tried once more", "bd export failed"} {
		if !strings.Contains(state.Failure, want) {
			t.Errorf("failure = %q, want it to say %q", state.Failure, want)
		}
	}
	if len(checkoutRetries(state)) != 1 {
		t.Errorf("retries = %+v, want one recorded", state.Retries)
	}
}

// A checkout that fails with an answer about the work is not asked again.
func TestOnceMoreDoesNotCutACheckoutAgainAfterAnAnswer(t *testing.T) {
	t.Parallel()

	worktrees, recorded, waits := publishWithCheckoutFailures(t, errors.New("fatal: invalid reference: main"))
	if got := worktrees.count(); got != 1 {
		t.Fatalf("Create calls = %d, want one", got)
	}
	if len(recorded) == 0 {
		t.Fatal("no run recorded")
	}
	if recorded[0].Status == runstate.StatusSucceeded || len(recorded[0].Retries) != 0 || len(waits) != 0 {
		t.Fatalf("status %s, retries %+v, waits %v; want a stop with nothing retried", recorded[0].Status, recorded[0].Retries, waits)
	}
}

func claimRun(t *testing.T, claimErr error) (*orchestratortest.Tracker, Outcome, error, runstate.State) {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{
		Item:              beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"},
		ClaimFailures:     1,
		TransientClaimErr: claimErr,
	}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Sleep = func(context.Context, time.Duration) error { return nil }
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	var state runstate.State
	if outcome.RunID != "" {
		loaded, loadErr := store.Load(outcome.RunID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		state = loaded
	}
	return tracker, outcome, err, state
}

// A claim that waited out another write's lock is read back, found missing, and
// made once more.
func TestOnceMoreMakesATrackerWriteAgainAfterItWaitedOutALock(t *testing.T) {
	t.Parallel()

	locked := fmt.Errorf("another write to yoyodyne-task held it for longer than 30s, so this one was not made: %w", beads.ErrUnanswered)
	tracker, outcome, err, state := claimRun(t, locked)
	if err != nil {
		t.Fatalf("Run() error = %v, want the claim made again", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("status = %s, want the run to go on", outcome.Status)
	}
	if got := calls(tracker, "claim"); got != 1 {
		t.Errorf("claims landed = %d, want one", got)
	}
	if tracker.ShowCalls == 0 {
		t.Error("the item was never read back before the second claim")
	}
	found := 0
	for _, retry := range state.Retries {
		if retry.Boundary == runstate.RetryTrackerWrite {
			found++
		}
	}
	if found == 0 {
		t.Errorf("retries = %+v, want the claim's retry recorded", state.Retries)
	}
}

// A claim the tracker refuses outright is not made again.
func TestOnceMoreDoesNotMakeARefusedTrackerWriteAgain(t *testing.T) {
	t.Parallel()

	tracker, _, err, _ := claimRun(t, errors.New("bd update failed with status failed and exit code 1: no issue found"))
	if err == nil {
		t.Fatal("Run() succeeded, want the refused claim to stop it")
	}
	if tracker.Claimed || tracker.ClaimFailures != 0 {
		t.Errorf("claimed = %t, failures left = %d, want the one refused claim and no more", tracker.Claimed, tracker.ClaimFailures)
	}
}

func TestPassingFailure(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		err  error
		want bool
	}{
		"a tracker call that did not answer": {fmt.Errorf("another write held it: %w", beads.ErrUnanswered), true},
		"a checkout killed at its limit":     {fmt.Errorf("fetch: %w", gitworktree.ErrCheckoutKilled), true},
		"a connection reset":                 {errors.New("read tcp: connection reset by peer"), true},
		"an answer about the work":           {errors.New("fatal: invalid reference: main"), false},
	} {
		if got := passingFailure(test.err); got != test.want {
			t.Errorf("%s: passingFailure = %t, want %t", name, got, test.want)
		}
	}
}
