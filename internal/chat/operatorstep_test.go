package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
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
