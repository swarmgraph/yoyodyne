package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamclaim"
)

// fakeTeamClaims stands in for the shared remote's claims.
type fakeTeamClaims struct {
	mu       sync.Mutex
	takeErr  error
	previous string
	taken    []string
	named    []string
	released []string
	// claimedInTrackerAtTake is whether the tracker had been claimed when the
	// remote claim was taken, which must never be true.
	tracker                *orchestratortest.Tracker
	claimedInTrackerAtTake bool
}

func (f *fakeTeamClaims) Take(_ context.Context, workItemID, runID string) (runstate.TeamClaim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tracker != nil && f.tracker.Claimed {
		f.claimedInTrackerAtTake = true
	}
	if f.takeErr != nil {
		return runstate.TeamClaim{}, f.takeErr
	}
	f.taken = append(f.taken, runID)
	return runstate.TeamClaim{WorkItemID: workItemID, RunID: runID, PreviousHolder: f.previous}, nil
}

// TakeNamed is Take for an item the operator named, recorded apart so a test
// can tell which the pipeline asked for. It is never refused for the team
// pause, as the real one is not.
func (f *fakeTeamClaims) TakeNamed(ctx context.Context, workItemID, runID string) (runstate.TeamClaim, error) {
	f.mu.Lock()
	f.named = append(f.named, runID)
	takeErr := f.takeErr
	f.mu.Unlock()
	var paused teamclaim.PausedError
	if errors.As(takeErr, &paused) {
		f.mu.Lock()
		f.takeErr = nil
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.takeErr = takeErr; f.mu.Unlock() }()
	}
	return f.Take(ctx, workItemID, runID)
}

func (f *fakeTeamClaims) Release(_ context.Context, _, runID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, runID)
	return nil
}

// An item another factory holds is not started: nothing is reserved, the
// tracker is not claimed, and no provider is asked.
func TestThePipelineStartsNothingOnAnItemAnotherFactoryHolds(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.TeamClaims = &fakeTeamClaims{takeErr: teamclaim.HeldError{WorkItemID: "yoyodyne-task", Holder: "yoyodyne on beta"}}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	var held teamclaim.HeldError
	if !errors.As(err, &held) || held.Holder != "yoyodyne on beta" {
		t.Fatalf("Run() error = %v, want the item held by beta", err)
	}
	if outcome.RunID != "" || tracker.Claimed || len(provider.Requests) != 0 {
		t.Fatalf("outcome = %#v, claimed = %v, requests = %d, want nothing reserved, claimed or asked", outcome, tracker.Claimed, len(provider.Requests))
	}
	if incomplete, err := store.Incomplete(); err != nil || len(incomplete) != 0 {
		t.Fatalf("runs in flight = %v, %v, want none", incomplete, err)
	}
}

// The claim on the remote is taken before the tracker's, and a claim taken
// over from another factory is noted on the item naming that factory.
func TestTheRemoteClaimIsTakenBeforeTheTrackerClaimAndATakeoverIsNoted(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	claims := &fakeTeamClaims{tracker: tracker, previous: "yoyodyne on beta"}
	pipeline.TeamClaims = claims

	outcome, _ := pipeline.Run(context.Background(), tracker.Item.ID)
	if len(claims.taken) != 1 || claims.taken[0] != outcome.RunID {
		t.Fatalf("remote claims taken = %v, want one for run %s", claims.taken, outcome.RunID)
	}
	if claims.claimedInTrackerAtTake {
		t.Fatalf("the tracker was claimed before the claim on the remote was taken")
	}
	if !tracker.Claimed {
		t.Fatalf("the tracker was never claimed after the remote claim was taken")
	}
	if !strings.Contains(tracker.Notes, "took over the claim on this item from yoyodyne on beta") {
		t.Errorf("item notes = %q, want the earlier holder named", tracker.Notes)
	}
}

// The scheduler records an item held on the remote as a start it chose and did
// not make, naming the holder, and tries it again at the next poll.
func TestAnItemHeldOnTheRemoteIsSkippedNamingTheHolder(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	renewed := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	harness.run = func(_ *scheduleHarness, id string) (Outcome, error) {
		return Outcome{}, teamclaim.HeldError{WorkItemID: id, Holder: "yoyodyne on beta", RenewedAt: renewed}
	}
	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the chosen item reported", schedule.Started)
	}
	started := schedule.Started[0]
	if started.Failure != "" || !strings.Contains(started.Declined, "claimed on the shared remote by yoyodyne on beta") {
		t.Fatalf("started = %#v, want a declined start naming the holder", started)
	}
	if because := excludedBecause(started); !strings.Contains(because, "did not start it") || strings.Contains(because, "another process") {
		t.Fatalf("excluded because %q, want it to say the session did not start it", because)
	}
}

