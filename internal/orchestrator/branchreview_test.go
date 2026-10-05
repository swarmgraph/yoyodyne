package orchestrator

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
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const branchReviewID = "review-abcdef0123456789abcdef0123456789"

// accumulatedRepository builds the shape a branch review exists for: several
// commits, each of which is a complete and consistent change on its own, whose
// combination is the only place a defect between them could be seen.
func accumulatedRepository(t *testing.T) string {
	t.Helper()
	repository := pipelineRepository(t)
	runPipelineGit(t, repository, "checkout", "-b", "milestone")
	for _, commit := range []struct{ file, content, message string }{
		{"store.go", "package harness\n\nfunc write(key string) {}\n", "record the durable evidence"},
		{"reader.go", "package harness\n\nfunc read(other string) {}\n", "read the durable evidence back"},
		{"docs/design.md", "design content\nand what the two of them do\n", "describe both halves"},
	} {
		if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(commit.file)), []byte(commit.content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		runPipelineGit(t, repository, "add", commit.file)
		runPipelineGit(t, repository, "commit", "-m", commit.message)
	}
	runPipelineGit(t, repository, "checkout", "main")
	return repository
}

func newBranchReviewer(t *testing.T, repository string, provider backend.Backend) (BranchReviewer, *runstate.BranchReviewStore, *runstate.ReportStore) {
	t.Helper()
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	stateRoot := t.TempDir()
	reviews, err := runstate.NewBranchReviewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewBranchReviewStore() error = %v", err)
	}
	reports, err := runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewReportStore() error = %v", err)
	}
	cfg := config.Config{
		Version: config.CurrentVersion,
		Product: config.Product{
			ID: "yoyodyne", RepositoryID: "yoyodyne", Repository: repository,
			Specifications: config.DefaultSpecifications,
			Invariants:     config.DefaultInvariants,
			Designs:        config.DefaultDesigns,
			Decisions:      config.DefaultDecisions,
		},
		Agents: map[string]config.AgentConfig{
			"reviewer": {Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: testReviewerModel, Instances: 1},
		},
	}
	return BranchReviewer{
		Worktrees: worktrees,
		// The real reviewer over a fake provider: the contract, the verdict
		// decoding, and the independence evidence are all exercised, and only the
		// provider itself is a double.
		Reviewer: review.Reviewer{
			Backend: provider,
			Model:   testReviewerModel,
			Clock:   fixedBranchClock{},
		},
		Reviews:     reviews,
		Reports:     reports,
		Clock:       fixedBranchClock{},
		NewReviewID: func() (string, error) { return branchReviewID, nil },
		Repository:  repository,
		Config:      cfg,
	}, reviews, reports
}

// branchProvider answers one reviewer invocation with a fixed reply.
func branchProvider(reply string) *orchestratortest.Backend {
	return &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			Backend:       domain.BackendClaudeCode,
			SessionID:     "branch-review-session",
			ResolvedModel: "claude-opus-5",
			FinalText:     reply,
			Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
		}, nil
	}}
}

