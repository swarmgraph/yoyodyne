package orchestrator

// A run already started, or stopped and taken up again, whose item is in none of
// the epics the operator named: the wait, and each way of resuming a run.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// epicSwitch is a product's epic mode, off until it is turned on with one
// member, which is blocked and never the item under test.
type epicSwitch struct {
	t        *testing.T
	root     string
	operator *epicscope.Operator
}

func newEpicSwitch(t *testing.T) *epicSwitch {
	t.Helper()
	root := t.TempDir()
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return &epicSwitch{t: t, root: root, operator: operator}
}

func (e *epicSwitch) decide(start maystart.Start) maystart.Decision {
	return maystart.Ask(e.root, "yoyodyne", start, baseTime)
}

func (e *epicSwitch) on() error {
	_, err := e.operator.Start(epicscope.Naming{Epic: namedEpic, From: "terminal", Members: []string{blockedMember}}, baseTime)
	return err
}

// permits is the two ways the operator lets a waiting run go on.
func (e *epicSwitch) permits(item string) map[string]func() error {
	return map[string]func() error{
		"its item is added": func() error {
			_, err := e.operator.Add(namedEpic, item, baseTime)
			return err
		},
		"epic mode is turned off": func() error {
			_, err := e.operator.TurnOff()
			return err
		},
	}
}

func (e *epicSwitch) refused(want ...maystart.Path) {
	e.t.Helper()
	record, err := maystart.ReadRefusals(e.root, "yoyodyne")
	if err != nil {
		e.t.Fatal(err)
	}
	var got []maystart.Path
	for _, item := range record.Items {
		got = append(got, item.Path)
	}
	if !reflect.DeepEqual(got, want) {
		e.t.Fatalf("refused starts by %v, want %v", got, want)
	}
}

// waitsForEpic checks a run waits with its checkout kept and its slot given back.
func waitsForEpic(t *testing.T, store *runstate.Store, runID string) runstate.State {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil || !pausedForEpic(state) || state.HoldsDeveloperSlot() || state.Branch == "" {
		t.Fatalf("run = %s, wait %+v, holds a slot %t, %v; want it waiting with its slot given back", state.Status, state.EpicWait, state.HoldsDeveloperSlot(), err)
	}
	if _, err := os.Stat(state.WorktreePath); err != nil {
		t.Fatalf("the waiting run's checkout is gone: %v", err)
	}
	return state
}

