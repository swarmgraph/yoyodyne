package orchestrator

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
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run that exited on its in-process usage-limit bound has the phantom shape
// 428.4 settled for a provider stopped on time — no live process, no ending, a
// developer slot held — and nothing wrong with it: it is a wait whose process
// went away. Inside the deadline it is the wait it is, and the sweep leaves it
// exactly as it stands. Past the deadline with nothing holding its lease the
// sweep continues it itself, in the same worktree and developer session, the
// run's record says the sweep did, and the wait ends with nobody typing
// `yoyo run`.
func TestTheSweepContinuesARunThatExitedOnItsInProcessUsageLimitBound(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	// The first process refuses the attempt on the limit and exits on its
	// in-process bound, leaving the run in flight with the deadline recorded.
	first := usageLimitBackend(1, limit, approveVerdict)
	firstClock := &pausingClock{now: baseTime}
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		firstClock, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}
	exited, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if exited.UsageLimitResetsAt == nil || !exited.UsageLimitResetsAt.Equal(resetsAt) {
		t.Fatalf("exited run = %#v, want the deadline recorded", exited)
	}
	tracker.Item.Status = "in_progress"

	// Inside the deadline the run is the wait it is, whether or not a process
	// is asleep on it, and a sweep with a continuation wired continues nothing.
	continued := 0
	serving := usageLimitBackend(0, limit, approveVerdict)
	servingClock := &pausingClock{now: resetsAt.Add(time.Minute)}
	servingPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, serving, []string{"exit 0"}), serving),
		servingClock, 6*time.Hour, time.Minute)
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Clock:     &pausingClock{now: resetsAt.Add(-time.Minute)},
		Continue: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			continued++
			return servingPipeline.Continue(ctx, workItemID, runID)
		},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation = %#v, want the waiting run left resumable", results)
	}
	if !strings.Contains(results[0].Detail, "can continue once it asks again") || !strings.Contains(results[0].Detail, "continues it") {
		t.Fatalf("reconciliation detail %q does not say the run is waiting and what continues it after the deadline", results[0].Detail)
	}
	early, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(early) != 0 || continued != 0 {
		t.Fatalf("ContinueWaits() inside the deadline = %#v with %d continuation(s), want the wait left as it is", early, continued)
	}
	untouched, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if untouched.Status != exited.Status || untouched.UsageLimitResetsAt == nil || len(untouched.SweepContinuations) != 0 {
		t.Fatalf("a sweep inside the deadline disturbed the waiting run: %#v", untouched)
	}

	// Past the deadline, with nothing holding the lease, the settle says the
	// sweep continues it and the continuation step does.
	reconciler.Clock = servingClock
	results, err = reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() past the deadline error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation past the deadline = %#v, want the run left for the continuation step", results)
	}
	for _, want := range []string{"has passed with no process serving the wait", "`yoyo reconcile` continues it"} {
		if !strings.Contains(results[0].Detail, want) {
			t.Fatalf("reconciliation detail %q does not say %q", results[0].Detail, want)
		}
	}
	continuations, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(continuations) != 1 || continued != 1 {
		t.Fatalf("ContinueWaits() = %#v with %d continuation(s), want the one exited run continued once", continuations, continued)
	}
	continuation := continuations[0]
	if continuation.RunID != paused.RunID || continuation.WorkItemID != tracker.Item.ID || !continuation.Continued || continuation.Failure != "" {
		t.Fatalf("continuation = %#v, want the exited run continued without failure", continuation)
	}
	if !continuation.Deadline.Equal(resetsAt) || !strings.Contains(continuation.Waited, "five_hour usage limit") {
		t.Fatalf("continuation = %#v, want the recorded deadline and what was waited out", continuation)
	}
	// The wait ended without a person: the continued run is the same run, in the
	// same worktree and developer session, and it landed.
	if continuation.Outcome == nil || continuation.Outcome.RunID != paused.RunID || continuation.Outcome.Integration == nil {
		t.Fatalf("continued outcome = %#v, want the same run integrated", continuation.Outcome)
	}
	if continuation.Outcome.WorktreePath != exited.WorktreePath || continuation.Outcome.ProviderSessionID != exited.ProviderSessionID {
		t.Fatalf("continued outcome = %#v, want the exited run's worktree %q and session %q", continuation.Outcome, exited.WorktreePath, exited.ProviderSessionID)
	}
	if len(serving.Requests) == 0 || serving.Requests[0].SessionID != exited.ProviderSessionID {
		t.Fatalf("continued attempt requests = %#v, want the developer session the refused attempt established", serving.Requests)
	}

	// The record says the sweep continued it, and says what it saw.
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if landed.Status != runstate.StatusSucceeded || landed.UsageLimitResetsAt != nil {
		t.Fatalf("landed run = %#v, want a succeeded run no longer waiting", landed)
	}
	recorded, ok := landed.LastSweepContinuation()
	if !ok || len(landed.SweepContinuations) != 1 {
		t.Fatalf("sweep continuations = %#v, want the one continuation recorded", landed.SweepContinuations)
	}
	if recorded.Cause != runstate.PauseUsageLimit || !recorded.Deadline.Equal(resetsAt) || !recorded.ContinuedAt.Equal(servingClock.now) {
		t.Fatalf("recorded continuation = %#v, want the cause, the deadline that had passed, and when the sweep took it up", recorded)
	}
	for _, want := range []string{"the reconcile sweep continued it", "no live process held it", resetsAt.UTC().Format(time.RFC3339), "own worktree and developer session"} {
		if !strings.Contains(recorded.Reason, want) {
			t.Fatalf("recorded reason %q does not say %q", recorded.Reason, want)
		}
	}
	if err := landed.Validate(); err != nil {
		t.Fatalf("landed record does not validate: %v", err)
	}

	// A further sweep finds nothing waiting and continues nothing.
	again, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("second ContinueWaits() error = %v", err)
	}
	if len(again) != 0 || continued != 1 {
		t.Fatalf("second ContinueWaits() = %#v with %d continuation(s), want nothing to continue", again, continued)
	}
}

