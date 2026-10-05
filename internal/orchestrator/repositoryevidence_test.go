package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

func TestReviewerGetsUnchangedExtensionlessSourcesAndBinaryPresenceAtHead(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	if err := os.MkdirAll(filepath.Join(repository, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"docs/guide.md":  "# Guide\n\nliteral literal literal\n",
		"docs/count.txt": "unchanged unchanged unchanged unchanged\n",
		"docs/icon.bin":  "\x00already in the repository\n",
		"Makefile":       "check:\n\t./scripts/check\n",
		"Dockerfile":     "FROM scratch\n",
		"LICENSE":        "Permission to use this fixture.\n",
		"scripts/check":  "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(repository, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "unchanged sources and binary fixture")
	base := gitLine(t, repository, "rev-parse", "HEAD")
	tracker := newOutcomeTracker()
	tracker.Item.Description = "Edit docs/guide.md while preserving docs/icon.bin."
	tracker.Item.AcceptanceCriteria = "docs/guide.md contains literal four times; docs/count.txt contains unchanged four times. Compare unchanged Makefile, Dockerfile, LICENSE and ./scripts//check."
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "docs", "guide.md"), []byte("# Guide\n\nliteral literal literal literal\n"), 0o600)
	}, approveVerdict)
	respond := provider.Respond
	var prompt string
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			prompt = request.Prompt
		}
		return respond(request)
	}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.RepairAttempts != 0 || outcome.ReviewDecision != review.DecisionApprove {
		t.Fatalf("Run() = %#v, %v", outcome, err)
	}
	for _, want := range []string{
		"## Referenced file: docs/guide.md (at base commit " + base + ")",
		"\"docs/icon.bin\"",
		"Whole file, 41 bytes.",
		"literal literal literal literal\n",
		"unchanged unchanged unchanged unchanged\n",
		"File at reviewed commit " + outcome.ReviewHeadCommit + ": docs/count.txt",
		"File at reviewed commit " + outcome.ReviewHeadCommit + ": Makefile",
		"check:\n\t./scripts/check\n",
		"File at reviewed commit " + outcome.ReviewHeadCommit + ": Dockerfile",
		"FROM scratch\n",
		"File at reviewed commit " + outcome.ReviewHeadCommit + ": LICENSE",
		"Permission to use this fixture.\n",
		"File at reviewed commit " + outcome.ReviewHeadCommit + ": scripts/check",
		"#!/bin/sh\nexit 0\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("reviewer evidence omitted %q:\n%s", want, prompt)
		}
	}
}

func TestReviewerGetsTheBlockersOwnReasonAndTrackerState(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := newOutcomeTracker()
	tracker.Item.Dependencies = []beads.Dependency{{ID: "yoyodyne-design", Type: "blocks", Status: "blocked"}}
	tracker.HoldsItem(beads.WorkItem{ID: "yoyodyne-design", Title: "Decide the conversion", Status: "blocked", Description: "The architect has not decided the conversion; implementing it would invent a design."})
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, orchestratortest.RoleBackend(writeFeature, approveVerdict), []string{"exit 0"})
	run := activeRun{pipeline: pipeline, item: tracker.Item, worktree: gitworktree.Worktree{Path: repository}}
	text, err := run.reviewedContext(context.Background(), gitLine(t, repository, "rev-parse", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Blocker states: yoyodyne-design: blocked", "Decide the conversion (yoyodyne-design)", "The architect has not decided the conversion"} {
		if !strings.Contains(text, want) {
			t.Fatalf("dependency evidence omitted %q: %s", want, text)
		}
	}
}

func TestCitedHeadContentThatOutgrowsTheBudgetIsExplicit(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	if err := os.WriteFile(filepath.Join(repository, "Makefile"), []byte(strings.Repeat("x", maxRepositoryContentBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "an oversized cited source")
	pipeline, _ := newAutomaticPipeline(t, repository, newOutcomeTracker(), orchestratortest.RoleBackend(writeFeature, approveVerdict), []string{"exit 0"})
	evidence := reviewedRepository(context.Background(), pipeline.Worktrees, gitLine(t, repository, "rev-parse", "HEAD"), beads.WorkItem{AcceptanceCriteria: "Compare against Makefile."}, gitworktree.ChangeDiff{})
	if len(evidence.Contents) != 1 || evidence.Contents[0].Path != "Makefile" || evidence.Contents[0].Content != "" || !strings.Contains(evidence.Contents[0].Unavailable, "content budget") {
		t.Fatalf("oversized cited content was not stated as unavailable: %#v", evidence)
	}
}

func TestAbsenceClaimSurvivesDurableFindingConversion(t *testing.T) {
	t.Parallel()
	findings := []review.Finding{{Severity: review.SeverityMajor, Message: "missing source", Absent: "docs/source.md"}}
	restored := reportedFindings(durableFindings(findings))
	if len(restored) != 1 || restored[0].Absent != findings[0].Absent {
		t.Fatalf("absence claim lost across durable storage: %#v", restored)
	}
}