func TestBranchReviewSuppliesStandingGoalsAtItsBase(t *testing.T) {
	for _, checkout := range []string{"edited", "deleted", "renamed", "home removed"} {
		t.Run(checkout, func(t *testing.T) {
			repository := pipelineRepository(t)
			goalsPath := filepath.Join(repository, "docs", "product", "goals.md")
			if err := os.MkdirAll(filepath.Dir(goalsPath), 0o700); err != nil {
				t.Fatal(err)
			}
			const goals = "# Goals\n\n## Goals\n\n- Use plain language.\n- Run autonomously.\n\n## Standing goals\n\nBoth goals apply to every change.\n"
			if err := os.WriteFile(goalsPath, []byte(goals), 0o600); err != nil {
				t.Fatal(err)
			}
			runPipelineGit(t, repository, "add", ".")
			runPipelineGit(t, repository, "commit", "-m", "record standing goals")
			runPipelineGit(t, repository, "checkout", "-b", "milestone")
			if err := os.WriteFile(filepath.Join(repository, "status.go"), []byte("package harness\n\nconst message = \"The provider's posture changed.\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runPipelineGit(t, repository, "add", ".")
			runPipelineGit(t, repository, "commit", "-m", "add status message")
			// Neither changed contents nor a removed path in the checkout may
			// replace the standing set recorded at the branch's base.
			var changeErr error
			switch checkout {
			case "edited":
				changeErr = os.WriteFile(goalsPath, []byte("# A later set\n"), 0o600)
			case "deleted":
				changeErr = os.Remove(goalsPath)
			case "renamed":
				changeErr = os.Rename(goalsPath, filepath.Join(filepath.Dir(goalsPath), "renamed.md"))
			case "home removed":
				changeErr = os.RemoveAll(filepath.Dir(goalsPath))
			}
			if changeErr != nil {
				t.Fatal(changeErr)
			}
			provider := branchProvider(`{"decision":"repair","summary":"Checked the plain-language and autonomy standing goals; the status message breaks plain language.","findings":[{"severity":"major","message":"The standing plain-language goal forbids the retired term posture; write tool access.","location":{"file":"status.go","line":3}}]}`)
			reviewer, records, _ := newBranchReviewer(t, repository, provider)
			outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Approved() || outcome.Decision != review.DecisionRepair {
				t.Fatalf("review = %#v", outcome)
			}
			for _, want := range []string{goals, "Authoritative product intent: docs/product/goals.md", "base commit " + outcome.BaseCommit, "The provider's posture changed."} {
				if !strings.Contains(provider.Requests[0].Prompt, want) {
					t.Errorf("review evidence is missing %q", want)
				}
			}
			if strings.Contains(provider.Requests[0].Prompt, "A later set") {
				t.Fatal("branch review used product intent from the later checkout")
			}
			itemReview := activeRun{
				pipeline: Pipeline{Repository: repository, Worktrees: reviewer.Worktrees.(WorktreeManager), Config: reviewer.Config},
				item:     beads.WorkItem{ID: "yoyodyne-task", Title: "Record attribution", Status: "in_progress"},
			}
			itemContext, err := itemReview.reviewedContext(context.Background(), outcome.BaseCommit)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(itemContext, goals) || !strings.Contains(itemContext, "Authoritative product intent: docs/product/goals.md") || strings.Contains(itemContext, "A later set") {
				t.Fatalf("per-item review did not carry the standing goals at its base:\n%s", itemContext)
			}
			saved, err := records.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(saved) != 1 || saved[0].Summary != outcome.Summary || !strings.Contains(saved[0].Summary, "plain-language and autonomy") {
				t.Fatalf("recorded summary = %#v", saved)
			}
		})
	}
}

type fixedBranchClock struct{}

type intentListingReader struct {
	BranchChangeReader
	err     error
	omitted int
}

func (r intentListingReader) FilesAtCommit(ctx context.Context, commit string, maxFiles, maxBytes int) (gitworktree.CommitListing, error) {
	if r.err != nil {
		return gitworktree.CommitListing{}, r.err
	}
	listing, err := r.BranchChangeReader.FilesAtCommit(ctx, commit, maxFiles, maxBytes)
	listing.Omitted = r.omitted
	return listing, err
}

func TestBranchReviewRefusesAnUnreadableOrPartialBaseIntentListing(t *testing.T) {
	for _, test := range []struct {
		name, want string
		err        error
		omitted    int
	}{
		{name: "failed", want: "base listing unavailable", err: errors.New("base listing unavailable")},
		{name: "bounded", want: "base repository listing omitted 3 path(s) (limits: 20000 paths, 131072 bytes)", omitted: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := accumulatedRepository(t)
			provider := branchProvider(`{"decision":"approve","summary":"fine"}`)
			reviewer, records, _ := newBranchReviewer(t, repository, provider)
			reviewer.Worktrees = intentListingReader{BranchChangeReader: reviewer.Worktrees, err: test.err, omitted: test.omitted}
			_, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
			if err == nil || !strings.Contains(err.Error(), "discover product documents at base commit") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("review error = %v", err)
			}
			if len(provider.Requests) != 0 {
				t.Fatal("reviewer was invoked without a complete base intent listing")
			}
			if saved, err := records.List(); err != nil || len(saved) != 0 {
				t.Fatalf("review records = %#v, %v", saved, err)
			}
		})
	}
}

