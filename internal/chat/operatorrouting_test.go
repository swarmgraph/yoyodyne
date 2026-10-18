package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The operator is named only for a reason on his closed list
// (`docs/designs/ownership-and-the-operator.md`, under "The escalation
// ladder"). These are the three ways a role names him — the development
// manager's escalation, a report handling, and an ask — each refused without a
// reason with the rung to use instead, and each still going through with one.

// An escalation that names the operator for no reason on the list is refused
// whole: nothing is recorded on the item's triage record, the item is not
// blocked, and what is handed back names the rungs above the development
// manager. Leaving "to" out names him, which is how every escalation read before
// the ladder; five escalations for the architect and the Lead Product Manager
// reached his line that way because the decision could name no role.
func TestAnEscalationNamingTheOperatorWithoutAClosedListReasonIsRefused(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		extra string
	}{
		{name: "no target names the operator", extra: ``},
		{name: "the operator with no reason", extra: `,"to":"operator"`},
		{name: "a reason not on the list", extra: `,"to":"operator","operator_reason":"approval"`},
		// The October 2 alert: internal scheduling recovery said to need a person.
		{name: "internal scheduling recovery", extra: `,"operator_reason":"scheduler-recovery"`},
		// An exchange awaiting an answer, or one closed after the architect
		// answered, is the asking role's to follow up and never his.
		{name: "an exchange awaiting follow-up", extra: `,"operator_reason":"exchange-closed"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			answer := reportReply(
				trackerReply("This one is upstream of the change.",
					`{"action":"triage","id":"yoyodyne-ifd.1.9","run":"`+stoppedRun+`","decision":"escalate"`+testCase.extra+`,"reason":"the findings dispute the acceptance criteria"}`),
				`{"severity":"warning","message":"yoyodyne-ifd.1.9 disputes its own criteria."}`)
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.1.9": {ID: "yoyodyne-ifd.1.9", Title: "the item that keeps coming back", Status: "open"},
			}}
			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			options := triageOptions(t, tracker, budgets, answer)
			options.Reports = &fakeReports{}
			reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")

			refusal := strings.Join(reply.HandedBack, "\n")
			if err != nil {
				refusal += "\n" + err.Error()
			}
			if !strings.Contains(refusal, ownership.Rung(domain.RoleDevelopmentManager)) {
				t.Fatalf("the refusal does not name the rung to use instead:\n%s", refusal)
			}
			for _, outcome := range reply.Actions {
				if outcome.Applied {
					t.Fatalf("a refused escalation was carried out: %#v", outcome)
				}
			}
			if len(tracker.blocked) != 0 || len(tracker.updates) != 0 {
				t.Fatalf("a refused escalation changed the item: blocked %#v, updates %#v", tracker.blocked, tracker.updates)
			}
			if _, decided := budgets.counters(t, "yoyodyne-ifd.1.9").DecisionOf(stoppedRun); decided {
				t.Fatal("a refused escalation was recorded on the item's triage record")
			}
		})
	}
}

// An escalation up the ladder goes through: to the Lead Product Manager on the
// item's intent and to the architect on its design, each blocking the item for
// that role and recorded as that role's, so the read model does not list it as
// the operator's. One to the operator for a reason on the list goes through too,
// carrying the reason.
func TestAnEscalationUpTheLadderOrForAClosedListReasonGoesThrough(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name, extra, report string
		to                  domain.AgentRole
		operatorReason      ownership.OperatorReason
		says                string
	}{
		{name: "the Lead Product Manager on intent", extra: `,"to":"product-manager"`, report: `,"for":"product-manager"`,
			to: domain.RoleProductManager, says: "Escalated to the Lead Product Manager by triage"},
		{name: "the architect on design", extra: `,"to":"architect"`, report: `,"for":"architect"`,
			to: domain.RoleArchitect, says: "Escalated to the architect by triage"},
		{name: "the operator for a repository setting", extra: `,"to":"operator","operator_reason":"repository-setting"`,
			operatorReason: ownership.OperatorRepositorySetting, says: "Escalated to the operator by triage, for a repository setting"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			answer := reportReply(
				trackerReply("Escalating.",
					`{"action":"triage","id":"yoyodyne-ifd.1.9","run":"`+stoppedRun+`","decision":"escalate"`+testCase.extra+`,"reason":"it cannot go on as it stands"}`),
				`{"severity":"warning"`+testCase.report+`,"message":"yoyodyne-ifd.1.9 cannot go on as it stands."}`)
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.1.9": {ID: "yoyodyne-ifd.1.9", Title: "the item", Status: "open"},
			}}
			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			reply := triageReply(t, tracker, budgets, answer)

			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			if len(tracker.blocked) != 1 || !strings.Contains(tracker.blocked[0][1], testCase.says) {
				t.Fatalf("blocked = %#v, want the item blocked saying %q", tracker.blocked, testCase.says)
			}
			decision, decided := budgets.counters(t, "yoyodyne-ifd.1.9").DecisionOf(stoppedRun)
			if !decided || decision.EscalatedTo != testCase.to || decision.OperatorReason != testCase.operatorReason {
				t.Fatalf("decision = %#v, want escalated to %q for %q", decision, testCase.to, testCase.operatorReason)
			}
			if decision.EscalatesToOperator() != (testCase.to == "") {
				t.Fatalf("EscalatesToOperator() = %v for %#v", decision.EscalatesToOperator(), decision)
			}
		})
	}
}

// An escalation to a role reaches it through a report addressed to it; one
// whose only report is addressed to nobody would land on the Lead Product
// Manager's pile rather than the architect's, and is refused like an escalation
// with no report at all.
func TestAnEscalationToARoleNeedsAReportAddressedToThatRole(t *testing.T) {
	t.Parallel()

	answer := reportReply(
		trackerReply("Escalating.",
			`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"escalate","to":"architect","reason":"the design does not say how this is meant to work"}`),
		`{"severity":"warning","message":"yoyodyne-ifd.90 needs a design decision."}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item", Status: "open"},
	}}
	options := triageOptions(t, tracker, nil, answer)
	options.Reports = &fakeReports{}
	_, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
	escalation := &EscalationError{}
	if !errors.As(err, &escalation) || escalation.For != domain.RoleArchitect || escalation.WorkItemID != "yoyodyne-ifd.90" {
		t.Fatalf("Send() error = %v, want the escalation refused for want of a report for the architect", err)
	}
	if len(tracker.blocked) != 0 {
		t.Fatalf("a refused escalation blocked the item: %#v", tracker.blocked)
	}
}

