package orchestrator

// The merge queue worker's verification of a candidate. The project here is
// not a Go project: its checks are shell commands over its own files, which is
// all the harness ever assumes a project declares.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var queueKey = runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}

// queueChecks are the fixture project's checks: a file test and a script the
// project carries, neither of them anything to do with Go.
var queueChecks = []string{"test -f feature.txt", "sh ./scripts/check.sh"}

type queueFixture struct {
	t          *testing.T
	repository string
	head       string
	worker     MergeQueueWorker
	queue      *runstate.MergeQueueStore
	records    *flakyGenerations
	checks     *countingChecks
	reviewer   *countingReviewer
	runs       StateStore
	entry      runstate.MergeQueueEntry
	events     *eventRecorder
}

func newQueueFixture(t *testing.T, verdicts ...string) *queueFixture {
	t.Helper()
	repository := pipelineRepository(t)
	writeQueueFile(t, repository, "scripts/check.sh", "#!/bin/sh\nset -e\ntest -f other.txt\ngrep -q implemented feature.txt\n")
	writeQueueFile(t, repository, "other.txt", "other work\n")
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "the project and its checks")
	runPipelineGit(t, repository, "switch", "-c", "change")
	writeQueueFile(t, repository, "feature.txt", "implemented\n")
	runPipelineGit(t, repository, "add", "feature.txt")
	runPipelineGit(t, repository, "commit", "-m", "the approved change")
	head := gitLine(t, repository, "rev-parse", "HEAD")
	runPipelineGit(t, repository, "switch", "main")
	moveTarget(t, repository, "landed-before.txt")

	if len(verdicts) == 0 {
		verdicts = []string{approveVerdict, approveVerdict, approveVerdict, approveVerdict, approveVerdict}
	}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, verdicts...)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Add the feature", Description: "Add feature.txt.", Status: "in_progress"}}
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, queueChecks)

	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := queue.Admit(context.Background(), runstate.MergeQueueAdmission{
		Key: queueKey, WorkItemID: "yoyodyne-task", WorkItemTitle: "Add the feature", RunID: runID,
		ApprovedHead: head, IntegrationPolicy: "automatic", Mode: runstate.MergeQueueHarness,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The run that made the change was approved at its head, by a reviewer of
	// its own. That approval admitted it; it is not the candidate's.
	runs := &admittedRun{StateStore: store, state: runstate.State{
		RunID: runID, WorkItemID: "yoyodyne-task", ProviderSessionID: "developer-session",
		ReviewDecision: string(review.DecisionApprove), ReviewSessionID: "earlier-reviewer-session", ReviewHeadCommit: head,
	}}
	pipeline.Store = runs
	fixture := &queueFixture{
		t: t, repository: repository, head: head, queue: queue, entry: entry, runs: runs,
		records:  &flakyGenerations{MergeQueueStore: queue},
		checks:   &countingChecks{runner: pipeline.Checks},
		reviewer: &countingReviewer{reviewer: pipeline.Reviewer},
		events:   &eventRecorder{},
	}
	pipeline.Checks = fixture.checks
	pipeline.Reviewer = fixture.reviewer
	fixture.worker = MergeQueueWorker{
		Pipeline: &pipeline, Queue: fixture.records,
		Candidates: pipeline.Worktrees.(*gitworktree.Manager),
		Events:     fixture.events.record,
	}
	return fixture
}

func (f *queueFixture) work() (MergeQueueVerification, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return f.worker.Work(ctx, queueKey)
}

func (f *queueFixture) generations() []runstate.MergeQueueGeneration {
	f.t.Helper()
	generations, err := f.queue.Generations(queueKey, f.entry.EntryID)
	if err != nil {
		f.t.Fatalf("Generations() error = %v", err)
	}
	return generations
}

func (f *queueFixture) configured() runstate.MergeQueueCheckConfiguration {
	return runstate.NewMergeQueueCheckConfiguration(f.worker.Pipeline.Config.Checks)
}

func TestTheQueueWorkerVerifiesTheExactCandidateWithoutThePromotionLease(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	target := gitLine(t, f.repository, "rev-parse", "main")
	// Whoever is promoting into the branch holds its promotion lease the whole
	// time; the queue's checks and review must not need it.
	promotion, err := f.runs.LeasePromotion(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer promotion.Release()

	verification, err := f.work()
	if err != nil {
		t.Fatalf("Work() error = %v", err)
	}
	if !verification.Verified || verification.Drift != 0 || verification.Entry.EntryID != f.entry.EntryID {
		t.Fatalf("Work() = %#v, want the entry verified", verification)
	}
	generation := verification.Generation
	if generation.Number != 1 || generation.TargetBase != target || len(generation.Heads) != 1 || generation.Heads[0] != f.head ||
		generation.AuthorSession != "developer-session" || !generation.Checks.Same(f.configured()) {
		t.Fatalf("generation = %#v, want the target, the approved head, the author and the configured checks", generation)
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", generation.Candidate); parents != generation.Candidate+" "+target+" "+f.head {
		t.Fatalf("candidate parents = %q, want the target then the approved head", parents)
	}
	if content := gitLine(t, f.repository, "rev-parse", generation.Candidate+"^{tree}"); content != generation.Content {
		t.Fatalf("content = %s, recorded %s", content, generation.Content)
	}
	if len(generation.CheckRun.Results) != len(queueChecks) {
		t.Fatalf("check results = %#v", generation.CheckRun.Results)
	}
	if generation.Review.SessionID != "reviewer-session" || generation.Review.Decision != string(review.DecisionApprove) {
		t.Fatalf("review = %#v, want the candidate's own approval", generation.Review)
	}
	// The run's own approval admitted the change and did not stand in for the
	// candidate's review, which was asked for the item the change was made for.
	if f.reviewer.calls() != 1 {
		t.Fatalf("reviewer asked %d times, want once for the candidate", f.reviewer.calls())
	}
	request := f.reviewer.last()
	if request.WorkItemID != "yoyodyne-task" || !strings.Contains(request.Changes.Patch, "+implemented") ||
		strings.Contains(request.Changes.Patch, "landed-before") || request.Spend.RunID != f.entry.RunID || request.Spend.WorkItemID != "yoyodyne-task" ||
		!strings.Contains(request.Context, generation.Candidate) {
		t.Fatalf("review request = %#v, want the candidate over its base, charged to the entry's run and item", request)
	}
	if f.checks.directory() != generation.Checkout || readQueueFile(t, generation.Checkout, "feature.txt") != "implemented\n" {
		t.Fatalf("checks ran in %s, want the candidate's checkout %s", f.checks.directory(), generation.Checkout)
	}
	if f.events.count() == 0 {
		t.Fatal("the checks and the review recorded no events")
	}
	// Nothing moved: not the target, and not the change's own branch.
	if gitLine(t, f.repository, "rev-parse", "main") != target || gitLine(t, f.repository, "rev-parse", "change") != f.head {
		t.Fatal("verifying the candidate moved a branch")
	}
	verified, err := f.queue.VerifiedGeneration(queueKey, f.entry.EntryID, f.configured())
	if err != nil || verified.Binding() != generation.Binding() {
		t.Fatalf("VerifiedGeneration() = %v, want the generation a promotion can read", err)
	}

	// Asking again finds the evidence and runs nothing again.
	again, err := f.work()
	if err != nil || !again.Verified || again.Generation.Binding() != generation.Binding() {
		t.Fatalf("second Work() = %#v, %v", again, err)
	}
	if f.checks.calls() != 1 || f.reviewer.calls() != 1 {
		t.Fatalf("checks ran %d times and the review %d, want each once", f.checks.calls(), f.reviewer.calls())
	}
}

func TestTheTargetMovingDuringTheChecksRebuildsAndVerifiesAfresh(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.checks.before = func(call int) {
		if call == 1 {
			moveTarget(t, f.repository, "landed-during-checks.txt")
		}
	}
	verification, err := f.work()
	if err != nil || !verification.Verified || verification.Drift != 1 {
		t.Fatalf("Work() = %#v, %v; want one rebuild and a verified candidate", verification, err)
	}
	generations := f.generations()
	if len(generations) != 2 {
		t.Fatalf("generations = %d, want the drifted one and its replacement", len(generations))
	}
	first, second := generations[0], generations[1]
	moved := gitLine(t, f.repository, "rev-parse", "main")
	if first.Invalidated == nil || first.Invalidated.Reason != runstate.MergeQueueTargetMoved || first.Invalidated.ObservedTarget != moved {
		t.Fatalf("first generation invalidation = %#v, want the move recorded", first.Invalidated)
	}
	if second.TargetBase != moved || readQueueFile(t, second.Checkout, "landed-during-checks.txt") == "" {
		t.Fatalf("second generation base = %s, want it built on the moved target %s", second.TargetBase, moved)
	}
	// The first generation's checks stay where they were earned and authorize
	// nothing; the review was only ever asked about the second.
	if first.CheckRun == nil || first.CheckRun.Binding != first.Binding() || first.Review != nil {
		t.Fatalf("first generation evidence = %#v, %#v", first.CheckRun, first.Review)
	}
	if f.checks.calls() != 2 || f.reviewer.calls() != 1 {
		t.Fatalf("checks ran %d times and the review %d, want 2 and 1", f.checks.calls(), f.reviewer.calls())
	}
	// Drift is charged to nobody: the change's run is untouched.
	if state, err := f.runs.Load(f.entry.RunID); err != nil || state.RepairAttempts != 0 {
		t.Fatalf("run record = %#v, %v; drift must charge no repair", state, err)
	}
}

func TestTheTargetMovingDuringTheReviewRebuildsAndVerifiesAfresh(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.reviewer.before = func(call int) {
		if call == 1 {
			moveTarget(t, f.repository, "landed-during-review.txt")
		}
	}
	verification, err := f.work()
	if err != nil || !verification.Verified || verification.Drift != 1 {
		t.Fatalf("Work() = %#v, %v; want one rebuild and a verified candidate", verification, err)
	}
	generations := f.generations()
	if len(generations) != 2 || generations[0].Review == nil || generations[0].Invalidated == nil {
		t.Fatalf("generations = %#v, want the reviewed-then-drifted one and its replacement", generations)
	}
	if err := generations[0].Gate(f.configured()); err == nil {
		t.Fatal("the drifted generation still passes its gate")
	}
	if generations[1].Review.Binding != generations[1].Binding() || generations[1].Review.Binding == generations[0].Review.Binding {
		t.Fatal("the second generation's review is not its own")
	}
	if f.checks.calls() != 2 || f.reviewer.calls() != 2 {
		t.Fatalf("checks ran %d times and the review %d, want each twice", f.checks.calls(), f.reviewer.calls())
	}
}

func TestChangedChecksInvalidateAVerifiedGeneration(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	if verification, err := f.work(); err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v", verification, err)
	}
	f.worker.Pipeline.Config.Checks = append(append([]string(nil), queueChecks...), "test -f other.txt")
	verification, err := f.work()
	if err != nil || !verification.Verified || verification.Generation.Number != 2 {
		t.Fatalf("Work() = %#v, %v; want a second generation verified under the new checks", verification, err)
	}
	if first := f.generations()[0]; first.Invalidated == nil || first.Invalidated.Reason != runstate.MergeQueueChecksChanged {
		t.Fatalf("first generation invalidation = %#v", first.Invalidated)
	}
	if len(verification.Generation.CheckRun.Results) != 3 || f.reviewer.calls() != 2 {
		t.Fatalf("second generation checks = %#v, reviews = %d", verification.Generation.CheckRun.Results, f.reviewer.calls())
	}
}

func TestAFailingCheckOrARepairVerdictIsAVerificationThatDidNotPass(t *testing.T) {
	t.Parallel()

	failing := newQueueFixture(t)
	failing.worker.Pipeline.Config.Checks = []string{"test -f feature.txt", "test -f missing.txt"}
	verification, err := failing.work()
	if err != nil || verification.Verified || !strings.Contains(verification.Refusal, "did not pass") {
		t.Fatalf("Work() = %#v, %v; want a refusal for the failing check", verification, err)
	}
	if failing.reviewer.calls() != 0 {
		t.Fatal("a candidate that failed its checks was sent for review")
	}

	repaired := newQueueFixture(t, repairVerdict)
	verification, err = repaired.work()
	if err != nil || verification.Verified || !strings.Contains(verification.Refusal, `"repair"`) {
		t.Fatalf("Work() = %#v, %v; want a refusal for the repair verdict", verification, err)
	}
}

func TestARestartDuringTheChecksRunsThemAgainOnTheSameCandidate(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	// The worker dies with the checks finished and their result not written:
	// what is on the record is checks that began.
	f.records.fail = func(g runstate.MergeQueueGeneration) (bool, error) {
		if g.CheckRun != nil && g.CheckRun.FinishedAt != nil {
			return false, errors.New("the worker died")
		}
		return false, nil
	}
	if _, err := f.work(); err == nil {
		t.Fatal("Work() succeeded past a lost record")
	}
	before := f.generations()[0]
	f.records.fail = nil
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() after the restart = %#v, %v", verification, err)
	}
	after := verification.Generation
	if after.Number != 1 || after.Candidate != before.Candidate || len(after.Interruptions) != 1 || after.Interruptions[0].Stage != runstate.MergeQueueStageChecks {
		t.Fatalf("generation = %#v, want the same candidate with the interrupted checks recorded", after)
	}
	if f.checks.calls() != 2 || f.reviewer.calls() != 1 {
		t.Fatalf("checks ran %d times and the review %d, want 2 and 1", f.checks.calls(), f.reviewer.calls())
	}
}

