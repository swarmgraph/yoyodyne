package orchestrator

// The paths that begin a run for a work item, each asking the one decision on
// what may start under the named epics (internal/maystart).

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
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const namedEpic, blockedMember = "yoyodyne-9k1y", "yoyodyne-9k1y.1"

// epicMode turns epic mode on with the given members, and returns the decision
// and the operator's own way of adding an item.
func epicMode(t *testing.T, members ...string) (StartDecision, func(item string)) {
	t.Helper()
	decide, add, _ := epicModeKept(t, members...)
	return decide, add
}

// epicModeKept is epicMode with a check of the record of refused starts: that
// it has exactly one entry for the item by the path, and no other path's.
func epicModeKept(t *testing.T, members ...string) (StartDecision, func(item string), func(path maystart.Path, item string)) {
	t.Helper()
	root := t.TempDir()
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Start(epicscope.Naming{Epic: namedEpic, From: "terminal", Members: members}, baseTime); err != nil {
		t.Fatal(err)
	}
	decide := func(start maystart.Start) maystart.Decision {
		return maystart.Ask(root, "yoyodyne", start, baseTime)
	}
	refused := func(path maystart.Path, item string) {
		t.Helper()
		record, err := maystart.ReadRefusals(root, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		want := []maystart.RefusedItem{{WorkItemID: item, Path: path, Why: item + " is not in an open epic the operator named", Since: baseTime.UTC(), Last: baseTime.UTC()}}
		if path == "" {
			want = nil
		}
		if len(record.Items) != len(want) || (len(want) == 1 && record.Items[0] != want[0]) {
			t.Fatalf("refused starts = %+v, want %+v", record.Items, want)
		}
	}
	return decide, func(item string) {
		t.Helper()
		if _, err := operator.Add(namedEpic, item, baseTime); err != nil {
			t.Fatal(err)
		}
	}, refused
}

// Every selection is refused at the pipeline's door, the operator's own naming
// included, with nothing reserved, claimed or asked of a provider, and the same
// selection starts once the operator adds the item.
func TestThePipelineStartsOnlyAMemberOfANamedEpic(t *testing.T) {
	t.Parallel()
	repair := runstate.TeamFeedbackRepair{FeedbackID: "line-comment-21", PullRequest: 7}
	for path, selection := range map[maystart.Path]runstate.Selection{
		maystart.ReadyWork:      {By: runstate.SelectedByScheduler, Reason: "the highest-priority ready item", At: baseTime},
		maystart.NamedRun:       runstate.OperatorSelection("the operator ran this item by name", baseTime),
		maystart.HoldTest:       {By: runstate.SelectedByBrake, Reason: "the probe run", At: baseTime},
		maystart.DecidedRerun:   {By: runstate.SelectedByDevelopmentManager, Reason: "decided", At: baseTime},
		maystart.FeedbackRepair: {By: runstate.SelectedByDevelopmentManager, Reason: repair.RepairMarker() + " decided", At: baseTime},
	} {
		t.Run(string(path), func(t *testing.T) {
			t.Parallel()
			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "change.txt"), []byte("changed\n"), 0o600)
			}, approveVerdict)
			pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
			pipeline.Selection = selection
			if got := pipeline.startPath(); got != path {
				t.Fatalf("startPath() = %q, want %q", got, path)
			}
			decide, add, refused := epicModeKept(t, blockedMember)
			var asked []maystart.Path
			pipeline.MayStart = func(start maystart.Start) maystart.Decision {
				asked = append(asked, start.Path())
				return decide(start)
			}

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			var outside *OutsideNamedEpicsError
			if !errors.As(err, &outside) || len(asked) != 1 || asked[0] != path {
				t.Fatalf("Run() error = %v asked as %v, want the item refused as outside the named epics", err, asked)
			}
			for _, want := range []string{"yoyodyne-task", "`yoyo epic add " + namedEpic + " yoyodyne-task`", "`yoyo epic off`"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not give %q", err, want)
				}
			}
			incomplete, incompleteErr := store.Incomplete()
			if outcome.RunID != "" || tracker.Claimed || len(provider.Requests) != 0 || incompleteErr != nil || len(incomplete) != 0 {
				t.Fatalf("outcome = %#v, claimed = %t, requests = %d, in flight = %v, %v; want nothing begun", outcome, tracker.Claimed, len(provider.Requests), incomplete, incompleteErr)
			}
			refused(path, "yoyodyne-task")

			add("yoyodyne-task")
			if outcome, err = pipeline.Run(context.Background(), tracker.Item.ID); err != nil || outcome.RunID == "" {
				t.Fatalf("Run() of a member = %#v, %v; want it started", outcome, err)
			}
			refused("", "")
		})
	}
}

