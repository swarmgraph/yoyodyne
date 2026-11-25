package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// drive is one path driven the way the harness starts work by it, over work the
// decision is asked about. It returns how much started: a provider's session, a
// run, a claim or a slot.
type drive func(t *testing.T, decide StartDecision) int

// drivenBy is every path that asks, each with what drives it.
var drivenBy = map[maystart.Path][]drive{
	maystart.ReadyWork:        {drivePipeline(runstate.Selection{By: runstate.SelectedByScheduler, Reason: "the highest-priority ready item", At: baseTime}), driveScheduler},
	maystart.NamedRun:         {drivePipeline(runstate.OperatorSelection("named", baseTime))},
	maystart.HoldTest:         {drivePipeline(runstate.Selection{By: runstate.SelectedByBrake, Reason: "the test run", At: baseTime})},
	maystart.DecidedRerun:     {drivePipeline(runstate.Selection{By: runstate.SelectedByDevelopmentManager, Reason: "decided", At: baseTime}), driveRerun},
	maystart.DecidedRepair:    {driveRepair},
	maystart.FeedbackRepair:   {driveFeedbackRepair, driveFeedbackRound},
	maystart.FinishMerge:      {driveFinishMerge},
	maystart.ContinueRun:      {driveContinue},
	maystart.PickUpRun:        {drivePickUp},
	maystart.PublishDocument:  {driveDocument},
	maystart.BringUpToDate:    {driveBringUpToDate},
	maystart.ResolveConflict:  {driveConflict},
	maystart.BringInFile:      {driveOutsideFile},
	maystart.UpdateQueued:     {driveQueuedUpdate},
	maystart.UpdateForPerson:  {drivePersonUpdate},
	maystart.AfterLogin:       {driveAfterLogin},
	maystart.RecurringPass:    {driveRecurring},
	maystart.PromptedPass:     {driveCorrection, driveDelivery, driveSummons},
	maystart.UnmetGoalReview:  {driveGoalReview},
	maystart.MessagesForWork:  {driveMessages},
	maystart.TeamBranchUpdate: {driveTeamBranchUpdate},
}

// Every path on the closed list that asks is driven with an item outside the
// members, and nothing starts; driven with no epic named, something does, so a
// drive that could never start anything fails too. A path marked wired that
// nothing here drives fails, and one that is not wired starts whatever is named,
// which is why it keeps `yoyo epic start` refused.
func TestEveryPathOnTheClosedListStartsNothingOutsideTheNamedEpics(t *testing.T) {
	t.Parallel()
	wired := 0
	for _, entry := range maystart.Paths() {
		if !entry.Wired {
			if len(drivenBy[entry.Path]) != 0 {
				t.Errorf("%s is driven here and not marked wired", entry.Path)
			}
			continue
		}
		if entry.AlwaysStarts {
			continue // a person's own command, which does not ask
		}
		wired++
		if len(drivenBy[entry.Path]) == 0 {
			t.Errorf("%s is marked wired and nothing drives it", entry.Path)
		}
		for index, drive := range drivenBy[entry.Path] {
			t.Run(string(entry.Path)+string(rune('a'+index)), func(t *testing.T) {
				t.Parallel()
				// Every member and every role's pass is refused: the one epic has
				// ended, and the item under test was never in it.
				epics := newPassEpics(t)
				epics.end()
				var mu sync.Mutex
				var asked []maystart.Path
				started := drive(t, func(start maystart.Start) maystart.Decision {
					mu.Lock()
					defer mu.Unlock()
					asked = append(asked, start.Path())
					return epics.ask(start)
				})
				if started != 0 {
					t.Errorf("%d started outside the named epics", started)
				}
				if !slices.Contains(asked, entry.Path) {
					t.Errorf("asked as %v, want it asked as %s", asked, entry.Path)
				}
				if drive(t, nil) == 0 {
					t.Error("nothing starts with no epic named either, so this drives nothing")
				}
			})
		}
	}
	if len(drivenBy) != wired {
		t.Errorf("%d paths are driven and %d are wired", len(drivenBy), wired)
	}
}

