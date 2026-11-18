package orchestrator

// With checks on CI on, a merge-queue candidate runs only the quick local
// checks, its review runs beside the wait for the forge's required checks, and
// a required check failing on the candidate stops the review
// (mergequeuequick.go).

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// queueForge is the forge a queue candidate is pushed to and read from. answer
// gives each required check's result on a commit, told how many reads came
// before.
type queueForge struct {
	mu       sync.Mutex
	answer   func(commit, name string, read int) publish.CheckResult
	reads    int
	pushes   []string
	ensured  []publish.Request
	readsFor []string
}

func (f *queueForge) PushBranch(context.Context, publish.PushRequest) error { return nil }

func (f *queueForge) EnsureDraft(context.Context, publish.Request) (publish.PullRequest, error) {
	return publish.PullRequest{}, errors.New("a queue candidate is never opened as a draft")
}

func (f *queueForge) RequiredChecks(_ context.Context, commit string, required []string) (publish.RequiredCheckReading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	read := f.reads
	f.reads++
	f.readsFor = append(f.readsFor, commit)
	reading := publish.RequiredCheckReading{Commit: commit}
	for _, name := range required {
		reading.Checks = append(reading.Checks, publish.RequiredCheck{Name: name, Result: f.answer(commit, name, read), Commit: commit})
	}
	return reading, nil
}

func (f *queueForge) CheckOutput(context.Context, publish.RequiredCheck) (publish.CheckOutput, error) {
	return publish.CheckOutput{}, nil
}

func (f *queueForge) WorkflowDigest(context.Context, string) (string, error) {
	return "sha256:workflows-v1", nil
}

func (f *queueForge) Availability(context.Context) (publish.Availability, error) {
	return publish.Availability{}, nil
}

func (f *queueForge) Ensure(_ context.Context, request publish.Request) (publish.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, request)
	return publish.PullRequest{Number: 41, URL: "https://forge.example/pull/41", State: "OPEN"}, nil
}

func (f *queueForge) Merge(context.Context, publish.MergeRequest) (publish.MergeResult, error) {
	return publish.MergeResult{}, errors.New("the worker never merges")
}

func (f *queueForge) State(context.Context, string) (publish.PullRequest, error) {
	return publish.PullRequest{}, nil
}

func (f *queueForge) Protection(context.Context, string) (publish.BranchProtection, error) {
	return publish.BranchProtection{Protected: true}, nil
}

func (f *queueForge) QueueCandidateBranchCommit(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (f *queueForge) PublishQueueCandidate(_ context.Context, branch, candidate, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, branch+"@"+candidate)
	return nil
}

func (f *queueForge) pushed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pushes)
}

// quickFixture is the queue fixture with checks on CI on: its first check is
// the quick local check, and the forge requires "test" and "race".
func quickFixture(t *testing.T, answer func(commit, name string, read int) publish.CheckResult) (*queueFixture, *queueForge) {
	t.Helper()
	f := newQueueFixture(t)
	forge := &queueForge{answer: answer}
	p := f.worker.Pipeline
	p.CIForge, p.Publisher = forge, forge
	p.Config.Approvals.Publishing = domain.ApprovalAutomatic
	p.Config.Execution.CIChecks.Enabled = true
	p.Config.Execution.CIChecks.Local = []string{queueChecks[0]}
	p.Config.Execution.CIChecks.Required = []string{"test", "race"}
	f.worker.Publish = forge
	f.worker.ForgePoll = 10 * time.Millisecond
	return f, forge
}

