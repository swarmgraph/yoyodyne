package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestMissedPassRecordsSleepDowntimeAndWaitingBehindAnotherPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 16, 52, 0, 0, time.UTC)
	due := now.Add(-3 * time.Hour)
	for _, test := range []struct {
		name, want string
		waiting    time.Duration
		severity   report.Severity
	}{
		{"sleep", "the machine was asleep", 0, report.SeverityWarning},
		{"down", "the harness was not watching", 0, report.SeverityWarning},
		{"waiting", "waiting its turn behind", 3 * time.Hour, report.SeverityCritical},
		{"short-wait", "waiting its turn behind", 30 * time.Minute, report.SeverityWarning},
		{"unknown", "no machine sleep, harness downtime or wait behind another pass was established", 0, report.SeverityCritical},
	} {
		t.Run(test.name, func(t *testing.T) {
			sweeps := sweepStore(t)
			due := due
			if test.name == "short-wait" {
				due = now.Add(-75 * time.Minute)
			}
			if _, err := sweeps.Claim(ctx, "owed-pass", time.Hour, due.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			machine, err := runstate.NewSupervisionStore(t.TempDir(), "example")
			if err != nil {
				t.Fatal(err)
			}
			watch, err := runstate.NewWatchStore(t.TempDir(), "example")
			if err != nil {
				t.Fatal(err)
			}
			observation := runstate.MachineObservation{At: due, Watching: true}
			switch test.name {
			case "sleep":
				observation.Power = []runstate.PowerEvent{{At: due, Source: "pmset: Sleep"}, {At: now.Add(-time.Minute), Awake: true, Source: "pmset: Wake"}}
			case "down":
				observation.Watching = false
			case "waiting", "short-wait":
				if err := watch.Record(runstate.WatchTransition{SchemaVersion: runstate.WatchSchemaVersion, ProductID: "example", SessionID: "watch-0123456789abcdef0123456789abcdef", State: runstate.WatchWatching, At: due, RecurringPass: &runstate.WatchPass{Task: "another-pass", At: due}}); err != nil {
					t.Fatal(err)
				}
				if err := sweeps.Append(runstate.Sweep{Task: "another-pass", Role: domain.RoleArchitect, StartedAt: due, EndedAt: due.Add(test.waiting), Turns: 1, Result: complete("nothing")}); err != nil {
					t.Fatal(err)
				}
			}
			if err := machine.RecordMachine(ctx, observation); err != nil {
				t.Fatal(err)
			}
			if test.name == "down" {
				if err := machine.RecordMachine(ctx, runstate.MachineObservation{At: due.Add(time.Minute), Watching: true}); err != nil {
					t.Fatal(err)
				}
			}
			if err := machine.RecordMachine(ctx, runstate.MachineObservation{At: now, Watching: true}); err != nil {
				t.Fatal(err)
			}
			filed := &filedReports{}
			trigger := Trigger{Tasks: map[string]config.RecurringTask{"owed-pass": {Enabled: true, Role: domain.RoleArchitect, Every: config.Duration(time.Hour)}}, Claims: sweeps, Reports: sweeps, Clock: &movingRecurringClock{now: now}, Breakage: filed, Attribution: report.Attribution{ProductID: "example", RepositoryID: "example"}}
			trigger.Availability = func(from, to time.Time, task string) readmodel.GapCause {
				availability := readmodel.ReadWatchAvailability(readmodel.Sources{Machine: machine, Sessions: watch, Sweeps: sweeps, Now: func() time.Time { return to }})
				return availability.Cause(from, to, task)
			}
			watching := recurringWatch{opened: now, missed: map[string]time.Time{}, held: recurringHold{why: "previous pass failed", at: due.Add(-time.Minute), refused: true}}
			scheduler := Scheduler{Now: func() time.Time { return now }}
			schedule := Schedule{}
			scheduler.missed(ctx, &schedule, Pull{Recurring: trigger}, &watching)
			passes, _, err := sweeps.List()
			if err != nil {
				t.Fatal(err)
			}
			miss := passes[len(passes)-1]
			if !miss.IsMiss() || !strings.Contains(miss.Problem, test.want) || strings.Contains(miss.Problem, "previous pass failed") {
				t.Fatalf("miss: %+v", miss)
			}
			if len(filed.filed) != 1 || filed.filed[0].Severity != test.severity {
				t.Fatalf("reports: %+v", filed.filed)
			}
			scheduler.missed(ctx, &schedule, Pull{Recurring: trigger}, &watching)
			if len(filed.filed) != 1 {
				t.Fatal("the same gap was reported twice")
			}
		})
	}
}

