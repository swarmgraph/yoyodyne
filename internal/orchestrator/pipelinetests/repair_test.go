package pipelinetests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordingReviewer keeps the evidence each round was reviewed against, and
// hands the review on unchanged. What it is for is the one fact no assertion
// after the run can recover: the evidence of a round that has been superseded by
// the next one.
type recordingReviewer struct {
	reviewer orchestrator.ChangeReviewer
	evidence []gitworktree.ChangeDiff
}

func (r *recordingReviewer) Review(ctx context.Context, request review.Request) (review.Result, error) {
	r.evidence = append(r.evidence, request.Changes)
	return r.reviewer.Review(ctx, request)
}

// A repair round is reviewed against the round's own change, and the evidence
// says so at the grain the evidence is bound to: the tip commit.
//
// Everything the reviewer is shown is measured against the base commit, so a
// change left in the working tree reaches it either way — which is why this went
// unnoticed. What did not reach it was the tip: the branch a publishing run
// pushes, the commits a reviewer is told the patch spans, and the tip the
// verdict is recorded against all named a commit from before the round. On
// run-f3755e3f the branch tip sat at the repair-3 commit d18d295 through four
// further invocations, its developer reporting that both findings were fixed in
// a worktree HEAD did not carry.
func TestPipelineReviewsARepairRoundAgainstATipThatCarriesIt(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		content := "incomplete\n"
		if attempts > 1 {
			content = "implemented\n"
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(content), 0o600)
	}, testharness.RepairVerdict, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	recorder := &recordingReviewer{reviewer: pipeline.Reviewer}
	pipeline.Reviewer = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.RepairAttempts != 1 || outcome.Integration == nil {
		t.Fatalf("Run() outcome = %#v, want one repair round promoted", outcome)
	}
	if len(recorder.evidence) != 2 {
		t.Fatalf("reviews = %d, want the first attempt and its repair", len(recorder.evidence))
	}
	first, repair := recorder.evidence[0], recorder.evidence[1]

	// Each round's tip is a commit, and the two are different commits: a repair
	// reviewed at the tip its predecessor was reviewed at is a repair judged on
	// the code it replaced.
	if first.HeadCommit == "" || first.HeadCommit == first.BaseCommit {
		t.Fatalf("the first round was reviewed at %q against base %q, want its own commit", first.HeadCommit, first.BaseCommit)
	}
	if repair.HeadCommit == first.HeadCommit {
		t.Fatalf("the repair round was reviewed at %q, the tip its first attempt was reviewed at", repair.HeadCommit)
	}
	// And each tip carries that round's change, which is the whole of the claim.
	if shown := testharness.GitLine(t, repository, "show", first.HeadCommit+":feature.txt"); shown != "incomplete" {
		t.Errorf("the first round's tip carries %q, want the attempt the reviewer sent back", shown)
	}
	if shown := testharness.GitLine(t, repository, "show", repair.HeadCommit+":feature.txt"); shown != "implemented" {
		t.Errorf("the repair round's tip carries %q, want the repair the round made", shown)
	}
	// The promoted commit is the tip the approving verdict was given, rather than
	// a commit made after it out of a worktree nobody reviewed.
	if outcome.Integration.SourceCommit != repair.HeadCommit {
		t.Errorf("promoted %q, want the tip the approval was recorded against (%q)", outcome.Integration.SourceCommit, repair.HeadCommit)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReviewHeadCommit != repair.HeadCommit {
		t.Errorf("the record names %q as what was reviewed, want the repair round's tip %q", state.ReviewHeadCommit, repair.HeadCommit)
	}
}

// A round the harness cannot commit never reaches the checks or the reviewer.
// Both of those judge the worktree and are recorded against a tip commit, so a
// tip that is not the round's is how an approval comes to authorize a change
// nobody read — which makes a commit that refuses the end of the round rather
// than something to carry on past.
func TestPipelineFailsARoundItCannotCommit(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	pipeline.Worktrees = refusingCommitWorktrees{WorktreeManager: pipeline.Worktrees}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "commit what the developer attempt left in the worktree") {
		t.Fatalf("Run() error = %v, want the round failed on the commit", err)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a round that was never committed reached integration: %#v, closed = %t", outcome.Integration, tracker.Closed)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 0 {
		t.Fatalf("reviewer invocations = %d, want none: the round ended at the commit", reviews)
	}
}

// refusingCommitWorktrees is the real manager with the attempt's commit refused,
// which is the one failure this pipeline has no other way to produce: the commit
// is the harness's own Git write, and everything that would make it fail is
// outside the repository.
type refusingCommitWorktrees struct{ orchestrator.WorktreeManager }