func TestAnUnreadableRecordOfNamedEpicsStartsNothingAndSaysWhy(t *testing.T) {
	t.Parallel()
	refused := StartDecision(func(maystart.Start) maystart.Decision {
		return maystart.Decision{Answer: maystart.CouldNotTell, Why: "the record could not be read"}
	}).refuseRun(maystart.ReadyWork, "yoyodyne-task")
	if refused == nil || !strings.Contains(refused.Error(), "the record could not be read") || strings.Contains(refused.Error(), "epic add") {
		t.Fatalf("refused = %v, want a start refused for the record and no command that adds the item", refused)
	}
}

// The pull takes the member and leaves the ready item outside, however far
// ahead of it in the order; with every member blocked or held for design it
// starts nothing, and writes nothing to the item it left.
func TestTheSchedulerPullsOnlyMembersOfANamedEpic(t *testing.T) {
	t.Parallel()
	outside := beads.WorkItem{ID: "yoyodyne-outside", Title: "Outside", Status: "open", Priority: 0}
	member := beads.WorkItem{ID: blockedMember, Title: "Member", Status: "open", Priority: 2}
	blocked := member
	blocked.Status = "blocked"
	held := member
	held.Labels = []string{domain.NeedsDesignLabel}
	for name, test := range map[string]struct {
		member beads.WorkItem
		want   int
	}{
		"a ready member":           {member, 1},
		"every member blocked":     {blocked, 0},
		"a member held for design": {held, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(outside, test.member)
			decide, add, refused := epicModeKept(t, blockedMember)
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.MayStart = decide
				return pull, err
			}
			schedule, err := Scheduler{Open: open}.Schedule(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(schedule.Started) != test.want || (test.want == 1 && schedule.Started[0].WorkItemID != blockedMember) {
				t.Fatalf("started = %#v, want only a ready member", schedule.Started)
			}
			said := false
			for _, deferral := range schedule.Deferred {
				said = said || (deferral.WorkItemID == outside.ID && strings.Contains(deferral.Reason, "yoyo epic add"))
			}
			if !said || harness.Items[0].SchedulingWait != nil || harness.Items[0].Status != "open" {
				t.Fatalf("deferred = %#v, item = %#v; want the item outside named on the pass and left as it was", schedule.Deferred, harness.Items[0])
			}
			refused(maystart.ReadyWork, outside.ID)
			add(outside.ID)
			schedule, err = Scheduler{Open: open}.Schedule(context.Background())
			if err != nil || len(schedule.Started) == 0 || schedule.Started[0].WorkItemID != outside.ID {
				t.Fatalf("started = %#v, %v; want the item started once it is added", schedule.Started, err)
			}
		})
	}
}

