package orchestrator

// Who checks the merge candidate whole. With the merge queue switched off, a
// run whose checks before review were narrowed runs them once more over the
// approved change told the whole module before it promotes it. With the queue
// on, the run does not: the queue runs every configured check, told the whole
// module, over the candidate it builds from the target as it then stands and
// lands, so a second whole pass here would repeat it. The run's record and the
// item's notes say which check stood in, and a change held from the queue that
// is promoted the old way after the switch is turned off is checked whole first.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
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

// With the queue on, the run's checks run narrowed and nothing more on this
// machine before admission; the record and the item name the queue's checks as
// what stands in, and the queue's own checks over the candidate are told the
// whole module before it lands.
func TestWithTheMergeQueueOnTheRunDoesNotCheckTheCandidateWholeItself(t *testing.T) {
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
	if state.ChecksPassed == nil || state.ChecksPassed.Narrowed == "" || state.ChecksPassed.WholeBy != mergeQueueChecksWhole {
		t.Fatalf("evidence = %#v, want the narrowed checks kept with the queue's checks named as standing in", state.ChecksPassed)
	}
	notes := strings.Join(r.tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "standing in for that is "+mergeQueueChecksWhole) {
		t.Fatalf("item notes do not say which check stood in for the whole run:\n%s", notes)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if pass := r.driver().Pass(ctx, outcome.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	if got := readTold(t, told); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the check was told %q, want the queue's run over the candidate told the whole module", got)
	}
	if !r.tracker.Closed {
		t.Fatal("the item was not settled on the queue's landing")
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
	if state.ChecksPassed == nil || state.ChecksPassed.Narrowed != "" || state.ChecksPassed.WholeBy != "" {
		t.Fatalf("promoted on evidence %#v, want the whole pass this run made", state.ChecksPassed)
	}
}

// A change approved while the queue was on and held from it, which is then
// promoted the old way because the switch was turned off, is checked whole on
// this machine before it is merged: nothing else ran the whole pass on it.
func TestAHeldChangePromotedAfterTheSwitchIsTurnedOffIsCheckedWholeFirst(t *testing.T) {
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
	if state.ChecksPassed == nil || state.ChecksPassed.Narrowed != "" || state.ChecksPassed.WholeBy != "" {
		t.Fatalf("merged on evidence %#v, want the whole pass this run made", state.ChecksPassed)
	}
}
