package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeCIForge is the forge a run pushes to and waits on: it records every push
// and answers every read of the required checks with one result.
type fakeCIForge struct {
	mu     sync.Mutex
	pushes []publish.PushRequest
	drafts int
	result publish.CheckResult
}

func (f *fakeCIForge) PushBranch(_ context.Context, request publish.PushRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, request)
	return nil
}

func (f *fakeCIForge) EnsureDraft(_ context.Context, request publish.Request) (publish.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drafts++
	return publish.PullRequest{Number: 7, URL: "https://forge.example/pull/7", State: "OPEN"}, nil
}

func (f *fakeCIForge) RequiredChecks(_ context.Context, commit string, required []string) (publish.RequiredCheckReading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reading := publish.RequiredCheckReading{Commit: commit}
	for _, name := range required {
		reading.Checks = append(reading.Checks, publish.RequiredCheck{Name: name, Result: f.result, Commit: commit})
	}
	return reading, nil
}

func (f *fakeCIForge) setResult(result publish.CheckResult) {
	f.mu.Lock()
	f.result = result
	f.mu.Unlock()
}

func (f *fakeCIForge) pushed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pushes)
}

type ciFixture struct {
	repository, worktreeRoot string
	store                    *runstate.Store
	tracker                  *orchestratortest.Tracker
	forge                    *fakeCIForge
	shortLog, fullLog        string
	providers                []*orchestratortest.Backend
	mu                       sync.Mutex
}

func newCIFixture(t *testing.T) *ciFixture {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	dir := t.TempDir()
	return &ciFixture{
		repository: repository, worktreeRoot: worktreeRoot, store: store,
		tracker:  &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}},
		forge:    &fakeCIForge{result: publish.CheckPending},
		shortLog: filepath.Join(dir, "short.log"), fullLog: filepath.Join(dir, "full.log"),
	}
}

// pipeline is a harness process of its own over the fixture's repository and
// run store, with checks on CI switched on or off.
func (f *ciFixture) pipeline(t *testing.T, enabled bool) Pipeline {
	t.Helper()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	f.mu.Lock()
	f.providers = append(f.providers, provider)
	f.mu.Unlock()
	short := "echo short >> '" + f.shortLog + "'"
	full := "echo full >> '" + f.fullLog + "'"
	pipeline := automatic(newSharedPipeline(t, f.repository, f.worktreeRoot, f.store, f.tracker, provider, []string{short, full}), provider)
	pipeline.Config.Execution.MaxConcurrentDevelopers = 1
	pipeline.Config.Execution.CIChecks.Enabled = enabled
	pipeline.Config.Execution.CIChecks.Local = []string{short}
	pipeline.Config.Execution.CIChecks.Required = []string{"test", "race"}
	pipeline.CIForge = f.forge
	return pipeline
}

func (f *ciFixture) reviewed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	reviews := 0
	for _, provider := range f.providers {
		reviews += len(provider.RequestsForRole(domain.RoleReviewer))
	}
	return reviews
}

func logLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func (f *ciFixture) park(t *testing.T) (Outcome, runstate.State) {
	t.Helper()
	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Paused || outcome.PausedByCIWait == nil {
		t.Fatalf("outcome = %#v, want a run waiting for the forge's checks", outcome)
	}
	state, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return outcome, state
}

// With the switch on, only the short check runs before review; the candidate
// commit is pushed; the wait records the commit, the push time and the required
// checks; the run holds no developer slot; and no review has started.
func TestChecksOnCIPushesRecordsTheWaitAndGivesBackTheSlot(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, state := f.park(t)

	if logLines(t, f.shortLog) != 1 || logLines(t, f.fullLog) != 0 {
		t.Fatalf("short check ran %d times and full check %d times, want 1 and 0", logLines(t, f.shortLog), logLines(t, f.fullLog))
	}
	if f.forge.pushed() != 1 || f.forge.pushes[0].Commit != state.HarnessCommit {
		t.Fatalf("pushes = %#v, want exactly the candidate commit %q", f.forge.pushes, state.HarnessCommit)
	}
	wait := state.CIWait
	if wait == nil || wait.Commit != state.HarnessCommit || wait.PushedAt.IsZero() || strings.Join(wait.Required, ",") != "test,race" {
		t.Fatalf("recorded wait = %#v, want the commit, the push time and the required checks", wait)
	}
	if state.HoldsDeveloperSlot() {
		t.Fatal("the run holds a developer slot while it waits")
	}
	if f.reviewed() != 0 {
		t.Fatalf("review started %d times before the forge's checks passed", f.reviewed())
	}
}

