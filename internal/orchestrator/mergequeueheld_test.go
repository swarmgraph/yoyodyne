package orchestrator

// Taking up held changes: in the order they were approved, one queue at a
// time, only once what held them has cleared, and never past a full harness.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

type heldRuns map[string]runstate.State

func (r heldRuns) Load(runID string) (runstate.State, error) { return r[runID], nil }

type clearedRefusals struct{ cleared []runstate.MergeQueueKey }

func (c *clearedRefusals) ClearRefusal(_ context.Context, key runstate.MergeQueueKey) error {
	c.cleared = append(c.cleared, key)
	return nil
}

func heldEntryAndRun(t *testing.T, branch string, heldAt time.Time) (triage.Entry, runstate.State) {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	run := runstate.State{RunID: runID, RepositoryID: "yoyodyne", TargetBranch: branch,
		IntegrationStop: &runstate.IntegrationStop{Cause: runstate.CauseMergeQueueHeld, Phase: runstate.PhaseIntegrating, RecordedAt: heldAt}}
	entry := triage.Entry{Class: triage.ClassStoppedRun, RunID: runID, Key: "held-" + runID,
		IntegrationStop: &triage.IntegrationStop{Cause: string(runstate.CauseMergeQueueHeld), Phase: string(runstate.PhaseIntegrating)}}
	return entry, run
}

func TestHeldChangesAreTakenUpInTheOrderTheyWereApprovedOnceTheirQueueClears(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	docket := &memoryDocket{}
	runs := heldRuns{}
	var order []string
	add := func(branch string, heldAt time.Time) string {
		entry, run := heldEntryAndRun(t, branch, heldAt)
		docket.entries = append(docket.entries, entry)
		runs[run.RunID] = run
		return run.RunID
	}
	// Listed on the docket out of the order they were approved in.
	second := add("main", start.Add(2*time.Minute))
	first := add("main", start)
	other := add("release", start.Add(time.Minute))

	required := map[string]bool{"main": false, "release": true}
	refusals := &clearedRefusals{}
	full := false
	held := MergeQueueHeldChanges{
		Docket: docket, Runs: runs, Refusals: refusals, Enabled: true, ProductID: "yoyodyne",
		Harness: queuemode.Harness{PullRequests: true},
		Forge:   heldForge(required),
		Resume: func(_ context.Context, runID string) (IntegrationResumeResult, error) {
			if full {
				return IntegrationResumeResult{CapacityFull: &runstate.CapacityError{Active: 1, Limit: 1}}, nil
			}
			order = append(order, runID)
			return IntegrationResumeResult{Resumed: true}, nil
		},
	}
	pass := held.Take(context.Background())
	// The queue whose target cleared takes its changes up oldest approval
	// first; the one still required takes none, and its record is kept.
	if !reflect.DeepEqual(order, []string{first, second}) || pass.Held != 3 || pass.Resumed != 2 {
		t.Fatalf("taken up %v (%#v), want %v and the release queue left held", order, pass, []string{first, second})
	}
	if len(refusals.cleared) != 1 || refusals.cleared[0].TargetBranch != "main" {
		t.Fatalf("cleared = %#v, want only main's condition cleared", refusals.cleared)
	}

	// A full harness stops the taking up there, so nothing behind it goes first.
	order, full = nil, true
	required["release"] = false
	if pass := held.Take(context.Background()); pass.Resumed != 0 || len(order) != 0 {
		t.Fatalf("taken up %v (%#v) with every slot occupied, want nothing", order, pass)
	}
	full = false
	if pass := held.Take(context.Background()); !reflect.DeepEqual(order, []string{first, second, other}) || pass.Resumed != 3 {
		t.Fatalf("taken up %v (%#v), want each queue's held changes in order", order, pass)
	}
}

func TestATurnedOffSwitchClearsEveryHoldWithoutAskingTheForge(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	entry, run := heldEntryAndRun(t, "main", time.Now())
	docket.entries = append(docket.entries, entry)
	resumed := 0
	pass := MergeQueueHeldChanges{
		Docket: docket, Runs: heldRuns{run.RunID: run}, Refusals: &clearedRefusals{}, Enabled: false, ProductID: "yoyodyne",
		Forge: heldForge(map[string]bool{"main": true}),
		Resume: func(context.Context, string) (IntegrationResumeResult, error) {
			resumed++
			return IntegrationResumeResult{Resumed: true}, nil
		},
	}.Take(context.Background())
	if resumed != 1 || pass.Resumed != 1 {
		t.Fatalf("pass = %#v, want the held change taken up once the switch is off", pass)
	}
}

// heldForge answers whether each branch requires the forge's own merge queue.
type heldForge map[string]bool

func (f heldForge) QueueCapabilities(_ context.Context, branch string) (publish.QueueCapabilities, error) {
	observed := publish.QueueCapabilities{Forge: "github", TargetBranch: branch, ObservedAt: time.Now(), Protected: true}
	if f[branch] {
		observed.QueueAvailable, observed.QueueRequired = true, true
	}
	return observed, nil
}
