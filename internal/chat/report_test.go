package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

func TestTheProductManagerReportsWithoutItChangingTheTurn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	reports := &fakeReports{}
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: reportReply(
			trackerReply(
				"I closed the duplicate, and there is something you should know.",
				`{"action":"close","id":"yoyodyne-2","reason":"yoyodyne-1 already covers it"}`,
			),
			`{"severity":"warning","message":"the goals still name a milestone the brief dropped."}`,
		)},
		{SessionID: "session-1", FinalText: "That is everything."},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	options.Reports = reports
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// The report changed nothing about the turn: the action it accompanied still
	// ran, and the answer is still the answer.
	if len(tracker.closed) != 1 || tracker.closed[0][0] != "yoyodyne-2" {
		t.Fatalf("closed = %#v, want the action to have run anyway", tracker.closed)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if strings.Contains(reply.Text, "yoyodyne-report") || strings.Contains(reply.Text, "severity") {
		t.Fatalf("the block reached the operator as prose: %q", reply.Text)
	}
	if reply.ReportProblem != "" {
		t.Fatalf("a readable report was reported as a problem: %q", reply.ReportProblem)
	}

	// It is collected, durably and with the attribution the harness knows rather
	// than anything the product manager asserted.
	if len(reports.appended) != 1 || len(reply.Reports) != 1 {
		t.Fatalf("collected = %#v, reported = %#v", reports.appended, reply.Reports)
	}
	collected := reports.appended[0]
	if collected.Role != "product-manager" || collected.RunID != session.Evidence().ConversationID {
		t.Fatalf("attribution = %#v", collected)
	}
	if collected.Severity != report.SeverityWarning || !strings.Contains(collected.Message, "milestone the brief dropped") {
		t.Fatalf("collected report = %#v", collected)
	}
	// A conversation has no assigned work item, and says so by naming none
	// rather than by naming something it was not working on.
	if collected.WorkItemID != "" {
		t.Fatalf("a conversation's report claimed work item %q", collected.WorkItemID)
	}
	if payload := onlyEventPayload(t, root, session, execution.EventReportRecorded); !strings.Contains(payload, collected.ID) {
		t.Fatalf("the conversation's log does not record the report: %s", payload)
	}
}

func TestAnUnreadableReportBlockCostsTheTurnNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	reports := &fakeReports{}
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: reportReply(
			trackerReply(
				"Closing the duplicate.",
				`{"action":"close","id":"yoyodyne-2","reason":"yoyodyne-1 already covers it"}`,
			),
			`{"severity":"blocker","message":"a report is not a blocker, so this vocabulary is refused"}`,
		)},
		{SessionID: "session-1", FinalText: "Done."},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	options.Reports = reports
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	// A report that could not be read is not a failed turn and not a refused
	// action: the work the turn asked for still happened, and what is lost is
	// only what the report was trying to say.
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.closed) != 1 {
		t.Fatalf("closed = %#v, want the action to have run anyway", tracker.closed)
	}
	if len(reports.appended) != 0 || len(reply.Reports) != 0 {
		t.Fatalf("an unreadable block was collected: %#v", reports.appended)
	}
	if !strings.Contains(reply.ReportProblem, "cannot read") {
		t.Fatalf("report problem = %q", reply.ReportProblem)
	}
	if strings.Contains(reply.Text, "yoyodyne-report") {
		t.Fatalf("the broken block reached the operator as prose: %q", reply.Text)
	}
	if payload := onlyEventPayload(t, root, session, execution.EventReportUnreadable); !strings.Contains(payload, "cannot read") {
		t.Fatalf("the conversation's log does not record the lost report: %s", payload)
	}
}

func TestAReportThatCannotBeCollectedIsSaidRatherThanSwallowed(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{err: errors.New("the report log is read-only")}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: reportReply("Noted.", `{"severity":"note","message":"worth knowing"}`)},
	}})
	options.Reports = reports
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Anything else?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Reports) != 0 || !strings.Contains(reply.ReportProblem, "read-only") {
		t.Fatalf("reply = %#v, %q", reply.Reports, reply.ReportProblem)
	}

	// A conversation with nowhere to collect says that rather than appearing to
	// have kept the report.
	bare := openTestSession(t, testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-2", FinalText: reportReply("Noted.", `{"severity":"note","message":"worth knowing"}`)},
	}}))
	reply, err = bare.Send(context.Background(), "Anything else?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(reply.ReportProblem, "no report collection is wired") {
		t.Fatalf("report problem = %q", reply.ReportProblem)
	}
}

