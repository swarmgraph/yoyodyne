package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func TestAForgeCheckHandbackContinuesThePreservedChangeThroughRepairCarryOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, forge, original := queuedOnProtectedTarget(t)
	fixture.docket = &memoryDocket{}
	forge.reading = redOnTheChange()
	reconciler := fixture.sweep(t, forge, false)
	reconciler.JobLogs = &refusingJobLogs{tail: "feature.txt:3: line is longer than 100 characters"}
	if _, err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, err := fixture.store.Load(original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.CheckFailure == nil || stopped.CheckFailure.ForgeHeadCommit != original.PullRequest.HeadCommit {
		t.Fatalf("failing check = %#v, want the forge failure bound to the withdrawn head", stopped.CheckFailure)
	}
	if stopped.Integration != nil || stopped.ChecksPassed != nil || stopped.ReviewDecision != "" || stopped.ReviewHeadCommit != "" {
		t.Fatal("a red change retained promotion, checks or review credit")
	}
	entries, err := fixture.docket.List()
	if err != nil || len(entries) != 1 || entries[0].Class != triage.ClassStoppedRun || entries[0].Check == nil {
		t.Fatalf("docket = %#v, %v; want a stopped run with its failing check", entries, err)
	}
	if rendered := entries[0].Render(); !strings.Contains(rendered, "A repair continues the preserved change") || strings.Contains(rendered, "merge request may be repeated") {
		t.Fatalf("docket does not direct this change to repair:\n%s", rendered)
	}
	if _, _, err := rearmablePublication(stopped); err == nil || !strings.Contains(err.Error(), "unchanged revision cannot be re-armed") {
		t.Fatalf("re-arm refusal = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		kept, err := os.ReadFile(filepath.Join(request.WorkingDirectory, "feature.txt"))
		if err != nil || string(kept) != "implemented\n" {
			t.Fatalf("developer received %q, %v; want the original change", kept, err)
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("repaired\n"), 0o600)
	}, approveVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, fixture.repository, fixture.worktreeRoot, fixture.store, fixture.tracker, provider, []string{"test \"$(cat feature.txt)\" = repaired"}), provider), forge)
	continuer := RepairContinuer{
		Docket: fixture.docket, Runs: fixture.store, Intake: intake, Decisions: fixture.store.Triage(),
		Items: fixture.tracker, Worktrees: pipeline.Worktrees.(RepairWorktrees),
		ConfiguredAttempts: 2, Capacity: 1, Start: pipeline.Continue, Clock: fixedClock{at: stopped.UpdatedAt},
	}
	if _, err := fixture.store.Triage().GrantRepair(ctx, stopped.WorkItemID,
		triageDecided(runstate.TriageDecisionRepair, stopped.RunID), continueGrantRounds, docketedNow, continueCaps); err != nil {
		t.Fatal(err)
	}
	carrying := CarryOut{
		Docket: fixture.docket, Decisions: fixture.store.Triage(), Reruns: fixture.store.Reruns(),
		Runs: fixture.store, Repairer: continuer, Clock: fixedClock{at: stopped.UpdatedAt},
	}
	carried, repaired, err := carrying.Carry(ctx, theOneOutstanding(t, carrying))
	if err != nil || !carried.Carried || repaired.RunID != original.RunID || repaired.PullRequest == nil || !repaired.PullRequest.MergeQueued {
		t.Fatalf("carry-out = %#v, outcome = %#v, %v", carried, repaired, err)
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 1 || requests[0].SessionID != stopped.ProviderSessionID || requests[0].WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("developer requests = %#v; want the preserved session and checkout", requests)
	}
	for _, want := range []string{"Forge check: lint", "How the forge ended lint: failure", "feature.txt:3: line is longer than 100 characters", stopped.CheckFailure.ForgeHeadCommit} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Errorf("developer prompt does not carry %q:\n%s", want, requests[0].Prompt)
		}
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatal("the repaired change was published without fresh independent review")
	}
	settled, err := fixture.store.Load(original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.CheckFailure != nil || settled.ChecksPassed == nil || settled.GrantedRepairAttempts() != continueGrantRounds || settled.RepairAttempts != stopped.RepairAttempts+1 {
		t.Fatalf("repaired record = %#v; want fresh checks and the existing repair budget", settled)
	}
	if settled.PublishFailure != "" || settled.MergeDrop != nil || settled.PullRequest.Checks != nil {
		t.Fatal("the repaired publication inherited the old head's drop or forge reading")
	}
	if settled.PullRequest.Number != original.PullRequest.Number || settled.PullRequest.HeadCommit == original.PullRequest.HeadCommit || settled.ReviewHeadCommit != settled.PullRequest.HeadCommit {
		t.Fatal("the repair did not update the same publication with a freshly reviewed head")
	}
}

func TestAnOlderForgeCheckHandbackNamesTheSupportedRepairAlternative(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, forge, original := queuedOnProtectedTarget(t)
	prior, err := fixture.store.Load(original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.docket = &memoryDocket{}
	forge.reading = redOnTheChange()
	if _, err := fixture.sweep(t, forge, false).Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	older, err := fixture.store.Load(original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	// The pre-change record retained its approval and promotion, and kept the
	// check only on the publication. Its docket could be a publication alone.
	prior.PullRequest, prior.MergeDrop = older.PullRequest, older.MergeDrop
	prior.PublishFailure, prior.Blocker = older.PublishFailure, older.Blocker
	older = prior
	if err := fixture.store.Save(older); err != nil {
		t.Fatal(err)
	}
	fixture.docket = &memoryDocket{}
	if _, err := docketerOverStore(fixture.docket, fixture.store, docketConfig()).Build(); err != nil {
		t.Fatal(err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	continuer := RepairContinuer{
		Docket: fixture.docket, Runs: fixture.store, Intake: intake, Decisions: fixture.store.Triage(),
		Items: fixture.tracker, Worktrees: &fakeOwnership{}, ConfiguredAttempts: 2, Capacity: 1,
		Start: func(context.Context, string, string) (Outcome, error) {
			t.Fatal("an older record was continued without repair input")
			return Outcome{}, nil
		},
	}
	if _, err := fixture.store.Triage().GrantRepair(ctx, older.WorkItemID,
		triageDecided(runstate.TriageDecisionRepair, older.RunID), continueGrantRounds, docketedNow, continueCaps); err != nil {
		t.Fatal(err)
	}
	_, err = continuer.Continue(ctx, RepairContinueRequest{Run: older.RunID})
	if err == nil || !strings.Contains(err.Error(), "supported alternative") || !strings.Contains(err.Error(), "yoyo triage rerun "+older.RunID) {
		t.Fatalf("older repair refusal = %v; want the supported alternative named", err)
	}
	// Some older stops were docketed only as publications. They get the same
	// supported alternative, rather than the generic missing-stoppage refusal.
	var publications []triage.Entry
	for _, entry := range fixture.docket.entries {
		if entry.Class == triage.ClassPublication {
			publications = append(publications, entry)
		}
	}
	if len(publications) == 0 {
		t.Fatal("the older record did not produce its publication entry")
	}
	fixture.docket.entries = publications
	_, err = continuer.Continue(ctx, RepairContinueRequest{Run: older.RunID})
	if err == nil || !strings.Contains(err.Error(), "yoyo triage rerun "+older.RunID) {
		t.Fatalf("publication-only repair refusal = %v", err)
	}
	unchanged, err := fixture.store.Load(older.RunID)
	if err != nil || unchanged.GrantedRepairAttempts() != 0 || unchanged.Blocker != older.Blocker {
		t.Fatal("a refused repair changed the stopped run or spent its grant")
	}
}
