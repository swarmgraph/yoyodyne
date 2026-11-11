package orchestrator

// The merge queue's checks and its independent review of a candidate run side
// by side (docs/designs/merge-queue-pace.md, decision 2): both start together on
// the same candidate commit, a failing check stops the review, a refusing
// review stops the checks, and only every check passing and the review
// approving verifies the candidate.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// meet blocks until signal is closed, failing the test if it is not closed
// within a bound: it is how one stage proves the other started while it was
// still going.
func meet(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(30 * time.Second):
		t.Errorf("%s never happened while the other stage was still going", what)
	}
}

func TestTheQueuesChecksAndReviewStartTogetherOnTheSameCandidate(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	checksStarted, reviewStarted := make(chan struct{}), make(chan struct{})
	// Each stage waits, before it goes on, for the other to have started. Run
	// one after the other, the first would wait out the bound and fail.
	f.checks.before = func(call int) {
		if call == 1 {
			close(checksStarted)
			meet(t, reviewStarted, "the review starting")
		}
	}
	f.reviewer.before = func(call int) {
		if call == 1 {
			close(reviewStarted)
			meet(t, checksStarted, "the checks starting")
		}
	}
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v; want the candidate verified", verification, err)
	}
	generation := verification.Generation
	binding := generation.Binding()
	if generation.CheckRun.Binding != binding || generation.Review.Binding != binding {
		t.Fatalf("checks bound to %s and review to %s, want both bound to the candidate's %s", generation.CheckRun.Binding, generation.Review.Binding, binding)
	}
	if !generation.Review.StartedAt.Before(*generation.CheckRun.FinishedAt) || !generation.CheckRun.StartedAt.Before(*generation.Review.FinishedAt) {
		t.Fatalf("checks ran %s to %s and the review %s to %s, want them overlapping",
			generation.CheckRun.StartedAt, generation.CheckRun.FinishedAt, generation.Review.StartedAt, generation.Review.FinishedAt)
	}
	// The reviewer judged exactly the commit the checks ran on, and was told
	// the checks were running beside it rather than shown results it could not
	// have had.
	request := f.reviewer.last()
	if !strings.Contains(request.Context, generation.Candidate) || request.WorktreePath != f.checks.directory() {
		t.Fatalf("review asked about %q in %s, want candidate %s in the checks' checkout %s", request.Context, request.WorktreePath, generation.Candidate, f.checks.directory())
	}
	if len(request.Checks) != 0 || !strings.Contains(request.ChecksBeside, "running on this same candidate") ||
		!strings.Contains(request.Context, "running on this same candidate while you review it") {
		t.Fatalf("review shown checks %#v with %q, want none and the checks said to be running beside it", request.Checks, request.ChecksBeside)
	}
	// Both stages wrote the one event stream, which stays numbered in order
	// with no number used twice, and the generation records where it ends.
	f.events.mu.Lock()
	events := append([]execution.Event(nil), f.events.events...)
	f.events.mu.Unlock()
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d has sequence %d, want the stream numbered in order", index, event.Sequence)
		}
	}
	if generation.LastSequence != uint64(len(events)) {
		t.Fatalf("generation records event %d as its last, and %d were written", generation.LastSequence, len(events))
	}
	if f.checks.calls() != 1 || f.reviewer.calls() != 1 {
		t.Fatalf("checks ran %d times and the review %d, want each once", f.checks.calls(), f.reviewer.calls())
	}
}

// stoppableReviewer is a reviewer's provider that answers nothing until it is
// stopped, as a review still being written when a check fails is.
type stoppableReviewer struct {
	started chan struct{}
	once    sync.Once
}

func (s *stoppableReviewer) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
	case <-time.After(60 * time.Second):
		return backend.RunResult{}, errors.New("the review was never stopped")
	}
	return backend.RunResult{
		Backend: domain.BackendClaudeCode, SessionID: "stopped-reviewer-session", IsError: true,
		Process: execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
	}, ctx.Err()
}