// tolerated fails on an error that is not the refusal under test.
func tolerated(t *testing.T, err error) {
	t.Helper()
	var outside *OutsideNamedEpicsError
	if err != nil && !errors.As(err, &outside) {
		t.Fatal(err)
	}
}

func oneIf(happened bool) int {
	if happened {
		return 1
	}
	return 0
}

func drivePipeline(selection runstate.Selection) drive {
	return func(t *testing.T, decide StartDecision) int {
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
		provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "change.txt"), []byte("changed\n"), 0o600)
		}, approveVerdict)
		pipeline, store := newPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
		pipeline.Selection, pipeline.MayStart = selection, decide
		_, err := pipeline.Run(context.Background(), tracker.Item.ID)
		tolerated(t, err)
		runs, err := store.Recorded()
		if err != nil {
			t.Fatal(err)
		}
		return len(provider.Requests) + len(runs) + oneIf(tracker.Claimed)
	}
}

func driveScheduler(t *testing.T, decide StartDecision) int {
	harness := newScheduleHarness(readyItems("yoyodyne-outside")...)
	schedule, err := Scheduler{Open: func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.MayStart = decide
		return pull, err
	}}.Schedule(context.Background())
	tolerated(t, err)
	return len(schedule.Started)
}

func driveRerun(t *testing.T, decide StartDecision) int {
	harness := newRerunHarness(t, stoppedState())
	rerunner := harness.rerunner()
	rerunner.MayStart = decide
	_, err := rerunner.Rerun(context.Background(), rerunRequest())
	tolerated(t, err)
	return len(harness.started)
}

func driveRepair(t *testing.T, decide StartDecision) int {
	harness := newContinueHarness(t, continuableState())
	continuer := harness.continuer()
	continuer.MayStart = decide
	_, err := continuer.Continue(context.Background(), continueRequest())
	tolerated(t, err)
	return len(harness.started)
}

func driveFeedbackRepair(t *testing.T, decide StartDecision) int {
	_, _, _, starts, repairer := repairFixture(t)
	repairer.MayStart = decide
	if _, err := repairer.Decide(context.Background(), decideRequest("yoyodyne-a", "line-comment-21")); err != nil {
		t.Fatal(err)
	}
	_, err := repairer.CarryOut(context.Background())
	tolerated(t, err)
	return len(*starts)
}

func driveFeedbackRound(t *testing.T, decide StartDecision) int {
	f := newPersonMergeRepairFixture(t)
	number := f.run.PullRequest.Number
	reading := f.feedback.readings[number]
	reading.Reviews = append(reading.Reviews, publish.FeedbackReview{ID: 61, Author: "teammate", State: publish.ReviewChangesRequested, Body: "Please rename the flag.", At: reading.Comments[0].At})
	f.feedback.readings[number] = reading
	reconciler := f.reconciler(t)
	reconciler.Feedback, reconciler.Reports, reconciler.MayStart = f.feedback, f.reports, decide
	reconciler.Capacity, reconciler.RepairLimit = 1, 3
	settled, err := reconciler.TakeUpFeedback(context.Background())
	tolerated(t, err)
	return oneIf(len(settled) == 1 && settled[0].RepairStarted)
}

func driveFinishMerge(t *testing.T, decide StartDecision) int {
	h := newResumeHarness(t, approvedStoppedState())
	resumer := h.resumer()
	resumer.MayStart = decide
	_, err := resumer.Resume(context.Background(), resumeRequest())
	tolerated(t, err)
	return len(h.started)
}

func driveContinue(t *testing.T, decide StartDecision) int {
	stopped := crossRoleCheckStageState()
	h := newUndecidedHarness(t, stopped)
	continuer := checkStageRecoveryContinuer(h, &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true, Present: true})
	continuer.MayStart = decide
	_, err := continuer.Continue(context.Background(), CheckStageContinueRequest{Run: stopped.RunID})
	tolerated(t, err)
	return len(h.started)
}