// Epic mode turned on under a working developer lets that step finish, and the
// run waits before its review. Its slot goes to another run, a restart starts
// nothing, and the same run goes on once the operator lets it, at a pull.
func TestARunLeftOutsideTheNamedEpicsWaitsBeforeItsNextCallAndGoesOn(t *testing.T) {
	t.Parallel()
	for name := range (&epicSwitch{}).permits("") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			epics := newEpicSwitch(t)
			harness := newRealScheduleHarness(t, 1, "yoyodyne-task")
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				if request.Role != domain.RoleDeveloper {
					return nil
				}
				if err := epics.on(); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			// Each start is a pipeline of its own, as a restart's is.
			start := func(ctx context.Context, id string, selection runstate.Selection) (Outcome, error) {
				pipeline := automatic(newSharedPipeline(t, harness.repository, harness.worktreeRoot, harness.store, harness, provider, []string{"exit 0"}), provider)
				pipeline.Config.Execution.MaxConcurrentDevelopers = 1
				pipeline.Selection, pipeline.MayStart = selection, epics.decide
				return pipeline.Run(ctx, id)
			}
			schedule := func() Schedule {
				t.Helper()
				made, err := Scheduler{Open: func(ctx context.Context) (Pull, error) {
					pull, err := harness.open(ctx)
					pull.Continuations, pull.Start, pull.MayStart = harnessContinuations{harness: harness}, start, epics.decide
					return pull, err
				}}.Schedule(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				return made
			}
			calls := func(developed, reviewed int) {
				t.Helper()
				if d, r := len(provider.RequestsForRole(domain.RoleDeveloper)), len(provider.RequestsForRole(domain.RoleReviewer)); d != developed || r != reviewed {
					t.Fatalf("%d developer and %d reviewer calls, want %d and %d", d, r, developed, reviewed)
				}
			}

			first := schedule()
			if len(first.Started) != 1 || !first.Started[0].Outcome.Paused || first.Started[0].Outcome.PausedByEpic == nil || !deferredSaying(first, "yoyodyne-task", "yoyo epic add "+namedEpic) {
				t.Fatalf("pass = %s (%+v), want the run paused for its epic and the command that adds its item", first.Render(), first.Deferred)
			}
			runID := first.Started[0].Outcome.RunID
			waiting := waitsForEpic(t, harness.store, runID)
			calls(1, 0)
			epics.refused(maystart.PickUpRun)
			item, err := harness.Show(context.Background(), "yoyodyne-task")
			if err != nil || item.Status != "in_progress" || !strings.Contains(strings.Join(harness.recordedNotes(), "\n"), "waits for its epic") {
				t.Fatalf("item = %+v, %v; want its claim kept and the wait noted", item, err)
			}

			// Neither a restart's pull nor the operator naming the item starts it.
			if again := schedule(); len(again.Started) != 0 || !deferredSaying(again, "yoyodyne-task", "waits for its epic") {
				t.Fatalf("pass after a restart = %s, want nothing started", again.Render())
			}
			var outside *OutsideNamedEpicsError
			if _, err := start(context.Background(), "yoyodyne-task", runstate.OperatorSelection("named", baseTime)); !errors.As(err, &outside) || outside.RunID != runID {
				t.Fatalf("Run() of the waiting item = %v, want it refused naming the run", err)
			}
			calls(1, 0)

			// The one slot is another run's while this one waits, and is waited for.
			other := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: "run-fedcba9876543210fedcba9876543210", ProductID: waiting.ProductID, RepositoryID: waiting.RepositoryID,
				WorkItemID: "yoyodyne-other", Backend: waiting.Backend, Status: runstate.StatusPending, StartedAt: baseTime, UpdatedAt: baseTime}
			lease, err := harness.store.Reserve(context.Background(), other, 1)
			if err != nil {
				t.Fatalf("Reserve() beside the waiting run = %v, want its slot free", err)
			}
			if err := epics.permits("yoyodyne-task")[name](); err != nil {
				t.Fatal(err)
			}
			if full := schedule(); len(full.Started) != 0 {
				t.Fatalf("pass with no slot free = %s, want the run still waiting", full.Render())
			}
			waitsForEpic(t, harness.store, runID)
			other.Status, other.CompletedAt = runstate.StatusSucceeded, &other.StartedAt
			if err := errors.Join(harness.store.Save(other), lease.Release()); err != nil {
				t.Fatal(err)
			}

			resumed := schedule()
			if len(resumed.Started) != 1 || resumed.Started[0].Outcome.RunID != runID || resumed.Started[0].Outcome.Integration == nil {
				t.Fatalf("pass once it may go on = %s, want the same run finished", resumed.Render())
			}
			calls(1, 1)
			if last := schedule(); len(last.Started) != 0 {
				t.Fatalf("last pass = %s, want nothing started twice", last.Render())
			}
			epics.refused()
		})
	}
}

