package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func TestAForgeCheckHandbackContinuesThePreservedChangeThroughRepairCarryOut(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		fixture func(*testing.T) (queuedFixture, *checkedForge, Outcome)
	}{
		{name: "through the pull request", fixture: queuedOnProtectedTarget},
		{name: "locally promoted evidence with preserved artifacts", fixture: queuedLocalEvidenceWithPreservedArtifacts},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			forgeCheckRepairCarryOut(t, test.fixture)
		})
	}
}

func queuedLocalEvidenceWithPreservedArtifacts(t *testing.T) (queuedFixture, *checkedForge, Outcome) {
	t.Helper()
	fixture := newQueuedFixture(t)
	provider := orchestratortest.RoleBackend(writeFeature, approveEvidenceVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, fixture.repository, fixture.worktreeRoot, fixture.store, fixture.tracker, provider, []string{"exit 0"}), provider), fixture.forge)
	// A locally promoted run whose cleanup could not start still holds its
	// change. Evidence keeps its item open, so the forge handback can be repaired.
	pipeline.Worktrees = &hookedWorktrees{
		WorktreeManager: pipeline.Worktrees,
		beforeCleanup:   func() error { return errors.New("worktree is busy") },
	}
	outcome, err := pipeline.Run(context.Background(), fixture.tracker.Record().Item.ID)
	if err != nil || outcome.Integration == nil || outcome.Integration.ThroughPullRequest || outcome.PullRequest == nil || !outcome.PullRequest.MergeQueued || outcome.WorktreeRemoved || outcome.BranchRemoved {
		t.Fatalf("local evidence landing = %#v, %v; want a queued local promotion with preserved artifacts", outcome, err)
	}
	if outcome.WorkItemClosed || fixture.tracker.Record().Item.Status == "closed" || outcome.ReviewApproves != "evidence" {
		t.Fatal("the evidence landing closed its item")
	}
	return fixture, &checkedForge{queuedForge: fixture.forge}, outcome
}

