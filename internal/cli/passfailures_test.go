package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/maintain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestTheProductWiresMaintenanceFindingsIntoTheConfiguredFactoryFlowPass(t *testing.T) {
	t.Parallel()
	resolved, err := config.LoadResolved(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	resolved.Config.Services.Maintenance.Enabled = true
	resolved.Config.Agents["factory-watch"] = config.AgentConfig{Role: domain.RoleProgramManager, Lane: "factory-flow", Triggers: config.Triggers{Every: config.Duration(time.Hour)}}
	root := t.TempDir()
	p := &product{resolved: resolved, stateRoot: root, program: "/opt/yoyo/bin/yoyo"}
	_, maintenance, err := p.residents(nil, nil)
	if err != nil || maintenance == nil || maintenance.RecordFailures == nil {
		t.Fatalf("maintenance = %+v, %v", maintenance, err)
	}
	store, _ := runstate.NewStore(root, resolved.Config.Product.ID)
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		at := start.Add(time.Duration(i) * 10 * time.Minute)
		if err := store.Sweeps().Append(runstate.Sweep{Task: config.MaintenanceTaskName, StartedAt: at, EndedAt: at.Add(time.Minute), Steps: []runstate.SweepStep{{Name: "reconcile", Outcome: runstate.StepFailed, Detail: "state refused"}}, Problem: "state refused"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := maintenance.RecordFailures(context.Background()); err != nil {
		t.Fatal(err)
	}
	reports, _ := runstate.NewReportStore(root, resolved.Config.Product.ID)
	filed, err := reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("filed = %+v, %v", filed, err)
	}
	trigger := recurringTrigger(components{config: resolved.Config, store: store, reports: reports, stateRoot: root}, "", io.Discard).(*orchestrator.Trigger)
	message := trigger.PassFailures(domain.RoleProgramManager, "factory-watch")
	if !strings.Contains(message, filed[0].ID) || !strings.Contains(message, "factory-flow program manager factory-watch") {
		t.Fatalf("factory-flow message = %q", message)
	}
	if other := trigger.PassFailures(domain.RoleProgramManager, "other-watch"); other != "" {
		t.Fatalf("another lane received %q", other)
	}
	if message := trigger.PassFailures(domain.RoleDevelopmentManager, ""); !strings.Contains(message, filed[0].ID) {
		t.Fatalf("resolving manager received %q", message)
	}
}

// The trim of the shared build cache is the supervisor's whether or not the
// maintenance pass is on, is held to the configured limit, and is left to the
// pass only where that runs at least hourly.
func TestTheProductWiresTheBuildCacheTrim(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		enabled bool
		every   time.Duration
		inPass  bool
	}{{true, 10 * time.Minute, true}, {true, 2 * time.Hour, false}, {false, 10 * time.Minute, false}} {
		resolved, err := config.LoadResolved(writeConfig(t, validConfig))
		if err != nil {
			t.Fatal(err)
		}
		resolved.Config.Services.Maintenance.Enabled = test.enabled
		resolved.Config.Services.Maintenance.Every = config.Duration(test.every)
		resolved.Config.Execution.BuildCacheLimitGiB = 7
		p := &product{resolved: resolved, stateRoot: t.TempDir(), program: "/opt/yoyo/bin/yoyo"}
		residents, pass, err := p.residents(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var trim *maintain.BuildCache
		for _, resident := range residents {
			if found, ok := resident.(*maintain.BuildCache); ok {
				trim = found
			}
		}
		if trim == nil || trim.Limit != 7<<30 || trim.InPass != test.inPass {
			t.Fatalf("%+v: trim = %+v", test, trim)
		}
		if test.enabled && pass.BuildCache != trim {
			t.Fatalf("%+v: the pass takes another trim than the supervisor's", test)
		}
	}
}
