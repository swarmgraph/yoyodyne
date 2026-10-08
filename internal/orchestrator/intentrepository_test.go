package orchestrator

// A project whose intent is kept in a companion intent repository
// (docs/designs/machine-home.md, "The companion intent repository"): its own
// repository holds nothing of the harness's — no .yoyodyne, no docs/product,
// no designs, decision records, or invariants — and a run against it still
// hands the developer and the reviewer the same documents a project keeping
// them in its own repository would, read from the companion repository.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	companionBriefSentence  = "Calc adds up the numbers a person writes down."
	companionDesignSentence = "Answers are written one per line under src."
	companionInvariantTitle = "At most one promotion per target branch, taken by the harness"
)

func TestRunAgainstAProjectRepositoryWithNoneOfTheHarnessFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// The project's own repository: its code, and nothing else.
	repository := filepath.Join(root, "calc")
	writeCompanionFile(t, repository, "src/total.txt", "0\n")
	initCompanionRepository(t, repository)
	t.Cleanup(func() { removeLinkedPipelineWorktrees(t, repository) })

	// The project's directory in the machine home: the configuration, and the
	// companion intent repository beside it.
	projectDirectory := filepath.Join(root, "home", "projects", "calc")
	intent := filepath.Join(projectDirectory, "intent")
	writeCompanionFile(t, intent, "docs/product/brief.md", "# Calc\n\n"+companionBriefSentence+"\n")
	writeCompanionFile(t, intent, "docs/designs/sums.md", "# Sums\n\n"+companionDesignSentence+"\n")
	invariant, err := os.ReadFile(filepath.Join("..", "..", "docs", "decisions", "invariants", "one-promotion-per-target-branch.md"))
	if err != nil {
		t.Fatalf("read the invariant fixture: %v", err)
	}
	writeCompanionFile(t, intent, "docs/decisions/invariants/one-promotion-per-target-branch.md", string(invariant))
	initCompanionRepository(t, intent)

	configPath := filepath.Join(projectDirectory, config.FileName)
	writeCompanionFile(t, projectDirectory, config.FileName, `version: 1
extends: builtin:v1
product:
  id: calc
  repository: `+repository+`
intent:
  repository: intent
approvals:
  integration: automatic
checks:
  - test -f src/answer.txt
`)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if cfg.Product.IntentRepository != intent || cfg.Product.IntentRoot(repository) != intent {
		t.Fatalf("intent repository = %q, root = %q, want %q", cfg.Product.IntentRepository, cfg.Product.IntentRoot(repository), intent)
	}

	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "calc-1",
		Title:              "Write the answer",
		Description:        "Follow docs/designs/sums.md",
		AcceptanceCriteria: "src/answer.txt holds the answer",
		Status:             "open",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "answer.txt"), []byte("42\n"), 0o600)
	}, approveVerdict)
	reviewer := cfg.Agents["reviewer"]
	store, err := runstate.NewStore(t.TempDir(), cfg.Product.ID)
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	processRunner := execution.OSProcessRunner{}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         processRunner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	pipeline := Pipeline{
		Tracker:    tracker,
		Worktrees:  worktrees,
		Store:      store,
		Backend:    provider,
		Reviewer:   review.Reviewer{Backend: provider, Model: reviewer.Model},
		Checks:     checks.Runner{Process: processRunner},
		Directives: newDirectiveStore(t),
		Holds:      newOperatorHoldStore(t),
		Intake:     newIntakeHoldStore(t),
		NewRunID:   func() (string, error) { return pipelineRunID, nil },
		Repository: repository,
		Config:     cfg,
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Phase != runstate.PhaseComplete || outcome.Integration == nil {
		t.Fatalf("Run() outcome = %#v", outcome)
	}

	developer := provider.RequestsForRole(domain.RoleDeveloper)
	reviewing := provider.RequestsForRole(domain.RoleReviewer)
	if len(developer) != 1 || len(reviewing) != 1 {
		t.Fatalf("invocations: developer = %d, reviewer = %d", len(developer), len(reviewing))
	}
	for _, want := range []string{companionBriefSentence, companionDesignSentence, companionInvariantTitle, "companion intent repository"} {
		if !strings.Contains(developer[0].Prompt, want) {
			t.Errorf("developer prompt does not carry %q from the intent repository:\n%s", want, developer[0].Prompt)
		}
	}
	for _, want := range []string{companionBriefSentence, companionInvariantTitle} {
		if !strings.Contains(reviewing[0].Prompt, want) {
			t.Errorf("reviewer prompt does not carry %q from the intent repository", want)
		}
	}

	// The project's repository still holds its code and nothing of the
	// harness's, including after the run integrated into it.
	listing, err := attemptPipelineGit(repository, "ls-files")
	if err != nil {
		t.Fatalf("git ls-files error = %v: %s", err, listing)
	}
	tracked := strings.Fields(listing)
	sort.Strings(tracked)
	if strings.Join(tracked, " ") != "src/answer.txt src/total.txt" {
		t.Fatalf("project repository tracks %v, want only its own code", tracked)
	}
	for _, name := range []string{config.DirectoryName, "docs", ".beads", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Lstat(filepath.Join(repository, name)); err == nil {
			t.Errorf("project repository holds %s", name)
		}
	}
}

func writeCompanionFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func initCompanionRepository(t *testing.T, repository string) {
	t.Helper()
	runPipelineGit(t, repository, "init", "-b", "main")
	runPipelineGit(t, repository, "config", "user.name", "Yoyodyne Test")
	runPipelineGit(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	disablePipelineMaintenance(t, repository)
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "initial")
}
