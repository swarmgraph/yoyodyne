package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The running line names the preferred label beside each developer slot: which
// slot each run is in, and the free slots with what each prefers. It is read off
// the labels each run recorded at its claim, from the same derivation the
// scheduler fills the free slots from.
func TestTheRunningLineNamesThePreferredLabelBesideEachSlot(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 3
	sources.Slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{
			{RunID: "run-a", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-2 * time.Hour)},
			{RunID: "run-b", WorkItemID: "yoyodyne-ifd.2", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Hour), WorkItemLabels: []string{"dashboard"}},
		},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-ifd.1": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 1}}},
			"yoyodyne-ifd.2": {Runs: []runstate.RunPrice{{RunID: "run-b", CostUSD: 2}}},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.DeveloperSlots) != 3 {
		t.Fatalf("developer slots = %+v, want the three configured", standing.DeveloperSlots)
	}
	if standing.Running[1].Slot != 1 || standing.Running[0].Slot != 2 {
		t.Fatalf("running = %+v, want the dashboard run in slot 1 and the other in slot 2", standing.Running)
	}
	rendered := standing.Render()
	for _, want := range []string{
		"  yoyodyne-ifd.1 (not in the tracker) — developing, 2h00m elapsed, $1.00 so far, in developer slot 2\n",
		"  yoyodyne-ifd.2 (not in the tracker) — developing, 1h00m elapsed, $2.00 so far, in developer slot 1 (prefers the dashboard label)\n",
		"  developer slot 3 is free and prefers no label\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
	// The brief rendering keeps the head and drops the slots with the entries.
	if brief := standing.RenderBrief(); strings.Contains(brief, "developer slot") {
		t.Fatalf("brief rendering names slots:\n%s", brief)
	}
}

func TestAFreePreferringSlotIsNamedUnderAnEmptyRunningLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 2
	sources.Slots = []domain.DeveloperSlot{{}, {Prefer: []string{"reliability", "dashboard"}}}
	standing := ReadStanding(context.Background(), sources)
	rendered := standing.Render()
	want := "Running: nothing\n  developer slot 1 is free and prefers no label\n  developer slot 2 is free and prefers the reliability or dashboard labels\n"
	if !strings.HasPrefix(rendered, want) {
		t.Fatalf("rendered:\n%s\nwant it to open with:\n%s", rendered, want)
	}
}

// A project whose slots prefer nothing reads exactly as it did: no slot is
// carried and none is named.
func TestSlotsPreferringNothingAreNotNamed(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 2
	sources.Slots = []domain.DeveloperSlot{{}, {}}
	standing := ReadStanding(context.Background(), sources)
	if standing.DeveloperSlots != nil {
		t.Fatalf("developer slots = %+v, want none carried where none prefers a label", standing.DeveloperSlots)
	}
	if rendered := standing.Render(); strings.Contains(rendered, "developer slot") {
		t.Fatalf("rendered names slots:\n%s", rendered)
	}
}

// Runs that could not be read leave the slots unsaid rather than reported free.
func TestSlotsAreNotReportedFreeOverRunsNobodyCouldRead(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 1
	sources.Slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}}
	sources.Runs = fakeRuns{failIncomplete: context.DeadlineExceeded}
	standing := ReadStanding(context.Background(), sources)
	if standing.DeveloperSlots != nil {
		t.Fatalf("developer slots = %+v, want none over runs that could not be read", standing.DeveloperSlots)
	}
}

