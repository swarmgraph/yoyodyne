package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runrecover"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// interruptedAt puts the fixture's paused run back as a process that died
// outright leaves it: in flight at phase, with no wait and no stop recorded,
// and with whatever stop is given. It returns the record as saved.
func (f dependencyPhaseFixture) interruptedAt(t *testing.T, phase runstate.Phase, stop *runstate.RedeployStop) runstate.State {
	t.Helper()
	state := f.load(t)
	state.DependencyPause = nil
	state.Phase = phase
	state.RedeployStop = stop
	if err := f.store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return f.load(t)
}

// developerResumes runs the item in a fresh pipeline and reports the session
// and directory the one developer invocation was given.
func (f dependencyPhaseFixture) developerResumes(t *testing.T) (Outcome, *orchestratortest.Backend, string, string) {
	t.Helper()
	var session, directory string
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		session, directory = request.SessionID, request.WorkingDirectory
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("finished\n"), 0o600)
	}, approveVerdict)
	outcome, err := f.pipeline(t, provider).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return outcome, provider, session, directory
}

// A developer a redeploy put down, whose item was made to wait on other work
// while it was down, is given a durable wait by the next dispatch of its item
// rather than a pause that is never written: at the phase it was interrupted
// at, holding no developer slot, with nothing else on it changed. When that
// work closes, the same run is continued — the same developer, session,
// worktree and branch — and no counter has moved.
func TestAWorkerlessRunWhoseItemGainedADependencyIsGivenADurableWaitAndResumes(t *testing.T) {
	t.Parallel()

	fixture := newDependencyPhaseFixture(t)
	stop := &runstate.RedeployStop{At: time.Now().UTC().Add(-time.Hour), Phase: runstate.PhaseDeveloping, BoundSeconds: 900, SessionID: "watch-before"}
	before := fixture.interruptedAt(t, runstate.PhaseDeveloping, stop)
	if !before.HoldsDeveloperSlot() {
		t.Fatal("the interrupted run should hold its developer slot before the dispatch")
	}

	// The item still waits on yoyodyne-blocker, which the fixture's developer linked.
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error {
		t.Error("a provider was invoked for an item that waits on unfinished work")
		return nil
	}, approveVerdict)
	paused, err := fixture.pipeline(t, provider).Run(context.Background(), fixture.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !paused.Paused || paused.PausedByDependency == nil || paused.RunID != before.RunID {
		t.Fatalf("outcome = %#v, want the run named as waiting on its dependency", paused)
	}
	waiting := fixture.load(t)
	if waiting.DependencyPause == nil || waiting.DependencyPause.Summary() != "yoyodyne-blocker" {
		t.Fatalf("the wait was not made durable: %#v", waiting.DependencyPause)
	}
	if waiting.Phase != runstate.PhaseDeveloping || waiting.HoldsDeveloperSlot() {
		t.Fatalf("waiting run is at %q holding a slot %t, want developing and no slot", waiting.Phase, waiting.HoldsDeveloperSlot())
	}
	if waiting.RedeployStop == nil || waiting.ProviderSessionID != before.ProviderSessionID || waiting.Branch != before.Branch ||
		waiting.RepairAttempts != before.RepairAttempts || waiting.ReviewRounds != before.ReviewRounds {
		t.Fatalf("the wait changed what the run keeps:\nbefore %#v\nafter  %#v", before, waiting)
	}
	if !readmodel.AwaitingContinuation(waiting) {
		t.Fatal("the claim audit would read the waiting run as one nothing continues")
	}

	fixture.release()
	resumed, developers, session, directory := fixture.developerResumes(t)
	if resumed.RunID != before.RunID {
		t.Fatalf("resumed run = %q, want %q continued rather than re-run", resumed.RunID, before.RunID)
	}
	if len(developers.RequestsForRole(domain.RoleDeveloper)) != 1 || session != before.ProviderSessionID || directory != before.WorktreePath {
		t.Fatalf("developer resumed %d times in session %q at %q, want once in %q at %q",
			len(developers.RequestsForRole(domain.RoleDeveloper)), session, directory, before.ProviderSessionID, before.WorktreePath)
	}
	finished := fixture.load(t)
	if finished.RepairAttempts != before.RepairAttempts || finished.Readopted == nil {
		t.Fatalf("finished run = %#v, want no repair spent and the redeploy stop kept as the re-adoption", finished)
	}
}

