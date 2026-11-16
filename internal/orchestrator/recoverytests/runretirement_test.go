package recoverytests

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestRetirementKeepsANewerRequestWhenAnOlderRequestCompletesAfterItStarted(t *testing.T) {
	t.Parallel()
	f, _, r, newer := testharness.UpdatingRetirementFixture(t)
	original := newer
	original.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: newer.PullRequest.HeadCommit, TargetCommit: newer.PullRequest.HeadCommit, PreviousTargetCommit: newer.BaseCommit, ThroughPullRequest: true}
	by := testharness.RecordSupersedingMerge(t, f, original)
	by.StartedAt = newer.StartedAt.Add(-time.Minute)
	if err := f.Store.Save(by); err != nil {
		t.Fatal(err)
	}
	p := *newer.PullRequest
	p.Number = by.PullRequest.Number + 1
	p.URL = "https://example.invalid/pull/752"
	newer.PullRequest = &p
	if err := f.Store.Save(newer); err != nil {
		t.Fatal(err)
	}
	if !by.CompletedAt.After(newer.StartedAt) {
		t.Fatal("the older request must finish after the newer run starts")
	}
	testharness.CloseRetirementItem(f)
	r.Continue = func(context.Context, string, string) (orchestrator.Outcome, error) {
		t.Fatal("a closed item's newer request must not be resumed")
		return orchestrator.Outcome{}, nil
	}
	results, err := r.ContinueUpdates(context.Background())
	if err != nil || len(results) != 1 {
		t.Fatalf("continuation pass = %+v, %v", results, err)
	}
	result := results[0]
	saved := testharness.LoadRun(t, f.Store, newer.RunID)
	if result.Retired || saved.Retirement != nil || result.Failure == "" || result.Finding == nil || result.Finding.Mover != readmodel.MoverDevelopmentManager {
		t.Fatalf("the older request did not leave an owned per-item finding: %+v", result)
	}
	if !saved.HoldsDeveloperSlot() || saved.Status != newer.Status || saved.Phase != newer.Phase || saved.Branch != newer.Branch || saved.WorktreePath != newer.WorktreePath || saved.ProviderSessionID != newer.ProviderSessionID {
		t.Fatal("the newer request's run was changed by the older merge")
	}
	if f.Tracker.Record().Item.Status != "closed" || f.Tracker.(*orchestratortest.Tracker).Reopened {
		t.Fatal("the closed item was reopened")
	}
}

func TestQueuedContinuationRetirementDoesNotGuessFromClosedStatus(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"no merge", "unsettled merge", "newer unsettled merge", "newer replaying publication", "reopened", "wrong target", "evidence landing", "publication held"} {
		t.Run(kind, func(t *testing.T) {
			f, _, r, updating := testharness.UpdatingRetirementFixture(t)
			original := updating
			original.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: updating.PullRequest.HeadCommit, TargetCommit: updating.PullRequest.HeadCommit, PreviousTargetCommit: updating.BaseCommit, ThroughPullRequest: true}
			var release func() error
			if kind != "no merge" {
				by := testharness.RecordSupersedingMerge(t, f, original)
				switch kind {
				case "unsettled merge":
					by.PullRequest.MergeCommit = ""
					by.PublishFailure = "confirm the merge reached main: transport unavailable"
				case "wrong target":
					integration := *by.Integration
					integration.TargetBranch = "other"
					by.Integration = &integration
					by.TargetBranch = "other"
				case "evidence landing":
					by.LandingOutcome, by.LandingReason = runstate.LandingEvidence, "the change leaves work outstanding"
				}
				if err := f.Store.Save(by); err != nil {
					t.Fatal(err)
				}
				if kind == "newer unsettled merge" || kind == "newer replaying publication" {
					newer := by
					newer.RunID = "run-ffffffffffffffffffffffffffffffff"
					newer.StartedAt = by.UpdatedAt.Add(time.Second)
					newer.UpdatedAt = by.UpdatedAt.Add(time.Minute)
					newer.CompletedAt = &newer.UpdatedAt
					p := *newer.PullRequest
					p.Number++
					p.URL = "https://example.invalid/pull/752"
					p.MergeCommit = ""
					newer.PullRequest = &p
					newer.PublishFailure = "confirmation outstanding"
					if kind == "newer replaying publication" {
						newer.Integration = nil
						newer.Status, newer.Phase, newer.CompletedAt = runstate.StatusRunning, runstate.PhaseIntegrating, nil
						newer.WorktreeRemoved, newer.BranchRemoved = false, false
						p.Merged, p.MergeQueued, p.State = false, false, "OPEN"
					}
					if err := f.Store.Create(newer); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "publication held" {
					_, lease, err := f.Store.AdoptRun(context.Background(), by.RunID)
					if err != nil {
						t.Fatal(err)
					}
					release = lease.Release
					defer release()
				}
			}
			testharness.CloseRetirementItem(f)
			if kind == "reopened" {
				tracker := f.Tracker.(*orchestratortest.Tracker)
				tracker.Item.Status = "in_progress"
				tracker.Item.Notes += "Explicitly reopened to complete more work."
			}
			continued := false
			r.Continue = func(context.Context, string, string) (orchestrator.Outcome, error) {
				continued = true
				return orchestrator.Outcome{}, nil
			}
			updates, err := r.ContinueUpdates(context.Background())
			if err != nil || len(updates) == 0 || testharness.LoadRun(t, f.Store, updating.RunID).Retirement != nil {
				t.Fatalf("negative case retired: %+v, %v", updates, err)
			}
			var update orchestrator.UpdateContinuation
			for _, result := range updates {
				if result.Retired {
					t.Fatalf("negative case retired: %+v", result)
				}
				if result.RunID == updating.RunID {
					update = result
				}
			}
			if kind == "reopened" {
				if !continued || update.Failure != "" {
					t.Fatal("an authorized reopening did not retain its continuation")
				}
			} else if continued || update.Failure == "" || update.Finding == nil {
				t.Fatalf("closed refusal was not kept as an item finding: %+v", updates)
			}
			if kind == "no merge" && update.Finding.Mover != readmodel.MoverDevelopmentManager {
				t.Fatalf("owner = %s", update.Finding.Mover)
			}
		})
	}
}
