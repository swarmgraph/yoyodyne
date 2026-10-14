package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The developer and the reviewer both notice something outside the work they
// were given, and both say so while their work succeeds. The run integrates
// exactly as it would have, and the reports are collected where somebody will
// read them rather than left in prose nothing surfaces.
func TestReportsAreCollectedWithoutChangingWhatTheRunDid(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict+"\n\n"+reportBlock(`{"severity":"note","message":"the fixture repository declares no checks of its own"}`))
	provider.DeveloperFinalText = "implemented the work item\n\n" +
		reportBlock(`{"severity":"warning","message":"the declared bundle version is inert; nothing reads it."}`)
	collector := &fakeReports{}
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Reports = collector
	pipeline.Build = "0123456789abcdef0123456789abcdef01234567"

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("a report changed what the run did: %#v", outcome)
	}
	if outcome.ReportProblem != "" {
		t.Fatalf("readable reports were reported as a problem: %q", outcome.ReportProblem)
	}
	if state, err := store.Load(outcome.RunID); err != nil || state.Failure != "" {
		t.Fatalf("run state = %#v, %v", state, err)
	}

	// Both roles reached the pile, each attributed to the role, the agent, the
	// run, and the work item it came from.
	if len(collector.appended) != 2 || len(outcome.Reports) != 2 {
		t.Fatalf("collected = %#v", collector.appended)
	}
	byRole := map[domain.AgentRole]report.Report{}
	for _, reported := range collector.appended {
		byRole[reported.Role] = reported
	}
	developed, reported := byRole[domain.RoleDeveloper], byRole[domain.RoleReviewer]
	if developed.Severity != report.SeverityWarning || !strings.Contains(developed.Message, "bundle version is inert") {
		t.Fatalf("developer report = %#v", developed)
	}
	if reported.Severity != report.SeverityNote || !strings.Contains(reported.Message, "declares no checks") {
		t.Fatalf("reviewer report = %#v", reported)
	}
	for _, collected := range collector.appended {
		if collected.RunID != outcome.RunID || collected.WorkItemID != tracker.Item.ID {
			t.Fatalf("report is not attributed to the run that made it: %#v", collected)
		}
		if collected.Agent != string(collected.Role) {
			t.Fatalf("report does not name the configured agent: %#v", collected)
		}
		// And the build the run executed, which is what the report is a claim
		// about: a defect reported from a build that predates its fix must not
		// read like a live one.
		if collected.Build != pipeline.Build {
			t.Fatalf("report build = %q, want the run's %q", collected.Build, pipeline.Build)
		}
	}

	// The developer's summary is what it said about the work, without the block
	// the operator was never meant to read as prose.
	if strings.Contains(outcome.Summary, "yoyodyne-report") || strings.Contains(outcome.Summary, "severity") {
		t.Fatalf("the report block stayed in the summary: %q", outcome.Summary)
	}
	if outcome.Summary != "implemented the work item" {
		t.Fatalf("summary = %q", outcome.Summary)
	}
	// The reviewer reported alongside an approval, and the approval stands.
	if outcome.ReviewDecision != "approve" {
		t.Fatalf("review decision = %q", outcome.ReviewDecision)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	requests := map[string]execution.ToolAudit{}
	performed := 0
	for _, event := range events {
		if event.Type != execution.EventToolRequested && event.Type != execution.EventToolPerformed {
			continue
		}
		var audit execution.ToolAudit
		if err := json.Unmarshal(event.Payload, &audit); err != nil {
			t.Fatal(err)
		}
		if audit.Tool != "report.file" || audit.Bounds.RequestsPerReply != 5 || strings.Contains(string(event.Payload), "fixture") || strings.Contains(string(event.Payload), "inert") {
			t.Fatalf("bad report tool audit: %s", event.Payload)
		}
		if event.Type == execution.EventToolRequested {
			requests[audit.ID] = audit
		} else {
			requested, found := requests[audit.ID]
			if !found || requested.Role != audit.Role {
				t.Fatalf("outcome without request: %#v", audit)
			}
			performed++
		}
	}
	if len(requests) != 2 || performed != 2 {
		t.Fatalf("tool requests=%d performed=%d", len(requests), performed)
	}
}

// A report the harness cannot read or cannot keep is named on the outcome and
// costs the run nothing. A run that failed because an agent mentioned a risk
// would teach every agent to stop mentioning them.
func TestAReportThatCannotBeCollectedNeverFailsTheRun(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "implemented the work item\n\n" +
		reportBlock(`{"severity":"note","message":"worth knowing"}`)
	collector := &fakeReports{err: errors.New("the report log is read-only")}
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Reports = collector

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("a lost report changed what the run did: %#v", outcome)
	}
	if len(outcome.Reports) != 0 || !strings.Contains(outcome.ReportProblem, "read-only") {
		t.Fatalf("outcome report evidence = %#v, %q", outcome.Reports, outcome.ReportProblem)
	}
	// And on the run's record, in the same words. The outcome is printed once by
	// the process that made it; a report lost only there was, afterwards, a report
	// nobody ever filed.
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ReportProblem != outcome.ReportProblem {
		t.Fatalf("the record says %q of the lost report, the outcome %q", recorded.ReportProblem, outcome.ReportProblem)
	}
}

func TestTheDeveloperContractSaysWhatMeritsAReport(t *testing.T) {
	t.Parallel()

	// Guidance on what merits a report belongs in the contract, where a persona
	// cannot weaken it, and it is repeated on a repair attempt for the same
	// reason the rest of the contract is.
	for name, prompt := range map[string]string{
		"first attempt": developerPrompt("", "", "# Assigned work item\n", "/scratch", nil),
		"check repair":  checkRepairPrompt("", "/scratch", nil, runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}, 1, 2),
	} {
		for _, required := range []string{report.Fence, "A report is not a blocker", "Most replies carry no report at all."} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("%s prompt is missing %q", name, required)
			}
		}
	}
}

// reportBlock renders what an agent writes when it reports, the way the
// contract asks for it: after everything else it says.
func reportBlock(entries ...string) string {
	return report.Fence + "\n{\"reports\":[" + strings.Join(entries, ",") + "]}\n```\n"
}

// fakeReports is the collected pile without a filesystem. err refuses every
// append, which is how a run that cannot keep a report is tested.
type fakeReports struct {
	appended []report.Report
	err      error
}

func (f *fakeReports) Append(reported report.Report) error {
	if f.err != nil {
		return f.err
	}
	if err := reported.Validate(); err != nil {
		return err
	}
	f.appended = append(f.appended, reported)
	return nil
}
