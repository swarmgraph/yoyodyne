package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run waiting to try a failed install step again is said as that, in place
// of its phase, naming the step and the local time of the next try; once the
// wait is off the record the line says the phase again.
func TestARunWaitingToTryAnInstallStepAgainSaysSoOnTheRunningLine(t *testing.T) {
	t.Parallel()
	next := moment.Add(4 * time.Minute)
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID:       "run-a",
			WorkItemID:  "yoyodyne-okf6",
			Status:      runstate.StatusRunning,
			Phase:       runstate.PhaseDeveloping,
			StartedAt:   moment.Add(-time.Hour),
			InstallWait: &runstate.InstallWait{Name: "npm packages", Retry: 1, NextAt: next},
		}},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-okf6": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 0}}},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 {
		t.Fatalf("running = %+v, want the one run", standing.Running)
	}
	said := standing.Running[0].Install
	if !strings.Contains(said, `"npm packages"`) || !strings.Contains(said, "trying again at "+next.Local().Format("15:04 MST")) {
		t.Fatalf("install = %q, want the step and the local time of the next try", said)
	}
	if rendered := standing.Render(); !strings.Contains(rendered, "yoyodyne-okf6 (not in the tracker) — "+said+", 1h00m elapsed") {
		t.Fatalf("rendered:\n%s\nmissing the wait in place of the phase", rendered)
	}

	sources.Runs.(fakeRuns).incomplete[0].InstallWait = nil
	standing = ReadStanding(context.Background(), sources)
	if standing.Running[0].Install != "" {
		t.Fatalf("running = %+v, want nothing said once the wait is off the record", standing.Running)
	}
}
