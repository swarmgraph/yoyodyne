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
	if free, err := (Reconciler{Store: f.store, Capacity: 1}).slotFree(); err != nil || !free {
		t.Fatalf("queued integration capacity still reserved: %t, %v", free, err)
	}
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

func TestRetirementAlsoSettlesAnExpiredProviderWait(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"confirmed merge", "confirmed merge without a request", "earlier merge without a request", "no merge", "unsettled merge", "reopened"} {
		t.Run(kind, func(t *testing.T) {
			f, _, r, prior := updatingRetirementFixture(t)
			original := prior
			original.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: prior.PullRequest.HeadCommit, TargetCommit: prior.PullRequest.HeadCommit, PreviousTargetCommit: prior.BaseCommit, ThroughPullRequest: true}
			var by runstate.State
			if kind != "no merge" {
				by = recordSupersedingMerge(t, f, original)
				if kind == "unsettled merge" {
					by.PullRequest.MergeCommit = ""
					by.PublishFailure = "confirmation outstanding"
					if err := f.store.Save(by); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "earlier merge without a request" {
					by.StartedAt = prior.StartedAt.Add(-time.Minute)
					by.UpdatedAt = prior.StartedAt
					by.CompletedAt = &by.UpdatedAt
					if err := f.store.Save(by); err != nil {
						t.Fatal(err)
					}
				}
			}
			if strings.Contains(kind, "without a request") {
				prior.PullRequest = nil
			}
			deadline := r.clock().Now().Add(-time.Minute)
			prior.Phase = runstate.PhaseDeveloping
			prior.UsageLimitResetsAt, prior.PauseCause = &deadline, runstate.PauseUsageLimit
			prior.RedeployStop = &runstate.RedeployStop{At: deadline, Phase: prior.Phase, BoundSeconds: 900, SessionID: "watch-0123456789abcdef"}
			if err := f.store.Save(prior); err != nil {
				t.Fatal(err)
			}
			closeRetirementItem(f)
			if kind == "reopened" {
				f.tracker.(*orchestratortest.Tracker).Item.Status = "in_progress"
			}
			continued := false
			r.Continue = func(context.Context, string, string) (Outcome, error) { continued = true; return Outcome{}, nil }
			results, err := r.ContinueWaits(context.Background())
			if err != nil || len(results) != 1 {
				t.Fatalf("wait continuation = %+v, %v", results, err)
			}
			result := results[0]
			if kind == "confirmed merge" || kind == "confirmed merge without a request" {
				if continued || result.Failure != "" || result.Outcome == nil || result.Outcome.Retirement == nil {
					t.Fatalf("expired wait did not retire: %+v", result)
				}
				saved := assertRunRetired(t, f, prior, by)
				if saved.UsageLimitResetsAt != nil || saved.RedeployStop != nil || !strings.Contains(saved.Retirement.PriorWait, deadline.Format(time.RFC3339Nano)) || !strings.Contains(saved.Retirement.PriorWait, prior.RedeployStop.SessionID) {
					t.Fatal("retirement lost the old wait's history or still promised continuation")
				}
				if again, err := r.ContinueWaits(context.Background()); err != nil || len(again) != 0 {
					t.Fatalf("retirement announced again: %+v, %v", again, err)
				}
			} else {
				if loadRun(t, f.store, prior.RunID).Retirement != nil {
					t.Fatal("a wait was retired without applicable merge evidence")
				}
				if kind == "reopened" {
					if !continued || result.Failure != "" {
						t.Fatalf("reopened wait not continued: %+v", result)
					}
				} else if continued || result.Failure == "" || result.Finding == nil {
					t.Fatalf("closed refusal did not leave a per-item finding: %+v", result)
				}
			}
		})
	}
}