func TestARestartDuringTheReviewAsksAgainAndKeepsTheChecks(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.records.fail = func(g runstate.MergeQueueGeneration) (bool, error) {
		if g.Review != nil && g.Review.FinishedAt != nil {
			return false, errors.New("the worker died")
		}
		return false, nil
	}
	if _, err := f.work(); err == nil {
		t.Fatal("Work() succeeded past a lost record")
	}
	f.records.fail = nil
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() after the restart = %#v, %v", verification, err)
	}
	if interrupted := verification.Generation.Interruptions; len(interrupted) != 1 || interrupted[0].Stage != runstate.MergeQueueStageReview {
		t.Fatalf("interruptions = %#v, want the review", interrupted)
	}
	if f.checks.calls() != 1 || f.reviewer.calls() != 2 {
		t.Fatalf("checks ran %d times and the review %d, want 1 and 2", f.checks.calls(), f.reviewer.calls())
	}
}

func TestAnUncertainSaveIsSettledFromTheRecordBeforeAnythingRunsAgain(t *testing.T) {
	t.Parallel()

	// The checks' result landed and the save said it might not have: the next
	// worker reads the record, finds the checks finished, and does not run them.
	landed := newQueueFixture(t)
	landed.records.fail = func(g runstate.MergeQueueGeneration) (bool, error) {
		if g.CheckRun != nil && g.CheckRun.FinishedAt != nil {
			return true, runstate.ErrMergeQueueGenerationSaveUncertain
		}
		return false, nil
	}
	if _, err := landed.work(); !errors.Is(err, runstate.ErrMergeQueueGenerationSaveUncertain) {
		t.Fatalf("Work() error = %v, want the uncertain save reported", err)
	}
	landed.records.fail = nil
	verification, err := landed.work()
	if err != nil || !verification.Verified || landed.checks.calls() != 1 || len(verification.Generation.Interruptions) != 0 {
		t.Fatalf("Work() = %#v, %v with %d check runs; want the landed checks kept", verification, err, landed.checks.calls())
	}

	// The candidate's generation is the uncertain save: where it landed the
	// candidate is taken up as it is, and where it did not one is built anew.
	for _, wrote := range []bool{true, false} {
		f := newQueueFixture(t)
		f.records.fail = func(g runstate.MergeQueueGeneration) (bool, error) {
			if g.CheckRun == nil {
				return wrote, runstate.ErrMergeQueueGenerationSaveUncertain
			}
			return false, nil
		}
		if _, err := f.work(); !errors.Is(err, runstate.ErrMergeQueueGenerationSaveUncertain) {
			t.Fatalf("Work() error = %v, want the uncertain save reported", err)
		}
		recorded := f.generations()
		f.records.fail = nil
		verification, err := f.work()
		if err != nil || !verification.Verified || verification.Generation.Number != 1 {
			t.Fatalf("Work() after an uncertain generation save (landed %t) = %#v, %v", wrote, verification, err)
		}
		if wrote && verification.Generation.Candidate != recorded[0].Candidate {
			t.Fatal("a generation that landed was built again")
		}
		if f.checks.calls() != 1 {
			t.Fatalf("checks ran %d times, want once", f.checks.calls())
		}
	}
}

