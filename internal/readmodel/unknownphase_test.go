package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A running record a newer build wrote with a phase this build does not know is
// listed with its phase said as unknown beside what was recorded, and in a stage
// of its own, rather than printed as a phase this build knows and filed under
// one it may not belong to.
func TestARunningRunWithAnUnknownPhaseIsShownAsUnknown(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{incomplete: []runstate.State{{
		RunID: "run-a", WorkItemID: "item-later", Status: runstate.StatusRunning, Phase: "a-phase-from-a-later-build", StartedAt: moment.Add(-time.Minute),
		UnknownValues: []runstate.UnknownValueError{{Field: "phase", Value: "a-phase-from-a-later-build"}},
	}}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 {
		t.Fatalf("running = %+v, want the run listed", standing.Running)
	}
	run := standing.Running[0]
	if run.Stage != StageUnknown {
		t.Errorf("stage = %q, want %q", run.Stage, StageUnknown)
	}
	shown := `phase unknown to this build (recorded as "a-phase-from-a-later-build")`
	if string(run.Phase) != shown {
		t.Errorf("phase = %q, want %q", run.Phase, shown)
	}
	if rendered := standing.Render(); !strings.Contains(rendered, shown) {
		t.Errorf("rendered standing does not say the phase is unknown:\n%s", rendered)
	}
	if says := stageSays(run); says != "at a step this build does not know" {
		t.Errorf("stageSays() = %q", says)
	}
}
