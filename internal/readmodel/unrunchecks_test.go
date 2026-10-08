package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const codexResume = "make codex-resume"

// unrunStage is one run whose check stage began at the given hour, with the
// checks that ran and the ones that could not.
func unrunStage(runID string, at time.Time, ran []string, couldNotRun ...string) runstate.State {
	finished := at.Add(time.Minute)
	stage := &runstate.CheckStage{StartedAt: at, FinishedAt: &finished, BoundSeconds: 1800, Ran: ran}
	for _, command := range couldNotRun {
		stage.CouldNotRun = append(stage.CouldNotRun, runstate.CheckCouldNotRun{Command: command, Reason: "codex is not installed (" + runID + ")"})
	}
	return runstate.State{RunID: runID, CheckStage: stage}
}

// A check is unrunnable from the threshold'th change in a row it could not run
// on, counted back to the last change it ran on. A run whose stage never
// reached the check neither counts nor ends the count, a stage still running
// is not read, and the order is the order the stages began, not the order the
// records came in.
func TestACheckIsCountedFromTheLastChangeItRanOn(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	hour := func(n int) time.Time { return at.Add(time.Duration(n) * time.Hour) }
	running := unrunStage("run-running", hour(9), nil, codexResume)
	running.CheckStage.FinishedAt = nil
	states := []runstate.State{
		unrunStage("run-5", hour(5), []string{"make test"}, codexResume),
		unrunStage("run-1", hour(1), []string{"make test", codexResume}),
		unrunStage("run-2", hour(2), []string{"make test"}, codexResume),
		// The stage stopped at make test and never reached the check.
		unrunStage("run-3", hour(3), []string{"make test"}),
		unrunStage("run-4", hour(4), []string{"make test"}, codexResume),
		running,
		{RunID: "run-no-stage"},
	}
	unrun := UnrunChecksOf(states, 3)
	if len(unrun) != 1 {
		t.Fatalf("unrun = %+v, want the one check", unrun)
	}
	got := unrun[0]
	if got.Command != codexResume || got.Changes != 3 || !got.Since.Equal(hour(2)) || !got.LatestAt.Equal(hour(5)) || got.LatestRunID != "run-5" || got.Reason != "codex is not installed (run-5)" {
		t.Fatalf("unrun = %+v, want three changes since hour 2, the latest run-5's", got)
	}
	if more := UnrunChecksOf(states, 4); len(more) != 0 {
		t.Fatalf("UnrunChecksOf(4) = %+v, want nothing under the threshold", more)
	}
	// The check running again ends it.
	ranAgain := append(states, unrunStage("run-6", hour(6), []string{"make test", codexResume}))
	if after := UnrunChecksOf(ranAgain, 1); len(after) != 0 {
		t.Fatalf("UnrunChecksOf after the check ran = %+v, want nothing", after)
	}
	// No threshold reads the default.
	if defaulted := UnrunChecksOf(states, 0); len(defaulted) != 1 || DefaultCouldNotRunBeforeStatus != 3 {
		t.Fatalf("UnrunChecksOf(0) = %+v, want the default of three to raise it", defaulted)
	}
}

// The entry is on the attention line `yoyo status` prints, as the development
// manager's move, naming the check, how many changes, and since when, and it
// leaves once the configured number is not reached.
func TestAnUnrunCheckIsOnTheAttentionLine(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	states := []runstate.State{
		unrunStage("run-1", at, []string{"make test"}, codexResume),
		unrunStage("run-2", at.Add(time.Hour), []string{"make test"}, codexResume),
	}
	sources := quietSources()
	sources.Now = func() time.Time { return at.Add(3 * time.Hour) }
	sources.Runs = fakeRuns{recorded: states, prices: map[string]runstate.ItemPrice{}}
	sources.CouldNotRunBeforeStatus = 2

	standing := ReadStanding(context.Background(), sources)
	var found *Attention
	for index, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionUnrunCheck {
			found = &standing.NeedsHuman[index]
		}
	}
	if found == nil {
		t.Fatalf("NeedsHuman = %+v, want the check on it", standing.NeedsHuman)
	}
	if found.Mover != MoverDevelopmentManager || found.ID != codexResume {
		t.Fatalf("entry = %+v, want the development manager's move about the check", found)
	}
	rendered := standing.Render()
	for _, want := range []string{
		"Waiting on the development manager",
		"the check make codex-resume could not run on 2 changes in a row, since " + localMoment(at),
		"latest reason: codex is not installed (run-2)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Render() is missing %q:\n%s", want, rendered)
		}
	}

	sources.CouldNotRunBeforeStatus = 3
	for _, entry := range ReadStanding(context.Background(), sources).NeedsHuman {
		if entry.Kind == AttentionUnrunCheck {
			t.Fatalf("NeedsHuman carries %+v under the configured three", entry)
		}
	}
}