func TestReportsCommandShowsWhatEveryRoleReported(t *testing.T) {
	t.Parallel()

	// The operator reads the whole pile from the conversation they are already
	// in, including what a developer reported in a run that is long over.
	reports := &fakeReports{}
	fromDeveloper := report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          "developer",
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.19",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityCritical,
		Message:       "bd lint could not run in its sandbox, so nothing linted the item",
		RecordedAt:    fixedClock{}.Now(),
	}
	if err := reports.Append(fromDeveloper); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	options := testOptions(t, &fakeBackend{})
	options.Reports = reports
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader("/reports\n/exit\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	for _, required := range []string{
		"1 of 1 collected report(s) are unhandled",
		"critical",
		"from the developer on title unavailable (yoyodyne-ifd.19)",
		"bd lint could not run",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	if !strings.Contains(commandHelp, "/reports") {
		t.Fatal("the command list does not offer /reports")
	}
}

func TestReportsCommandSaysWhenNobodyHasReportedAnything(t *testing.T) {
	t.Parallel()

	// "Nothing has been reported" is an answer; a blank space is not.
	options := testOptions(t, &fakeBackend{})
	options.Reports = &fakeReports{}
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("/reports\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if !strings.Contains(out.String(), "reports: nothing has been reported.") {
		t.Fatalf("transcript = %q", out.String())
	}
}

func TestAFinishedRunSaysItReportedSomething(t *testing.T) {
	t.Parallel()

	// A run finishing is when the operator finds out there is something new in
	// the pile; the count is named there rather than left for them to go looking.
	rendered := RunReport{
		WorkItemID:    "yoyodyne-ifd.19",
		Status:        "succeeded",
		Reported:      2,
		ReportedWorst: report.SeverityCritical,
	}.Render(console.Theme{})
	if !strings.Contains(rendered, "reported 2 thing(s) without stopping") || !strings.Contains(rendered, "/reports") {
		t.Fatalf("run report = %q", rendered)
	}
	// A count alone reads the same for two notes and for something already
	// costing somebody, so the worst of them is named and marked. Neither says
	// anything in colour: this is the theme a redirected stream gets.
	if !strings.Contains(rendered, "!! reported") || !strings.Contains(rendered, "the worst of them critical") {
		t.Fatalf("a critical report was not marked out of the run's closing lines: %q", rendered)
	}
	// A note is real news and asks for nothing, so it is counted and left alone.
	quietWorst := RunReport{WorkItemID: "yoyodyne-ifd.19", Status: "succeeded", Reported: 1, ReportedWorst: report.SeverityNote}.Render(console.Theme{})
	if strings.Contains(quietWorst, "!") {
		t.Fatalf("a note was marked as though it wanted attention: %q", quietWorst)
	}
	// A record written before the worst was carried says how many rather than
	// claiming a severity it does not have.
	older := RunReport{WorkItemID: "yoyodyne-ifd.19", Status: "succeeded", Reported: 2}.Render(console.Theme{})
	if !strings.Contains(older, "reported 2 thing(s) without stopping;") {
		t.Fatalf("an older run report = %q", older)
	}
	// A run that reported nothing says nothing about reports at all.
	if quiet := (RunReport{WorkItemID: "yoyodyne-ifd.19", Status: "succeeded"}).Render(console.Theme{}); strings.Contains(quiet, "reported") {
		t.Fatalf("a quiet run mentioned reports: %q", quiet)
	}
}

func TestTheContractTellsTheProductManagerHowAndWhenToReport(t *testing.T) {
	t.Parallel()

	// Guidance on what merits a report belongs in the contract, where a persona
	// cannot weaken it.
	prompt := SystemPrompt(domain.RoleProductManager, Admission{}, nil, hostilePersona)
	for _, required := range []string{
		report.Fence,
		"A report is not a blocker",
		"Most replies carry no report at all.",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the product-manager contract is missing %q", required)
		}
	}
}

// reportReply renders a provider answer that carries reports the way the
// contract asks for them: after everything else it says.
func reportReply(answer string, entries ...string) string {
	return strings.TrimRight(answer, "\n") + "\n\n" + report.Fence + "\n{\"reports\":[" + strings.Join(entries, ",") + "]}\n```\n"
}

// fakeReports is the collected pile without a filesystem. err refuses every
// append, which is how a conversation that cannot keep a report is tested, and
// handlingErr refuses only what became of them, which is how a listing that can
// read the pile but not its dispositions is tested.
type fakeReports struct {
	appended    []report.Report
	handled     []report.Handling
	err         error
	handlingErr error
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

func (f *fakeReports) List() ([]report.Report, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.appended, nil
}

func (f *fakeReports) Handle(handling report.Handling) error {
	if f.handlingErr != nil {
		return f.handlingErr
	}
	if err := handling.Validate(); err != nil {
		return err
	}
	f.handled = append(f.handled, handling)
	return nil
}

func (f *fakeReports) Handlings() ([]report.Handling, error) {
	if f.handlingErr != nil {
		return nil, f.handlingErr
	}
	return f.handled, nil
}
