package slack

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// The harness's question about an open item outside a named epic that could
// block a member is said to the operators directly as well as in its thread, once,
// as information; any other report is said in the channel alone.
func TestAQuestionAboutAnOutsideBlockerIsSaidToTheOperatorsDirectlyOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityWarning, moment)
	if err := harness.reports.Append(report.Report{
		SchemaVersion: report.SchemaVersion, ID: "report-0123456789abcdef0123456789abcde1", Role: report.HarnessReporter,
		RunID: report.OutsideEpicBlockerRun + "yoyodyne-ep", WorkItemID: "yoyodyne-outside", DuringEpic: "yoyodyne-ep",
		ProductID: "yoyodyne", RepositoryID: "yoyodyne", Severity: report.SeverityWarning, For: domain.RoleProductManager,
		Message: "A question about scope: yoyodyne-outside could block a member", RecordedAt: moment.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	direct := map[string]bool{}
	for _, delivery := range batch.Deliveries {
		if delivery.Posts() && delivery.Notification.Event.Kind == notify.KindReportFiled {
			direct[delivery.Notification.Event.Refs.WorkItemID] = delivery.Direct
		}
	}
	if len(direct) != 2 || !direct["yoyodyne-outside"] || direct["yoyodyne-ifd.68.3"] {
		t.Fatalf("said directly = %v, want only the question about the outside blocker", direct)
	}
}
