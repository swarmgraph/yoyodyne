package cli

import (
	"io"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
)

// Every part that begins a run for a work item is wired the one decision on this
// product's own record. A part wired without it refuses nothing, which no test
// that supplies its own decision would show.
func TestEveryPartThatBeginsARunIsWiredTheDecisionOnTheNamedEpics(t *testing.T) {
	// Not parallel: the state root the components read is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := epicscope.ForOperator(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Start(epicscope.Naming{Epic: "yoyodyne-9k1y", From: "terminal", Members: []string{"yoyodyne-9k1y.1"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	pull, err := openPull(configPath, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for name, decide := range map[string]orchestrator.StartDecision{
		"pipeline":                            pipelineFrom(parts).MayStart,
		"scheduler's pull":                    pull.MayStart,
		"decided fresh run":                   rerunnerFrom(parts).MayStart,
		"decided repair":                      repairContinuerFrom(parts).MayStart,
		"carry-out of both":                   carryOutFrom(parts).MayStart,
		"repair decided from feedback":        feedbackRepairerFrom(parts).MayStart,
		"repair round asked for on a request": reconcilerFrom(parts).MayStart,
	} {
		if decide == nil {
			t.Errorf("the %s is wired no decision, so it would start anything", name)
			continue
		}
		if decide(maystart.Run(maystart.ReadyWork, "yoyodyne-outside")).MayStart() || !decide(maystart.Run(maystart.ReadyWork, "yoyodyne-9k1y.1")).MayStart() {
			t.Errorf("the %s does not read this product's named epics", name)
		}
	}
}