// A run another process holds is that process's to pause, and the dispatch
// leaves its record exactly as it was.
func TestADispatchLeavesAWaitToTheProcessHoldingTheRun(t *testing.T) {
	t.Parallel()

	fixture := newDependencyPhaseFixture(t)
	before := fixture.interruptedAt(t, runstate.PhaseDeveloping, nil)
	_, lease, err := fixture.store.AdoptRun(context.Background(), before.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	defer lease.Release()

	provider := orchestratortest.RoleBackend(nil, approveVerdict)
	paused, err := fixture.pipeline(t, provider).Run(context.Background(), fixture.tracker.Item.ID)
	if err != nil || !paused.Paused || paused.RunID != "" {
		t.Fatalf("Run() = %#v, %v; want an unnamed pause that touched nothing", paused, err)
	}
	if after := fixture.load(t); after.DependencyPause != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("a held run was written: %#v", after.DependencyPause)
	}
}

// A run a process died under, with no stop recorded, is not one the pipeline
// takes up by itself. Once the development manager asks for it through
// run.recover it is: the same developer in the same session, the request marked
// taken up, and the hold it carried cleared.
func TestARunAskedToContinueIsTakenUpByThePipeline(t *testing.T) {
	t.Parallel()

	fixture := newDependencyPhaseFixture(t)
	fixture.release()
	before := fixture.interruptedAt(t, runstate.PhaseDeveloping, nil)

	refused := orchestratortest.RoleBackend(nil, approveVerdict)
	if _, err := fixture.pipeline(t, refused).Run(context.Background(), fixture.tracker.Item.ID); !errors.As(err, &ExistingRunError{}) {
		t.Fatalf("Run() of an interrupted run nobody asked about = %v, want it refused as a run in flight", err)
	}

	if err := RecoveryContinuable(before); err != nil {
		t.Fatalf("RecoveryContinuable() = %v, want the interrupted run continuable", err)
	}
	asked := before
	at := time.Now().UTC()
	asked.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: "dm-1", Action: runstate.RecoverContinue, Reason: "nothing blocks it now", RequestedAt: at}
	asked.RecoveryHold = &runstate.RecoveryHold{RequestID: "dm-0", Owner: domain.RoleDevelopmentManager, NextAction: "continue it", FollowUp: "next pass", At: at}
	if err := fixture.store.Save(asked); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !readmodel.AwaitingContinuation(fixture.load(t)) {
		t.Fatal("the claim audit would settle a run the development manager asked to have continued")
	}

	resumed, developers, session, directory := fixture.developerResumes(t)
	if resumed.RunID != before.RunID || len(developers.RequestsForRole(domain.RoleDeveloper)) != 1 || session != before.ProviderSessionID || directory != before.WorktreePath {
		t.Fatalf("resumed %q with %d developer invocations in %q at %q, want %q once in %q at %q",
			resumed.RunID, len(developers.RequestsForRole(domain.RoleDeveloper)), session, directory, before.RunID, before.ProviderSessionID, before.WorktreePath)
	}
	finished := fixture.load(t)
	if finished.RecoveryContinuation == nil || finished.RecoveryContinuation.TakenUpAt == nil || finished.RecoveryHold != nil {
		t.Fatalf("after the resume the request is %#v and the hold %#v; want it taken up and the hold cleared", finished.RecoveryContinuation, finished.RecoveryHold)
	}
	if finished.RepairAttempts != before.RepairAttempts {
		t.Fatalf("repair attempts %d→%d across a continuation", before.RepairAttempts, finished.RepairAttempts)
	}
}

