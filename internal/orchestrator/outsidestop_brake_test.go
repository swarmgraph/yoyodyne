package orchestrator

// Stops from outside the work and the hold on new work.
//
// The hold counts verdicts on a change. Two runs paused only for a move to a
// new build, whose pause notes timed out against a slow tracker, were counted
// as blocked and tripped it; then both probes meant to find out whether the
// line was fine were stopped from outside — a login the provider refused, a
// harness restart — and each kept the hold a whole cooldown more. Each test
// below is one of those shapes, beside a run that genuinely blocked, which
// still counts.
//
// Leaving a stop out of the count is all the hold does for it. A probe that
// stops that way still waits the usual time before the next one, so a stop
// that keeps recurring, or one sorted outside the work by mistake, never starts
// probes back to back; and the harness's own failures, and causes this build
// does not recognise, count as a verdict does.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// pausedForBuildMoveNoteFailed is a run the session paused for its own move to
// a new build, whose pause is durable and whose note on the item timed out.
func pausedForBuildMoveNoteFailed(h *scheduleHarness, id string) (Outcome, error) {
	h.retire(id)
	return Outcome{
			RunID:      "run-" + id,
			WorkItemID: id,
			Status:     runstate.StatusRunning,
			Phase:      runstate.PhaseReviewing,
			Paused:     true,
			RedeployStop: &runstate.RedeployStop{
				At: h.clock(), Phase: runstate.PhaseReviewing, BoundSeconds: 900, SessionID: "watch-before",
			},
		}, PauseNoteError{
			What: "the redeploy stop",
			Err:  errors.New("bd update yoyodyne-ifd.428.104: signal: killed after 30s"),
		}
}

// A build-move pause whose note failed counts toward nothing and clears
// nothing, while the genuinely blocked runs either side of it still count: the
// brake trips on the third verdict and names those three alone.
func TestABuildMovePauseWhoseNoteFailedIsNeverCountedTowardTheHold(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four", "yoyodyne-five")...)
	harness.blockedRuns = 3
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if id == "yoyodyne-two" || id == "yoyodyne-three" {
			return pausedForBuildMoveNoteFailed(h, id)
		}
		return blockedStorm(h, id)
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked == nil || schedule.Braked.Brake == nil {
		t.Fatalf("schedule = %s, want the three genuine blockings to trip the brake", schedule.Render())
	}
	var tripped []string
	for _, blocked := range schedule.Braked.Brake.Blocked {
		tripped = append(tripped, blocked.WorkItemID)
	}
	if strings.Join(tripped, ",") != "yoyodyne-one,yoyodyne-four,yoyodyne-five" {
		t.Fatalf("trip names %v, want the three blocked runs and neither paused one", tripped)
	}
	for _, started := range schedule.Started {
		if started.WorkItemID != "yoyodyne-two" && started.WorkItemID != "yoyodyne-three" {
			continue
		}
		if started.Failure != "" {
			t.Fatalf("started %#v, want the paused run not reported as failed", started)
		}
		if !strings.Contains(started.NoteProblem, "record the redeploy stop on the work item") || !strings.Contains(started.NoteProblem, "killed after 30s") {
			t.Fatalf("note problem = %q, want the failed note said plainly", started.NoteProblem)
		}
		if state := started.state(); state != "stopped for the redeploy and preserved" {
			t.Fatalf("state = %q, want the paused run named as stopped for the redeploy", state)
		}
	}
	if rendered := schedule.Render(); !strings.Contains(rendered, "the run is paused and resumable, and the note saying so on its work item could not be written") {
		t.Fatalf("schedule = %s, want the failed note reported plainly", rendered)
	}
}

// Paused runs whose notes failed, and nothing else: no hold, no failure on the
// pass, and nothing counted.
func TestBuildMovePausesAloneNeverTripTheHold(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.blockedRuns = 2
	harness.run = pausedForBuildMoveNoteFailed
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{}, errors.New("nobody should be summoned over paused runs")
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked != nil || schedule.BlockedInARow != 0 || len(harness.summonses) != 0 {
		t.Fatalf("braked = %#v, blocked in a row = %d, want paused runs counted toward nothing", schedule.Braked, schedule.BlockedInARow)
	}
	if schedule.Failed() {
		t.Fatalf("the pass reports a failure over runs that are only paused: %s", schedule.Render())
	}
}