// Epic mode turned on before the developer's first call, and one a restart put
// down, each wait: the run keeps its checkout and goes on in it.
func TestARunInterruptedOutsideTheNamedEpicsGivesItsSlotBackWhenItIsPickedUp(t *testing.T) {
	t.Parallel()
	epics := newEpicSwitch(t)
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.MayStart = func(start maystart.Start) maystart.Decision {
		decision := epics.decide(start)
		if start.Path() == maystart.NamedRun || start.Path() == maystart.ReadyWork {
			if err := epics.on(); err != nil {
				t.Error(err)
			}
		}
		return decision
	}
	paused, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil || paused.PausedByEpic == nil || len(provider.Requests) != 0 || !tracker.Claimed {
		t.Fatalf("Run() = %#v, %v; want it waiting before its first call with its item claimed", paused, err)
	}
	pipeline.MayStart = epics.decide
	// A run a restart stopped holds its slot and carries no wait until it is
	// picked up.
	stopped := waitsForEpic(t, store, paused.RunID)
	stopped.EpicWait = nil
	stopped.RedeployStop = &runstate.RedeployStop{SessionID: "watch-session-1", Phase: stopped.Phase, At: baseTime, BoundSeconds: 60}
	if err := store.Save(stopped); err != nil {
		t.Fatal(err)
	}
	var outside *OutsideNamedEpicsError
	if _, err := pipeline.Run(context.Background(), "yoyodyne-task"); !errors.As(err, &outside) {
		t.Fatalf("Run() of the interrupted run = %v, want it refused", err)
	}
	first := waitsForEpic(t, store, paused.RunID)
	if _, err := pipeline.Run(context.Background(), "yoyodyne-task"); !errors.As(err, &outside) {
		t.Fatalf("second Run() = %v, want it refused", err)
	}
	if again := waitsForEpic(t, store, paused.RunID); !again.EpicWait.Since.Equal(first.EpicWait.Since) || !again.UpdatedAt.Equal(first.UpdatedAt) || len(provider.Requests) != 0 {
		t.Fatalf("a second look changed the wait or called a provider")
	}
	// The sweep leaves it, and a run waiting out a provider's limit is not
	// continued by it either.
	if !resumable(first) || exitedWait(first, baseTime.Add(time.Hour)) {
		t.Fatal("the waiting run is not left to whatever picks it up")
	}
	if err := epics.permits("yoyodyne-task")["its item is added"](); err != nil {
		t.Fatal(err)
	}
	finished, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil || finished.RunID != paused.RunID || finished.Integration == nil || len(provider.RequestsForRole(domain.RoleDeveloper)) != 1 {
		t.Fatalf("Run() once added = %#v, %v; want the same run finished", finished, err)
	}
}

