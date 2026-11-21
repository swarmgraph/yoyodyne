package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const rerunCommit = "0123456789abcdef0123456789abcdef01234567"

// rerunStore keeps every record saved, and refuses saves while refuse is set.
type rerunStore struct {
	StateStore
	saved  []runstate.State
	refuse bool
	// held is the run as stored before anything was saved here.
	held runstate.State
}

func (s *rerunStore) Save(state runstate.State) error {
	if s.refuse {
		return errors.New("state store is unavailable")
	}
	saved := state
	saved.UntouchedReruns = slices.Clone(state.UntouchedReruns)
	s.saved = append(s.saved, saved)
	return nil
}

func (s *rerunStore) AppendEvent(execution.Event) error { return nil }

// Update applies the narrow update to the latest record saved, or to held
// where none is yet, as the store does to the stored record.
func (s *rerunStore) Update(_ context.Context, _ string, update runstate.RunUpdate) (runstate.State, error) {
	if s.refuse {
		return runstate.State{}, errors.New("state store is unavailable")
	}
	stored := s.held
	if len(s.saved) > 0 {
		stored = s.saved[len(s.saved)-1]
	}
	next, _, err := runstate.ApplyUpdate(stored, update)
	if err != nil {
		return runstate.State{}, err
	}
	return next, s.Save(next)
}

// scriptedChecks answers each Run with the next scripted answer, and keeps each
// request it was asked.
type scriptedChecks struct {
	answers  []scriptedAnswer
	requests []checks.Request
	// saved is what the store held when each request was asked.
	store   *rerunStore
	atAsked [][]runstate.UntouchedRerun
}

type scriptedAnswer struct {
	results []checks.Result
	err     error
}

func (c *scriptedChecks) Run(_ context.Context, request checks.Request, _ func(execution.Event) error) ([]checks.Result, uint64, error) {
	c.requests = append(c.requests, request)
	if c.store != nil && len(c.store.saved) > 0 {
		c.atAsked = append(c.atAsked, c.store.saved[len(c.store.saved)-1].UntouchedReruns)
	} else {
		c.atAsked = append(c.atAsked, nil)
	}
	if len(c.answers) == 0 {
		return nil, request.LastSequence, errors.New("no answer scripted")
	}
	answer := c.answers[0]
	c.answers = c.answers[1:]
	return answer.results, request.LastSequence + 1, answer.err
}

func passedCheck(command string) checks.Result {
	return checks.Result{Command: command, Passed: true, Process: execution.ProcessResult{Status: execution.ProcessSucceeded}}
}

func failedCheckNaming(command string, tests ...checks.FailingTest) checks.Result {
	return checks.Result{
		Command:       command,
		Process:       execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2},
		FailureOutput: "--- FAIL: TestTotals (0.00s)\n    alpha_test.go:7: total = 3, want 4\n",
		FailingTests:  &checks.FailingTests{Tests: tests},
	}
}

var alphaTest = checks.FailingTest{Name: "TestTotals", File: "internal/alpha/alpha_test.go"}

// rerunFixture is a run whose stage of three checks ended on the second, failing
// only in a test the change does not touch.
func rerunFixture(answers ...scriptedAnswer) (*activeRun, *scriptedChecks, *rerunStore, checks.Request, []checks.Result) {
	held := runstate.State{RunID: "run-rerun", HarnessCommit: rerunCommit}
	store := &rerunStore{held: held}
	runner := &scriptedChecks{answers: answers, store: store}
	a := &activeRun{pipeline: Pipeline{Store: store, Checks: runner}, state: held}
	request := checks.Request{
		RunID:        "run-rerun",
		Directory:    "/worktree",
		Commands:     []string{"make fmtcheck", "make test", "make vet"},
		Env:          []string{"KEPT=yes"},
		ProviderCLIs: map[int]bool{2: true},
	}
	stage := []checks.Result{passedCheck("make fmtcheck"), failedCheckNaming("make test", alphaTest)}
	return a, runner, store, request, stage
}

