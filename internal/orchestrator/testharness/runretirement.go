package testharness

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const SupersedingRun = "run-abcdef0123456789abcdef0123456789"

// The later record carries a confirmed merge of the same change through a
// different run's publication. The remote actually contains the merge, while
// the older request remains queued in the forge's answer.
func RecordSupersedingMerge(t *testing.T, f QueuedFixture, old runstate.State) runstate.State {
	t.Helper()
	if err := f.Forge.MergeIntoRemote("main", old.Branch); err != nil {
		t.Fatal(err)
	}
	merge, err := f.Forge.Git("rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	landed := old
	landed.RunID = SupersedingRun
	landed.StartedAt = old.StartedAt.Add(time.Minute)
	landed.UpdatedAt = old.UpdatedAt.Add(time.Hour)
	landed.CompletedAt = &landed.UpdatedAt
	landed.Status = runstate.StatusSucceeded
	landed.Phase = runstate.PhaseComplete
	landed.WorktreeRemoved, landed.BranchRemoved = true, true
	p := *old.PullRequest
	p.Number = 751
	p.URL = "https://example.invalid/pull/751"
	p.Merged, p.MergeQueued, p.State, p.MergeCommit = true, false, "MERGED", merge
	landed.PullRequest = &p
	if err := f.Store.Create(landed); err != nil {
		t.Fatal(err)
	}
	return landed
}

func CloseRetirementItem(f QueuedFixture) {
	tracker := f.Tracker.(*orchestratortest.Tracker)
	tracker.Item.Status = "closed"
	tracker.Closed = true
}

func RetirementReading() publish.CheckReading {
	return publish.CheckReading{Files: []string{"feature.txt"}, BehindBy: 10,
		Failing: []publish.FailedCheck{{Name: "build", Paths: []string{"unrelated.txt"}}}}
}

// This is the observed stale record: a previous sweep already withdrew the
// merge and made the run live, so no publication settlement selects it now.
func UpdatingRetirementFixture(t *testing.T) (QueuedFixture, *orchestratortest.CheckedForge, orchestrator.Reconciler, runstate.State) {
	t.Helper()
	f, forge, _ := QueuedOnProtectedTarget(t)
	forge.Reading = RetirementReading()
	r := f.Sweep(t, forge, true)
	if results, err := r.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != orchestrator.ActionUpdating {
		t.Fatalf("prepare replay = %+v, %v", results, err)
	}
	return f, forge, r, LoadRun(t, f.Store, PipelineRunID)
}
