package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The scheduler reaches the availability callback wired by recurringTrigger,
// with a cadence held in place so the second pull records the miss. The role
// and tracker are doubles; the availability and scheduler are production code.
func TestProductionMachineAvailabilityPreservesCurrentHoldsAndSeparatesReadProblems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	due := time.Date(2000, 9, 30, 13, 52, 0, 0, time.UTC)
	reset := "weekly limit; resets at 09:52 PDT"
	for _, test := range []struct {
		name, refusal, want string
		severity            report.Severity
	}{
		{"capacity", reset, reset, report.SeverityWarning},
		{"conversation", "the conversation is held by another process", "conversation is held", report.SeverityWarning},
		{"schedule", "the schedule cannot be claimed", "schedule cannot be claimed", report.SeverityCritical},
		{"sleep and capacity", reset, reset, report.SeverityWarning},
		{"history unavailable", reset, reset, report.SeverityWarning},
		{"history cannot be opened", reset, reset, report.SeverityWarning},
		{"previous firing failed", "previous pass failed", "no machine sleep, harness downtime or wait behind another pass was established", report.SeverityCritical},
		{"refusal before this gap", "previous pass failed", "no machine sleep, harness downtime or wait behind another pass was established", report.SeverityCritical},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := runstate.NewStore(root, "example")
			if err != nil {
				t.Fatal(err)
			}
			watch, err := runstate.NewWatchStore(root, "example")
			if err != nil {
				t.Fatal(err)
			}
			intake, err := runstate.NewIntakeHoldStore(root, "example")
			if err != nil {
				t.Fatal(err)
			}
			directives, err := runstate.NewDirectiveStore(root, "example")
			if err != nil {
				t.Fatal(err)
			}
			parts := components{stateRoot: root, store: store, watch: watch, config: config.Config{
				Product: config.Product{ID: "example"}, RecurringTasks: developmentManagerSweep(),
			}}
			if test.name != "history unavailable" {
				machine, err := runstate.NewSupervisionStore(root, "example")
				if err != nil {
					t.Fatal(err)
				}
				observation := runstate.MachineObservation{At: due, Watching: true}
				if test.name == "sleep and capacity" {
					observation.Power = []runstate.PowerEvent{{At: due}, {At: due.Add(time.Hour), Awake: true}}
				}
				if err := machine.RecordMachine(ctx, observation); err != nil {
					t.Fatal(err)
				}
				if err := machine.RecordMachine(ctx, runstate.MachineObservation{At: due.Add(time.Hour), Watching: true}); err != nil {
					t.Fatal(err)
				}
			}
			if test.name == "history cannot be opened" {
				parts.stateRoot = ""
			}
			wired := recurringTrigger(parts, "", io.Discard).(*orchestrator.Trigger)
			current := due
			if test.name == "refusal before this gap" {
				current = due.Add(-time.Minute)
			}
			task := "development-manager-sweep"
			cadence := &machineCadence{trigger: wired, due: due, firing: orchestrator.Fired{Task: task, Role: domain.RoleDevelopmentManager, Problem: test.refusal}}
			if test.name == "schedule" {
				cadence.problem = errors.New(test.refusal)
			}
			if test.name == "previous firing failed" {
				cadence.firing.Turns = 1
			}
			pulls := 0
			scheduler := orchestrator.Scheduler{Watching: true, Now: func() time.Time { return current },
				Open: func(context.Context) (orchestrator.Pull, error) {
					pulls++
					return orchestrator.Pull{Tracker: emptyMachineQueue{}, Runs: store, Gates: store, Intake: intake, Directives: directives, Capacity: 1, Poll: time.Minute, RedeployDrainLimit: time.Minute, Recurring: cadence,
						Start: func(context.Context, string, runstate.Selection) (orchestrator.Outcome, error) {
							t.Error("an empty queue started a run")
							return orchestrator.Outcome{}, errors.New("empty queue")
						}}, nil
				},
				Sleep: func(context.Context, time.Duration) bool {
					current = due.Add(time.Hour)
					return pulls < 2
				},
			}
			if _, err := scheduler.Schedule(ctx); err != nil {
				t.Fatal(err)
			}
			if len(cadence.misses) != 1 {
				t.Fatalf("misses: %+v", cadence.misses)
			}
			miss := cadence.misses[0]
			if !strings.Contains(miss.Why, test.want) || miss.Severity != test.severity {
				t.Fatalf("miss: %+v, want %q at %s", miss, test.want, test.severity)
			}
			if test.name == "sleep and capacity" && !strings.Contains(miss.Why, "the machine was asleep") {
				t.Fatalf("current hold replaced sleep evidence: %+v", miss)
			}
			if strings.HasPrefix(test.name, "history") && !strings.Contains(miss.Why, "machine observations incomplete:") {
				t.Fatalf("history problem was lost: %+v", miss)
			}
			if strings.Contains(test.refusal, "previous pass failed") && strings.Contains(miss.Why, "previous pass failed") {
				t.Fatalf("an earlier failure was blamed for this gap: %+v", miss)
			}
		})
	}
}

// machineCadence is the production trigger's miss reading with the firing
// stubbed. It holds the trigger rather than embedding it, so it fires in place
// through Fire and the scheduler does not take the trigger's own firings beside
// the pull.
type machineCadence struct {
	trigger *orchestrator.Trigger
	due     time.Time
	firing  orchestrator.Fired
	problem error
	misses  []orchestrator.RecurringMiss
}

func (c *machineCadence) Fire(context.Context) (orchestrator.RecurringSweep, error) {
	return orchestrator.RecurringSweep{Fired: []orchestrator.Fired{c.firing}}, c.problem
}

func (c *machineCadence) MissCause(from, to time.Time, task string) readmodel.GapCause {
	return c.trigger.MissCause(from, to, task)
}

func (c *machineCadence) Cadence(context.Context) ([]orchestrator.RecurringDue, error) {
	return []orchestrator.RecurringDue{{Task: c.firing.Task, Role: c.firing.Role, Every: time.Hour, At: c.due}}, nil
}

func (c *machineCadence) Missed(_ context.Context, miss orchestrator.RecurringMiss) error {
	c.misses = append(c.misses, miss)
	return nil
}

type emptyMachineQueue struct{}

func (emptyMachineQueue) List(context.Context, string) ([]beads.WorkItem, error) { return nil, nil }
func (emptyMachineQueue) Ready(context.Context) ([]beads.WorkItem, error)        { return nil, nil }
