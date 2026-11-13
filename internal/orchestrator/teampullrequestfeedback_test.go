package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/factory"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeTeamFeedback answers every pull request's feedback with one reading.
type fakeTeamFeedback struct {
	readings map[int]publish.FeedbackReading
	reads    int
}

func (f *fakeTeamFeedback) Feedback(_ context.Context, number int) (publish.FeedbackReading, error) {
	f.reads++
	reading, ok := f.readings[number]
	if !ok {
		return publish.FeedbackReading{}, fmt.Errorf("pull request %d does not exist in this repository", number)
	}
	reading.Number = number
	return reading, nil
}

// fakeLandingPaths answers the paths each landing commit changed.
type fakeLandingPaths map[string][]string

func (f fakeLandingPaths) LandingPaths(_ context.Context, commit string) ([]string, error) {
	return f[commit], nil
}

// teamFeedbackFixture is a pull request #7 into the team's branch tracked as
// carrying three landed items: yoyodyne-first, which touched nothing listed,
// yoyodyne-a, which touched internal/a.go, and yoyodyne-b, which touched
// internal/b.go.
func teamFeedbackFixture(t *testing.T) (teamPullRequestFixture, *fakeTeamFeedback, *collectedReports, Reconciler) {
	t.Helper()
	f := newTeamPullRequestFixture(t, "yoyodyne-a", "yoyodyne-b")
	f.open(7, f.landing["yoyodyne-first"], f.landing["yoyodyne-a"], f.landing["yoyodyne-b"])
	f.mustTrack(t, 7, false, "yoyodyne-first", "yoyodyne-a", "yoyodyne-b")
	forge := &fakeTeamFeedback{readings: map[int]publish.FeedbackReading{}}
	reports := &collectedReports{}
	reconciler := f.reconciler(t)
	reconciler.Feedback = forge
	reconciler.Reports = reports
	reconciler.LandingPaths = fakeLandingPaths{
		f.landing["yoyodyne-first"]: {"README.md"},
		f.landing["yoyodyne-a"]:     {"internal/a.go"},
		f.landing["yoyodyne-b"]:     {"internal/b.go"},
	}
	return f, forge, reports, reconciler
}

// assertNothingStarted checks every carried run still waits for the team's
// merge with its repair attempts untouched, and every item is as it was.
func assertNothingStarted(t *testing.T, f teamPullRequestFixture, before map[string]runstate.State) {
	t.Helper()
	for item := range f.runs {
		state := f.load(t, item)
		if !state.WaitingForTeamMerge() || state.Status != before[item].Status || state.RepairAttempts != before[item].RepairAttempts || state.ForgeFeedback != nil {
			t.Fatalf("run of %s = status %s, repairs %d, feedback %#v; want it left waiting for the team's merge with nothing started", item, state.Status, state.RepairAttempts, state.ForgeFeedback)
		}
		if status := f.tracker.items[item].Status; status != "in_progress" {
			t.Fatalf("%s status = %q, want it left in progress", item, status)
		}
	}
}

func snapshotRuns(t *testing.T, f teamPullRequestFixture) map[string]runstate.State {
	t.Helper()
	states := map[string]runstate.State{}
	for item := range f.runs {
		states[item] = f.load(t, item)
	}
	return states
}

func TestTakeUpTeamFeedbackListsRequestedChangesWithTheItemsThatTouchedTheFile(t *testing.T) {
	t.Parallel()

	f, forge, reports, reconciler := teamFeedbackFixture(t)
	before := snapshotRuns(t, f)
	product := before["yoyodyne-first"].ProductID
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	forge.readings[7] = publish.FeedbackReading{
		State: "OPEN",
		Reviews: []publish.FeedbackReview{
			{ID: 11, Author: "teammate", State: publish.ReviewChangesRequested, Body: "This batch needs another look.", At: later},
		},
		LineComments: []publish.FeedbackComment{
			{ID: 21, Author: "teammate", Body: "This loop never ends.", At: later.Add(time.Minute), Path: "internal/a.go", Line: 12},
			// The harness's own words, marked as this product's, are not a request.
			{ID: 22, Author: "teammate", Body: "Answered by the harness.\n" + factory.Identity{Product: nonEmptyProduct(product)}.Mark(), At: later.Add(2 * time.Minute), Path: "internal/b.go", Line: 3},
		},
	}

	settled, err := reconciler.TakeUpTeamFeedback(context.Background())
	if err != nil {
		t.Fatalf("TakeUpTeamFeedback() error = %v", err)
	}
	if len(settled) != 1 || settled[0].Failure != "" || settled[0].New != 2 || settled[0].Requested != 2 || settled[0].Listed != 0 {
		t.Fatalf("settled = %#v, want two requests listed for the development manager and nothing failed", settled)
	}
	if !strings.Contains(settled[0].Detail, "nothing was started") {
		t.Fatalf("detail = %q, want it to say nothing was started", settled[0].Detail)
	}
	if len(reports.reported) != 2 {
		t.Fatalf("reports = %#v, want one for the review and one for the line comment", reports.reported)
	}
	for _, reported := range reports.reported {
		if reported.For != domain.RoleDevelopmentManager || reported.Severity != report.SeverityWarning || reported.Role != report.HarnessReporter {
			t.Fatalf("report = %#v, want a warning from the harness addressed to the development manager", reported)
		}
		if !strings.Contains(reported.Message, "pull request #7") || !strings.Contains(reported.Message, "Nothing was started") || strings.Contains(reported.Message, "Answered by the harness") {
			t.Fatalf("message = %q, want the pull request named, nothing started, and the harness's own words left out", reported.Message)
		}
	}
	review, line := reports.reported[0], reports.reported[1]
	if !strings.Contains(review.Message, "names no file, so every item the pull request carries is named: yoyodyne-a, yoyodyne-b, yoyodyne-first") {
		t.Fatalf("review message = %q, want every carried item named", review.Message)
	}
	if !strings.Contains(line.Message, "The landed changes that touched internal/a.go are those of yoyodyne-a.") || strings.Contains(line.Message, "yoyodyne-b") || line.WorkItemID != "yoyodyne-a" {
		t.Fatalf("line comment report = %#v, want only yoyodyne-a named and attributed", line)
	}
	assertNothingStarted(t, f, before)
	for item := range f.runs {
		cursor := f.load(t, item).TeamMerge.PullRequest.Feedback
		if cursor == nil || cursor.Through == nil {
			t.Fatalf("run of %s carries no feedback cursor, want it moved past what was acted on", item)
		}
	}

	// The next check acts on none of it again.
	settled, err = reconciler.TakeUpTeamFeedback(context.Background())
	if err != nil || len(settled) != 1 || settled[0].New != 0 || len(reports.reported) != 2 {
		t.Fatalf("second check = %#v, %v, %d reports; want nothing new and no further report", settled, err, len(reports.reported))
	}
}

