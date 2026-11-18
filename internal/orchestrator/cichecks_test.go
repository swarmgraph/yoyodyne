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
	// then, where set, is the result every read after the first answers with,
	// so a test can have the forge answer once the run has stopped waiting.
	then    publish.CheckResult
	reads   int
	readErr error
	pushErr error
	// failOn is a commit the forge reports failing whatever result says, so a
	// repaired commit can pass where the one before it failed.
	failOn string
	// outputReads counts reads of a failing check's output.
	outputReads int
	// digest is the workflow digest the forge gives; reading another commit is
	// answered as if the forge's checks had run on readCommit.
	digest     string
	readCommit string
}

func (f *fakeCIForge) WorkflowDigest(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.digest == "" {
		return "sha256:workflows-v1", nil
	}
	return f.digest, nil
}

func (f *fakeCIForge) PushBranch(_ context.Context, request publish.PushRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pushErr != nil {
		return f.pushErr
	}
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
	if f.readErr != nil {
		return publish.RequiredCheckReading{}, f.readErr
	}
	result := f.result
	if f.reads > 0 && f.then != "" {
		result = f.then
	}
	f.reads++
	if f.readCommit != "" {
		commit = f.readCommit
	}
	reading := publish.RequiredCheckReading{Commit: commit}
	for _, name := range required {
		result := result
		if f.failOn != "" {
			result = publish.CheckPassed
			if commit == f.failOn {
				result = publish.CheckFailed
			}
		}
		reading.Checks = append(reading.Checks, publish.RequiredCheck{Name: name, Result: result, Commit: commit, ID: 11})
	}
	return reading, nil
}

func (f *fakeCIForge) CheckOutput(_ context.Context, check publish.RequiredCheck) (publish.CheckOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputReads++
	return publish.CheckOutput{FailingTests: []string{"TestWidget"}, Tail: "--- FAIL: TestWidget (0.00s)\nFAIL\n"}, nil
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
	developed                int
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
		f.mu.Lock()
		f.developed++
		content := fmt.Sprintf("implemented %d\n", f.developed)
		f.mu.Unlock()
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(content), 0o600)
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
	// The evidence from the short check alone is narrowed, so nothing lands on it:
	// the whole candidate is checked on this machine before it is promoted.
	if logLines(t, f.shortLog) != 2 || logLines(t, f.fullLog) != 1 {
		t.Fatalf("short=%d full=%d, want the short check before the push and every configured check once over the candidate", logLines(t, f.shortLog), logLines(t, f.fullLog))
	}
}

// Two pulls a few seconds apart read the forge once for one waiting run.
func TestForgeCIWaitsReadsTheForgeOncePerMinuteAcrossReaders(t *testing.T) {
	t.Parallel()
	forge := &fakeCIForge{result: publish.CheckPending}
	counting := &countingForge{fakeCIForge: forge}
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	state := runstate.State{RunID: "run-poll-test", CIWait: &runstate.CIWait{Commit: strings.Repeat("b", 40), Branch: "b", PushedAt: now, Required: []string{"test"}, WaitLimitSeconds: 3600}}
	for _, at := range []time.Time{now.Add(time.Second), now.Add(5 * time.Second)} {
		reader := &ForgeCIWaits{Forge: counting, Enabled: func() bool { return true }, Now: func() time.Time { return at }}
		if decided, _, err := reader.Decided(context.Background(), state); err != nil || decided {
			t.Fatalf("Decided() = %v, %v, want not decided", decided, err)
		}
	}
	if counting.reads != 1 {
		t.Fatalf("the forge was read %d times by two pulls seconds apart, want 1", counting.reads)
	}
}

type countingForge struct {
	*fakeCIForge
	reads int
}

