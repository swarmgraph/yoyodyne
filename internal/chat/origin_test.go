package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// admitOne opens a product manager's conversation that admits one item with the
// given create action, as a recurring pass where pass is named, and returns
// what the tracker was asked to create.
func admitOne(t *testing.T, pass, action string, configure func(*Options)) []createdOrigin {
	t.Helper()
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.", action)},
		{SessionID: "session-1", FinalText: "It is in the backlog."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	if configure != nil {
		configure(&options)
	}
	session := openTestSession(t, options)
	session.ForPass(pass)
	reply, err := session.Send(context.Background(), "Go on.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation applied", reply.Actions)
	}
	origins := make([]createdOrigin, 0, len(tracker.created))
	for _, created := range tracker.created {
		origins = append(origins, createdOrigin{origin: created.Origin, notes: created.Notes})
	}
	return origins
}

type createdOrigin struct {
	origin domain.WorkItemOrigin
	notes  string
}

const createAction = `{"action":"create","title":"The export is refused in a run's diff","description":"Refuse it.","goal":"` + theGoal + `","reason":"it needs doing"`

// Work admitted in a conversation the operator is speaking in records that the
// operator asked for it, and the Lead Product Manager admitted it.
func TestAnAdmissionInTheOperatorsConversationRecordsTheOperatorAsAsking(t *testing.T) {
	t.Parallel()

	created := admitOne(t, "", createAction+`}`, nil)
	want := domain.WorkItemOrigin{Asker: domain.AskerOperator, AdmittedBy: domain.RoleProductManager}
	if len(created) != 1 || created[0].origin != want {
		t.Fatalf("origins = %#v, want %#v", created, want)
	}
	if created[0].origin.AskedBy() != "operator" || created[0].origin.OnBehalfOf() != "operator" {
		t.Fatalf("asked by %q on behalf of %q, want the operator both times", created[0].origin.AskedBy(), created[0].origin.OnBehalfOf())
	}
}

// Work admitted on a role's own recurring pass, with nobody speaking, records
// the pass as what asked for it — which is the Lead Product Manager's own sweep
// rather than the operator.
func TestAnAdmissionOnTheLeadProductManagersSweepRecordsTheSweepAsAsking(t *testing.T) {
	t.Parallel()

	created := admitOne(t, "product-manager-sweep", createAction+`}`, nil)
	want := domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager}
	if len(created) != 1 || created[0].origin != want {
		t.Fatalf("origins = %#v, want %#v", created, want)
	}
	if got := created[0].origin.AskedBy(); got != string(domain.RoleProductManager) {
		t.Fatalf("asked by %q, want the product manager's own pass", got)
	}
}

// Work admitted from a role's report records the report and the role that filed
// it as who asked, whichever session admitted it: the report is the record the
// work answers.
func TestAnAdmissionCitingAReportRecordsTheReportAndItsReporter(t *testing.T) {
	t.Parallel()

	const filed = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	withReport := func(options *Options) {
		options.Reports = &fakeReports{appended: []report.Report{filedReport(filed, "the skip-worktree bit can be flipped")}}
	}
	for _, pass := range []string{"", "product-manager-sweep"} {
		created := admitOne(t, pass, createAction+`,"report":"`+filed+`"}`, withReport)
		want := domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager, Report: filed, ReportedBy: domain.RoleDeveloper}
		if len(created) != 1 || created[0].origin != want {
			t.Fatalf("pass %q: origins = %#v, want %#v", pass, created, want)
		}
		if got := created[0].origin.AskedBy(); got != string(domain.RoleDeveloper) {
			t.Fatalf("pass %q: asked by %q, want the developer who filed the report", pass, got)
		}
	}
}

