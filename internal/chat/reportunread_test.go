package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Where only her pass runs, a report for the development manager is shown to
// the Lead Product Manager marked as addressed to a role no pass reads here,
// and one naming no role is shown as it always was. With the development
// manager's pass enabled as well, the same report is shown unmarked: the
// reader is worked out from the configuration, never written on the report.
func TestTheLeadProductManagerIsShownReportsNoOtherPassReadsMarked(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		readers report.Readers
		marked  bool
	}{
		"only her pass":             {readers: report.ReadersOf([]domain.AgentRole{domain.RoleProductManager}), marked: true},
		"the development manager's": {readers: report.ReadersOf([]domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager})},
	} {
		t.Run(name, func(t *testing.T) {
			reports := &fakeReports{}
			forDM := collectedReport("report-00000000000000000000000000000001", report.SeverityWarning, "the test server is left running", 1)
			forDM.For = domain.RoleDevelopmentManager
			seedReports(t, reports, forDM, collectedReport("report-00000000000000000000000000000002", report.SeverityNote, "the temporary folder is refused", 2))
			provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
			options := testOptions(t, provider)
			options.Reports = reports
			options.ReportReaders = tc.readers
			session, err := Open(options)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if _, err := session.Send(context.Background(), "what needs deciding?"); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			prompt := provider.requests[0].Prompt
			mark := "Addressed to the development manager, a role no pass reads here, so it is yours to handle as you handle a report naming no role."
			if strings.Contains(prompt, mark) != tc.marked || strings.Count(prompt, "a role no pass reads here") > 1 {
				t.Fatalf("marked = %v, want %v:\n%s", strings.Contains(prompt, mark), tc.marked, prompt)
			}
			if !strings.Contains(prompt, "the temporary folder is refused") {
				t.Fatalf("the report naming no role was not shown:\n%s", prompt)
			}
		})
	}
}