// A failure confined to untouched tests runs only those tests once more, on the
// same commit, recorded before it starts; where they pass the check counts as
// passed, the checks after it run, and both results are kept on the run.
func TestARerunThatPassesCountsTheCheckAsPassedAndKeepsBothResults(t *testing.T) {
	t.Parallel()

	a, runner, store, request, stage := rerunFixture(
		scriptedAnswer{results: []checks.Result{passedCheck("make test")}},
		scriptedAnswer{results: []checks.Result{passedCheck("make vet")}},
	)
	results, _, err := a.rerunUntouched(context.Background(), request, stage, 7, []string{"feature.txt"})
	if err != nil {
		t.Fatalf("rerunUntouched() error = %v", err)
	}
	if len(results) != 3 || !results[0].Passed || !results[1].Passed || !results[2].Passed || results[2].Command != "make vet" {
		t.Fatalf("results = %#v, want the check counted as passed and the check after it run", results)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("runs = %d, want the rerun and then the rest of the stage", len(runner.requests))
	}
	rerun := runner.requests[0]
	if !slices.Equal(rerun.Commands, []string{"make test"}) || !slices.Contains(rerun.Env, "YOYODYNE_FAILING_TESTS=TestTotals\n./internal/alpha") ||
		!slices.Contains(rerun.Env, "KEPT=yes") || len(rerun.ProviderCLIs) != 0 || rerun.LastSequence != 7 {
		t.Fatalf("rerun request = %#v, want only the failing check, told its failing test, with the stage's environment", rerun)
	}
	if rest := runner.requests[1]; !slices.Equal(rest.Commands, []string{"make vet"}) || !rest.ProviderCLIs[0] || slices.Contains(rest.Env, "YOYODYNE_FAILING_TESTS=TestTotals\n./internal/alpha") {
		t.Fatalf("rest of the stage = %#v, want the check after it, untold of the failing tests, keeping its provider CLIs", rest)
	}
	if recorded := runner.atAsked[0]; len(recorded) != 1 || recorded[0].Outcome != "" || recorded[0].Commit != rerunCommit || recorded[0].Stage != runstate.UntouchedRerunBeforeReview {
		t.Fatalf("record when the rerun was asked = %#v, want it on the run before it started", recorded)
	}
	final := store.saved[len(store.saved)-1].UntouchedReruns
	if len(final) != 1 || final[0].Outcome != runstate.UntouchedRerunPassed || final[0].EndedAt == nil ||
		!strings.Contains(final[0].FirstOutput, "TestTotals") || final[0].FirstExitCode != 2 || final[0].Tests[0] != alphaTest {
		t.Fatalf("record after the rerun = %#v, want the first failure with its output and the passing rerun", final)
	}
	described := describeUntouchedReruns(a.state)
	for _, want := range []string{"make test, in the checks before review", "TestTotals (internal/alpha/alpha_test.go)", "The first failure's output:", "alpha_test.go:7: total = 3, want 4"} {
		if !strings.Contains(described, want) {
			t.Errorf("reviewer evidence = %q, want it to contain %q", described, want)
		}
	}
}

