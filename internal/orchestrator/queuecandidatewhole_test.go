package orchestrator

// Who checks the merge candidate whole. With the merge queue switched off, a
// run whose checks before review were narrowed runs them once more over the
// approved change told the whole module before it promotes it. With the queue
// on, the run leaves that to the harness's own queue, which checks the
// candidate it lands over the whole module with every check the run narrowed
// as well as the configured ones, lands it once they all pass, and names that
// check on the run. Where admission would choose the forge's own queue, the run
// checks the change whole itself first and says why, and no transfer moves it
// there; a change the queue holds is checked whole only when it is promoted the
// old way after the switch is turned off.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// goModuleChange makes the repository a Go module with one package and returns
// a developer that changes that package, so the checks before review are
// narrowed to it.
func goModuleChange(t *testing.T, repository string) func(backend.RunRequest) error {
	t.Helper()
	writeCommitted(t, repository, "go.mod", "module example.com/project\n")
	writeCommitted(t, repository, "internal/checks/runner.go", "package checks\n")
	return func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "internal", "checks", "narrow.go"), []byte("package checks\n\nconst narrowed = true\n"), 0o600)
	}
}

// toldCheck is a check that appends what it was told the change touches to a
// file, and the file.
func toldCheck(t *testing.T) (string, string) {
	t.Helper()
	told := filepath.Join(t.TempDir(), "told.txt")
	return `printf '%s\n' "$` + checks.ChangedGoPackagesVariable + `" >> ` + told, told
}

func readTold(t *testing.T, told string) []string {
	t.Helper()
	read, err := os.ReadFile(told)
	if err != nil {
		t.Fatalf("the check did not write what it was told: %v", err)
	}
	return strings.Fields(string(read))
}

func newGoQueueRun(t *testing.T) (*queueRun, string) {
	t.Helper()
	repository := pipelineRepository(t)
	develop := goModuleChange(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Change one package", Status: "open"}}
	provider := orchestratortest.RoleBackend(develop, approveVerdict, approveVerdict, approveVerdict)
	check, told := toldCheck(t)
	pipeline, runs := newAutomaticPipeline(t, repository, tracker, provider, []string{check})
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue
	return &queueRun{repository: repository, tracker: tracker, pipeline: pipeline, runs: runs, queue: queue}, told
}

// With the change going to the harness's own queue, the run's checks run
// narrowed and nothing more on this machine before admission. The queue's own
// checks over the candidate are told the whole module, and once it lands the
// run's record names that check: the entry, the generation, and the commit it
// checked, which is the commit that landed.
func TestWithTheHarnessQueueTheRunLeavesTheWholeCheckToItAndRecordsWhichRanIt(t *testing.T) {
	t.Parallel()

	r, told := newGoQueueRun(t)
	r.pipeline.Config.Execution.MergeQueue = true
	outcome := r.run(t)
	if outcome.Status != runstate.StatusSucceeded || outcome.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", outcome)
	}
	if got := readTold(t, told); !slices.Equal(got, []string{"./internal/checks"}) {
		t.Fatalf("before the queue ran, the check was told %q, want only the narrowed run before review", got)
	}
	state, err := r.runs.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ChecksPassed == nil || state.ChecksPassed.Narrowed == "" || !state.ChecksPassed.WholeLeftToQueue || state.ChecksPassed.WholeBy != "" {
		t.Fatalf("evidence = %#v, want the narrowed checks kept, left to the queue, and no queue run named before one ran", state.ChecksPassed)
	}
	notes := strings.Join(r.tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "the queue does that on the candidate it lands") {
		t.Fatalf("item notes do not say the whole check was left to the queue:\n%s", notes)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if pass := r.driver().Pass(ctx, outcome.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	if got := readTold(t, told); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the check was told %q, want the queue's run over the candidate told the whole module", got)
	}
	landed := gitLine(t, r.repository, "rev-parse", "main")
	settled, err := r.runs.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	whole := settled.ChecksPassed.WholeBy
	if !strings.Contains(whole, "generation 1") || !strings.Contains(whole, outcome.MergeQueue.EntryID) || !strings.Contains(whole, landed) {
		t.Fatalf("whole_by = %q, want it to name the queue's entry %s, its generation, and the commit that landed (%s)", whole, outcome.MergeQueue.EntryID, landed)
	}
	if !r.tracker.Closed {
		t.Fatal("the item was not settled on the queue's landing")
	}
}

// The project's checks changing after a change was admitted does not leave
// the change uncovered, and does not stop the queue: the queue runs the check
// the run narrowed beside the configured ones, told the whole module, over the
// candidate it lands, lands it once both pass, and names that check on the run.
func TestAQueueWhoseChecksChangedStillRunsTheChecksTheRunLeftToItAndLands(t *testing.T) {
	t.Parallel()

	r, told := newGoQueueRun(t)
	r.pipeline.Config.Execution.MergeQueue = true
	outcome := r.run(t)
	if outcome.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", outcome)
	}
	narrowed := r.pipeline.Config.Checks[0]
	ranNew := filepath.Join(t.TempDir(), "new-check-ran")
	r.pipeline.Config.Checks = []string{"touch " + ranNew}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pass := r.driver().Pass(ctx, outcome.MergeQueue.Key())
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the change landed", pass)
	}
	if got := readTold(t, told); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the run's own check was told %q, want the queue to have run it over the candidate told the whole module", got)
	}
	if _, err := os.Stat(ranNew); err != nil {
		t.Fatalf("the newly configured check did not run on the candidate: %v", err)
	}
	generations, err := r.queue.Generations(outcome.MergeQueue.Key(), outcome.MergeQueue.EntryID)
	if err != nil || len(generations) == 0 {
		t.Fatalf("generations = %#v, %v", generations, err)
	}
	landed := generations[len(generations)-1]
	if !slices.Equal(landed.Checks.Commands, []string{"touch " + ranNew, narrowed}) {
		t.Fatalf("the queue checked the candidate with %q, want the configured check and the one the run left to it", landed.Checks.Commands)
	}
	settled, err := r.runs.Load(outcome.RunID)
	if err != nil || settled.ChecksPassed == nil || !strings.Contains(settled.ChecksPassed.WholeBy, gitLine(t, r.repository, "rev-parse", "main")) {
		t.Fatalf("run evidence = %#v, %v; want the queue's check of the landed commit named", settled.ChecksPassed, err)
	}
}