// Each way of resuming a stopped run writes nothing to a run whose item is
// outside the members, and resumes it once its item is added.
func TestAStoppedRunOutsideTheNamedEpicsIsNotResumed(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		path  maystart.Path
		build func(t *testing.T, decide StartDecision) (resume func() (*OutsideNamedEpicsError, bool, error), load func() runstate.State)
	}{
		"finishing an interrupted merge": {maystart.FinishMerge, func(t *testing.T, decide StartDecision) (func() (*OutsideNamedEpicsError, bool, error), func() runstate.State) {
			h := newResumeHarness(t, approvedStoppedState())
			resumer := h.resumer()
			resumer.MayStart = decide
			return func() (*OutsideNamedEpicsError, bool, error) {
				result, err := resumer.Resume(context.Background(), resumeRequest())
				if result.OutsideNamedEpics != nil && !strings.Contains(result.Render(), "yoyo epic add "+namedEpic) {
					t.Errorf("Render() = %q, want the command that adds the item", result.Render())
				}
				return result.OutsideNamedEpics, result.Resumed, err
			}, func() runstate.State { return h.reload(t) }
		}},
		"continuing checks that ran out of time": {maystart.ContinueRun, func(t *testing.T, decide StartDecision) (func() (*OutsideNamedEpicsError, bool, error), func() runstate.State) {
			stopped := crossRoleCheckStageState()
			h := newUndecidedHarness(t, stopped)
			continuer := checkStageRecoveryContinuer(h, &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true, Present: true})
			continuer.MayStart = decide
			carrying := h.carryOut()
			carrying.CheckStages = continuer
			task := theOneOutstanding(t, carrying)
			if carried, _, err := carrying.Carry(context.Background(), task); err != nil || carried.Carried || carried.Gate != runstate.TriageGateNamedEpics {
				t.Errorf("carried = %#v, %v; want a wait on the named epics", carried, err)
			}
			carrying.MayStart = decide
			if tasks, err := carrying.Outstanding(); err != nil || len(tasks) != 0 {
				t.Errorf("outstanding = %#v, %v; want it not offered to a slot", tasks, err)
			}
			return func() (*OutsideNamedEpicsError, bool, error) {
				result, err := continuer.Continue(context.Background(), CheckStageContinueRequest{Run: stopped.RunID})
				return result.OutsideNamedEpics, result.Continued, err
			}, func() runstate.State { return loadRecoveryRun(t, h.runs, stopped.RunID) }
		}},
		"continuing a silent run": {maystart.ContinueRun, func(t *testing.T, decide StartDecision) (func() (*OutsideNamedEpicsError, bool, error), func() runstate.State) {
			repository, worktreeRoot, store := restartableFixture(t)
			worktrees := newSweepManager(t, repository, worktreeRoot)
			stalled := settledRunWithCheckout(t, worktrees, store, 0)
			stalled.ProviderSessionID = "stalled-developer-session"
			stalled.Environmental = &runstate.EnvironmentalRefusal{Cause: runstate.CauseProcessVanished, ProviderStop: runstate.ProviderStopStalled, RecordedAt: *stalled.CompletedAt, Settled: true}
			stalled.Blocker = "The developer's first attempt went silent."
			if err := store.Save(stalled); err != nil {
				t.Fatal(err)
			}
			docket := &memoryDocket{}
			if _, err := docketerOver(nil, docket).RecordStoppedRun(stalled); err != nil {
				t.Fatal(err)
			}
			continuer := StallContinuer{
				Docket: docket, Runs: store, Intake: newIntakeHoldStore(t), Worktrees: worktrees, MayStart: decide,
				Items:    &orchestratortest.Tracker{Item: beads.WorkItem{ID: stalled.WorkItemID, Title: "Interrupted developer", Status: "blocked"}},
				Capacity: 2, Clock: docketClockAt{at: stalled.CompletedAt.Add(time.Minute)},
				Start: func(_ context.Context, id, runID string) (Outcome, error) {
					return Outcome{RunID: runID, WorkItemID: id}, nil
				},
			}
			return func() (*OutsideNamedEpicsError, bool, error) {
				result, err := continuer.Continue(context.Background(), StallContinueRequest{Run: stalled.RunID})
				return result.OutsideNamedEpics, result.Continued, err
			}, func() runstate.State { return loadRecoveryRun(t, store, stalled.RunID) }
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			decide, add, refused := epicModeKept(t, blockedMember)
			resume, load := test.build(t, decide)
			before := load()
			outside, resumed, err := resume()
			if err != nil || resumed || outside == nil {
				t.Fatalf("resumed %t, refused %v, %v; want it refused", resumed, outside, err)
			}
			if after := load(); !reflect.DeepEqual(after, before) {
				t.Fatalf("record = %#v, want the run as it stopped", after)
			}
			refused(test.path, before.WorkItemID)
			add(before.WorkItemID)
			if outside, resumed, err = resume(); err != nil || !resumed || outside != nil {
				t.Fatalf("resumed %t, refused %v, %v; want a member's run resumed", resumed, outside, err)
			}
			refused("", "")
		})
	}
}

