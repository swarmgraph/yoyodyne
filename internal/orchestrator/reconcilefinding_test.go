package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestReconcileRetriesAnUndeliveredFindingAndClearsItWhenTheForgeAnswers(t *testing.T) {
	t.Parallel()
	fixture, state := newPublicationFixture(t)
	tracker := &refuseSettlementNoteOnce{WorkTracker: fixture.tracker}
	forge := &answeringForge{err: errors.New("the forge is unreachable")}
	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, fixture.repository, fixture.worktreeRoot), Store: fixture.store, Publisher: forge}
	notes := len(fixture.tracker.NoteRecords)
	first, err := reconciler.RefreshPublications(context.Background())
	if err != nil || len(first) != 1 || first[0].Finding == nil || first[0].FindingProblem == "" {
		t.Fatalf("first refresh = %#v, %v, want the refusal saved despite failed note delivery", first, err)
	}
	if saved := loadRun(t, fixture.store, state.RunID); saved.ReconcileFindings == nil || !saved.ReconcileFindings[0].Pending {
		t.Fatal("the undelivered finding was lost")
	}
	for pass := 0; pass < 2; pass++ {
		results, err := reconciler.RefreshPublications(context.Background())
		if err != nil || len(results) != 1 || results[0].Finding == nil || results[0].FindingProblem != "" {
			t.Fatalf("refresh = %#v, %v", results, err)
		}
	}
	if len(fixture.tracker.NoteRecords) != notes+1 {
		t.Fatalf("finding note count = %d, want one successful delivery", len(fixture.tracker.NoteRecords)-notes)
	}
	if saved := loadRun(t, fixture.store, state.RunID); saved.ReconcileFindings == nil || saved.ReconcileFindings[0].Pending {
		t.Fatal("delivered finding still pending")
	}
	forge.err = nil
	forge.answer = publish.PullRequest{Number: state.PullRequest.Number, State: "MERGED", Merged: true}
	results, err := reconciler.RefreshPublications(context.Background())
	if err != nil || len(results) != 1 || !results[0].Updated || results[0].Finding != nil {
		t.Fatalf("working refresh = %#v, %v", results, err)
	}
	if saved := loadRun(t, fixture.store, state.RunID); saved.ReconcileFindings != nil {
		t.Fatal("resolved refusal still shown as a finding")
	}
}

func TestReconcileClearsARefreshFindingWhenTheRunSettlementRecordsTheMerge(t *testing.T) {
	t.Parallel()
	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	reconciler := fixture.reconciler(t)
	reconciler.Tracker = &refuseSettlementNoteOnce{WorkTracker: fixture.tracker}
	// A request can have a refresh finding from before its run was re-armed.
	// The run now owes its queued merge, so refresh no longer selects it.
	finding, problem := reconciler.recordReconcileFinding(context.Background(), outcome.RunID, runstate.ReconcileRefresh, "forge answer unreadable")
	if finding == nil || problem == "" {
		t.Fatalf("finding = %+v, problem = %q, want a pending refresh note", finding, problem)
	}
	fixture.forge.PerformQueuedMerge(t)
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionCompleted || results[0].FindingProblem != "" {
		t.Fatalf("settlement = %+v, %v", results, err)
	}
	if saved := loadRun(t, fixture.store, results[0].RunID); !saved.PullRequest.Merged || len(saved.ReconcileFindings) != 0 {
		t.Fatalf("settled publication still has findings: %+v", saved.ReconcileFindings)
	}
	findingNotes := 0
	for _, note := range fixture.tracker.Record().NoteRecords {
		if strings.HasPrefix(note, "Settlement finding:") {
			findingNotes++
		}
	}
	if findingNotes != 1 {
		t.Fatalf("finding notes = %d, want the pending refresh note delivered once", findingNotes)
	}
}

type refuseSettlementNoteOnce struct {
	WorkTracker
	refused bool
}

func (r *refuseSettlementNoteOnce) RecordOutcome(ctx context.Context, id, note string) (beads.WorkItem, error) {
	if !r.refused && strings.HasPrefix(note, "Settlement finding:") {
		r.refused = true
		return beads.WorkItem{}, errors.New("settlement finding note refused")
	}
	return r.WorkTracker.RecordOutcome(ctx, id, note)
}

func TestReconcileRefreshesOtherItemsWhenOneForgeAnswerCannotBeRead(t *testing.T) {
	t.Parallel()
	fixture, first := newPublicationFixture(t)
	other := first
	other.RunID = "run-ffffffffffffffffffffffffffffffff"
	other.WorkItemID = "yoyodyne-other"
	other.Branch += "-other"
	other.WorktreePath += "-other"
	published := *first.PullRequest
	published.Number = 2
	published.Branch = other.Branch
	other.PullRequest = &published
	if err := fixture.store.Create(other); err != nil {
		t.Fatal(err)
	}
	forge := &unreadableOneRequest{branch: first.Branch, ReconcilePullRequests: publicationAnswers{other.Branch: {Number: 2, State: "MERGED", Merged: true}}}
	refreshed := fixture.refresh(t, forge)
	if len(refreshed) != 2 {
		t.Fatalf("refresh = %#v", refreshed)
	}
	for _, result := range refreshed {
		if result.WorkItemID == first.WorkItemID {
			if result.Finding == nil || !strings.Contains(result.Finding.What(), "forge answer unreadable") || !strings.Contains(result.Finding.Whose(), "restore forge access") {
				t.Fatalf("refusal = %#v", result)
			}
		} else if !result.Updated || !result.Merged || result.Failure != "" {
			t.Fatalf("other refresh = %#v", result)
		}
	}
	if saved := loadRun(t, fixture.store, other.RunID); !saved.PullRequest.Merged {
		t.Fatal("other item was not refreshed")
	}
	if saved := loadRun(t, fixture.store, first.RunID); saved.ReconcileFindings == nil || saved.ReconcileFindings[0].Pending {
		t.Fatal("unreadable answer not recorded on its item")
	}
}