func (refusingCommitWorktrees) CommitAttempt(context.Context, gitworktree.Worktree, string) (string, error) {
	return "", errors.New("the object store is read-only")
}

func TestPipelineBlocksTheItemWhenTheRepairBudgetIsSpent(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name             string
		limit            int
		wantDeveloperRun int
	}{
		{name: "two permitted attempts", limit: 2, wantDeveloperRun: 3},
		// A project that permits no repair returns the findings to nobody: the
		// first repair verdict is already the end of the budget.
		{name: "no permitted attempts", limit: 0, wantDeveloperRun: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, testharness.RepairVerdict)
			pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			pipeline.Config.Execution.RepairAttemptsBeforeReplan = test.limit
			before := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopRepairBudget)
			if outcome.StopClass != runstate.StopRepairBudget {
				t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.StopRepairBudget)
			}
			wantFailure := fmt.Sprintf("independent review requires repair after %d of %d permitted attempt(s)", test.limit, test.limit)
			if err == nil || !strings.Contains(err.Error(), wantFailure) {
				t.Fatalf("Run() error = %v, want %q", err, wantFailure)
			}
			if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != test.wantDeveloperRun {
				t.Fatalf("developer invocations = %d, want %d", runs, test.wantDeveloperRun)
			}
			if outcome.Integration != nil || tracker.Closed {
				t.Fatalf("unapproved change was integrated: %#v, closed = %t", outcome.Integration, tracker.Closed)
			}
			if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
				t.Fatalf("main moved without an approval: %q, want %q", head, before)
			}

			// The findings the developer never resolved are recorded where the
			// work is tracked, rather than ending with the failed run.
			if !tracker.Blocked || !outcome.Blocked {
				t.Fatalf("spent repair budget did not block the item: tracker = %t, outcome = %t", tracker.Blocked, outcome.Blocked)
			}
			for _, want := range []string{
				fmt.Sprintf("Repair attempts: %d of %d permitted", test.limit, test.limit),
				"Finding [blocker] (feature.txt:1): add the missing file",
				outcome.WorktreePath,
				outcome.Branch,
			} {
				if !strings.Contains(tracker.BlockReason, want) {
					t.Fatalf("blocker is missing %q:\n%s", want, tracker.BlockReason)
				}
			}
			// The work is preserved for whoever replans it.
			if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
				t.Fatalf("blocked worktree was not preserved: %v", err)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if state.Status != runstate.StatusFailed || state.RepairAttempts != test.limit || len(state.ReviewFindingDetails) != 1 {
				t.Fatalf("state = %#v", state)
			}
		})
	}
}

// A run whose record the durable schema refuses must not read afterwards as a
// run nothing ever ended, whichever way its review went.
//
// The refusal is real: the schema rejects one field, the save fails, and what
// survives on disk is the pre-review snapshot with the evidence cleared. The run
// itself carries on knowing better — it reports the verdict to the tracker and
// the operator exactly as it would have — so the divergence is invisible from the
// process that caused it, and everything that reads the record afterwards
// believes the snapshot.
//
// Both verdicts are here because they end the run down different routes, and only
// one of them ends it directly. A repair verdict that spends the budget goes
// straight to the failure that ends the run. An approval goes to the promotion it
// authorized, whose own first save carries the refused field and hands the run to
// that same failure — so the guarantee covers the approved run through the route
// it actually takes rather than by assertion. What makes that work is the field:
// a schema refusal follows it into every later write, so the write that ends the
// run is refused for the reason the first one was.
func TestARefusedRecordStillEndsTheRunOnDisk(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		verdict  string
		refuse   func(runstate.State) bool
		problem  string
		field    string
		blocks   bool
		refusals int
	}{
		{
			name:     "the reviewer required repair",
			verdict:  testharness.RepairVerdict,
			refuse:   func(state runstate.State) bool { return len(state.ReviewFindingDetails) > 0 },
			problem:  `invalid run state: review_finding_details[0]: severity "advisory" must be "blocker", "major", or "minor"`,
			field:    "review_finding_details[0]",
			blocks:   true,
			refusals: 1,
		},
		{
			// The verdict itself is what the schema will not take here, which is the
			// same trap one field over: a decision vocabulary that grew where the
			// durable one did not.
			name:    "the reviewer approved",
			verdict: testharness.ApproveVerdict,
			refuse:  func(state runstate.State) bool { return state.ReviewDecision != "" },
			problem: "invalid run state: review_decision is invalid",
			field:   "review_decision",
			// The promotion's own record is refused, and then the record that ends
			// the run over it.
			refusals: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, test.verdict)
			pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
			refusing := &refusingStore{StateStore: pipeline.Store, refuse: test.refuse, problem: test.problem}
			pipeline.Store = refusing
			before := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("Run() error = %v, want the refused field %q named", err, test.field)
			}
			if refusing.refusals != test.refusals {
				t.Fatalf("refusals = %d, want %d", refusing.refusals, test.refusals)
			}
			// Nothing is promoted over a record nothing could store.
			if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
				t.Fatalf("main moved: %q, want %q", head, before)
			}
			// The run reported the verdict everywhere else exactly as it would have,
			// which is what made the divergence silent.
			if outcome.Blocked != test.blocks || tracker.Blocked != test.blocks {
				t.Fatalf("the refused save changed what the run reported: outcome = %t, tracker = %t, want %t",
					outcome.Blocked, tracker.Blocked, test.blocks)
			}

			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !state.Status.Terminal() || state.CompletedAt == nil {
				t.Fatalf("a refused save left the run readable as one still in flight: %#v", state)
			}
			for _, want := range []string{"could not be stored", test.field} {
				if !strings.Contains(state.Failure, want) {
					t.Fatalf("the record does not say what the store refused (%q missing):\n%s", want, state.Failure)
				}
			}
		})
	}
}

