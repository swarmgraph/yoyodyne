package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// teamRuns lists the waits for the team's merge from the run index, as
// *runstate.Store does.
type teamRuns struct{ fakeRuns }

func (r teamRuns) WaitingForTeamMerge() ([]runstate.State, error) {
	var waiting []runstate.State
	for _, state := range r.recorded {
		if state.WaitingForTeamMerge() {
			waiting = append(waiting, state)
		}
	}
	return waiting, nil
}

func teamMergeRun(workItemID, title string, found bool) runstate.State {
	wait := runstate.TeamMerge{TargetBranch: "staging", UpstreamBranch: "main", LandingCommit: strings.Repeat("b", 40), Since: moment.Add(-time.Hour)}
	if found {
		at := moment
		wait.FoundAt = &at
		wait.FoundOn = strings.Repeat("c", 40)
	}
	return runstate.State{
		RunID:         "run-" + workItemID,
		WorkItemID:    workItemID,
		WorkItemTitle: title,
		Status:        runstate.StatusSucceeded,
		TargetBranch:  "staging",
		Integration:   &runstate.Integration{TargetBranch: "staging"},
		TeamMerge:     &wait,
	}
}

// A run whose change landed on the target branch is said, with both branches,
// wherever the standing is read; a run whose landing the team's branch already
// holds is not.
func TestTheStandingSaysWhichBranchAnItemLandedOnAndWhichItWaitsToBeMergedInto(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = teamRuns{fakeRuns{prices: map[string]runstate.ItemPrice{}, recorded: []runstate.State{
		teamMergeRun("ex-1", "Recorded title", false),
		teamMergeRun("ex-2", "Found already", true),
	}}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.WaitingForTeamMerge) != 1 || standing.WaitingForTeamMerge[0].WorkItemID != "ex-1" {
		t.Fatalf("waiting = %#v, want only ex-1", standing.WaitingForTeamMerge)
	}
	want := "Waiting for the team's merge: Recorded title (ex-1): landed on staging as bbbbbbbbbbbb, waiting for the team to merge it into main\n"
	if rendered := standing.RenderLines(); !strings.Contains(rendered, want) || strings.Contains(rendered, "ex-2") {
		t.Fatalf("lines:\n%s\nwant %q and no ex-2", rendered, want)
	}
	encoded, err := json.Marshal(standing)
	if err != nil || !strings.Contains(string(encoded), `"waiting_for_team_merge":[`) {
		t.Fatalf("json = %s, %v", encoded, err)
	}

	sources.Runs = teamRuns{fakeRuns{prices: map[string]runstate.ItemPrice{}}}
	none := ReadStanding(context.Background(), sources)
	if strings.Contains(none.Render(), "team's merge") || len(none.WaitingForTeamMerge) != 0 {
		t.Fatalf("nothing waits, but %q is said", none.Render())
	}
}

// The tracker's title is preferred to the run's recorded one.
func TestTheTeamMergeWaitIsNamedWithTheTrackersTitle(t *testing.T) {
	t.Parallel()
	titles := NewWorkItemTitles([]beads.WorkItem{{ID: "ex-1", Title: "Current title", Priority: 1}}, "ex")
	items := withTrackerTitles(runstate.WaitingForTeamMergeOf([]runstate.State{teamMergeRun("ex-1", "Recorded title", false)}), titles)
	if len(items) != 1 || !strings.HasPrefix(items[0].Says(), "Current title (ex-1): landed on staging") {
		t.Fatalf("items = %#v", items)
	}
}