// The escalation's target and reason are held to the decision that takes them.
func TestOnlyAnEscalationNamesWhomItGoesTo(t *testing.T) {
	t.Parallel()

	for _, action := range []TrackerAction{
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "wait", To: "architect", Reason: "why"},
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "wait", OperatorReason: ownership.OperatorCredential, Reason: "why"},
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "escalate", To: "developer", Reason: "why"},
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "escalate", To: "architect", OperatorReason: ownership.OperatorCredential, Reason: "why"},
	} {
		if problems := action.triageProblems(); len(problems) == 0 {
			t.Fatalf("triageProblems(%#v) = none, want it refused", action)
		}
	}
}

// A handling that says a report needs the operator is refused, and nothing is
// recorded, unless it gives a reason on his closed list; the refusal names the
// rung the handling role uses instead. A report's severity, and its having been
// acknowledged, are no such reason. One with a reason, or with a valid
// person-only remedy, goes through.
func TestAHandlingNamingTheOperatorNeedsAClosedListReason(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		extra   string
		applied bool
		reason  ownership.OperatorReason
	}{
		{name: "no reason", extra: `,"needs":"operator"`},
		{name: "a protected file", extra: `,"needs":"operator","operator_reason":"protected-file"`, applied: true, reason: ownership.OperatorProtectedFile},
		{name: "a person-only remedy", extra: `,"needs":"operator","person_only":{"reason":"credential","target":"the forge login","step":"log in to the forge again"}`, applied: true, reason: ownership.OperatorCredential},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reports := &fakeReports{}
			seedReports(t, reports,
				collectedReport("report-00000000000000000000000000000002", report.SeverityCritical, "the scheduler missed its hourly passes and recovered on its own", 2),
			)
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Recording it.",
					`{"action":"handle","report":"report-00000000000000000000000000000002","reason":"what became of it"`+testCase.extra+`}`)},
				{SessionID: "session-1", FinalText: "Recorded."},
				{SessionID: "session-1", FinalText: "Nothing more."},
			}}
			options := testOptions(t, provider)
			options.Reports = reports
			options.Tracker = &fakeTracker{}
			reply, err := openTestSession(t, options).Send(context.Background(), "deal with the report")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Actions) != 1 || reply.Actions[0].Applied != testCase.applied {
				t.Fatalf("actions = %#v, want applied = %t", reply.Actions, testCase.applied)
			}
			if !testCase.applied {
				if len(reports.handled) != 0 {
					t.Fatalf("a handling naming the operator for no reason was recorded: %#v", reports.handled)
				}
				if !strings.Contains(reply.Actions[0].Failure, ownership.Rung(domain.RoleProductManager)) {
					t.Fatalf("the refusal does not name the rung to use instead: %q", reply.Actions[0].Failure)
				}
				return
			}
			if len(reports.handled) != 1 || !reports.handled[0].NeedsOperator || reports.handled[0].OperatorReason != testCase.reason {
				t.Fatalf("handlings = %#v, want one needing the operator for %q", reports.handled, testCase.reason)
			}
		})
	}
}

