package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Two runs landing back to back on one target branch run their landing checks
// one after the other rather than side by side: the second finds the first's
// landing running, says on its record that it is waiting and behind what, cuts
// no checkout while the first's is standing, and runs its checks only once the
// first's checkout is gone. The two landings are two stores over one state
// root, which is two processes as far as the lease is concerned.
func TestTwoLandingsOnOneTargetBranchRunOneAtATime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	landings := &queuedLandings{
		entered: make(chan string, 2),
		release: make(chan struct{}),
	}
	first := landingRun(t, root, landings, 1, "1111111111111111111111111111111111111111")
	second := landingRun(t, root, landings, 2, "2222222222222222222222222222222222222222")

	ctx := context.Background()
	var done sync.WaitGroup
	done.Add(1)
	go func() { defer done.Done(); first.runLandingChecks(ctx) }()
	if entered := <-landings.entered; entered != first.state.RunID {
		t.Fatalf("the first check to run was %s's, want the first landing's", entered)
	}

	done.Add(1)
	go func() { defer done.Done(); second.runLandingChecks(ctx) }()
	store := second.pipeline.Store.(*runstate.Store)
	waiting := awaitLanding(t, store, second.state.RunID, func(landed *runstate.LandingChecks) bool {
		return landed.Waiting()
	})
	described := waiting.Describe(time.Now())
	for _, want := range []string{"landing checks waiting over 222222222222", "behind another landing on main", "since "} {
		if !strings.Contains(described, want) {
			t.Fatalf("the waiting landing reads %q, want it to say %q", described, want)
		}
	}
	if cut := landings.cutFor(second.state.RunID); cut {
		t.Fatal("the second landing cut its checkout while the first's landing was running")
	}

	close(landings.release)
	done.Wait()

	if landings.most != 1 {
		t.Fatalf("%d landing check stages ran at once, want one", landings.most)
	}
	want := []string{
		"cut " + first.state.RunID, "remove " + first.state.RunID,
		"cut " + second.state.RunID, "remove " + second.state.RunID,
	}
	if strings.Join(landings.order, ",") != strings.Join(want, ",") {
		t.Fatalf("landing steps ran in the order %v, want %v", landings.order, want)
	}
	for _, run := range []*activeRun{first, second} {
		landed := run.outcome.LandingChecks
		if landed == nil || !landed.Green || landed.Problem != "" || landed.TargetBranch != "main" {
			t.Fatalf("landing for %s = %#v, want a green landing on main with nothing wrong around it", run.state.RunID, landed)
		}
	}
	queued := second.outcome.LandingChecks
	if queued.WaitingSince == nil || queued.AdmittedAt == nil || queued.AdmittedAt.Before(*queued.WaitingSince) {
		t.Fatalf("second landing = %#v, want when it began waiting and when it was let in recorded", queued)
	}
	if !strings.Contains(queued.Describe(time.Now()), "after waiting") {
		t.Fatalf("the second landing reads %q, want the wait said beside the result", queued.Describe(time.Now()))
	}
	if unqueued := first.outcome.LandingChecks; unqueued.WaitingSince != nil || strings.Contains(unqueued.Describe(time.Now()), "waiting") {
		t.Fatalf("first landing = %#v, want no wait recorded on a landing that never waited", unqueued)
	}
}

// A landing that finds its branch's landing lease held for the whole bound runs
// nothing: it is unverified, says which queue it waited on, and never cuts a
// checkout.
func TestALandingThatWaitsOutItsBoundRunsNothingAndIsUnverified(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	landings := &queuedLandings{entered: make(chan string, 1), release: make(chan struct{})}
	close(landings.release)
	run := landingRun(t, root, landings, 1, "1111111111111111111111111111111111111111")
	holder, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	lease, err := holder.LeaseLanding(context.Background(), "main", time.Second, nil)
	if err != nil {
		t.Fatalf("LeaseLanding() error = %v", err)
	}
	defer lease.Release()
	run.pipeline.Store = shortLandingWait{StateStore: run.pipeline.Store}

	run.runLandingChecks(context.Background())

	landed := run.outcome.LandingChecks
	if landed == nil || !landed.Unverified() || landed.Red() || len(landed.Checks) != 0 {
		t.Fatalf("landing = %#v, want an unverified landing that ran nothing", landed)
	}
	if !strings.Contains(landed.Problem, "never started") || !strings.Contains(landed.Problem, "another landing held the lease") {
		t.Fatalf("landing problem = %q, want the queue named", landed.Problem)
	}
	if len(landings.order) != 0 {
		t.Fatalf("landing steps = %v, want no checkout cut", landings.order)
	}
}