// An admission answering one of the operator's directives records the
// directive, and the work is on the operator's behalf whoever asked for it in
// the moment.
func TestAnAdmissionAnsweringADirectiveIsOnTheOperatorsBehalf(t *testing.T) {
	t.Parallel()

	const filed = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	directives := &fakeDirectives{}
	asked, err := directives.Record(context.Background(), DirectiveRequest{Kind: directive.KindOperational, Text: "Refuse the export in a run's diff."})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	created := admitOne(t, "product-manager-sweep", createAction+`,"directive":"`+asked.ID+`","report":"`+filed+`"}`, func(options *Options) {
		options.Directives = directives
		options.Reports = &fakeReports{appended: []report.Report{filedReport(filed, "the skip-worktree bit can be flipped")}}
	})
	want := domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager, Report: filed, ReportedBy: domain.RoleDeveloper, Directive: asked.ID}
	if len(created) != 1 || created[0].origin != want {
		t.Fatalf("origins = %#v, want %#v", created, want)
	}
	if got := created[0].origin.OnBehalfOf(); got != "operator" {
		t.Fatalf("on behalf of %q, want the operator whose directive it answers", got)
	}
	if got := created[0].origin.AskedBy(); got != string(domain.RoleDeveloper) {
		t.Fatalf("asked by %q, want the developer who filed the report", got)
	}
	// The backfill reads the same origin back out of the notes this admission
	// wrote, so an item backfilled and one admitted since say the same thing.
	if backfilled, stated := OriginFromNotes(created[0].notes); !stated || backfilled != want {
		t.Fatalf("OriginFromNotes() = %#v, %v; want %#v", backfilled, stated, want)
	}

	// A directive with no report is the operator asking in their own words, so a
	// sweep that admits work answering one records the operator as asking.
	answering := admitOne(t, "product-manager-sweep", createAction+`,"directive":"`+asked.ID+`"}`, func(options *Options) {
		options.Directives = directives
	})
	want = domain.WorkItemOrigin{Asker: domain.AskerOperator, AdmittedBy: domain.RoleProductManager, Directive: asked.ID}
	if len(answering) != 1 || answering[0].origin != want {
		t.Fatalf("origins = %#v, want %#v", answering, want)
	}
	if backfilled, stated := OriginFromNotes(answering[0].notes); !stated || backfilled != want {
		t.Fatalf("OriginFromNotes() = %#v, %v; want %#v", backfilled, stated, want)
	}
}

// A decomposition carves up work already admitted, so it records no origin of
// its own: the item it was carved from carries where the work came from.
func TestADecompositionRecordsNoOrigin(t *testing.T) {
	t.Parallel()

	created := admitOne(t, "", `{"action":"create","title":"Triage docket","description":"Stopped work reaches the manager.","goal":"`+theGoal+`","parent":"yoyodyne-ifd.102","reason":"nothing routes stopped work"}`,
		func(options *Options) {
			options.Role = domain.RoleDevelopmentManager
			options.Agent = string(domain.RoleDevelopmentManager)
		})
	if len(created) != 1 || created[0].origin.Known() {
		t.Fatalf("origins = %#v, want a decomposition to record none", created)
	}
}

// A proposal admitted without asking records who asked for it in the same write
// as the admission: the operator in their own conversation, and the role's pass
// on a sweep.
func TestAnAdmittedProposalRecordsItsOrigin(t *testing.T) {
	t.Parallel()

	for pass, asker := range map[string]domain.WorkItemAsker{"": domain.AskerOperator, "product-manager-sweep": domain.AskerSweep} {
		tracker := &fakeTracker{}
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1",
			FinalText: proposalReply("One follows.", proposeRecordedGoal("Resolve a work item's goal")),
		}}})
		options.Tracker = tracker
		options.Goals = recordedGoals(recordedGoal)
		session := openTestSession(t, options)
		session.ForPass(pass)
		reply, err := session.Send(context.Background(), "what follows from that")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Admitted) != 1 || len(tracker.created) != 1 {
			t.Fatalf("pass %q: admitted = %#v, created = %#v", pass, reply.Admitted, tracker.created)
		}
		want := domain.WorkItemOrigin{Asker: asker, AdmittedBy: domain.RoleProductManager}
		if got := tracker.created[0].Origin; got != want {
			t.Fatalf("pass %q: origin = %#v, want %#v", pass, got, want)
		}
	}
}

// A proposal the operator approves records who asked for it when it was made,
// which survives the proposal being put back on the table by a later process;
// one recorded before origins were records none rather than a guessed one.
func TestAnApprovedProposalRecordsTheOriginItWasMadeWith(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("One follows.", proposeRecordedGoal("Resolve a work item's goal")),
	}}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalHuman}
	session := openTestSession(t, options)
	session.ForPass("product-manager-sweep")
	reply, err := session.Send(context.Background(), "what follows from that")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v, want the work put to the operator", reply.Proposals)
	}
	restored := restoredProposal(reply.Proposals[0].ConversationID, reply.Proposals[0].recorded())
	if restored.Asker != domain.AskerSweep {
		t.Fatalf("restored asker = %q, want the sweep the proposal was made on", restored.Asker)
	}
	if _, err := session.Approve(context.Background(), reply.Proposals[0].ID); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	want := domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager}
	if len(tracker.created) != 1 || tracker.created[0].Origin != want {
		t.Fatalf("created = %#v, want origin %#v", tracker.created, want)
	}

	unrecorded := restored
	unrecorded.Asker = ""
	if origin := unrecorded.origin(domain.RoleProductManager); origin.Known() {
		t.Fatalf("origin of a proposal recorded before origins = %#v, want none", origin)
	}
}