// A run a live process is serving is that process's, however far past the
// deadline the sweep reads it: the process asleep on the wait wakes at the
// deadline itself. The sweep reports it held and continues nothing, and a
// sweep wired with no continuation continues nothing at all.
func TestTheSweepLeavesAWaitALiveProcessIsServing(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
	first := usageLimitBackend(1, limit, approveVerdict)
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}

	later := &pausingClock{now: resetsAt.Add(time.Minute)}
	unwired := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store, Clock: later}
	nothing, err := unwired.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(nothing) != 0 {
		t.Fatalf("an unwired sweep continued something: %#v", nothing)
	}

	// Another process holds the run for the length of the sweep.
	_, lease, err := store.AdoptRun(context.Background(), paused.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	defer lease.Release()
	continued := false
	wired := unwired
	wired.Continue = func(context.Context, string, string) (Outcome, error) {
		continued = true
		return Outcome{}, errors.New("nothing should be continued while a process holds the run")
	}
	held, err := wired.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(held) != 1 || held[0].Continued || held[0].Failure != "" || !strings.Contains(held[0].Detail, "a live process holds this run") {
		t.Fatalf("ContinueWaits() over a held run = %#v, want it reported held and left", held)
	}
	if continued {
		t.Fatal("the sweep continued a run a live process was serving")
	}
	unchanged, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(unchanged.SweepContinuations) != 0 || unchanged.UsageLimitResetsAt == nil {
		t.Fatalf("the sweep wrote to a run a live process holds: %#v", unchanged)
	}
}