// The October 2 regression: a critical report about the scheduler's own
// recovery was handled as though only a person could act. Handling it as the
// operator's is refused, naming the Lead Product Manager's rung; handling it
// again as the development manager's, which is whose remedy it is, goes
// through, and the record does not name the operator. The report's critical
// severity is no reason on his list.
func TestAReportAboutTheSchedulersOwnRecoveryStaysWithTheRoleWhoseRemedyItIs(t *testing.T) {
	t.Parallel()

	const reported = "report-a29273d0eff69eeeeb8f457b7d135108"
	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport(reported, report.SeverityCritical, "scheduled dispatch stopped for hours and the scheduler recovered it on its own", 2),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Only a person can act on this.",
			`{"action":"handle","report":"`+reported+`","needs":"operator","reason":"the scheduler's recovery needs looking at"}`)},
		{SessionID: "session-1", FinalText: trackerReply("It is the development manager's, then.",
			`{"action":"handle","report":"`+reported+`","reason":"the development manager owns the scheduler's recovery and is resolving the cause; nothing here is a person's act"}`)},
		{SessionID: "session-1", FinalText: "Recorded."},
		{SessionID: "session-1", FinalText: "Nothing more."},
	}}
	options := testOptions(t, provider)
	options.Reports = reports
	options.Tracker = &fakeTracker{}
	reply, err := openTestSession(t, options).Send(context.Background(), "deal with the scheduler report")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 {
		t.Fatalf("actions = %#v, want the refused handling and the one that followed", reply.Actions)
	}
	if reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, ownership.Rung(domain.RoleProductManager)) {
		t.Fatalf("first handling = %#v, want it refused naming the rung", reply.Actions[0])
	}
	if !reply.Actions[1].Applied {
		t.Fatalf("second handling = %#v, want it recorded", reply.Actions[1])
	}
	if len(reports.handled) != 1 || reports.handled[0].NeedsOperator || reports.handled[0].OperatorReason != "" ||
		!strings.Contains(reports.handled[0].Reason, "development manager") {
		t.Fatalf("handlings = %#v, want one handling that keeps the report with the development manager", reports.handled)
	}
}

// A reason not on the list is refused where the handling is read, before
// anything is looked up.
func TestAHandlingsOperatorReasonIsHeldToTheList(t *testing.T) {
	t.Parallel()

	for _, action := range []TrackerAction{
		{Action: actionHandle, Report: "report-00000000000000000000000000000002", Needs: "operator", OperatorReason: "approval", Reason: "why"},
		{Action: actionHandle, Report: "report-00000000000000000000000000000002", OperatorReason: ownership.OperatorCredential, Reason: "why"},
	} {
		if err := action.Validate(); err == nil {
			t.Fatalf("validate(%#v) = none, want the reason refused", action)
		}
	}
}

