package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// writeRepositoryFile puts one file in a test conversation's repository, so a
// step naming it names a file that exists.
func writeRepositoryFile(t *testing.T, root, name string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A handling naming the operator is refused, and nothing is recorded, where the
// step it asks of him cannot be taken or is not his: a file it names that does
// not exist, a file a developer run may change, or a hold of his that no longer
// stands. The same handling naming a file that is there and beyond every grant,
// or a hold that stands, is recorded.
func TestAHandlingNamingTheOperatorIsRefusedWhereItsStepDoesNotHold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		handle  string
		intake  *fakeIntake
		refusal string
	}{
		{name: "a folder that does not exist",
			handle:  `"operator_reason":"protected-file","reason":"edit the role-definition folder .yoyodyne/roles/developer/ to grant the path"`,
			refusal: ".yoyodyne/roles/developer/ does not exist in the repository"},
		{name: "a file a developer run may change",
			handle:  `"operator_reason":"protected-file","reason":"edit internal/config/grants.go by hand to grant the path"`,
			refusal: "internal/config/grants.go is a file a developer run may change"},
		{name: "a hold that is gone",
			handle:  `"operator_reason":"own-hold","reason":"lift your hold on new work"`,
			intake:  &fakeIntake{},
			refusal: "none stands now"},
		{name: "a settings file that is there",
			handle: `"operator_reason":"protected-file","reason":"add the PreToolUse hook to .claude/settings.json by hand"`},
		{name: "a hold that stands",
			handle: `"operator_reason":"own-hold","reason":"lift your hold on new work"`,
			intake: &fakeIntake{held: true, heldBy: runstate.IntakeHolderOperator}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reports := &fakeReports{}
			seedReports(t, reports, collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "only the operator can do this", 2))
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Recording it.",
					`{"action":"handle","report":"report-00000000000000000000000000000002","needs":"operator",`+tc.handle+`}`)},
				{SessionID: "session-1", FinalText: "Recorded."},
				{SessionID: "session-1", FinalText: "Nothing more."},
			}}
			options := testOptions(t, provider)
			options.Reports, options.Tracker = reports, &fakeTracker{}
			if tc.intake != nil {
				options.Intake = tc.intake
			}
			writeRepositoryFile(t, options.Repository, ".claude/settings.json")
			writeRepositoryFile(t, options.Repository, "internal/config/grants.go")
			reply, err := openTestSession(t, options).Send(context.Background(), "deal with the report")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Actions) != 1 {
				t.Fatalf("actions = %#v, want one", reply.Actions)
			}
			if tc.refusal == "" {
				if !reply.Actions[0].Applied || len(reports.handled) != 1 {
					t.Fatalf("actions = %#v, handlings = %#v, want it recorded", reply.Actions, reports.handled)
				}
				return
			}
			if reply.Actions[0].Applied || len(reports.handled) != 0 {
				t.Fatalf("handlings = %#v, want nothing recorded", reports.handled)
			}
			if failure := reply.Actions[0].Failure; !strings.Contains(failure, tc.refusal) || !strings.Contains(failure, "was not recorded") {
				t.Fatalf("refusal = %q, want it to say %q", failure, tc.refusal)
			}
		})
	}
}

