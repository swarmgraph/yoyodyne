package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// pausedAtChecks is the outcome a hosted run reports once it has stopped
// itself at its checks for the restart, as its pipeline would.
func pausedAtChecks(ctx context.Context, id string) (Outcome, error) {
	var drained RedeployDrain
	if !errors.As(context.Cause(ctx), &drained) {
		return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
	}
	return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: runstate.PhaseChecking, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
}

// waitedOnCheck is the first line the session recorded past its bound that
// names a wait on a running check.
func waitedOnCheck(sessions *recordedSessions) (recordedTransition, bool) {
	for _, transition := range sessions.recorded() {
		if transition.draining != nil && len(transition.draining.CheckWaits) > 0 {
			return transition, true
		}
	}
	return recordedTransition{}, false
}

// A check already running when a build is deployed is not started over
// because of it, however many builds follow: the session's drain bound runs
// out, a second build lands, and the run's 45-minute check goes on past both
// until it ends by itself. The run is then stopped before its next check, at
// its checks, so the session that comes back keeps what it passed. Meanwhile
// the session's own record says what it is waiting on: which run, which check,
// and until when.
func TestARestartLetsACheckRunningAcrossTwoNewBuildsFinish(t *testing.T) {
	t.Parallel()

	const id = "yoyodyne-racing"
	harness := newScheduleHarness(readyItems(id)...)
	harness.drainLimit = 15 * time.Minute
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	launched := began
	const limit = 45 * time.Minute
	running := make(chan context.Context, 1)
	finish := make(chan struct{})
	var builds atomic.Int32
	deploy := func() {
		builds.Add(1)
		deployment.deploy()
	}
	// stage is where the test has got to: 0 the run not yet seen, 1 the clock
	// moved to the bound, 2 a second build deployed and the clock moved well
	// past the bound, 3 the check let finish.
	var stage atomic.Int32
	var pullsPastSecond atomic.Int32
	var hosted context.Context
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 0 {
			// The first pull is the one that starts the run.
			return
		}
		switch stage.Load() {
		case 0:
			hosted = <-running
			stage.Store(1)
		case 1:
			h.mu.Lock()
			h.now = began.Add(h.drainLimit)
			h.mu.Unlock()
			if _, said := waitedOnCheck(sessions); said {
				deploy()
				h.mu.Lock()
				h.now = began.Add(40 * time.Minute)
				h.mu.Unlock()
				stage.Store(2)
			}
		case 2:
			if pullsPastSecond.Add(1) < 5 {
				return
			}
			if hosted.Err() != nil {
				t.Errorf("the running check was stopped before it ended: %v", context.Cause(hosted))
			}
			stage.Store(3)
			close(finish)
		}
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseChecking
		state.CheckStage = &runstate.CheckStage{StartedAt: began, BoundSeconds: int64((5 * time.Hour) / time.Second), Command: "make race", CheckLimitSeconds: int64(limit / time.Second), CheckStartedAt: &launched}
		h.inFlight[id] = state
		h.mu.Unlock()
		deploy()
		running <- ctx
		select {
		case <-finish:
		case <-ctx.Done():
			return pausedAtChecks(ctx, id)
		}
		// The check has ended by itself, and the stage is at its next boundary.
		if !yieldBetweenChecks(ctx) {
			t.Error("the run was not asked to stop before its next check")
			return h.complete(id), nil
		}
		return pausedAtChecks(ctx, id)
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if builds.Load() != 2 {
		t.Fatalf("builds deployed = %d, want two over the one check", builds.Load())
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted once the check ended: %s", schedule.Stopped, schedule.Render())
	}
	if elapsed := harness.clock().Sub(began); elapsed <= harness.drainLimit {
		t.Fatalf("session restarted %s after the deploy, want it to have waited past its %s bound for the check", elapsed, harness.drainLimit)
	}
	if len(schedule.Drain.Stopped) != 0 {
		t.Fatalf("drain stopped = %v, want the run with a check running left to stop itself", schedule.Drain.Stopped)
	}
	if got := schedule.Drain.ChecksWaited; len(got) != 1 || got[0] != id {
		t.Fatalf("checks waited = %v, want the run's check let finish", got)
	}
	started := schedule.Started[0]
	if started.Failure != "" || !started.Outcome.Paused || started.Outcome.RedeployStop == nil || started.Outcome.RedeployStop.Phase != runstate.PhaseChecking {
		t.Fatalf("started = %#v, want the run stopped at its checks for the session that comes back", started)
	}
	waited, said := waitedOnCheck(sessions)
	if !said {
		t.Fatal("no line the session wrote said it was waiting on the running check")
	}
	want := runstate.WatchCheckWait{RunID: "run-" + id, WorkItemID: id, Check: "make race", Until: launched.Add(limit)}
	if drain := waited.draining; len(drain.CheckWaits) != 1 || drain.CheckWaits[0] != want || drain.Checking != 1 || !drain.ChecksUntil.Equal(want.Until) {
		t.Fatalf("draining = %#v, want the wait on %#v", drain, want)
	}
	for _, words := range []string{"run-" + id, "make race", "not started over"} {
		if !strings.Contains(waited.reason, words) && !strings.Contains(waited.draining.Says(), words) {
			t.Fatalf("the line said %q (%s), want it to name %q", waited.reason, waited.draining.Says(), words)
		}
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "let finish rather than started over") || !strings.Contains(reason, id) {
		t.Fatalf("stopped reason = %q, want the run whose check was let finish named", reason)
	}
}

