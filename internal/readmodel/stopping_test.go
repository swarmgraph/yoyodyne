package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stoppingRuns is fakeRuns with stops recorded beside some of its runs.
type stoppingRuns struct {
	fakeRuns
	stops map[string]runstate.StopRequest
}

func (r stoppingRuns) StopRequested(runID string) (runstate.StopRequest, bool, error) {
	request, ok := r.stops[runID]
	return request, ok, nil
}

// A run somebody has asked to stop is said to be stopping on the running line,
// in place of the phase it last wrote, from the moment the stop is recorded;
// once the stop has waited past its bound the line says the run is overdue.
// On Nov 11 a run asked to stop at 04:10 PST still read "developing" at 04:55.
func TestARunAskedToStopIsSaidToBeStoppingOnTheRunningLine(t *testing.T) {
	t.Parallel()
	asked := moment.Add(-2 * time.Minute)
	runs := stoppingRuns{
		fakeRuns: fakeRuns{incomplete: []runstate.State{{
			RunID:      "run-a",
			WorkItemID: "yoyodyne-fo4v",
			Status:     runstate.StatusRunning,
			Phase:      runstate.PhaseDeveloping,
			StartedAt:  moment.Add(-time.Hour),
		}}},
		stops: map[string]runstate.StopRequest{"run-a": {RunID: "run-a", RequestedAt: asked, RequestedBy: "the development manager"}},
	}
	sources := quietSources()
	sources.Runs = runs
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 {
		t.Fatalf("running = %+v, want the one run", standing.Running)
	}
	want := "stopping: the development manager asked it to stop at " + runstate.LocalStamp(asked)
	if !strings.HasPrefix(standing.Running[0].Stopping, want) {
		t.Fatalf("stopping = %q, want it to begin %q", standing.Running[0].Stopping, want)
	}
	if rendered := standing.Render(); !strings.Contains(rendered, "yoyodyne-fo4v (not in the tracker) — "+want) || strings.Contains(rendered, "developing") {
		t.Fatalf("rendered:\n%s\nwant the run said to be stopping rather than developing", rendered)
	}

	runs.stops["run-a"] = runstate.StopRequest{RunID: "run-a", RequestedAt: moment.Add(-45 * time.Minute), RequestedBy: "the development manager"}
	standing = ReadStanding(context.Background(), sources)
	if says := standing.Running[0].Stopping; !strings.HasPrefix(says, "stopping, overdue:") || !strings.Contains(says, "still running 45 minutes later") {
		t.Fatalf("stopping = %q, want the run said to be overdue", says)
	}

	delete(runs.stops, "run-a")
	standing = ReadStanding(context.Background(), sources)
	if standing.Running[0].Stopping != "" {
		t.Fatalf("stopping = %q, want nothing said of a run nobody asked to stop", standing.Running[0].Stopping)
	}
}