// A test that fails again, a rerun stopped by its time limit, and one that
// cannot be started all leave the first failure standing as an ordinary
// failed check, recorded with why.
func TestARerunThatDoesNotPassLeavesTheFirstFailureStanding(t *testing.T) {
	t.Parallel()

	timedOut := checks.Result{Command: "make test", Process: execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}}
	for name, tc := range map[string]struct {
		answer  scriptedAnswer
		outcome runstate.UntouchedRerunOutcome
		says    string
	}{
		"fails again":       {scriptedAnswer{results: []checks.Result{failedCheckNaming("make test", alphaTest)}}, runstate.UntouchedRerunFailed, "failed again"},
		"stopped on time":   {scriptedAnswer{results: []checks.Result{timedOut}}, runstate.UntouchedRerunUnfinished, "stopped by its time limit"},
		"cannot be started": {scriptedAnswer{err: errors.New("run check: fork: resource unavailable")}, runstate.UntouchedRerunUnfinished, "could not be started"},
	} {
		t.Run(name, func(t *testing.T) {
			a, runner, _, request, stage := rerunFixture(tc.answer)
			results, _, err := a.rerunUntouched(context.Background(), request, stage, 0, []string{"feature.txt"})
			if err != nil {
				t.Fatalf("rerunUntouched() error = %v, want the first failure to stand rather than an error", err)
			}
			if len(results) != 2 || results[1].Passed || results[1].Process.Status != execution.ProcessFailed || results[1].Process.ExitCode != 2 {
				t.Fatalf("results = %#v, want the stage ending on the first failure, as an ordinary failed check", results)
			}
			if len(runner.requests) != 1 {
				t.Fatalf("runs = %d, want only the rerun, and nothing after it", len(runner.requests))
			}
			recorded := a.state.UntouchedReruns
			if len(recorded) != 1 || recorded[0].Outcome != tc.outcome || !strings.Contains(recorded[0].Detail, tc.says) || !strings.Contains(recorded[0].Detail, "first failure stands") {
				t.Fatalf("record = %#v, want %s saying %q", recorded, tc.outcome, tc.says)
			}
			if a.state.CheckStageContinuations != nil || len(a.state.CheckReruns()) != 0 {
				t.Fatalf("the rerun spent a rerun of a check stopped by its time limit: %#v", a.state.CheckReruns())
			}
			if described := describeUntouchedReruns(a.state); described != "" {
				t.Fatalf("reviewer evidence = %q, want nothing for a rerun that did not pass", described)
			}
		})
	}
}

// Nothing is run again for a failure that is not confined to untouched tests,
// for a check no reading names, or where the rerun cannot first be recorded.
func TestNoRerunWithoutAConfinedFailureOrARecordOfIt(t *testing.T) {
	t.Parallel()

	a, runner, _, request, stage := rerunFixture()
	if results, _, _ := a.rerunUntouched(context.Background(), request, stage, 0, []string{"internal/alpha/alpha.go"}); len(runner.requests) != 0 || len(results) != 2 {
		t.Fatalf("a failure in a touched test was run again: %#v", runner.requests)
	}
	stage[1].FailingTests = nil
	if a.rerunUntouched(context.Background(), request, stage, 0, []string{"feature.txt"}); len(runner.requests) != 0 {
		t.Fatalf("a failure no reading named was run again: %#v", runner.requests)
	}

	a, runner, store, request, stage := rerunFixture()
	store.refuse = true
	if results, _, _ := a.rerunUntouched(context.Background(), request, stage, 0, []string{"feature.txt"}); len(runner.requests) != 0 || len(results) != 2 || len(a.state.UntouchedReruns) != 0 {
		t.Fatalf("a rerun the record could not hold was run: requests %#v, record %#v", runner.requests, a.state.UntouchedReruns)
	}
}

// A harness that restarts after recording the rerun and before running it
// finds the rerun spent: the same stage on the same commit runs nothing again,
// and the other stage, or a new candidate commit, starts again.
func TestARestartBetweenRecordingAndRunningTheRerunNeverGrantsASecond(t *testing.T) {
	t.Parallel()

	a, runner, store, request, stage := rerunFixture(scriptedAnswer{err: errors.New("the harness stopped")})
	a.rerunUntouched(context.Background(), request, stage, 0, []string{"feature.txt"})
	// What a process killed as the rerun launched left on disk: the first save,
	// with the rerun recorded and never ended.
	left := store.saved[0]
	if len(left.UntouchedReruns) != 1 || left.UntouchedReruns[0].Outcome != "" {
		t.Fatalf("record at the kill = %#v, want the rerun recorded and not ended", left.UntouchedReruns)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("runs = %d", len(runner.requests))
	}

	restarted := &activeRun{pipeline: Pipeline{Store: &rerunStore{held: left}, Checks: runner}, state: left}
	results, _, err := restarted.rerunUntouched(context.Background(), request, stage, 0, []string{"feature.txt"})
	if err != nil || len(runner.requests) != 1 || len(results) != 2 || results[1].Passed {
		t.Fatalf("after the restart: results %#v, %v, runs %d; want the first failure standing and nothing run again", results, err, len(runner.requests))
	}

	runner.answers = []scriptedAnswer{{results: []checks.Result{passedCheck("make test")}}, {results: []checks.Result{passedCheck("make vet")}}}
	restarted.checkingCandidate = true
	if results, _, _ := restarted.rerunUntouched(context.Background(), request, stage, 0, []string{"feature.txt"}); len(runner.requests) != 3 || !results[1].Passed {
		t.Fatalf("the checks before promotion did not get their own rerun: runs %d, results %#v", len(runner.requests), results)
	}
	if got := restarted.state.UntouchedReruns[1].Stage; got != runstate.UntouchedRerunBeforePromotion {
		t.Fatalf("stage = %q, want the checks before promotion", got)
	}

	restarted.checkingCandidate = false
	restarted.state.HarnessCommit = "1123456789abcdef0123456789abcdef01234567"
	runner.answers = []scriptedAnswer{{results: []checks.Result{failedCheckNaming("make test", alphaTest)}}}
	restarted.rerunUntouched(context.Background(), request, stage, 0, []string{"feature.txt"})
	if len(runner.requests) != 4 {
		t.Fatalf("runs = %d, want a new candidate commit to start again", len(runner.requests))
	}
}