// In the look at GitHub login refusals, an approved change that may not land
// stays stopped, and a member's listed after it is taken up at the same look.
func TestALandingOutsideTheNamedEpicsDoesNotEndTheLook(t *testing.T) {
	t.Parallel()
	h := newResumeHarness(t, landingRefusedState())
	member := landingRefusedState()
	member.RunID, member.WorkItemID, member.WorkItemTitle = "run-fedcba9876543210fedcba9876543210", blockedMember, "A member"
	member.WorktreePath, member.Branch = "/state/worktrees/member", "yoyodyne/member/abc"
	if err := h.runs.Create(member); err != nil {
		t.Fatal(err)
	}
	docketer := docketerOverStore(h.docket, h.runs, docketConfig())
	docketer.Clock = docketClock{}
	if _, err := docketer.RecordStoppedRun(member); err != nil {
		t.Fatal(err)
	}
	decide, add, refused := epicModeKept(t, blockedMember)
	items := itemsByID{docketedItem: h.tracker, blockedMember: {Item: beads.WorkItem{ID: blockedMember, Title: "A member", Status: "in_progress"}}}
	resumer := h.resumer()
	resumer.MayStart, resumer.Items = decide, items
	stops := h.identityStops(&identityProbe{}, &landingResumes{})
	stops.Items = items
	stops.ResumeLanding = func(ctx context.Context, runID, reason string) (IntegrationResumeResult, error) {
		return resumer.Resume(ctx, IntegrationResumeRequest{Run: runID, Reason: reason})
	}
	before := h.reload(t)
	pass := stops.Take(context.Background())
	if pass.Stopped != 2 || pass.TakenUp != 1 || len(h.started) != 1 || h.started[0].runID != member.RunID || !strings.Contains(pass.Says, "yoyo epic add "+namedEpic) {
		t.Fatalf("look = %#v, started %v; want only the member's change resumed", pass, h.started)
	}
	if after := h.reload(t); !reflect.DeepEqual(after, before) {
		t.Fatalf("record = %#v, want the change as it stopped", after)
	}
	refused(maystart.FinishMerge, docketedItem)
	add(docketedItem)
	if pass := stops.Take(context.Background()); pass.TakenUp != 1 || len(h.started) != 2 {
		t.Fatalf("look once added = %#v, want the change resumed", pass)
	}
}

// A change in the merge queue lands, its review included, though epic mode was
// turned on after it was admitted.
func TestAChangeInTheMergeQueueLandsOutsideTheNamedEpics(t *testing.T) {
	t.Parallel()
	epics := newEpicSwitch(t)
	r := newQueueRun(t, false)
	r.pipeline.Config.Execution.MergeQueue = true
	r.pipeline.MayStart = epics.decide
	outcome := r.run(t)
	if outcome.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the change admitted", outcome)
	}
	if err := epics.on(); err != nil {
		t.Fatal(err)
	}
	if pass := r.driver().Pass(context.Background(), outcome.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the change landed", pass)
	}
	epics.refused()
}

// A conflict run already started waits before its developer, holding no slot, and
// the update keeps the decision and its round. The same run makes the merge once
// its item is added, or epic mode is turned off.
func TestAConflictRunLeftOutsideTheNamedEpicsWaits(t *testing.T) {
	t.Parallel()
	for name := range (&epicSwitch{}).permits("") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, epics, runID := waitingConflict(t)
			store := f.waiting.StateStore.(*runstate.Store)
			waitsForEpic(t, store, runID)
			epics.refused(maystart.ResolveConflict)
			// A later check asks again and starts nothing.
			if outcome := f.update(); outcome.Landed != "" || f.record().Decision.RunID != runID {
				t.Fatalf("Update() = %#v, want the same run still waiting", outcome)
			}
			if err := epics.permits("yoyodyne-task")[name](); err != nil {
				t.Fatal(err)
			}
			if outcome := f.update(); outcome.Landed == "" || f.record().Landed[0].ResolvedBy != runID {
				t.Fatalf("Update() once it may go on = %#v, want the waiting run's merge landed", outcome)
			}
			epics.refused()
		})
	}
}

