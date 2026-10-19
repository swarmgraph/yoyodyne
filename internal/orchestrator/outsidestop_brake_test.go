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

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
// spent, the development manager is not summoned over it again, and the next
// probe starts at the next poll rather than a cooldown later. A probe that
// genuinely blocked is the contrast, and still spends a cycle.
func TestAProbeStoppedFromOutsideTheWorkNeitherKeepsTheHoldNorCounts(t *testing.T) {
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
			name:  "a review that blocked the change",
			probe: blockedStorm,
		},
	} {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four", "yoyodyne-five", "yoyodyne-six")...)
			harness.blockedRuns = 3
			harness.cooldown = 30 * time.Minute
			started := map[string]time.Time{}
			harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
				h.mu.Lock()
				started[id] = h.now
				h.mu.Unlock()
				switch id {
				case "yoyodyne-four":
					return shape.probe(h, id)
				case "yoyodyne-five", "yoyodyne-six":
					landed := h.complete(id)
					landed.RunID = "run-" + id
					return landed, nil
				}
				return blockedStorm(h, id)
			}
			harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
				return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
			}
			harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 40 }

			scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
			if _, err := scheduler.Schedule(context.Background()); err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}

			if !shape.outside {
				// The contrast: a genuinely blocked probe spends a cycle and puts the
				// question to her again.
				hold, held, _ := harness.Held()
				if !held || hold.Brake == nil || hold.Brake.Cycles == 0 || len(harness.summonses) < 2 {
					t.Fatalf("hold = %#v, summoned %d time(s), want a blocked probe counted as a cycle", hold.Brake, len(harness.summonses))
				}
				return
			}
			if len(harness.releases) != 1 {
				t.Fatalf("released %d time(s), want the hold lifted by the next probe landing", len(harness.releases))
			}
			lifted := harness.releases[0].Brake
			if lifted == nil || lifted.Cycles != 0 || lifted.Probes != 2 || lifted.Probe == nil || lifted.Probe.WorkItemID != "yoyodyne-five" || !lifted.Probe.Landed {
				t.Fatalf("lifted hold = %#v, want no cycle spent and the second probe, of the fifth item, landed", lifted)
			}
			if len(harness.summonses) != 1 {
				t.Fatalf("summoned %d time(s), want her summoned only at the trip", len(harness.summonses))
			}
			harness.mu.Lock()
			gap := started["yoyodyne-five"].Sub(started["yoyodyne-four"])
			harness.mu.Unlock()
			if _, probed := started["yoyodyne-five"]; !probed || gap < 0 || gap > 2*time.Minute {
				t.Fatalf("the next probe started %s after the stopped one, want it at the next poll rather than a cooldown later", gap)
			}
		})
	}
}

// A probe whose session went away holding it — a harness restart that left no
// ending — is settled by the session that comes back, and the next probe
// starts at once rather than a cooldown later.
func TestAProbeLostToAHarnessRestartStartsTheNextProbeAtOnce(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.cooldown = 30 * time.Minute
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
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 5 }
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
	if gap := probedAt.Sub(began); gap > 2*time.Minute {
		t.Fatalf("the next probe started %s into the session, want it at once rather than a cooldown later", gap)
	}
}

// What the brake counts reads the stop cause the run recorded, and nothing
// else: a cause outside the work is never counted, a cause on the work or a
// bound on it is, and a cause nobody recorded is not guessed either way.
func TestTheBrakeReadsTheRecordedStopCause(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		class runstate.StopClass
		want  bool
	}{
		{runstate.StopRedeploy, false},
		{runstate.StopProvider, false},
		{runstate.StopDeveloperAccount, false},
		{runstate.StopDeadClaim, false},
		{runstate.StopCancelled, false},
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
	paused := Started{Failure: "record the pause on the work item: timed out", Outcome: Outcome{Paused: true}}
	if paused.blockedRun() {
		t.Error("blockedRun() = true for a paused run, want a paused run never counted")
	}
}