func forgeCheckRepairCarryOut(t *testing.T, makeFixture func(*testing.T) (queuedFixture, *checkedForge, Outcome)) {
	t.Helper()
	ctx := context.Background()
	fixture, forge, original := makeFixture(t)
	fixture.docket = &memoryDocket{}
	forge.reading = redOnTheChange()
	reconciler := fixture.sweep(t, forge, false)
	reconciler.JobLogs = &orchestratortest.JobLogs{Tail: "feature.txt:3: line is longer than 100 characters"}
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
	if !original.Integration.ThroughPullRequest && stopped.CheckFailure.LocalPromotion == nil {
		t.Fatal("the local promotion was not recorded as cleanup history")
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
		ConfiguredAttempts: 2, Capacity: 1, Clock: fixedClock{at: stopped.UpdatedAt},
		Start: func(ctx context.Context, item, run string) (Outcome, error) {
			continued, err := fixture.store.Load(run)
			if err != nil {
				t.Fatal(err)
			}
			if continued.Integration != nil || continued.ChecksPassed != nil || continued.ReviewDecision != "" || continued.MergeDrop != nil || continued.CheckFailure == nil || continued.CheckFailure.LocalPromotion != nil {
				t.Fatal("repair retained the old publication or promotion credit")
			}
			if len(continued.RepairContinuations) != 1 || !reflect.DeepEqual(continued.RepairContinuations[0].SupersededCheckFailure, stopped.CheckFailure) {
				t.Fatal("repair lost the failed check or its local promotion history")
			}
			return pipeline.Continue(ctx, item, run)
		},
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
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) == 0 {
		t.Fatal("the repaired change was published without fresh independent review")
	}
	for _, request := range reviews {
		if request.SessionID != "" {
			t.Fatal("the repair resumed an old review session")
		}
	}
	settled, err := fixture.store.Load(original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.CheckFailure != nil || settled.ChecksPassed == nil || settled.GrantedRepairAttempts() != continueGrantRounds || settled.RepairAttempts != stopped.RepairAttempts+1 {
		t.Fatalf("repaired record = %#v; want fresh checks and the existing repair budget", settled)
	}
	if !reflect.DeepEqual(settled.RepairContinuations[0].SupersededCheckFailure, stopped.CheckFailure) {
		t.Fatal("fresh checks discarded the history of the repaired failure")
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
		Items: fixture.tracker, Worktrees: &orchestratortest.Ownership{}, ConfiguredAttempts: 2, Capacity: 1,
		Start: func(context.Context, string, string) (Outcome, error) {
			t.Fatal("an older record was continued without repair input")
			return Outcome{}, nil
		},
	}
	if _, err := fixture.store.Triage().GrantRepair(ctx, older.WorkItemID,
		triageDecided(runstate.TriageDecisionRepair, older.RunID), continueGrantRounds, docketedNow, continueCaps); err != nil {
		t.Fatal(err)
	}
	carrying := CarryOut{
		Docket: fixture.docket, Decisions: fixture.store.Triage(), Reruns: fixture.store.Reruns(),
		Runs: fixture.store, Repairer: continuer, Clock: fixedClock{at: older.UpdatedAt},
	}
	carried, _, err := carrying.Carry(ctx, theOneOutstanding(t, carrying))
	if err != nil || carried.Carried || !strings.Contains(carried.Problem, "yoyo triage rerun "+older.RunID) {
		t.Fatalf("older repair carry-out = %#v, %v; want a refusal naming the supported re-run", carried, err)
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

func TestAForgeCheckHandbackRecoversAWithdrawnMergeAndKeepsClosedItemsClosed(t *testing.T) {
	t.Parallel()
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "withdrawn before settlement", true: "item already closed"}[closed], func(t *testing.T) {
			fixture, forge, original := queuedOnProtectedTarget(t)
			forge.reading = redOnTheChange()
			if closed {
				fixture.tracker.(*orchestratortest.Tracker).Item.Status = "closed"
			} else {
				forge.DropQueuedMerge()
			}
			if _, err := fixture.sweep(t, forge, false).Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			stopped, err := fixture.store.Load(original.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if stopped.CheckFailure == nil || stopped.CheckFailure.ForgeHeadCommit != original.PullRequest.HeadCommit || stopped.Integration != nil || stopped.PullRequest.MergeQueued {
				t.Fatal("the handback lost its repair input or retained publication credit")
			}
			if closed && fixture.tracker.Record().Item.Status != "closed" {
				t.Fatal("settling the forge failure reopened a closed item")
			}
		})
	}
}

func TestADroppedMergeRecordsItsOwnForgeFailureEvenWhenItCannotBeReplayed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		fixture func(*testing.T) (queuedFixture, *checkedForge, Outcome)
		change  func(*runstate.State)
		gone    bool
	}{
		{
			name: "promotion resumption limit reached",
			change: func(state *runstate.State) {
				for range runstate.MaxIntegrationResumptions {
					state.IntegrationResumptions = append(state.IntegrationResumptions, runstate.IntegrationResumption{
						Cause: runstate.CauseQueuedHeadBehind, Reason: "the queued head fell behind its target", ResumedAt: state.UpdatedAt,
					})
				}
			},
		},
		{
			name: "branch already removed",
			gone: true,
			change: func(state *runstate.State) {
				state.BranchRemoved = true
				state.BranchSweptAt = &state.UpdatedAt
			},
		},
		{
			name: "local promotion already cleaned up",
			gone: true,
			fixture: func(t *testing.T) (queuedFixture, *checkedForge, Outcome) {
				fixture := newQueuedFixture(t)
				original := fixture.run(t)
				return fixture, &checkedForge{queuedForge: fixture.forge}, original
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			makeFixture := test.fixture
			if makeFixture == nil {
				makeFixture = queuedOnProtectedTarget
			}
			fixture, forge, original := makeFixture(t)
			prior, err := fixture.store.Load(original.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if test.change != nil {
				test.change(&prior)
			}
			if why := unreplayable(prior); why == "" {
				t.Fatal("the fixture still permits replay")
			}
			if err := fixture.store.Save(prior); err != nil {
				t.Fatal(err)
			}
			fixture.docket = &memoryDocket{}
			forge.DropQueuedMerge()
			forge.reading = redOnTheChange()
			if _, err := fixture.sweep(t, forge, false).Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			stopped, err := fixture.store.Load(original.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if stopped.CheckFailure == nil || stopped.CheckFailure.Command != "lint" || stopped.CheckFailure.ForgeHeadCommit != original.PullRequest.HeadCommit {
				t.Fatalf("failing check = %#v; want the dropped head's own forge failure", stopped.CheckFailure)
			}
			if stopped.Integration != nil || stopped.PullRequest.MergeQueued {
				t.Fatal("the dropped red revision retained promotion credit or a queued merge")
			}
			if _, _, err := rearmablePublication(stopped); err == nil || !strings.Contains(err.Error(), "unchanged revision cannot be re-armed") {
				t.Fatalf("re-arm refusal = %v", err)
			}
			entries, err := fixture.docket.List()
			if err != nil || len(entries) != 1 || entries[0].Check == nil || entries[0].Class != triage.ClassStoppedRun {
				t.Fatalf("docket = %#v, %v; want the failing check on the stopped run", entries, err)
			}
			if strings.Contains(entries[0].Render(), "merge request may be repeated") {
				t.Fatal("the docket offered a re-arm of the unchanged red revision")
			}
			if test.gone {
				intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
				if err != nil {
					t.Fatal(err)
				}
				continuer := RepairContinuer{
					Docket: fixture.docket, Runs: fixture.store, Intake: intake, Decisions: fixture.store.Triage(),
					Items: fixture.tracker, Worktrees: &orchestratortest.Ownership{}, ConfiguredAttempts: 2, Capacity: 1,
					Start: func(context.Context, string, string) (Outcome, error) {
						t.Fatal("a missing change was continued")
						return Outcome{}, nil
					},
				}
				_, err = continuer.Continue(ctx, RepairContinueRequest{Run: stopped.RunID})
				if err == nil || !strings.Contains(err.Error(), "supported alternative") || !strings.Contains(err.Error(), "yoyo triage rerun "+stopped.RunID) {
					t.Fatalf("repair refusal = %v; want the supported re-run named", err)
				}
				if !strings.Contains(entries[0].Render(), "yoyo triage rerun "+stopped.RunID) {
					t.Fatal("the docket did not name the supported alternative when repair cannot continue")
				}
			}
		})
	}
}

func TestAnUnreplayableDroppedMergeKeepsItsRecoveryForUnrelatedFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, forge, original := queuedOnProtectedTarget(t)
	prior, err := fixture.store.Load(original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for range runstate.MaxIntegrationResumptions {
		prior.IntegrationResumptions = append(prior.IntegrationResumptions, runstate.IntegrationResumption{
			Cause: runstate.CauseQueuedHeadBehind, Reason: "the queued head fell behind its target", ResumedAt: prior.UpdatedAt,
		})
	}
	if err := fixture.store.Save(prior); err != nil {
		t.Fatal(err)
	}
	forge.DropQueuedMerge()
	forge.reading = redOnTheChange()
	forge.reading.Failing[0].Paths = []string{"unrelated.txt"}
	if _, err := fixture.sweep(t, forge, false).Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, err := fixture.store.Load(original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.CheckFailure != nil || stopped.Integration == nil || stopped.MergeDrop == nil || stopped.PullRequest.MergeQueued {
		t.Fatal("an unrelated failure lost the existing dropped-merge recovery")
	}
	if _, _, err := rearmablePublication(stopped); err != nil {
		t.Fatalf("re-arm eligibility for an unrelated failure = %v", err)
	}
}

func TestAnUnreplayableDroppedMergeRecordsUnannotatedFailuresAttributedToItsChange(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		for _, targetPasses := range []bool{false, true} {
			name := "resumption limit / "
			if missing {
				name = "missing branch / "
			}
			if targetPasses {
				name += "check passes on target"
			} else {
				name += "job log names changed package"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				fixture, forge, original := queuedOnProtectedTarget(t)
				prior := loadRun(t, fixture.store, original.RunID)
				if missing {
					prior.BranchRemoved = true
					prior.BranchSweptAt = &prior.UpdatedAt
				} else {
					for range runstate.MaxIntegrationResumptions {
						prior.IntegrationResumptions = append(prior.IntegrationResumptions, runstate.IntegrationResumption{
							Cause: runstate.CauseQueuedHeadBehind, Reason: "the queued head fell behind its target", ResumedAt: prior.UpdatedAt,
						})
					}
				}
				if why := unreplayable(prior); why == "" {
					t.Fatal("the fixture still permits replay")
				}
				if err := fixture.store.Save(prior); err != nil {
					t.Fatal(err)
				}
				fixture.docket = &memoryDocket{}
				forge.DropQueuedMerge()
				forge.reading = pullRequest907Reading()
				if recordedChecks(forge.reading, prior.UpdatedAt).ChangeFails() {
					t.Fatal("the fixture attributed the failure by annotation paths")
				}
				reconciler := fixture.sweep(t, forge, false)
				filer := &orchestratortest.RecordingFiler{}
				// Attribution also works where nothing is wired to file target work.
				if missing {
					reconciler.Filer = filer
				}
				main := &orchestratortest.TargetChecks{Reading: publish.BranchCheckReading{HeadCommit: prior.BaseCommit}}
				logs := &orchestratortest.JobLogs{Tail: pullRequest907Log}
				want := "the forge's account of build names internal/machinehome"
				if targetPasses {
					main.Reading.Passing = []string{"build"}
					logs.Tail = "Process completed with exit code 2."
					want = "build passes on main's own head"
				}
				reconciler.TargetChecks, reconciler.JobLogs = main, logs
				results, err := reconciler.Reconcile(ctx)
				if err != nil {
					t.Fatal(err)
				}
				assertHandedBackToTheChange(t, fixture, forge, fixture.tracker.(*orchestratortest.Tracker), filer, results, want)
				if len(main.Asked) != 1 || main.Asked[0] != "main" {
					t.Fatalf("target checks asked = %v; want one attribution reading", main.Asked)
				}
				stopped := loadRun(t, fixture.store, original.RunID)
				if stopped.CheckFailure == nil || stopped.CheckFailure.Command != "build" || stopped.CheckFailure.ForgeHeadCommit != original.PullRequest.HeadCommit {
					t.Fatalf("repair input = %#v; want the attributed forge failure", stopped.CheckFailure)
				}
				if !strings.Contains(stopped.CheckFailure.Output, "How the forge ended build: failure") || (!targetPasses && !strings.Contains(stopped.CheckFailure.Output, "internal/machinehome")) {
					t.Fatalf("repair input lost the forge's account: %s", stopped.CheckFailure.Output)
				}
				if stopped.Integration != nil || stopped.ChecksPassed != nil || stopped.ReviewDecision != "" {
					t.Fatal("the failing change retained publication credit")
				}
				if _, _, err := rearmablePublication(stopped); err == nil || !strings.Contains(err.Error(), "unchanged revision cannot be re-armed") {
					t.Fatalf("re-arm refusal = %v", err)
				}
				entries, err := fixture.docket.List()
				if err != nil || len(entries) != 1 || entries[0].Check == nil || entries[0].Class != triage.ClassStoppedRun {
					t.Fatalf("docket = %#v, %v; want the forge failure on the stopped run", entries, err)
				}
				if rendered := entries[0].Render(); !strings.Contains(rendered, "A repair continues the preserved change") || strings.Contains(rendered, "merge request may be repeated") {
					t.Fatalf("the docket did not direct the red change to repair: %s", rendered)
				}
			})
		}
	}
}

