package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// unreadClosedItemsRun begins the run a report about the sweep's unread closed
// work is filed under, followed by when it was filed. It is how a later sweep
// finds the report an earlier one filed.
const unreadClosedItemsRun = "reconcile-closed-items@"

// reportUnreadClosedItems files a report for the development manager where a
// step of the reconcile sweep could not read which work items are closed, and
// returns its id. The pass's own record already carries the failure, but a
// finding is raised on it only once the pass has failed three times in a row,
// and a sweep that cannot read closed work is one that leaves runs recorded as
// merged unchecked against their target branch: that is worth saying the first
// time.
//
// One report stands for a run of these failures. While a report filed by an
// earlier sweep is still unhandled, nothing more is filed; once the development
// manager has handled it, the next failure files a new one.
func reportUnreadClosedItems(reports *runstate.ReportStore, attribution report.Attribution, failure error, now time.Time) (string, error) {
	unread := orchestrator.UnreadClosedItems(failure)
	if reports == nil || len(unread) == 0 {
		return "", nil
	}
	filed, err := reports.List()
	if err != nil {
		return "", fmt.Errorf("read the reports before saying that which work items are closed could not be read: %w", err)
	}
	handlings, err := reports.Handlings()
	if err != nil {
		return "", fmt.Errorf("read the report handlings before saying that which work items are closed could not be read: %w", err)
	}
	for _, standing := range report.Unhandled(filed, handlings) {
		if standing.Role == report.HarnessReporter && strings.HasPrefix(standing.RunID, unreadClosedItemsRun) {
			return "", nil
		}
	}
	attribution.Role = report.HarnessReporter
	attribution.Agent = ""
	attribution.WorkItemID = ""
	attribution.RunID = unreadClosedItemsRun + now.UTC().Format(time.RFC3339)
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleDevelopmentManager,
		Message:  unreadClosedItemsMessage(unread, now),
	}}, attribution, now)
	if err != nil {
		return "", err
	}
	for _, reported := range collected {
		if err := reports.Append(reported); err != nil {
			return "", fmt.Errorf("say that which work items are closed could not be read: %w", err)
		}
	}
	return collected[0].ID, nil
}

// unreadClosedItemsMessage says what the sweep could not do, what the tracker
// said, and what the usual cause is, within a report's bound. The tracker's own
// words come last and are what is cut where the whole would not fit.
func unreadClosedItemsMessage(unread []*orchestrator.ClosedItemsUnread, now time.Time) string {
	var needs, causes []string
	for _, step := range unread {
		needs = append(needs, step.Need)
		causes = append(causes, step.Err.Error())
	}
	said := fmt.Sprintf("The scheduled clean-up (yoyo reconcile) could not read which work items are closed at %s, so it could not %s on this pass; the next clean-up asks again. "+
		"A tracker read that does not answer within its limit is most often waiting behind other tracker commands, because each one locks the whole store while it runs; where other commands held the store when the read was stopped, what the tracker said names them. "+
		"Further failures file nothing while this report is unhandled, so handle it once the clean-up reads closed work again. What the tracker said: ",
		now.Local().Format("2006-01-02 15:04 MST"), strings.Join(needs, " or "))
	return said + oneline.Fold(strings.Join(causes, "; "), report.MaxMessageBytes-len(said)-len(oneline.Marker))
}