// drivePickUp has a run a restart stopped before its first call picked up.
func drivePickUp(t *testing.T, decide StartDecision) int {
	epics := newEpicSwitch(t)
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.MayStart = func(start maystart.Start) maystart.Decision {
		decision := epics.decide(start)
		if err := epics.on(); err != nil && start.Path() != maystart.PickUpRun {
			t.Error(err)
		}
		return decision
	}
	paused, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil || paused.PausedByEpic == nil {
		t.Fatalf("Run() = %#v, %v; want a run to pick up", paused, err)
	}
	stopped := waitsForEpic(t, store, paused.RunID)
	stopped.EpicWait = nil
	stopped.RedeployStop = &runstate.RedeployStop{SessionID: "watch-session-1", Phase: stopped.Phase, At: baseTime, BoundSeconds: 60}
	if err := store.Save(stopped); err != nil {
		t.Fatal(err)
	}
	pipeline.MayStart = decide
	_, err = pipeline.Run(context.Background(), "yoyodyne-task")
	tolerated(t, err)
	return len(provider.Requests)
}

func driveDocument(t *testing.T, decide StartDecision) int {
	_, _, _, pipeline, store := documentPipeline(t)
	pipeline.MayStart = decide
	_, err := pipeline.PublishWaitingDocument(context.Background(), epicDesign(t, pipeline, "a-design", "document-1.1", "yoyodyne-other: ruled"))
	tolerated(t, err)
	runs, err := store.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	return len(runs)
}

func driveBringUpToDate(t *testing.T, decide StartDecision) int {
	r, _ := newCatchUpRun(t, resolveBothSides)
	admitted := r.run(t)
	conflictOnTarget(t, r.repository, "something else\n")
	driver := r.catchUpDriver()
	repair := driver.Promoter.Repair.(MergeQueueRunHandback)
	repair.MayStart = decide
	driver.Promoter.Repair = repair
	driver.Pass(context.Background(), admitted.MergeQueue.Key())
	state, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return oneIf(catchUpNotTakenUp(state))
}

func driveConflict(t *testing.T, decide StartDecision) int {
	f, _, _ := conflictFixture(t)
	f.developer = writeIn("", "app.txt", "one\nstaging and team two\nthree\n")
	f.pipeline.MayStart = decide
	if _, err := f.decide(); err != nil {
		t.Fatal(err)
	}
	f.update()
	runs, err := f.waiting.StateStore.(*runstate.Store).Runs("yoyodyne-task")
	if err != nil {
		t.Fatal(err)
	}
	return len(runs)
}

func driveOutsideFile(t *testing.T, decide StartDecision) int {
	_, _, _, pipeline, store, _, _ := outsideFileFixture(t, nil)
	ctx := context.Background()
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due {
		t.Fatalf("NextOutsideFile() = %t, %v; want the record due", due, err)
	}
	pipeline.MayStart = decide
	_, chosen, err := pipeline.NextOutsideFile(ctx)
	tolerated(t, err)
	_, err = pipeline.BringInOutsideFile(ctx, landing)
	tolerated(t, err)
	runs, err := store.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	return len(runs) + oneIf(chosen)
}

func driveQueuedUpdate(t *testing.T, decide StartDecision) int {
	fixture, forge, outcome := queuedOnProtectedTarget(t)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: 1, Failing: []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}}}
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, true)
	reconciler.StuckMergeAge, reconciler.MayStart, reconciler.HostsRuns = stuckLimit, decide, true
	reconciler.Clock = sweepClock{now: outcome.PullRequest.QueuedAt.Add(time.Minute)}
	_, err := reconciler.Reconcile(context.Background())
	tolerated(t, err)
	state, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatal(err)
	}
	return oneIf(updatingQueuedHead(state))
}