// A branch that moves under a waiting conflict run ends it, because its merge is
// one nobody would land, and leaves the decision to start afresh with no round
// spent.
func TestABranchMovingUnderAWaitingConflictRunKeepsTheDecision(t *testing.T) {
	t.Parallel()
	f, epics, runID := waitingConflict(t)
	f.teamMerges("app.txt", "one\nteam three\nthree\n", "the team changes it again")
	f.update()
	if state, err := f.pipeline.Store.Load(runID); err != nil || !state.Status.Terminal() {
		t.Fatalf("run = %s, %v; want it ended", state.Status, err)
	}
	if record := f.record(); record.Decision == nil || record.Decision.Started() || record.ConflictRounds != 1 {
		t.Fatalf("record = %#v, want the decision standing unstarted with its one round", record)
	}
	if err := epics.permits("yoyodyne-task")["its item is added"](); err != nil {
		t.Fatal(err)
	}
	f.pipeline.NewRunID = runstate.NewRunID
	if outcome := f.update(); outcome.Landed == "" {
		t.Fatalf("Update() = %#v, want a fresh run's merge landed", outcome)
	}
}

func waitingConflict(t *testing.T) (*teamUpdateFixture, *epicSwitch, string) {
	t.Helper()
	f, _, _ := conflictFixture(t)
	epics := newEpicSwitch(t)
	f.developer = writeIn("", "app.txt", "one\nstaging and team two\nthree\n")
	f.pipeline.MayStart = func(start maystart.Start) maystart.Decision {
		decision := epics.decide(start)
		if decision.MayStart() {
			_ = epics.on() // refused once the epic is open, which is what is wanted
		}
		return decision
	}
	if _, err := f.decide(); err != nil {
		t.Fatal(err)
	}
	if outcome := f.update(); outcome.Landed != "" || !strings.Contains(outcome.Waiting, "waits for its epic") {
		t.Fatalf("Update() = %#v, want the run waiting for its epic", outcome)
	}
	f.pipeline.MayStart = epics.decide
	record := f.record()
	if record.Decision == nil || !record.Decision.Started() || record.Decision.RunID == "" || record.ConflictRounds != 1 {
		t.Fatalf("record = %#v, want the decision's run started and waiting", record)
	}
	return f, epics, record.Decision.RunID
}

// A run that brings a file in carries on to its end when epic mode is turned on
// under it, and so does one a restart finds at its review.
func TestAnOutsideFileRunAlreadyStartedCarriesOnOutsideTheNamedEpics(t *testing.T) {
	t.Parallel()
	for name, restarted := range map[string]bool{"turned on under it": false, "after a restart": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, _, pipeline, store, _, _ := outsideFileFixture(t, nil)
			epics := newEpicSwitch(t)
			pipeline.MayStart = epics.decide
			ctx := context.Background()
			pipeline.Checks = &hookedChecks{CheckRunner: pipeline.Checks, before: func() {
				if err := epics.on(); err != nil {
					t.Error(err)
				}
				if restarted {
					panic("the process ended")
				}
			}}
			landing, due, err := pipeline.NextOutsideFile(ctx)
			if err != nil || !due {
				t.Fatalf("NextOutsideFile() = %t, %v", due, err)
			}
			func() {
				defer func() {
					if ended := recover(); (ended != nil) != restarted {
						t.Errorf("the process ended = %v, want %t", ended, restarted)
					}
				}()
				if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
					t.Error(err)
				}
			}()
			if restarted {
				if state, err := store.Load(landing.RunID()); err != nil || state.Status.Terminal() {
					t.Fatalf("run after the restart = %s, %v; want it in flight", state.Status, err)
				}
				next, due, err := pipeline.NextOutsideFile(context.Background())
				if err != nil || !due {
					t.Fatalf("NextOutsideFile() after the restart = %t, %v; want the run reached", due, err)
				}
				if _, err := pipeline.BringInOutsideFile(context.Background(), next); err != nil {
					t.Fatal(err)
				}
			}
			if state, err := store.Load(landing.RunID()); err != nil || state.Status != runstate.StatusSucceeded || state.Integration == nil || state.EpicWait != nil {
				t.Fatalf("run = %s, %+v, %v; want it carried on to its end", state.Status, state.Integration, err)
			}
			epics.refused()
		})
	}
}

// hookedChecks does something once, before the first check runs.
type hookedChecks struct {
	CheckRunner
	before func()
}