func TestRetirementKeepsANewerRequestWhenAnOlderRequestCompletesAfterItStarted(t *testing.T) {
	t.Parallel()
	f, _, r, newer := updatingRetirementFixture(t)
	original := newer
	original.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: newer.PullRequest.HeadCommit, TargetCommit: newer.PullRequest.HeadCommit, PreviousTargetCommit: newer.BaseCommit, ThroughPullRequest: true}
	by := recordSupersedingMerge(t, f, original)
	by.StartedAt = newer.StartedAt.Add(-time.Minute)
	if err := f.store.Save(by); err != nil {
		t.Fatal(err)
	}
	p := *newer.PullRequest
	p.Number = by.PullRequest.Number + 1
	p.URL = "https://example.invalid/pull/752"
	newer.PullRequest = &p
	if err := f.store.Save(newer); err != nil {
		t.Fatal(err)
	}
	if !by.CompletedAt.After(newer.StartedAt) {
		t.Fatal("the older request must finish after the newer run starts")
	}
	closeRetirementItem(f)
	r.Continue = func(context.Context, string, string) (Outcome, error) {
		t.Fatal("a closed item's newer request must not be resumed")
		return Outcome{}, nil
	}
	results, err := r.ContinueUpdates(context.Background())
	if err != nil || len(results) != 1 {
		t.Fatalf("continuation pass = %+v, %v", results, err)
	}
	result := results[0]
	saved := loadRun(t, f.store, newer.RunID)
	if result.Retired || saved.Retirement != nil || result.Failure == "" || result.Finding == nil || result.Finding.Mover != readmodel.MoverDevelopmentManager {
		t.Fatalf("the older request did not leave an owned per-item finding: %+v", result)
	}
	if !saved.HoldsDeveloperSlot() || saved.Status != newer.Status || saved.Phase != newer.Phase || saved.Branch != newer.Branch || saved.WorktreePath != newer.WorktreePath || saved.ProviderSessionID != newer.ProviderSessionID {
		t.Fatal("the newer request's run was changed by the older merge")
	}
	if f.tracker.Record().Item.Status != "closed" || f.tracker.(*orchestratortest.Tracker).Reopened {
		t.Fatal("the closed item was reopened")
	}
}

func TestQueuedContinuationRetirementDoesNotGuessFromClosedStatus(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"no merge", "unsettled merge", "newer unsettled merge", "newer replaying publication", "reopened", "wrong target", "evidence landing", "publication held"} {
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
			if err != nil || len(updates) == 0 || loadRun(t, f.store, updating.RunID).Retirement != nil {
				t.Fatalf("negative case retired: %+v, %v", updates, err)
			}
			var update UpdateContinuation
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

func TestRetirementIgnoresProgressAfterTheMergeOnTwoOlderPublications(t *testing.T) {
	t.Parallel()
	f, _, r, old := updatingRetirementFixture(t)
	original := old
	original.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: old.PullRequest.HeadCommit, TargetCommit: old.PullRequest.HeadCommit, PreviousTargetCommit: old.BaseCommit, ThroughPullRequest: true}
	by := recordSupersedingMerge(t, f, original)
	// Both requests were opened before the merge, but their obsolete runs
	// recorded replay progress afterwards. Neither update is a new publication.
	old.UpdatedAt = by.UpdatedAt.Add(time.Hour)
	if err := f.store.Save(old); err != nil {
		t.Fatal(err)
	}
	other := old
	other.RunID = "run-ffffffffffffffffffffffffffffffff"
	other.UpdatedAt = old.UpdatedAt.Add(time.Minute)
	p := *other.PullRequest
	p.Number++
	p.URL = "https://example.invalid/pull/2"
	other.PullRequest = &p
	if err := f.store.Create(other); err != nil {
		t.Fatal(err)
	}
	closeRetirementItem(f)
	r.Clock = fixedClock{at: by.UpdatedAt.Add(time.Hour)}
	results, err := r.ContinueUpdates(context.Background())
	if err != nil || len(results) != 2 {
		t.Fatalf("obsolete continuations = %+v, %v", results, err)
	}
	for _, result := range results {
		if !result.Retired || result.Failure != "" {
			t.Fatalf("one retirement masked the real merge: %+v", result)
		}
	}
	assertRunRetired(t, f, old, by)
	assertRunRetired(t, f, other, by)
	notes := len(f.tracker.Record().NoteRecords)
	if again, err := r.ContinueUpdates(context.Background()); err != nil || len(again) != 0 {
		t.Fatalf("retirements announced again: %+v, %v", again, err)
	}
	if len(f.tracker.Record().NoteRecords) != notes {
		t.Fatal("a later pass announced retirement again")
	}
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
	restarted, err := runstate.NewStore(stateRootOf(f.store.Root()), "yoyodyne")
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