func (c *countingForge) RequiredChecks(ctx context.Context, commit string, required []string) (publish.RequiredCheckReading, error) {
	c.reads++
	return c.fakeCIForge.RequiredChecks(ctx, commit, required)
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

// A required check that failed on the forge goes back to the developer as a
// failed check, spends one repair round, runs the short check again, and the
// repaired commit is pushed and needs a result of its own.
func TestChecksOnCIAFailedRequiredCheckGoesBackToTheDeveloperAndSpendsOneRepairRound(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)
	f.forge.mu.Lock()
	f.forge.failOn = parked.CIWait.Commit
	f.forge.mu.Unlock()

	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Paused || outcome.PausedByCIWait == nil {
		t.Fatalf("outcome = %#v, want the repaired commit waiting for its own result", outcome)
	}
	state, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want exactly one", state.RepairAttempts)
	}
	if state.CIWait.Commit == parked.CIWait.Commit || f.forge.pushed() != 2 {
		t.Fatalf("waiting on %s after %d pushes, want a new commit pushed", state.CIWait.Commit, f.forge.pushed())
	}
	if logLines(t, f.shortLog) != 2 || logLines(t, f.fullLog) != 0 || f.reviewed() != 0 {
		t.Fatalf("short=%d full=%d reviews=%d, want the short check again and nothing else", logLines(t, f.shortLog), logLines(t, f.fullLog), f.reviewed())
	}
	// The developer's repair prompt carries what was recorded: the failing
	// checks, the failing tests, the end of the output and the commit.
	var prompt string
	f.mu.Lock()
	for _, provider := range f.providers {
		for _, request := range provider.RequestsForRole(domain.RoleDeveloper) {
			if strings.Contains(request.Prompt, "repair attempt 1 of") {
				prompt = request.Prompt
			}
		}
	}
	f.mu.Unlock()
	for _, want := range []string{"checks on the forge: test, race", "TestWidget", "--- FAIL: TestWidget", parked.CIWait.Commit} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt lacks %q:\n%s", want, prompt)
		}
	}
	if state.CIWaitEnd == nil || state.CIWaitEnd.Passed || state.CIWaitEnd.Commit != parked.CIWait.Commit {
		t.Fatalf("how the first wait ended = %#v", state.CIWaitEnd)
	}
	// The earlier commit's failure does not carry forward: the new commit passes
	// on its own result and review then goes ahead.
	end, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil || end.Paused || end.Status != runstate.StatusSucceeded {
		t.Fatalf("after the repaired commit passed: outcome=%#v err=%v", end, err)
	}
	if f.reviewed() != 1 {
		t.Fatalf("reviews = %d, want 1", f.reviewed())
	}
}

func TestCIFailureResultKeepsTheOutputWithinTheRecordedBound(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a line of output that is long enough\n", 1000)
	failed := []publish.RequiredCheck{{Name: "test", Result: publish.CheckFailed}, {Name: "race", Result: publish.CheckFailed}}
	result := ciFailureResult(strings.Repeat("c", 40), failed, []publish.CheckOutput{{Tail: big, Cut: true}, {Tail: big}})
	if len(result.FailureOutput) > runstate.MaxCheckOutputBytes {
		t.Fatalf("output is %d bytes, over %d", len(result.FailureOutput), runstate.MaxCheckOutputBytes)
	}
	if !strings.Contains(result.FailureOutput, "Check test") || !strings.Contains(result.FailureOutput, "Check race") {
		t.Fatalf("each failing check should keep a share of its output: %q", result.FailureOutput[:200])
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

// overdue moves a parked run's push back past its wait limit, as if the forge
// had taken longer than the limit to answer.
func (f *ciFixture) overdue(t *testing.T, runID string) {
	t.Helper()
	state, err := f.store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	state.CIWait.PushedAt = state.CIWait.PushedAt.Add(-2 * time.Hour)
	if err := f.store.Save(state); err != nil {
		t.Fatal(err)
	}
}

// Each way the forge can leave the wait undecided — a required check cancelled,
// skipped or in another state, still running or never started past the wait
// limit, or a forge that cannot be read — runs the full declared checks on this
// machine over the same commit, records why on the run, spends no repair round,
// and goes on to review.
func TestChecksOnCIFallsBackToTheFullChecksWithoutSpendingARepairRound(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		result  publish.CheckResult
		overdue bool
		readErr error
		says    string
	}{
		"cancelled":            {result: publish.CheckCancelled, says: "test ended cancelled on the forge"},
		"skipped":              {result: publish.CheckSkipped, says: "test ended skipped on the forge"},
		"another state":        {result: publish.CheckOther, says: "test ended other on the forge"},
		"running past limit":   {result: publish.CheckPending, overdue: true, says: "test had not finished within the wait limit"},
		"never started":        {result: publish.CheckNotStarted, overdue: true, says: "test had not finished within the wait limit"},
		"forge cannot be read": {readErr: errors.New("gh: HTTP 502"), says: "the forge could not be read: gh: HTTP 502"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newCIFixture(t)
			_, parked := f.park(t)
			f.forge.mu.Lock()
			f.forge.result, f.forge.readErr = test.result, test.readErr
			f.forge.mu.Unlock()
			if test.overdue {
				f.overdue(t, parked.RunID)
			}

			outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Paused || outcome.Status != runstate.StatusSucceeded {
				t.Fatalf("outcome = %#v, want the run to fall back, be reviewed and finish", outcome)
			}
			if logLines(t, f.fullLog) != 1 || f.forge.pushed() != 1 || f.reviewed() != 1 {
				t.Fatalf("full checks=%d pushes=%d reviews=%d, want the full checks once over the same commit, no second push, one review",
					logLines(t, f.fullLog), f.forge.pushed(), f.reviewed())
			}
			end, _ := f.store.Load(parked.RunID)
			if end.RepairAttempts != 0 || end.CheckFailure != nil {
				t.Fatalf("repair attempts=%d check failure=%#v, want no repair round spent", end.RepairAttempts, end.CheckFailure)
			}
			if end.CIWaitEnd == nil || end.CIWaitEnd.Passed || end.CIWaitEnd.Commit != parked.CIWait.Commit ||
				!strings.HasPrefix(end.CIWaitEnd.Says, test.says) || !strings.Contains(end.CIWaitEnd.Says, "full checks run on this machine") {
				t.Fatalf("how the wait ended = %#v, want the reason %q recorded", end.CIWaitEnd, test.says)
			}
			if end.CIWaitEnd.Late != nil {
				t.Fatalf("later answer = %#v, want none recorded while the forge has not answered", end.CIWaitEnd.Late)
			}
		})
	}
}