func (fixedBranchClock) Now() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }

func TestBranchReviewJudgesEveryCommitTogetherAndRecordsTheVerdict(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"the three commits agree with one another"}` + "\n" +
		"```yoyodyne-report\n{\"reports\":[{\"severity\":\"note\",\"message\":\"the two halves are named differently\"}]}\n```")
	reviewer, reviews, reports := newBranchReviewer(t, repository, provider)

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !outcome.Approved() || outcome.Decision != review.DecisionApprove {
		t.Fatalf("Review() = %#v", outcome)
	}
	if outcome.Commits != 3 || outcome.CommitsOmitted != 0 || outcome.Truncated {
		t.Errorf("reviewed change = %#v", outcome)
	}
	// The same session and model evidence a per-item review carries.
	if outcome.SessionID != "branch-review-session" || outcome.Model != testReviewerModel || outcome.ResolvedModel != "claude-opus-5" {
		t.Errorf("independence evidence = %#v", outcome)
	}
	if outcome.BaseCommit == outcome.HeadCommit || outcome.BaseCommit != gitLine(t, repository, "rev-parse", "refs/heads/main") {
		t.Errorf("reviewed range = %#v", outcome)
	}

	// One provider invocation, made as the reviewer, with no tools and nothing
	// to write with — the independence a per-item review is held to.
	requests := provider.RequestsForRole(domain.RoleReviewer)
	if len(requests) != 1 {
		t.Fatalf("reviewer invocations = %d", len(requests))
	}
	request := requests[0]
	if len(request.AllowedTools) != 0 || request.SessionID != "" {
		t.Errorf("reviewer invocation = %#v", request)
	}
	// All three commits reached it as one change, described as a history.
	for _, want := range []string{
		"record the durable evidence",
		"read the durable evidence back",
		"describe both halves",
		"b/store.go",
		"b/reader.go",
		"accumulated 3 commit(s)",
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Errorf("accumulated evidence is missing %q", want)
		}
	}
	if !strings.Contains(request.SystemPrompt, "A finding may span commits") {
		t.Error("the reviewer was not told a finding may span commits")
	}

	recorded, err := reviews.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded reviews = %#v", recorded)
	}
	if !recorded[0].Approved() || recorded[0].Branch != "milestone" || recorded[0].Commits != 3 ||
		recorded[0].SessionID != "branch-review-session" || recorded[0].ResolvedModel != "claude-opus-5" {
		t.Errorf("recorded review = %#v", recorded[0])
	}
	// What the reviewer noticed beside its verdict is collected exactly as a
	// run's reports are.
	collected, err := reports.List()
	if err != nil || len(collected) != 1 || collected[0].Severity != report.SeverityNote {
		t.Fatalf("collected reports = %#v, %v", collected, err)
	}
	if collected[0].RunID != branchReviewID || collected[0].Role != domain.RoleReviewer {
		t.Errorf("report attribution = %#v", collected[0])
	}
}

func TestBranchRepairVerdictIsRecordedAndChangesNothingAlreadyIntegrated(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	head := gitLine(t, repository, "rev-parse", "refs/heads/milestone")
	provider := branchProvider(`{"decision":"repair","summary":"the two halves disagree about the key they use",` +
		`"findings":[{"severity":"major","message":"store.go writes under key and reader.go reads under other","location":{"file":"reader.go","line":3}}]}`)
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)

	// A repair verdict at this scope is a completed review, not a failed one:
	// the review worked and asked for repairs.
	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if outcome.Decision != review.DecisionRepair || len(outcome.Findings) != 1 {
		t.Fatalf("Review() = %#v", outcome)
	}
	// It is not an approval of the branch, and that is the one question anything
	// downstream asks.
	if outcome.Approved() {
		t.Fatal("a repair verdict read as an approval of the branch")
	}
	// And it decides nothing about the work already integrated: the branch is
	// where it was, and nothing was reverted, reopened, or unmerged.
	if now := gitLine(t, repository, "rev-parse", "refs/heads/milestone"); now != head {
		t.Errorf("branch moved from %s to %s on a repair verdict", head, now)
	}

	recorded, err := reviews.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, %v", recorded, err)
	}
	if recorded[0].Decision != runstate.ReviewRepair || len(recorded[0].Findings) != 1 ||
		recorded[0].Findings[0].File != "reader.go" || recorded[0].Findings[0].Severity != runstate.SeverityMajor {
		t.Errorf("recorded repair = %#v", recorded[0])
	}
}

