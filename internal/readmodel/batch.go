package readmodel

// Where each item waiting for the team's merge stands on the harness's own batch
// pull request, and what the open batch is waiting on. Only a project whose
// route is on (execution.batch_pull_request.enabled with
// execution.upstream_branch set) has any of it: Sources.BatchRoute is nil
// otherwise, and nothing here says anything. The words come from
// runstate.WaitingOn and runstate.BatchRecord.StateOf, which the item's notes
// use too, so a surface and a note never say two things.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// BatchRecords is the batch records the read model projects. It is an optional
// source, satisfied by *runstate.BatchRecordStore.
type BatchRecords interface {
	All() ([]runstate.BatchRecord, error)
}

// BatchWaitInputFor is the plain values runstate.WaitingOn needs, from the
// settings, the clock, and the items waiting on one target branch that are in no
// open batch.
func BatchWaitInputFor(settings config.BatchPullRequest, now time.Time, unpublished []runstate.WaitingForTeamMergeItem) runstate.BatchWaitInput {
	if settings.MinItems == 0 {
		settings.MinItems = config.DefaultBatchMinItems
	}
	if settings.MaxWait == 0 {
		settings.MaxWait = config.DefaultBatchMaxWait
	}
	input := runstate.BatchWaitInput{
		Now:               now,
		WindowOpen:        settings.ReviewWindow.Open(now),
		MinItems:          settings.MinItems,
		MaxWait:           settings.MaxWait.Duration(),
		Unpublished:       len(unpublished),
		AutomatedReviewer: settings.AutomatedReviewer,
		RequestConfigured: settings.ReviewRequest != nil,
	}
	for _, item := range unpublished {
		if input.OldestSince.IsZero() || item.Since.Before(input.OldestSince) {
			input.OldestSince = item.Since
		}
	}
	if !input.WindowOpen {
		if next, ok := settings.ReviewWindow.NextOpen(now); ok {
			input.WindowOpensAt = "on " + windowTime(settings.ReviewWindow, next)
		}
	}
	return input
}

// windowTime says a moment in the review window's own time zone.
func windowTime(window *config.ReviewWindow, at time.Time) string {
	if window != nil {
		if location, err := time.LoadLocation(window.TimeZone); err == nil {
			at = at.In(location)
		}
	}
	return at.Format("Monday 15:04 MST")
}

// ApplyBatchRoute gives each item the state it stands in on the batch route, and
// says what each target branch's batch waits on, one sentence a line. With the
// route off it returns the items as they were and says nothing. A record that
// cannot be read is said as a problem, and every item then reads as unpublished
// only where there is no record to contradict it, so nothing is claimed that was
// not read.
func ApplyBatchRoute(sources Sources, items []runstate.WaitingForTeamMergeItem, now time.Time) ([]runstate.WaitingForTeamMergeItem, []string, string) {
	if sources.BatchRoute == nil {
		return items, nil, ""
	}
	var records []runstate.BatchRecord
	if sources.BatchRecords != nil {
		var err error
		if records, err = sources.BatchRecords.All(); err != nil {
			return items, nil, fmt.Sprintf("the record of the batch pull request could not be read, so the state of each item on the batch route is not said: %v", err)
		}
	}
	recordOf := map[string]*runstate.BatchRecord{}
	for index := range records {
		recordOf[records[index].TargetBranch] = &records[index]
	}
	targets := map[string]bool{}
	for target := range recordOf {
		targets[target] = true
	}
	stated := make([]runstate.WaitingForTeamMergeItem, len(items))
	copy(stated, items)
	unpublished := map[string][]runstate.WaitingForTeamMergeItem{}
	for index := range stated {
		item := &stated[index]
		targets[item.TargetBranch] = true
		item.BatchState = runstate.BatchUnpublished
		if record := recordOf[item.TargetBranch]; record != nil {
			item.BatchState = record.StateOf(item.WorkItemID, item.LandingCommit)
		}
		if item.BatchState == runstate.BatchUnpublished {
			unpublished[item.TargetBranch] = append(unpublished[item.TargetBranch], *item)
		}
	}
	names := make([]string, 0, len(targets))
	for target := range targets {
		names = append(names, target)
	}
	sort.Strings(names)
	var says []string
	for _, target := range names {
		wait := runstate.WaitingOn(recordOf[target], BatchWaitInputFor(*sources.BatchRoute, now, unpublished[target]))
		if len(names) > 1 {
			wait.Says = fmt.Sprintf("For %s: %s", target, wait.Says)
		}
		says = append(says, wait.Says)
	}
	return stated, says, ""
}

// renderBatchPullRequest is what the batch waits on, one line each, and nothing
// with the route off.
func (s Standing) renderBatchPullRequest() string {
	var rendered strings.Builder
	if s.BatchPullRequestProblem != "" {
		rendered.WriteString(s.BatchPullRequestProblem + "\n")
	}
	for _, said := range s.BatchPullRequestSays {
		rendered.WriteString(said + "\n")
	}
	return rendered.String()
}