func TestAFailingCheckEndsTheCandidateAndStopsTheReviewWithItsSpendRecorded(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	// A check the target passes at the candidate's base and the change fails:
	// the change adds the file it says must not be there.
	f.worker.Pipeline.Config.Checks = []string{"test ! -f feature.txt"}
	provider := &stoppableReviewer{started: make(chan struct{})}
	spent := &recordingSpendLog{}
	f.reviewer.reviewer = review.Reviewer{Backend: provider, Model: testReviewerModel, Spend: spent}
	// The check fails only once the reviewer is already at work on the
	// candidate, so what stops is a review in progress.
	f.checks.before = func(int) { meet(t, provider.started, "the review starting") }

	verification, err := f.work()
	if err != nil || verification.Verified || !strings.Contains(verification.Refusal, "test ! -f feature.txt did not pass") {
		t.Fatalf("Work() = %#v, %v; want the candidate ended by its failing check", verification, err)
	}
	generation := verification.Generation
	stopped := generation.Review
	if stopped == nil || stopped.FinishedAt == nil || stopped.Decision != "" || stopped.Binding != generation.Binding() ||
		!strings.Contains(stopped.Problem, "stopped because test ! -f feature.txt failed on the candidate") {
		t.Fatalf("review evidence = %#v, want the review stopped by the failing check and said so", stopped)
	}
	// The stopped review's provider invocation is on the cost log, charged to
	// the entry's run and work item as review spend.
	if len(spent.lines) != 1 {
		t.Fatalf("cost log = %#v, want one line for the stopped review", spent.lines)
	}
	if line := spent.lines[0]; line.RunID != f.entry.RunID || line.WorkItemID != "yoyodyne-task" || line.Phase != runstate.SpendPhaseReview || line.Role != domain.RoleReviewer {
		t.Fatalf("cost line = %#v, want the review's spend charged to run %s and its item", line, f.entry.RunID)
	}
	// The failure is the change's: the target passes the check at the base.
	if generation.BaseCheck == nil || !generation.BaseCheck.Settled() || !generation.BaseCheck.Passed {
		t.Fatalf("base check = %#v, want the target found passing at the base", generation.BaseCheck)
	}
	failure, failed, err := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{Generations: f.generations(), Configured: f.configured()})
	if err != nil || !failed || failure.Class != runstate.MergeQueueCandidateDefect || failure.Check != "test ! -f feature.txt" {
		t.Fatalf("ClassifyMergeQueueFailure() = %#v, %v, %v; want the change's defect on its failing check", failure, failed, err)
	}
	if _, err := f.queue.VerifiedGeneration(queueKey, f.entry.EntryID, f.configured()); err == nil {
		t.Fatal("a candidate whose check failed is one a promotion may land")
	}
	// Nothing is asked again of the ended candidate. The checks ran twice: on
	// the candidate, and the failing one on the target at its base.
	if f.checks.calls() != 2 {
		t.Fatalf("checks ran %d times, want the candidate's and the base's", f.checks.calls())
	}
	again, err := f.work()
	if err != nil || again.Verified || f.checks.calls() != 2 || f.reviewer.calls() != 1 {
		t.Fatalf("second Work() = %#v, %v with %d check runs and %d reviews; want nothing run again", again, err, f.checks.calls(), f.reviewer.calls())
	}
}

