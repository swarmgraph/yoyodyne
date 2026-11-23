package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A check that needs only to read an outside system is not the operator's under
// "credential" (ownership.CheckCredential). These drive a developer's report of
// a read-only check through the Lead Product Manager's handling, and the
// development manager's escalation, and show which reach his list.

const readOnlyCheckReport = "report-00000000000000000000000000000e42"

// handleReadOnlyCheck files a developer's report of a check that needed only to
// read a ticket tracker, opening with "credential:" as the report contract
// allows, and has the Lead Product Manager answer it with one handling.
func handleReadOnlyCheck(t *testing.T, handling string) (Reply, *fakeReports) {
	t.Helper()
	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport(readOnlyCheckReport, report.SeverityWarning,
			"credential: the acceptance check reads ticket ABC-12 in the project's ticket tracker, and no role can read the tracker", 2),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Recording it.",
			`{"action":"handle","report":"`+readOnlyCheckReport+`","reason":"the check reads the ticket tracker"`+handling+`}`)},
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
	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v, want the one handling", reply.Actions)
	}
	return reply, reports
}

// Naming the operator under "credential" for read access, or without saying
// which secret or account the step needs, is refused and nothing is recorded,
// so nothing reaches his list; the refusal says what to record instead.
func TestAHandlingNamingTheOperatorForReadAccessIsRefused(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name, extra, says string
	}{
		{name: "read access", extra: `,"needs":"operator","operator_reason":"credential","credential":{"needs":"read-access","what":"the project's ticket tracker"}`,
			says: "missing capability"},
		// The harness cannot tell the two apart, so it asks the role that raised
		// it rather than naming him.
		{name: "not saying which", extra: `,"needs":"operator","operator_reason":"credential"`,
			says: "cannot tell a secret or account only a person holds from read access a role lacks"},
		{name: "a credential with another reason", extra: `,"needs":"operator","operator_reason":"protected-file","credential":{"needs":"secret","what":"x"}`,
			says: `"credential" is taken only with the reason "credential"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reply, reports := handleReadOnlyCheck(t, testCase.extra)
			if reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, testCase.says) {
				t.Fatalf("handling = %#v, want it refused saying %q", reply.Actions[0], testCase.says)
			}
			if len(reports.handled) != 0 {
				t.Fatalf("a refused handling was recorded: %#v", reports.handled)
			}
		})
	}
}

// The handling the refusal points at records the read-only check as a missing
// capability naming the role and the system, and it is on nobody's list for the
// operator: neither a finding for him nor one handed back to a role as his.
func TestAReadOnlyOutsideCheckIsRecordedAsAMissingCapabilityAndNeverReachesTheOperator(t *testing.T) {
	t.Parallel()

	reply, reports := handleReadOnlyCheck(t, `,"missing_capability":{"role":"developer","system":"the project's ticket tracker"}`)
	if !reply.Actions[0].Applied {
		t.Fatalf("handling = %#v, want it recorded", reply.Actions[0])
	}
	if !strings.Contains(reply.Actions[0].Summary, "not the operator's") || !strings.Contains(reply.Actions[0].Summary, "admit the defect") {
		t.Fatalf("summary = %q, want it said as a defect to admit rather than his", reply.Actions[0].Summary)
	}
	if len(reports.handled) != 1 {
		t.Fatalf("handlings = %#v, want one", reports.handled)
	}
	handled := reports.handled[0]
	if handled.NeedsOperator || handled.OperatorReason != "" || handled.Hand != "" || handled.MissingCapability == nil ||
		handled.MissingCapability.Role != domain.RoleDeveloper || handled.MissingCapability.System != "the project's ticket tracker" {
		t.Fatalf("handling = %#v, want a missing capability naming the developer and the tracker, and nothing for the operator", handled)
	}
	if !strings.Contains(handled.Render(), "missing capability, a defect to admit: the developer has no read access to the project's ticket tracker") {
		t.Fatalf("the record does not say what is missing:\n%s", handled.Render())
	}
	if actions := readmodel.OperatorActions(reports.appended, reports.handled, readmodel.StepRecheck{}); len(actions) != 0 {
		t.Fatalf("operator's findings = %#v, want none", actions)
	}
	if returned := readmodel.ReturnedHandlings(reports.appended, reports.handled, readmodel.StepRecheck{}); len(returned) != 0 {
		t.Fatalf("returned handlings = %#v, want none", returned)
	}
}

// A step that does need a secret only a person holds still reaches him, and the
// record says which secret it is.
func TestAHandlingNamingTheSecretStillReachesTheOperatorAndSaysWhich(t *testing.T) {
	t.Parallel()

	reply, reports := handleReadOnlyCheck(t, `,"needs":"operator","operator_reason":"credential","credential":{"needs":"secret","what":"the tracker's API token, which only the operator's account can issue"}`)
	if !reply.Actions[0].Applied || len(reports.handled) != 1 {
		t.Fatalf("handling = %#v, handlings = %#v, want it recorded", reply.Actions[0], reports.handled)
	}
	handled := reports.handled[0]
	if handled.Credential == nil || handled.Credential.Needs != ownership.CredentialSecret ||
		!strings.Contains(handled.Render(), "needs: a secret only a person holds: the tracker's API token") {
		t.Fatalf("handling = %#v, want the secret recorded and rendered", handled)
	}
	if actions := readmodel.OperatorActions(reports.appended, reports.handled, readmodel.StepRecheck{}); len(actions) != 1 || actions[0].Reason != ownership.OperatorCredential {
		t.Fatalf("operator's findings = %#v, want this one under credential", actions)
	}
}

// The development manager's escalation is held the same way: read access is
// refused with the way to the Lead Product Manager, and a named account goes
// through and is recorded on the decision.
func TestAnEscalationUnderCredentialSaysWhichSecretOrAccount(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name, extra string
		applied     bool
	}{
		{name: "read access", extra: `,"credential":{"needs":"read-access","what":"the service that switches the project's features on and off"}`},
		{name: "not saying which", extra: ``},
		{name: "an account", extra: `,"credential":{"needs":"account","what":"the paid plan on the provider account"}`, applied: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			answer := reportReply(
				trackerReply("Escalating.",
					`{"action":"triage","id":"yoyodyne-ifd.1.9","run":"`+stoppedRun+`","decision":"escalate","to":"operator","operator_reason":"credential"`+testCase.extra+`,"reason":"its check cannot run"}`),
				`{"severity":"warning","message":"yoyodyne-ifd.1.9 cannot run its check."}`)
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.1.9": {ID: "yoyodyne-ifd.1.9", Title: "the item", Status: "open"},
			}}
			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			options := triageOptions(t, tracker, budgets, answer)
			options.Reports = &fakeReports{}
			reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
			decision, decided := budgets.counters(t, "yoyodyne-ifd.1.9").DecisionOf(stoppedRun)
			if !testCase.applied {
				refusal := strings.Join(reply.HandedBack, "\n")
				if err != nil {
					refusal += "\n" + err.Error()
				}
				if decided || len(tracker.blocked) != 0 || !strings.Contains(refusal, `"to": "product-manager"`) {
					t.Fatalf("decided = %v, blocked = %#v, refusal:\n%s\nwant it refused, pointing at the Lead Product Manager", decided, tracker.blocked, refusal)
				}
				return
			}
			if err != nil || !decided || decision.Credential == nil || decision.Credential.Needs != ownership.CredentialAccount {
				t.Fatalf("err = %v, decision = %#v, want the account recorded on it", err, decision)
			}
			if len(tracker.blocked) != 1 || !strings.Contains(tracker.blocked[0][1], "an account only a person holds: the paid plan") {
				t.Fatalf("blocked = %#v, want the item blocked naming the account", tracker.blocked)
			}
		})
	}
}

// A program manager's Blocked on a human entry under "credential" is held the
// same way: one for read access, and one that does not say which, are each
// refused on their own, with the way to the Lead Product Manager, so neither is
// written and neither reaches the operator's list; one naming a secret is kept
// with the secret recorded.
func TestALaneReportEntryForReadAccessIsRefusedAndNeverReachesTheOperator(t *testing.T) {
	t.Parallel()

	_, content, found, err := extractLaneReport(laneReportBlock(`{"blocked_on_a_human":[` +
		`{"item":"yoyodyne-ifd.9","what":"give the check read access to the ticket tracker","reason":"credential","credential":{"needs":"read-access","what":"the project's ticket tracker"},"cites":"report-7","since":"2026-10-12T16:40:00Z"},` +
		`{"item":"yoyodyne-ifd.10","what":"let the check read the other repository","reason":"credential","cites":"report-8","since":"2026-10-12T16:40:00Z"},` +
		`{"item":"yoyodyne-ifd.11","what":"issue a new forge token","reason":"credential","credential":{"needs":"secret","what":"the forge token"},"cites":"report-9","since":"2026-10-12T16:40:00Z"}],` +
		`"summary":"s","remaining":[],"blockers":[]}`))
	if err != nil || !found || content == nil {
		t.Fatalf("extractLaneReport() = %+v, %v, %v; want the report read", content, found, err)
	}
	if len(content.BlockedOnAHuman) != 1 || content.BlockedOnAHuman[0].Item != "yoyodyne-ifd.11" ||
		content.BlockedOnAHuman[0].Credential == nil || content.BlockedOnAHuman[0].Credential.What != "the forge token" {
		t.Fatalf("kept %+v, want only the entry naming the forge token", content.BlockedOnAHuman)
	}
	refused := strings.Join(content.Refused, "\n")
	if len(content.Refused) != 2 || !strings.Contains(refused, "missing capability") ||
		!strings.Contains(refused, "cannot tell a secret or account only a person holds from read access a role lacks") ||
		!strings.Contains(refused, "by a report to the Lead Product Manager rather than an entry here") {
		t.Fatalf("refused %q, want the read-access and unsaid entries refused, pointing at the Lead Product Manager", refused)
	}
	for _, entry := range content.BlockedOnAHuman {
		if entry.Item != "yoyodyne-ifd.11" && ownership.ResolveBlockedOnAHuman(entry.Reason).IsOperator() {
			t.Fatalf("entry %+v reached the operator's list", entry)
		}
	}
}

// A refused ask under "credential" is described by what was wrong with it: a
// field given with another reason, read access, or a secret left unnamed.
func TestARefusedCredentialAskIsDescribedByWhatWasWrong(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		ask  exchange.Ask
		says string
	}{
		{name: "another reason", ask: exchange.Ask{Role: "operator", OperatorReason: ownership.OperatorFundamentalIntent, Credential: &ownership.Credential{Needs: ownership.CredentialSecret, What: "x"}},
			says: `a question for "fundamental-intent" with a "credential" field`},
		{name: "read access", ask: exchange.Ask{Role: "operator", OperatorReason: ownership.OperatorCredential, Credential: &ownership.Credential{Needs: ownership.CredentialReadAccess, What: "the ticket tracker"}},
			says: "for read access to the ticket tracker"},
		{name: "naming nothing", ask: exchange.Ask{Role: "operator", OperatorReason: ownership.OperatorCredential},
			says: "without naming a secret or account only a person holds"},
	} {
		if said := refusedAskSays(domain.RoleArchitect, testCase.ask); !strings.Contains(said, testCase.says) {
			t.Errorf("%s: refusedAskSays() = %q, want it to say %q", testCase.name, said, testCase.says)
		}
	}
}