// The wait on a running check is bounded by that check's own limit: a check
// still going well past it is stopped at the restart like any other run.
func TestARestartStopsACheckStillRunningPastItsOwnLimit(t *testing.T) {
	t.Parallel()

	const id = "yoyodyne-overdue"
	harness := newScheduleHarness(readyItems(id)...)
	harness.drainLimit = 15 * time.Minute
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	// Launched 44 minutes before the deploy under a 45-minute limit, so by the
	// time the bound runs out the check is past its limit and the grace after.
	launched := began.Add(-44 * time.Minute)
	running := make(chan struct{}, 1)
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 0 {
			return
		}
		if pulls == 1 {
			<-running
			return
		}
		h.mu.Lock()
		h.now = began.Add(h.drainLimit)
		h.mu.Unlock()
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseChecking
		state.CheckStage = &runstate.CheckStage{StartedAt: launched, BoundSeconds: int64((5 * time.Hour) / time.Second), Command: "make race", CheckLimitSeconds: int64((45 * time.Minute) / time.Second), CheckStartedAt: &launched}
		h.inFlight[id] = state
		h.mu.Unlock()
		deployment.deploy()
		running <- struct{}{}
		<-ctx.Done()
		return pausedAtChecks(ctx, id)
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session restarted: %s", schedule.Stopped, schedule.Render())
	}
	if got := schedule.Drain.Stopped; len(got) != 1 || got[0] != id {
		t.Fatalf("drain stopped = %v, want the check past its own limit stopped at the bound", got)
	}
	if len(schedule.Drain.ChecksWaited) != 0 {
		t.Fatalf("checks waited = %v, want no wait on a check past its own limit", schedule.Drain.ChecksWaited)
	}
}

// A document landing run is never stopped for a restart, at whatever step it
// is: here its review is still going past the drain bound, and the session
// waits for it, says so, and restarts once it has landed.
func TestARestartWaitsForADocumentLandingRunAtItsReview(t *testing.T) {
	t.Parallel()

	const id = "yoyodyne-document"
	harness := newScheduleHarness(readyItems(id)...)
	harness.drainLimit = 15 * time.Minute
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	running := make(chan context.Context, 1)
	finish := make(chan struct{})
	var hosted context.Context
	var pullsPastBound atomic.Int32
	harness.onPull = func(h *scheduleHarness, pulls int) {
		switch {
		case pulls == 0:
		case pulls == 1:
			hosted = <-running
		case pulls == 2:
			h.mu.Lock()
			h.now = began.Add(h.drainLimit)
			h.mu.Unlock()
		case pullsPastBound.Add(1) == 5:
			if hosted.Err() != nil {
				t.Errorf("the document landing run was stopped: %v", context.Cause(hosted))
			}
			close(finish)
		}
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseReviewing
		document := waitingDesign("write-1").Document
		state.Document = &document
		h.inFlight[id] = state
		h.mu.Unlock()
		deployment.deploy()
		running <- ctx
		select {
		case <-finish:
		case <-ctx.Done():
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		}
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted once the document landed: %s", schedule.Stopped, schedule.Render())
	}
	if len(schedule.Drain.Stopped) != 0 {
		t.Fatalf("drain stopped = %v, want the document landing run left to finish", schedule.Drain.Stopped)
	}
	if started := schedule.Started[0]; started.Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("started = %#v, want the document landing run to have finished", started)
	}
	publishing := false
	for _, transition := range sessions.recorded() {
		if transition.draining != nil && transition.draining.BoundReached && transition.draining.Publishing == 1 &&
			strings.Contains(transition.draining.Says(), "document landing run(s) to finish") {
			publishing = true
		}
	}
	if !publishing {
		t.Fatal("no line the session wrote said it was waiting for the document landing run")
	}
}

