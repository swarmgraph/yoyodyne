package readmodel

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// On 2026-10-03 a dashboard on an older build refused run records carrying
// stop causes a newer build had written, and one refused record cost the
// running and not-startable lines their whole reading. Every cause a current
// build writes — the newer bounds and every cause outside the work — is read by
// the readings the dashboard serves, with no problem said in place of an
// answer and every ending counted under its cause.
func TestTheReadingsTheDashboardServesReadEveryNewerStopCause(t *testing.T) {
	t.Parallel()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	classes := []runstate.StopClass{runstate.StopRelaunchBudget, runstate.StopEscalated}
	for _, cause := range runstate.EnvironmentalCauses() {
		classes = append(classes, cause.StopClass())
	}
	for index, class := range classes {
		id, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		completed := noon.Add(-time.Duration(index+1) * time.Minute)
		if err := store.Create(runstate.State{
			SchemaVersion: runstate.StateSchemaVersion,
			RunID:         id,
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			WorkItemID:    "yoyodyne-ifd.1",
			Backend:       "claude-code",
			Status:        runstate.StatusFailed,
			StopClass:     class,
			Failure:       string(class) + ": what stopped it",
			StartedAt:     completed.Add(-time.Hour),
			UpdatedAt:     completed,
			CompletedAt:   &completed,
		}); err != nil {
			t.Fatalf("Create() with stop class %q error = %v", class, err)
		}
	}

	standing := ReadStanding(context.Background(), Sources{Runs: store, Now: func() time.Time { return noon }})
	if standing.RunningProblem != "" {
		t.Errorf("running_problem = %q, want the runs read", standing.RunningProblem)
	}
	throughput := ReadThroughput(context.Background(), ThroughputSources{Runs: store, Now: func() time.Time { return noon }})
	if throughput.RunsProblem != "" {
		t.Fatalf("runs_problem = %q, want the runs read", throughput.RunsProblem)
	}
	today := window(t, throughput, "today")
	for _, class := range classes {
		if today.StopsByCause[class] != 1 {
			t.Errorf("today's stops = %v, want one under %q", today.StopsByCause, class)
		}
	}
}