// A run paused on work its item waits on holds no developer slot, so status says
// it as paused rather than running and counts the slot it gave back as free. On
// 2026-09-27 such a run read as running for nineteen hours while it held the
// only slot a ready queue could have used.
func TestARunPausedOnADependencyIsSaidAsPausedAndItsSlotAsFree(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 2
	sources.Slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{
			{RunID: "run-a", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-2 * time.Hour)},
			{
				RunID: "run-b", WorkItemID: "yoyodyne-ifd.428.34", Status: runstate.StatusRunning, Phase: runstate.PhaseChecking,
				StartedAt: moment.Add(-20 * time.Hour), UpdatedAt: moment.Add(-19 * time.Hour),
				DependencyPause: &runstate.DependencyPause{Blockers: []string{"yoyodyne-ifd.398"}},
			},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 || standing.Running[0].RunID != "run-a" {
		t.Fatalf("running = %+v, want only the run holding a slot", standing.Running)
	}
	if len(standing.PausedRuns) != 1 || standing.PausedRuns[0].RunID != "run-b" || standing.PausedRuns[0].WaitingOn != "yoyodyne-ifd.398" {
		t.Fatalf("paused runs = %+v, want the dependency-paused run naming what it waits on", standing.PausedRuns)
	}
	free := 0
	for _, slot := range standing.DeveloperSlots {
		if slot.Free() {
			free++
		}
	}
	if free != 1 {
		t.Fatalf("developer slots = %+v, want the slot the paused run gave back counted free", standing.DeveloperSlots)
	}
	rendered := standing.Render()
	for _, want := range []string{
		"Running (1 developer run):\n",
		"Paused, holding no developer slot (1 developer run):\n",
		"  yoyodyne-ifd.428.34 (not in the tracker) — waiting on unfinished work it depends on: yoyodyne-ifd.398, paused 19h00m ago\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
	if brief := standing.RenderBrief(); !strings.Contains(brief, "Paused, holding no developer slot (1 developer run)\n") {
		t.Fatalf("brief rendering:\n%s\nwant the paused head kept", brief)
	}
}

// A confirmed document's landing is running, not paused, and holds no developer
// slot: it is listed with the running runs and every developer slot beside it
// reads free.
func TestADocumentLandingIsSaidAsRunningAndHoldsNoDeveloperSlot(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 1
	sources.Slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}}
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-document", WorkItemID: "run-document", Status: runstate.StatusRunning, Phase: runstate.PhaseChecking,
			StartedAt: moment.Add(-time.Minute), Document: &runstate.DocumentPublication{WriteID: "document-4.1"},
		}},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 || standing.Running[0].RunID != "run-document" || len(standing.PausedRuns) != 0 {
		t.Fatalf("running = %+v, paused = %+v; want the landing running", standing.Running, standing.PausedRuns)
	}
	if len(standing.DeveloperSlots) != 1 || !standing.DeveloperSlots[0].Free() {
		t.Fatalf("developer slots = %+v, want the one slot free beside the landing", standing.DeveloperSlots)
	}
}

// holdingRuns is a run store that lists what holds each developer slot, as
// runstate.Store.SlotHolders does.
type holdingRuns struct {
	fakeRuns
}

func (holdingRuns) SlotHolders(states []runstate.State, now time.Time) []runstate.SlotHolder {
	holders := make([]runstate.SlotHolder, 0, len(states))
	for _, state := range states {
		holders = append(holders, runstate.SlotHolder{Slot: 1, RunID: state.RunID, WorkItemID: state.WorkItemID, Since: state.StartedAt, Says: "counted"})
	}
	return holders
}

// What holds each developer slot is read off the runs in flight by the store's
// own rule, whatever the slots prefer, so it is there to be shown where no
// slot prefers anything.
func TestTheStandingCarriesWhatHoldsEachDeveloperSlot(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 1
	sources.Runs = holdingRuns{fakeRuns{incomplete: []runstate.State{
		{RunID: "run-a", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Hour)},
	}}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.SlotHolders) != 1 || standing.SlotHolders[0].RunID != "run-a" || standing.SlotHoldersProblem != "" {
		t.Fatalf("SlotHolders = %+v, problem %q; want run-a listed", standing.SlotHolders, standing.SlotHoldersProblem)
	}
}

// A dedicated slot's running line says whether its run is on the label's work
// or outside it because none was ready, and a free dedicated slot is said as
// kept for its label rather than preferring it.
func TestADedicatedSlotSaysWhetherItsRunIsOnItsLabel(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		labels []string
		want   string
	}{
		{"on-label", []string{"team-development"}, "in developer slot 1 (kept for the team-development label), working on work carrying the team-development label\n"},
		{"outside", nil, "in developer slot 1 (kept for the team-development label), working outside the team-development label because none of its work was ready\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// One slot, so the run is read into it whatever it carries.
			sources := quietSources()
			sources.Capacity = 1
			sources.Slots = []domain.DeveloperSlot{{Prefer: []string{"team-development"}, Dedicated: true}}
			sources.Runs = fakeRuns{
				incomplete: []runstate.State{
					{RunID: "run-a", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Hour), WorkItemLabels: test.labels},
				},
				prices: map[string]runstate.ItemPrice{"yoyodyne-ifd.1": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 1}}}},
			}
			rendered := ReadStanding(context.Background(), sources).Render()
			if !strings.Contains(rendered, test.want) {
				t.Fatalf("rendered:\n%s\nmissing: %q", rendered, test.want)
			}
		})
	}

	sources := quietSources()
	sources.Capacity = 2
	sources.Slots = []domain.DeveloperSlot{{}, {Prefer: []string{"team-development"}, Dedicated: true}}
	if rendered := ReadStanding(context.Background(), sources).Render(); !strings.Contains(rendered, "  developer slot 2 is free and kept for the team-development label\n") {
		t.Fatalf("rendered:\n%s\nwant the free dedicated slot said as kept for its label", rendered)
	}
}