// Past the bound a recurring pass starts no new document landing run, and one
// already going is counted until it ends; outside a pass nothing is counted.
func TestPassPublicationsStartNoNewDocumentLandingOnceClosed(t *testing.T) {
	t.Parallel()

	var outside *passPublications
	if !outside.begin(false) || outside.active() != 0 {
		t.Fatal("a document landing outside any pass was refused or counted")
	}
	publications := &passPublications{}
	if !publications.begin(false) || publications.active() != 1 {
		t.Fatalf("a new document landing in an open pass was refused, active = %d", publications.active())
	}
	publications.close()
	if publications.begin(false) {
		t.Fatal("a new document landing started after the bound ran out")
	}
	if !publications.begin(true) || publications.active() != 2 {
		t.Fatalf("a recorded document landing was not continued after the bound, active = %d", publications.active())
	}
	publications.end()
	publications.end()
	if publications.active() != 0 {
		t.Fatalf("active = %d after both ended, want none", publications.active())
	}
}

// A yield nobody asked for stops nothing; one asked stops the run with the
// restart as the cause, and asking again keeps the first moment.
func TestACheckYieldStopsTheRunOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	if yieldBetweenChecks(context.Background()) {
		t.Fatal("a run hosted with no yield stopped")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx, yield := withCheckYield(ctx, cancel)
	if yieldBetweenChecks(ctx) || ctx.Err() != nil {
		t.Fatal("a run nobody asked to stop stopped")
	}
	first := RedeployDrain{At: time.Date(2026, 10, 16, 9, 47, 0, 0, time.UTC), Bound: 15 * time.Minute, SessionID: "watch-1"}
	yield.ask(first)
	yield.ask(RedeployDrain{At: first.At.Add(time.Minute), Bound: 15 * time.Minute, SessionID: "watch-1"})
	if !yieldBetweenChecks(ctx) {
		t.Fatal("a run asked to stop did not")
	}
	if drained, ok := drainedForRedeploy(ctx); !ok || drained != first {
		t.Fatalf("cause = %v, want the first ask %+v", context.Cause(ctx), first)
	}
}