// A branch review is a paid provider invocation, so it leaves the event stream
// every other one leaves: it can be followed while it runs, and the cost the
// provider reported survives in the only place that figure is ever written down.
func TestBranchReviewRecordsTheEventStreamOfItsInvocation(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	// The provider emits its own result event through the sink it was handed,
	// exactly as the real backend does, because that event is what carries cost.
	provider := &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
		event, err := execution.NewEvent(request.RunID, request.LastSequence+1, time.Now(), execution.EventRunCompleted, "claude-code", map[string]any{
			"total_cost_usd": 1.25,
		})
		if err != nil {
			return backend.RunResult{}, err
		}
		if err := request.EventSink(event); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{
			Backend:       domain.BackendClaudeCode,
			SessionID:     "branch-review-session",
			ResolvedModel: "claude-opus-5",
			FinalText:     `{"decision":"approve","summary":"the commits agree"}`,
			Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
		}, nil
	}}
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !outcome.Approved() {
		t.Fatalf("Review() = %#v", outcome)
	}
	events, err := reviews.LoadEvents(branchReviewID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	var kinds []string
	var priced bool
	for _, event := range events {
		kinds = append(kinds, string(event.Type))
		if strings.Contains(string(event.Payload), `"total_cost_usd":1.25`) {
			priced = true
		}
	}
	// The review brackets itself the way a run's does, so following one shows the
	// same shape, and the provider's own report sits between the two.
	for _, want := range []execution.EventType{execution.EventReviewStarted, execution.EventReviewCompleted} {
		if !slices.Contains(kinds, string(want)) {
			t.Errorf("the recorded stream %v is missing %s", kinds, want)
		}
	}
	if !priced {
		t.Errorf("what the invocation cost did not survive into the stream: %v", kinds)
	}
	// Sequence numbers are strictly increasing, which is what lets a follower
	// replay the stream in the order it happened.
	for index := 1; index < len(events); index++ {
		if events[index].Sequence <= events[index-1].Sequence {
			t.Fatalf("events %d and %d are out of order: %#v", index-1, index, events)
		}
	}
}

func TestBranchReviewRecordsAReviewThatNeverAnswered(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := &orchestratortest.Backend{Respond: func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New("claude is not installed")
	}}
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "independent branch review failed") {
		t.Fatalf("Review() error = %v", err)
	}
	if outcome.Approved() || outcome.Decision != "" {
		t.Fatalf("a failed review produced a decision: %#v", outcome)
	}
	// A branch nobody could review is not the same fact as a branch nobody
	// reviewed, so the attempt is durable either way.
	recorded, err := reviews.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, %v", recorded, err)
	}
	if recorded[0].Decision != "" || !strings.Contains(recorded[0].Failure, "claude is not installed") {
		t.Errorf("recorded failure = %#v", recorded[0])
	}
}

