package maintain

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

type failureWatch struct{ message string }

func (w *failureWatch) Wake(_ context.Context, _ domain.AgentRole, _, _, _, message string, _ orchestrator.RecurringTurnOptions) (orchestrator.Turn, error) {
	w.message = message
	return orchestrator.Turn{Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "watching the failure"}}, nil
}

func TestRepeatedMaintenanceFailuresAreFiledDeliveredKeptCurrentAndCleared(t *testing.T) {
	for _, watcher := range []string{"factory-flow-pm", ""} {
		t.Run("watcher="+watcher, func(t *testing.T) {
			f := newFixture(t)
			reports, err := runstate.NewReportStore(strings.TrimSuffix(f.sweeps.Root(), "/products/calc/sweeps"), "calc")
			if err != nil {
				t.Fatal(err)
			}
			attribution := report.Attribution{ProductID: "calc", RepositoryID: "calc"}
			record := func(ctx context.Context) error {
				return f.sweeps.RecordPassFailures(ctx, reports, attribution, watcher)
			}
			f.pass.RecordFailures = record
			f.runner.result = execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "read the run store: permission denied\nlast error"}
			sources := readmodel.Sources{Passes: f.sweeps, Reports: reports}
			if watcher != "" {
				sources.ProgramManagers = []readmodel.ProgramManagerInstance{{Agent: watcher, Lane: "factory-flow"}}
			}
			first := f.now
			for i := 1; i <= 5; i++ {
				f.pass1(t)
				filed, err := reports.List()
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if i >= 3 {
					want = 1
				}
				if len(filed) != want {
					t.Fatalf("failure %d filed %d findings, want %d", i, len(filed), want)
				}
				standing := readmodel.ReadStanding(context.Background(), sources)
				if len(standing.FactoryProblems) != want {
					t.Fatalf("failure %d standing = %+v", i, standing.FactoryProblems)
				}
				if i >= 3 {
					entry := standing.FactoryProblems[0]
					if entry.FailingTask.Failures != i || !entry.FailingTask.FirstAt.Equal(first) || entry.FailingTask.ReportID != filed[0].ID {
						t.Fatalf("finding = %+v", entry)
					}
					if strings.Contains(entry.What(), "\n") || !strings.Contains(entry.What(), first.In(time.Local).Format("2006-01-02 15:04 MST")) {
						t.Fatalf("failure line = %q", entry.What())
					}
					if !strings.Contains(standing.Render(), "maintenance has failed") {
						t.Fatalf("status omitted finding: %s", standing.Render())
					}
				}
				if i == 3 {
					wake := &failureWatch{}
					role, agent := domain.RoleProgramManager, watcher
					if watcher == "" {
						role = domain.RoleDevelopmentManager
					}
					trigger := orchestrator.Trigger{Claims: f.sweeps, Reports: f.sweeps, Roles: wake, RecordFailures: record,
						PassFailures: func(r domain.AgentRole, a string) string { return readmodel.RenderPassFailures(sources, r, a) }}
					if agent != "" {
						trigger.Instances = map[string]config.AgentConfig{agent: {Role: role, Lane: "factory-flow", Triggers: config.Triggers{Every: config.Duration(time.Hour)}}}
					} else {
						trigger.Tasks = map[string]config.RecurringTask{"manager-pass": {Role: role, Every: config.Duration(time.Hour), Enabled: true, Prompt: "watch"}}
					}
					if _, err := trigger.Fire(context.Background()); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(wake.message, filed[0].ID) || !strings.Contains(wake.message, "must") && !strings.Contains(wake.message, "Answer each finding") {
						t.Fatalf("watcher was not told: %q", wake.message)
					}
					if watcher == "" && !strings.Contains(wake.message, "no factory-flow program manager is configured") {
						t.Fatalf("missing fallback explanation: %q", wake.message)
					}
					// Handling and completing the watching pass must not clear it.
					if err := reports.Handle(report.Handling{SchemaVersion: report.HandlingSchemaVersion, ReportID: filed[0].ID, Role: domain.RoleProductManager, RunID: "chat-one", ProductID: "calc", RepositoryID: "calc", RecordedAt: f.now, Reason: "the development manager is investigating"}); err != nil {
						t.Fatal(err)
					}
					if entries, _ := readmodel.ReadPassFailures(sources); len(entries) != 1 {
						t.Fatalf("watcher completion or handling cleared failure: %+v", entries)
					}
				}
				f.now = f.now.Add(10 * time.Minute)
			}
			f.runner.result = execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "settled"}
			f.pass1(t)
			if err := record(context.Background()); err != nil {
				t.Fatal(err)
			}
			standing := readmodel.ReadStanding(context.Background(), sources)
			if len(standing.FactoryProblems) != 0 {
				t.Fatalf("success did not clear finding: %+v", standing.FactoryProblems)
			}
			handlings, err := reports.Handlings()
			if err != nil {
				t.Fatal(err)
			}
			if len(handlings) != 2 || handlings[1].PassFailureCleared != 5 || !strings.Contains(handlings[1].Reason, "after 5 consecutive failures") {
				t.Fatalf("clearing = %+v", handlings)
			}
			filed, _ := reports.List()
			if len(filed) != 1 {
				t.Fatalf("filed = %+v", filed)
			}
		})
	}
}
