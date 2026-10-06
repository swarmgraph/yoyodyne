package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The record of a failed run's publication used to be whatever the forge last
// said before the run died, for good: nothing re-asked, so a request somebody
// merged afterwards stayed recorded open and unmerged, and everything reading it
// — the docket, the sweep over the requests the harness left open, the status
// surfaces — read that instead of the truth.
func TestReconcileRecordsThatTheForgeMergedAFailedRunsPullRequest(t *testing.T) {
	t.Parallel()

	fixture, before := newPublicationFixture(t)
	forge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{
		Number: before.PullRequest.Number,
		URL:    before.PullRequest.URL,
		State:  "MERGED",
		Merged: true,
	}}
	calls := len(fixture.tracker.Calls)

	refreshed := fixture.refresh(t, forge)
	if len(refreshed) != 1 || !refreshed[0].Updated || refreshed[0].Failure != "" {
		t.Fatalf("refresh = %#v, want the one stale record corrected", refreshed)
	}
	if refreshed[0].Recorded != "OPEN" || refreshed[0].State != "MERGED" || !refreshed[0].Merged {
		t.Fatalf("refresh = %#v, want the disagreement it settled reported from both sides", refreshed[0])
	}
	if len(forge.Heads) != 1 || forge.Heads[0] != before.PullRequest.Branch {
		t.Fatalf("forge asked about %v, want the branch the run published", forge.Heads)
	}

	after := loadRun(t, fixture.store, before.RunID)
	if !after.PullRequest.Merged || after.PullRequest.State != "MERGED" {
		t.Fatalf("recorded publication = %#v, want what the forge says", after.PullRequest)
	}
	// The publication record is the only thing this touches. The run is still the
	// failed run it was, nothing was promoted on the strength of a merge the
	// harness did not make, and the item is untouched: what to do about a merge
	// that landed outside the harness is somebody else's decision.
	if after.Status != before.Status || after.Integration != nil || after.Blocker != before.Blocker {
		t.Fatalf("run = %#v, want only its publication record changed", after)
	}
	if len(fixture.tracker.Calls) != calls {
		t.Fatalf("the refresh made %v on the work item", fixture.tracker.Calls[calls:])
	}
}

// Merged is the one answer a forge does not take back, so it is the answer that
// ends the asking. Without that, a sweep over a long history would re-ask about
// every publication it ever settled, forever.
func TestReconcileStopsAskingAboutAPublicationTheForgeMerged(t *testing.T) {
	t.Parallel()

	fixture, before := newPublicationFixture(t)
	forge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{
		Number: before.PullRequest.Number,
		URL:    before.PullRequest.URL,
		State:  "MERGED",
		Merged: true,
	}}
	if refreshed := fixture.refresh(t, forge); len(refreshed) != 1 || !refreshed[0].Updated {
		t.Fatalf("first refresh = %#v, want the record corrected", refreshed)
	}

	if again := fixture.refresh(t, forge); len(again) != 0 {
		t.Fatalf("second refresh = %#v, want a settled publication left alone", again)
	}
	if forge.Asked != 1 {
		t.Fatalf("the forge was asked %d time(s), want the settled question asked once", forge.Asked)
	}
}

// The three answers a forge gives about a request, against a record that says
// it was open. Two of them are news and one is not, and a record that already
// agrees is never rewritten: a sweep over a repository whose records are true
// writes nothing at all.
func TestReconcileRecordsEachAnswerTheForgeGivesAboutAPublication(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		answer      publish.PullRequest
		wantUpdated bool
		wantState   string
		wantMerged  bool
	}{
		{
			name:        "the forge merged it",
			answer:      publish.PullRequest{State: "MERGED", Merged: true},
			wantUpdated: true, wantState: "MERGED", wantMerged: true,
		},
		{
			name:        "somebody closed it unmerged",
			answer:      publish.PullRequest{State: "CLOSED"},
			wantUpdated: true, wantState: "CLOSED",
		},
		{
			name:      "it is still open",
			answer:    publish.PullRequest{State: "OPEN"},
			wantState: "OPEN",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture, before := newPublicationFixture(t)
			answer := test.answer
			answer.Number = before.PullRequest.Number
			answer.URL = before.PullRequest.URL

			refreshed := fixture.refresh(t, &orchestratortest.AnsweringForge{Answer: answer})
			if len(refreshed) != 1 || refreshed[0].Failure != "" || refreshed[0].Kept != "" {
				t.Fatalf("refresh = %#v, want the record asked about and answered", refreshed)
			}
			if refreshed[0].Updated != test.wantUpdated {
				t.Fatalf("refresh = %#v, want updated = %t", refreshed[0], test.wantUpdated)
			}
			after := loadRun(t, fixture.store, before.RunID)
			if after.PullRequest.State != test.wantState || after.PullRequest.Merged != test.wantMerged {
				t.Fatalf("recorded publication = %#v, want %q merged = %t", after.PullRequest, test.wantState, test.wantMerged)
			}
			if !test.wantUpdated && !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("a record the forge agreed with was rewritten at %s, was %s", after.UpdatedAt, before.UpdatedAt)
			}
		})
	}
}

