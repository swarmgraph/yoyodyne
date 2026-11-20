package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// A raise past the development manager's own five is the Lead Product
// Manager's to grant, so an escalation handing it to the operator's keyboard is
// refused with that path, records nothing, and leaves the item unblocked.
func TestAnEscalationHandingACapRaiseToTheOperatorIsRefusedWithTheLeadProductManagersPath(t *testing.T) {
	t.Parallel()

	action := TrackerAction{Action: actionTriage, ID: "yoyodyne-ifd.1.9", Run: stoppedRun, Decision: "escalate", To: "operator",
		Hand: ownership.HandCapCrossing, Reason: "cross the review-rounds cap to 8: the last round found the cause"}
	problems := errors.Join(action.triageProblems()...)
	if problems == nil || !strings.Contains(problems.Error(), `"grant"`) || !strings.Contains(problems.Error(), `"to": "product-manager"`) {
		t.Fatalf("triageProblems() = %v, want the escalation refused naming the Lead Product Manager's grant", problems)
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