// The test run that finds out whether the hold can lift is chosen from the
// members, and no item outside them is ever named on the hold as its test.
func TestTheHoldsTestRunIsOnlyAMember(t *testing.T) {
	t.Parallel()
	storm := []string{"yoyodyne-one", "yoyodyne-two", "yoyodyne-three"}
	for name, test := range map[string]struct {
		members []string
		blocked string
		want    string
	}{
		"a ready member behind an item outside": {append([]string{"yoyodyne-five"}, storm...), "", "yoyodyne-five"},
		"every member left blocked":             {append([]string{"yoyodyne-five"}, storm...), "yoyodyne-five", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(readyItems(append(append([]string(nil), storm...), "yoyodyne-four", "yoyodyne-five")...)...)
			for index := range harness.Items {
				if harness.Items[index].ID == test.blocked {
					harness.Items[index].Status = "blocked"
				}
			}
			harness.blockedRuns = 3
			harness.cooldown = 10 * time.Minute
			harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
				if id == "yoyodyne-four" || id == "yoyodyne-five" {
					landed := h.complete(id)
					landed.RunID = "run-" + id
					return landed, nil
				}
				return blockedStorm(h, id)
			}
			harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 14 }
			decide, _ := epicMode(t, test.members...)
			asked := make(chan maystart.Path, 1)
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.MayStart = func(start maystart.Start) maystart.Decision {
					if start.Path() == maystart.HoldTest {
						select {
						case asked <- maystart.HoldTest:
						default:
						}
					}
					return decide(start)
				}
				return pull, err
			}
			schedule, err := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}.Schedule(context.Background())
			if err != nil || schedule.Braked == nil {
				t.Fatalf("schedule = %s, %v; want the hold placed", schedule.Render(), err)
			}
			probe := ""
			for _, started := range schedule.Started {
				if started.WorkItemID == "yoyodyne-four" {
					t.Fatalf("started = %s, want nothing outside the members started", schedule.Render())
				}
				if started.Probe {
					probe = started.WorkItemID
				}
			}
			if probe != test.want {
				t.Fatalf("test run = %q, want %q: %s", probe, test.want, schedule.Render())
			}
			if len(asked) == 0 {
				t.Fatal("the decision was never asked as the hold's test run")
			}
		})
	}
}

// A decided fresh run outside the members is not offered to a slot, claims
// nothing and writes nothing to its item; asked for directly it waits the same
// way; and the same decision starts once the item is added.
func TestADecidedFreshRunOutsideTheNamedEpicsKeepsItsClaim(t *testing.T) {
	t.Parallel()
	harness := newRerunHarness(t, stoppedState())
	decide, add, refused := epicModeKept(t, blockedMember)
	rerunner := harness.rerunner()
	rerunner.MayStart = decide
	carrying := harness.carryOut()
	carrying.Rerunner, carrying.Notes = rerunner, harness

	task := theOneOutstanding(t, carrying)
	carrying.MayStart = decide
	if outstanding, err := carrying.Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, %v; want the decision held back", outstanding, err)
	}
	refused(maystart.DecidedRerun, docketedItem)
	before, beforeItem := rerunBudget(t, harness), harness.item
	if _, err := carrying.RecordUnattempted(context.Background(), 0, nil); err != nil {
		t.Fatal(err)
	}
	carried, _, err := carrying.Carry(context.Background(), task)
	if err != nil || carried.Carried || !carried.Waiting || carried.Gate != runstate.TriageGateNamedEpics || carried.RecordProblem != "" {
		t.Fatalf("carried = %#v, %v; want a wait on the named epics", carried, err)
	}
	result, err := rerunner.Rerun(context.Background(), rerunRequest())
	if err != nil || result.Started || result.OutsideNamedEpics == nil || !strings.Contains(result.Render(), "yoyo epic add "+namedEpic) {
		t.Fatalf("result = %#v, %v; want nothing started and the command that adds the item", result, err)
	}
	after := rerunBudget(t, harness)
	claimed, err := harness.reruns.Claimed(docketedItem)
	if err != nil || len(claimed) != 0 || after.Reruns != before.Reruns || len(harness.started) != 0 ||
		len(harness.released) != 0 || len(harness.noted) != 0 || harness.item.Status != beforeItem.Status || harness.item.SchedulingWait != nil {
		t.Fatalf("claimed = %v, reruns %d then %d, item = %#v; want nothing spent and the item as it was", claimed, before.Reruns, after.Reruns, harness.item)
	}

	add(docketedItem)
	carried, _, err = carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil || !carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, %v with %d started; want the same decision carried out", carried, err, len(harness.started))
	}
}