// What can be continued, and what can have its merge finished, is said with
// why not where it cannot.
func TestRecoveryContinuableAndFinishableSayWhyNot(t *testing.T) {
	t.Parallel()

	interrupted := runstate.State{RunID: "run-" + strings.Repeat("d", 32), Status: runstate.StatusRunning, Phase: runstate.PhaseReviewing,
		WorktreePath: "/w", Branch: "b", BaseCommit: "c", ProviderSessionID: "s"}
	if err := RecoveryContinuable(interrupted); err != nil {
		t.Fatalf("RecoveryContinuable(interrupted review) = %v", err)
	}
	for name, change := range map[string]func(*runstate.State){
		"ended":       func(s *runstate.State) { s.Status = runstate.StatusFailed },
		"integrating": func(s *runstate.State) { s.Phase = runstate.PhaseIntegrating },
		"no session":  func(s *runstate.State) { s.ProviderSessionID = "" },
		"no worktree": func(s *runstate.State) { s.WorktreePath = "" },
		"a document":  func(s *runstate.State) { s.Document = &runstate.DocumentPublication{} },
	} {
		state := interrupted
		change(&state)
		if err := RecoveryContinuable(state); err == nil {
			t.Errorf("RecoveryContinuable(%s) accepted it", name)
		}
	}

	stopped := approvedStoppedState()
	if err := RecoveryFinishable(stopped); err != nil {
		t.Fatalf("RecoveryFinishable(approved, stopped at integration) = %v", err)
	}
	for name, change := range map[string]func(*runstate.State){
		"in flight":   func(s *runstate.State) { s.Status = runstate.StatusRunning },
		"no approval": func(s *runstate.State) { s.ReviewDecision = runstate.ReviewRepair },
		"no stop":     func(s *runstate.State) { s.IntegrationStop = nil },
		"promoted": func(s *runstate.State) {
			s.Integration = &runstate.Integration{TargetBranch: "main", TargetCommit: "abc"}
		},
		"no session":  func(s *runstate.State) { s.ProviderSessionID = "" },
		"no worktree": func(s *runstate.State) { s.WorktreePath = "" },
	} {
		state := stopped
		change(&state)
		if err := RecoveryFinishable(state); err == nil {
			t.Errorf("RecoveryFinishable(%s) accepted it", name)
		}
	}
}

// A watching session picks up a run in flight the development manager asked to
// have continued, whichever session last held it and with no redeploy stop on
// it, and says why it did; a run nobody asked about is left to its own path.
func TestAWatchingSessionReadoptsARunTheDevelopmentManagerAskedToContinue(t *testing.T) {
	t.Parallel()

	for _, asked := range []bool{true, false} {
		harness := newScheduleHarness()
		harness.capacity = 2
		state := runstate.State{RunID: "run-asked", WorkItemID: "yoyodyne-one", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping,
			WorktreePath: "/w", Branch: "b", BaseCommit: "c", ProviderSessionID: "s"}
		if asked {
			state.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: "dm-7", Action: runstate.RecoverContinue, Reason: "the redeploy is over", RequestedAt: harness.now}
		}
		harness.inFlight["yoyodyne-one"] = state
		tries := 0
		harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
			tries++
			return h.complete(id), nil
		}
		harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 1 }
		schedule, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}).Schedule(context.Background())
		if err != nil {
			t.Fatalf("Schedule() error = %v", err)
		}
		if !asked {
			if tries != 0 {
				t.Fatalf("a run nobody asked about was re-adopted %d times", tries)
			}
			continue
		}
		if tries != 1 || len(schedule.Started) != 1 || schedule.Started[0].Readopted != "run-asked" {
			t.Fatalf("tries = %d, started = %#v; want the asked run re-adopted once", tries, schedule.Started)
		}
		if reason := schedule.Started[0].Reason; !strings.Contains(reason, "run.recover (request dm-7") || !strings.Contains(reason, "the redeploy is over") {
			t.Fatalf("reason = %q, want the request and its reasoning named", reason)
		}
	}
}

