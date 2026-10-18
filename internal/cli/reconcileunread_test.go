package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// unansweredClosedListing is what the tracker client said on 2026-10-18 at
// 04:44 PDT when the clean-up's listing of closed work was stopped at its limit
// on every attempt, with the commands then holding the store named.
const unansweredClosedListing = "bd list did not answer within its 30s bound on any of 3 attempts over 1m41s: " +
	"bd list failed with status timed_out and exit code -1: listing the work items for yoyo reconcile, which settles stopped and finished runs and runs in every maintenance pass " +
	"was stopped after 30s, at the 30s limit each tracker call is given; when it was stopped, the tracker's store was held open by process 38601 (bd show yoyodyne-ifd --json), " +
	"running since 2026-10-18 04:46:34 PDT; process 39706 (bd list --json --limit=0 --status=open), running since 2026-10-18 04:46:40 PDT"

// countingTracker is a tracker whose listings are counted, and whose closed
// listing fails with the next of failures before it answers.
type countingTracker struct {
	closed   []beads.WorkItem
	failures []error
	lists    map[string]int
	shows    int
}

func (c *countingTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	if c.lists == nil {
		c.lists = map[string]int{}
	}
	c.lists[status]++
	if status != "closed" {
		return nil, nil
	}
	if len(c.failures) > 0 {
		failure := c.failures[0]
		c.failures = c.failures[1:]
		return nil, failure
	}
	return c.closed, nil
}

func (c *countingTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	c.shows++
	return beads.WorkItem{ID: id, Status: "open"}, nil
}

func unreadReportStore(t *testing.T) *runstate.ReportStore {
	t.Helper()
	reports, err := runstate.NewReportStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	return reports
}

func standingDocket(t *testing.T) *runstate.DocketStore {
	t.Helper()
	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	entry := deliveredEntry()
	entry.RecordedAt = time.Now().Add(-time.Hour).UTC()
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	return store
}

// The read that failed the clean-up twice: the tracker did not answer the
// listing of closed work within its limit. The step says it could not read
// which work items are closed, as a failure the sweep can tell from the others,
// and the development manager is told on that first failure, in a report that
// carries what the tracker said, including what held the store.
func TestAClosedWorkReadTheTrackerDidNotAnswerIsReportedTheFirstTime(t *testing.T) {
	t.Parallel()

	tracker := &countingTracker{failures: []error{errors.New(unansweredClosedListing)}}
	_, _, err := sweepClosedItems(context.Background(), orchestrator.Docketer{Docket: standingDocket(t)}, &orchestrator.ClosedListing{Tracker: tracker})
	unread := orchestrator.UnreadClosedItems(errors.Join(errors.New("an unrelated step failed"), err))
	if len(unread) != 1 || unread[0].Need != "close their docket entries" {
		t.Fatalf("sweepClosedItems() error = %v, want it read as the closed work unread", err)
	}

	reports := unreadReportStore(t)
	now := time.Date(2026, 10, 18, 11, 51, 38, 0, time.UTC)
	id, err := reportUnreadClosedItems(reports, report.Attribution{ProductID: "yoyodyne", RepositoryID: "yoyodyne"}, errors.Join(errors.New("an unrelated step failed"), unread[0]), now)
	if err != nil || id == "" {
		t.Fatalf("reportUnreadClosedItems() = %q, %v; want a report filed", id, err)
	}
	filed, err := reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("reports = %#v, %v; want one", filed, err)
	}
	reported := filed[0]
	if reported.ID != id || reported.Role != report.HarnessReporter || reported.For != domain.RoleDevelopmentManager || reported.Severity != report.SeverityWarning {
		t.Fatalf("report = %#v, want the harness's warning for the development manager", reported)
	}
	for _, want := range []string{
		"could not read which work items are closed",
		"so it could not close their docket entries on this pass",
		"each one locks the whole store",
		"did not answer within its 30s bound on any of 3 attempts over 1m41s",
		"held open by process 38601 (bd show yoyodyne-ifd --json)",
	} {
		if !strings.Contains(reported.Message, want) {
			t.Errorf("report message %q does not say %q", reported.Message, want)
		}
	}
	if len(reported.Message) > report.MaxMessageBytes {
		t.Errorf("report message is %d bytes, over the bound of %d", len(reported.Message), report.MaxMessageBytes)
	}
}

