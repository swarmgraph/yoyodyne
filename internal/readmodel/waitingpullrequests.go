package readmodel

// The limit on pull requests waiting for a person to merge them, as every
// surface says it. The scheduler decides from the same reading
// (runstate.WaitingPullRequestsOf, read in orchestrator/waitingpullrequests.go),
// so the sentence a surface prints and the reason nothing new starts are one
// count of one set of records.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// personMergeWaits is the run index's listing of the runs waiting for a person
// to merge, which reads only the records it names. It is an optional
// capability of Runs, satisfied by *runstate.Store; a reading over runs without
// it says nothing about the limit rather than reading every record to find
// the waits.
type personMergeWaits interface {
	WaitingForPersonMerge() ([]runstate.State, error)
}

// WaitingPullRequestsOf is the limit on pull requests waiting for a person to
// merge (execution.max_waiting_pull_requests, as Sources carries it) read
// against the recorded runs, and why it could not be read where it could not.
// A limit of zero is none, and reads nothing.
func WaitingPullRequestsOf(sources Sources) (runstate.WaitingPullRequests, string) {
	if sources.MaxWaitingPullRequests <= 0 || sources.Runs == nil {
		return runstate.WaitingPullRequests{Limit: sources.MaxWaitingPullRequests}, ""
	}
	waits, ok := sources.Runs.(personMergeWaits)
	if !ok {
		return runstate.WaitingPullRequests{Limit: sources.MaxWaitingPullRequests}, ""
	}
	states, err := waits.WaitingForPersonMerge()
	if err != nil {
		return runstate.WaitingPullRequests{Limit: sources.MaxWaitingPullRequests},
			fmt.Sprintf("the pull requests waiting for a person to merge could not be read, so whether new work is held back for them is not known: %v", err)
	}
	return runstate.WaitingPullRequestsOf(sources.MaxWaitingPullRequests, states), ""
}

// SayWaitingPullRequests is why no new work is starting, with each pull
// request's work item named as this reading's titles name it — its current
// priority, labels, and title from the tracker — and as its run recorded it
// where the tracker does not hold the item or could not be read. It is empty
// where the limit is not reached.
func SayWaitingPullRequests(waiting runstate.WaitingPullRequests, titles *WorkItemTitles) string {
	return waiting.SaysNaming(func(request runstate.WaitingPullRequest) string {
		if _, known := titles.Title(request.WorkItemID); known && titles.Readable() {
			return fmt.Sprintf("#%d for %s", request.Number, titles.Name(request.WorkItemID))
		}
		return request.Says()
	})
}