// A handling that has waited on the operator for more than three days is
// checked again when the role's turn is built: one whose file is not there is
// listed to the role as its own to finish, saying why, and not as standing with
// him.
func TestAHandlingWhoseStepFailsAfterThreeDaysComesBackToTheRole(t *testing.T) {
	t.Parallel()
	reports := &fakeReports{}
	seedReports(t, reports, collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "the grant needs widening", 2))
	reports.handled = []report.Handling{{
		SchemaVersion: report.HandlingSchemaVersion, ReportID: "report-00000000000000000000000000000002",
		Role: domain.RoleProductManager, RunID: "chat-old",
		Reason:     "edit the role-definition folder .yoyodyne/roles/developer/ to grant the path",
		RecordedAt: fixedClock{}.Now().Add(-4 * 24 * time.Hour), NeedsOperator: true, OperatorReason: ownership.OperatorProtectedFile,
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Noted."}}}
	options := testOptions(t, provider)
	options.Reports, options.Tracker = reports, &fakeTracker{}
	if _, err := openTestSession(t, options).Send(context.Background(), "anything waiting?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	prompt := provider.requests[len(provider.requests)-1].Prompt
	if !strings.Contains(prompt, "Reports you handled as the operator's that are yours to finish") ||
		!strings.Contains(prompt, ".yoyodyne/roles/developer/ does not exist in the repository") {
		t.Fatalf("the handling was not listed back to the role with why:\n%s", prompt)
	}
	if strings.Contains(prompt, "Reports you handled as needing the operator's hand") {
		t.Fatalf("the handling was still listed as standing with the operator:\n%s", prompt)
	}
}

// An escalation of a stopped run to the operator is checked the same way when
// the development manager records it: a file that does not exist, a file a
// developer run may change, a hold of his that is gone, or a restart the
// product does itself is refused, nothing is recorded, and the item is not
// blocked. A step he can take goes through.
func TestAnEscalationToTheOperatorIsRefusedWhereItsStepDoesNotHold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, escalation string
		intake           *fakeIntake
		refusal          string
	}{
		{name: "a folder that does not exist",
			escalation: `"operator_reason":"protected-file","reason":"the grant needs .yoyodyne/roles/developer/ changed by hand"`,
			refusal:    ".yoyodyne/roles/developer/ does not exist in the repository"},
		{name: "a file a developer run may change",
			escalation: `"operator_reason":"protected-file","reason":"the grant needs internal/config/grants.go changed by hand"`,
			refusal:    "internal/config/grants.go is a file a developer run may change"},
		{name: "a hold that is gone",
			escalation: `"operator_reason":"own-hold","reason":"the run waits on the operator lifting his hold on new work"`,
			intake:     &fakeIntake{},
			refusal:    "none stands now"},
		{name: "a restart the product does itself",
			escalation: `"operator_reason":"repository-setting","reason":"the scheduler has to be restarted onto the new build before the merge can go on"`,
			refusal:    "operating rule 6de828a8"},
		{name: "a settings file that is there",
			escalation: `"operator_reason":"protected-file","reason":"the run needs the hook added to .claude/settings.json by hand"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			answer := reportReply(
				trackerReply("Escalating.",
					`{"action":"triage","id":"yoyodyne-ifd.1.9","run":"`+stoppedRun+`","decision":"escalate","to":"operator",`+tc.escalation+`}`),
				`{"severity":"warning","message":"yoyodyne-ifd.1.9 needs the operator."}`)
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.1.9": {ID: "yoyodyne-ifd.1.9", Title: "the item", Status: "open"},
			}}
			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			options := triageOptions(t, tracker, budgets, answer)
			options.Reports = &fakeReports{}
			if tc.intake != nil {
				options.Intake = tc.intake
			}
			writeRepositoryFile(t, options.Repository, ".claude/settings.json")
			writeRepositoryFile(t, options.Repository, "internal/config/grants.go")
			reply := triageSend(t, options)
			if len(reply.Actions) != 1 {
				t.Fatalf("actions = %#v, want one", reply.Actions)
			}
			if tc.refusal == "" {
				if !reply.Actions[0].Applied || len(tracker.blocked) != 1 {
					t.Fatalf("actions = %#v, blocked = %#v, want it recorded", reply.Actions, tracker.blocked)
				}
				return
			}
			if reply.Actions[0].Applied || len(tracker.blocked) != 0 {
				t.Fatalf("actions = %#v, blocked = %#v, want nothing recorded", reply.Actions, tracker.blocked)
			}
			if _, decided := budgets.counters(t, "yoyodyne-ifd.1.9").DecisionOf(stoppedRun); decided {
				t.Fatal("the escalation was recorded as a decision")
			}
			if failure := reply.Actions[0].Failure; !strings.Contains(failure, tc.refusal) || !strings.Contains(failure, "was not recorded") {
				t.Fatalf("refusal = %q, want it to say %q", failure, tc.refusal)
			}
		})
	}
}

// A report handling asking the operator to restart part of the product is
// refused too, saying the product does that restart itself.
func TestAHandlingAskingTheOperatorForARestartIsRefused(t *testing.T) {
	t.Parallel()
	reports := &fakeReports{}
	seedReports(t, reports, collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "Slack is on an old build", 2))
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Recording it.",
			`{"action":"handle","report":"report-00000000000000000000000000000002","needs":"operator","operator_reason":"credential","credential":{"needs":"account","what":"the Slack login"},"reason":"log in again and restart Slack onto the new build"}`)},
		{SessionID: "session-1", FinalText: "Recorded."},
		{SessionID: "session-1", FinalText: "Nothing more."},
	}}
	options := testOptions(t, provider)
	options.Reports, options.Tracker = reports, &fakeTracker{}
	reply, err := openTestSession(t, options).Send(context.Background(), "deal with the report")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || len(reports.handled) != 0 ||
		!strings.Contains(reply.Actions[0].Failure, "the product does itself under operating rule 6de828a8") {
		t.Fatalf("actions = %#v, handlings = %#v, want the restart refused", reply.Actions, reports.handled)
	}
}

// fakeNamedEpics is the record of the epics the operator named.
type fakeNamedEpics struct {
	mode epicscope.Mode
	fail error
}

func (f fakeNamedEpics) Named() (epicscope.Record, error) {
	return epicscope.Record{Mode: f.mode}, f.fail
}

// epicModeReadings is epic mode read each way, with whether an own-hold request
// is let stand by it and what is said of it.
var epicModeReadings = []struct {
	name   string
	epics  fakeNamedEpics
	stands bool
	says   string
}{
	{"on", fakeNamedEpics{mode: epicscope.ModeEpic}, true, ""},
	{"off", fakeNamedEpics{mode: epicscope.ModeGoal}, false, "none stands now"},
	{"unreadable", fakeNamedEpics{fail: errors.New("epic-scope.json is cut short")}, true, "whether epic mode is on could not be read: epic-scope.json is cut short"},
}

// Epic mode is a hold of the operator's while it is on: a handling asking him
// to add work to a named epic is recorded then, refused once it is off and
// nothing else of his stands, the brake's hold included, and recorded saying so
// where epic mode cannot be read.
func TestAHandlingNamingEpicModeIsCheckedAgainstIt(t *testing.T) {
	t.Parallel()
	for _, tc := range epicModeReadings {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reports := &fakeReports{}
			seedReports(t, reports, collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "piece 7 needs its second part added", 2))
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Recording it.",
					`{"action":"handle","report":"report-00000000000000000000000000000002","needs":"operator","operator_reason":"own-hold","reason":"add yoyodyne-9k1y.17 with yoyo epic add"}`)},
				{SessionID: "session-1", FinalText: "Recorded."},
				{SessionID: "session-1", FinalText: "Nothing more."},
			}}
			options := testOptions(t, provider)
			options.Reports, options.Tracker, options.Epics = reports, &fakeTracker{}, tc.epics
			options.Intake = &fakeIntake{held: true, heldBy: runstate.IntakeHolderBrake}
			reply, err := openTestSession(t, options).Send(context.Background(), "deal with the report")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Actions) != 1 || reply.Actions[0].Applied != tc.stands || (len(reports.handled) == 1) != tc.stands {
				t.Fatalf("actions = %#v, handlings = %#v, want recorded %t", reply.Actions, reports.handled, tc.stands)
			}
			said := reply.Actions[0].Summary + reply.Actions[0].Failure
			if !strings.Contains(said, tc.says) || (tc.says == "" && strings.Contains(said, "could not be read")) {
				t.Fatalf("outcome = %q, want it to say %q", said, tc.says)
			}
		})
	}
}

// A handling already waiting on epic mode is listed as standing with the
// operator while it is on or cannot be read, saying the second, and comes back
// to the role once it is off.
func TestAHandlingWaitingOnEpicModeComesBackOnceItIsOff(t *testing.T) {
	t.Parallel()
	for _, tc := range epicModeReadings {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reports := &fakeReports{}
			seedReports(t, reports, collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "piece 7 needs its second part added", 2))
			reports.handled = []report.Handling{{
				SchemaVersion: report.HandlingSchemaVersion, ReportID: "report-00000000000000000000000000000002",
				Role: domain.RoleProductManager, RunID: "chat-old", Reason: "add yoyodyne-9k1y.17 with yoyo epic add",
				RecordedAt: fixedClock{}.Now().Add(-time.Hour), NeedsOperator: true, OperatorReason: ownership.OperatorOwnHold,
			}}
			provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Noted."}}}
			options := testOptions(t, provider)
			options.Reports, options.Tracker, options.Epics = reports, &fakeTracker{}, tc.epics
			if _, err := openTestSession(t, options).Send(context.Background(), "anything waiting?"); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			prompt := provider.requests[len(provider.requests)-1].Prompt
			if strings.Contains(prompt, "# Reports you handled as needing the operator's hand") != tc.stands ||
				strings.Contains(prompt, "# Reports you handled as the operator's that are yours to finish") == tc.stands ||
				!strings.Contains(prompt, tc.says) {
				t.Fatalf("want standing with the operator %t, saying %q:\n%s", tc.stands, tc.says, prompt)
			}
		})
	}
}

// An escalation of a stopped run naming epic mode is checked against it as a
// handling is.
func TestAnEscalationNamingEpicModeIsCheckedAgainstIt(t *testing.T) {
	t.Parallel()
	for _, tc := range epicModeReadings {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			answer := reportReply(
				trackerReply("Escalating.",
					`{"action":"triage","id":"yoyodyne-ifd.1.9","run":"`+stoppedRun+`","decision":"escalate","to":"operator","operator_reason":"own-hold","reason":"the run waits on the operator adding the item to his named epic"}`),
				`{"severity":"warning","message":"yoyodyne-ifd.1.9 needs the operator."}`)
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.1.9": {ID: "yoyodyne-ifd.1.9", Title: "the item", Status: "open"},
			}}
			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			options := triageOptions(t, tracker, budgets, answer)
			options.Reports, options.Epics = &fakeReports{}, tc.epics
			reply := triageSend(t, options)
			if len(reply.Actions) != 1 || reply.Actions[0].Applied != tc.stands || (len(tracker.blocked) == 1) != tc.stands {
				t.Fatalf("actions = %#v, blocked = %#v, want recorded %t", reply.Actions, tracker.blocked, tc.stands)
			}
			said := reply.Actions[0].Summary + reply.Actions[0].Failure
			if !strings.Contains(said, tc.says) || (tc.says == "" && strings.Contains(said, "could not be read")) {
				t.Fatalf("outcome = %q, want it to say %q", said, tc.says)
			}
		})
	}
}
