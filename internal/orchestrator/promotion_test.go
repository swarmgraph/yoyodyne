package orchestrator

// Development is parallel and integration is serial. These tests are about the
// second half: two runs that finish together promote one at a time, each
// holding the target branch's lease while it does, and both land.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Two runs cut from the same commit reach their promotion phase at the same
// moment, which before the lease was a race both could enter and either could
// lose in the middle of. They now queue: one promotes while the other waits,
// and the waiting one finds the target where the first left it, replays onto
// it, and promotes after all. Nothing is lost and nothing is forced — both
// changes are on the target branch when the two runs are done.
func TestTwoRunsPromotingIntoOneTargetBranchSerializeAndBothLand(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	// Worktrees are registered and unregistered underneath these two runs for
	// the whole test, by Git run outside the worktree manager and so outside its
	// registry lease. This test was one of the two seen failing intermittently
	// on a command that crossed another run's half-written registration, so the
	// condition is part of what it judges rather than something that has to be
	// turned on: see startCreationLoop.
	startCreationLoop(t, repository)
	// One state root and one worktree root for both pipelines: that is what two
	// concurrent processes on one machine actually share.
	stateRoot := t.TempDir()
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")

	witness := &promotionWitness{}
	// Both runs leave review together, so both arrive at the promotion queue at
	// once rather than one happening to be finished before the other starts. The
	// gate opens on the second arrival or on a run abandoning it, and never on a
	// clock: see "A test never bounds a wait in wall-clock time" in
	// docs/developing-yoyo.md.
	gate := newArrivalGate(2)

	items := promotionRunItems
	outcomes := make([]Outcome, len(items))
	failures := make([]error, len(items))
	var running sync.WaitGroup
	for index, item := range items {
		pipeline := witnessedPipeline(t, repository, stateRoot, worktreeRoot, witness, gate, item)
		running.Add(1)
		go func() {
			defer running.Done()
			// A run that ends before it reviews leaves nobody to meet the other at
			// the gate, so it opens the gate on its way out. Without that, any
			// failure in either run stops being a failed test and becomes a hung
			// package: the survivor waits at the gate for a partner that has already
			// returned, until the whole test binary times out ten minutes later and
			// says nothing about what actually went wrong.
			defer gate.abandon()
			outcomes[index], failures[index] = pipeline.Run(context.Background(), item)
		}()
	}
	running.Wait()

	for index, item := range items {
		if failures[index] != nil {
			t.Fatalf("Run(%s) error = %v", item, failures[index])
		}
		if outcomes[index].Status != runstate.StatusSucceeded || outcomes[index].Integration == nil {
			t.Fatalf("Run(%s) outcome = %#v", item, outcomes[index])
		}
	}

	// The promotions never overlapped, which is the whole claim. Every one of
	// them held the branch's promotion lease while it happened, so nothing else
	// could have promoted into the branch even had it tried — and the lease was
	// held by the harness path that promotes, which is the only path there is.
	promotions, overlaps, unleased := witness.observed()
	if promotions < len(items) {
		t.Fatalf("promotions = %d, want at least one per run", promotions)
	}
	if overlaps != 0 {
		t.Fatalf("%d promotion(s) started while another was still in flight", overlaps)
	}
	if unleased != 0 {
		t.Fatalf("%d promotion(s) happened without the target branch's lease held", unleased)
	}

	// Queueing is not the same as winning. The run admitted second found the
	// target where the first left it, so exactly one of the two replayed its
	// change and promoted it afterwards.
	retries := outcomes[0].IntegrationRetries + outcomes[1].IntegrationRetries
	if retries != 1 {
		t.Fatalf("integration retries = %d + %d, want the run that queued second to replay once",
			outcomes[0].IntegrationRetries, outcomes[1].IntegrationRetries)
	}

	// Both changes are on the target branch, in a straight line above the commit
	// both runs were cut from.
	history := strings.Fields(gitOutput(t, repository, "rev-list", "refs/heads/main"))
	if len(history) != 3 || history[2] != base {
		t.Fatalf("main history = %v, want both promotions above %q", history, base)
	}
	for _, item := range items {
		if _, err := os.Stat(filepath.Join(repository, item+".txt")); err != nil {
			t.Fatalf("main is missing what %s promoted: %v", item, err)
		}
	}
}

