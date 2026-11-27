package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A project holds one standing report for the development manager. The
// developer is handed it at its start and names it in a report of its own; the
// reviewer files the same fact in other numbers without naming it; and the
// developer names a second report nobody handed it. The run ends exactly as it
// would have, the standing report is counted twice, and the unhandled name is
// filed as a new report with the reason on the outcome.
func TestADeveloperAndAReviewerRepeatAStandingReportRatherThanFilingItAgain(t *testing.T) {
	t.Parallel()

	const standingID = "report-0123456789abcdef0123456789abcdef"
	const unlistedID = "report-fedcba9876543210fedcba9876543210"
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict+"\n\n"+reportBlock(`{"severity":"warning","message":"a test server was left running on port 9090 by run run-2222","for":"development-manager"}`))
	provider.DeveloperFinalText = "implemented the work item\n\n" + reportBlock(
		`{"severity":"note","message":"the server again, in my own words","for":"development-manager","repeats":"`+standingID+`"}`,
		`{"severity":"note","message":"something else entirely","repeats":"`+unlistedID+`"}`,
	)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pile, err := runstate.NewReportStore(t.TempDir(), pipeline.Config.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	standing := report.Report{
		SchemaVersion: report.SchemaVersion, ID: standingID, Role: domain.RoleDeveloper, RunID: "run-1111",
		ProductID: pipeline.Config.Product.ID, RepositoryID: string(pipeline.Config.Product.RepositoryID),
		Severity: report.SeverityWarning, Message: "a test server was left running on port 8080 by run run-1111",
		For: domain.RoleDevelopmentManager, RecordedAt: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
	}
	if err := pile.Append(standing); err != nil {
		t.Fatal(err)
	}
	pipeline.Reports = pile

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.ReportProblem != "" {
		t.Fatalf("outcome = %s, report problem %q", outcome.Status, outcome.ReportProblem)
	}

	var developerPrompt, reviewerPrompt string
	for _, request := range provider.Requests {
		switch request.Role {
		case domain.RoleDeveloper:
			developerPrompt = request.Prompt
		case domain.RoleReviewer:
			reviewerPrompt = request.Prompt
		}
	}
	for role, prompt := range map[string]string{"developer": developerPrompt, "reviewer": reviewerPrompt} {
		if !strings.Contains(prompt, "# Reports already standing in this project") || !strings.Contains(prompt, "- "+standingID+", for the development manager, warning: a test server") {
			t.Errorf("the %s was not handed the standing report", role)
		}
	}

	if len(outcome.Repeats) != 2 || outcome.Repeats[0].Count != 1 || outcome.Repeats[1].Count != 2 || !outcome.Repeats[0].Repeat.Named || outcome.Repeats[1].Repeat.Named {
		t.Fatalf("repeats = %#v", outcome.Repeats)
	}
	if len(outcome.Reports) != 1 || outcome.Reports[0].Message != "something else entirely" {
		t.Fatalf("new reports = %#v", outcome.Reports)
	}
	if !strings.Contains(outcome.ReportNotes, "was filed as a new report because it names") || !strings.Contains(outcome.ReportNotes, "not on the list") {
		t.Fatalf("report notes = %q", outcome.ReportNotes)
	}
	if reports, err := pile.List(); err != nil || len(reports) != 2 || reports[0].Message != standing.Message {
		t.Fatalf("pile = %#v, %v", reports, err)
	}
	recorded, err := store.Load(outcome.RunID)
	// The reviewer was handed the developer's new report as well, which stood
	// by then.
	if err != nil || len(recorded.StandingReportsOffered) != 2 || recorded.StandingReportsOffered[0] != standingID || recorded.StandingReportsOffered[1] != outcome.Reports[0].ID {
		t.Fatalf("offered on the record = %#v, %v", recorded.StandingReportsOffered, err)
	}
}