// One report stands for a run of failures: a second failure while the first
// report is unhandled files nothing, and once the development manager has
// handled it the next failure files a new one. A sweep whose reads all
// answered files nothing at all.
func TestOneUnhandledReportStandsForEveryLaterUnreadClosedWork(t *testing.T) {
	t.Parallel()

	reports := unreadReportStore(t)
	attribution := report.Attribution{ProductID: "yoyodyne", RepositoryID: "yoyodyne"}
	failure := &orchestrator.ClosedItemsUnread{Need: "find runs recorded as merged that are not on their target branch", Err: errors.New(unansweredClosedListing)}
	at := time.Date(2026, 10, 17, 21, 1, 16, 0, time.UTC)

	if id, err := reportUnreadClosedItems(reports, attribution, errors.New("a forge read failed"), at); err != nil || id != "" {
		t.Fatalf("reportUnreadClosedItems(no unread closed work) = %q, %v; want nothing filed", id, err)
	}
	first, err := reportUnreadClosedItems(reports, attribution, failure, at)
	if err != nil || first == "" {
		t.Fatalf("first failure = %q, %v; want a report", first, err)
	}
	if again, err := reportUnreadClosedItems(reports, attribution, failure, at.Add(10*time.Minute)); err != nil || again != "" {
		t.Fatalf("second failure = %q, %v; want nothing more while the first is unhandled", again, err)
	}
	if err := reports.Handle(report.Handling{
		SchemaVersion: report.HandlingSchemaVersion,
		ReportID:      first,
		Role:          domain.RoleDevelopmentManager,
		Agent:         "development-manager",
		RunID:         "chat-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Reason:        "the clean-up read closed work again on its next pass",
		RecordedAt:    at.Add(20 * time.Minute),
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	later, err := reportUnreadClosedItems(reports, attribution, failure, at.Add(time.Hour))
	if err != nil || later == "" || later == first {
		t.Fatalf("failure after handling = %q, %v; want a new report", later, err)
	}
}

// The sweep's two steps that need the closed work share one reading of it: the
// second is answered from the first without asking the tracker again. A
// reading that failed is not kept, so the step after it asks again, and every
// other listing goes to the tracker as it would have.
func TestTheSweepsStepsShareOneReadingOfClosedWork(t *testing.T) {
	t.Parallel()

	tracker := &countingTracker{
		closed:   []beads.WorkItem{{ID: deliveredEntry().WorkItemID, Status: "closed"}},
		failures: []error{errors.New(unansweredClosedListing)},
	}
	listing := &orchestrator.ClosedListing{Tracker: tracker}
	if _, err := listing.List(context.Background(), "closed"); err == nil {
		t.Fatal("List() error = nil, want the first reading to fail as the tracker's did")
	}
	if _, err := listing.List(context.Background(), "closed"); err != nil {
		t.Fatalf("List() after a failed reading error = %v, want it asked again and answered", err)
	}
	closed, _, err := sweepClosedItems(context.Background(), orchestrator.Docketer{Docket: standingDocket(t)}, listing)
	if err != nil || closed != 1 {
		t.Fatalf("sweepClosedItems() = %d, %v; want the entry closed from the shared reading", closed, err)
	}
	if tracker.lists["closed"] != 2 {
		t.Fatalf("closed work listed %d times, want 2: the failed reading and the one shared", tracker.lists["closed"])
	}
	for range 2 {
		if _, err := listing.List(context.Background(), "open"); err != nil {
			t.Fatalf("List(open) error = %v", err)
		}
	}
	if tracker.lists["open"] != 2 {
		t.Fatalf("open work listed %d times, want each asked of the tracker", tracker.lists["open"])
	}
}