// After a restart during the wait, a process that finds the forge still running
// pushes nothing, starts no review, and leaves the wait as it was.
func TestChecksOnCIRestartDuringTheWaitPushesNothingAndKeepsWaiting(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)

	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Paused || outcome.PausedByCIWait == nil {
		t.Fatalf("outcome = %#v, want the run still waiting", outcome)
	}
	again, _ := f.store.Load(parked.RunID)
	if again.CIWait == nil || again.CIWait.Commit != parked.CIWait.Commit || !again.CIWait.PushedAt.Equal(parked.CIWait.PushedAt) || again.CIWait.SlotTaken {
		t.Fatalf("wait after restart = %#v, want the same wait with no slot held (was %#v)", again.CIWait, parked.CIWait)
	}
	if f.forge.pushed() != 1 || f.reviewed() != 0 || logLines(t, f.fullLog) != 0 {
		t.Fatalf("pushes=%d reviews=%d full checks=%d, want 1, 0, 0", f.forge.pushed(), f.reviewed(), logLines(t, f.fullLog))
	}
}

// Review starts only once every required check passed on the commit, and a
// restart then pushes nothing and runs no check again.
func TestChecksOnCIReviewStartsOnlyOnceEveryRequiredCheckPassed(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	f.park(t)
	f.forge.setResult(publish.CheckPassed)

	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Paused || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to review and finish", outcome)
	}
	if f.reviewed() != 1 || f.forge.pushed() != 1 {
		t.Fatalf("reviews=%d pushes=%d, want 1 and 1", f.reviewed(), f.forge.pushed())
	}
	if logLines(t, f.shortLog) != 1 || logLines(t, f.fullLog) != 0 {
		t.Fatalf("short=%d full=%d, want the short check once and no full check", logLines(t, f.shortLog), logLines(t, f.fullLog))
	}
}

// Switching the setting off during the wait stops it: the full local checks run
// on the same commit, and nothing is pushed.
func TestChecksOnCISwitchedOffDuringTheWaitRunsTheFullChecksOnTheSameCommit(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)

	outcome, err := f.pipeline(t, false).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Paused || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to finish", outcome)
	}
	if logLines(t, f.fullLog) != 1 || f.forge.pushed() != 1 {
		t.Fatalf("full checks=%d pushes=%d, want the full checks once and no second push", logLines(t, f.fullLog), f.forge.pushed())
	}
	end, _ := f.store.Load(parked.RunID)
	if end.CIWaitEnd == nil || end.CIWaitEnd.Commit != parked.CIWait.Commit || end.CIWaitEnd.Passed {
		t.Fatalf("how the wait ended = %#v", end.CIWaitEnd)
	}
}

// A required check that failed on the forge sends the run to the full local
// checks on the same commit; the wait limit passing does the same.
func TestChecksOnCIAFailedRequiredCheckFallsBackToTheFullChecks(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	f.park(t)
	f.forge.setResult(publish.CheckFailed)

	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Paused || logLines(t, f.fullLog) != 1 || f.forge.pushed() != 1 {
		t.Fatalf("outcome=%#v full=%d pushes=%d, want the full checks once over the same commit", outcome, logLines(t, f.fullLog), f.forge.pushed())
	}
}

func TestCIWaitDecidedOnReadings(t *testing.T) {
	t.Parallel()
	pushed := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	wait := runstate.CIWait{Commit: strings.Repeat("a", 40), Branch: "b", PushedAt: pushed, Required: []string{"test"}, WaitLimitSeconds: 600}
	reading := func(result publish.CheckResult) publish.RequiredCheckReading {
		return publish.RequiredCheckReading{Commit: wait.Commit, Checks: []publish.RequiredCheck{{Name: "test", Result: result, Commit: wait.Commit}}}
	}
	for name, test := range map[string]struct {
		result publish.CheckResult
		at     time.Time
		want   bool
	}{
		"passed":               {publish.CheckPassed, pushed.Add(time.Minute), true},
		"running within limit": {publish.CheckPending, pushed.Add(time.Minute), false},
		"running past limit":   {publish.CheckPending, pushed.Add(11 * time.Minute), true},
		"not started in limit": {publish.CheckNotStarted, pushed.Add(time.Minute), false},
		"failed":               {publish.CheckFailed, pushed.Add(time.Minute), true},
		"cancelled":            {publish.CheckCancelled, pushed.Add(time.Minute), true},
	} {
		if got, _ := ciWaitDecided(wait, reading(test.result), test.at); got != test.want {
			t.Errorf("%s: decided = %v, want %v", name, got, test.want)
		}
	}
}

func TestACIWaitHoldsNoSlotUntilItTakesOneBack(t *testing.T) {
	t.Parallel()
	state := runstate.State{Status: runstate.StatusRunning, CIWait: &runstate.CIWait{Commit: "c", Branch: "b", PushedAt: time.Now(), Required: []string{"test"}}}
	if state.HoldsDeveloperSlot() {
		t.Fatal("a waiting run holds a developer slot")
	}
	state.CIWait.SlotTaken = true
	if !state.HoldsDeveloperSlot() {
		t.Fatal("a run that took its slot back holds none")
	}
}