// What the queue's check of a candidate has to show to stand in for a run's
// whole check, case by case.
func TestTheQueueStandsInOnlyForAWholeCheckItsOwnChecksCovered(t *testing.T) {
	t.Parallel()

	finished := time.Now()
	generation := func(results ...runstate.MergeQueueCheckResult) runstate.MergeQueueGeneration {
		g := runstate.MergeQueueGeneration{Number: 2, EntryID: "entry", TargetBranch: "main", TargetBase: "base", Candidate: "candidate"}
		g.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: g.Binding(), FinishedAt: &finished, Results: results}
		return g
	}
	run := func(left bool) runstate.State {
		return runstate.State{RunID: "run-1", ChecksPassed: &runstate.ChecksPassed{
			Commands: []string{"make test", "make race", "make getting-started"}, Narrowed: "./internal/checks", WholeLeftToQueue: left,
			CouldNotRun: []runstate.CheckCouldNotRun{{Command: "make vet", Reason: "no toolchain"}},
		}}
	}
	pass := func(command string) runstate.MergeQueueCheckResult {
		return runstate.MergeQueueCheckResult{Command: command, Passed: true}
	}
	paths := []config.PathCheck{{Command: "make getting-started"}}
	all := generation(pass("make test"), pass("make race"), pass("make vet"))

	for _, test := range []struct {
		name       string
		run        runstate.State
		mode       runstate.MergeQueueMode
		generation runstate.MergeQueueGeneration
		want       string
	}{
		{name: "every check passed, the path check aside", run: run(true), mode: runstate.MergeQueueHarness, generation: all},
		{name: "a run that checked its change whole itself", run: run(false), mode: runstate.MergeQueueForge, generation: runstate.MergeQueueGeneration{}},
		{name: "the race check missing", run: run(true), mode: runstate.MergeQueueHarness, generation: generation(pass("make test"), pass("make vet")), want: "make race is not among the queue's checks"},
		{name: "the race check could not run", run: run(true), mode: runstate.MergeQueueHarness,
			generation: generation(pass("make test"), runstate.MergeQueueCheckResult{Command: "make race", CouldNotRun: "no space"}, pass("make vet")), want: "make race could not run: no space"},
		{name: "the race check failed", run: run(true), mode: runstate.MergeQueueHarness,
			generation: generation(pass("make test"), runstate.MergeQueueCheckResult{Command: "make race"}, pass("make vet")), want: "make race did not pass"},
		{name: "a check the run could not run is not passed by the queue either", run: run(true), mode: runstate.MergeQueueHarness,
			generation: generation(pass("make test"), pass("make race")), want: "make vet is not among the queue's checks"},
		{name: "the forge's own queue lands a commit this harness never checked", run: run(true), mode: runstate.MergeQueueForge, generation: all, want: "forge's own merge queue"},
		{name: "a check run recorded against another candidate", run: run(true), mode: runstate.MergeQueueHarness,
			generation: func() runstate.MergeQueueGeneration { g := all; g.Candidate = "another"; return g }(), want: "no finished check of its own candidate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := mergeQueueStandsIn(test.run, test.mode, test.generation, paths)
			if test.want == "" && got != "" {
				t.Fatalf("mergeQueueStandsIn() = %q, want it to stand in", got)
			}
			if test.want != "" && !strings.Contains(got, test.want) {
				t.Fatalf("mergeQueueStandsIn() = %q, want it refused naming %q", got, test.want)
			}
		})
	}
}