// A probe stopped from outside the work is not a blocked probe: no cycle is
// spent and the development manager is not summoned over it again. Two such
// probes in a row are each followed by the usual wait between probes rather
// than by another probe at once. A probe that genuinely blocked, one the
// harness's own step failed around, and one stopped for a cause this build
// does not recognise are the contrast, and each spends a cycle.
func TestAProbeStoppedFromOutsideTheWorkSpendsNoCycleAndWaitsTheUsualTime(t *testing.T) {
	t.Parallel()

	const loginRefused = "the developer's provider refused the login: Invalid API key · Please run /login"
	for _, shape := range []struct {
		name  string
		probe func(*scheduleHarness, string) (Outcome, error)
		// outside is a probe the shape stops from outside the work.
		outside bool
	}{
		{
			name: "a login the provider refused, ending the run",
			probe: func(h *scheduleHarness, id string) (Outcome, error) {
				h.retire(id)
				return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusFailed, Blocked: true,
					Failure: loginRefused, StopClass: runstate.StopProvider}, errors.New(loginRefused)
			},
			outside: true,
		},
		{
			name: "a login the provider refused, pausing the run",
			probe: func(h *scheduleHarness, id string) (Outcome, error) {
				h.retire(id)
				return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusRunning, Paused: true,
					PauseCause: runstate.PauseProviderUnauthenticated}, nil
			},
			outside: true,
		},
		{
			name: "a harness restart the run could not be continued from",
			probe: func(h *scheduleHarness, id string) (Outcome, error) {
				h.retire(id)
				failure := "the watching session stopped this run to restart into the build deployed over it; cancelled with its branch and worktree preserved"
				return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusCancelled,
					Failure: failure, StopClass: runstate.StopRedeploy}, errors.New(failure)
			},
			outside: true,
		},
		{
			name:    "a harness restart that paused the run, whose note failed",
			probe:   pausedForBuildMoveNoteFailed,
			outside: true,
		},
		{
			name: "a stop the development manager decided",
			probe: func(h *scheduleHarness, id string) (Outcome, error) {
				h.retire(id)
				return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusCancelled,
					Failure: "the development manager stopped the run", StopClass: runstate.StopManager}, errors.New("the development manager stopped the run")
			},
			outside: true,
		},
		{
			name: "a cancellation whose origin is not recorded",
			probe: func(h *scheduleHarness, id string) (Outcome, error) {
				h.retire(id)
				return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusCancelled,
					Failure: "the run was cancelled", StopClass: runstate.StopCancelled}, errors.New("the run was cancelled")
			},
		},
		{
			name:  "a review that blocked the change",
			probe: blockedStorm,
		},
		{
			name:  "the harness's own step failing around the run",
			probe: stoppedFor(runstate.StopHarness),
		},
		{
			name:  "a stop reason this build does not recognise",
			probe: stoppedFor(inventedStop),
		},
	} {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			const cooldown = 10 * time.Minute
			harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four", "yoyodyne-five", "yoyodyne-six", "yoyodyne-seven")...)
			harness.blockedRuns = 3
			harness.cooldown = cooldown
			started := map[string]time.Time{}
			harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
				h.mu.Lock()
				started[id] = h.now
				h.mu.Unlock()
				switch id {
				case "yoyodyne-four", "yoyodyne-five":
					return shape.probe(h, id)
				case "yoyodyne-six", "yoyodyne-seven":
					landed := h.complete(id)
					landed.RunID = "run-" + id
					return landed, nil
				}
				return blockedStorm(h, id)
			}
			harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
				return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
			}
			harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 60 }

			scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
			if _, err := scheduler.Schedule(context.Background()); err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}

			if !shape.outside {
				// The contrast: a genuinely blocked probe spends a cycle and puts the
				// question to her again, once for each of the two. The probe after
				// them lands and lifts the hold, so the cycles are read off the
				// hold it lifted.
				if len(harness.releases) != 1 {
					t.Fatalf("released %d time(s), want the hold lifted by the probe after the two blocked ones", len(harness.releases))
				}
				lifted := harness.releases[0].Brake
				if lifted == nil || lifted.Cycles != 2 || len(harness.summonses) != 3 {
					t.Fatalf("lifted hold = %#v, summoned %d time(s), want each blocked probe counted as a cycle and put to her", lifted, len(harness.summonses))
				}
				return
			}
			if len(harness.releases) != 1 {
				t.Fatalf("released %d time(s), want the hold lifted by the next probe landing", len(harness.releases))
			}
			lifted := harness.releases[0].Brake
			if lifted == nil || lifted.Cycles != 0 || lifted.Probes != 3 || lifted.Probe == nil || lifted.Probe.WorkItemID != "yoyodyne-six" || !lifted.Probe.Landed {
				t.Fatalf("lifted hold = %#v, want no cycle spent and the third probe, of the sixth item, landed", lifted)
			}
			if len(harness.summonses) != 1 {
				t.Fatalf("summoned %d time(s), want her summoned only at the trip", len(harness.summonses))
			}
			harness.mu.Lock()
			defer harness.mu.Unlock()
			for _, pair := range [][2]string{{"yoyodyne-four", "yoyodyne-five"}, {"yoyodyne-five", "yoyodyne-six"}} {
				_, probed := started[pair[1]]
				if gap := started[pair[1]].Sub(started[pair[0]]); !probed || gap < cooldown {
					t.Fatalf("the probe of %s started %s after the stopped probe of %s, want the usual %s between probes", pair[1], gap, pair[0], cooldown)
				}
			}
		})
	}
}

