package maincheck_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/maincheck"
	"github.com/mason-bryant/yoyodyne/internal/maintain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	cleanCommit  = "1111111111111111111111111111111111111111"
	brokenCommit = "2222222222222222222222222222222222222222"
)

// git stands in for Git in the primary checkout: the remote has main at head,
// and every other question is answered as a healthy repository would.
type git struct {
	mu       sync.Mutex
	head     string
	asked    []string
	worktree []string
}

func (g *git) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	joined := strings.Join(command.Args, " ")
	g.asked = append(g.asked, joined)
	ok := func(out string) (execution.ProcessResult, error) {
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: out}, nil
	}
	switch command.Args[0] {
	case "symbolic-ref":
		return ok("main\n")
	case "ls-remote":
		return ok(g.head + "\trefs/heads/main\n")
	case "rev-list":
		return ok("2\n")
	case "log":
		if command.Args[1] == "-1" {
			return ok("Make the scheduler faster\n")
		}
		return ok("2222222 Make the scheduler faster\n1234567 Tidy the queue\n")
	case "worktree":
		g.worktree = append(g.worktree, joined)
	}
	return ok("")
}

// runner stands in for the checks runner, answering with what the test sets.
type runner struct {
	mu       sync.Mutex
	results  []checks.Result
	requests []checks.Request
}

func (r *runner) Run(_ context.Context, request checks.Request, _ func(execution.Event) error) ([]checks.Result, uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	return r.results, 0, nil
}

func passing(command string) checks.Result {
	return checks.Result{Command: command, Passed: true, Process: execution.ProcessResult{Status: execution.ProcessSucceeded}}
}

func failing(command string, output string) checks.Result {
	return checks.Result{Command: command, Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stdout: output}}
}

type fixture struct {
	check   *maincheck.Check
	git     *git
	checks  *runner
	runs    *runstate.MainCheckStore
	reports *runstate.ReportStore
	now     time.Time
}

func newFixture(t *testing.T, root string) *fixture {
	t.Helper()
	runs, err := runstate.NewMainCheckStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	reports, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{git: &git{head: cleanCommit}, checks: &runner{}, runs: runs, reports: reports,
		now: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)}
	f.check = &maincheck.Check{
		Commands:     []string{"make race"},
		Repository:   root,
		WorkRoot:     root + "/main-check",
		Git:          f.git,
		Checks:       f.checks,
		CheckTimeout: 2 * time.Hour,
		Runs:         runs,
		Reports:      reports,
		Product:      "yoyodyne",
		RepositoryID: "yoyodyne",
		Now:          func() time.Time { return f.now },
	}
	return f
}

// look takes one look at now and waits for any run it started.
func (f *fixture) look(t *testing.T) (maincheck.Outcome, bool) {
	t.Helper()
	f.check.Look(context.Background(), f.now)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return f.check.Wait(ctx)
}

func TestAPassingRunIsRecordedWithItsCommitAndTimeAndFilesNothing(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.checks.results = []checks.Result{passing("make race")}
	outcome, ran := f.look(t)
	if !ran || !outcome.Recorded {
		t.Fatalf("outcome = %+v, ran %v; want a recorded run", outcome, ran)
	}
	runs, err := f.runs.List()
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	run := runs[0]
	if run.Commit != cleanCommit || run.Branch != "main" || run.Outcome != runstate.MainCheckPassed || !run.StartedAt.Equal(f.now) {
		t.Fatalf("run = %+v; want main at %s passed at %s", run, cleanCommit, f.now)
	}
	if len(run.Checks) != 1 || run.Checks[0].Command != "make race" || run.Checks[0].Outcome != runstate.MainCheckPassed {
		t.Fatalf("checks = %+v", run.Checks)
	}
	request := f.checks.requests[0]
	if !request.Unbounded || request.Timeout != 2*time.Hour || len(request.Commands) != 1 || request.Commands[0] != "make race" {
		t.Fatalf("request = %+v; want make race whole with no stage bound", request)
	}
	if !strings.HasPrefix(request.Directory, f.check.WorkRoot) {
		t.Fatalf("checks ran in %s, not a worktree under %s", request.Directory, f.check.WorkRoot)
	}
	if !askedFor(f.git.worktree, "worktree add --detach") || !askedFor(f.git.worktree, "worktree remove --force") {
		t.Fatalf("worktree = %v; want one made detached and removed", f.git.worktree)
	}
	filed, err := f.reports.List()
	if err != nil || len(filed) != 0 {
		t.Fatalf("reports = %+v, %v; a pass files nothing", filed, err)
	}
}