func (h *hookedChecks) Run(ctx context.Context, request checks.Request, sink func(execution.Event) error) ([]checks.Result, uint64, error) {
	if h.before != nil {
		before := h.before
		h.before = nil
		before()
	}
	return h.CheckRunner.Run(ctx, request, sink)
}

// A run put back to be brought up to date before epic mode was turned on is not
// hosted outside the members: it waits with its slot given back, writes nothing
// more at the next sweep, and is hosted once its item is added.
func TestARunPutBackToBeBroughtUpToDateOutsideTheNamedEpicsWaits(t *testing.T) {
	t.Parallel()
	fixture, forge, _ := queuedOnProtectedTarget(t)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: 1,
		Failing: []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}}}
	reconciler := fixture.sweep(t, forge, true)
	if results, err := reconciler.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != ActionUpdating {
		t.Fatalf("Reconcile() = %#v, %v; want the run put back", results, err)
	}
	epics := newEpicSwitch(t)
	if err := epics.on(); err != nil {
		t.Fatal(err)
	}
	reconciler.MayStart = epics.decide
	host := func() UpdateContinuation {
		t.Helper()
		updates, err := reconciler.ContinueUpdates(context.Background())
		if err != nil || len(updates) != 1 || updates[0].Failure != "" {
			t.Fatalf("ContinueUpdates() = %#v, %v; want the one run and no failure", updates, err)
		}
		return updates[0]
	}
	if update := host(); update.Continued || !strings.Contains(update.Detail, "yoyo epic add "+namedEpic) {
		t.Fatalf("update = %#v, want it left waiting", update)
	}
	first := waitsForEpic(t, fixture.store, pipelineRunID)
	epics.refused(maystart.PickUpRun)
	if results, err := reconciler.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("Reconcile() of the waiting run = %#v, %v; want it left", results, err)
	}
	host()
	if again := waitsForEpic(t, fixture.store, pipelineRunID); !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatal("a second sweep wrote to the waiting run")
	}
	if err := epics.permits("yoyodyne-task")["its item is added"](); err != nil {
		t.Fatal(err)
	}
	if update := host(); !update.Continued || update.Outcome == nil || update.Outcome.PullRequest == nil || !update.Outcome.PullRequest.MergeQueued {
		t.Fatalf("update = %#v, want the run hosted and its merge queued again", update)
	}
	epics.refused()
}

// A run the merge queue put back for one of its attempts to catch up with its target
// before epic mode was turned on waits with its attempt kept, and goes on to
// rejoin the queue once its item is added.
func TestACatchUpOutsideTheNamedEpicsWaitsWithItsAttemptKept(t *testing.T) {
	t.Parallel()
	r, provider := newCatchUpRun(t, resolveBothSides)
	admitted := r.run(t)
	if admitted.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", admitted)
	}
	conflictOnTarget(t, r.repository, "something else\n")
	if pass := r.catchUpPass(t, admitted.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflicting change handed back", pass)
	}
	epics := newEpicSwitch(t)
	if err := epics.on(); err != nil {
		t.Fatal(err)
	}
	r.pipeline.MayStart = epics.decide
	var outside *OutsideNamedEpicsError
	if _, err := r.pipeline.Continue(context.Background(), "yoyodyne-task", admitted.RunID); !errors.As(err, &outside) {
		t.Fatalf("Continue() = %v, want it refused", err)
	}
	waiting := waitsForEpic(t, r.runs, admitted.RunID)
	if !catchUpNotTakenUp(waiting) || provider.DeveloperAttempts != 1 {
		t.Fatalf("attempt still owed %t, developer attempts %d; want the attempt kept and no developer called", catchUpNotTakenUp(waiting), provider.DeveloperAttempts)
	}
	epics.refused(maystart.PickUpRun)
	if err := epics.permits("yoyodyne-task")["its item is added"](); err != nil {
		t.Fatal(err)
	}
	rejoined, err := r.pipeline.Continue(context.Background(), "yoyodyne-task", admitted.RunID)
	if err != nil || rejoined.MergeQueue == nil || rejoined.MergeQueue.EntryID == admitted.MergeQueue.EntryID {
		t.Fatalf("Continue() = %#v, %v; want the change admitted to the queue again", rejoined, err)
	}
	epics.refused()
}

