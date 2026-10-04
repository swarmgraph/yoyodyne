package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestAPassFailureNamesTheOperatorOnlyForARecordedPersonOnlyStep(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	passes, _ := runstate.NewSweepStore(root, "example")
	reports, _ := runstate.NewReportStore(root, "example")
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		if err := passes.Append(runstate.Sweep{Task: "manager-pass", Role: domain.RoleDevelopmentManager, StartedAt: at, EndedAt: at.Add(time.Minute), Failed: true, Problem: "settings.json cannot be changed"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := passes.RecordPassFailures(context.Background(), reports, report.Attribution{RepositoryID: "example"}, "factory-watch"); err != nil {
		t.Fatal(err)
	}
	sources := Sources{Passes: passes, Reports: reports, ProgramManagers: []ProgramManagerInstance{{Agent: "factory-watch", Lane: "factory-flow"}}}
	entries, problem := ReadPassFailures(sources)
	if problem != "" || len(entries) != 1 || entries[0].Mover != MoverProgramManager {
		t.Fatalf("failure = %+v, %s", entries, problem)
	}
	const step = "put the notes-writer hook in .claude/settings.json by hand"
	if err := reports.Handle(report.Handling{SchemaVersion: report.HandlingSchemaVersion, ReportID: entries[0].FailingTask.ReportID, Role: domain.RoleProductManager, RunID: "chat-one", ProductID: "example", RepositoryID: "example", RecordedAt: start.Add(3 * time.Hour), NeedsOperator: true, Reason: step}); err != nil {
		t.Fatal(err)
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.FactoryProblems) != 1 || standing.FactoryProblems[0].Mover != MoverOperator || !strings.Contains(standing.FactoryProblems[0].Whose(), step) {
		t.Fatalf("person's finding = %+v", standing.FactoryProblems)
	}
	count := 0
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionFailingTask || entry.Kind == AttentionOperatorAction {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("finding appeared %d times: %+v", count, standing.NeedsHuman)
	}
	if !strings.Contains(RenderPassFailures(sources, domain.RoleProgramManager, "factory-watch"), step) {
		t.Fatal("watcher was not told the person's step")
	}
}