// The record changing between the re-run's question and its run's gives the
// claim back, and the decision stays a wait.
func TestAFreshRunRefusedAtThePipelinesDoorKeepsItsClaim(t *testing.T) {
	t.Parallel()
	harness := newRerunHarness(t, stoppedState())
	decide, _ := epicMode(t, blockedMember)
	harness.outcome, harness.failure = Outcome{}, decideRefusal(decide, docketedItem)
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil || carried.Carried || !carried.Waiting || carried.Gate != runstate.TriageGateNamedEpics {
		t.Fatalf("carried = %#v, %v; want a wait on the named epics", carried, err)
	}
	if claimed, err := harness.reruns.Claimed(docketedItem); err != nil || len(claimed) != 0 {
		t.Fatalf("claimed = %v, %v; want the claim given back", claimed, err)
	}
}

func decideRefusal(decide StartDecision, item string) error {
	return decide.refuseRun(maystart.DecidedRerun, item)
}

func TestADecidedRepairOutsideTheNamedEpicsKeepsItsGrant(t *testing.T) {
	t.Parallel()
	harness := newContinueHarness(t, continuableState())
	decide, add, refused := epicModeKept(t, blockedMember)
	continuer := harness.continuer()
	continuer.MayStart = decide
	carrying := harness.carryOut()
	carrying.Repairer = continuer

	task := theOneOutstanding(t, carrying)
	carrying.MayStart = decide
	if outstanding, err := carrying.Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, %v; want the decision held back", outstanding, err)
	}
	refused(maystart.DecidedRepair, docketedItem)
	before, stopped, notes := harness.spent(t), harness.reload(t), harness.tracker.Notes
	carried, _, err := carrying.Carry(context.Background(), task)
	if err != nil || carried.Carried || !carried.Waiting || carried.Gate != runstate.TriageGateNamedEpics {
		t.Fatalf("carried = %#v, %v; want a wait on the named epics", carried, err)
	}
	result, err := continuer.Continue(context.Background(), continueRequest())
	if err != nil || result.Continued || result.OutsideNamedEpics == nil || !strings.Contains(result.Render(), "yoyo epic add "+namedEpic) {
		t.Fatalf("result = %#v, %v; want nothing continued and the command that adds the item", result, err)
	}
	after, state := harness.spent(t), harness.reload(t)
	if len(harness.started) != 0 || harness.carried(t) != 0 || state.Status != stopped.Status || state.Branch != stopped.Branch ||
		after.RepairGrants != before.RepairGrants || after.GrantedRounds != before.GrantedRounds ||
		harness.tracker.Item.Status != "blocked" || harness.tracker.Notes != notes {
		t.Fatalf("counters %#v then %#v, run %s, item %s; want nothing spent and everything as it was", before, after, state.Status, harness.tracker.Item.Status)
	}

	add(docketedItem)
	carried, _, err = carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil || !carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, %v with %d started; want the same decision carried out", carried, err, len(harness.started))
	}
}