// A run carrying the development manager's unresolved hold, or her request to
// continue it, is not settled by reconciliation as a park nothing serves,
// however long its record has sat still.
func TestReconcileLeavesARunTheDevelopmentManagerAnswered(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{"hold", "continue"} {
		repository, tracker, _, pipeline, store := automaticFixture(t)
		holds := newOperatorHoldStore(t)
		pipeline.Holds = holds
		provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
			if _, err := holds.Hold(time.Now()); err != nil {
				return err
			}
			return writeFeature(request)
		}, approveVerdict)
		pipeline.Backend = provider
		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err != nil || !outcome.Paused {
			t.Fatalf("Run() = %+v, error = %v; want the spending hold to park the run", outcome, err)
		}
		if _, _, err := holds.Release(); err != nil {
			t.Fatal(err)
		}
		state, err := store.Load(outcome.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if answer == "hold" {
			state.RecoveryHold = &runstate.RecoveryHold{RequestID: "dm-1", Owner: domain.RoleDevelopmentManager, NextAction: "ask the architect", FollowUp: "at the next pass", At: state.UpdatedAt}
		} else {
			state.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: "dm-2", Action: runstate.RecoverContinue, Reason: "continue it", RequestedAt: state.UpdatedAt}
		}
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}
		reconciler := Reconciler{
			Tracker: tracker, Store: store, Holds: holds,
			Worktrees: newObserver(t, repository, filepath.Dir(state.WorktreePath)),
			Clock:     fixedClock{at: state.UpdatedAt.Add(DefaultVanishedGrace + time.Hour)},
		}
		results, err := reconciler.Reconcile(context.Background())
		if err != nil || len(results) != 1 || results[0].Action != ActionResumable {
			t.Fatalf("Reconcile() with a %s = %+v, error = %v; want the run left resumable", answer, results, err)
		}
		if after, err := store.Load(outcome.RunID); err != nil || !after.Status.InFlight() {
			t.Fatalf("after reconcile the run is %v (%v), want it still in flight", after.Status, err)
		}
	}
}

// A merge the development manager asked through run.recover to have finished
// is fired by the work session's carry-out through the integration resumer,
// which marks the request taken up; a refusal is written onto the request, so
// it is not fired again until she asks again.
func TestAFinishedMergeAskedForIsCarriedOutAndARefusalIsRecorded(t *testing.T) {
	t.Parallel()

	for _, refuse := range []bool{false, true} {
		state := approvedStoppedState()
		state.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: "dm-finish", Action: runstate.RecoverFinishMerge, Reason: "the checkout is clean again", RequestedAt: docketedNow}
		harness := newResumeHarness(t, state)
		if refuse {
			harness.ownership.ReadyErr = fmt.Errorf("%w: primary repository has uncommitted changes: AGENTS.md", gitworktree.ErrPrimaryNotReady)
		}
		carry := CarryOut{Docket: harness.docket, Decisions: harness.runs.Triage(), Reruns: harness.runs.Reruns(), Runs: harness.runs,
			Integrations: RecoveredIntegrations{Runs: harness.runs, Resumer: harness.resumer()}}
		tasks, err := carry.Outstanding()
		if err != nil || len(tasks) != 1 || tasks[0].Decision != DecisionFinishMerge || tasks[0].RunID != docketedRunID {
			t.Fatalf("Outstanding() = %#v, %v; want the asked-for merge offered", tasks, err)
		}
		carried, _, _ := carry.Carry(context.Background(), tasks[0])
		after := harness.reload(t)
		if refuse {
			if carried.Carried || after.RecoveryContinuation == nil || !strings.Contains(after.RecoveryContinuation.Refused, "AGENTS.md") {
				t.Fatalf("carried %#v, request %#v; want the refusal written onto the request", carried, after.RecoveryContinuation)
			}
			if again, err := carry.Outstanding(); err != nil || len(again) != 0 {
				t.Fatalf("Outstanding() after the refusal = %#v, %v; want nothing offered until she asks again", again, err)
			}
			continue
		}
		if !carried.Carried || len(harness.started) != 1 {
			t.Fatalf("carried = %#v, started = %#v; want the run resumed at its promotion", carried, harness.started)
		}
		if after.RecoveryContinuation == nil || after.RecoveryContinuation.TakenUpAt == nil || after.Phase != runstate.PhaseIntegrating {
			t.Fatalf("after the carry-out the request is %#v at %q; want it taken up with the run at its promotion", after.RecoveryContinuation, after.Phase)
		}
	}
}