// promotionInvariantID is the invariant recorded for the rule the test above
// enforces. It is checked against this repository's own invariants rather than
// a fixture, because the fixture cannot fail the way that matters: an invariant
// nobody recorded, or one recorded with a scope, reaches no developer and no
// reviewer and there is nothing else to notice it.
const promotionInvariantID = "one-promotion-per-target-branch"

// Recording the constraint and delivering it are two different facts, and only
// the second is something code can hold. This asserts the second: that the
// recorded invariant is active, repository-wide, and actually rendered into
// both the developer's context and the reviewer's evidence. An invariant nobody
// is shown constrains nothing, and a scope quietly added to this one would take
// it out of every prompt without anything else noticing.
func TestThisRepositoryDeliversThePromotionInvariantToEveryRoleThatCouldBreakIt(t *testing.T) {
	t.Parallel()

	set, err := invariant.Store{RepositoryRoot: "../..", Directory: config.DefaultInvariants}.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recorded, found := set.Find(promotionInvariantID)
	if !found {
		t.Fatalf("no invariant %q is recorded in %s; nothing tells a developer or a reviewer that at most "+
			"one promotion per target branch happens at a time, enforced by a store lease the promoting "+
			"run's harness acquires, and that no agent performs a promotion", promotionInvariantID, set.Directory)
	}
	if !recorded.Active() {
		t.Fatalf("invariant %q is %s", promotionInvariantID, recorded.Status)
	}
	// A promotion is something any change to the harness can walk into, so the
	// constraint is repository-wide rather than scoped: that is what makes it
	// reach a work item that says nothing about integration at all.
	if len(recorded.Scope) != 0 {
		t.Fatalf("invariant %q is scoped to %v, so a work item that names none of it never sees it", promotionInvariantID, recorded.Scope)
	}

	// The developer's evidence is the work item's prose and the reviewer's is
	// that plus the change. Neither mentions promotion here, which is exactly the
	// case a transcribed constraint would miss.
	developer := set.Select("Add a feature", "Add the feature the acceptance criteria describe.")
	reviewer := set.Select("Add a feature", "Add the feature the acceptance criteria describe.", "M\tREADME.md", "1 file changed")
	for name, delivery := range map[string]invariant.Delivery{"developer": developer, "reviewer": reviewer} {
		if !strings.Contains(delivery.Text(), promotionInvariantID) {
			t.Fatalf("the %s context does not carry %q:\n%s", name, promotionInvariantID, delivery.Text())
		}
	}
}

// witnessedPipeline builds one of the two concurrent pipelines: its own tracker,
// its own provider, its own run state store over the shared root, and a worktree
// manager that reports what is true while it promotes. Nothing here serializes
// worktree creation: the two runs take their worktrees exactly the way two
// `yoyo run` invocations started at the same moment would.
func witnessedPipeline(t *testing.T, repository, stateRoot, worktreeRoot string, witness *promotionWitness, gate *arrivalGate, item string) Pipeline {
	t.Helper()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	probe, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() probe error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: item, Title: "Task", Status: "open"}}
	// Each run writes its own file, so a replayed change never conflicts with
	// the promotion that beat it and both are visible on the target afterwards.
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, item+".txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	served := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := served(request)
		if request.Role == domain.RoleReviewer {
			gate.arrive()
		}
		return result, err
	}

	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	// Two developers may work at once; only the promotion at the end of each is
	// serialized, which is the whole shape of the invariant.
	pipeline.Config.Execution.MaxConcurrentDevelopers = len(promotionRunItems)
	developer := pipeline.Config.Agents["developer"]
	developer.Instances = len(promotionRunItems)
	pipeline.Config.Agents["developer"] = developer
	pipeline.NewRunID = runstate.NewRunID
	pipeline.Worktrees = &witnessedWorktrees{WorktreeManager: pipeline.Worktrees, witness: witness, probe: probe}
	return pipeline
}

