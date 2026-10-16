package readmodel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestAPassFailureIsNamedEvenAfterTheStatusLineReachesItsLimit(t *testing.T) {
	t.Parallel()
	var entries []Attention
	for i := 0; i < maxListed+1; i++ {
		entries = append(entries, failingTaskAttention(FailingTask{Task: fmt.Sprintf("earlier-%d", i), Failures: 2}))
	}
	entries = append(entries, failingTaskAttention(FailingTask{Task: "maintenance", ProductPass: true, Failures: 3, FirstAt: time.Now(), Problem: "last error"}))
	if rendered := renderWaiting(entries); !strings.Contains(rendered, "maintenance has failed 3 times") {
		t.Fatalf("the pass was folded into an unnamed count: %s", rendered)
	}
}

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
	// A flag and ordinary repair prose cannot transfer ownership to a person.
	if err := reports.Handle(report.Handling{SchemaVersion: report.HandlingSchemaVersion, ReportID: entries[0].FailingTask.ReportID, Role: domain.RoleProductManager, RunID: "chat-one", ProductID: "example", RepositoryID: "example", RecordedAt: start.Add(150 * time.Minute), NeedsOperator: true, Reason: "repair the harness and rerun its tests"}); err != nil {
		t.Fatal(err)
	}
	entries, problem = ReadPassFailures(sources)
	if problem != "" || len(entries) != 1 || entries[0].Mover != MoverProgramManager || entries[0].FailingTask.Ownership.PersonStep != "" {
		t.Fatalf("ordinary repair was assigned to the operator: %+v, %s", entries, problem)
	}
	const step = "put the notes-writer hook in .claude/settings.json by hand"
	if err := reports.Handle(report.Handling{SchemaVersion: report.HandlingSchemaVersion, ReportID: entries[0].FailingTask.ReportID, Role: domain.RoleProductManager, RunID: "chat-one", ProductID: "example", RepositoryID: "example", RecordedAt: start.Add(3 * time.Hour), NeedsOperator: true, Reason: "a provider-refused file needs changing", PersonOnly: &ownership.PersonOnlyRemedy{Reason: ownership.PersonProtectedFile, Target: ".claude/settings.json", Step: step}}); err != nil {
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

func TestFailedManagersPassFailureReachesTheRoleThatCanAnswer(t *testing.T) {
	for _, tc := range []struct {
		failed, receiver           domain.AgentRole
		failedAgent, receiverAgent string
	}{
		{domain.RoleDevelopmentManager, domain.RoleProgramManager, "", "factory-watch"},
		{domain.RoleProgramManager, domain.RoleProductManager, "factory-watch", ""},
	} {
		t.Run(string(tc.failed), func(t *testing.T) {
			passes, _ := runstate.NewSweepStore(t.TempDir(), "example")
			start := time.Now()
			for i := 0; i < 3; i++ {
				at := start.Add(time.Duration(i) * time.Hour)
				if err := passes.Append(runstate.Sweep{Task: "failed-manager", Role: tc.failed, Agent: tc.failedAgent, StartedAt: at, EndedAt: at.Add(time.Minute), Failed: true, Problem: "process exited", FailureOutput: "last printed cause"}); err != nil {
					t.Fatal(err)
				}
			}
			sources := Sources{Passes: passes, ProgramManagers: []ProgramManagerInstance{{Agent: "factory-watch", Lane: "factory-flow"}}}
			text := RenderPassFailures(sources, tc.receiver, tc.receiverAgent)
			if !strings.Contains(text, "last printed cause") || !strings.Contains(text, "must answer this finding") {
				t.Fatalf("receiver got %q", text)
			}
			if text := RenderPassFailures(sources, tc.failed, tc.failedAgent); text != "" {
				t.Fatalf("failed role was asked to repair itself: %s", text)
			}
			if text := RenderPassFailures(sources, domain.RoleProgramManager, "another-manager"); text != "" {
				t.Fatalf("another program manager received the finding: %s", text)
			}
		})
	}
}
