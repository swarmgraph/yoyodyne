package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// On 2026-09-29 a reconcile pass over 798 recorded publications took more than
// an hour, asking the forge about each one by itself, most of them settled long
// before. A sweep over hundreds of settled records and a handful of unsettled
// ones asks only about the unsettled ones, and asks in batches.
func TestRefreshAsksTheForgeOnlyAboutUnsettledPublicationsInBatches(t *testing.T) {
	t.Parallel()

	fixture, template := newPublicationFixture(t)
	save := func(index int, shape func(*runstate.PullRequest)) runstate.State {
		state := template
		state.RunID = fmt.Sprintf("run-%032x", index+1)
		state.Branch = fmt.Sprintf("yoyodyne/yoyodyne-task/%08x", index+1)
		state.WorktreePath = fmt.Sprintf("%s-%08x", template.WorktreePath, index+1)
		published := *template.PullRequest
		published.Branch = state.Branch
		published.Number = 1000 + index
		published.URL = fmt.Sprintf("https://example.invalid/pull/%d", published.Number)
		shape(&published)
		state.PullRequest = &published
		if err := fixture.store.Create(state); err != nil {
			t.Fatalf("Create() run %d error = %v", index, err)
		}
		return state
	}

	// Three hundred settled records, in every shape that settles one.
	index := 0
	for ; index < 300; index++ {
		switch index % 4 {
		case 0, 1:
			save(index, func(published *runstate.PullRequest) { published.State, published.Merged = "MERGED", true })
		case 2:
			save(index, func(published *runstate.PullRequest) { published.State = "CLOSED" })
		default:
			if index%8 == 3 {
				save(index, func(published *runstate.PullRequest) { published.Superseded = "pull request #1 superseded it" })
			} else {
				save(index, func(published *runstate.PullRequest) {
					published.HandedBack = &runstate.PublicationHandBack{At: time.Now().UTC(), Reason: "re-run"}
				})
			}
		}
	}
	// Sixty-one unsettled: the fixture's own run and sixty more still open.
	unsettled := map[string]int{template.PullRequest.Branch: template.PullRequest.Number}
	for ; index < 360; index++ {
		state := save(index, func(*runstate.PullRequest) {})
		unsettled[state.Branch] = state.PullRequest.Number
	}

	forge := &orchestratortest.BatchingForge{Numbers: unsettled}
	tally := &ForgeQuestions{}
	refreshed, err := Reconciler{
		Tracker:   fixture.tracker,
		Worktrees: newObserver(t, fixture.repository, fixture.worktreeRoot),
		Store:     fixture.store,
		Publisher: forge,
		Forge:     tally,
	}.RefreshPublications(context.Background())
	if err != nil {
		t.Fatalf("RefreshPublications() error = %v", err)
	}

	if forge.Single != 0 {
		t.Fatalf("the forge was asked about %d branch(es) one at a time, want every question batched", forge.Single)
	}
	asked := map[string]bool{}
	for _, batch := range forge.Batches {
		if len(batch) > publish.MaxStatesPerQuery {
			t.Fatalf("one batch asked about %d branches, want at most %d", len(batch), publish.MaxStatesPerQuery)
		}
		for _, head := range batch {
			if _, ok := unsettled[head]; !ok {
				t.Fatalf("the forge was asked about %s, whose publication is settled", head)
			}
			if asked[head] {
				t.Fatalf("the forge was asked about %s twice", head)
			}
			asked[head] = true
		}
	}
	if len(asked) != len(unsettled) {
		t.Fatalf("the forge was asked about %d branch(es), want the %d unsettled ones", len(asked), len(unsettled))
	}
	if len(forge.Batches) != 2 {
		t.Fatalf("the forge was asked %d batch(es), want 2 for %d branches", len(forge.Batches), len(unsettled))
	}
	if len(refreshed) != len(unsettled) {
		t.Fatalf("refresh reported %d publication(s), want %d", len(refreshed), len(unsettled))
	}
	for _, refresh := range refreshed {
		if refresh.Failure != "" || refresh.Kept != "" || refresh.Updated {
			t.Fatalf("refresh = %#v, want an open request the record already agreed with", refresh)
		}
	}
	if tally.Publications != len(unsettled) || tally.Queries != 2 || tally.Failed != 0 {
		t.Fatalf("tally = %d publication(s) in %d request(s), %d failed; want %d in 2", tally.Publications, tally.Queries, tally.Failed, len(unsettled))
	}
	if said := tally.Describe(); !strings.Contains(said, fmt.Sprintf("%d unsettled publication(s) in 2 request(s)", len(unsettled))) {
		t.Fatalf("Describe() = %q, want how many publications and requests", said)
	}
}