func drivePersonUpdate(t *testing.T, decide StartDecision) int {
	fixture := newPersonMergeUpdateFixture(t)
	driftRemoteTarget(t, fixture.remote, "main")
	reconciler := fixture.reconciler(t)
	reconciler.MayStart = decide
	settled, err := reconciler.SettlePersonMerges(context.Background())
	tolerated(t, err)
	return oneIf(len(settled) == 1 && settled[0].Updating)
}

func driveAfterLogin(t *testing.T, decide StartDecision) int {
	h := newResumeHarness(t, publishRefusedState())
	stops := h.identityStops(&identityProbe{}, &landingResumes{})
	stops.MayStart = decide
	stops.Take(context.Background())
	return len(h.started)
}

// driveRecurring is a recurring pass of each role that has them.
func driveRecurring(t *testing.T, decide StartDecision) int {
	store := sweepStore(t)
	trigger := Trigger{Claims: store, Reports: store, Roles: &wokenRole{}, Clock: recurringClock{}, Tasks: map[string]config.RecurringTask{}, Instances: instance(time.Hour), MayStart: decide}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect} {
		trigger.Tasks[string(role)] = config.RecurringTask{Role: role, Every: config.Duration(time.Hour), Enabled: true, Prompt: "look", MaxTurns: 1}
	}
	start, err := trigger.Start(context.Background(), nil, MaxConcurrentFirings)
	tolerated(t, err)
	return len(start.Started)
}

func driveCorrection(t *testing.T, decide StartDecision) int {
	store := conversationStore(t, t.TempDir())
	refused := refusedConversation(t, store, "product-manager", 4, correctionNow.Add(-time.Minute), "unreadable")
	role := &correctedRole{turns: []CorrectionTurn{{ConversationID: refused.ConversationID, Actions: 3}}}
	_, err := Corrector{Conversations: store, Claims: store, Roles: role, Clock: correctionClock{}, MayStart: decide}.Correct(context.Background())
	tolerated(t, err)
	return len(role.woken)
}

func driveDelivery(t *testing.T, decide StartDecision) int {
	judge := &standingJudge{}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	escalator.MayStart = decide
	_, err := escalator.Escalate(context.Background())
	tolerated(t, err)
	return len(judge.shown)
}

func driveSummons(t *testing.T, decide StartDecision) int {
	firings := newRecurringFirings(context.Background())
	firings.taken = &sync.WaitGroup{}
	defer firings.taken.Wait()
	store := sweepStore(t)
	trigger := Trigger{Tasks: hourlyTask("look"), Claims: store, Reports: store, Roles: &wokenRole{}, Clock: recurringClock{}, MayStart: decide}
	brake := &heldBrake{hold: runstate.IntakeHold{HeldAt: baseTime, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{}}}
	var schedule Schedule
	Scheduler{Now: func() time.Time { return baseTime }}.summon(context.Background(), &schedule, firings, brake, trigger, brake.hold)
	return len(firings.inFlight)
}

func driveGoalReview(t *testing.T, decide StartDecision) int {
	r := newDailyReview(t)
	r.trigger.MayStart = decide
	r.pull(t, novemberDay(20, 10, 0))
	return len(r.role.messages) + r.source.reads
}

func driveMessages(t *testing.T, decide StartDecision) int {
	intake := &fakeIntake{due: []string{"bugs"}, started: map[string]int{}, release: make(chan struct{})}
	close(intake.release)
	runs := newIntakeRuns(context.Background())
	defer runs.cancel()
	runs.mayStart = decide
	var schedule Schedule
	Scheduler{}.startIntakeRuns(&schedule, intake, runs)
	collectIntakeRuns(&schedule, runs, true)
	return schedule.IntakeRuns
}

func driveTeamBranchUpdate(t *testing.T, decide StartDecision) int {
	f := newTeamUpdateFixture(t)
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	f.pipeline.MayStart = decide
	f.update()
	return f.checks.calls() + f.reviewer.calls()
}