func TestALostCandidateCheckoutIsRestoredNotRebuilt(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.records.fail = func(g runstate.MergeQueueGeneration) (bool, error) {
		if g.Review != nil {
			return false, errors.New("the worker died")
		}
		return false, nil
	}
	if _, err := f.work(); err == nil {
		t.Fatal("Work() succeeded past a lost record")
	}
	generation := f.generations()[0]
	if err := os.RemoveAll(generation.Checkout); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, f.repository, "worktree", "prune")
	f.records.fail = nil
	verification, err := f.work()
	if err != nil || !verification.Verified || verification.Generation.Candidate != generation.Candidate || verification.Generation.Number != 1 {
		t.Fatalf("Work() = %#v, %v; want the recorded candidate verified", verification, err)
	}
	if f.checks.calls() != 1 {
		t.Fatalf("checks ran %d times; the finished checks belong to the restored candidate", f.checks.calls())
	}
}

func TestOneWorkerVerifiesAQueueAtATimeInAdmissionOrder(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	held, ok, err := f.queue.LeaseWorker(context.Background(), queueKey)
	if err != nil || !ok {
		t.Fatalf("LeaseWorker() = %t, %v", ok, err)
	}
	if _, err := f.work(); !errors.Is(err, ErrMergeQueueWorkerBusy) {
		t.Fatalf("Work() error = %v, want the queue reported as worked by another", err)
	}
	if f.checks.calls() != 0 || len(f.generations()) != 0 {
		t.Fatal("a worker without the lease built or checked something")
	}
	_ = held.Release()

	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	later, _, err := f.queue.Admit(context.Background(), runstate.MergeQueueAdmission{
		Key: queueKey, WorkItemID: "yoyodyne-task", WorkItemTitle: "Later work on the same item", RunID: runID,
		ApprovedHead: f.head, IntegrationPolicy: "automatic", Mode: runstate.MergeQueueHarness,
	})
	if err != nil {
		t.Fatal(err)
	}
	verification, err := f.work()
	if err != nil || verification.Entry.EntryID != f.entry.EntryID {
		t.Fatalf("Work() = %#v, %v; want the first admitted entry", verification, err)
	}
	f.worker.Waiting = func(entry runstate.MergeQueueEntry) bool { return entry.EntryID != f.entry.EntryID }
	f.runs.(*admittedRun).state.RunID = runID
	verification, err = f.work()
	if err != nil || verification.Entry.EntryID != later.EntryID {
		t.Fatalf("Work() = %#v, %v; want the next entry once the first no longer waits", verification, err)
	}
}