// A whole run: a check that fails only in a test the change does not touch is
// run once more told that test, passes, and the run goes to review and lands
// with no repair attempt spent; the reviewer is shown the test that failed and
// then passed, with the first failure's output.
func TestARunWhoseCheckFailsOnlyInAnUntouchedTestLandsWithoutARepair(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	calls := filepath.Join(t.TempDir(), "calls")
	flaky := `printf '[%s]' "$YOYODYNE_FAILING_TESTS" >> '` + calls + `'; [ -n "$YOYODYNE_FAILING_TESTS" ] || { printf -- '--- FAIL: TestTotals (0.00s)\n    alpha_test.go:7: total = 3, want 4\nFAIL\nFAIL\texample.com/shop/internal/alpha\t0.2s\n'; exit 1; }`
	after := `printf after >> '` + calls + `'`
	develop := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	provider := orchestratortest.RoleBackend(develop, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{flaky, after}), provider)
	pipeline.Checks = checks.Runner{Process: execution.OSProcessRunner{}, FailingTests: []checks.TestReading{goTestReading("printf")}}
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || outcome.RepairAttempts != 0 {
		t.Fatalf("outcome = %#v, want the change landed with no repair attempt", outcome)
	}
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer invocations = %d, want one", len(developers))
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.HasPrefix(got, "[][TestTotals\n./internal/alpha]after") {
		t.Fatalf("check runs = %q, want the first run, the rerun told only the failing test, then the check after it", got)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(finished.UntouchedReruns) != 2 || finished.UntouchedReruns[0].Outcome != runstate.UntouchedRerunPassed ||
		finished.UntouchedReruns[0].Stage != runstate.UntouchedRerunBeforeReview || !strings.Contains(finished.UntouchedReruns[0].FirstOutput, "TestTotals") {
		t.Fatalf("reruns on the record = %#v, want the passing rerun with the first failure kept", finished.UntouchedReruns)
	}
	// The checks before review ran whole, which alone would have left the
	// candidate unchecked again; a test that passed only when run once more
	// has the candidate checked whole before it lands, where the same failure
	// gets the promotion stage's own rerun.
	if promotion := finished.UntouchedReruns[1]; promotion.Stage != runstate.UntouchedRerunBeforePromotion || promotion.Outcome != runstate.UntouchedRerunPassed {
		t.Fatalf("the checks before promotion = %#v, want the candidate checked whole with its own rerun", promotion)
	}
	if got := string(data); got != "[][TestTotals\n./internal/alpha]after[][TestTotals\n./internal/alpha]after" {
		t.Fatalf("check runs = %q, want the stage before review and then the whole stage over the candidate", got)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 {
		t.Fatalf("reviews = %d, want one", len(reviews))
	}
	for _, want := range []string{"## Tests that failed and then passed", "TestTotals (internal/alpha/alpha_test.go)", "alpha_test.go:7: total = 3, want 4"} {
		if !strings.Contains(reviews[0].Prompt, want) {
			t.Errorf("the reviewer's prompt does not contain %q", want)
		}
	}
}