// A forge that answers after the run fell back has its answer recorded beside
// the local result: a pass is kept on the run, and a failure counts as a
// failure even though the full checks passed on this machine.
func TestChecksOnCIRecordsTheForgesLaterAnswerAndCountsItsFailure(t *testing.T) {
	t.Parallel()
	t.Run("passed", func(t *testing.T) {
		t.Parallel()
		f := newCIFixture(t)
		_, parked := f.park(t)
		f.forge.mu.Lock()
		f.forge.result, f.forge.then = publish.CheckCancelled, publish.CheckPassed
		f.forge.mu.Unlock()

		outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
		if err != nil || outcome.Status != runstate.StatusSucceeded {
			t.Fatalf("Run() = %#v, %v, want the run to finish", outcome, err)
		}
		end, _ := f.store.Load(parked.RunID)
		if end.CIWaitEnd == nil || end.CIWaitEnd.Late == nil || !end.CIWaitEnd.Late.Passed || end.RepairAttempts != 0 {
			t.Fatalf("how the wait ended = %#v, repairs=%d, want the forge's later pass recorded and no repair", end.CIWaitEnd, end.RepairAttempts)
		}
	})
	t.Run("failed", func(t *testing.T) {
		t.Parallel()
		f := newCIFixture(t)
		_, parked := f.park(t)
		f.forge.mu.Lock()
		f.forge.result, f.forge.then = publish.CheckCancelled, publish.CheckFailed
		f.forge.mu.Unlock()

		// The fixture's developer repairs nothing, so the run spends its repair
		// rounds on the forge's failure and stops; that stop is not the question.
		_, _ = f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
		if logLines(t, f.fullLog) < 1 {
			t.Fatal("the full checks never ran on this machine")
		}
		end, _ := f.store.Load(parked.RunID)
		if end.RepairAttempts < 1 || f.reviewed() != 0 {
			t.Fatalf("repair attempts=%d reviews=%d, want the forge's later failure to spend repair rounds before any review", end.RepairAttempts, f.reviewed())
		}
		if end.CIWaitEnd == nil || end.CIWaitEnd.Commit != parked.CIWait.Commit {
			t.Fatalf("how the wait ended = %#v", end.CIWaitEnd)
		}
		// The developer's repair prompt is what proves the forge's failure was
		// handed back.
		var handedBack bool
		for _, provider := range f.providers {
			for _, request := range provider.RequestsForRole(domain.RoleDeveloper) {
				if strings.Contains(request.Prompt, "failed on the forge for the same commit") {
					handedBack = true
				}
			}
		}
		if !handedBack {
			t.Fatal("the forge's later failure never reached the developer")
		}
	})
}

// A push the forge refuses holds the run for the development manager with the
// forge's own words, runs nothing on this machine instead, and spends no repair
// round.
func TestChecksOnCIARefusedPushHoldsTheRunForTheDevelopmentManager(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	f.forge.pushErr = errors.New("remote: Permission to swarmgraph/yoyodyne.git denied")

	outcome, _ := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if outcome.Paused || outcome.Status == runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run held", outcome)
	}
	if !f.tracker.Blocked || !strings.Contains(f.tracker.BlockReason, "Permission to swarmgraph/yoyodyne.git denied") {
		t.Fatalf("blocked=%v reason=%q, want the item blocked with the forge's refusal", f.tracker.Blocked, f.tracker.BlockReason)
	}
	if logLines(t, f.fullLog) != 0 || f.reviewed() != 0 {
		t.Fatalf("full checks=%d reviews=%d, want neither", logLines(t, f.fullLog), f.reviewed())
	}
	state, _ := f.store.Load(outcome.RunID)
	if state.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want none", state.RepairAttempts)
	}
}