// moveTarget lands one commit on main in the primary checkout.
func moveTarget(t *testing.T, repository, file string) {
	t.Helper()
	writeQueueFile(t, repository, file, file+"\n")
	runPipelineGit(t, repository, "add", file)
	runPipelineGit(t, repository, "commit", "-m", "landed "+file)
}

func writeQueueFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readQueueFile(t *testing.T, root, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		return ""
	}
	return string(content)
}

// admittedRun answers for the run an entry was admitted for.
type admittedRun struct {
	StateStore
	state runstate.State
}

func (r *admittedRun) Load(runID string) (runstate.State, error) {
	if runID != r.state.RunID {
		return runstate.State{}, runstate.ErrNoRunInFlight
	}
	return r.state, nil
}

// flakyGenerations loses or half-loses the generation writes fail picks: write
// is whether the record lands anyway, and the error is what the writer is told.
type flakyGenerations struct {
	*runstate.MergeQueueStore
	fail func(runstate.MergeQueueGeneration) (write bool, err error)
}

func (f *flakyGenerations) RecordGeneration(lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) error {
	if f.fail != nil {
		if write, err := f.fail(generation); err != nil {
			if write {
				if saved := f.MergeQueueStore.RecordGeneration(lease, key, generation); saved != nil {
					return saved
				}
			}
			return err
		}
	}
	return f.MergeQueueStore.RecordGeneration(lease, key, generation)
}

