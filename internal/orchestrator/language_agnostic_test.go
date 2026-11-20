package orchestrator

// Invariant 11 of docs/designs/v1-harness-design.md: "The harness assumes no language,
// build system, or test framework in the managed project. Verification is the
// commands the project declares, run in the worktree; the harness decides only
// whether they passed."
//
// Everything else in this suite drives the harness over a Go repository, so a
// change that quietly taught context assembly, worktree handling, diff
// summarization, or check execution to expect Go would leave every one of those
// tests passing. These drive it over a project that is not Go — a TypeScript
// fixture whose declared checks are shell scripts in the project itself — so
// the claim is verified rather than inferred from the check runner shelling out
// to /bin/sh.
//
// The fixture needs no toolchain beyond a POSIX shell, deliberately: a fixture
// that ran `npx tsc` would be verifying that Node.js is installed wherever the
// tests run, which is a fact about the CI image rather than about Yoyodyne.

import (
	"context"
	"os"
	"path/filepath"
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
	// nonGoFixture is the managed project these tests point the harness at.
	nonGoFixture = "testdata/typescript-fixture"
	// designDocument is where the invariant these tests verify is recorded.
	designDocument = "../../docs/designs/v1-harness-design.md"
	// languageInvariant is the sentence that states it. The fixture exists for
	// this claim alone, so the two are tied together here rather than left to be
	// rediscovered.
	languageInvariant = "The harness assumes no language, build system, or test framework in the managed project."

	nonGoExportsCheck     = "sh scripts/check-exports.sh"
	nonGoIndentationCheck = "sh scripts/check-indentation.sh"
)

// A project that is not Go goes through every phase: context assembled from its
// Markdown, a worktree, its own declared checks, a review of a diff naming its
// files, and integration.
func TestPipelineDrivesANonGoProjectThroughEveryPhase(t *testing.T) {
	t.Parallel()

	repository := nonGoFixtureRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "typescript-fixture-1",
		Title:              "Add a farewell greeting",
		Description:        "Follow docs/design.md",
		AcceptanceCriteria: "src/farewell.ts exports farewell()",
		Status:             "open",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "farewell.ts"), []byte(nonGoSource("  ")), 0o600)
	}, approveVerdict)
	pipeline, store := nonGoFixturePipeline(t, repository, tracker, provider)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Phase != runstate.PhaseComplete || !outcome.WorkItemClosed {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if outcome.ReviewDecision != review.DecisionApprove || outcome.Integration == nil {
		t.Fatalf("Run() review and integration = %#v, %#v", outcome.ReviewDecision, outcome.Integration)
	}
	if _, err := os.Stat(filepath.Join(repository, "src", "farewell.ts")); err != nil {
		t.Fatalf("integrated change is missing from the primary checkout: %v", err)
	}

	// The commands the run verified with are the ones the project declared, and
	// they are the only thing the harness knew about its toolchain.
	if got := pipeline.Config.Checks; len(got) != 2 || got[0] != nonGoExportsCheck || got[1] != nonGoIndentationCheck {
		t.Fatalf("configured checks = %#v, want the fixture's own", got)
	}
	if len(outcome.Checks) != 2 {
		t.Fatalf("check results = %#v, want both declared checks", outcome.Checks)
	}
	for _, result := range outcome.Checks {
		if !result.Passed || result.Process.ExitCode != 0 {
			t.Fatalf("check %q = %#v, want a pass", result.Command, result)
		}
	}

	// Context assembly read this project's Markdown, and the reviewer was shown a
	// diff of its files. Neither step has a language to recognize.
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	reviewerRequests := provider.RequestsForRole(domain.RoleReviewer)
	if len(developerRequests) != 1 || len(reviewerRequests) != 1 {
		t.Fatalf("invocations: developer = %d, reviewer = %d", len(developerRequests), len(reviewerRequests))
	}
	if !strings.Contains(developerRequests[0].Prompt, "pure functions") {
		t.Fatalf("developer prompt did not carry the referenced design:\n%s", developerRequests[0].Prompt)
	}
	if !strings.Contains(reviewerRequests[0].Prompt, "src/farewell.ts") {
		t.Fatalf("reviewer prompt did not name the changed source:\n%s", reviewerRequests[0].Prompt)
	}

	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusSucceeded || state.Phase != runstate.PhaseComplete || state.Integration == nil {
		t.Fatalf("state = %#v", state)
	}
}