// A run parked on the operator's pause exits on the same in-process bound and
// leaves the same shape behind — no live process, no ending, a slot held — and
// the sweep leaves it exactly where the operator left it: what lifts a park is
// `yoyo resume` rather than a clock, and a sweep that continued one would be
// the harness spending against a pause the operator placed. The exclusion is
// decided on the park itself rather than on the deadline field, so a record
// carrying a passed deadline beside its park is still the park, and the settle
// promises it no continuation the sweep will not make.
func TestTheSweepLeavesARunParkedOnTheOperatorsPauseAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	holds := newOperatorHoldStore(t)
	// The operator pauses as the item is claimed, so the run parks at its first
	// developer attempt: in the developing phase, with its worktree cut, which is
	// the shape a usage-limit wait has and the one the exclusion is asked about.
	tracker.OnClaim = func() error {
		_, err := holds.Hold(baseTime)
		return err
	}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error {
		return errors.New("the developer must not be invoked while the operator holds activity")
	}, approveVerdict)
	// No time at all is spent holding the process open, so the run exits on the
	// bound with the park recorded, as one does past the in-process pause.
	pipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider),
		&pausingClock{now: baseTime}, 6*time.Hour, 0)
	pipeline.Holds = holds
	parked, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !parked.Paused || parked.PausedByOperator == nil {
		t.Fatalf("Run() error = %v, outcome = %#v, want a run parked on the operator's pause", err, parked)
	}
	if len(provider.Requests) != 0 {
		t.Fatalf("the provider was invoked under the operator's pause: %#v", provider.Requests)
	}
	before, err := store.Load(parked.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if before.OperatorHeldSince == nil || before.PauseCause != runstate.PauseOperatorHold || before.Phase != runstate.PhaseDeveloping || before.WorktreePath == "" {
		t.Fatalf("parked run = %#v, want the park recorded on a developing run with its worktree", before)
	}

	continued := false
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		// However long the park has stood: a day later is still the operator's.
		Clock: &pausingClock{now: baseTime.Add(24 * time.Hour)},
		Continue: func(context.Context, string, string) (Outcome, error) {
			continued = true
			return Outcome{}, errors.New("nothing should be continued for a run the operator parked")
		},
	}
	nothing, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(nothing) != 0 || continued {
		t.Fatalf("ContinueWaits() over a parked run = %#v with continued=%t, want the park left alone", nothing, continued)
	}

	// The shape the exclusion is pinned against: the same park with a deadline
	// recorded beside it that has long passed. Read on the deadline alone it
	// would be a wait nothing is serving; read as the park it is, it is left.
	passed := baseTime.Add(time.Hour)
	withDeadline := before
	withDeadline.UsageLimitResetsAt = &passed
	if err := store.Save(withDeadline); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation = %#v, want the parked run left resumable", results)
	}
	if strings.Contains(results[0].Detail, "continues it") {
		t.Fatalf("reconciliation detail %q promises a continuation the sweep does not make for a parked run", results[0].Detail)
	}
	still, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(still) != 0 || continued {
		t.Fatalf("ContinueWaits() over a parked run carrying a deadline = %#v with continued=%t, want the park left alone", still, continued)
	}
	after, err := store.Load(parked.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(after.SweepContinuations) != 0 || after.OperatorHeldSince == nil || after.Status != before.Status || after.Phase != before.Phase {
		t.Fatalf("the sweep disturbed a run the operator parked: %#v", after)
	}
	if tracker.Blocked || tracker.Closed {
		t.Fatalf("the sweep acted on the item of a parked run: blocked=%t closed=%t", tracker.Blocked, tracker.Closed)
	}
}