// While the team pause is set, work the harness chose is not started: nothing
// is reserved, the tracker is not claimed, and the scheduler records it as a
// start it chose and did not make, naming who set the pause.
func TestTheTeamPauseStartsNoWorkTheHarnessChose(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pause := teamclaim.Pause{SetBy: "mason", Factory: "yoyodyne on alpha"}
	claims := &fakeTeamClaims{takeErr: teamclaim.PausedError{WorkItemID: "yoyodyne-task", Pause: pause}}
	pipeline.TeamClaims = claims

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	var paused teamclaim.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("Run() error = %v, want the team pause", err)
	}
	if len(claims.named) != 0 {
		t.Fatalf("a harness selection was claimed as one the operator named")
	}
	if outcome.RunID != "" || tracker.Claimed || len(provider.Requests) != 0 {
		t.Fatalf("outcome = %#v, claimed = %v, requests = %d, want nothing reserved, claimed or asked", outcome, tracker.Claimed, len(provider.Requests))
	}
	if incomplete, err := store.Incomplete(); err != nil || len(incomplete) != 0 {
		t.Fatalf("runs in flight = %v, %v, want none", incomplete, err)
	}

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.run = func(_ *scheduleHarness, id string) (Outcome, error) {
		return Outcome{}, teamclaim.PausedError{WorkItemID: id, Pause: pause}
	}
	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the chosen item reported", schedule.Started)
	}
	started := schedule.Started[0]
	if started.Failure != "" || !strings.Contains(started.Declined, "team pause is set on the shared remote, by mason (yoyodyne on alpha)") {
		t.Fatalf("started = %#v, want a declined start naming who set the pause", started)
	}
}

// An item the operator named is his exception to the team pause, as it is to
// the intake hold: its claim is taken by the form that does not read the pause.
func TestAnItemTheOperatorNamedIsClaimedThroughTheTeamPause(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Selection = runstate.OperatorSelection("named at a terminal", time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	claims := &fakeTeamClaims{tracker: tracker, takeErr: teamclaim.PausedError{WorkItemID: "yoyodyne-task", Pause: teamclaim.Pause{SetBy: "mason"}}}
	pipeline.TeamClaims = claims

	outcome, _ := pipeline.Run(context.Background(), tracker.Item.ID)
	if len(claims.named) != 1 || claims.named[0] != outcome.RunID {
		t.Fatalf("named claims = %v, want one for run %s", claims.named, outcome.RunID)
	}
	if !tracker.Claimed {
		t.Fatalf("the operator's named item was not started under the team pause")
	}
}

// recordingTend captures what the watch loop says each claim's run holds.
type recordingTend struct {
	verdicts map[string]bool
	err      error
}

func (r *recordingTend) Tend(_ context.Context, holding teamclaim.Holding) (teamclaim.Upkeep, error) {
	r.verdicts = map[string]bool{}
	for _, claim := range []runstate.TeamClaim{
		{WorkItemID: "item-running", RunID: "run-in-flight"},
		{WorkItemID: "item-blocked", RunID: "run-ended-blocked"},
		{WorkItemID: "item-in-progress", RunID: "run-ended-claimed"},
		{WorkItemID: "item-done", RunID: "run-ended"},
	} {
		held, _ := holding(claim)
		r.verdicts[claim.WorkItemID] = held
	}
	return teamclaim.Upkeep{Renewed: []string{"item-running"}, Problems: []string{"the claim on item-x could not be renewed and will be tried again"}}, r.err
}

// The watch loop keeps a claim while its run is in flight or its item is still
// claimed or blocked here, which is a run waiting for a merge or stopped and
// kept, and releases every other; what it did and could not do is on the
// schedule.
func TestTheWatchLoopKeepsClaimsWhileTheirRunHoldsTheItem(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		beads.WorkItem{ID: "item-blocked", Title: "blocked", Status: "blocked", Priority: 2},
		beads.WorkItem{ID: "item-in-progress", Title: "in progress", Status: "in_progress", Priority: 2},
	)
	harness.inFlight["run-in-flight"] = runstate.State{RunID: "run-in-flight", WorkItemID: "item-running", Status: runstate.StatusRunning}
	tend := &recordingTend{}
	harness.teamClaims = tend

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := map[string]bool{"item-running": true, "item-blocked": true, "item-in-progress": true, "item-done": false}
	for item, held := range want {
		if tend.verdicts[item] != held {
			t.Errorf("%s held = %v, want %v (all: %v)", item, tend.verdicts[item], held, tend.verdicts)
		}
	}
	if schedule.TeamClaims == nil || len(schedule.TeamClaims.Renewed) != 1 || !strings.Contains(schedule.TeamClaimProblem, "tried again") {
		t.Fatalf("schedule team claims = %#v, problem %q, want the upkeep and its problem recorded", schedule.TeamClaims, schedule.TeamClaimProblem)
	}
}

// What the upkeep did that a person is owed is in the schedule's text, not only
// its JSON: a claim released, a claim found taken over by another factory, and
// a renewal that could not reach the remote.
func TestTheScheduleSaysWhatBecameOfTheClaimsOnTheRemote(t *testing.T) {
	t.Parallel()

	lost := "the claim on item-lost was no longer this machine's when it came to renew it, so it was left alone; it is held by yoyodyne on beta"
	schedule := Schedule{
		TeamClaims: &teamclaim.Upkeep{
			Renewed:  []string{"item-renewed"},
			Released: []string{"item-ended"},
			Lost:     []string{lost},
		},
		TeamClaimProblem: "the claim on item-x could not be renewed and will be tried again: could not resolve host",
	}
	rendered := schedule.Render()
	for _, want := range []string{
		"the claim on item-ended on the shared remote was released, because its run no longer holds the item",
		lost,
		"the claim on item-x could not be renewed and will be tried again",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered schedule lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "item-renewed") {
		t.Errorf("rendered schedule names a routine renewal:\n%s", rendered)
	}
}