func TestARefusingReviewEndsTheCandidateAndStopsTheChecks(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t, repairVerdict)
	// A check that would run for half a minute, and ends by itself whatever
	// happens to the test.
	slow := `i=0; while [ "$i" -lt 300 ]; do sleep 0.1; i=$((i+1)); done`
	f.worker.Pipeline.Config.Checks = []string{"test -f feature.txt", slow}
	checksStarted := make(chan struct{})
	f.checks.before = func(call int) {
		if call == 1 {
			close(checksStarted)
		}
	}
	f.reviewer.before = func(call int) { meet(t, checksStarted, "the checks starting") }

	began := time.Now()
	verification, err := f.work()
	if err != nil || verification.Verified || !strings.Contains(verification.Refusal, `"repair"`) {
		t.Fatalf("Work() = %#v, %v; want the candidate ended by the review's refusal", verification, err)
	}
	if took := time.Since(began); took > 25*time.Second {
		t.Fatalf("Work() took %s, want the slow check stopped rather than run to its end", took)
	}
	generation := verification.Generation
	if generation.Review == nil || generation.Review.Decision != string(review.DecisionRepair) || generation.Review.Binding != generation.Binding() {
		t.Fatalf("review evidence = %#v, want the reviewer's refusal of this candidate", generation.Review)
	}
	checked := generation.CheckRun
	if checked == nil || checked.FinishedAt == nil || checked.Binding != generation.Binding() ||
		!strings.Contains(checked.Problem, `stopped because the independent reviewer decided "repair"`) {
		t.Fatalf("check evidence = %#v, want the checks stopped by the refusal and said so", checked)
	}
	failure, failed, err := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{Generations: f.generations(), Configured: f.configured()})
	if err != nil || !failed || failure.Class != runstate.MergeQueueCandidateDefect || !strings.Contains(failure.Reason, `the independent reviewer decided "repair"`) {
		t.Fatalf("ClassifyMergeQueueFailure() = %#v, %v, %v; want the change's defect, read from the refusal", failure, failed, err)
	}
	if _, err := f.queue.VerifiedGeneration(queueKey, f.entry.EntryID, f.configured()); err == nil {
		t.Fatal("a candidate its reviewer refused is one a promotion may land")
	}
	// The checks it stopped are not run again for a candidate that has ended.
	again, err := f.work()
	if err != nil || again.Verified || f.checks.calls() != 1 || f.reviewer.calls() != 1 {
		t.Fatalf("second Work() = %#v, %v with %d check runs and %d reviews; want nothing run again", again, err, f.checks.calls(), f.reviewer.calls())
	}
}

func TestACandidateIsVerifiedOnlyWithEveryCheckPassedAndTheReviewApproving(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v", verification, err)
	}
	configured := f.configured()
	verified := verification.Generation
	if err := verified.Gate(configured); err != nil {
		t.Fatalf("Gate() = %v on every check passed and the review approving", err)
	}
	stoppedAt := *verified.CheckRun.FinishedAt
	for name, change := range map[string]func(*runstate.MergeQueueGeneration){
		"a check failed and the review was stopped": func(g *runstate.MergeQueueGeneration) {
			checked := *g.CheckRun
			checked.Results = append([]runstate.MergeQueueCheckResult(nil), checked.Results...)
			checked.Results[1].Passed, checked.Results[1].ExitCode = false, 1
			g.CheckRun = &checked
			reviewed := *g.Review
			reviewed.Decision, reviewed.Problem = "", "stopped because sh ./scripts/check.sh failed on the candidate"
			g.Review = &reviewed
		},
		"the review refused and the checks were stopped": func(g *runstate.MergeQueueGeneration) {
			checked := *g.CheckRun
			checked.Results = checked.Results[:1]
			checked.FinishedAt, checked.Problem = &stoppedAt, `stopped because the independent reviewer decided "repair"`
			g.CheckRun = &checked
			reviewed := *g.Review
			reviewed.Decision = string(review.DecisionRepair)
			g.Review = &reviewed
		},
		"the review refused after every check passed": func(g *runstate.MergeQueueGeneration) {
			reviewed := *g.Review
			reviewed.Decision = string(review.DecisionRepair)
			g.Review = &reviewed
		},
		"every check passed and the review was stopped": func(g *runstate.MergeQueueGeneration) {
			reviewed := *g.Review
			reviewed.Decision, reviewed.Problem = "", "stopped"
			g.Review = &reviewed
		},
	} {
		revised := verified
		change(&revised)
		if err := revised.Gate(configured); err == nil {
			t.Errorf("Gate() passed a candidate where %s", name)
		}
	}
}