func TestWithChecksOnCIOnlyTheQuickChecksAndTheOwedRaceCheckRunLocally(t *testing.T) {
	t.Parallel()

	p := &Pipeline{}
	p.Config.Checks = []string{"make fmtcheck", "make test", "make race", "make vet", "make build", "make test-macos"}
	p.Config.MergeQueueRaceCheck = "make race"
	p.Config.Execution.CIChecks.Local = []string{"make test-macos", "make fmtcheck", "make vet", "make build"}
	entry := runstate.MergeQueueEntry{RunID: "run-missing", ApprovedHead: strings.Repeat("a", 40)}

	// The target requires a race check of its own, so none runs here.
	p.Config.Execution.CIChecks.Required = []string{"test", "race", "vet"}
	got := p.quickQueueChecks(entry)
	if want := []string{"make fmtcheck", "make vet", "make build", "make test-macos"}; !slices.Equal(got.Commands, want) {
		t.Fatalf("commands = %v, want %v", got.Commands, want)
	}
	if !slices.Equal(got.ForgeChecks, []string{"test", "race", "vet"}) || got.Declared == nil {
		t.Fatalf("configuration = %#v, want the required checks recorded beside the quick list", got)
	}

	// While it requires none, the race check runs here, narrowed by the worker
	// to what the candidate touches (addedRaceCheck), for a change that owes it.
	p.Config.Execution.CIChecks.Required = []string{"test", "vet"}
	got = p.quickQueueChecks(entry)
	if want := []string{"make fmtcheck", "make vet", "make build", "make test-macos", "make race"}; !slices.Equal(got.Commands, want) {
		t.Fatalf("commands = %v, want %v", got.Commands, want)
	}
	if slices.Contains(got.Declared.Commands, "make race") {
		t.Fatalf("declared = %v, want the race check left off it so it is run narrowed", got.Declared.Commands)
	}

	// A different required list is a different check set.
	if got.Same(p.quickQueueChecks(entry)) == false {
		t.Fatal("the same configuration read twice differs")
	}
	whole := runstate.NewMergeQueueDeclaredConfiguration(got.Commands, got.Declared.Commands)
	if got.Same(whole) {
		t.Fatal("a configuration leaning on the forge's checks reads as the same as one that does not")
	}
}

func TestWithChecksOnCIACandidateRunsOnlyTheQuickChecksAndLandsOnTheForgesPass(t *testing.T) {
	t.Parallel()

	f, forge := quickFixture(t, func(string, string, int) publish.CheckResult { return publish.CheckPassed })
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v, want the candidate verified", verification, err)
	}
	if ran := f.checks.ran(); len(ran) != 1 || !slices.Equal(ran[0], []string{queueChecks[0]}) {
		t.Fatalf("checks run = %v, want only the quick check %q", ran, queueChecks[0])
	}
	generation := verification.Generation
	if pushed := forge.pushed(); len(pushed) != 1 || pushed[0] != runstate.MergeQueueCandidateBranch(f.entry.EntryID)+"@"+generation.Candidate {
		t.Fatalf("pushes = %v, want the candidate on its candidate branch once", pushed)
	}
	if generation.ForgePush == nil || generation.ForgePush.PullRequest != 41 {
		t.Fatalf("push record = %#v, want the pull request that carries it", generation.ForgePush)
	}
	if generation.CIChecks == nil || generation.CIChecks.Commit != generation.Candidate || generation.CIChecks.WorkflowDigest == "" {
		t.Fatalf("forge reading = %#v, want the passing reading on the candidate recorded", generation.CIChecks)
	}
	if f.reviewer.calls() != 1 {
		t.Fatalf("reviews = %d, want one", f.reviewer.calls())
	}
	// Without the forge's reading the quick checks alone do not land it.
	stripped := generation
	stripped.CIChecks = nil
	if err := stripped.Gate(generation.Checks); err == nil || !strings.Contains(err.Error(), "forge's required checks") {
		t.Fatalf("Gate() without the forge's reading = %v, want a refusal naming it", err)
	}
	// The check the run left whole to the queue is covered by the forge's.
	if refusal := mergeQueueStandsIn(runstate.State{RunID: "run", ChecksPassed: &runstate.ChecksPassed{WholeLeftToQueue: true, Commands: []string{queueChecks[1]}}},
		runstate.MergeQueueHarness, generation, nil, queueDeclaration{}); refusal != "" {
		t.Fatalf("mergeQueueStandsIn() = %q, want the forge's checks to stand in", refusal)
	}
}

// blockingReviewer holds the review until its context ends, and says whether
// it was ended.
type blockingReviewer struct {
	started chan struct{}
	ended   chan error
}

