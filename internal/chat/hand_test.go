package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The development manager's cap crossing past her own five is her decision,
// and only the operator's keyboard can record it: the escalation hands it to
// his hand, naming the capability the harness lacks rather than a reason of
// his, and the decision is recorded with the hand and no reason.
func TestAnEscalationHandsACapCrossingToTheOperatorsHand(t *testing.T) {
	t.Parallel()

	answer := reportReply(
		trackerReply("Handing the crossing to the operator's hand.",
			`{"action":"triage","id":"yoyodyne-ifd.1.9","run":"`+stoppedRun+`","decision":"escalate","to":"operator","hand":"cap-crossing","reason":"cross the review-rounds cap to 8: the last round found the cause"}`),
		`{"severity":"warning","message":"yoyodyne-ifd.1.9 needs a sixth cap crossing."}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.1.9": {ID: "yoyodyne-ifd.1.9", Title: "the item", Status: "open"},
	}}
	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	reply := triageReply(t, tracker, budgets, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.blocked) != 1 || !strings.Contains(tracker.blocked[0][1], "the development manager's own decision that needs the operator's hand") {
		t.Fatalf("blocked = %#v, want the item blocked saying the decision is hers and needs his hand", tracker.blocked)
	}
	decision, decided := budgets.counters(t, "yoyodyne-ifd.1.9").DecisionOf(stoppedRun)
	if !decided || decision.Hand != ownership.HandCapCrossing || decision.OperatorReason != "" || !decision.EscalatesToOperator() {
		t.Fatalf("decision = %#v, want an escalation to the operator's hand for a cap crossing", decision)
	}
}

// A hand is held to the escalation that takes it: never beside a reason of the
// operator's, never on an escalation to a role or another decision, and never
// naming a gap nobody recorded.
func TestAHandIsHeldToTheEscalationThatTakesIt(t *testing.T) {
	t.Parallel()

	for _, action := range []TrackerAction{
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "wait", Hand: ownership.HandCapCrossing, Reason: "why"},
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "escalate", To: "architect", Hand: ownership.HandCapCrossing, Reason: "why"},
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "escalate", To: "operator", Hand: ownership.HandCapCrossing, OperatorReason: ownership.OperatorCredential, Reason: "why"},
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "escalate", To: "operator", Hand: "approval", Reason: "why"},
	} {
		if problems := action.triageProblems(); len(problems) == 0 {
			t.Fatalf("triageProblems(%#v) = none, want it refused", action)
		}
	}
}

// The Lead Product Manager's configuration decision that only the operator can
// write into .yoyodyne/config.yaml is recorded as hers with his hand, not as a
// finding naming him; a hand beside "needs" is refused and records nothing.
func TestAHandlingRecordsAConfigurationDecisionAsTheRolesWithTheOperatorsHand(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		extra   string
		applied bool
	}{
		{name: "her decision", extra: `,"hand":"operational-configuration"`, applied: true},
		{name: "beside needs", extra: `,"needs":"operator","operator_reason":"protected-file","hand":"operational-configuration"`},
		{name: "a gap nobody recorded", extra: `,"hand":"approval"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reports := &fakeReports{}
			seedReports(t, reports,
				collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "the race check takes twenty minutes", 2),
			)
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Recording it.",
					`{"action":"handle","report":"report-00000000000000000000000000000002","reason":"set checks.race to the narrowed line in .yoyodyne/config.yaml"`+testCase.extra+`}`)},
				{SessionID: "session-1", FinalText: "Recorded."},
				{SessionID: "session-1", FinalText: "Nothing more."},
			}}
			options := testOptions(t, provider)
			options.Reports = reports
			options.Tracker = &fakeTracker{}
			reply, err := openTestSession(t, options).Send(context.Background(), "deal with the report")
			if err != nil && testCase.applied {
				t.Fatalf("Send() error = %v", err)
			}
			if !testCase.applied {
				if len(reports.handled) != 0 {
					t.Fatalf("a handling with a misplaced hand was recorded: %#v", reports.handled)
				}
				return
			}
			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v, want the handling applied", reply.Actions)
			}
			handled := reports.handled
			if len(handled) != 1 || handled[0].NeedsOperator || handled[0].Hand != ownership.HandOperationalConfiguration {
				t.Fatalf("handlings = %#v, want one recording her decision with his hand", handled)
			}
		})
	}
}