// With the queue off, a narrowed gate is followed by the whole pass over the
// merge candidate on this machine, and only that pass is the evidence the run
// promotes on.
func TestWithTheMergeQueueOffTheRunStillChecksTheCandidateWhole(t *testing.T) {
	t.Parallel()

	r, told := newGoQueueRun(t)
	outcome := r.run(t)
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || outcome.MergeQueue != nil {
		t.Fatalf("Run() = %#v, want the change promoted by the run itself", outcome)
	}
	if got := readTold(t, told); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the check was told %q, want the narrowed run and then the whole module over the candidate", got)
	}
	state, err := r.runs.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ChecksPassed == nil || state.ChecksPassed.Narrowed != "" || state.ChecksPassed.WholeLeftToQueue || state.ChecksPassed.WholeBy != "" {
		t.Fatalf("promoted on evidence %#v, want the whole pass this run made", state.ChecksPassed)
	}
}

// forgeQueueAnswers is a forge whose answers about its own merge queue are
// given by answer, call by call.
type forgeQueueAnswers struct {
	*orchestratortest.Forge
	mu     sync.Mutex
	calls  int
	answer func(call int, branch string) publish.QueueCapabilities
}

func (f *forgeQueueAnswers) QueueCapabilities(_ context.Context, branch string) (publish.QueueCapabilities, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.answer(f.calls, branch), nil
}

func newGoPublishedQueueRun(t *testing.T, answer func(call int, branch string) publish.QueueCapabilities) (Pipeline, *runstate.Store, *forgeQueueAnswers, *orchestratortest.Tracker, string) {
	t.Helper()
	repository := pipelineRepository(t)
	develop := goModuleChange(t, repository)
	remote := addBareRemote(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &forgeQueueAnswers{Forge: &orchestratortest.Forge{Remote: remote}, answer: answer}
	provider := orchestratortest.RoleBackend(develop, approveVerdict, approveVerdict)
	check, told := toldCheck(t)
	pipeline, runs := newPublishingPipeline(t, repository, tracker, provider, forge, []string{check})
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue
	pipeline.Reports = &fakeReports{}
	pipeline.Config.Execution.MergeQueue = true
	return pipeline, runs, forge, tracker, told
}

// The forge's own merge queue running the project's checks is not the
// harness's queue checking the commit that lands, so a change that would go
// there is checked whole by its run at its promotion, before it is admitted,
// and the stage's record says why.
func TestAChangeBoundForTheForgesOwnQueueIsCheckedWholeByItsRun(t *testing.T) {
	t.Parallel()

	var digest string
	pipeline, runs, _, _, told := newGoPublishedQueueRun(t, func(_ int, branch string) publish.QueueCapabilities {
		return qualifyingQueue(queuemode.Harness{PullRequests: true, CheckConfiguration: digest})(branch)
	})
	digest = runstate.NewMergeQueueCheckConfiguration(pipeline.Config.Checks).Digest
	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.MergeQueue == nil || outcome.MergeQueue.Mode != runstate.MergeQueueForge {
		t.Fatalf("Run() = %#v, want the change admitted to the forge's own queue", outcome.MergeQueue)
	}
	if got := readTold(t, told); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the check was told %q, want the run to check the candidate whole itself", got)
	}
	state, err := runs.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ChecksPassed == nil || state.ChecksPassed.Narrowed != "" || state.ChecksPassed.WholeLeftToQueue {
		t.Fatalf("admitted on evidence %#v, want the whole pass this run made", state.ChecksPassed)
	}
	if state.CheckStage == nil || !strings.Contains(state.CheckStage.Narrowed, "forge's own merge queue") {
		t.Fatalf("stage record = %#v, want it to say the run checked whole because the forge's queue would land the change", state.CheckStage)
	}
}

