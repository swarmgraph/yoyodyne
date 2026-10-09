package orchestrator

// The switch, end to end through a run: off, a run promotes its own change as
// it always has; on, the approved change is admitted to the queue with no
// replay of its own, the run ends holding no developer slot, and the queue
// lands a candidate it checked and had reviewed; and on while the target lands
// changes only through the forge's own queue, the queue is not used and the
// change still merges, with the repository setting that would let it be used
// named.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// queueRun is a run fixture whose developer writes feature.txt and whose
// project checks it with a shell command, with a merge queue wired in.
type queueRun struct {
	repository string
	tracker    *orchestratortest.Tracker
	pipeline   Pipeline
	runs       *runstate.Store
	queue      *runstate.MergeQueueStore
}

func newQueueRun(t *testing.T, moveTargetWhileDeveloping bool) *queueRun {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Add the feature", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if moveTargetWhileDeveloping {
			// Somebody lands on the target while the change is being written,
			// which with the switch off is what a run replays its change over.
			moveTarget(t, repository, "landed-meanwhile.txt")
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict, approveVerdict, approveVerdict)
	pipeline, runs := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue
	return &queueRun{repository: repository, tracker: tracker, pipeline: pipeline, runs: runs, queue: queue}
}

func (r *queueRun) run(t *testing.T) Outcome {
	t.Helper()
	outcome, err := r.pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return outcome
}

func (r *queueRun) driver() MergeQueueDriver {
	pipeline := r.pipeline
	worktrees := pipeline.Worktrees.(*gitworktree.Manager)
	return MergeQueueDriver{
		Worker: MergeQueueWorker{Pipeline: &pipeline, Queue: r.queue, Candidates: worktrees, Events: r.queue.AppendEvent, Waiting: MergeQueueWaiting(r.queue)},
		Promoter: MergeQueuePromoter{
			Pipeline: &pipeline, Queue: r.queue, Lander: worktrees,
			Completion: MergeQueueRunCompletion{Store: r.runs, Tracker: r.tracker},
			Repair:     MergeQueueRunHandback{Store: r.runs, Tracker: r.tracker},
		},
		Queues: r.queue,
	}
}

func TestWithTheSwitchOffARunPromotesItsOwnChangeAndAdmitsNothing(t *testing.T) {
	t.Parallel()

	r := newQueueRun(t, true)
	outcome := r.run(t)
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || outcome.MergeQueue != nil {
		t.Fatalf("Run() = %#v, want the change promoted by the run itself", outcome)
	}
	// It lost the race to the change that landed meanwhile, replayed onto it,
	// and was checked and reviewed again: the path the switch leaves alone.
	if outcome.IntegrationRetries != 1 {
		t.Fatalf("integration retries = %d, want the replay the switch off keeps", outcome.IntegrationRetries)
	}
	if keys, err := r.queue.Keys(); err != nil || len(keys) != 0 {
		t.Fatalf("queues = %v, %v; want nothing admitted with the switch off", keys, err)
	}
	if !r.tracker.Closed {
		t.Fatal("the item was not closed on the run's own landing")
	}
}

func TestWithTheSwitchOnTheQueueLandsACheckedAndReviewedCandidateWithNoReplay(t *testing.T) {
	t.Parallel()

	r := newQueueRun(t, true)
	r.pipeline.Config.Execution.MergeQueue = true
	base := gitLine(t, r.repository, "rev-parse", "main")
	outcome := r.run(t)
	if outcome.Status != runstate.StatusSucceeded || outcome.MergeQueue == nil || outcome.Integration != nil || outcome.IntegrationRetries != 0 {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue with no replay", outcome)
	}
	if readQueueFile(t, r.repository, "feature.txt") != "" {
		t.Fatal("the run put its change on the target although it went to the queue")
	}
	if r.tracker.Closed {
		t.Fatal("the item was closed on an admission, before anything landed")
	}
	state, err := r.runs.Load(outcome.RunID)
	if err != nil || !state.AwaitingMergeQueue() || state.HoldsDeveloperSlot() || state.MergeQueue.ApprovedHead != state.HarnessCommit {
		t.Fatalf("run = %#v, %v; want it ended waiting on the queue, holding no slot, with its committed head admitted", state.MergeQueue, err)
	}
	key := outcome.MergeQueue.Key()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pass := r.driver().Pass(ctx, key)
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	generations, err := r.queue.Generations(key, outcome.MergeQueue.EntryID)
	if err != nil || len(generations) == 0 {
		t.Fatalf("generations = %#v, %v", generations, err)
	}
	landed := generations[len(generations)-1]
	// What landed is the target as it stood with the change merged onto it,
	// checked and reviewed as that candidate.
	if gitLine(t, r.repository, "rev-parse", "main") != landed.Candidate || landed.TargetBase == base || landed.Review == nil || landed.Review.Decision != "approve" {
		t.Fatalf("main = %s, landed generation = %#v; want the verified candidate on the moved target", gitLine(t, r.repository, "rev-parse", "main"), landed)
	}
	if readQueueFile(t, r.repository, "landed-meanwhile.txt") == "" || readQueueFile(t, r.repository, "feature.txt") != "implemented\n" {
		t.Fatal("the target does not carry both the change and what landed meanwhile")
	}
	if settled, err := r.runs.Load(outcome.RunID); err != nil || settled.Integration == nil || settled.Integration.TargetCommit != landed.Candidate {
		t.Fatalf("run integration = %#v, %v; want the landing recorded on the run", settled.Integration, err)
	}
	if !r.tracker.Closed {
		t.Fatal("the item was not settled on the queue's landing")
	}
}

// requiredForgeQueue is a forge whose target lands changes only through the
// forge's own merge queue, as GitHub reports a branch whose rules require it.
type requiredForgeQueue struct {
	*orchestratortest.Forge
}

func (f requiredForgeQueue) QueueCapabilities(_ context.Context, branch string) (publish.QueueCapabilities, error) {
	return publish.QueueCapabilities{
		Forge: "github", TargetBranch: branch, ObservedAt: time.Now(), Protected: true, ProtectedBy: "a ruleset",
		QueueAvailable: true, QueueRequired: true,
	}, nil
}

func TestASwitchTurnedOnWhileTheTargetRequiresTheForgesQueueStillMerges(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, requiredForgeQueue{Forge: forge}, []string{"test -f feature.txt"})
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue
	pipeline.Config.Execution.MergeQueue = true

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The change merged the way it does with the switch off.
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || outcome.MergeQueue != nil || len(forge.Merges) != 1 {
		t.Fatalf("Run() = %#v with %d merges, want the change merged without the queue", outcome, len(forge.Merges))
	}
	// And the reason the queue was not used is said, naming the setting.
	if !strings.Contains(outcome.MergeQueueRefused, "merge queue requirement") || !strings.Contains(outcome.MergeQueueRefused, "only a person can change") {
		t.Fatalf("refusal = %q, want the repository setting named as a person's to change", outcome.MergeQueueRefused)
	}
	refusal, found, err := queue.Refusal(runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"})
	if err != nil || !found || !refusal.PersonOnly || refusal.RunID != outcome.RunID {
		t.Fatalf("Refusal() = %#v, %t, %v; want it recorded beside the queue for status to show", refusal, found, err)
	}
	if entries, err := queue.Entries(runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}); err != nil || len(entries) != 0 {
		t.Fatalf("entries = %#v, %v; want nothing admitted", entries, err)
	}
	if !strings.Contains(tracker.Notes, "cannot be used for main") {
		t.Fatal("the item was not told why the queue was not used")
	}
}