// shortLandingWait bounds the landing queue at a length a test can wait out,
// in place of the landing's whole budget.
type shortLandingWait struct {
	StateStore
}

func (s shortLandingWait) LeaseLanding(ctx context.Context, targetBranch string, _ time.Duration, queued func()) (*runstate.Lease, error) {
	return s.StateStore.LeaseLanding(ctx, targetBranch, 50*time.Millisecond, queued)
}

// landingRun is a run that has integrated a commit into main and is about to
// run its landing checks, with a store of its own over the shared root.
func landingRun(t *testing.T, root string, landings *queuedLandings, index int, commit string) *activeRun {
	t.Helper()
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	state := recordedRun(t, "yoyodyne-task-"+string(rune('0'+index)), runstate.StatusSucceeded, time.Now())
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	pipeline := Pipeline{
		Store:    store,
		Tracker:  &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Task", Status: "closed"}},
		Checks:   landings,
		Landings: landings,
	}
	pipeline.Config.LandingChecks = []string{"make race"}
	return &activeRun{
		pipeline: pipeline,
		state:    state,
		outcome: Outcome{
			RunID:       state.RunID,
			WorkItemID:  state.WorkItemID,
			Status:      runstate.StatusSucceeded,
			Integration: &gitworktree.Integration{TargetBranch: "main", TargetCommit: commit},
		},
	}
}

// awaitLanding reads a run's record until its landing satisfies ready, which is
// what `yoyo status` reads it from. It sets no deadline: one a loaded machine
// can reach with the landing working fails a change that never touched it, and
// a landing that never gets there is reported by the binary's own -timeout,
// naming this wait.
func awaitLanding(t *testing.T, store *runstate.Store, runID string, ready func(*runstate.LandingChecks) bool) runstate.LandingChecks {
	t.Helper()
	for {
		if state, err := store.Load(runID); err == nil && state.LandingChecks != nil && ready(state.LandingChecks) {
			return *state.LandingChecks
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// queuedLandings stands in for both the landing checkouts and the check runner,
// so it sees every step of every landing in one order. A check stage announces
// itself on entered and then blocks until release is closed, and the most stages
// ever running at once is kept.
type queuedLandings struct {
	mu      sync.Mutex
	order   []string
	running int
	most    int
	entered chan string
	release chan struct{}
}

func (q *queuedLandings) CheckoutCommit(_ context.Context, runID, _ string) (string, error) {
	q.record("cut " + runID)
	return "/landing/" + runID, nil
}

func (q *queuedLandings) RemoveCheckout(_ context.Context, path string) error {
	q.record("remove " + strings.TrimPrefix(path, "/landing/"))
	return nil
}

func (q *queuedLandings) cutFor(runID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, step := range q.order {
		if step == "cut "+runID {
			return true
		}
	}
	return false
}

func (q *queuedLandings) record(step string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.order = append(q.order, step)
}

func (q *queuedLandings) Run(_ context.Context, request checks.Request, _ func(execution.Event) error) ([]checks.Result, uint64, error) {
	q.mu.Lock()
	q.running++
	if q.running > q.most {
		q.most = q.running
	}
	q.mu.Unlock()
	q.entered <- request.RunID
	<-q.release
	q.mu.Lock()
	q.running--
	q.mu.Unlock()
	started := time.Now()
	results := make([]checks.Result, 0, len(request.Commands))
	for _, command := range request.Commands {
		results = append(results, checks.Result{
			Command: command,
			Passed:  true,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded, StartedAt: started, FinishedAt: started},
		})
	}
	return results, request.LastSequence, nil
}