// A read of an item says where it came from, and says an unrecorded origin is
// unknown; the queue listing names who asked only where it is known.
func TestAReadAndTheListingShowTheOrigin(t *testing.T) {
	t.Parallel()

	known := domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager, Report: "report-1", ReportedBy: domain.RoleDevelopmentManager, Directive: "directive-1"}
	read := renderWorkItemHead(beads.WorkItem{ID: "yoyodyne-a", Title: "Known", Origin: known}, recordedGoals(theGoal), true)
	if want := "origin: asked for by report report-1, filed by the development manager, admitted by the Lead Product Manager, on the operator's behalf in answer to directive directive-1\n"; !strings.Contains(read, want) {
		t.Fatalf("read = %q, want %q", read, want)
	}
	unknown := renderWorkItemHead(beads.WorkItem{ID: "yoyodyne-b", Title: "Unknown"}, recordedGoals(theGoal), true)
	if want := "origin: not recorded; the item was admitted before origins were recorded\n"; !strings.Contains(unknown, want) {
		t.Fatalf("read = %q, want %q", unknown, want)
	}
	if got := originLabel(known); got != ", asked by development-manager for operator" {
		t.Fatalf("listing label = %q", got)
	}
	if got := originLabel(domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager}); got != ", asked by product-manager" {
		t.Fatalf("listing label = %q", got)
	}
	if got := originLabel(domain.WorkItemOrigin{}); got != "" {
		t.Fatalf("listing label for an unknown origin = %q, want nothing", got)
	}
}

// The backfill reads an origin out of an item's notes only where the notes say
// it exactly, in the words an admission writes, and guesses nothing otherwise.
func TestOriginFromNotesGuessesNothing(t *testing.T) {
	t.Parallel()

	admitting := &Session{state: runstate.Conversation{ConversationID: "chat-1", Role: domain.RoleProductManager, Turns: 7}}
	admitted := admitting.trackerProvenance("Admitted to the backlog", "it needs doing") + "\n\nGoal served: " + theGoal
	inLane := admitting.trackerProvenance("Admitted to the backlog in lane factory-flow", "")
	harnessReport := filedReport("report-2", "a pass keeps failing")
	harnessReport.Role = report.HarnessReporter
	answered := directive.Directive{ID: "directive-9", ReceivedBy: domain.RoleProductManager, Text: "Do the thing."}

	for _, test := range []struct {
		name   string
		notes  string
		want   domain.WorkItemOrigin
		stated bool
	}{
		{"a report", admitted + reportNote(filedReport("report-1", "it broke")),
			domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager, Report: "report-1", ReportedBy: domain.RoleDeveloper}, true},
		{"a report the harness filed", inLane + reportNote(harnessReport),
			domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager, Report: "report-2", ReportedBy: report.HarnessReporter}, true},
		{"a directive", admitted + directiveNote(answered),
			domain.WorkItemOrigin{Asker: domain.AskerOperator, AdmittedBy: domain.RoleProductManager, Directive: "directive-9"}, true},
		{"a report and a directive", admitted + directiveNote(answered) + reportNote(filedReport("report-1", "it broke")),
			domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager, Report: "report-1", ReportedBy: domain.RoleDeveloper, Directive: "directive-9"}, true},
		// Notes never said whether the operator or a sweep asked.
		{"neither", admitted, domain.WorkItemOrigin{}, false},
		// Nothing names who admitted it.
		{"no admitter", "Filed by hand." + reportNote(filedReport("report-1", "it broke")), domain.WorkItemOrigin{}, false},
		// Two different reports is not one origin.
		{"two reports", admitted + reportNote(filedReport("report-1", "it broke")) + reportNote(filedReport("report-3", "so did this")), domain.WorkItemOrigin{}, false},
		// A title no role carries is not a role.
		{"an unknown admitter", "Admitted to the backlog by the night shift in conversation chat-1, after turn 2." + directiveNote(answered), domain.WorkItemOrigin{}, false},
	} {
		got, stated := OriginFromNotes(test.notes)
		if stated != test.stated || got != test.want {
			t.Errorf("%s: OriginFromNotes() = %#v, %v; want %#v, %v", test.name, got, stated, test.want, test.stated)
		}
	}
}
