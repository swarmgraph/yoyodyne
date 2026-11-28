package slack

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Where the Lead Product Manager's handling of a question about scope names the
// operator, the harness's own question, built from the identifiers, is said to
// him directly, and her handling is not said to him as a second question.
func TestTheOperatorIsSaidTheHarnesssQuestionAboutScopeAndNotHerWords(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.feed.Epics = fakeNamedEpics{mode: epicscope.ModeEpic}
	scope, asked := "report-0123456789abcdef0123456789abcde2", "report-0123456789abcdef0123456789abcde3"
	for _, filed := range []report.Report{
		{ID: scope, RunID: report.ScopeQuestionRun + "judgment-1", Message: "A question about scope: yoyodyne-ep.1.1 was judged outside", RecordedAt: moment},
		{ID: asked, RunID: report.ScopeOperatorQuestionRun + "yoyodyne-ep", Message: "A question for the operator: `yoyo epic add yoyodyne-ep yoyodyne-ep.1.1` puts it in", RecordedAt: moment.Add(2 * time.Hour)},
	} {
		filed.SchemaVersion, filed.Role, filed.WorkItemID, filed.DuringEpic = report.SchemaVersion, report.HarnessReporter, "yoyodyne-ep.1.1", "yoyodyne-ep"
		filed.ProductID, filed.RepositoryID, filed.Severity, filed.For = "yoyodyne", "yoyodyne", report.SeverityWarning, domain.RoleProductManager
		if err := harness.reports.Append(filed); err != nil {
			t.Fatal(err)
		}
	}
	harness.handleFor(t, scope, "the epic cannot be delivered without it", true, ownership.OperatorOwnHold, moment.Add(time.Hour))
	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	direct := map[string]bool{}
	for _, delivery := range batch.Deliveries {
		if !delivery.Posts() {
			continue
		}
		if delivery.Stream == operatorActionStream {
			t.Fatalf("her handling was said to the operator: %#v", delivery.Notification.Event)
		}
		if delivery.Notification.Event.Kind == notify.KindReportFiled {
			direct[delivery.Notification.Event.Refs.RunID] = delivery.Direct
		}
	}
	if len(direct) != 2 || !direct[report.ScopeOperatorQuestionRun+"yoyodyne-ep"] || direct[report.ScopeQuestionRun+"judgment-1"] {
		t.Fatalf("said directly = %v, want only the harness's question to the operator", direct)
	}
}