// A failing check in a project that is not Go stops the run exactly where a
// failing Go check does: before review, before integration, with the work
// preserved and the item blocked once the repair budget is spent.
func TestPipelineStopsANonGoProjectAtItsFailingCheck(t *testing.T) {
	t.Parallel()

	repository := nonGoFixtureRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "typescript-fixture-2",
		Title:              "Add a farewell greeting",
		Description:        "Follow docs/design.md",
		AcceptanceCriteria: "src/farewell.ts exports farewell()",
		Status:             "open",
	}}
	// The module exports what it should and is indented with a tab, so the
	// project's first check passes and its second fails. A run stopped by that is
	// stopped by the check that failed rather than by having checks at all.
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "farewell.ts"), []byte(nonGoSource("\t")), 0o600)
	}, approveVerdict)
	pipeline, store := nonGoFixturePipeline(t, repository, tracker, provider)
	before := gitLine(t, repository, "rev-parse", "refs/heads/main")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	wantFailure := "verification failed after 2 of 2 permitted attempt(s)"
	if err == nil || !strings.Contains(err.Error(), wantFailure) {
		t.Fatalf("Run() error = %v, want %q", err, wantFailure)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 0 {
		t.Fatal("a failing check in a non-Go project reached the reviewer")
	}
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 3 {
		t.Fatalf("developer invocations = %d, want the first attempt and both repairs", runs)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a failing check reached integration: %#v, closed = %t", outcome.Integration, tracker.Closed)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
		t.Fatalf("main moved on a failing check: %q, want %q", head, before)
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "src", "farewell.ts")); err != nil {
		t.Fatalf("failed worktree was not preserved: %v", err)
	}

	// One check passed and one failed, and the run stopped at the failing one
	// without running anything after it.
	if len(outcome.Checks) != 2 {
		t.Fatalf("check results = %#v, want the passing check and the failing one", outcome.Checks)
	}
	if !outcome.Checks[0].Passed || outcome.Checks[0].Command != nonGoExportsCheck {
		t.Fatalf("first check = %#v, want the fixture's passing check", outcome.Checks[0])
	}
	if outcome.Checks[1].Passed || outcome.Checks[1].Command != nonGoIndentationCheck {
		t.Fatalf("second check = %#v, want the fixture's failing check", outcome.Checks[1])
	}

	// What could not be repaired is recorded where the work is tracked, in the
	// project's own words rather than a harness paraphrase of them.
	if !tracker.Blocked || !outcome.Blocked {
		t.Fatalf("spent repair budget did not block the item: tracker = %t, outcome = %t", tracker.Blocked, outcome.Blocked)
	}
	for _, want := range []string{
		"Failing check: " + nonGoIndentationCheck + " (exit 1)",
		"indented with tabs rather than spaces",
		"src/farewell.ts",
	} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Fatalf("blocker is missing %q:\n%s", want, tracker.BlockReason)
		}
	}

	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusFailed || state.Phase != runstate.PhaseChecking || state.ReviewDecision != "" || state.Integration != nil {
		t.Fatalf("state = %#v", state)
	}
	if state.CheckFailure == nil || state.CheckFailure.Command != nonGoIndentationCheck || state.CheckFailure.ExitCode != 1 {
		t.Fatalf("durable check failure = %#v", state.CheckFailure)
	}
}

// The fixture is only evidence for the invariant while both halves of the link
// hold: the design document still makes the claim, and the fixture still needs
// nothing but a shell to verify it. An amendment to either is a reason to
// revisit the other, so it fails here rather than being noticed later.
func TestNonGoFixtureVerifiesTheLanguageAgnosticInvariant(t *testing.T) {
	t.Parallel()

	design, err := os.ReadFile(designDocument)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", designDocument, err)
	}
	if !strings.Contains(string(design), languageInvariant) {
		t.Fatalf("%s no longer states %q; %s is what verifies that claim and needs revisiting with it", designDocument, languageInvariant, nonGoFixture)
	}

	cfg, err := config.Load(filepath.Join(nonGoFixture, config.DirectoryName, "config.yaml"))
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if len(cfg.Checks) == 0 {
		t.Fatal("the fixture declares no checks")
	}
	for _, check := range cfg.Checks {
		script, found := strings.CutPrefix(check, "sh ")
		if !found {
			t.Fatalf("check %q runs something other than a shell script; the fixture must need no runtime beyond a POSIX shell", check)
		}
		contents, err := os.ReadFile(filepath.Join(nonGoFixture, script))
		if err != nil {
			t.Fatalf("check %q names no script in the fixture: %v", check, err)
		}
		if !strings.HasPrefix(string(contents), "#!/bin/sh\n") {
			t.Fatalf("%s is not a POSIX shell script", script)
		}
	}
}

// nonGoSource is the module a fake developer writes, indented with whatever the
// caller passes: two spaces is what the project requires, and a tab is what its
// second check fails on.
func nonGoSource(indent string) string {
	return "export function farewell(name: string): string {\n" + indent + "return `Goodbye, ${name}!`;\n}\n"
}

// nonGoFixtureRepository stands the fixture up as a Git repository of its own,
// which is how a managed project actually reaches the harness: a working tree
// carrying its own .yoyodyne configuration, committed.
func nonGoFixtureRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "typescript-project")
	if err := os.CopyFS(repository, os.DirFS(nonGoFixture)); err != nil {
		t.Fatalf("CopyFS(%q) error = %v", nonGoFixture, err)
	}
	runPipelineGit(t, repository, "init", "-b", "main")
	runPipelineGit(t, repository, "config", "user.name", "Yoyodyne Test")
	runPipelineGit(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	disablePipelineMaintenance(t, repository)
	// Registered after the TempDir call above and therefore run before TempDir's
	// removal, so the repository is idle by the time Go deletes it.
	t.Cleanup(func() { removeLinkedPipelineWorktrees(t, repository) })
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "initial")
	return repository
}

// nonGoFixturePipeline wires the harness over the fixture project's own
// configuration, so the checks a run verifies with are the ones that project
// declared rather than ones a test chose for it. Everything else is the wiring
// the Go fixtures use, which is the point: nothing between the two is told what
// language it is looking at.
func nonGoFixturePipeline(t *testing.T, repository string, tracker *orchestratortest.Tracker, provider *orchestratortest.Backend) (Pipeline, *runstate.Store) {
	t.Helper()
	cfg, err := config.Load(filepath.Join(repository, config.DirectoryName, "config.yaml"))
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	// Configuration states the repository relative to the project; the harness is
	// given the resolved path, exactly as the CLI resolves it.
	cfg.Product.Repository = repository
	reviewer, found := cfg.Agents["reviewer"]
	if !found {
		t.Fatal("the fixture inherits no reviewer agent")
	}

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
	return Pipeline{
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
		StateRoot:  t.TempDir(),
	}, store
}
