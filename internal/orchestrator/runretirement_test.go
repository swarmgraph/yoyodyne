package orchestrator

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const supersedingRun = "run-abcdef0123456789abcdef0123456789"

// The later record carries a confirmed merge of the same change through a
// different run's publication. The remote actually contains the merge, while
// the older request remains queued in the forge's answer.
func recordSupersedingMerge(t *testing.T, f queuedFixture, old runstate.State) runstate.State {
	t.Helper()
	if err := f.forge.(*orchestratortest.Forge).MergeIntoRemote("main", old.Branch); err != nil {
		t.Fatal(err)
	}
	merge, err := f.forge.Git("rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	landed := old
	landed.RunID = supersedingRun
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
	if err := f.store.Create(landed); err != nil {
		t.Fatal(err)
	}
	return landed
}

func closeRetirementItem(f queuedFixture) {
	tracker := f.tracker.(*orchestratortest.Tracker)
	tracker.Item.Status = "closed"
	tracker.Closed = true
}

func retirementReading() publish.CheckReading {
	return publish.CheckReading{Files: []string{"feature.txt"}, BehindBy: 10,
		Failing: []publish.FailedCheck{{Name: "build", Paths: []string{"unrelated.txt"}}}}
}

func assertRunRetired(t *testing.T, f queuedFixture, prior, by runstate.State) runstate.State {
	t.Helper()
	saved := loadRun(t, f.store, prior.RunID)
	if saved.Retirement == nil || saved.Retirement.RunID != by.RunID || saved.Retirement.Commit != by.PullRequest.MergeCommit || saved.Retirement.NotedAt == nil {
		t.Fatalf("retirement = %+v", saved.Retirement)
	}
	if saved.Status != runstate.StatusCancelled || saved.Outstanding() || saved.AwaitingForge() || saved.HoldsDeveloperSlot() {
		t.Fatalf("retired run still holds work: %+v", saved)
	}
	if saved.WorktreePath != prior.WorktreePath || saved.Branch != prior.Branch || saved.ProviderSessionID != prior.ProviderSessionID ||
		saved.WorktreeRemoved != prior.WorktreeRemoved || saved.BranchRemoved != prior.BranchRemoved || !reflect.DeepEqual(saved.IntegrationResumptions, prior.IntegrationResumptions) {
		t.Fatal("retirement changed the run's artifacts or execution history")
	}
	if _, err := os.Stat(prior.WorktreePath); err != nil || publishedCommit(t, f.repository, prior.Branch) == "" {
		t.Fatalf("artifacts lost: %v", err)
	}
	if f.tracker.Record().Item.Status != "closed" || f.tracker.(*orchestratortest.Tracker).Reopened || f.tracker.Record().Blocked {
		t.Fatal("retirement changed the item's closed status")
	}
	for _, want := range []string{prior.RunID, by.RunID, by.PullRequest.MergeCommit, "#751"} {
		if !strings.Contains(f.tracker.Record().Notes, want) {
			t.Fatalf("retirement note omits %s", want)
		}
	}
	occupied, err := occupiedItems(f.store)
	if err != nil || len(occupied) != 0 {
		t.Fatalf("predicted-file holders = %+v, %v", occupied, err)
	}
	guard := newInFlight()
	for _, state := range occupied {
		guard.take(beads.WorkItem{ID: state.WorkItemID, Description: "internal/orchestrator/queuedchecks.go"}, state.RunID)
	}
	if conflict, held := guard.against(beads.WorkItem{ID: "next-item", Description: "internal/orchestrator/queuedchecks.go"}); held {
		t.Fatalf("retired run still reserves a file: %+v", conflict)
	}
	lease, err := f.store.LeasePromotion(context.Background(), "main")
	if err != nil {
		t.Fatalf("integration reservation held: %v", err)
	}
	lease.Release()
	return saved
}

func TestAQueuedReplayRetiresARunWhoseItemMergedThroughAnotherRun(t *testing.T) {
	t.Parallel()
	f, forge, _ := queuedOnProtectedTarget(t)
	old := loadRun(t, f.store, pipelineRunID)
	by := recordSupersedingMerge(t, f, old)
	closeRetirementItem(f)
	forge.reading = retirementReading()
	r := f.sweep(t, forge, true)
	results, err := r.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionRetired || results[0].Failure != "" {
		t.Fatalf("reconcile = %+v, %v", results, err)
	}
	assertRunRetired(t, f, old, by)
	if len(forge.withdrawn) != 0 {
		t.Fatal("an obsolete run was put back at its promotion")
	}
	notes := len(f.tracker.Record().NoteRecords)
	for pass := 0; pass < 2; pass++ {
		if got, err := r.Reconcile(context.Background()); err != nil || len(got) != 0 {
			t.Fatalf("later pass = %+v, %v", got, err)
		}
		if got, err := r.ContinueUpdates(context.Background()); err != nil || len(got) != 0 {
			t.Fatalf("later continuations = %+v, %v", got, err)
		}
		if _, err := r.Converge(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	assertRunRetired(t, f, old, by)
	if len(f.tracker.Record().NoteRecords) != notes {
		t.Fatal("a later pass announced retirement again")
	}
}

// This is the observed stale record: a previous sweep already withdrew the
// merge and made the run live, so no publication settlement selects it now.
func updatingRetirementFixture(t *testing.T) (queuedFixture, *checkedForge, Reconciler, runstate.State) {
	t.Helper()
	f, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = retirementReading()
	r := f.sweep(t, forge, true)
	if results, err := r.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != ActionUpdating {
		t.Fatalf("prepare replay = %+v, %v", results, err)
	}
	return f, forge, r, loadRun(t, f.store, pipelineRunID)
}

func TestAnAlreadySelectedQueuedContinuationRechecksItsCompletedItem(t *testing.T) {
	t.Parallel()
	f, _, r, updating := updatingRetirementFixture(t)
	// Keep the original integration for the superseding publication, which the
	// queued update has already cleared from its own current revision.
	original := updating
	original.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: updating.PullRequest.HeadCommit, TargetCommit: updating.PullRequest.HeadCommit, PreviousTargetCommit: updating.BaseCommit, ThroughPullRequest: true}
	continuePipeline := r.Continue
	r.Continue = func(ctx context.Context, item, run string) (Outcome, error) {
		recordSupersedingMerge(t, f, original)
		closeRetirementItem(f)
		return continuePipeline(ctx, item, run)
	}
	updates, err := r.ContinueUpdates(context.Background())
	if err != nil || len(updates) != 1 || !updates[0].Retired || updates[0].Failure != "" {
		t.Fatalf("updates = %+v, %v", updates, err)
	}
	by := loadRun(t, f.store, supersedingRun)
	assertRunRetired(t, f, updating, by)
}

func TestQueuedContinuationRetirementDoesNotGuessFromClosedStatus(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"no merge", "unsettled merge", "newer unsettled merge", "reopened", "wrong target", "evidence landing", "publication held"} {
		t.Run(kind, func(t *testing.T) {
			f, _, r, updating := updatingRetirementFixture(t)
			original := updating
			original.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: updating.PullRequest.HeadCommit, TargetCommit: updating.PullRequest.HeadCommit, PreviousTargetCommit: updating.BaseCommit, ThroughPullRequest: true}
			var release func() error
			if kind != "no merge" {
				by := recordSupersedingMerge(t, f, original)
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
				if err := f.store.Save(by); err != nil {
					t.Fatal(err)
				}
				if kind == "newer unsettled merge" {
					newer := by
					newer.RunID = "run-ffffffffffffffffffffffffffffffff"
					newer.UpdatedAt = by.UpdatedAt.Add(time.Minute)
					newer.CompletedAt = &newer.UpdatedAt
					p := *newer.PullRequest
					p.MergeCommit = ""
					newer.PullRequest = &p
					newer.PublishFailure = "confirmation outstanding"
					if err := f.store.Create(newer); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "publication held" {
					_, lease, err := f.store.AdoptRun(context.Background(), by.RunID)
					if err != nil {
						t.Fatal(err)
					}
					release = lease.Release
					defer release()
				}
			}
			closeRetirementItem(f)
			if kind == "reopened" {
				tracker := f.tracker.(*orchestratortest.Tracker)
				tracker.Item.Status = "in_progress"
				tracker.Item.Notes += "Explicitly reopened to complete more work."
			}
			continued := false
			r.Continue = func(context.Context, string, string) (Outcome, error) { continued = true; return Outcome{}, nil }
			updates, err := r.ContinueUpdates(context.Background())
			if err != nil || len(updates) != 1 || updates[0].Retired || loadRun(t, f.store, updating.RunID).Retirement != nil {
				t.Fatalf("negative case retired: %+v, %v", updates, err)
			}
			if kind == "reopened" {
				if !continued || updates[0].Failure != "" {
					t.Fatal("an authorized reopening did not retain its continuation")
				}
			} else if continued || updates[0].Failure == "" || updates[0].Finding == nil {
				t.Fatalf("closed refusal was not kept as an item finding: %+v", updates)
			}
			if kind == "no merge" && updates[0].Finding.Mover != readmodel.MoverDevelopmentManager {
				t.Fatalf("owner = %s", updates[0].Finding.Mover)
			}
		})
	}
}

func TestRetirementSettlesOtherItemsOnTheSamePass(t *testing.T) {
	t.Parallel()
	f, forge, _ := queuedOnProtectedTarget(t)
	old := loadRun(t, f.store, pipelineRunID)
	by := recordSupersedingMerge(t, f, old)
	closeRetirementItem(f)
	forge.reading = retirementReading()
	other := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: "run-ffffffffffffffffffffffffffffffff",
		ProductID: old.ProductID, RepositoryID: old.RepositoryID, WorkItemID: "other-item",
		WorkItemTitle: "Other work", Backend: old.Backend, Status: runstate.StatusPending,
		StartedAt: old.StartedAt, UpdatedAt: old.UpdatedAt,
	}
	f.tracker.(*orchestratortest.Tracker).HoldsItem(beads.WorkItem{ID: other.WorkItemID, Title: other.WorkItemTitle, Status: "closed"})
	if err := f.store.Create(other); err != nil {
		t.Fatal(err)
	}
	r := f.sweep(t, forge, true)
	results, err := r.Reconcile(context.Background())
	if err != nil || len(results) != 2 {
		t.Fatalf("pass = %+v, %v", results, err)
	}
	for _, result := range results {
		want := ActionFailed // the interrupted other run built nothing
		if result.RunID == old.RunID {
			want = ActionRetired
		}
		if result.Action != want || result.Failure != "" {
			t.Fatalf("settlement = %+v, want %s", result, want)
		}
	}
	assertRunRetired(t, f, old, by)
}