// A branch review is a provider invocation with no run to park, so an exhausted
// limit that stops one leaves nothing behind unless it is written down here.
// What it records is the refusal itself: what was waiting, and when the provider
// said it lifts.
func TestABranchReviewTheProviderRefusedRecordsTheExhaustedLimit(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	resetsAt := fixedBranchClock{}.Now().Add(3 * time.Hour)
	provider := &orchestratortest.Backend{Respond: func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt},
			Process:    execution.ProcessResult{Status: execution.ProcessSucceeded},
		}, nil
	}}
	reviewer, _, _ := newBranchReviewer(t, repository, provider)
	limits, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewUsageLimitStore() error = %v", err)
	}
	reviewer.UsageLimits = limits

	if _, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"}); err == nil {
		t.Fatal("Review() error = nil, want the refused review still failed")
	}
	recorded, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the refusal recorded once", recorded)
	}
	refusal := recorded[0]
	if !strings.Contains(refusal.Waiting, branchReviewID) || !strings.Contains(refusal.Waiting, "milestone") {
		t.Fatalf("waiting = %q, want the review and the branch that were stopped", refusal.Waiting)
	}
	if refusal.Kind != "five_hour" || refusal.ResetsAt == nil || !refusal.ResetsAt.Equal(resetsAt) {
		t.Fatalf("refusal = %#v, want the limit and when it lifts", refusal)
	}

	// A review that failed for anything else is not a refusal, and a review that
	// answered is not one either: neither is hours of silence anybody is waiting
	// through.
	unrefused, _, _ := newBranchReviewer(t, repository, branchProvider(`{"decision":"approve","summary":"looks consistent"}`))
	quiet, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewUsageLimitStore() error = %v", err)
	}
	unrefused.UsageLimits = quiet
	if _, err := unrefused.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if refusals, err := quiet.List(); err != nil || len(refusals) != 0 {
		t.Fatalf("List() = %#v, error %v, want an answered review recorded as no refusal", refusals, err)
	}
}

func TestBranchReviewCannotApproveAChangeItCouldNotSeeInFull(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"looks consistent"}`)
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)
	// A bound too small for the accumulated patch is the ordinary way a large
	// branch is described, and an approval of what was cut is refused.
	reviewer.Limits = gitworktree.DiffLimits{MaxTotalBytes: 120}

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "cannot approve an incomplete change representation") {
		t.Fatalf("Review() error = %v", err)
	}
	if outcome.Approved() {
		t.Fatal("a truncated accumulated change was approved")
	}
	if !outcome.Truncated {
		t.Errorf("a clamped change was not reported as truncated: %#v", outcome)
	}
	recorded, err := reviews.List()
	if err != nil || len(recorded) != 1 || !recorded[0].Truncated || recorded[0].Approved() {
		t.Fatalf("recorded review = %#v, %v", recorded, err)
	}
}

func TestBranchReviewRefusesABranchThatAccumulatedNothing(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"fine"}`)
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)

	if _, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "milestone"}); !errors.Is(err, gitworktree.ErrNoAccumulatedChange) {
		t.Fatalf("Review() of an empty range error = %v", err)
	}
	// Nothing was invoked and nothing was recorded: there was no change to judge.
	if len(provider.Requests) != 0 {
		t.Errorf("the provider was invoked %d times for an empty range", len(provider.Requests))
	}
	if recorded, err := reviews.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, %v", recorded, err)
	}
}

// A shadow review is the same review made to measure the reviewer. It runs and
// records exactly as any other does; what it must not do is leave an approval of
// the branch behind it, however it decided.
func TestAShadowBranchReviewIsRecordedAndApprovesNothing(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"the three commits agree with one another"}`)
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main", Shadow: true})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	// The verdict is what the reviewer said, and it decided; what it is not is
	// an approval of this branch, which is the only question anything downstream
	// asks of it.
	if outcome.Decision != review.DecisionApprove || !outcome.Decided() {
		t.Fatalf("Review() = %#v", outcome)
	}
	if outcome.Approved() {
		t.Fatal("a shadow review approved the branch")
	}
	if !outcome.Shadow {
		t.Errorf("the outcome does not say it was a shadow review: %#v", outcome)
	}

	recorded, err := reviews.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, %v", recorded, err)
	}
	// The durable record carries the same enforcement, because it outlives the
	// process that made it and is what a later reader actually asks.
	if !recorded[0].Shadow || recorded[0].Approved() || recorded[0].Decision != runstate.ReviewApprove {
		t.Errorf("recorded review = %#v", recorded[0])
	}
}
