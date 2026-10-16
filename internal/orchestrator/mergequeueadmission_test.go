package orchestrator

// The switch, end to end through a run: off, a run promotes its own change as
// it always has; on, the approved change is admitted to the queue with no
// replay of its own, the run ends holding no developer slot, and the queue
// lands a candidate it checked and had reviewed; and on while the target lands
// changes only through the forge's own queue, the change is held rather than
// merged the old way, with the repository setting that holds it named, until
// the requirement is removed or the switch is turned off.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/report"
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

// switchableForgeQueue is a forge whose target lands changes only through the
// forge's own merge queue while required is set, as GitHub reports a branch
// whose rules require it, and whose protection cannot be read while unread is
// set. A person removing the requirement is required going false.
type switchableForgeQueue struct {
	*orchestratortest.Forge
	required *bool
	unread   *error
}

func (f switchableForgeQueue) QueueCapabilities(_ context.Context, branch string) (publish.QueueCapabilities, error) {
	if f.unread != nil && *f.unread != nil {
		return publish.QueueCapabilities{}, *f.unread
	}
	observed := publish.QueueCapabilities{Forge: "github", TargetBranch: branch, ObservedAt: time.Now(), Protected: true, ProtectedBy: "a ruleset"}
	if *f.required {
		observed.QueueAvailable, observed.QueueRequired = true, true
	}
	return observed, nil
}

// heldQueueRun is a published run whose target requires the forge's own merge
// queue, with the merge queue switched on.
type heldQueueRun struct {
	repository string
	tracker    *orchestratortest.Tracker
	forge      *orchestratortest.Forge
	pipeline   Pipeline
	runs       *runstate.Store
	queue      *runstate.MergeQueueStore
	reports    *fakeReports
	required   bool
	unread     error
}

func newHeldQueueRun(t *testing.T, queue *runstate.MergeQueueStore, reports *fakeReports) *heldQueueRun {
	t.Helper()
	repository, remote := publishedRepository(t)
	r := &heldQueueRun{
		repository: repository, queue: queue, reports: reports, required: true,
		tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}},
		forge:   &orchestratortest.Forge{Remote: remote},
	}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict, approveVerdict)
	r.pipeline, r.runs = newPublishingPipeline(t, repository, r.tracker, provider, switchableForgeQueue{Forge: r.forge, required: &r.required, unread: &r.unread}, []string{"test -f feature.txt"})
	r.pipeline.MergeQueue = queue
	r.pipeline.Reports = reports
	r.pipeline.Config.Execution.MergeQueue = true
	return r
}

// held looks at the changes the queue holds the way a watching session does,
// resuming a held run at its promotion through the same action `yoyo triage
// resume` is.
func (r *heldQueueRun) held(t *testing.T, outcome Outcome) MergeQueueHeldPass {
	t.Helper()
	docket := &memoryDocket{}
	stopped, err := r.runs.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docketerOverStore(docket, r.runs, r.pipeline.Config).RecordStoppedRun(stopped); err != nil {
		t.Fatal(err)
	}
	resumer := IntegrationResumer{
		Docket: docket, Runs: r.runs, Intake: newIntakeHoldStore(t), Items: r.tracker,
		Worktrees: r.pipeline.Worktrees.(*gitworktree.Manager), Capacity: 1,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return r.pipeline.Continue(ctx, workItemID, runID)
		},
	}
	forge := switchableForgeQueue{Forge: r.forge, required: &r.required, unread: &r.unread}
	return MergeQueueHeldChanges{
		Docket: docket, Runs: r.runs, Refusals: r.queue, Forge: forge,
		Harness: queuemode.Harness{PullRequests: true}, Enabled: r.pipeline.Config.Execution.MergeQueue, ProductID: "yoyodyne",
		Resume: func(ctx context.Context, runID string) (IntegrationResumeResult, error) {
			return resumer.Resume(ctx, IntegrationResumeRequest{Run: runID})
		},
	}.Take(context.Background())
}

var mainQueueKey = runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}

// With the switch on and the target requiring the forge's own merge queue,
// nothing is admitted and nothing is merged the old way: the change is held at
// its promotion with the setting named, one report is filed however many
// changes are held, and no repository setting is touched.
func TestASwitchTurnedOnWhileTheTargetRequiresTheForgesQueueHoldsTheChangeAndReportsOnce(t *testing.T) {
	t.Parallel()

	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	reports := &fakeReports{}
	for round := 1; round <= 2; round++ {
		r := newHeldQueueRun(t, queue, reports)
		outcome, err := r.pipeline.Run(context.Background(), "yoyodyne-task")
		if err == nil || !errors.Is(err, ErrMergeQueueHeld) {
			t.Fatalf("round %d: Run() error = %v, want the change held from the merge queue", round, err)
		}
		if outcome.Integration != nil || outcome.MergeQueue != nil || len(r.forge.Merges) != 0 || outcome.IntegrationRetries != 0 {
			t.Fatalf("round %d: Run() = %#v with %d merges; want nothing admitted, replayed, or merged", round, outcome, len(r.forge.Merges))
		}
		if outcome.IntegrationStop == nil || outcome.IntegrationStop.Cause != runstate.CauseMergeQueueHeld {
			t.Fatalf("round %d: integration stop = %#v, want the change held at its promotion with its approval standing", round, outcome.IntegrationStop)
		}
		if !r.tracker.Blocked || !strings.Contains(r.tracker.BlockReason, "merge queue requirement") || !strings.Contains(r.tracker.BlockReason, "only a person can change") {
			t.Fatalf("round %d: block reason = %q, want the setting that holds it named as a person's", round, r.tracker.BlockReason)
		}
		if !environmentalStop(outcome) {
			t.Fatalf("round %d: the hold counts toward the failure-storm brake", round)
		}
		if remote, err := r.forge.Git("rev-parse", "refs/heads/main"); err != nil || remote != gitLine(t, r.repository, "rev-parse", "main") {
			t.Fatalf("round %d: the remote target moved (%s, %v)", round, remote, err)
		}
	}
	if len(reports.appended) != 1 || reports.appended[0].Role != report.HarnessReporter {
		t.Fatalf("reports = %#v, want exactly one report of the condition from the harness", reports.appended)
	}
	if entries, err := queue.Entries(mainQueueKey); err != nil || len(entries) != 0 {
		t.Fatalf("entries = %#v, %v; want nothing admitted", entries, err)
	}
	if refusal, found, err := queue.Refusal(mainQueueKey); err != nil || !found || !refusal.PersonOnly {
		t.Fatalf("Refusal() = %#v, %t, %v; want the condition recorded beside the queue", refusal, found, err)
	}
}

