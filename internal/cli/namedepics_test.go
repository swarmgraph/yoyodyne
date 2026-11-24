package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
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

	// What was refused is kept for yoyo status, until it may start, except on the
	// two paths whose refusals are not kept.
	decide := pipelineFrom(parts).MayStart
	for _, path := range []maystart.Path{maystart.ResolveConflict, maystart.BringInFile} {
		if decide(maystart.Run(path, "yoyodyne-unkept")).MayStart() {
			t.Errorf("%s starts an item outside the named epics", path)
		}
	}
	start := maystart.Run(maystart.NamedRun, "yoyodyne-other")
	decide(start)
	refusals, err := maystart.ReadRefusals(parts.stateRoot, parts.config.Product.ID)
	if err != nil || len(refusals.Items) != 2 || refusals.Items[1].WorkItemID != "yoyodyne-other" || refusals.Items[1].Path != maystart.NamedRun {
		t.Fatalf("refusals = %+v, %v; want each item refused above kept once", refusals.Items, err)
	}
	if _, err := operator.Add("yoyodyne-9k1y", "yoyodyne-other", time.Now()); err != nil {
		t.Fatal(err)
	}
	decide(start)
	if refusals, err = maystart.ReadRefusals(parts.stateRoot, parts.config.Product.ID); err != nil || len(refusals.Items) != 1 {
		t.Fatalf("refusals = %+v, %v; want the added item's taken away", refusals.Items, err)
	}
	blocking := filepath.Join(home.ProductDirectory(parts.stateRoot, string(parts.config.Product.ID)), "not-started.json")
	if err := os.WriteFile(blocking, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refused := decide(maystart.Run(maystart.NamedRun, "yoyodyne-outside")); refused.MayStart() || !strings.Contains(refused.Why, "could not be recorded") {
		t.Fatalf("refused = %+v, want a refusal saying it could not be recorded", refused)
	}
}
