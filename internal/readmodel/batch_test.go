package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeBatchRecords struct {
	records []runstate.BatchRecord
	err     error
}

func (f fakeBatchRecords) All() ([]runstate.BatchRecord, error) { return f.records, f.err }

func batchSettings() *config.BatchPullRequest {
	settings := config.DefaultBatchPullRequest()
	settings.Enabled = true
	return &settings
}

func batchCarried(workItemID string, commit string) runstate.WaitingForTeamMergeItem {
	return runstate.WaitingForTeamMergeItem{WorkItemID: workItemID, TargetBranch: "staging", UpstreamBranch: "main", LandingCommit: strings.Repeat(commit, 40), Since: moment.Add(-2 * time.Hour)}
}

func TestTheStandingSaysEachItemsStateOnTheBatchRoute(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = teamRuns{fakeRuns{prices: map[string]runstate.ItemPrice{}, recorded: []runstate.State{
		teamMergeRun("ex-1", "In the batch", false),
		teamMergeRun("ex-2", "Not yet", false),
	}}}
	// ex-1 and ex-2 both land as the same commit in this fixture, so the batch
	// carries ex-1 alone by item.
	head := strings.Repeat("a", 40)
	sources.BatchRoute = batchSettings()
	sources.BatchRecords = fakeBatchRecords{records: []runstate.BatchRecord{{
		SchemaVersion: runstate.BatchRecordSchemaVersion, TargetBranch: "staging", UpstreamBranch: "main",
		PullRequest: &runstate.BatchPullRequestRef{Number: 12, OpenedAt: moment}, Head: head,
		Items:     []runstate.BatchRecordItem{{WorkItemID: "ex-1", LandingCommit: strings.Repeat("b", 40)}},
		UpdatedAt: moment,
	}}}
	standing := ReadStanding(context.Background(), sources)
	says := strings.Join(standing.WaitingForTeamMergeSays, "\n")
	if !strings.Contains(says, "In the batch (ex-1)") || !strings.Contains(says, "batch state: in the open batch") || !strings.Contains(says, "batch state: unpublished") {
		t.Fatalf("says = %q, want ex-1 in the open batch and ex-2 unpublished", says)
	}
	if len(standing.BatchPullRequestSays) != 1 || !strings.Contains(standing.BatchPullRequestSays[0], "Pull request #12 is waiting on a person's approval") {
		t.Fatalf("batch says = %q", standing.BatchPullRequestSays)
	}
	rendered := standing.Render()
	if !strings.Contains(rendered, "waiting on a person's approval") {
		t.Fatalf("the terminal rendering lacks what the batch waits on:\n%s", rendered)
	}
}

func TestTheStandingSaysNothingOfTheBatchRouteWithItOff(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = teamRuns{fakeRuns{prices: map[string]runstate.ItemPrice{}, recorded: []runstate.State{teamMergeRun("ex-1", "Waiting", false)}}}
	sources.BatchRecords = fakeBatchRecords{err: errors.New("must not be read")}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.BatchPullRequestSays) != 0 || standing.BatchPullRequestProblem != "" || strings.Contains(standing.Render(), "batch") {
		t.Fatalf("with the route off the standing says %q %q", standing.BatchPullRequestSays, standing.BatchPullRequestProblem)
	}
	if standing.WaitingForTeamMerge[0].BatchState != "" {
		t.Fatalf("an item carries batch state %q with the route off", standing.WaitingForTeamMerge[0].BatchState)
	}
}

func TestAnUnreadableBatchRecordIsSaidAndNoStateIsClaimed(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.BatchRoute = batchSettings()
	sources.BatchRecords = fakeBatchRecords{err: errors.New("disk gone")}
	items, says, problem := ApplyBatchRoute(sources, []runstate.WaitingForTeamMergeItem{batchCarried("ex-1", "b")}, moment)
	if items[0].BatchState != "" || len(says) != 0 || !strings.Contains(problem, "disk gone") {
		t.Fatalf("items %v says %v problem %q", items, says, problem)
	}
}

func TestTheBatchWaitsOnTheReviewWindowAndNamesWhenItOpens(t *testing.T) {
	t.Parallel()
	settings := batchSettings()
	settings.ReviewWindow = &config.ReviewWindow{TimeZone: "UTC", Days: []string{"mon"}, From: "09:00", To: "17:00"}
	sources := quietSources()
	sources.BatchRoute = settings
	// 2026-11-22 is a Sunday.
	sunday := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	_, says, _ := ApplyBatchRoute(sources, []runstate.WaitingForTeamMergeItem{batchCarried("ex-1", "b")}, sunday)
	if len(says) != 1 || !strings.Contains(says[0], "waiting on the review window; it opens on Monday 09:00 UTC") {
		t.Fatalf("says = %q", says)
	}
}