// A run whose hosting session asks it to stop for a restart while a check is
// running finishes that check, launches none after it, and stops at its
// checks with what it passed kept; the run picked up after the restart runs
// only the check that had not started, so nothing is run twice.
func TestARunAskedToStopForARestartFinishesItsRunningCheckAndStartsNoOther(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	dir := t.TempDir()
	at := func(name string) string { return "'" + filepath.Join(dir, name) + "'" }
	counted := keptChecksFixture{dir: dir}
	// The second check says it is running and then runs until it is released,
	// for at most a minute.
	commands := []string{
		"printf x >> " + at("first"),
		"printf x >> " + at("second") + " && touch " + at("running") + " && i=0 && while [ ! -f " + at("release") + " ] && [ $i -lt 1200 ]; do sleep 0.05; i=$((i+1)); done",
		"printf x >> " + at("third"),
	}
	develop := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	first := orchestratortest.RoleBackend(develop, approveVerdict)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx, yield := withCheckYield(ctx, cancel)
	asked := RedeployDrain{At: time.Date(2026, 10, 16, 9, 47, 0, 0, time.UTC), Bound: 15 * time.Minute, SessionID: "watch-before"}
	go func() {
		for {
			if _, err := os.Stat(filepath.Join(dir, "running")); err == nil {
				yield.ask(asked)
				// The check goes on after the ask, and is let end by itself.
				time.Sleep(100 * time.Millisecond)
				if ctx.Err() != nil {
					t.Errorf("the running check was stopped by the ask: %v", context.Cause(ctx))
				}
				_ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	paused, err := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, commands), first).Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a run stopped for a restart reported as a pause", err)
	}
	if !paused.Paused || paused.RedeployStop == nil || paused.RedeployStop.Phase != runstate.PhaseChecking {
		t.Fatalf("outcome = %#v, want a run paused at its checks", paused)
	}
	if got := []int{counted.runs(t, "first"), counted.runs(t, "second"), counted.runs(t, "third")}; !slices.Equal(got, []int{1, 1, 0}) {
		t.Fatalf("runs of each check = %v, want [1 1 0]: the running check finished and none launched after it", got)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.KeptChecks == nil || len(stopped.KeptChecks.Checks) != 2 || stopped.KeptChecks.Checks[1].Command != commands[1] {
		t.Fatalf("kept checks at the stop = %#v, want the first two, which passed", stopped.KeptChecks)
	}

	second := orchestratortest.RoleBackend(develop, approveVerdict)
	outcome, err := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, commands), second).Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the picked-up run did not finish the same change: %#v", outcome)
	}
	if got := []int{counted.runs(t, "first"), counted.runs(t, "second"), counted.runs(t, "third")}; !slices.Equal(got, []int{1, 1, 1}) {
		t.Fatalf("runs of each check = %v, want [1 1 1]: no check run twice", got)
	}
	if outcome.CheckStage == nil || !slices.Equal(outcome.CheckStage.Kept, commands[:2]) || !slices.Equal(outcome.CheckStage.Ran, commands[2:]) {
		t.Fatalf("check stage = %#v, want the first two kept and only the third run", outcome.CheckStage)
	}
}

// A pass of a session whose drain bound has run out starts no new document
// landing run: its role's confirmed design is left waiting, with no run
// recorded, for the session that comes back, whose first pull starts it and
// lands it through its reviewed run without the design being written again.
func TestAPassPastTheRestartBoundLeavesItsDocumentForTheSessionThatComesBack(t *testing.T) {
	repository, _, provider, pipeline, store := documentPipeline(t)
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	publications := &passPublications{}
	publications.close()
	if _, err := session.Send(withPassPublications(context.Background(), publications), "Write the design."); err != nil {
		t.Fatal(err)
	}
	recorded, err := store.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range recorded {
		if state.Document != nil {
			t.Fatalf("run %s was started for the document past the restart bound", state.RunID)
		}
	}
	waits, err := store.DocumentWaits()
	if err != nil || len(waits) != 1 {
		t.Fatalf("waits = %+v, %v; want the design left waiting for the next session", waits, err)
	}
	if publications.active() != 0 {
		t.Fatalf("active = %d, want nothing counted for a document that did not start", publications.active())
	}

	outcome, err := pipeline.PublishWaitingDocument(context.Background(), waits[0].Document)
	if err != nil {
		t.Fatalf("PublishWaitingDocument() error = %v", err)
	}
	landed, err := store.Load(outcome.RunID)
	if err != nil || landed.Status != runstate.StatusSucceeded || landed.Integration == nil {
		t.Fatalf("run = %+v, %v; want the design landed by the next session", landed, err)
	}
	if content := publicationGit(t, repository, "show", "main:docs/designs/recovery-design.md"); content != strings.TrimSpace(landed.Document.Candidate.Content) {
		t.Fatalf("published different bytes: %s", content)
	}
	if waits, err := store.DocumentWaits(); err != nil || len(waits) != 0 {
		t.Fatalf("waits = %+v, %v; want nothing left waiting", waits, err)
	}
}

// restartQueues is two merge queues for a restart to find: one whose
// candidate's checks are running, and one whose pass is at its review.
type restartQueues struct {
	checking, reviewing runstate.MergeQueueKey
	started             chan struct{}
	release             chan struct{}
	checkStopped        atomic.Bool
	reviewStopped       atomic.Bool
	passes              atomic.Int32
	at                  func() time.Time
}