// A forge that fails one read is waited out; one that stays unreadable for
// ciUnreadableFor decides the wait, so the run falls back without waiting out
// the whole wait limit.
func TestForgeCIWaitsDecidesAWaitOnAForgeThatStaysUnreadable(t *testing.T) {
	t.Parallel()
	forge := &fakeCIForge{readErr: errors.New("gh: connection reset")}
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	state := runstate.State{RunID: "run-unreadable-test", CIWait: &runstate.CIWait{Commit: strings.Repeat("c", 40), Branch: "b", PushedAt: now, Required: []string{"test"}, WaitLimitSeconds: 3600}}
	read := func(at time.Time) (bool, error) {
		reader := &ForgeCIWaits{Forge: forge, Enabled: func() bool { return true }, Now: func() time.Time { return at }}
		decided, _, err := reader.Decided(context.Background(), state)
		return decided, err
	}
	if decided, err := read(now.Add(time.Minute)); decided || err == nil {
		t.Fatalf("first failed read: decided=%v err=%v, want undecided with the error", decided, err)
	}
	if decided, _ := read(now.Add(3 * time.Minute)); decided {
		t.Fatal("decided after two minutes unreadable, want it waited out")
	}
	if decided, _ := read(now.Add(time.Minute + ciUnreadableFor)); !decided {
		t.Fatalf("not decided after %s unreadable", ciUnreadableFor)
	}
}

func (f *ciFixture) reviewerPrompts() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all strings.Builder
	for _, provider := range f.providers {
		for _, request := range provider.RequestsForRole(domain.RoleReviewer) {
			all.WriteString(request.Prompt)
			all.WriteString(request.SystemPrompt)
		}
	}
	return all.String()
}

// A passing result is recorded with each required check, the commit it ran on,
// and the digest of the workflow files at that commit, and the reviewer is shown
// each required check's name, result and commit.
func TestChecksOnCIRecordsPassingResultAsEvidenceAndShowsTheReviewer(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)
	f.forge.setResult(publish.CheckPassed)

	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	end := state.CIWaitEnd
	if end == nil || !end.Passed || end.WorkflowDigest != "sha256:workflows-v1" || len(end.Checks) != 2 {
		t.Fatalf("recorded end = %#v, want a pass with both checks and the workflow digest", end)
	}
	commit := parked.CIWait.Commit
	if ok, why := end.CountsFor(commit, []string{"test", "race"}, "sha256:workflows-v1"); !ok {
		t.Fatalf("the recorded result does not count for the candidate: %s", why)
	}
	prompts := f.reviewerPrompts()
	for _, name := range []string{"test", "race"} {
		line := "- " + name + ": passed, on commit " + commit
		if !strings.Contains(prompts, line) {
			t.Errorf("the reviewer was not shown %q", line)
		}
	}
}

// A result the forge gives for a commit other than the candidate is never taken
// as the candidate's: it records no evidence and the run runs the full checks.
func TestChecksOnCIResultForAnotherHeadGivesNoCredit(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	f.park(t)
	f.forge.setResult(publish.CheckPassed)
	f.forge.mu.Lock()
	f.forge.readCommit = strings.Repeat("c", 40)
	f.forge.mu.Unlock()

	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if end := state.CIWaitEnd; end == nil || end.Passed || len(end.Checks) != 0 || end.WorkflowDigest != "" {
		t.Fatalf("recorded end = %#v, want no evidence from another head's result", end)
	}
	if logLines(t, f.fullLog) == 0 {
		t.Fatal("the full checks did not run in place of the other head's result")
	}
}

// If the workflow files change after the result was recorded, the reviewer is
// told the result gives no credit.
func TestChecksOnCIChangedWorkflowFileWithdrawsTheResultFromTheReviewer(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	f.park(t)
	f.forge.setResult(publish.CheckPassed)
	pipeline := f.pipeline(t, true)
	// The digest is read once to record the result and again for the reviewer; the
	// forge answers differently the second time.
	pipeline.CIForge = &changingDigestForge{fakeCIForge: f.forge}
	if _, err := pipeline.Run(context.Background(), f.tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if prompts := f.reviewerPrompts(); !strings.Contains(prompts, "give no credit as evidence for the candidate commit: the workflow files changed") {
		t.Fatalf("the reviewer was not told the result gives no credit:\n%s", prompts)
	}
}

type changingDigestForge struct {
	*fakeCIForge
	calls int
}

func (f *changingDigestForge) WorkflowDigest(_ context.Context, _ string) (string, error) {
	f.calls++
	return "sha256:workflows-v" + string(rune('0'+f.calls)), nil
}