func TestTakeUpTeamFeedbackThatAsksForNothingStartsNothing(t *testing.T) {
	t.Parallel()

	f, forge, reports, reconciler := teamFeedbackFixture(t)
	before := snapshotRuns(t, f)
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	forge.readings[7] = publish.FeedbackReading{
		State: "OPEN",
		Reviews: []publish.FeedbackReview{
			{ID: 31, Author: "lead", State: publish.ReviewApproved, Body: "Looks right.", At: later},
			// Said before the pull request was tracked, so not about anything the
			// harness was told it carries.
			{ID: 32, Author: "lead", State: publish.ReviewChangesRequested, Body: "Old.", At: time.Now().Add(-24 * time.Hour)},
		},
		Comments: []publish.FeedbackComment{
			{ID: 41, Author: "teammate", Body: "When does this ship?", At: later.Add(time.Minute)},
		},
	}

	settled, err := reconciler.TakeUpTeamFeedback(context.Background())
	if err != nil {
		t.Fatalf("TakeUpTeamFeedback() error = %v", err)
	}
	if len(settled) != 1 || settled[0].Failure != "" || settled[0].New != 2 || settled[0].Requested != 0 || settled[0].Listed != 1 || settled[0].Approvals != 1 {
		t.Fatalf("settled = %#v, want one comment listed and one approval recorded", settled)
	}
	if len(reports.reported) != 1 {
		t.Fatalf("reports = %#v, want one for the comment", reports.reported)
	}
	comment := reports.reported[0]
	if comment.Severity != report.SeverityNote || comment.For != domain.RoleDevelopmentManager || comment.WorkItemID != "" ||
		!strings.Contains(comment.Message, "no item is named and nothing was started") || strings.Contains(comment.Message, "yoyodyne-a") {
		t.Fatalf("comment report = %#v, want a note to the development manager naming no item", comment)
	}
	for item := range f.runs {
		notes := f.tracker.items[item].Notes
		if !strings.Contains(notes, "lead approved the pull request") || !strings.Contains(notes, "does not stand in for the independent review") {
			t.Fatalf("%s notes = %q, want the approval recorded as information", item, notes)
		}
		cursor := f.load(t, item).TeamMerge.PullRequest.Feedback
		if cursor == nil || len(cursor.Approvals) != 1 {
			t.Fatalf("run of %s feedback = %#v, want the approval kept", item, cursor)
		}
	}
	assertNothingStarted(t, f, before)

	notes := f.tracker.notes["yoyodyne-a"]
	settled, err = reconciler.TakeUpTeamFeedback(context.Background())
	if err != nil || settled[0].New != 0 || len(reports.reported) != 1 || f.tracker.notes["yoyodyne-a"] != notes {
		t.Fatalf("second check = %#v, %v; want nothing acted on twice", settled, err)
	}
}

func TestCheckForgeReadsFeedbackOnTrackedPullRequests(t *testing.T) {
	t.Parallel()

	f, forge, reports, reconciler := teamFeedbackFixture(t)
	reconciler.Worktrees = quietTeamBranch{f.worktree}
	forge.readings[7] = publish.FeedbackReading{State: "OPEN", Comments: []publish.FeedbackComment{
		{ID: 51, Author: "teammate", Body: "Thanks.", At: time.Now().Add(time.Hour)},
	}}

	check, err := reconciler.CheckForge(context.Background())
	if err != nil {
		t.Fatalf("CheckForge() error = %v", err)
	}
	if len(check.TeamFeedback) != 1 || check.TeamFeedback[0].Listed != 1 || check.Failures() != 0 || len(reports.reported) != 1 {
		t.Fatalf("check = %#v, want the comment on #7 listed", check)
	}
	failing := ForgeCheck{TeamFeedback: []TeamFeedbackSettlement{{Failure: "x"}}}
	if failing.Failures() != 1 {
		t.Fatalf("Failures() = %d, want 1", failing.Failures())
	}
}

func nonEmptyProduct(product domain.ProductID) string {
	if product == "" {
		return "yoyodyne"
	}
	return string(product)
}