type countingChecks struct {
	mu     sync.Mutex
	runner CheckRunner
	before func(call int)
	count  int
	dir    string
}

func (c *countingChecks) Run(ctx context.Context, request checks.Request, sink func(execution.Event) error) ([]checks.Result, uint64, error) {
	c.mu.Lock()
	c.count++
	call := c.count
	c.dir = request.Directory
	c.mu.Unlock()
	if c.before != nil {
		c.before(call)
	}
	return c.runner.Run(ctx, request, sink)
}

func (c *countingChecks) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (c *countingChecks) directory() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dir
}

type countingReviewer struct {
	mu       sync.Mutex
	reviewer ChangeReviewer
	before   func(call int)
	// instead answers a call in the reviewer's place where it says so.
	instead  func(call int) (review.Result, error, bool)
	requests []review.Request
}

func (r *countingReviewer) Review(ctx context.Context, request review.Request) (review.Result, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	call := len(r.requests)
	r.mu.Unlock()
	if r.before != nil {
		r.before(call)
	}
	if r.instead != nil {
		if result, err, answered := r.instead(call); answered {
			return result, err
		}
	}
	return r.reviewer.Review(ctx, request)
}

func (r *countingReviewer) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *countingReviewer) last() review.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[len(r.requests)-1]
}