// A probe whose session went away holding it — a harness restart that left no
// ending — is settled by the session that comes back, spends no cycle, and the
// next probe waits the usual time between probes rather than starting at once.
func TestAProbeLostToAHarnessRestartWaitsTheUsualTimeBeforeTheNextProbe(t *testing.T) {
	t.Parallel()

	const cooldown = 10 * time.Minute
	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.cooldown = cooldown
	tripped := harness.now.Add(-time.Hour)
	harness.held = &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        tripped,
		HeldBy:        runstate.IntakeHolderBrake,
		Reason:        "3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3",
		Brake: &runstate.IntakeBrake{
			Blocked:        []runstate.BrakeBlockedRun{{WorkItemID: "yoyodyne-earlier", Reason: "review blocked it"}},
			CooldownEndsAt: harness.now.Add(-time.Minute),
			Probe:          &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.428.99.11", StartedAt: harness.now.Add(-10 * time.Minute)},
			Probes:         1,
		},
	}
	var probedAt time.Time
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		if probedAt.IsZero() {
			probedAt = h.now
		}
		h.mu.Unlock()
		landed := h.complete(id)
		landed.RunID = "run-" + id
		return landed, nil
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 20 }
	began := harness.now

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	if _, err := scheduler.Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.releases) != 1 {
		t.Fatalf("released %d time(s), want the next probe to have landed and lifted the hold", len(harness.releases))
	}
	lifted := harness.releases[0].Brake
	if lifted == nil || lifted.Cycles != 0 || lifted.Probe == nil || lifted.Probe.WorkItemID != "yoyodyne-one" || !lifted.Probe.Landed {
		t.Fatalf("lifted hold = %#v, want no cycle spent and the new probe landed", lifted)
	}
	if gap := probedAt.Sub(began); gap < cooldown {
		t.Fatalf("the next probe started %s into the session, want the usual %s between probes", gap, cooldown)
	}
}