func TestAFailingRunFilesACriticalReportNamingTheCommitTheTestsAndTheLastCleanCommit(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.checks.results = []checks.Result{passing("make race")}
	f.look(t)

	f.now = f.now.Add(4 * time.Hour)
	f.git.head = brokenCommit
	f.checks.results = []checks.Result{failing("make race", "=== RUN   TestQueue\n--- FAIL: TestQueue (0.01s)\nFAIL\tgithub.com/example/queue\t0.2s\n")}
	outcome, ran := f.look(t)
	if !ran || outcome.Run.Outcome != runstate.MainCheckFailed {
		t.Fatalf("outcome = %+v; want a failed run", outcome)
	}
	filed, err := f.reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("reports = %+v, %v; want one", filed, err)
	}
	got := filed[0]
	if got.Severity != report.SeverityCritical || got.Role != report.HarnessReporter {
		t.Fatalf("report = %+v; want a critical report from the harness", got)
	}
	for _, want := range []string{brokenCommit, "TestQueue", "github.com/example/queue", cleanCommit, "2 commit(s) landed between them", "1234567 Tidy the queue", "not run again", "priority 0"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("report message does not say %q:\n%s", want, got.Message)
		}
	}
	runs, _ := f.runs.List()
	last := runs[len(runs)-1]
	if last.Commit != brokenCommit || last.ReportID != got.ID || len(last.Checks[0].FailingTests) == 0 {
		t.Fatalf("recorded run = %+v; want the failure with its report and failing tests", last)
	}

	// The failure is not run again on the same commit, however long it waits.
	f.now = f.now.Add(8 * time.Hour)
	outcome, ran = f.look(t)
	if !ran || outcome.Recorded || !strings.Contains(outcome.Said, "still at") {
		t.Fatalf("outcome = %+v; want no run on an unmoved branch", outcome)
	}
	if len(f.checks.requests) != 2 {
		t.Fatalf("checks ran %d times; the failure was run again", len(f.checks.requests))
	}
	if filed, _ := f.reports.List(); len(filed) != 1 {
		t.Fatalf("reports = %d; an unmoved branch files nothing more", len(filed))
	}
}

func TestARunIsDueEveryFourHoursAndTheScheduleSurvivesARestart(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	f.checks.results = []checks.Result{passing("make race")}
	start := f.now
	if _, ran := f.look(t); !ran {
		t.Fatal("the first look ran nothing")
	}
	if want := start.Add(4 * time.Hour); !f.check.NextDue().Equal(want) {
		t.Fatalf("next due = %s, want %s: the default interval is four hours", f.check.NextDue(), want)
	}
	f.git.head = brokenCommit
	f.now = start.Add(4*time.Hour - time.Minute)
	if _, ran := f.look(t); ran {
		t.Fatal("a run started before four hours had passed")
	}

	// A supervisor that comes back up reads the schedule from the record.
	restarted := newFixture(t, root)
	restarted.git.head = brokenCommit
	restarted.checks.results = []checks.Result{passing("make race")}
	restarted.now = start.Add(time.Hour)
	if _, ran := restarted.look(t); ran {
		t.Fatal("a restarted supervisor ran early instead of waiting for the run that was due")
	}
	again := newFixture(t, root)
	again.git.head = brokenCommit
	again.checks.results = []checks.Result{passing("make race")}
	again.now = start.Add(4 * time.Hour)
	outcome, ran := again.look(t)
	if !ran || outcome.Run.Commit != brokenCommit {
		t.Fatalf("outcome = %+v; want a run on the moved branch once four hours had passed", outcome)
	}
}

func TestARunUnderWayHoldsTheMoveOntoANewBuildBack(t *testing.T) {
	f := newFixture(t, t.TempDir())
	held := make(chan struct{})
	f.check.Checks = blocking{release: held}
	var _ maintain.Underway = f.check
	if wait := f.check.Underway(); wait != "" {
		t.Fatalf("underway = %q before any run", wait)
	}
	f.check.Look(context.Background(), f.now)
	if wait := f.check.Underway(); !strings.Contains(wait, "checks on main") {
		t.Fatalf("underway = %q; want the run under way named", wait)
	}
	close(held)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.check.Wait(ctx)
	if wait := f.check.Underway(); wait != "" {
		t.Fatalf("underway = %q after the run ended", wait)
	}
}

func TestACheckThatCouldNotRunIsRecordedAndTriedAgainOnTheSameCommit(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.checks.results = []checks.Result{{Command: "make race", CouldNotRun: "the race detector is not supported here", Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 3}}}
	outcome, _ := f.look(t)
	if outcome.Run.Outcome != runstate.MainCheckCouldNotRun {
		t.Fatalf("outcome = %+v; want could not run", outcome)
	}
	if filed, _ := f.reports.List(); len(filed) != 0 {
		t.Fatalf("a check that could not run filed %d reports", len(filed))
	}
	f.now = f.now.Add(4 * time.Hour)
	f.checks.results = []checks.Result{passing("make race")}
	outcome, _ = f.look(t)
	if outcome.Run.Outcome != runstate.MainCheckPassed || outcome.Run.Commit != cleanCommit {
		t.Fatalf("outcome = %+v; want the same commit checked again and passing", outcome)
	}
}

type blocking struct{ release chan struct{} }

func (b blocking) Run(ctx context.Context, request checks.Request, _ func(execution.Event) error) ([]checks.Result, uint64, error) {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return []checks.Result{passing(request.Commands[0])}, 0, nil
}

func askedFor(asked []string, prefix string) bool {
	for _, question := range asked {
		if strings.HasPrefix(question, prefix) {
			return true
		}
	}
	return false
}