func (r *blockingReviewer) Review(ctx context.Context, request review.Request) (review.Result, error) {
	close(r.started)
	<-ctx.Done()
	r.ended <- ctx.Err()
	return review.Result{}, ctx.Err()
}

func TestAFailingRequiredCheckStopsTheReview(t *testing.T) {
	t.Parallel()

	var base string
	reviewer := &blockingReviewer{started: make(chan struct{}), ended: make(chan error, 1)}
	reviewing := func() bool {
		select {
		case <-reviewer.started:
			return true
		default:
			return false
		}
	}
	f, _ := quickFixture(t, func(commit, name string, read int) publish.CheckResult {
		switch {
		case commit == base:
			return publish.CheckPassed
		case !reviewing():
			// Still running until the reviewer has the candidate.
			return publish.CheckPending
		case name == "test":
			return publish.CheckFailed
		}
		return publish.CheckPassed
	})
	base = gitLine(t, f.repository, "rev-parse", "main")
	f.worker.Pipeline.Reviewer = reviewer

	verification, err := f.work()
	if err != nil {
		t.Fatalf("Work() = %v", err)
	}
	select {
	case <-reviewer.started:
	default:
		t.Fatal("the review never started beside the wait")
	}
	if ended := <-reviewer.ended; !errors.Is(ended, context.Canceled) {
		t.Fatalf("the review ended with %v, want it stopped", ended)
	}
	if verification.Verified || !strings.Contains(verification.Refusal, "test") {
		t.Fatalf("Work() = %#v, want a refusal naming the failed required check", verification)
	}
	generations := f.generations()
	generation := generations[len(generations)-1]
	if failed := generation.CIChecks.FailedOnForge(); !slices.Equal(failed, []string{"test"}) {
		t.Fatalf("failed on the forge = %v, want test", failed)
	}
	if result, read := generation.CIChecks.ResultAtBase("test"); !read || result != "passed" {
		t.Fatalf("result at the base = %q, %v, want it read as passed", result, read)
	}
	// The failure is the change's, handed back like a failing local check.
	failure, failed, err := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{
		Generations: generations, Configured: f.worker.Pipeline.mergeQueueChecks(f.entry, generations),
	})
	if err != nil || !failed || failure.Class != runstate.MergeQueueCandidateDefect || failure.Check != "test" {
		t.Fatalf("ClassifyMergeQueueFailure() = %#v, %v, %v, want a defect of the change on test", failure, failed, err)
	}
}

func TestAnApprovedCandidateWaitsForTheForgesChecksToFinish(t *testing.T) {
	t.Parallel()

	f, _ := quickFixture(t, func(string, string, int) publish.CheckResult { return publish.CheckPending })
	verification, err := f.work()
	if err != nil || verification.Verified || !strings.Contains(verification.Waiting, "not finished yet") {
		t.Fatalf("Work() = %#v, %v, want it waiting on the forge", verification, err)
	}
	generations := f.generations()
	if _, failed, err := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{
		Generations: generations, Configured: f.worker.Pipeline.mergeQueueChecks(f.entry, generations),
	}); failed || err != nil {
		t.Fatalf("ClassifyMergeQueueFailure() = %v, %v, want a candidate still being verified", failed, err)
	}
}

func TestACheckTheForgeCannotSettleHasTheNextCandidateRunTheFullLocalChecks(t *testing.T) {
	t.Parallel()

	f, _ := quickFixture(t, func(string, string, int) publish.CheckResult { return publish.CheckCancelled })
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v, want the entry verified by the full local checks", verification, err)
	}
	generations := f.generations()
	if len(generations) != 2 || generations[0].CIChecks == nil || !strings.Contains(generations[0].CIChecks.FellBack, "cancelled") {
		t.Fatalf("generations = %#v, want the first set aside with the reason recorded", generations)
	}
	second := generations[1]
	if len(second.Checks.ForgeChecks) != 0 || !slices.Equal(second.Checks.Commands, queueChecks) {
		t.Fatalf("second candidate's checks = %#v, want the full local checks", second.Checks)
	}
}