func (q *restartQueues) Pending() ([]runstate.MergeQueueKey, error) {
	if q.passes.Load() >= 2 {
		return nil, nil
	}
	return []runstate.MergeQueueKey{q.checking, q.reviewing}, nil
}

func (q *restartQueues) Pass(ctx context.Context, key runstate.MergeQueueKey) runstate.MergeQueuePass {
	q.passes.Add(1)
	if key == q.reviewing {
		<-ctx.Done()
		q.reviewStopped.Store(true)
		return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassWaiting, Says: "stopped at its review"}
	}
	stages := queueCheckStagesOf(ctx)
	if !stages.begin(key, 3, q.at()) {
		return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassWaiting, Says: "no check stage started"}
	}
	defer stages.end(key)
	stages.started(key, "make race", q.at())
	stages.launched(key, 45*time.Minute, q.at())
	close(q.started)
	select {
	case <-q.release:
		return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassLanded, Says: "the candidate's checks passed and it landed"}
	case <-ctx.Done():
		q.checkStopped.Store(true)
		return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassWaiting, Says: "stopped in its checks"}
	}
}

func (q *restartQueues) TakeHeld(context.Context) MergeQueueHeldPass { return MergeQueueHeldPass{} }

// A merge queue candidate whose checks are running when the restart limit runs
// out is let finish them rather than having every check run again on the new
// build: the session waits for that pass, says which queue's check it is
// waiting on, and restarts once it has ended. A queue pass that is not in its
// checks is stopped at the limit as before, and no queue starts a new check
// stage past it.
func TestARestartLetsAMergeQueueCandidatesChecksFinish(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.drainLimit = 15 * time.Minute
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	queues := &restartQueues{
		checking:  runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"},
		reviewing: runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "release"},
		started:   make(chan struct{}),
		release:   make(chan struct{}),
		at:        harness.clock,
	}
	// With no work item ready the session sleeps between pulls; it goes on
	// watching through every sleep.
	harness.onSleep = func(*scheduleHarness, int) bool { return true }
	var stage atomic.Int32
	var pullsPastBound atomic.Int32
	harness.onPull = func(h *scheduleHarness, pulls int) {
		switch stage.Load() {
		case 0:
			if pulls == 0 {
				return
			}
			<-queues.started
			deployment.deploy()
			stage.Store(1)
		case 1:
			h.mu.Lock()
			h.now = began.Add(h.drainLimit)
			h.mu.Unlock()
			stage.Store(2)
		case 2:
			if pullsPastBound.Add(1) < 5 {
				return
			}
			if queues.checkStopped.Load() {
				t.Error("the candidate's checks were stopped at the restart limit")
			}
			stage.Store(3)
			close(queues.release)
		}
	}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.MergeQueues = queues
		return pull, err
	}
	scheduler := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted once the candidate's checks ended: %s", schedule.Stopped, schedule.Render())
	}
	if queues.checkStopped.Load() {
		t.Fatal("the candidate's checks were stopped, so they would run again on the new build")
	}
	if !queues.reviewStopped.Load() {
		t.Fatal("the queue pass at its review was not stopped at the restart limit")
	}
	if got := schedule.Drain.QueueChecksWaited; len(got) != 1 || got[0] != "main" {
		t.Fatalf("queue checks waited = %v, want the main queue's candidate", got)
	}
	said := false
	for _, transition := range sessions.recorded() {
		if drain := transition.draining; drain != nil && len(drain.CheckWaits) == 1 && drain.CheckWaits[0].Queue == "main" &&
			drain.CheckWaits[0].Generation == 3 && drain.CheckWaits[0].Check == "make race" &&
			strings.Contains(drain.Says(), "the merge queue for main, checking candidate generation 3, to finish make race") {
			said = true
		}
	}
	if !said {
		t.Fatal("no line the session wrote said it was waiting on the merge queue's running check")
	}
	if stages := (&queueCheckStages{}); !stages.begin(queues.checking, 4, began) {
		t.Fatal("a fresh record refused a check stage")
	}
	closed := &queueCheckStages{}
	closed.close()
	if closed.begin(queues.checking, 4, began) {
		t.Fatal("a check stage started past the restart limit")
	}
}