// A request to finish a merge that the recovery tool accepted is carried out
// by a work session that started after it was accepted, and after an earlier
// session found every developer slot occupied and wrote that onto the request
// and the item. Two shapes are tried. In one the development manager records a
// wait beside the request, as a triage decision about the run and a closure of
// its stoppage: the wait does not hide the request, because the request is what
// she is waiting on. In the other the docket never recorded the stoppage, so
// the request is reachable only from the run, which is walked too.
func TestAFinishMergeRequestIsCarriedOutByASessionStartedAfterItWasAccepted(t *testing.T) {
	t.Parallel()

	for _, shape := range []string{"a wait beside it", "never docketed"} {
		root := t.TempDir()
		harness := newResumeHarness(t, approvedStoppedState())
		harness.capacity = 1
		docketRoot := harness.runsRoot
		if shape == "never docketed" {
			empty, err := runstate.NewDocketStore(root, "yoyodyne")
			if err != nil {
				t.Fatalf("NewDocketStore() error = %v", err)
			}
			harness.docket, docketRoot = empty, root
		}
		occupant := approvedStoppedState()
		occupant.RunID, occupant.WorkItemID = "run-"+strings.Repeat("e", 32), "yoyodyne-occupant"
		occupant.Status, occupant.Phase, occupant.CompletedAt = runstate.StatusRunning, runstate.PhaseDeveloping, nil
		occupant.IntegrationStop, occupant.Environmental, occupant.Failure = nil, nil, ""
		if err := harness.runs.Create(occupant); err != nil {
			t.Fatalf("Create(occupant) error = %v", err)
		}

		// The development manager asks, through the recovery tool itself.
		observations, err := runstate.NewHealthObservationStore(root, "yoyodyne")
		if err != nil {
			t.Fatalf("NewHealthObservationStore() error = %v", err)
		}
		askedAt := docketedNow.Add(30 * time.Second)
		if err := observations.Save("health-finish", askedAt.Add(-time.Minute), json.RawMessage(`{}`)); err != nil {
			t.Fatalf("Save(observation) error = %v", err)
		}
		recoverer := runrecover.Recoverer{Runs: harness.runs, Observations: observations, Finishable: RecoveryFinishable, Now: func() time.Time { return askedAt }}
		accepted := recoverer.Recover(context.Background(), domain.RoleDevelopmentManager, runrecover.Request{
			ID: "dm-finish", Run: docketedRunID, Observation: "health-finish", ExpectStatus: runstate.StatusFailed, ExpectPhase: runstate.PhaseIntegrating,
			Action: runstate.RecoverFinishMerge, Reason: "approved with its checks passing; only the replay onto a moved main ran out of time",
		})
		if accepted.Outcome != runrecover.Applied {
			t.Fatalf("%s: Recover() = %#v, want the request accepted", shape, accepted)
		}

		// The first session finds every slot occupied, and says so on the run and the item.
		first := CarryOut{Docket: harness.docket, Decisions: harness.runs.Triage(), Reruns: harness.runs.Reruns(), Runs: harness.runs,
			Integrations: RecoveredIntegrations{Runs: harness.runs, Resumer: harness.resumer(), Items: harness.tracker, Clock: docketClock{}}}
		tasks, err := first.Outstanding()
		if err != nil || len(tasks) != 1 || tasks[0].Decision != DecisionFinishMerge {
			t.Fatalf("%s: first session Outstanding() = %#v, %v; want the request offered", shape, tasks, err)
		}
		if carried, _, _ := first.Carry(context.Background(), tasks[0]); carried.Carried || !carried.Waiting || carried.Gate != runstate.TriageGateCapacity {
			t.Fatalf("%s: first session Carry() = %#v, want it waiting on a developer slot", shape, carried)
		}
		waiting := harness.reload(t).RecoveryContinuation
		if !waiting.Outstanding() || !strings.Contains(waiting.Waiting, "every developer slot is occupied") || waiting.WaitingSince == nil {
			t.Fatalf("%s: after the wait the request is %#v, want it outstanding and saying what it waits on", shape, waiting)
		}
		if !strings.Contains(harness.tracker.Notes, "request dm-finish") || !strings.Contains(harness.tracker.Notes, "is waiting") {
			t.Fatalf("%s: item notes = %q, want the wait noted on the item", shape, harness.tracker.Notes)
		}

		if shape == "a wait beside it" {
			if _, err := harness.runs.Triage().RecordDecision(context.Background(), docketedItem,
				triageDecided(runstate.TriageDecisionWait, docketedRunID), docketedNow.Add(time.Minute)); err != nil {
				t.Fatalf("RecordDecision() error = %v", err)
			}
			if _, err := harness.docket.Close(triage.Closure{SchemaVersion: triage.ClosureSchemaVersion, Key: triage.Key(triage.ClassStoppedRun, docketedRunID),
				ProductID: "yoyodyne", RunID: docketedRunID, WorkItemID: docketedItem, Decision: runstate.TriageDecisionWait,
				Reason: "the harness has been asked to finish the merge", DecidedBy: "the development manager", ClosedAt: docketedNow.Add(time.Minute),
				RevisitAfter: docketedNow.Add(3 * time.Hour)}); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		}

		// The occupant finishes, and a session started afterwards opens the same
		// stores afresh: nothing the first session held carries over.
		done, err := harness.runs.Load(occupant.RunID)
		if err != nil {
			t.Fatalf("Load(occupant) error = %v", err)
		}
		completed := docketedNow.Add(2 * time.Minute)
		done.Status, done.CompletedAt = runstate.StatusSucceeded, &completed
		if err := harness.runs.Save(done); err != nil {
			t.Fatalf("Save(occupant) error = %v", err)
		}
		restarted, err := runstate.NewStore(harness.runsRoot, "yoyodyne")
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		docket, err := runstate.NewDocketStore(docketRoot, "yoyodyne")
		if err != nil {
			t.Fatalf("NewDocketStore() error = %v", err)
		}
		harness.runs, harness.docket = restarted, docket
		later := docketClockAt{at: docketedNow.Add(5 * time.Minute)}
		resumer := harness.resumer()
		resumer.Clock = later
		second := CarryOut{Docket: docket, Decisions: restarted.Triage(), Reruns: restarted.Reruns(), Runs: restarted, Clock: later,
			Integrations: RecoveredIntegrations{Runs: restarted, Resumer: resumer, Items: harness.tracker, Clock: later}}
		tasks, err = second.Outstanding()
		if err != nil || len(tasks) != 1 || tasks[0].Decision != DecisionFinishMerge || tasks[0].RunID != docketedRunID {
			t.Fatalf("%s: restarted session Outstanding() = %#v, %v; want the request offered", shape, tasks, err)
		}
		carried, _, _ := second.Carry(context.Background(), tasks[0])
		if !carried.Carried || len(harness.started) != 1 || harness.started[0].runID != docketedRunID {
			t.Fatalf("%s: restarted session Carry() = %#v, started %#v; want the merge finished", shape, carried, harness.started)
		}
		after := harness.reload(t)
		if after.RecoveryContinuation == nil || after.RecoveryContinuation.TakenUpAt == nil || after.RecoveryContinuation.Waiting != "" || after.Phase != runstate.PhaseIntegrating {
			t.Fatalf("%s: after the restart the request is %#v at %q; want it taken up with the run at its promotion", shape, after.RecoveryContinuation, after.Phase)
		}
		if !strings.Contains(harness.tracker.Notes, "run.recover (request dm-finish)") {
			t.Fatalf("%s: item notes = %q, want the resumption noted on the item naming the request", shape, harness.tracker.Notes)
		}
		if again, err := second.Outstanding(); err != nil || len(again) != 0 {
			t.Fatalf("%s: Outstanding() after the merge = %#v, %v; want nothing offered again", shape, again, err)
		}
	}
}