type eventRecorder struct {
	mu     sync.Mutex
	events []execution.Event
}

func (e *eventRecorder) record(event execution.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
	return nil
}

func (e *eventRecorder) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

func TestEveryCheckIsWrittenDownBeforeItRunsAndHoldsItsStage(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	// Descriptor 4 is the stage's hold, which every check inherits.
	f.worker.Pipeline.Config.Checks = append(append([]string(nil), queueChecks...), "test -e /dev/fd/4")
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v", verification, err)
	}
	launches := verification.Generation.Launches
	if len(launches) != 3 {
		t.Fatalf("launches = %#v, want one for each check", launches)
	}
	for index, launch := range launches {
		if launch.Stage != runstate.MergeQueueStageChecks || launch.Command != f.worker.Pipeline.Config.Checks[index] || launch.PID <= 0 || launch.Host == "" {
			t.Fatalf("launch %d = %#v, want the check's process written down", index, launch)
		}
	}
}

func TestARestartWaitsOutAStageAnEarlierWorkerLeftRunning(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.records.fail = func(g runstate.MergeQueueGeneration) (bool, error) {
		if g.CheckRun != nil && g.CheckRun.FinishedAt != nil {
			return false, errors.New("the worker died")
		}
		return false, nil
	}
	if _, err := f.work(); err == nil {
		t.Fatal("Work() succeeded past a lost record")
	}
	f.records.fail = nil
	generation := f.generations()[0]
	writeQueueFile(t, generation.Checkout, "still-building.out", "half\n")

	// A process the dead worker started still holds the checks' hold.
	orphan, err := os.OpenFile(f.queue.StageHoldPath(queueKey, generation, runstate.MergeQueueStageChecks), os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(orphan.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	verification, err := f.work()
	if err != nil || verification.Waiting == "" || verification.Verified || !strings.Contains(verification.Waiting, "still running") {
		t.Fatalf("Work() = %#v, %v; want it to wait for the running checks", verification, err)
	}
	if f.checks.calls() != 1 || readQueueFile(t, generation.Checkout, "still-building.out") != "half\n" {
		t.Fatal("a second run of the checks started, or their checkout was touched, while the first was still running")
	}
	if recorded := f.generations()[0]; len(recorded.Interruptions) != 0 || recorded.CheckRun == nil || recorded.CheckRun.FinishedAt != nil {
		t.Fatalf("generation = %#v, want the running checks left exactly as recorded", recorded)
	}

	// Once it has ended, the checks are set aside and run again.
	orphan.Close()
	verification, err = f.work()
	if err != nil || !verification.Verified || len(verification.Generation.Interruptions) != 1 || f.checks.calls() != 2 {
		t.Fatalf("Work() = %#v, %v with %d check runs; want the checks run again once the first ended", verification, err, f.checks.calls())
	}
}

func TestTheOperatorsPauseHoldsTheQueuesChecksAndReview(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	holds := f.worker.Pipeline.Holds.(*runstate.OperatorHoldStore)
	if _, err := holds.Hold(time.Now()); err != nil {
		t.Fatal(err)
	}
	verification, err := f.work()
	if err != nil || !strings.Contains(verification.Waiting, "paused harness activity") || f.checks.calls() != 0 {
		t.Fatalf("Work() = %#v, %v with %d check runs; want nothing started while paused", verification, err, f.checks.calls())
	}
	if _, _, err := holds.Release(); err != nil {
		t.Fatal(err)
	}

	// Paused while the checks run: the checks finish, and the review waits.
	f.checks.before = func(call int) {
		if call == 1 {
			if _, err := holds.Hold(time.Now()); err != nil {
				t.Error(err)
			}
		}
	}
	verification, err = f.work()
	if err != nil || !strings.Contains(verification.Waiting, "review") || f.reviewer.calls() != 0 {
		t.Fatalf("Work() = %#v, %v with %d reviews; want the review held", verification, err, f.reviewer.calls())
	}
	if _, _, err := holds.Release(); err != nil {
		t.Fatal(err)
	}
	verification, err = f.work()
	if err != nil || !verification.Verified || f.checks.calls() != 1 || f.reviewer.calls() != 1 {
		t.Fatalf("Work() = %#v, %v; want the review asked once the pause lifted, and the checks kept", verification, err)
	}
}

func TestAReviewTheProviderRefusedIsAWaitThatEarnsNothing(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	limits := &countingUsageLimits{}
	f.worker.UsageLimits = limits
	f.reviewer.instead = func(call int) (review.Result, error, bool) {
		if call != 1 {
			return review.Result{}, nil, false
		}
		return review.Result{UsageLimit: &backend.UsageLimit{Kind: "five_hour"}}, errors.New("usage limit reached"), true
	}
	verification, err := f.work()
	if err != nil || verification.Verified || !strings.Contains(verification.Waiting, "usage limit") || limits.count != 1 {
		t.Fatalf("Work() = %#v, %v; want a wait with the refusal recorded", verification, err)
	}
	if err := verification.Generation.Gate(f.configured()); err == nil {
		t.Fatal("a refused review passed the gate")
	}
	verification, err = f.work()
	if err != nil || !verification.Verified || f.reviewer.calls() != 2 || f.checks.calls() != 1 {
		t.Fatalf("Work() = %#v, %v; want the review asked again and approved", verification, err)
	}
	if set := verification.Generation.Interruptions; len(set) != 1 || set[0].Stage != runstate.MergeQueueStageReview || !strings.Contains(set[0].Problem, "usage limit") {
		t.Fatalf("interruptions = %#v, want the refused review set aside", set)
	}
}

func TestAKnownUsageLimitHoldsTheReviewBeforeTheProviderIsAsked(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.worker.Pipeline.EndpointLimits = knownLimit{says: "the five-hour limit"}
	verification, err := f.work()
	if err != nil || !strings.Contains(verification.Waiting, "out of capacity") || f.reviewer.calls() != 0 || f.checks.calls() != 1 {
		t.Fatalf("Work() = %#v, %v; want the checks run and the review held", verification, err)
	}
}

type countingUsageLimits struct{ count int }

func (c *countingUsageLimits) Record(runstate.UsageLimitExhaustion) error {
	c.count++
	return nil
}

type knownLimit struct{ says string }

func (k knownLimit) KnownLimited(string, string, time.Time) (KnownLimit, bool, error) {
	return KnownLimit{Says: k.says}, true, nil
}
