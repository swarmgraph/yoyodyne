package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// A report written as every role's contract says (report.Contract): one that
// truly needs the operator opens with a reason on his closed list and a colon,
// and the Lead Product Manager's handling records that reason, which puts it on
// his line. One that opens with no reason is a role's: addressed to the
// architect, it waits on the architect; handled as needing the operator without
// a reason, as a handling recorded before such a handling was refused, it goes
// back to the Lead Product Manager to handle again. Neither reaches him.
func TestAReportOpeningWithAClosedListReasonReachesTheOperatorAndOneWithoutStaysWithItsRole(t *testing.T) {
	t.Parallel()

	reasoned := filedReport("report-00000000000000000000000000000011", report.SeverityWarning, moment.Add(-3*time.Hour))
	reasoned.Message = "protected-file: the PreToolUse hook has to be added to .claude/settings.json by hand"
	opening, _, _ := strings.Cut(reasoned.Message, ":")
	if !ownership.OperatorReason(opening).Valid() {
		t.Fatalf("the report opens with %q, which is not on the closed list", opening)
	}
	addressed := filedReport("report-00000000000000000000000000000012", report.SeverityCritical, moment.Add(-2*time.Hour))
	addressed.For = domain.RoleArchitect
	addressed.Message = "the design does not say which store the new index reads"
	unreasoned := filedReport("report-00000000000000000000000000000013", report.SeverityWarning, moment.Add(-time.Hour))
	unreasoned.Message = "the nightly pass keeps missing its window"

	sources := quietSources()
	sources.Reports = fakeReports{
		reports: []report.Report{reasoned, addressed, unreasoned},
		handlings: []report.Handling{
			{ReportID: reasoned.ID, Role: domain.RoleProductManager, RunID: "chat-1", Reason: "add the PreToolUse hook to .claude/settings.json by hand", RecordedAt: moment.Add(-150 * time.Minute), NeedsOperator: true, OperatorReason: ownership.OperatorReason(opening)},
			{ReportID: unreasoned.ID, Role: domain.RoleProductManager, RunID: "chat-1", Reason: "somebody should look at the nightly pass", RecordedAt: moment.Add(-30 * time.Minute), NeedsOperator: true},
		},
	}
	standing := ReadStanding(context.Background(), sources)

	movers := map[string]ownership.Mover{}
	for _, waiting := range standing.NeedsHuman {
		for _, id := range []string{reasoned.ID, addressed.ID, unreasoned.ID} {
			if strings.Contains(waiting.What(), id) {
				movers[id] = waiting.Mover
				if waiting.Mover == ownership.MoverOperator && waiting.Ownership.Reason != ownership.OperatorProtectedFile {
					t.Errorf("%s reaches the operator for %q, want %q", id, waiting.Ownership.Reason, ownership.OperatorProtectedFile)
				}
			}
		}
	}
	for id, want := range map[string]ownership.Mover{
		reasoned.ID:   ownership.MoverOperator,
		addressed.ID:  MoverArchitect,
		unreasoned.ID: MoverProductManager,
	} {
		if got, listed := movers[id]; !listed || got != want {
			t.Errorf("%s is listed under %q (listed: %v), want %q; NeedsHuman = %#v", id, got, listed, want, standing.NeedsHuman)
		}
	}
}
