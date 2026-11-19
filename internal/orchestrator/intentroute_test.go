package orchestrator

import (
	"context"
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestIntentRevisionIsNilWithoutACompanionIntentRepository(t *testing.T) {
	t.Parallel()

	revision, commit, err := Pipeline{}.intentRevision(context.Background(), "")
	if err != nil || revision != nil || commit != "" {
		t.Fatalf("intentRevision() = %v, %q, %v; want nil, empty, nil for a project with its intent in its own repository", revision, commit, err)
	}
	// A project that names a companion repository, read by a pipeline with no
	// access to it, reads what it always read.
	companion := Pipeline{}
	companion.Config.Product.IntentRepository = t.TempDir()
	revision, commit, err = companion.intentRevision(context.Background(), "")
	if err != nil || revision != nil || commit != "" {
		t.Fatalf("intentRevision() without access = %v, %q, %v; want nil, empty, nil", revision, commit, err)
	}
}

func TestIntentRouteSetsTheCompanionRepositoryAsTheRunsWorktrees(t *testing.T) {
	t.Parallel()

	project := &stubIntentWorktrees{}
	intent := &stubIntentWorktrees{}
	routed := Pipeline{Worktrees: project, IntentWorktrees: intent}.intentRoute()
	if routed.Worktrees != WorktreeManager(intent) || routed.projectWorktrees != WorktreeManager(project) || !routed.intentLanding {
		t.Fatalf("intentRoute() = %+v; want the intent manager as Worktrees, the project's kept, and intentLanding set", routed)
	}
}

func TestADocumentLandingInTheIntentRepositoryRunsOnlyTheIntentChecks(t *testing.T) {
	t.Parallel()

	var cfg config.Config
	cfg.DocumentChecks = []string{"make document-check"}
	cfg.Checks = []string{"make check"}
	cfg.Intent.Checks = []string{"yoyo artifact check --intent ."}
	document := &runstate.DocumentPublication{}
	run := &activeRun{pipeline: Pipeline{Config: cfg, intentLanding: true}, state: runstate.State{Document: document}}
	commands, declared := run.documentChecks()
	if !declared || !slices.Equal(commands, cfg.IntentChecks()) {
		t.Fatalf("documentChecks() = %v, %v; want the intent checks %v", commands, declared, cfg.IntentChecks())
	}
	// The run's own record decides too, so a run taken up again by a pipeline
	// that was not routed behaves as it did.
	recorded := &activeRun{pipeline: Pipeline{Config: cfg}, state: runstate.State{Document: document, Repository: runstate.RepositoryIntent}}
	if commands, declared := recorded.documentChecks(); !declared || !slices.Equal(commands, cfg.IntentChecks()) {
		t.Fatalf("documentChecks() for a recorded intent run = %v, %v; want %v", commands, declared, cfg.IntentChecks())
	}
	own := &activeRun{pipeline: Pipeline{Config: cfg}, state: runstate.State{Document: document}}
	if commands, declared := own.documentChecks(); !declared || !slices.Equal(commands, cfg.DocumentChecks) {
		t.Fatalf("documentChecks() for the project's own run = %v, %v; want %v", commands, declared, cfg.DocumentChecks)
	}
}

func TestADocumentLandingInTheIntentRepositoryPublishesNothingAndJoinsNoQueue(t *testing.T) {
	t.Parallel()

	var cfg config.Config
	cfg.Approvals.Publishing = domain.ApprovalAutomatic
	cfg.Execution.MergeQueue = true
	own := Pipeline{Config: cfg, MergeQueue: stubAdmissions{}}
	if !own.publishes() || !(&activeRun{pipeline: own}).admitsToMergeQueue() {
		t.Fatal("the project's own pipeline stopped publishing or admitting to the merge queue")
	}
	routed := own
	routed.intentLanding = true
	if routed.publishes() {
		t.Error("a landing in the companion intent repository publishes a pull request")
	}
	if (&activeRun{pipeline: routed}).admitsToMergeQueue() {
		t.Error("a landing in the companion intent repository is admitted to the project's merge queue")
	}
	recorded := &activeRun{pipeline: own, state: runstate.State{Repository: runstate.RepositoryIntent}}
	if recorded.admitsToMergeQueue() {
		t.Error("a recorded intent run is admitted to the project's merge queue")
	}
}

func TestWorktreesForChoosesTheRepositoryTheRunIsIn(t *testing.T) {
	t.Parallel()

	project := &stubIntentWorktrees{}
	intent := &stubIntentWorktrees{}
	p := Pipeline{Worktrees: project, IntentWorktrees: intent}
	if got := p.worktreesFor(runstate.State{}); got != WorktreeManager(project) {
		t.Errorf("worktreesFor(project run) = %v; want the project's manager", got)
	}
	if got := p.worktreesFor(runstate.State{Repository: runstate.RepositoryIntent}); got != WorktreeManager(intent) {
		t.Errorf("worktreesFor(intent run) = %v; want the intent manager", got)
	}
	if got := (Pipeline{Worktrees: project}).worktreesFor(runstate.State{Repository: runstate.RepositoryIntent}); got != nil {
		t.Errorf("worktreesFor(intent run) without access = %v; want nil, never the project's manager", got)
	}
}

type stubIntentWorktrees struct{ IntentWorktrees }

type stubAdmissions struct{ MergeQueueAdmissions }