// Two runs that exited on the bound are continued by one sweep at once rather
// than one after the other — each already holds its slot, and a developer
// attempt waiting on another's would be a slot held for nothing. Each is
// continued through a pipeline of its own, which is the shape the sweep verb
// builds one with (a pipeline per continuation, as a pull builds one per run),
// so what the two share is the run state root, the repository, and the target
// branch their promotions are serialized into. Both land, on their own sessions,
// and each record says the sweep continued it.
func TestTheSweepContinuesTwoExitedRunsAtOnce(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	stateRoot := t.TempDir()
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
	items := []string{"yoyodyne-first", "yoyodyne-second"}

	// Each run has its own tracker, its own provider, and its own clock, exactly
	// as two `yoyo run` invocations would; only the state root is shared.
	exited := map[string]runstate.State{}
	serving := map[string]Pipeline{}
	providers := map[string]*orchestratortest.Backend{}
	for _, item := range items {
		first := usageLimitBackend(1, limit, approveVerdict)
		paused, err := exitedPipeline(t, repository, stateRoot, worktreeRoot, item, "open", first, &pausingClock{now: baseTime}).Run(context.Background(), item)
		if err != nil || !paused.Paused {
			t.Fatalf("Run(%s) error = %v, paused = %t", item, err, paused.Paused)
		}
		store, err := runstate.NewStore(stateRoot, "yoyodyne")
		if err != nil {
			t.Fatalf("runstate.NewStore() error = %v", err)
		}
		state, err := store.Load(paused.RunID)
		if err != nil {
			t.Fatalf("Load(%s) error = %v", paused.RunID, err)
		}
		exited[item] = state
		providers[item] = usageLimitBackend(0, limit, approveVerdict)
		// Each served attempt also writes a file of its own, so the change the
		// second promotion replays onto the first's landing is not one the target
		// already carries whole.
		served, own := providers[item].Respond, item+".txt"
		providers[item].Respond = func(request backend.RunRequest) (backend.RunResult, error) {
			result, err := served(request)
			if err == nil && request.Role == domain.RoleDeveloper {
				err = os.WriteFile(filepath.Join(request.WorkingDirectory, own), []byte("implemented\n"), 0o600)
			}
			return result, err
		}
		// The fake tracker does not move an item on its claim, so the serving
		// pipeline is handed the item as the claim left it.
		serving[item] = exitedPipeline(t, repository, stateRoot, worktreeRoot, item, "in_progress", providers[item], &pausingClock{now: resetsAt.Add(time.Minute)})
	}

	sweepStore, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	// Neither continuation proceeds until both have been entered, so the gate is
	// the whole of the claim: reaching the other side of it is what says the two
	// were in flight at once, and a sweep that hosted them one after the other
	// has no second arrival to open it with.
	//
	// So the gate is never abandoned on a clock, which is what this waited on
	// before. A thirty-second bound on the second arrival was reached with the
	// sweep working — 110s and 51s on two loaded runs of `make race`, against
	// 1.6s idle — and failed changes that never touched this package. That is
	// the bound-that-fails-on-load this repository has ruled out; see "A test
	// never bounds a wait in wall-clock time" in docs/developing-yoyo.md.
	//
	// What the bound bought was a failure instead of a hang, and `go test` buys
	// that already: a serialized sweep waits here until the binary's own
	// -timeout, which reports it with a dump of every goroutine naming this
	// gate. That is a verdict about the sweep, which a clock could not give.
	gate := newArrivalGate(len(items))
	var (
		mu      sync.Mutex
		entered int
	)
	arrive := func() {
		mu.Lock()
		entered++
		mu.Unlock()
		gate.arrive()
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: items[0], Title: "Task", Status: "in_progress"}}
	tracker.HoldsItem(beads.WorkItem{ID: items[1], Title: "Task", Status: "in_progress"})
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     sweepStore,
		Clock:     &pausingClock{now: resetsAt.Add(time.Minute)},
		Continue: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			arrive()
			return serving[workItemID].Continue(ctx, workItemID, runID)
		},
	}
	continuations, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if entered != len(items) {
		t.Fatalf("Continue was entered %d time(s), want once per exited run", entered)
	}
	if len(continuations) != len(items) {
		t.Fatalf("ContinueWaits() = %#v, want both exited runs continued", continuations)
	}
	for _, continuation := range continuations {
		was := exited[continuation.WorkItemID]
		if continuation.RunID != was.RunID || !continuation.Continued || continuation.Failure != "" {
			t.Fatalf("continuation = %#v, want %s's exited run continued without failure", continuation, continuation.WorkItemID)
		}
		if continuation.Outcome == nil || continuation.Outcome.Integration == nil || continuation.Outcome.RunID != was.RunID {
			t.Fatalf("continued outcome for %s = %#v, want the same run integrated", continuation.WorkItemID, continuation.Outcome)
		}
		if continuation.Outcome.WorktreePath != was.WorktreePath || continuation.Outcome.ProviderSessionID != was.ProviderSessionID {
			t.Fatalf("continued outcome for %s = %#v, want its own worktree %q and session %q", continuation.WorkItemID, continuation.Outcome, was.WorktreePath, was.ProviderSessionID)
		}
		requests := providers[continuation.WorkItemID].Requests
		if len(requests) == 0 || requests[0].SessionID != was.ProviderSessionID {
			t.Fatalf("continued attempt requests for %s = %#v, want its own developer session resumed", continuation.WorkItemID, requests)
		}
		landed, err := sweepStore.Load(was.RunID)
		if err != nil {
			t.Fatalf("Load(%s) error = %v", was.RunID, err)
		}
		if landed.Status != runstate.StatusSucceeded || len(landed.SweepContinuations) != 1 || landed.SweepContinuations[0].Cause != runstate.PauseUsageLimit {
			t.Fatalf("landed run for %s = %#v, want a succeeded run whose record says the sweep continued it once", continuation.WorkItemID, landed)
		}
	}
	// Reaching here is the claim: ContinueWaits returned, so both continuations
	// passed a gate that only opens once both have arrived, so the second was
	// never made to wait for the first to land. There is nothing left to assert
	// about it — a sweep that hosted them one after the other never gets here.
}