// A run moved to another developer slot when it was taken up again says so on
// its running line — the slot it left and why — whether or not any slot
// prefers a label, so the move is never silent; a run never moved says nothing
// more than it did.
func TestTheRunningLineSaysARunWasMovedToAnotherSlotAndWhy(t *testing.T) {
	t.Parallel()
	moved := runstate.State{RunID: "run-a", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-2 * time.Hour),
		Routing: &runstate.RunRouting{Version: runstate.RoutingVersion, Slot: &runstate.RoutedSlot{
			Number: 2, Origin: runstate.RoutingClaimed, RecordedAt: moment.Add(-time.Hour), MovedFrom: 3,
			MovedBecause: "developer slot 3 was held by run run-b when this run was taken up again, and this run keeps no endpoint pair that would make it wait for that slot",
		}}}
	still := runstate.State{RunID: "run-c", WorkItemID: "yoyodyne-ifd.3", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Hour)}
	for name, slots := range map[string][]domain.DeveloperSlot{
		"no slot prefers a label":    {{}, {}, {}},
		"a slot is kept for a label": {{}, {}, {Prefer: []string{"team-development"}, Dedicated: true}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sources := quietSources()
			sources.Capacity = 3
			sources.Slots = slots
			sources.Runs = fakeRuns{incomplete: []runstate.State{moved, still}}
			standing := ReadStanding(context.Background(), sources)
			rendered := standing.Render()
			var line, other string
			for _, each := range strings.Split(rendered, "\n") {
				switch {
				case strings.Contains(each, "yoyodyne-ifd.1"):
					line = each
				case strings.Contains(each, "yoyodyne-ifd.3"):
					other = each
				}
			}
			if !strings.Contains(line, "moved from developer slot 3 to developer slot 2") || !strings.Contains(line, "because developer slot 3 was held by run run-b") {
				t.Fatalf("running line %q, want it to say the run moved from slot 3 and why\nrendered:\n%s", line, rendered)
			}
			if other == "" || strings.Contains(other, "moved") {
				t.Fatalf("running line %q, want a run never moved to say nothing about a move", other)
			}
		})
	}
}

// A run waiting at its merge step for GitHub to be reachable again holds no
// developer slot, so status says it as paused, in plain words, with when it next
// tries; and the banner every surface shows says it too, because a surface that
// lists only the running runs would otherwise not show it at all.
func TestARunWaitingForGitHubIsSaidAsPausedWithItsNextTry(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 1
	sources.Slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}}
	since := moment.Add(-14 * time.Minute)
	next := moment.Add(3 * time.Minute)
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"": {{ID: "yoyodyne-ifd.435.43", Title: "Second MCP piece", Status: "in_progress"}}}}}
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-merge", WorkItemID: "yoyodyne-ifd.435.43", Status: runstate.StatusRunning, Phase: runstate.PhaseIntegrating,
			StartedAt: moment.Add(-3 * time.Hour), UpdatedAt: moment.Add(-time.Minute),
			ForgeWait: &runstate.ForgeWait{
				Step:  "reading where origin's main stands before landing",
				Since: since, Tries: 4, NextTryAt: next,
				Failure: "ssh: connect to host ssh.github.com port 443: Undefined error: 0",
			},
		}},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 0 {
		t.Fatalf("running = %+v, want the waiting run off the running line", standing.Running)
	}
	if len(standing.PausedRuns) != 1 || standing.PausedRuns[0].WaitingForGitHub == "" {
		t.Fatalf("paused runs = %+v, want the run waiting for GitHub", standing.PausedRuns)
	}
	if len(standing.DeveloperSlots) != 1 || !standing.DeveloperSlots[0].Free() {
		t.Fatalf("developer slots = %+v, want the slot counted free while the run waits", standing.DeveloperSlots)
	}
	nextSaid := "next try at " + next.Local().Format("15:04 MST")
	if next.Local().YearDay() != time.Now().Local().YearDay() {
		nextSaid = "next try at " + next.Local().Format("2006-01-02 15:04 MST")
	}
	rendered := standing.Render()
	for _, want := range []string{
		"Paused, holding no developer slot (1 developer run):\n",
		"Second MCP piece (yoyodyne-ifd.435.43) — approved and waiting for GitHub to be reachable again: GitHub could not be reached while reading where origin's main stands before landing, 4 tries since ",
		nextSaid + ", waiting 14m\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
	// The banner names the work item by its title, with its number after it.
	if !strings.HasPrefix(standing.Paused, "Waiting for GitHub: the approved change for ") ||
		!strings.Contains(standing.Paused, "Second MCP piece (yoyodyne-ifd.435.43) is waiting for GitHub to be reachable again") ||
		!strings.Contains(standing.Paused, nextSaid) {
		t.Fatalf("banner = %q, want it to say the run is waiting for GitHub and when it next tries", standing.Paused)
	}
}