// An update that stopped before its review to wait for its epic did not lose the
// process hosting it. Once its item may go on it waits for a slot with nothing
// said on the item, however many sweeps pass, and is hosted when one frees.
func TestAnUpdateThatWaitedForItsEpicAtItsReviewIsNotSaidToHaveLostItsHost(t *testing.T) {
	t.Parallel()
	fixture, forge, _ := queuedOnProtectedTarget(t)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: 1,
		Failing: []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}}}
	reconciler := fixture.sweep(t, forge, true)
	if results, err := reconciler.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != ActionUpdating {
		t.Fatalf("Reconcile() = %#v, %v; want the run put back", results, err)
	}
	// Epic mode is turned on once the update has been taken up.
	epics := newEpicSwitch(t)
	provider := orchestratortest.RoleBackend(nil, approveVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, fixture.repository, fixture.worktreeRoot, fixture.store, fixture.tracker, provider, []string{"exit 0"}), provider), fixture.forge)
	pipeline.Publisher = forge
	pipeline.Config.Execution.MaxConcurrentDevelopers = 1
	pipeline.MayStart = func(start maystart.Start) maystart.Decision {
		decision := epics.decide(start)
		_ = epics.on() // refused once the epic is open, which is what is wanted
		return decision
	}
	reconciler.Continue, reconciler.MayStart = pipeline.Continue, epics.decide
	host := func() UpdateContinuation {
		t.Helper()
		updates, err := reconciler.ContinueUpdates(context.Background())
		if err != nil || len(updates) != 1 || updates[0].Failure != "" {
			t.Fatalf("ContinueUpdates() = %#v, %v; want the one run and no failure", updates, err)
		}
		return updates[0]
	}
	host()
	waiting := waitsForEpic(t, fixture.store, pipelineRunID)
	if !updateLostItsHost(waiting) || len(provider.RequestsForRole(domain.RoleReviewer)) != 0 {
		t.Fatalf("phase %s, want the update waiting at its review", waiting.Phase)
	}
	other := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: "run-fedcba9876543210fedcba9876543210", ProductID: waiting.ProductID, RepositoryID: waiting.RepositoryID,
		WorkItemID: "yoyodyne-other", Backend: waiting.Backend, Status: runstate.StatusPending, StartedAt: baseTime, UpdatedAt: baseTime}
	lease, err := fixture.store.Reserve(context.Background(), other, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := epics.permits("yoyodyne-task")["its item is added"](); err != nil {
		t.Fatal(err)
	}
	notes := fixture.tracker.Record().Notes
	for range 2 {
		if update := host(); update.Continued {
			t.Fatalf("update = %#v, want it waiting for a slot", update)
		}
	}
	waitsForEpic(t, fixture.store, pipelineRunID)
	if said := fixture.tracker.Record().Notes; said != notes || strings.Contains(said, "the process hosting it") {
		t.Fatalf("notes:\n%s\nwant nothing written while the run waits for a slot, over:\n%s", said, notes)
	}
	other.Status, other.CompletedAt = runstate.StatusSucceeded, &other.StartedAt
	if err := errors.Join(fixture.store.Save(other), lease.Release()); err != nil {
		t.Fatal(err)
	}
	if update := host(); !update.Continued || !strings.Contains(update.Detail, "waited for its epic") || update.Outcome == nil || update.Outcome.PullRequest == nil || !update.Outcome.PullRequest.MergeQueued {
		t.Fatalf("update = %#v, want the run hosted and its merge queued again", update)
	}
	if said := fixture.tracker.Record().Notes; strings.Contains(said, "the process hosting") {
		t.Fatalf("notes:\n%s\nwant no lost process named", said)
	}
}