// A repair decided from feedback on a pull request stands untaken while its
// item is outside the members, and starts once it is added.
func TestARepairDecidedFromFeedbackOutsideTheNamedEpicsStands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, items, _, starts, repairer := repairFixture(t)
	decide, add, refused := epicModeKept(t, blockedMember)
	repairer.MayStart = decide
	if _, err := repairer.Decide(ctx, decideRequest("yoyodyne-a", "line-comment-21")); err != nil {
		t.Fatal(err)
	}
	notes := items.items["yoyodyne-a"].Notes
	carried, err := repairer.CarryOut(ctx)
	if err != nil || len(carried) != 1 || carried[0].Started || !strings.Contains(carried[0].Waiting, "yoyo epic add "+namedEpic) {
		t.Fatalf("carried = %#v, %v; want the decision waiting on the named epics", carried, err)
	}
	owed, err := repairer.Owed(ctx)
	if err != nil || len(owed) != 0 || len(*starts) != 0 || len(items.released) != 0 || items.items["yoyodyne-a"].Notes != notes ||
		len(f.load(t, "yoyodyne-a").TeamMerge.RepairsOwed()) != 1 {
		t.Fatalf("owed = %v, %v, starts = %v; want nothing offered, taken or written", owed, err, *starts)
	}
	refused(maystart.FeedbackRepair, "yoyodyne-a")
	add("yoyodyne-a")
	if carried, err = repairer.CarryOut(ctx); err != nil || len(carried) != 1 || !carried[0].Started || len(*starts) != 1 {
		t.Fatalf("carried = %#v, %v; want the repair started once its item is added", carried, err)
	}
}

// A repair round asked for on a pull request a person is to merge is not put on
// its run while the item is outside the members; nothing said is passed over,
// and the same feedback starts the round once the item is added.
func TestARepairRoundFromFeedbackOutsideTheNamedEpicsIsLeftForTheNextCheck(t *testing.T) {
	t.Parallel()
	f := newPersonMergeRepairFixture(t)
	number := f.run.PullRequest.Number
	reading := f.feedback.readings[number]
	reading.Reviews = append(reading.Reviews, publish.FeedbackReview{ID: 61, Author: "teammate", State: publish.ReviewChangesRequested, Body: "Please rename the flag.", At: reading.Comments[0].At})
	f.feedback.readings[number] = reading
	decide, add, refused := epicModeKept(t, blockedMember)
	reconciler := f.reconciler(t)
	reconciler.Feedback, reconciler.Reports, reconciler.MayStart = f.feedback, f.reports, decide
	reconciler.Capacity, reconciler.RepairLimit = 1, 3
	before, err := f.store.Load(f.run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	settled, err := reconciler.TakeUpFeedback(context.Background())
	if err != nil || len(settled) != 1 || settled[0].RepairStarted || settled[0].Failure != "" || !strings.Contains(settled[0].Detail, "yoyo epic add "+namedEpic) {
		t.Fatalf("settled = %#v, %v; want the round left for the next check", settled, err)
	}
	after, err := f.store.Load(f.run.RunID)
	if err != nil || after.Status != before.Status || after.RepairAttempts != before.RepairAttempts || after.PullRequest.Feedback != nil || len(f.reports.reported) != 0 {
		t.Fatalf("run = %s with %d attempts, feedback %#v, %v; want the run as it was", after.Status, after.RepairAttempts, after.PullRequest.Feedback, err)
	}
	refused(maystart.FeedbackRepair, f.run.WorkItemID)
	add(f.run.WorkItemID)
	settled, err = reconciler.TakeUpFeedback(context.Background())
	if err != nil || len(settled) != 1 || !settled[0].RepairStarted {
		t.Fatalf("settled = %#v, %v; want the round started once the item is added", settled, err)
	}
}

// A start the pipeline declined because the record changed after the pull chose
// the item is not a failure.
func TestAnItemOutsideTheNamedEpicsAfterThePullDeclinesTheStart(t *testing.T) {
	t.Parallel()
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	decide, _ := epicMode(t, blockedMember)
	harness.run = func(_ *scheduleHarness, id string) (Outcome, error) {
		return Outcome{}, decideRefusal(decide, id)
	}
	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil || len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, %v", schedule.Started, err)
	}
	if started := schedule.Started[0]; started.Failure != "" || !strings.Contains(excludedBecause(started), "did not start it") {
		t.Fatalf("started = %#v, want a declined start", started)
	}
}

func rerunBudget(t *testing.T, harness *rerunHarness) runstate.TriageCounters {
	t.Helper()
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	return counters
}