func TestRetirementNoteDeliverySurvivesARefusedMarkerAndRestart(t *testing.T) {
	t.Parallel()
	f, forge, _ := queuedOnProtectedTarget(t)
	old := loadRun(t, f.store, pipelineRunID)
	by := recordSupersedingMerge(t, f, old)
	closeRetirementItem(f)
	forge.reading = retirementReading()
	r := f.sweep(t, forge, true)
	tracker := &durableFindingNotes{Tracker: f.tracker.(*orchestratortest.Tracker)}
	r.Tracker = tracker
	store := &refusedRetirementMarker{ReconcileStore: f.store, refuse: true}
	r.Store = store
	notes := len(tracker.NoteRecords)
	for pass := 0; pass < 2; pass++ {
		results, err := r.Reconcile(context.Background())
		if err != nil || len(results) != 1 || !strings.Contains(results[0].Failure, "retirement marker refused") {
			t.Fatalf("refused marker = %+v, %v", results, err)
		}
	}
	if saved := loadRun(t, f.store, old.RunID); saved.Retirement == nil || saved.HoldsDeveloperSlot() {
		t.Fatal("note refusal lost retirement or held its slot")
	}
	retirementNotes := 0
	for _, note := range tracker.NoteRecords[notes:] {
		if strings.HasPrefix(note, "Yoyodyne retired a run") {
			retirementNotes++
		}
	}
	if retirementNotes != 1 {
		t.Fatalf("retirement announced %d times", retirementNotes)
	}
	store.refuse = false
	// A fresh Store sees the pending delivery without any process-local state.
	restarted, err := runstate.NewStore(strings.TrimSuffix(f.store.Root(), "/products/yoyodyne/runs"), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	r.Store = restarted
	if _, err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertRunRetired(t, f, old, by)
}

type refusedRetirementMarker struct {
	ReconcileStore
	refuse bool
}

func (s *refusedRetirementMarker) Save(state runstate.State) error {
	if s.refuse && state.Retirement != nil && state.Retirement.NotedAt != nil {
		return errors.New("retirement marker refused")
	}
	return s.ReconcileStore.Save(state)
}