type unreadableOneRequest struct {
	ReconcilePullRequests
	branch string
}

func (f *unreadableOneRequest) State(ctx context.Context, branch string) (publish.PullRequest, error) {
	if branch == f.branch {
		return publish.PullRequest{}, errors.New("forge answer unreadable")
	}
	return f.ReconcilePullRequests.State(ctx, branch)
}

// Listing the pass's state is a different failure from one unreadable request:
// there is no item the pass can safely select and settle.
func TestReconcileFailsWhenThePassCannotReadItsRuns(t *testing.T) {
	t.Parallel()
	fixture := newQueuedFixture(t)
	reconciler := fixture.reconciler(t)
	reconciler.Store = unreadablePass{ReconcileStore: fixture.store}
	if results, err := reconciler.Reconcile(context.Background()); err == nil || len(results) != 0 || !strings.Contains(err.Error(), "state unreadable") {
		t.Fatalf("Reconcile() = %#v, %v, want an unreadable pass failure", results, err)
	}
}

type unreadablePass struct{ ReconcileStore }

func (unreadablePass) Outstanding() ([]runstate.State, error) {
	return nil, errors.New("state unreadable")
}

func TestReconcileKeepsSeparateRefusalsOnTheSameItemWithoutRepeatingTheirNotes(t *testing.T) {
	t.Parallel()
	fixture, state := newPublicationFixture(t)
	reconciler := Reconciler{Tracker: fixture.tracker, Worktrees: newObserver(t, fixture.repository, fixture.worktreeRoot), Store: fixture.store}
	problems := map[runstate.ReconcileStep]string{
		runstate.ReconcileRefresh: "ask the forge: answer unreadable",
		runstate.ReconcileBranch:  "observe leftover branch: repository unreadable",
	}
	notes := len(fixture.tracker.NoteRecords)
	for pass := 0; pass < 2; pass++ {
		for step, problem := range problems {
			finding, failure := reconciler.recordReconcileFinding(context.Background(), state.RunID, step, problem)
			if finding == nil || failure != "" {
				t.Fatalf("finding = %+v, failure = %q", finding, failure)
			}
		}
	}
	if len(fixture.tracker.NoteRecords) != notes+2 {
		t.Fatalf("finding notes = %d, want two", len(fixture.tracker.NoteRecords)-notes)
	}
	if saved := loadRun(t, fixture.store, state.RunID); len(saved.ReconcileFindings) != 2 {
		t.Fatalf("saved findings = %+v", saved.ReconcileFindings)
	}
	if _, failure := reconciler.recordReconcileFinding(context.Background(), state.RunID, runstate.ReconcileRefresh, ""); failure != "" {
		t.Fatal(failure)
	}
	if saved := loadRun(t, fixture.store, state.RunID); len(saved.ReconcileFindings) != 1 || saved.ReconcileFindings[0].Step != runstate.ReconcileBranch {
		t.Fatalf("clearing one refusal removed the other: %+v", saved.ReconcileFindings)
	}
}

func TestReconcileDoesNotRepeatAFindingWhoseDeliveryMarkerCannotBeSaved(t *testing.T) {
	t.Parallel()
	fixture, state := newPublicationFixture(t)
	tracker := &durableFindingNotes{Tracker: fixture.tracker}
	store := &refusedFindingDeliveryMarker{ReconcileStore: fixture.store, refuse: true}
	reconciler := Reconciler{Tracker: tracker, Store: store, Worktrees: newObserver(t, fixture.repository, fixture.worktreeRoot), Publisher: &answeringForge{err: errors.New("forge unreadable")}}
	notes := len(fixture.tracker.NoteRecords)
	for pass := 0; pass < 2; pass++ {
		results, err := reconciler.RefreshPublications(context.Background())
		if err != nil || len(results) != 1 || !strings.Contains(results[0].FindingProblem, "marker save refused") {
			t.Fatalf("refresh = %#v, %v", results, err)
		}
	}
	if len(fixture.tracker.NoteRecords) != notes+1 {
		t.Fatal("the same delivered finding was announced again after its marker save failed")
	}
	store.refuse = false
	if _, err := reconciler.RefreshPublications(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saved := loadRun(t, fixture.store, state.RunID); len(saved.ReconcileFindings) != 1 || saved.ReconcileFindings[0].Pending {
		t.Fatalf("finding delivery still pending: %+v", saved.ReconcileFindings)
	}
	if len(fixture.tracker.NoteRecords) != notes+1 {
		t.Fatal("recording the marker delivered the note again")
	}
}

type durableFindingNotes struct{ *orchestratortest.Tracker }

func (t *durableFindingNotes) RecordOutcome(ctx context.Context, id, note string) (beads.WorkItem, error) {
	item, err := t.Tracker.RecordOutcome(ctx, id, note)
	if err == nil {
		t.Item.Notes += "\n" + note
	}
	return item, err
}

type refusedFindingDeliveryMarker struct {
	ReconcileStore
	refuse bool
}

func (s *refusedFindingDeliveryMarker) Save(state runstate.State) error {
	if s.refuse {
		for _, finding := range state.ReconcileFindings {
			if !finding.Pending {
				return errors.New("marker save refused")
			}
		}
	}
	return s.ReconcileStore.Save(state)
}