// Which triage decision about a run replaces a request to finish its merge:
// one recorded at or after the request, other than a wait. A decision
// recorded before it is one the request decided past.
func TestOnlyALaterDecisionOtherThanAWaitReplacesAFinishMergeRequest(t *testing.T) {
	t.Parallel()

	asked := runstate.RecoveryContinuation{RequestID: "dm-finish", Action: runstate.RecoverFinishMerge, RequestedAt: docketedNow}
	for _, tried := range []struct {
		decision   string
		at         time.Time
		supersedes bool
	}{
		{runstate.TriageDecisionWait, docketedNow.Add(time.Hour), false},
		{runstate.TriageDecisionRepair, docketedNow.Add(-time.Hour), false},
		{runstate.TriageDecisionRerun, docketedNow.Add(-time.Hour), false},
		{runstate.TriageDecisionRepair, docketedNow.Add(time.Hour), true},
		{runstate.TriageDecisionRerun, docketedNow, true},
		{runstate.TriageDecisionEscalate, docketedNow.Add(time.Hour), true},
		{runstate.TriageDecisionProceed, docketedNow.Add(time.Hour), false},
	} {
		decision := triageDecided(tried.decision, docketedRunID)
		decision.DecidedAt = tried.at
		if got := supersedesFinishMerge(decision, asked); got != tried.supersedes {
			t.Errorf("a %s decided %s the request: supersedes = %t, want %t", tried.decision, tried.at.Sub(docketedNow), got, tried.supersedes)
		}
	}
}