func TestPipelineReturnsAFailingCheckToTheSameDeveloperUntilItPasses(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		// The first attempt leaves the check failing; the repair attempt makes it
		// pass in the same worktree.
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	command := `echo running the suite; test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{command})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("repaired work was not integrated: %#v, closed = %t, blocked = %t", outcome.Integration, tracker.Closed, tracker.Blocked)
	}
	// The failing check spent one attempt from the same budget review repairs
	// draw on.
	if outcome.RepairAttempts != 1 || outcome.ReviewDecision != review.DecisionApprove {
		t.Fatalf("Run() outcome = %#v", outcome)
	}

	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want 2", len(developerRequests))
	}
	repair := developerRequests[1]
	if repair.SessionID != provider.DeveloperSession {
		t.Fatalf("repair attempt session = %q, want %q", repair.SessionID, provider.DeveloperSession)
	}
	if repair.WorkingDirectory != developerRequests[0].WorkingDirectory || repair.WorkingDirectory != outcome.WorktreePath {
		t.Fatalf("repair attempt ran in %q, want %q", repair.WorkingDirectory, outcome.WorktreePath)
	}
	// The developer is handed the command, its exit code, and what it printed on
	// both streams, which is what makes a check better repair input than a
	// finding: it names the exact failure and can be re-run.
	for _, want := range []string{
		"repair attempt 1 of 2",
		"Command: " + command,
		"Exit code: 3",
		"running the suite",
		"feature.txt is missing",
	} {
		if !strings.Contains(repair.Prompt, want) {
			t.Fatalf("check repair prompt is missing %q:\n%s", want, repair.Prompt)
		}
	}
	// The reviewer only ever saw the change whose checks passed.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want the one attempt that passed its checks", reviews)
	}
	if integrated := testharness.GitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the repaired content", integrated)
	}
	// An integrated run carries no outstanding check failure: the change that
	// was approved is the one whose checks passed.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.CheckFailure != nil || state.RepairAttempts != 1 {
		t.Fatalf("integrated run kept the repaired check failure: %#v", state)
	}
}

func TestPipelineBlocksTheItemWhenAFailingCheckSpendsTheRepairBudget(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	command := `echo "the suite is still red" >&2; exit 3`
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{command})
	before := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopRepairBudget)
	if outcome.StopClass != runstate.StopRepairBudget {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.StopRepairBudget)
	}
	wantFailure := "verification failed after 2 of 2 permitted attempt(s)"
	if err == nil || !strings.Contains(err.Error(), wantFailure) {
		t.Fatalf("Run() error = %v, want %q", err, wantFailure)
	}
	// One budget covers both repair kinds, so the check spends exactly the
	// attempts a reviewer's findings would have.
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 3 {
		t.Fatalf("developer invocations = %d, want the first attempt and both repairs", runs)
	}
	// The reviewer would have approved every one of those attempts. It never
	// gets the chance, because review is unreachable while a check fails.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 0 {
		t.Fatalf("reviews = %d, want none while a check fails", reviews)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a failing check reached integration: %#v, closed = %t", outcome.Integration, tracker.Closed)
	}
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
		t.Fatalf("main moved with a failing check: %q, want %q", head, before)
	}

	// What the developer could not fix is recorded where the work is tracked.
	if !tracker.Blocked || !outcome.Blocked {
		t.Fatalf("spent repair budget did not block the item: tracker = %t, outcome = %t", tracker.Blocked, outcome.Blocked)
	}
	for _, want := range []string{
		"Repair attempts: 2 of 2 permitted",
		"Failing check: " + command + " (exit 3)",
		"the suite is still red",
		outcome.WorktreePath,
		outcome.Branch,
	} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Fatalf("blocker is missing %q:\n%s", want, tracker.BlockReason)
		}
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("blocked worktree was not preserved: %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusFailed || state.RepairAttempts != 2 || state.CheckFailure == nil {
		t.Fatalf("state = %#v", state)
	}
	if state.CheckFailure.Command != command || state.CheckFailure.ExitCode != 3 {
		t.Fatalf("durable check failure = %#v", state.CheckFailure)
	}
}

func TestPipelineResumesTheRepairLoopAtTheRecordedAttempt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		// allowSaves chooses the step the first process dies on: none lets the
		// second attempt be refused as it is recorded, one lets it be recorded
		// and then loses the developer that was about to run.
		allowSaves int
		// wantRecorded is the attempt count that survives on disk, wantPhase
		// the phase it survives in, and wantResumedDeveloperRuns how many
		// developer invocations the resumed run is entitled to make.
		wantRecorded             int
		wantPhase                runstate.Phase
		verdict                  string
		wantResumedDeveloperRuns int
	}{
		{
			name: "resumed attempt is approved", allowSaves: 0,
			wantRecorded: 1, wantPhase: runstate.PhaseReviewing,
			verdict: testharness.ApproveVerdict, wantResumedDeveloperRuns: 0,
		},
		{
			name: "resumed attempt exhausts the budget it inherited", allowSaves: 0,
			wantRecorded: 1, wantPhase: runstate.PhaseReviewing,
			verdict: testharness.RepairVerdict, wantResumedDeveloperRuns: 1,
		},
		// Dying once the attempt is recorded leaves the developer never
		// invoked, so the resumed run has to rebuild the repair prompt from the
		// durable findings and issue it to the recorded session.
		{
			name: "resumed attempt was recorded but never issued", allowSaves: 1,
			wantRecorded: 2, wantPhase: runstate.PhaseDeveloping,
			verdict: testharness.ApproveVerdict, wantResumedDeveloperRuns: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store := testharness.RestartableFixture(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			write := func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}

			// The first process is interrupted on its second repair attempt:
			// nothing after that point reaches durable state.
			interrupted := &testharness.InterruptedStore{StateStore: store, AtAttempt: 2, AllowSaves: test.allowSaves}
			first := orchestratortest.RoleBackend(write, testharness.RepairVerdict)
			firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{"exit 0"}), first)
			firstOutcome, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
			if err == nil || !interrupted.Stopped {
				t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.Stopped)
			}
			interruptedState, err := store.Load(firstOutcome.RunID)
			if err != nil {
				t.Fatalf("Load() interrupted state error = %v", err)
			}
			if interruptedState.Status.Terminal() || interruptedState.RepairAttempts != test.wantRecorded || interruptedState.Phase != test.wantPhase {
				t.Fatalf("interrupted state = %#v, want %d attempt(s) in phase %q", interruptedState, test.wantRecorded, test.wantPhase)
			}

			// A second process over the same durable state picks the run up
			// rather than starting a second developer on the same item.
			second := orchestratortest.RoleBackend(write, test.verdict)
			resumed := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
			outcome, err := resumed.Run(context.Background(), tracker.Item.ID)
			if outcome.RunID != firstOutcome.RunID || outcome.WorktreePath != firstOutcome.WorktreePath || outcome.Branch != firstOutcome.Branch {
				t.Fatalf("resumed run = %#v, want the interrupted run %s in %s", outcome, firstOutcome.RunID, firstOutcome.WorktreePath)
			}
			if claims := testharness.CountCalls(tracker.Calls, "claim"); claims != 1 {
				t.Fatalf("claims = %d, want the item claimed once", claims)
			}

			// The inherited attempt count is what bounds the resumed run: the
			// remainder of the original budget, never a fresh one.
			developerRequests := second.RequestsForRole(domain.RoleDeveloper)
			if len(developerRequests) != test.wantResumedDeveloperRuns {
				t.Fatalf("resumed developer invocations = %d, want %d", len(developerRequests), test.wantResumedDeveloperRuns)
			}
			for _, reissued := range developerRequests {
				// Whatever attempt the resumed run makes, it continues the
				// recorded session with the findings from durable state rather
				// than starting the change over.
				if reissued.SessionID != second.DeveloperSession {
					t.Fatalf("resumed attempt session = %q, want %q", reissued.SessionID, second.DeveloperSession)
				}
				if !strings.Contains(reissued.Prompt, `"message": "add the missing file"`) {
					t.Fatalf("resumed repair prompt lost the durable findings:\n%s", reissued.Prompt)
				}
			}

			if test.verdict == testharness.ApproveVerdict {
				if err != nil {
					t.Fatalf("resumed Run() error = %v", err)
				}
				if outcome.Integration == nil || !tracker.Closed || outcome.RepairAttempts != test.wantRecorded {
					t.Fatalf("resumed run did not integrate at the recorded attempt: %#v", outcome)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), "after 2 of 2 permitted attempt(s)") {
				t.Fatalf("resumed Run() error = %v", err)
			}
			if !tracker.Blocked || outcome.RepairAttempts != 2 {
				t.Fatalf("resumed run did not block at the inherited limit: blocked = %t, outcome = %#v", tracker.Blocked, outcome)
			}
		})
	}
}

func TestPipelineResumesACheckRepairFromTheRecordedFailure(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	command := `test -f fixed.txt || { echo "fixed.txt is missing" >&2; exit 3; }`

	// The first process never makes the check pass, and it is interrupted once
	// its second attempt is already recorded. What survives is an attempt
	// counted against the budget together with the check that triggered it.
	interrupted := &testharness.InterruptedStore{StateStore: store, AtAttempt: 2, AllowSaves: 1}
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{command}), first)
	firstOutcome, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !interrupted.Stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.Stopped)
	}
	interruptedState, err := store.Load(firstOutcome.RunID)
	if err != nil {
		t.Fatalf("Load() interrupted state error = %v", err)
	}
	if interruptedState.Status.Terminal() || interruptedState.RepairAttempts != 2 || interruptedState.Phase != runstate.PhaseDeveloping {
		t.Fatalf("interrupted state = %#v, want 2 attempts in the developing phase", interruptedState)
	}
	if interruptedState.CheckFailure == nil || interruptedState.CheckFailure.ExitCode != 3 {
		t.Fatalf("interrupted state lost the failing check: %#v", interruptedState.CheckFailure)
	}
	// A run inside a check repair carries no findings to compete with the check
	// for the resumed attempt.
	if len(interruptedState.ReviewFindingDetails) != 0 {
		t.Fatalf("interrupted state kept review findings beside a failing check: %#v", interruptedState)
	}

	// The second process rebuilds the interrupted attempt from durable state and
	// makes the check pass.
	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "fixed.txt"), []byte("fixed\n"), 0o600)
	}, testharness.ApproveVerdict)
	resumed := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{command}), second)
	outcome, err := resumed.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != firstOutcome.RunID || outcome.WorktreePath != firstOutcome.WorktreePath {
		t.Fatalf("resumed run = %#v, want the interrupted run %s in %s", outcome, firstOutcome.RunID, firstOutcome.WorktreePath)
	}
	// The recorded attempt is inherited rather than re-counted, so the restart
	// buys the run no additional budget.
	if outcome.RepairAttempts != 2 || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("resumed run did not finish at the recorded attempt: %#v", outcome)
	}
	developerRequests := second.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 {
		t.Fatalf("resumed developer invocations = %d, want the one recorded attempt reissued", len(developerRequests))
	}
	reissued := developerRequests[0]
	if reissued.SessionID != second.DeveloperSession {
		t.Fatalf("resumed attempt session = %q, want %q", reissued.SessionID, second.DeveloperSession)
	}
	// The prompt is rebuilt from the durable failure, not from a check this
	// process re-ran to discover.
	for _, want := range []string{"repair attempt 2 of 2", "Command: " + command, "Exit code: 3", "fixed.txt is missing"} {
		if !strings.Contains(reissued.Prompt, want) {
			t.Fatalf("resumed check repair prompt is missing %q:\n%s", want, reissued.Prompt)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.CheckFailure != nil {
		t.Fatalf("integrated run kept the repaired check failure: %#v", state.CheckFailure)
	}
}

func TestPipelineRefusesToResumeARunThatIsNotInsideItsRepairLoop(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, testharness.ApproveVerdict)
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	// A first developer attempt that was interrupted has no repair attempt to
	// resume and no findings to hand anyone. Reconciling it is not this
	// pipeline's job, so it is refused rather than re-run.
	now := time.Now().UTC()
	if err := store.Create(runstate.State{
		SchemaVersion:     runstate.StateSchemaVersion,
		RunID:             testharness.PipelineRunID,
		ProductID:         "yoyodyne",
		RepositoryID:      "yoyodyne",
		WorkItemID:        tracker.Item.ID,
		Backend:           domain.BackendClaudeCode,
		Status:            runstate.StatusRunning,
		Phase:             runstate.PhaseDeveloping,
		StartedAt:         now,
		UpdatedAt:         now,
		WorktreePath:      filepath.Join(worktreeRoot, "yoyodyne-task"),
		Branch:            "yoyodyne/yoyodyne-task/0123456789ab",
		BaseCommit:        strings.Repeat("a", 40),
		TargetBranch:      "main",
		ProviderSessionID: "developer-session",
	}); err != nil {
		t.Fatalf("Create() interrupted state error = %v", err)
	}

	_, err := pipeline.Run(context.Background(), tracker.Item.ID)
	var existing orchestrator.ExistingRunError
	if !errors.As(err, &existing) {
		t.Fatalf("Run() error = %T %v, want ExistingRunError", err, err)
	}
	if tracker.Claimed || len(provider.Requests) != 0 {
		t.Fatalf("refused run acted on the item: claimed = %t, provider requests = %d", tracker.Claimed, len(provider.Requests))
	}
}

// A drifted target is never integrated onto from the base the change was
// approved on: the promotion is refused and the change replayed onto where the
// target went. That holds at an integration budget of zero, because the budget
// bounds replays that stop on the change and a lost race is not one — so the
// replayed change, checked and approved again, lands on top of the work that
// moved the target, and the drift is reported on the item as it happens.
func TestPipelineNeverIntegratesOntoADriftedTargetEvenAtBudgetZero(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		// Someone else advances the integration target while the run is working.
		if err := os.WriteFile(filepath.Join(repository, "concurrent.txt"), []byte("elsewhere\n"), 0o600); err != nil {
			return err
		}
		testharness.RunPipelineGit(t, repository, "add", "concurrent.txt")
		testharness.RunPipelineGit(t, repository, "commit", "-m", "concurrent work")
		return nil
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 0

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	moved := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main^")
	if err != nil {
		t.Fatalf("Run() error = %v, want the replayed change to land", err)
	}
	if outcome.Integration == nil || !tracker.Closed || outcome.Blocked {
		t.Fatalf("Run() outcome = %#v, closed = %t", outcome, tracker.Closed)
	}
	if outcome.Integration.PreviousTargetCommit != moved || outcome.BaseCommit != moved {
		t.Fatalf("promotion base = %q / %q, want the drifted target %q rather than the base the change was approved on",
			outcome.Integration.PreviousTargetCommit, outcome.BaseCommit, moved)
	}
	for _, name := range []string{"feature.txt", "concurrent.txt"} {
		if _, err := os.Stat(filepath.Join(repository, name)); err != nil {
			t.Fatalf("main is missing %s: %v", name, err)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != 1 || state.ChargedReplays != 0 || state.Blocker != "" {
		t.Fatalf("state: races %d, charged %d, blocker %q; want one race recorded and nothing charged", state.IntegrationRetries, state.ChargedReplays, state.Blocker)
	}
}

func TestPipelineReportsOutstandingCleanupWithoutRecastingASucceededRun(t *testing.T) {
	t.Parallel()

	t.Run("cleanup itself fails", func(t *testing.T) {
		t.Parallel()
		repository, tracker, _, pipeline, store := testharness.AutomaticFixture(t)
		pipeline.Worktrees = &testharness.HookedWorktrees{
			WorktreeManager: pipeline.Worktrees,
			BeforeCleanup:   func() error { return errors.New("worktree is busy") },
		}

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		// The change is integrated and the item is closed: that is a succeeded
		// run with a janitorial problem, not a failed run.
		if outcome.Status != runstate.StatusSucceeded || !outcome.WorkItemClosed || outcome.WorktreeRemoved {
			t.Fatalf("outcome = %#v", outcome)
		}
		if !strings.Contains(outcome.CleanupFailure, "worktree is busy") {
			t.Fatalf("cleanup failure = %q", outcome.CleanupFailure)
		}
		if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
			t.Fatalf("worktree was removed despite the cleanup failure: %v", err)
		}
		if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != outcome.Integration.TargetCommit {
			t.Fatalf("main = %q, want the integrated commit %q", head, outcome.Integration.TargetCommit)
		}
		if !strings.Contains(tracker.Notes, "post-completion cleanup did not finish") || !strings.Contains(tracker.Notes, "Remaining worktree: "+outcome.WorktreePath) {
			t.Fatalf("tracker was not told about the outstanding cleanup: %q", tracker.Notes)
		}
		state, err := store.Load(outcome.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state.Status != runstate.StatusSucceeded || state.Phase != runstate.PhaseCleaningUp || state.WorktreeRemoved || state.CleanupFailure == "" {
			t.Fatalf("state = %#v", state)
		}
	})

	t.Run("final completion save fails once then recovers", func(t *testing.T) {
		t.Parallel()
		_, tracker, _, pipeline, store := testharness.AutomaticFixture(t)
		pipeline.Store = &testharness.InterruptingStore{StateStore: pipeline.Store, FailPhase: runstate.PhaseComplete}

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		// An interrupted write that recovers is not a cleanup problem: the run
		// is complete, both artifacts are gone, and nothing warns about either.
		if outcome.Status != runstate.StatusSucceeded || outcome.Phase != runstate.PhaseComplete {
			t.Fatalf("outcome = %#v", outcome)
		}
		if !outcome.WorktreeRemoved || !outcome.BranchRemoved {
			t.Fatalf("outcome artifacts = %#v", outcome)
		}
		if outcome.CleanupFailure != "" || outcome.CompletionRecordingFailure != "" {
			t.Fatalf("recovered save reported a problem: cleanup = %q, completion = %q", outcome.CleanupFailure, outcome.CompletionRecordingFailure)
		}
		if _, err := os.Stat(outcome.WorktreePath); !os.IsNotExist(err) {
			t.Fatalf("worktree survived a successful cleanup: %v", err)
		}
		for _, reject := range []string{"Remaining worktree:", "Remaining branch:", "cleanup did not finish", "recording final completion failed"} {
			if strings.Contains(tracker.Notes, reject) {
				t.Fatalf("notes claim %q after a recovered save: %q", reject, tracker.Notes)
			}
		}
		// The retry leaves a clean terminal record.
		state, err := store.Load(outcome.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state.Status != runstate.StatusSucceeded || state.Phase != runstate.PhaseComplete {
			t.Fatalf("state = %#v", state)
		}
		if !state.WorktreeRemoved || !state.BranchRemoved || state.CleanupFailure != "" {
			t.Fatalf("state artifacts = %#v", state)
		}
	})

	t.Run("persistence is down for every completion save", func(t *testing.T) {
		t.Parallel()
		_, tracker, _, pipeline, store := testharness.AutomaticFixture(t)
		pipeline.Store = &testharness.InterruptingStore{StateStore: pipeline.Store, FailPhase: runstate.PhaseComplete, FailAlways: true}

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		// This is the crash-equivalent boundary: nothing after the pre-cleanup
		// write survives, and what survived is still a terminal, closed run with
		// an outstanding-cleanup marker rather than a lost one.
		if outcome.Status != runstate.StatusSucceeded || !outcome.WorktreeRemoved || !outcome.BranchRemoved || !tracker.Closed {
			t.Fatalf("outcome = %#v, closed = %t", outcome, tracker.Closed)
		}
		// Cleanup finished; only writing it down did not. That is a
		// completion-recording problem, never an incomplete cleanup.
		if outcome.Phase != runstate.PhaseComplete || outcome.CleanupFailure != "" {
			t.Fatalf("outcome misclassified a recording failure: %#v", outcome)
		}
		if !strings.Contains(outcome.CompletionRecordingFailure, "save completed run state after cleanup") {
			t.Fatalf("completion recording failure = %q", outcome.CompletionRecordingFailure)
		}
		if !strings.Contains(tracker.Notes, "recording final completion failed") ||
			!strings.Contains(tracker.Notes, "Worktree removed: true") ||
			!strings.Contains(tracker.Notes, "Branch removed: true") {
			t.Fatalf("notes do not report a finished cleanup: %q", tracker.Notes)
		}
		for _, reject := range []string{"Remaining worktree:", "Remaining branch:", "cleanup did not finish"} {
			if strings.Contains(tracker.Notes, reject) {
				t.Fatalf("notes claim %q after a complete cleanup: %q", reject, tracker.Notes)
			}
		}
		state, err := store.Load(outcome.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state.Status != runstate.StatusSucceeded || state.CompletedAt == nil || state.Integration == nil {
			t.Fatalf("durable completion was lost: %#v", state)
		}
		if state.Phase != runstate.PhaseCleaningUp || state.WorktreeRemoved || state.BranchRemoved {
			t.Fatalf("state = %#v, want the pre-cleanup marker", state)
		}
		// The ambiguity the marker leaves is resolvable by observation, and a
		// resumed cleanup on already-absent artifacts is a safe no-op.
		if _, err := os.Stat(outcome.WorktreePath); !os.IsNotExist(err) {
			t.Fatalf("worktree survived cleanup: %v", err)
		}
	})
}

func TestPipelineKeepsCompletionOrderingWhenPersistenceOrTheTrackerFails(t *testing.T) {
	t.Parallel()

	t.Run("interrupted persistence after integration", func(t *testing.T) {
		t.Parallel()
		repository := testharness.PipelineRepository(t)
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
		provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
		}, testharness.ApproveVerdict)
		pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
		pipeline.Store = &testharness.InterruptingStore{StateStore: pipeline.Store, FailPhase: runstate.PhaseCompleting}

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err == nil || !strings.Contains(err.Error(), "save integrated run state") {
			t.Fatalf("Run() error = %v", err)
		}
		// The change is integrated, so the evidence must survive even though the
		// item was never closed and the worktree was never removed.
		if outcome.Integration == nil || tracker.Closed {
			t.Fatalf("completion ran ahead of durable state: %#v, closed = %t", outcome.Integration, tracker.Closed)
		}
		if got := strings.Join(tracker.Calls, ","); got != "claim,record" {
			t.Fatalf("tracker calls = %q", got)
		}
		if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != outcome.Integration.TargetCommit {
			t.Fatalf("main = %q, want the integrated commit %q", head, outcome.Integration.TargetCommit)
		}
		if _, err := os.Stat(outcome.WorktreePath); err != nil {
			t.Fatalf("integrated worktree was removed despite the failure: %v", err)
		}
		state, err := store.Load(outcome.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state.Status != runstate.StatusFailed || state.Integration == nil || state.Integration.TargetCommit != outcome.Integration.TargetCommit {
			t.Fatalf("state = %#v", state)
		}
	})

	t.Run("tracker cannot close the integrated item", func(t *testing.T) {
		t.Parallel()
		repository := testharness.PipelineRepository(t)
		tracker := &orchestratortest.Tracker{
			Item:        beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"},
			CompleteErr: errors.New("bd close is unavailable"),
		}
		provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
		}, testharness.ApproveVerdict)
		pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err == nil || !strings.Contains(err.Error(), "close integrated work item") {
			t.Fatalf("Run() error = %v", err)
		}
		if outcome.WorkItemClosed || tracker.Closed {
			t.Fatalf("run claimed completion the tracker refused: %#v", outcome)
		}
		// Cleanup is reachable only through a closed item, so the proof of the
		// integrated work stays on disk for reconciliation.
		if _, err := os.Stat(outcome.WorktreePath); err != nil {
			t.Fatalf("worktree was removed without a closed item: %v", err)
		}
		if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != outcome.Integration.TargetCommit {
			t.Fatalf("main = %q, want the integrated commit %q", head, outcome.Integration.TargetCommit)
		}
		if !strings.Contains(tracker.Notes, "failed after the change was already integrated") {
			t.Fatalf("failure notes = %q", tracker.Notes)
		}
		state, err := store.Load(outcome.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state.Status != runstate.StatusFailed || state.Phase != runstate.PhaseCompleting || state.Integration == nil {
			t.Fatalf("state = %#v", state)
		}
	})
}

// refusingStore refuses every state one chosen field appears on, which is the
// shape a schema refusal has: one field the durable schema will not take,
// discovered at the moment the run tries to record what it decided. It refuses
// the way the real store does — with the classified refusal, not a store that
// happened to be unavailable — because the two are acted on differently. What
// stays on disk is whichever earlier state validated, and for a run being
// reviewed that is the snapshot taken with the evidence deliberately cleared.
//
// It is keyed on a field rather than on a status or a phase because that is what
// a schema refusal is keyed on, and the difference decides the case: a field
// follows the run into every later write, including the write that ends it, so
// the refusal is still there when the run tries to record its own ending.
type refusingStore struct {
	orchestrator.StateStore
	refuse   func(runstate.State) bool
	problem  string
	refusals int
}

func (s *refusingStore) Save(state runstate.State) error {
	if s.refuse(state) {
		s.refusals++
		return runstate.RefusedStateError{Problem: errors.New(s.problem)}
	}
	return s.StateStore.Save(state)
}

func countEvents(events []execution.Event, eventType execution.EventType) int {
	matching := 0
	for _, event := range events {
		if event.Type == eventType {
			matching++
		}
	}
	return matching
}

func hasEvent(events []execution.Event, eventType execution.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