// promotionRunItems are the work items the concurrency test drives, named once
// so the configured developer capacity and the test cannot drift apart.
var promotionRunItems = []string{"yoyodyne-first", "yoyodyne-second"}

// promotionWitness records what is true at the moment a promotion happens:
// whether another promotion is already in flight, and whether the target
// branch's promotion lease is actually held.
//
// It deliberately says nothing about whether an agent is running somewhere at
// the same time. Development is parallel, so one run's developer working
// through another run's promotion is the intended behavior rather than a
// violation; what the lease is for is the promotion, and the probe below is
// what says it was taken.
type promotionWitness struct {
	mu         sync.Mutex
	promoting  int
	promotions int
	overlaps   int
	unleased   int
}

func (w *promotionWitness) promotionStarted(leaseHeld bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.promotions++
	if w.promoting > 0 {
		w.overlaps++
	}
	if !leaseHeld {
		w.unleased++
	}
	w.promoting++
}

func (w *promotionWitness) promotionFinished() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.promoting--
}

func (w *promotionWitness) observed() (promotions, overlaps, unleased int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.promotions, w.overlaps, w.unleased
}

// witnessedWorktrees is the real manager with the promotion watched. The delay
// is deliberate: it widens the window a second promotion would have to
// interleave in, so what keeps the two apart is the lease rather than either of
// them being quick.
type witnessedWorktrees struct {
	WorktreeManager
	witness *promotionWitness
	// probe is a second view of the same run state, used to ask whether the
	// branch's promotion lease is held while the promotion is happening. The lock
	// belongs to the open file description rather than to the process, so a lease
	// this process holds refuses this probe exactly as another process's would.
	probe *runstate.Store
}

func (w *witnessedWorktrees) Integrate(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Integration, error) {
	w.witness.promotionStarted(w.leaseHeld(ctx, worktree.TargetBranch))
	defer w.witness.promotionFinished()
	time.Sleep(50 * time.Millisecond)
	return w.WorktreeManager.Integrate(ctx, worktree, message)
}

// leaseHeld asks once, without waiting. The probe's context is cancelled before
// it is used, and the queue tries the lock before it looks at the context, so a
// free lease is taken on that one try and a held one refuses with the
// cancellation straight after it. There is no deadline for load to reach: a
// clock here could only turn a free lease into one that read as held. Any other
// refusal is not an answer about the lease, so it counts as not held and fails
// the test rather than passing it.
func (w *witnessedWorktrees) leaseHeld(ctx context.Context, branch string) bool {
	probeCtx, cancel := context.WithCancel(ctx)
	cancel()
	lease, err := w.probe.LeasePromotion(probeCtx, branch)
	if err != nil {
		return errors.Is(err, context.Canceled)
	}
	_ = lease.Release()
	return false
}

// arrivalGate releases everybody once the expected number have arrived, and is
// a no-op afterwards: a run that comes back around for a second review has
// nobody left to wait for.
type arrivalGate struct {
	mu        sync.Mutex
	remaining int
	open      chan struct{}
}

func newArrivalGate(expected int) *arrivalGate {
	return &arrivalGate{remaining: expected, open: make(chan struct{})}
}

func (g *arrivalGate) arrive() {
	g.mu.Lock()
	if g.remaining > 0 {
		g.remaining--
		if g.remaining == 0 {
			close(g.open)
		}
	}
	g.mu.Unlock()
	<-g.open
}

// abandon opens the gate for whoever is left, because somebody who was expected
// is not coming. A run that failed before it reviewed is the case: waiting for it
// would turn one failed run into a test binary that hangs until its timeout and
// then reports the wait rather than the failure. Calling it on a run that arrived
// normally is a no-op, so it is safe to defer unconditionally.
func (g *arrivalGate) abandon() {
	g.mu.Lock()
	if g.remaining > 0 {
		g.remaining = 0
		close(g.open)
	}
	g.mu.Unlock()
}