// A request the integration resumer refuses is refused on the run and noted on
// the item, and is not offered again.
func TestARefusedFinishMergeRequestIsNotedOnTheItem(t *testing.T) {
	t.Parallel()

	state := approvedStoppedState()
	state.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: "dm-finish", Action: runstate.RecoverFinishMerge, Reason: "finish it", RequestedAt: docketedNow}
	harness := newResumeHarness(t, state)
	harness.ownership.ReadyErr = fmt.Errorf("%w: primary repository has uncommitted changes: AGENTS.md", gitworktree.ErrPrimaryNotReady)
	carry := CarryOut{Docket: harness.docket, Decisions: harness.runs.Triage(), Reruns: harness.runs.Reruns(), Runs: harness.runs,
		Integrations: RecoveredIntegrations{Runs: harness.runs, Resumer: harness.resumer(), Items: harness.tracker}}
	tasks, err := carry.Outstanding()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Outstanding() = %#v, %v; want the request offered", tasks, err)
	}
	if carried, _, _ := carry.Carry(context.Background(), tasks[0]); carried.Carried {
		t.Fatalf("Carry() = %#v, want it refused", carried)
	}
	if !strings.Contains(harness.tracker.Notes, "did not finish the merge") || !strings.Contains(harness.tracker.Notes, "AGENTS.md") {
		t.Fatalf("item notes = %q, want the refusal and its reason noted", harness.tracker.Notes)
	}
}

// A directive pause recorded on a run whose developer had not finished keeps it
// at developing, as the dependency wait does, so a continuation hands the
// developer back its attempt rather than starting the checks on unfinished work.
func TestADirectivePauseKeepsAnInterruptedDeveloperAtDeveloping(t *testing.T) {
	t.Parallel()

	fixture := newDependencyPhaseFixture(t)
	before := fixture.interruptedAt(t, runstate.PhaseDeveloping, nil)
	provider := orchestratortest.RoleBackend(nil, approveVerdict)
	run := &activeRun{pipeline: fixture.pipeline(t, provider), state: before}
	err := run.recordDirectivePause(directive.Directive{ID: "directive-1", Kind: directive.KindAmbiguous, Unresolved: "the operator is rewriting the item"})
	var paused directivePause
	if !errors.As(err, &paused) {
		t.Fatalf("recordDirectivePause() error = %v, want the pause reported", err)
	}
	after := fixture.load(t)
	if after.Phase != runstate.PhaseDeveloping || after.DirectivePause == nil {
		t.Fatalf("saved phase %q with pause %#v, want developing with the pause recorded", after.Phase, after.DirectivePause)
	}
}

