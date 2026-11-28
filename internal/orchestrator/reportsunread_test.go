package orchestrator

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// unreadReportsRun runs one item whose developer files a report for the
// development manager, one with a severity not on the list, and one addressed
// to the operator, in a project whose recurring passes are the ones given.
func unreadReportsRun(t *testing.T, passes bool) (Outcome, *orchestratortest.Tracker, *fakeReports) {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "implemented the work item\n\n" + reportBlock(
		`{"severity":"warning","message":"the test server is left running after the suite","for":"development-manager"}`,
		`{"severity":"loud","message":"the plugin crashes on a full inventory"}`,
		`{"severity":"note","message":"tell him the temporary folder is refused","for":"operator"}`,
	)
	collector := &fakeReports{}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Reports = collector
	if passes {
		agents := maps.Clone(pipeline.Config.Agents)
		lead := agents[string(domain.RoleDeveloper)]
		lead.Role = domain.RoleProductManager
		agents[string(domain.RoleProductManager)] = lead
		pipeline.Config.Agents = agents
		pipeline.Config.RecurringTasks = productManagerSweep()
	}
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("a report changed what the run did: %#v", outcome.Status)
	}
	return outcome, tracker, collector
}

// With no pass reading the development manager's reports, hers included, the
// report is accepted all the same, kept as written, and the run keeps it whole
// for what it prints when it ends and for the item's notes. The refused ones
// are said in the closing lines with what was wrong, and their messages are
// kept beside it.
func TestAReportNoPassReadsAndARefusedReportReachTheRunsEndAndTheItemsNotes(t *testing.T) {
	t.Parallel()

	outcome, tracker, collector := unreadReportsRun(t, false)
	if len(collector.appended) != 1 || collector.appended[0].For != domain.RoleDevelopmentManager {
		t.Fatalf("collected = %#v, want the development manager's report filed as written", collector.appended)
	}
	if len(outcome.UnreadReports) != 1 || outcome.UnreadReports[0].ID != collector.appended[0].ID {
		t.Fatalf("unread = %#v", outcome.UnreadReports)
	}
	if len(outcome.RefusedReports) != 2 || outcome.RefusedReports[0].Refused.Message != "the plugin crashes on a full inventory" {
		t.Fatalf("refused = %#v", outcome.RefusedReports)
	}
	for _, want := range []string{`a developer report was refused: severity "loud"`, `a developer report was refused: for "operator"`, "kept in this run's summary and in the work item's notes"} {
		if !strings.Contains(outcome.ReportProblem, want) {
			t.Fatalf("report problem = %q, want %q", outcome.ReportProblem, want)
		}
	}
	for _, want := range []string{
		"warning report " + collector.appended[0].ID + ", for the development manager, which no pass reads here: the test server is left running after the suite",
		"refused loud report from the developer, naming no role: the plugin crashes on a full inventory",
		`refused note report from the developer, for "operator": tell him the temporary folder is refused`,
	} {
		if !strings.Contains(tracker.Notes, want) {
			t.Fatalf("item notes = %q\nwant %q", tracker.Notes, want)
		}
	}
}

// Where her pass runs, the development manager's report goes to it rather
// than to the run's end; a refused report is kept whichever passes run.
func TestAReportHerPassReadsIsNotPrintedAsReachingNoPass(t *testing.T) {
	t.Parallel()

	outcome, tracker, _ := unreadReportsRun(t, true)
	if len(outcome.UnreadReports) != 0 {
		t.Fatalf("unread = %#v, want none where her pass runs", outcome.UnreadReports)
	}
	if strings.Contains(tracker.Notes, "which no pass reads here") || !strings.Contains(tracker.Notes, "the plugin crashes on a full inventory") {
		t.Fatalf("item notes = %q", tracker.Notes)
	}
}