func TestAnUnreplayableLevelHeadKeepsDroppedMergeRecoveryForUnrelatedFailures(t *testing.T) {
	t.Parallel()
	for _, confirmed := range []bool{false, true} {
		name := "target checks pending and log names unrelated work"
		if confirmed {
			name = "check also fails on target"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			filer := &orchestratortest.RecordingFiler{}
			fixture, forge, _, reconciler, logs := redTargetSweep(t, filer)
			prior := loadRun(t, fixture.store, pipelineRunID)
			for range runstate.MaxIntegrationResumptions {
				prior.IntegrationResumptions = append(prior.IntegrationResumptions, runstate.IntegrationResumption{
					Cause: runstate.CauseQueuedHeadBehind, Reason: "the queued head fell behind its target", ResumedAt: prior.UpdatedAt,
				})
			}
			if err := fixture.store.Save(prior); err != nil {
				t.Fatal(err)
			}
			forge.DropQueuedMerge()
			forge.reading = pullRequest907Reading()
			main := &orchestratortest.TargetChecks{Reading: publish.BranchCheckReading{HeadCommit: prior.BaseCommit, Pending: []string{"build"}}}
			if confirmed {
				main.Reading.Pending, main.Reading.Failing = nil, []string{"build"}
				// The target's own failure takes precedence even if the log names
				// a package this change also touches.
				logs.Tail = pullRequest907Log
			}
			reconciler.TargetChecks = main
			results, err := reconciler.Reconcile(ctx)
			if err != nil || len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
				t.Fatalf("reconciliation = %#v, %v; want the existing dropped-merge handback", results, err)
			}
			stopped := loadRun(t, fixture.store, pipelineRunID)
			if stopped.CheckFailure != nil || stopped.Integration == nil || stopped.MergeDrop == nil || stopped.PullRequest.MergeQueued || stopped.PullRequest.TargetRed != nil {
				t.Fatal("an unrelated failure lost its existing dropped-merge recovery")
			}
			if len(filer.Filed) != 0 || len(main.Asked) != 1 {
				t.Fatalf("filed = %#v, target checks asked = %v; want attribution only, with no new target wait", filer.Filed, main.Asked)
			}
			if _, _, err := rearmablePublication(stopped); err != nil {
				t.Fatalf("re-arm eligibility for an unrelated failure = %v", err)
			}
		})
	}
}