// A watching session picks up a run parked on a tracker read that went
// unanswered, whose process has exited, at its next pull, and says why; the
// dispatch re-reads the tracker before it takes the run. A park on a tracker
// write is the process's own wait and is not picked up.
func TestAWatchingSessionReadoptsARunParkedOnATrackerRead(t *testing.T) {
	t.Parallel()

	for _, boundary := range []string{runstate.RetryDependencyRead, runstate.RetryTrackerWrite} {
		harness := newScheduleHarness()
		harness.capacity = 2
		harness.inFlight["yoyodyne-one"] = runstate.State{RunID: "run-parked", WorkItemID: "yoyodyne-one", Status: runstate.StatusRunning, Phase: runstate.PhaseReviewing,
			WorktreePath: "/w", Branch: "b", BaseCommit: "c", ProviderSessionID: "s",
			TrackerPause: &runstate.TrackerPause{Boundary: boundary, Attempts: 3, WaitedSeconds: 600, Failure: "bd show was cancelled"}}
		tries := 0
		harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
			tries++
			return h.complete(id), nil
		}
		harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 1 }
		schedule, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}).Schedule(context.Background())
		if err != nil {
			t.Fatalf("Schedule() error = %v", err)
		}
		if boundary == runstate.RetryTrackerWrite {
			if tries != 0 {
				t.Fatalf("a park on a tracker write was re-adopted %d times", tries)
			}
			continue
		}
		if tries != 1 || len(schedule.Started) != 1 || schedule.Started[0].Readopted != "run-parked" {
			t.Fatalf("tries = %d, started = %#v; want the parked run re-adopted once", tries, schedule.Started)
		}
		if reason := schedule.Started[0].Reason; !strings.Contains(reason, "parked at its reviewing phase") || !strings.Contains(reason, "reading what this item waits on") {
			t.Fatalf("reason = %q, want the park and its phase named", reason)
		}
	}
}

// A dispatch of an item that waits on unfinished work leaves alone a run with
// no process behind it that was interrupted inside its integration: its change
// may already be on the target, and reconciliation settles that step, which a
// wait would hide from it. Nothing is written on the run.
func TestADispatchPutsNoWaitOnARunInterruptedInsideItsIntegration(t *testing.T) {
	t.Parallel()

	fixture := newDependencyPhaseFixture(t)
	before := fixture.interruptedAt(t, runstate.PhaseIntegrating, nil)
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error {
		t.Error("a provider was invoked for an item that waits on unfinished work")
		return nil
	}, approveVerdict)
	paused, err := fixture.pipeline(t, provider).Run(context.Background(), fixture.tracker.Item.ID)
	if err != nil || !paused.Paused || paused.PausedByDependency == nil {
		t.Fatalf("Run() = %#v, %v; want the item reported as waiting", paused, err)
	}
	after := fixture.load(t)
	if after.DependencyPause != nil || after.Phase != runstate.PhaseIntegrating {
		t.Fatalf("the run inside its integration was given a wait %#v at %q; want it left for reconciliation", after.DependencyPause, after.Phase)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("the run was written: updated %s, then %s", before.UpdatedAt, after.UpdatedAt)
	}
}

// The rule run.recover's wait is held to refuses a run interrupted inside its
// integration and one whose promotion is recorded, and allows one at its
// developer attempt.
func TestRecoveryWaitableRefusesARunOwedItsIntegration(t *testing.T) {
	t.Parallel()

	developing := runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping}
	if err := RecoveryWaitable(developing); err != nil {
		t.Fatalf("RecoveryWaitable(developing) = %v", err)
	}
	integrating := developing
	integrating.Phase = runstate.PhaseIntegrating
	promoted := developing
	promoted.Phase = runstate.PhaseReviewing
	promoted.Integration = &runstate.Integration{TargetBranch: "main", TargetCommit: "abc"}
	for name, state := range map[string]runstate.State{"integrating": integrating, "promotion recorded": promoted} {
		if err := RecoveryWaitable(state); err == nil || !strings.Contains(err.Error(), "integration") {
			t.Errorf("RecoveryWaitable(%s) = %v, want it refused naming the integration", name, err)
		}
	}
}