// A batch the forge does not answer fails each publication in it, and the
// record is left as it stands for the next sweep, exactly as one question the
// forge did not answer always has.
func TestRefreshReportsEachPublicationOfABatchTheForgeDidNotAnswer(t *testing.T) {
	t.Parallel()

	fixture, before := newPublicationFixture(t)
	forge := &orchestratortest.BatchingForge{Err: fmt.Errorf("HTTP 502")}
	tally := &ForgeQuestions{}
	refreshed, err := Reconciler{
		Tracker:   fixture.tracker,
		Worktrees: newObserver(t, fixture.repository, fixture.worktreeRoot),
		Store:     fixture.store,
		Publisher: forge,
		Forge:     tally,
	}.RefreshPublications(context.Background())
	if err != nil {
		t.Fatalf("RefreshPublications() error = %v", err)
	}
	if len(refreshed) != 1 || !strings.Contains(refreshed[0].Failure, "HTTP 502") || refreshed[0].Updated {
		t.Fatalf("refresh = %#v, want the publication failed with the forge's words", refreshed)
	}
	if tally.Failed != 1 || tally.Queries != 1 {
		t.Fatalf("tally = %+v, want the one unanswered request counted", tally)
	}
	if after := loadRun(t, fixture.store, before.RunID); after.PullRequest.State != before.PullRequest.State {
		t.Fatalf("recorded publication = %#v, want it untouched", after.PullRequest)
	}
}

// A run whose process died is settled before a queued merge's settlement asks
// the forge anything, so a slow forge never holds the slot the dead run keeps.
// The dead run sorts after the queued one in the store, so a sweep in the
// store's order would ask the forge first.
func TestReconcileSettlesDeadRunsBeforeAskingTheForge(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.run(t)
	queued := loadRun(t, fixture.store, pipelineRunID)
	dead := runstate.State{
		SchemaVersion: queued.SchemaVersion,
		RunID:         "run-ffffffffffffffffffffffffffffffff",
		ProductID:     queued.ProductID,
		RepositoryID:  queued.RepositoryID,
		WorkItemID:    queued.WorkItemID,
		Backend:       queued.Backend,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     queued.StartedAt,
		UpdatedAt:     queued.StartedAt,
	}
	if err := fixture.store.Create(dead); err != nil {
		t.Fatalf("Create() dead run error = %v", err)
	}

	watching := &orderingForge{ReconcilePullRequests: fixture.forge, store: fixture.store, dead: dead.RunID}
	reconciler := fixture.reconciler(t)
	reconciler.Publisher = watching
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if watching.asked == 0 {
		t.Fatal("the forge was never asked about the queued merge")
	}
	if !watching.deadSettledFirst {
		t.Fatal("the forge was asked about the queued merge before the dead run was settled")
	}
	// The results are still reported in the store's order.
	if len(results) != 2 || results[0].RunID != pipelineRunID || results[1].RunID != dead.RunID {
		t.Fatalf("results = %#v, want the queued run then the dead run, in the store's order", results)
	}
}

var _ PublicationStates = (*orchestratortest.BatchingForge)(nil)

// orderingForge records, when it is first asked about a pull request, whether
// the dead run had already been settled.
type orderingForge struct {
	ReconcilePullRequests
	store            *runstate.Store
	dead             string
	asked            int
	deadSettledFirst bool
}

func (f *orderingForge) State(ctx context.Context, head string) (publish.PullRequest, error) {
	if f.asked == 0 {
		if state, err := f.store.Load(f.dead); err == nil {
			f.deadSettledFirst = state.Status.Terminal()
		}
	}
	f.asked++
	return f.ReconcilePullRequests.State(ctx, head)
}