// A protection nobody can read holds the change in the same way, naming the
// read that failed, and is not a person's to clear unless the forge refused
// the harness's login.
func TestAProtectionThatCannotBeReadHoldsTheChangeNamingTheRead(t *testing.T) {
	t.Parallel()

	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	r := newHeldQueueRun(t, queue, &fakeReports{})
	r.unread = errors.New("gh: Server Error (HTTP 502)")
	outcome, err := r.pipeline.Run(context.Background(), "yoyodyne-task")
	if !errors.Is(err, ErrMergeQueueHeld) || len(r.forge.Merges) != 0 || outcome.MergeQueue != nil {
		t.Fatalf("Run() = %#v, %v with %d merges; want the change held", outcome, err, len(r.forge.Merges))
	}
	if !strings.Contains(r.tracker.BlockReason, "could not be read") || !strings.Contains(r.tracker.BlockReason, "HTTP 502") {
		t.Fatalf("block reason = %q, want the failed read named", r.tracker.BlockReason)
	}
	if len(r.reports.appended) != 1 || r.reports.appended[0].For != domain.RoleDevelopmentManager {
		t.Fatalf("reports = %#v, want one report for the development manager", r.reports.appended)
	}
	// It stays held while the protection still cannot be read.
	if pass := r.held(t, outcome); pass.Held != 1 || pass.Resumed != 0 {
		t.Fatalf("held pass = %#v, want the change still held", pass)
	}
}

// Removing the requirement lifts the hold at the next reading: the held
// change is resumed at its promotion and admitted to the queue, charged
// nothing, and the condition's record is cleared so a later one is reported
// again.
func TestRemovingTheRequirementAdmitsTheHeldChangeAtTheNextReading(t *testing.T) {
	t.Parallel()

	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	r := newHeldQueueRun(t, queue, &fakeReports{})
	outcome, err := r.pipeline.Run(context.Background(), "yoyodyne-task")
	if !errors.Is(err, ErrMergeQueueHeld) {
		t.Fatalf("Run() error = %v, want the change held", err)
	}
	if pass := r.held(t, outcome); pass.Held != 1 || pass.Resumed != 0 {
		t.Fatalf("held pass while required = %#v, want it still held", pass)
	}
	r.required = false
	if pass := r.held(t, outcome); pass.Held != 1 || pass.Resumed != 1 {
		t.Fatalf("held pass once the requirement is removed = %#v, want the change taken up", pass)
	}
	resumed, err := r.runs.Load(outcome.RunID)
	if err != nil || !resumed.AwaitingMergeQueue() || resumed.IntegrationRetries != 0 || len(r.forge.Merges) != 0 {
		t.Fatalf("run = %#v, %v with %d merges; want it admitted to the queue, unreplayed and unmerged", resumed.MergeQueue, err, len(r.forge.Merges))
	}
	if entries, err := queue.Entries(mainQueueKey); err != nil || len(entries) != 1 || entries[0].RunID != outcome.RunID {
		t.Fatalf("entries = %#v, %v; want the held change admitted", entries, err)
	}
	if _, found, err := queue.Refusal(mainQueueKey); err != nil || found {
		t.Fatalf("Refusal() found = %t, %v; want the condition cleared", found, err)
	}
}

// A switch turned off while a change is held lifts the hold too, and the
// change is integrated the way changes integrate with the switch off.
func TestASwitchTurnedOffWhileAChangeIsHeldIntegratesItTheOldWay(t *testing.T) {
	t.Parallel()

	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	r := newHeldQueueRun(t, queue, &fakeReports{})
	outcome, err := r.pipeline.Run(context.Background(), "yoyodyne-task")
	if !errors.Is(err, ErrMergeQueueHeld) {
		t.Fatalf("Run() error = %v, want the change held", err)
	}
	r.pipeline.Config.Execution.MergeQueue = false
	if pass := r.held(t, outcome); pass.Resumed != 1 {
		t.Fatalf("held pass = %#v, want the change taken up", pass)
	}
	if len(r.forge.Merges) != 1 {
		t.Fatalf("merges = %d, want the change merged through its pull request as with the switch off", len(r.forge.Merges))
	}
	if entries, err := queue.Entries(mainQueueKey); err != nil || len(entries) != 0 {
		t.Fatalf("entries = %#v, %v; want nothing admitted with the switch off", entries, err)
	}
}
