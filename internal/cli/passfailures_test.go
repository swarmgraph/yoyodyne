package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
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
