package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestAReportReadReturnsTheWholeMessageInTheSameTurnAndRecordsTheRead(t *testing.T) {
	t.Parallel()

	for _, role := range domain.Roles() {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			pile, err := runstate.NewReportStore(root, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			const id = "report-00000000000000000000000000000001"
			// Rendering each line with indentation would exceed an item read's
			// budget. The message itself fits the report store's bound.
			message := strings.Repeat("é\n", (report.MaxMessageBytes-30)/3) + "The last request must be read."
			reported := collectedReport(id, report.SeverityWarning, message, 1)
			if err := pile.Append(reported); err != nil {
				t.Fatal(err)
			}
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Reading the report.", `{"action":"read","report":"`+id+`"}`)},
				{SessionID: "session-1", FinalText: "I have read the whole request."},
			}}
			options := testOptions(t, provider)
			options.Role, options.Agent = role, string(role)
			options.Store = newTestStore(t, root)
			options.Reports = pile
			// No tracker is needed to read the report store.
			session := openTestSession(t, options)
			reply, err := session.Send(context.Background(), "Read the report before deciding.")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			outcome := reply.Actions[0]
			if outcome.WorkItemID != "" || !strings.Contains(outcome.Detail, message) || len(outcome.Detail) > maxTrackerItemBytes {
				t.Fatalf("the report was not read whole within the result bound: %#v", outcome)
			}
			if len(provider.requests) != 2 || !strings.Contains(provider.requests[1].Prompt, message) ||
				!strings.Contains(provider.requests[1].Prompt, "Report "+id+" as the report store holds it") {
				t.Fatal("the whole report was not delivered before the same turn finished")
			}
			if !strings.Contains(provider.requests[0].SystemPrompt, reportReadClause) {
				t.Fatal("the role was not told how to read a report")
			}
			if role == domain.RoleProductManager && !strings.Contains(provider.requests[0].Prompt, message) {
				t.Fatal("a valid long report was too large to be offered in the undecided list")
			}
			for _, event := range []execution.EventType{execution.EventTrackerActionRequested, execution.EventTrackerActionApplied} {
				if payload := onlyEventPayload(t, root, session, event); !strings.Contains(payload, id) {
					t.Fatalf("%s did not record which report was read: %s", event, payload)
				}
			}
			handlings, err := pile.Handlings()
			if err != nil || len(handlings) != 0 {
				t.Fatalf("a read changed what became of the report: %#v, %v", handlings, err)
			}
		})
	}
}

func TestAReportCitedInsideAnotherReportCanBeReadEvenWhenItWasHandled(t *testing.T) {
	t.Parallel()
	const id = "report-00000000000000000000000000000001"
	cited := collectedReport(id, report.SeverityWarning, "The original report's full request.", 1)
	pile := &fakeReports{appended: []report.Report{
		cited,
		collectedReport("report-00000000000000000000000000000002", report.SeverityNote, "The missed pass concerns "+id+".", 2),
	}, handled: []report.Handling{{ReportID: id}}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Reading the cited report.", `{"action":"read","report":"`+id+`"}`)},
		{SessionID: "session-1", FinalText: "The citation is now clear."},
	}}
	options := testOptions(t, provider)
	options.Reports = pile
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "Decide about the missed pass."); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(provider.requests[0].Prompt, cited.Message) {
		t.Fatal("the handled report was already shown; the test did not need to follow the citation")
	}
	if !strings.Contains(provider.requests[0].Prompt, "The missed pass concerns "+id) ||
		!strings.Contains(provider.requests[1].Prompt, cited.Message) {
		t.Fatal("the role could not read the report cited by the one it was shown")
	}
}

func TestAReportReadRefusesAnIdentifierTheStoreDoesNotHold(t *testing.T) {
	t.Parallel()
	const id = "report-00000000000000000000000000000099"
	root := t.TempDir()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Reading.", `{"action":"read","report":"`+id+`"}`)},
		{SessionID: "session-1", FinalText: "That report is absent."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Reports = &fakeReports{}
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Read the report.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "no report in the pile is "+id) {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(provider.requests[1].Prompt, "no report in the pile is "+id) {
		t.Fatal("the refusal was not handed back to the role")
	}
	if payload := onlyEventPayload(t, root, session, execution.EventTrackerActionFailed); !strings.Contains(payload, id) {
		t.Fatalf("the refusal was not recorded: %s", payload)
	}
}

func TestAReportReadRefusesAmbiguousOrMalformedSubjects(t *testing.T) {
	t.Parallel()
	for _, action := range []TrackerAction{
		{Action: actionRead, ID: "yoyodyne-ifd.19", Report: "report-00000000000000000000000000000001"},
		{Action: actionRead, Report: "report-missing"},
		{Action: actionRead, Report: "report-00000000000000000000000000000001", Reason: "unneeded"},
	} {
		if err := action.Validate(); err == nil {
			t.Fatalf("Validate(%#v) accepted a malformed read", action)
		}
	}
}

func TestAReportReadKeepsTheMessageWhenItsAttributionMustBeCut(t *testing.T) {
	t.Parallel()
	reported := collectedReport("report-00000000000000000000000000000001", report.SeverityNote, strings.Repeat("x", report.MaxMessageBytes), 1)
	reported.Agent = strings.Repeat("a", maxTrackerItemBytes)
	options := testOptions(t, &fakeBackend{})
	options.Reports = &fakeReports{appended: []report.Report{reported}}
	session := openTestSession(t, options)
	outcome := TrackerOutcome{Action: TrackerAction{Action: actionRead, Report: reported.ID}}
	session.readReport(&outcome)
	if !outcome.Applied || len(outcome.Detail) > maxTrackerItemBytes || !strings.Contains(outcome.Detail, reported.Message) ||
		!strings.Contains(outcome.Detail, "[cut at") || !strings.Contains(outcome.Detail, fmt.Sprintf("Message (%d bytes)", report.MaxMessageBytes)) {
		t.Fatalf("read did not keep the message and declare the attribution cut: %#v", outcome)
	}
}
