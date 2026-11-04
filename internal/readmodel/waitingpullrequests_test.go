package readmodel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// indexedRuns is runs that list the waits for a person's merge from the run
// index, as *runstate.Store does.
type indexedRuns struct{ fakeRuns }

func (r indexedRuns) WaitingForPersonMerge() ([]runstate.State, error) {
	var waiting []runstate.State
	for _, state := range r.recorded {
		if state.WaitingForPersonMerge() {
			waiting = append(waiting, state)
		}
	}
	return waiting, nil
}

func waitingRun(number int, workItemID, title string) runstate.State {
	return runstate.State{
		RunID:         fmt.Sprintf("run-%d", number),
		WorkItemID:    workItemID,
		WorkItemTitle: title,
		Status:        runstate.StatusSucceeded,
		Branch:        "yoyodyne/" + workItemID,
		TargetBranch:  "main",
		PullRequest:   &runstate.PullRequest{Number: number},
		PersonMerge:   &runstate.PersonMerge{Number: number, Since: moment.Add(time.Duration(number) * time.Minute)},
	}
}

// At the limit the banner every surface opens with says new work is not
// starting because the pull requests are waiting for a person to merge, and
// names each by number and by its work item's title; under it, or with no
// limit, nothing is said.
func TestTheBannerSaysWhyNoNewWorkStartsAtTheLimitOnWaitingPullRequests(t *testing.T) {
	t.Parallel()
	runs := []runstate.State{waitingRun(41, "item-one", "Recorded title one"), waitingRun(42, "item-two", "Recorded title two")}
	read := func(limit int) Standing {
		sources := quietSources()
		sources.Runs = indexedRuns{fakeRuns{prices: map[string]runstate.ItemPrice{}, recorded: runs}}
		sources.MaxWaitingPullRequests = limit
		return ReadStanding(context.Background(), sources)
	}

	held := read(2)
	if held.WaitingPullRequests == nil || len(held.WaitingPullRequests.Waiting) != 2 {
		t.Fatalf("waiting = %#v, want both pull requests read at the limit", held.WaitingPullRequests)
	}
	rendered := held.Render()
	for _, want := range []string{
		"Not starting new work because 2 pull requests are waiting for a person to merge",
		"#41 for Recorded title one (item-one)",
		"#42 for Recorded title two (item-two)",
	} {
		if !strings.Contains(held.RenderBanner(), want) || !strings.Contains(held.RenderBrief(), want) {
			t.Fatalf("banner does not say %q:\n%s", want, rendered)
		}
	}

	for _, limit := range []int{3, 0} {
		if quiet := read(limit); quiet.WaitingPullRequests != nil || quiet.RenderBanner() != "" {
			t.Fatalf("limit %d: waiting = %#v, banner %q; want nothing said", limit, quiet.WaitingPullRequests, quiet.RenderBanner())
		}
	}
}

// Where the tracker holds the item, its title is the tracker's, in the form
// every other list of work items is shown in.
func TestTheWaitingPullRequestsAreNamedWithTheTrackersTitles(t *testing.T) {
	t.Parallel()
	waiting := runstate.WaitingPullRequestsOf(1, []runstate.State{waitingRun(7, "ex-7", "Recorded title")})
	titles := NewWorkItemTitles([]beads.WorkItem{{ID: "ex-7", Title: "Current title", Priority: 1}}, "ex")
	said := SayWaitingPullRequests(waiting, titles)
	if !strings.Contains(said, "#7 for (P1) Current title (ex-7)") {
		t.Fatalf("said %q, want the tracker's title", said)
	}
	if said := SayWaitingPullRequests(waiting, nil); !strings.Contains(said, "#7 for Recorded title (ex-7)") {
		t.Fatalf("said %q, want the run's recorded title where no tracker is read", said)
	}
}