// exitedPipeline builds one of the concurrent pipelines a two-run sweep drives:
// its own tracker over the item named in the status given, its own run state
// store over the shared root, and room for two developers, so the second run
// can be in flight beside the first exactly as two runs started by a pull are.
func exitedPipeline(t *testing.T, repository, stateRoot, worktreeRoot, item, status string, provider recordingBackend, clock *pausingClock) Pipeline {
	t.Helper()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: item, Title: "Task", Status: status}}
	pipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider),
		clock, 6*time.Hour, time.Minute)
	pipeline.Config.Execution.MaxConcurrentDevelopers = 2
	developer := pipeline.Config.Agents["developer"]
	developer.Instances = 2
	pipeline.Config.Agents["developer"] = developer
	pipeline.NewRunID = runstate.NewRunID
	return pipeline
}

// A continuation the pipeline refuses is reported as the failure it is, beside
// the record already saying the sweep took the run up: the run is still holding
// its slot with nothing serving it, which is the state this step exists to end.
func TestAContinuationThePipelineRefusesIsReportedAsAFailure(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
	first := usageLimitBackend(1, limit, approveVerdict)
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}

	clock := &pausingClock{now: resetsAt.Add(time.Minute)}
	refusals := 0
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Clock:     clock,
		Continue: func(context.Context, string, string) (Outcome, error) {
			refusals++
			return Outcome{}, errors.New("the provider is not installed")
		},
	}
	continuations, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(continuations) != 1 || !continuations[0].Continued || !strings.Contains(continuations[0].Failure, "the provider is not installed") {
		t.Fatalf("ContinueWaits() = %#v, want the refused continuation reported with the refusal", continuations)
	}
	recorded, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.SweepContinuations) != 1 || recorded.UsageLimitResetsAt == nil || recorded.Status != runstate.StatusRunning {
		t.Fatalf("recorded run = %#v, want the continuation recorded and the run still in flight and waiting", recorded)
	}

	// A sweep running beside the first reads the continuation just recorded for
	// this deadline and leaves the run to it, rather than recording a second
	// continuation and reporting the pipeline's refusal of it as a failure — so
	// repeating the sweep is as safe for this step as for every other.
	beside, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() beside the first error = %v", err)
	}
	if len(beside) != 1 || beside[0].Continued || beside[0].Failure != "" || !strings.Contains(beside[0].Detail, "its continuation is being entered") {
		t.Fatalf("ContinueWaits() beside the first = %#v, want the run left to the sweep that took it up", beside)
	}
	if refusals != 1 {
		t.Fatalf("Continue was called %d times, want once", refusals)
	}
	// Once that window has passed with the record still standing on the same
	// deadline, the continuation was refused rather than entered, and a later
	// sweep takes the run up again.
	clock.now = clock.now.Add(sweepContinuationEntry)
	later, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("later ContinueWaits() error = %v", err)
	}
	if len(later) != 1 || !later[0].Continued || refusals != 2 {
		t.Fatalf("later ContinueWaits() = %#v with %d refusal(s), want the run taken up again", later, refusals)
	}
	recorded, err = store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.SweepContinuations) != 2 {
		t.Fatalf("sweep continuations = %#v, want the second attempt recorded beside the first", recorded.SweepContinuations)
	}
}
