package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// batchExample is the worked example the operator guide ships
// (docs/configuration/runs.md, "The batch pull request").
var batchExample = filepath.Join("..", "..", "docs", "examples", "batch-pull-request.yaml")

func TestTheBatchWorkedExampleLoadsThroughOrdinaryConfigurationLoading(t *testing.T) {
	t.Parallel()

	example, err := os.ReadFile(batchExample)
	if err != nil {
		t.Fatalf("read the worked example: %v", err)
	}
	if !strings.HasPrefix(string(example), "# AN EXAMPLE TO COPY AND ADAPT. Yoyodyne never loads this file.") {
		t.Fatal("the worked example does not open by saying it is an example to copy and adapt")
	}
	cfg := loadProject(t, minimalProjectConfig+string(example), nil).Config
	batch := cfg.Execution.BatchPullRequest
	if !cfg.Execution.BatchRouteOn() || batch.MinItems != 5 || batch.MaxWait != DefaultBatchMaxWait || batch.MergeMethod != BatchMergeMethodMerge {
		t.Fatalf("the example loaded as %+v", batch)
	}
	if batch.ReviewWindow == nil || batch.ReviewWindow.TimeZone != "America/Los_Angeles" || len(batch.ReviewWindow.Days) != 5 {
		t.Fatalf("the example's review window loaded as %+v", batch.ReviewWindow)
	}
	if batch.AutomatedReviewer != "reviewbot[bot]" || batch.ReviewRequest == nil || len(batch.ReviewRequest.Command) != 3 || batch.ReviewRequest.FormatInForce() != "json" {
		t.Fatalf("the example's reviewer and request loaded as %q and %+v", batch.AutomatedReviewer, batch.ReviewRequest)
	}
}

func TestBatchPullRequestDefaultsAreOffAndWithinBounds(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	batch := cfg.Execution.BatchPullRequest
	if batch.Enabled || cfg.Execution.BatchRouteOn() {
		t.Fatal("the batch route is on by default")
	}
	if batch.MinItems != 5 || batch.MaxWait != Duration(24*time.Hour) || batch.MergeMethod != "merge" || batch.ReviewWindow != nil || batch.AutomatedReviewer != "" || batch.ReviewRequest != nil {
		t.Fatalf("defaults = %+v", batch)
	}
	if !batch.IsZero() {
		t.Error("a project that says nothing about batching does not read as zero, so its configuration revision would change")
	}
	// Enabled without the team's branch runs nothing.
	enabled := loadProject(t, minimalProjectConfig+"execution:\n  batch_pull_request:\n    enabled: true\n", nil).Config
	if enabled.Execution.BatchRouteOn() {
		t.Error("the batch route is on with execution.upstream_branch unset")
	}
}

func TestBatchPullRequestRefusesValuesOutsideTheirBounds(t *testing.T) {
	t.Parallel()

	const head = "execution:\n  batch_pull_request:\n"
	window := func(zone, days, from, to string) string {
		return head + "    review_window:\n      time_zone: " + zone + "\n      days: " + days + "\n      from: \"" + from + "\"\n      to: \"" + to + "\"\n"
	}
	cases := []struct {
		name, yaml, want string
	}{
		{"min_items zero", head + "    min_items: 0\n", "min_items is 0, and it must be from 1 to 100"},
		{"min_items over", head + "    min_items: 101\n", "min_items is 101, and it must be from 1 to 100"},
		{"max_wait short", head + "    max_wait: 14m\n", "max_wait is 14m0s, and it must be from 15m0s to 336h0m0s"},
		{"max_wait long", head + "    max_wait: 337h\n", "max_wait is 337h0m0s"},
		{"merge_method", head + "    merge_method: rebase\n", `merge_method "rebase" must be merge or squash`},
		{"zone", window("Nowhere/Land", "[mon]", "09:00", "17:00"), `time_zone "Nowhere/Land" is not a time zone name`},
		{"day", window("UTC", "[monday]", "09:00", "17:00"), `has "monday", which is not a day`},
		{"no days", window("UTC", "[]", "09:00", "17:00"), "days must name at least one day"},
		{"hours backwards", window("UTC", "[mon]", "17:00", "09:00"), "must be later than from"},
		{"time format", window("UTC", "[mon]", "9am", "17:00"), `from "9am" is not a 24-hour time`},
		{"too little", window("UTC", "[mon]", "09:00", "09:30"), "must be open at least 1h0m0s a week"},
		{"reviewer", head + "    automated_reviewer: two accounts\n", "is not one forge account name"},
		{"request empty", head + "    review_request:\n      format: json\n", "names neither a command nor an https address"},
		{"request both", head + "    review_request:\n      command: [tell]\n      url: https://example.com/x\n", "names both a command and an address"},
		{"request http", head + "    review_request:\n      url: http://example.com/x\n", "must be an https address with a host"},
		{"request password", head + "    review_request:\n      url: https://u:p@example.com/x\n", "must not carry a user name or password"},
		{"request format", head + "    review_request:\n      command: [tell]\n      format: xml\n", `format "xml" must be json or text`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadProjectError(t, minimalProjectConfig+tc.yaml, nil)
			if err == nil || !strings.Contains(err.Error(), "execution.batch_pull_request") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one saying %q", err, tc.want)
			}
		})
	}
}

func TestBatchPullRequestAcceptsTheEdgesOfItsBounds(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"    min_items: 1\n    max_wait: 15m\n",
		"    min_items: 100\n    max_wait: 336h\n    merge_method: squash\n",
		"    review_window:\n      time_zone: UTC\n      days: [sat]\n      from: \"00:00\"\n      to: \"01:00\"\n",
		"    automated_reviewer: a\n    review_request:\n      url: https://example.com/x\n      format: text\n",
	} {
		loadProject(t, minimalProjectConfig+"execution:\n  batch_pull_request:\n"+body, nil)
	}
}

func TestReviewWindowSaysWhenItIsOpen(t *testing.T) {
	t.Parallel()

	var unset *ReviewWindow
	now := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	if !unset.Open(now) {
		t.Error("an unset window is not open any time")
	}
	window := &ReviewWindow{TimeZone: "America/Los_Angeles", Days: []string{"mon", "fri"}, From: "09:00", To: "17:00"}
	at := func(day int, hour int) time.Time {
		return time.Date(2026, 11, day, hour, 0, 0, 0, time.FixedZone("PST", -8*3600))
	}
	// 2026-11-23 is a Monday.
	for _, tc := range []struct {
		at   time.Time
		open bool
	}{
		{at(23, 8), false}, {at(23, 9), true}, {at(23, 16), true}, {at(23, 17), false}, {at(24, 12), false}, {at(27, 12), true},
	} {
		if got := window.Open(tc.at); got != tc.open {
			t.Errorf("Open(%s) = %v, want %v", tc.at, got, tc.open)
		}
	}
	next, ok := window.NextOpen(at(23, 18))
	if !ok || !next.Equal(at(27, 9)) {
		t.Errorf("NextOpen(Monday 18:00) = %s, %v, want Friday 09:00", next, ok)
	}
}