// An ask naming the operator for no reason on the list is refused and handed
// back with the rung the asking role uses instead; nothing is opened on the ask
// channel. One with a reason is put to him beside the reply and opens nothing
// either, because he answers on no exchange.
func TestAnAskNamingTheOperatorNeedsAClosedListReason(t *testing.T) {
	t.Parallel()

	carried := `{"severity":"warning","message":"the forge login has lapsed and only the operator can renew it; may the architect go ahead once he has?"}`
	for _, testCase := range []struct {
		name   string
		role   domain.AgentRole
		extra  string
		report string
		put    bool
		// noReport is a valid reason with nothing to carry the question to him.
		noReport bool
	}{
		{name: "the architect with no reason", role: domain.RoleArchitect, report: carried},
		{name: "a program manager with a reason off the list", role: domain.RoleProgramManager, extra: `,"operator_reason":"approval"`, report: carried},
		{name: "the Lead Product Manager with no reason", role: domain.RoleProductManager, report: carried},
		// A scheduled pass's reply is read by nobody, so an ask with no report
		// carrying it would reach no person; it is refused rather than said to
		// have been put to him.
		{name: "the architect for a credential with no report", role: domain.RoleArchitect, extra: `,"operator_reason":"credential"`, noReport: true},
		{name: "the architect for a credential, with a report addressed to a role", role: domain.RoleArchitect, extra: `,"operator_reason":"credential"`,
			report: `{"severity":"warning","for":"development-manager","message":"the forge login has lapsed."}`, noReport: true},
		{name: "the architect for a credential", role: domain.RoleArchitect, extra: `,"operator_reason":"credential"`, report: carried, put: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			answer := "Asking.\n\n" + askBlock(`{"ask":{"role":"operator"`+testCase.extra+`,"question":"may I go ahead?"}}`)
			if testCase.report != "" {
				answer = reportReply(answer, testCase.report)
			}
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: answer},
				{SessionID: "session-1", FinalText: "Carrying on."},
			}}
			channel := &fakeExchanges{answers: []string{"never reached"}}
			reports := &fakeReports{}
			options := testOptions(t, provider)
			options.Role = testCase.role
			options.Agent = string(testCase.role)
			options.Exchanges = channel
			options.Reports = reports
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "Carry on.")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(channel.conducted) != 0 || channel.open != nil {
				t.Fatalf("an ask naming the operator opened an exchange: %+v", channel.conducted)
			}
			if len(provider.requests) != 2 {
				t.Fatalf("provider invocations = %d, want what became of the ask handed back as a second round", len(provider.requests))
			}
			handedBack := provider.requests[1].Prompt
			if testCase.noReport {
				if len(reply.RefusedAsks) != 1 || len(reply.Exchanges) != 1 || strings.Contains(reply.Exchanges[0].State, "put to the operator") {
					t.Fatalf("refused asks = %q, exchanges = %+v, want the ask refused for want of a report", reply.RefusedAsks, reply.Exchanges)
				}
				if !strings.Contains(reply.RefusedAsks[0], "no report at warning or above to carry it to him") {
					t.Fatalf("refused ask reads %q, want it to say no report carried it", reply.RefusedAsks[0])
				}
				if !strings.Contains(handedBack, "was not put to anybody") || !strings.Contains(handedBack, "reaches him only through a report") {
					t.Fatalf("the hand-back does not say the question needs a report to reach him:\n%s", handedBack)
				}
				return
			}
			if !testCase.put {
				if len(reply.RefusedAsks) != 1 || !strings.Contains(reply.RefusedAsks[0], ownership.Rung(testCase.role)) {
					t.Fatalf("refused asks = %q, want the rung named", reply.RefusedAsks)
				}
				if !strings.Contains(handedBack, "was not put to anybody") || !strings.Contains(handedBack, ownership.Rung(testCase.role)) {
					t.Fatalf("the hand-back does not name the rung:\n%s", handedBack)
				}
				return
			}
			if len(reply.RefusedAsks) != 0 {
				t.Fatalf("an ask with a closed-list reason was refused: %q", reply.RefusedAsks)
			}
			if len(reply.Exchanges) != 1 || !strings.Contains(reply.Exchanges[0].State, "put to the operator") || reply.Exchanges[0].Question != "may I go ahead?" {
				t.Fatalf("exchanges = %+v, want the question put to the operator", reply.Exchanges)
			}
			if !strings.Contains(handedBack, "Your question is the operator's") || !strings.Contains(handedBack, "through the report you filed") {
				t.Fatalf("the asking role was not told where its question went:\n%s", handedBack)
			}
			// What reaches him is the report, which outlives the reply: a test that
			// found the question only on this reply's screen output would pass for
			// an ask that reached nobody.
			if len(reports.appended) != 1 || reports.appended[0].Severity != report.SeverityWarning || reports.appended[0].For != "" ||
				!strings.Contains(reports.appended[0].Message, "may the architect go ahead") {
				t.Fatalf("collected reports = %#v, want the report carrying the question kept", reports.appended)
			}
			var told strings.Builder
			session.reportExchanges(&told, reply)
			if !strings.Contains(told.String(), "may I go ahead?") || !strings.Contains(told.String(), "put to the operator") {
				t.Fatalf("the operator was not shown the question:\n%s", told.String())
			}
		})
	}
}