// The forge is asked about the branch, which is the durable handle a published
// run keeps on its request. Another request answering for that branch is a
// publication this record was never about, and taking its state would replace a
// stale record with a wrong one.
func TestReconcileLeavesARecordAloneWhenTheForgeAnswersForAnotherRequest(t *testing.T) {
	t.Parallel()

	fixture, before := newPublicationFixture(t)
	forge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{
		Number: before.PullRequest.Number + 41,
		URL:    "https://example.invalid/pull/99",
		State:  "MERGED",
		Merged: true,
	}}

	refreshed := fixture.refresh(t, forge)
	// It is kept rather than failed: the forge answered, and a branch carrying
	// some other request is a fact to report rather than a sweep that broke.
	if len(refreshed) != 1 || refreshed[0].Updated || refreshed[0].Failure != "" {
		t.Fatalf("refresh = %#v, want the record kept without failing the sweep", refreshed)
	}
	if !strings.Contains(refreshed[0].Kept, before.PullRequest.Branch) {
		t.Fatalf("kept = %q, want the branch the two requests disagree about", refreshed[0].Kept)
	}
	after := loadRun(t, fixture.store, before.RunID)
	if after.PullRequest.Merged || after.PullRequest.State != before.PullRequest.State {
		t.Fatalf("recorded publication = %#v, want the other request's state kept out of it", after.PullRequest)
	}
}

// A forge that cannot be reached leaves the record exactly as it stands and says
// so. The sweep itself does not fail, because the next one asks the same
// question again.
func TestReconcileReportsAPublicationTheForgeCouldNotBeAskedAbout(t *testing.T) {
	t.Parallel()

	fixture, before := newPublicationFixture(t)
	forge := &orchestratortest.AnsweringForge{Err: errors.New("the forge is unreachable")}

	refreshed := fixture.refresh(t, forge)
	if len(refreshed) != 1 || refreshed[0].Updated {
		t.Fatalf("refresh = %#v, want the record left where it stands", refreshed)
	}
	if !strings.Contains(refreshed[0].Failure, "the forge is unreachable") {
		t.Fatalf("failure = %q, want what stopped the forge being asked", refreshed[0].Failure)
	}
	after := loadRun(t, fixture.store, before.RunID)
	if after.PullRequest.State != before.PullRequest.State || after.PullRequest.Merged {
		t.Fatalf("recorded publication = %#v, want it untouched", after.PullRequest)
	}
}

// A merge the forge queued is settled by the settle path, which decides the
// whole run on the answer. This sweep must not ask about it as well: two paths
// deciding one merge is exactly the race the queued record exists to avoid.
func TestReconcileLeavesAQueuedMergeToTheSettlePath(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.run(t)
	forge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{Number: 1, State: "MERGED", Merged: true}}

	refreshed, err := Reconciler{
		Tracker:   fixture.tracker,
		Worktrees: newObserver(t, fixture.repository, fixture.worktreeRoot),
		Store:     fixture.store,
		Publisher: forge,
	}.RefreshPublications(context.Background())
	if err != nil {
		t.Fatalf("RefreshPublications() error = %v", err)
	}
	if len(refreshed) != 0 || forge.Asked != 0 {
		t.Fatalf("refresh = %#v after %d question(s), want the queued merge left to the settle path", refreshed, forge.Asked)
	}
	held := loadRun(t, fixture.store, pipelineRunID)
	if held.PullRequest == nil || !held.PullRequest.MergeQueued {
		t.Fatalf("held state = %#v, want the queued merge untouched", held.PullRequest)
	}
}

// publicationFixture is a repository, a worktree root and a run state store a
// second process can also see, which is what a reconciling sweep is.
type publicationFixture struct {
	repository   string
	worktreeRoot string
	store        *runstate.Store
	tracker      *orchestratortest.Tracker
}

// newPublicationFixture drives a whole publishing run that ends blocked with
// nothing integrated, and hands back the record it left. That is the run this
// sweep exists for: its pull request is recorded exactly as it stood when the
// run ended, and no settle path is ever going to ask about it again.
func newPublicationFixture(t *testing.T) (publicationFixture, runstate.State) {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	remote := addBareRemote(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider), &orchestratortest.Forge{Remote: remote})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() error = nil, want the run to end on its spent repair budget")
	}
	state := loadRun(t, store, pipelineRunID)
	if state.Integration != nil || state.PullRequest == nil {
		t.Fatalf("run = %#v, want a failed run that published and integrated nothing", state)
	}
	if state.PullRequest.Merged || state.PullRequest.State != "OPEN" || state.Outstanding() {
		t.Fatalf("publication = %#v, want it recorded open and unmerged with nothing owed", state.PullRequest)
	}
	return publicationFixture{repository: repository, worktreeRoot: worktreeRoot, store: store, tracker: tracker}, state
}

// refresh is the later sweep that re-asks the forge about what the run left
// recorded.
func (f publicationFixture) refresh(t *testing.T, forge ReconcilePullRequests) []PublicationRefresh {
	t.Helper()
	refreshed, err := Reconciler{
		Tracker:   f.tracker,
		Worktrees: newObserver(t, f.repository, f.worktreeRoot),
		Store:     f.store,
		Publisher: forge,
	}.RefreshPublications(context.Background())
	if err != nil {
		t.Fatalf("RefreshPublications() error = %v", err)
	}
	return refreshed
}

func loadRun(t *testing.T, store *runstate.Store, runID string) runstate.State {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

var _ ReconcilePullRequests = (*orchestratortest.AnsweringForge)(nil)