// A change the queue would hold is not admitted, so nothing checks it whole
// while it waits; promoting it the old way after the switch is turned off
// checks it whole on this machine before it is merged.
func TestAChangeTheQueueHoldsIsCheckedWholeOnlyWhenItIsPromotedTheOldWay(t *testing.T) {
	t.Parallel()

	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	repository := pipelineRepository(t)
	develop := goModuleChange(t, repository)
	remote := addBareRemote(t, repository)
	r := &heldQueueRun{
		repository: repository, queue: queue, reports: &fakeReports{}, required: true,
		tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}},
		forge:   &orchestratortest.Forge{Remote: remote},
	}
	provider := orchestratortest.RoleBackend(develop, approveVerdict, approveVerdict)
	check, told := toldCheck(t)
	r.pipeline, r.runs = newPublishingPipeline(t, repository, r.tracker, provider, switchableForgeQueue{Forge: r.forge, required: &r.required, unread: &r.unread}, []string{check})
	r.pipeline.MergeQueue = queue
	r.pipeline.Reports = r.reports
	r.pipeline.Config.Execution.MergeQueue = true

	outcome, err := r.pipeline.Run(context.Background(), "yoyodyne-task")
	if !errors.Is(err, ErrMergeQueueHeld) {
		t.Fatalf("Run() error = %v, want the change held", err)
	}
	if got := readTold(t, told); !slices.Equal(got, []string{"./internal/checks"}) {
		t.Fatalf("while held, the check was told %q, want only the narrowed run", got)
	}
	r.pipeline.Config.Execution.MergeQueue = false
	if pass := r.held(t, outcome); pass.Resumed != 1 {
		t.Fatalf("held pass = %#v, want the change taken up", pass)
	}
	if got := readTold(t, told); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the check was told %q, want the candidate checked whole before it was merged", got)
	}
	if len(r.forge.Merges) != 1 {
		t.Fatalf("merges = %d, want the change merged through its pull request as with the switch off", len(r.forge.Merges))
	}
	state, err := r.runs.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.CheckStage == nil || !strings.Contains(state.CheckStage.Narrowed, "switched off after the change was left to it") {
		t.Fatalf("stage record = %#v, want it to say why the change was checked whole here", state.CheckStage)
	}
}

// A change whose run left its whole check to the harness's queue is not moved
// to the forge's own queue, which lands a commit this harness never checks:
// the transfer is refused saying why, and the entry stays where it can land.
func TestAChangeLeftToTheHarnessQueueIsNotMovedToTheForgesQueue(t *testing.T) {
	t.Parallel()

	r, _ := newGoQueueRun(t)
	r.pipeline.Config.Execution.MergeQueue = true
	outcome := r.run(t)
	if outcome.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", outcome)
	}
	_, err := r.driver().Promoter.Transfer(context.Background(), outcome.MergeQueue.Key(), outcome.MergeQueue.EntryID, runstate.MergeQueueForge)
	var refused MergeQueueTransferRefusal
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "left checking its change over the whole module") {
		t.Fatalf("Transfer() error = %v, want it refused because the run left its whole check to the harness's queue", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if pass := r.driver().Pass(ctx, outcome.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the change landed from the harness's queue", pass)
	}
}