// What the brake counts reads the stop cause the run recorded, and nothing
// else: a cause outside the work and a stop the operator or the development
// manager asked for are never counted; a cause on the work or a bound on it,
// the harness's own step failing around the run, a dead process, a cancellation
// naming no origin, a publishing failure, a cause nobody recorded, and a cause
// this build does not recognise are.
func TestTheBrakeReadsTheRecordedStopCause(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		class runstate.StopClass
		want  bool
	}{
		{runstate.StopRedeploy, false},
		{runstate.StopProvider, false},
		{runstate.StopProviderIdle, false},
		{runstate.StopRelaunchBudget, false},
		{runstate.StopUsagePause, false},
		{runstate.CauseUsageWindow.StopClass(), false},
		{runstate.StopDeveloperAccount, false},
		{runstate.StopReviewAccount, false},
		{runstate.StopOperator, false},
		{runstate.StopManager, false},
		{runstate.CauseTransportFailure.StopClass(), false},
		{runstate.CauseMergeQueueHeld.StopClass(), false},
		{runstate.StopCheckTimeout, false},
		{runstate.CauseCheckTimeLimit.StopClass(), false},
		{runstate.CauseCheckStageBound.StopClass(), false},
		{runstate.StopLostRaces, false},
		{runstate.CauseNoCheckRan.StopClass(), false},
		{runstate.StopReviewBudget, false},
		{runstate.StopBaseFailing, false},
		{runstate.StopForgeQueueRequired, false},
		{runstate.StopIntegration, true},
		{runstate.StopIntegrationBudget, true},
		{runstate.StopProviderBudget, true},
		{runstate.StopPromotionWait, true},
		{runstate.StopDeadClaim, true},
		{runstate.StopCancelled, true},
		{runstate.StopPublish, true},
		{runstate.StopHarness, true},
		{runstate.StopRecording, true},
		{runstate.StopCleanup, true},
		{inventedStop, true},
		{runstate.StopReview, true},
		{runstate.StopChecks, true},
		{runstate.StopRepairBudget, true},
		{runstate.StopUnknown, true},
		{"", true},
	} {
		started := Started{Failure: "stopped", Outcome: Outcome{Blocked: true, StopClass: test.class}}
		if got := started.blockedRun(); got != test.want {
			t.Errorf("blockedRun() with stop class %q = %t, want %t", test.class, got, test.want)
		}
	}
	// A publishing failure the forge's retries running out ended is left out;
	// the same failure with retries still left in the window counts.
	var spent []runstate.Retry
	var waited time.Duration
	for attempt := 1; waited+recovery.Interval(attempt) <= recovery.Window; attempt++ {
		delay := recovery.Interval(attempt)
		spent = append(spent, runstate.Retry{Boundary: runstate.RetryMergeConfirmation, Attempt: attempt, DelaySeconds: int64(delay / time.Second)})
		waited += delay
	}
	exhausted := Started{Failure: "stopped", Outcome: Outcome{Blocked: true, StopClass: runstate.StopPublish, Retries: spent}}
	if exhausted.blockedRun() {
		t.Error("blockedRun() = true for a publishing failure after the forge's retries ran out, want it left out")
	}
	retried := Started{Failure: "stopped", Outcome: Outcome{Blocked: true, StopClass: runstate.StopPublish, Retries: spent[:1]}}
	if !retried.blockedRun() {
		t.Error("blockedRun() = false for a publishing failure with retries left, want it counted")
	}
	paused := Started{Failure: "record the pause on the work item: timed out", Outcome: Outcome{Paused: true}}
	if paused.blockedRun() {
		t.Error("blockedRun() = true for a paused run, want a paused run never counted")
	}
}

// inventedStop is a stop cause no build of the harness has ever recorded, as a
// record from a newer build would carry one this build does not know.
const inventedStop = runstate.StopClass("a-cause-from-a-newer-build")

// stoppedFor is a run that failed blocked with the given stop cause recorded.
func stoppedFor(class runstate.StopClass) func(*scheduleHarness, string) (Outcome, error) {
	return func(h *scheduleHarness, id string) (Outcome, error) {
		h.retire(id)
		failure := "the run stopped: " + string(class)
		return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusFailed, Blocked: true,
			Failure: failure, StopClass: class}, errors.New(failure)
	}
}