func TestCadenceChangesDoNotInventMissedObligations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	zone := time.FixedZone("PDT", -7*60*60)
	fired := time.Date(2026, 10, 2, 8, 18, 18, 0, zone)
	adopted := time.Date(2026, 10, 2, 16, 15, 0, 0, zone)
	now := time.Date(2026, 10, 2, 16, 27, 0, 0, zone)
	for _, test := range []struct {
		name      string
		old, next time.Duration
		missed    bool
		due       time.Time
	}{
		{"shortened", 24 * time.Hour, time.Hour, false, adopted},
		{"lengthened", time.Hour, 24 * time.Hour, false, fired.Add(24 * time.Hour)},
		{"unchanged", time.Hour, time.Hour, true, fired.Add(time.Hour)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := sweepStore(t)
			if _, err := store.Claim(ctx, "architect-pass", test.old, fired); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.Adopt(ctx, "architect-pass", test.next, adopted); err != nil {
				t.Fatal(err)
			}
			clock := &movingRecurringClock{now: now}
			trigger := Trigger{Tasks: map[string]config.RecurringTask{"architect-pass": {Enabled: true, Role: domain.RoleArchitect, Every: config.Duration(test.next)}}, Claims: store, Reports: store, Clock: clock}
			dues, err := trigger.Cadence(ctx)
			if err != nil || len(dues) != 1 || !dues[0].At.Equal(test.due) || !dues[0].LastFired.Equal(fired) {
				t.Fatalf("cadence: %+v, %v", dues, err)
			}
			watch := recurringWatch{opened: adopted, missed: map[string]time.Time{}}
			schedule := Schedule{}
			Scheduler{Now: func() time.Time { return clock.now }}.missed(ctx, &schedule, Pull{Recurring: trigger}, &watch)
			passes, _, err := store.List()
			if err != nil || (len(passes) > 0) != test.missed {
				t.Fatalf("passes: %+v, %v", passes, err)
			}
			// Genuine overdue detection remains after the changed cadence's first
			// interval. This second trigger stands in for a restarted watch.
			if test.name == "shortened" {
				clock.now = adopted.Add(75 * time.Minute)
				watch = recurringWatch{opened: clock.now, missed: map[string]time.Time{}}
				Scheduler{Now: func() time.Time { return clock.now }}.missed(ctx, &schedule, Pull{Recurring: trigger}, &watch)
				passes, _, err = store.List()
				if err != nil || len(passes) != 1 || !passes[0].StartedAt.Equal(adopted) {
					t.Fatalf("genuine overdue pass: %+v, %v", passes, err)
				}
				if !strings.Contains(passes[0].Problem, "last actual firing") || !strings.Contains(passes[0].Problem, "overdue under the effective schedule: 1h15m0s") {
					t.Fatalf("firing age and overdue duration were not separate: %s", passes[0].Problem)
				}
			}
		})
	}
	unknown := RecurringDue{Every: time.Hour}
	scheduleEvidence(&unknown, runstate.SweepClaim{FiredAt: fired})
	if !unknown.At.IsZero() || !strings.Contains(unknown.ScheduleNote, "unavailable") || !unknown.LastFired.Equal(fired) {
		t.Fatalf("unknown adoption timing: %+v", unknown)
	}
}

func TestAStoppedResponseCarriesItsObservationInTheDurableRun(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := opaqueDeathBackend(10, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
	pipeline.Availability = func(from, to time.Time, task string) readmodel.GapCause {
		if from.IsZero() || to.IsZero() || task != "" {
			t.Errorf("response interval: %s to %s, %q", from, to, task)
		}
		return readmodel.GapCause{Why: "the machine was asleep during the response, according to the OS"}
	}
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(outcome.Failure, "the machine was asleep") {
		t.Fatalf("stopped response: %+v, %v", outcome, err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil || !strings.Contains(state.Failure, "the machine was asleep") {
		t.Fatalf("durable response cause: %+v, %v", state, err)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	observations := 0
	for _, event := range events {
		if event.Source == "harness" && strings.Contains(string(event.Payload), "observations during the stopped response") {
			observations++
		}
	}
	if observations != 3 {
		t.Fatalf("recorded %d stopped responses, want the initial attempt and two relaunches", observations)
	}
}
