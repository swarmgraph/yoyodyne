package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run that goes on with an environment still to be checked says so on its
// running line, naming the environment and the criteria it blocks, and a
// machine environment's instruction for a person; the phase is still said.
func TestARunWithAnEnvironmentStillToBeCheckedSaysSoOnStatus(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID:      "run-a",
			WorkItemID: "yoyodyne-env1",
			Status:     runstate.StatusRunning,
			Phase:      runstate.PhaseDeveloping,
			StartedAt:  moment.Add(-time.Hour),
			Environments: []runstate.EnvironmentCheck{
				{Name: "android client", Result: runstate.EnvironmentPassed, Where: "harness", Criteria: []int{1, 2}},
				{Name: "web ui", Result: runstate.EnvironmentFailed, Where: "developer", Criteria: []int{3}, Machine: true, HowToInstall: "npx playwright install chromium"},
			},
		}},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-env1": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 0}}},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 {
		t.Fatalf("running = %+v, want the one run", standing.Running)
	}
	said := standing.Running[0].Environments
	for _, want := range []string{`"web ui"`, "criterion 3", "npx playwright install chromium", "a person has to install on this machine"} {
		if !strings.Contains(said, want) {
			t.Fatalf("environments = %q, want it to say %q", said, want)
		}
	}
	if strings.Contains(said, "android") {
		t.Fatalf("environments = %q names the environment that passed", said)
	}
	rendered := standing.Render()
	if !strings.Contains(rendered, "    "+said+"\n") || !strings.Contains(rendered, "developing") {
		t.Fatalf("rendered:\n%s\nwant the phase and the environment still to be checked", rendered)
	}
}

// An item held because none of its environments was usable says which are
// missing and what each blocks.
func TestAnItemHeldForItsEnvironmentsSaysWhichAreMissing(t *testing.T) {
	t.Parallel()
	stopped := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	run := preservedRun("run-e1", "yoyodyne-env2", stopped)
	run.StopClass = runstate.CauseEnvironmentsMissing.StopClass()
	run.Environmental = &runstate.EnvironmentalRefusal{Cause: runstate.CauseEnvironmentsMissing, NothingRan: true, Settled: true, RecordedAt: stopped}
	run.Environments = []runstate.EnvironmentCheck{
		{Name: "ios client", Result: runstate.EnvironmentTimedOut, Where: "harness", Criteria: []int{2}},
	}
	held := heldForAPerson([]runstate.State{run}, nil, nothingDecided, asRecorded)
	reason := heldReason(t, held, "yoyodyne-env2")
	if !strings.Contains(reason, `"ios client"`) || !strings.Contains(reason, "criterion 2") || !strings.Contains(reason, "before any developer turn") {
		t.Fatalf("held for %q, want the missing environment and what it blocks", reason)
	}
}