// Runs stopped for a cause this build does not recognise, and runs the
// harness's own step failed around, count towards the hold and trip it; the
// session's log names the unrecognised cause each time it is met, and says
// nothing about the causes it does recognise.
func TestUnrecognisedStopsAndHarnessFailuresCountTowardsTheHold(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.blockedRuns = 3
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if id == "yoyodyne-two" {
			return stoppedFor(runstate.StopHarness)(h, id)
		}
		return stoppedFor(inventedStop)(h, id)
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	var log bytes.Buffer

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock, Log: &log}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked == nil || schedule.Braked.Brake == nil || len(schedule.Braked.Brake.Blocked) != 3 {
		t.Fatalf("schedule = %s, want all three stops counted and the hold tripped", schedule.Render())
	}
	logged := log.String()
	if got := strings.Count(logged, `does not recognise ("a-cause-from-a-newer-build")`); got != 2 {
		t.Fatalf("log = %q, want the unrecognised cause named once for each of its two stops", logged)
	}
	for _, id := range []string{"yoyodyne-one", "yoyodyne-three"} {
		if !strings.Contains(logged, id+": run run-"+id+" stopped for a reason this build does not recognise") {
			t.Errorf("log = %q, want the stop of %s named", logged, id)
		}
	}
	if strings.Contains(logged, "yoyodyne-two") {
		t.Errorf("log = %q, want nothing said about a cause the build recognises", logged)
	}
}

// diedAtStart is a claim audit that, at the first pull of the session, finds
// three runs dead together — what one restart of the machine leaves — and
// nothing at any later pull.
func diedAtStart(h *scheduleHarness, _ []beads.WorkItem) (ClaimSweep, error) {
	h.mu.Lock()
	first := h.audits == 1
	h.mu.Unlock()
	if !first {
		return ClaimSweep{}, nil
	}
	var died []runstate.ReleasedClaim
	for _, id := range []string{"yoyodyne-gone-one", "yoyodyne-gone-two", "yoyodyne-gone-three"} {
		died = append(died, runstate.ReleasedClaim{WorkItemID: id, RunID: "run-" + id})
	}
	return ClaimSweep{Released: died, Died: died}, nil
}

// Three runs found dead together when the watch session starts count toward
// the hold on new work as one stop, not three: with the brake at three, the
// restart alone holds nothing, and the runs after it that land clear it.
func TestRunsFoundDeadTogetherAtTheSessionsStartCountOnce(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.blockedRuns = 3
	harness.audit = diedAtStart
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{}, errors.New("nobody should be summoned over one restart")
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked != nil || schedule.BlockedInARow != 1 || len(harness.summonses) != 0 {
		t.Fatalf("braked = %#v, blocked in a row = %d, want the three dead runs counted as one stop and nothing held", schedule.Braked, schedule.BlockedInARow)
	}
}

// The one stop the dead runs count as is a real one: with the brake at two, it
// and one run that blocked after it trip the hold, and the trip names every
// dead run in the one entry beside the blocked run.
func TestRunsFoundDeadTogetherCountTowardTheHoldAsOneStop(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.blockedRuns = 2
	harness.audit = diedAtStart
	harness.run = blockedStorm
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked == nil || schedule.Braked.Brake == nil {
		t.Fatalf("schedule = %s, want the dead runs and one blocked run to trip the hold", schedule.Render())
	}
	blocked := schedule.Braked.Brake.Blocked
	if len(blocked) != 2 || blocked[0].WorkItemID != "yoyodyne-gone-one" || blocked[1].WorkItemID != "yoyodyne-one" {
		t.Fatalf("trip names %+v, want the dead runs as one entry and then the blocked run", blocked)
	}
	for _, run := range []string{"run-yoyodyne-gone-one", "run-yoyodyne-gone-two", "run-yoyodyne-gone-three"} {
		if !strings.Contains(blocked[0].Reason, run) {
			t.Errorf("the dead runs' entry says %q, want it to name %s", blocked[0].Reason, run)
		}
	}
	if !strings.Contains(blocked[0].Reason, "3 runs were found dead together, counted as one stop") {
		t.Errorf("the dead runs' entry says %q, want it to say the three count as one", blocked[0].Reason)
	}
}
