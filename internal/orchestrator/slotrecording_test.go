package orchestrator

// Every developer run records the developer slot it occupies, however it was
// started: pulled into a slot by the scheduler, started with none named by a
// re-run decision, or taken up again by a repair or continuation with none on
// its record. The health check and the status line read the slot off the
// record, so a run that recorded none was shown in no slot while it held one.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// occupySlot records a running run in developer slot number, holding it.
func occupySlot(t *testing.T, store *runstate.Store, workItemID string, number int) runstate.State {
	t.Helper()
	occupant := approvedStoppedState()
	occupant.RunID, occupant.WorkItemID = "run-"+strings.Repeat(string(rune('a'+number)), 32), workItemID
	occupant.Status, occupant.Phase, occupant.CompletedAt = runstate.StatusRunning, runstate.PhaseDeveloping, nil
	occupant.IntegrationStop, occupant.Environmental, occupant.Failure = nil, nil, ""
	if err := store.Create(occupant); err != nil {
		t.Fatalf("Create(occupant) error = %v", err)
	}
	claimed, err := store.ClaimSlot(context.Background(), occupant, number, 3, time.Now())
	if err != nil {
		t.Fatalf("ClaimSlot(occupant) error = %v", err)
	}
	return claimed
}

// A run started with no slot on its context — as a development manager's
// re-run decision starts one — records the first free slot, not none, and takes
// that slot's sub-agent setting, in a project where no slot has an endpoint
// pair.
func TestARunStartedWithNoSlotNamedRecordsTheFreeSlotItHolds(t *testing.T) {
	t.Parallel()

	provider, _, outcome, pipeline := subagentRunIn(t, context.Background(), func(pipeline *Pipeline, _ map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		threeSlotsWithSubagentsOnSlot2(pipeline)
		pipeline.Selection = runstate.Selection{By: runstate.SelectedByDevelopmentManager, Reason: "the development manager's triage decided a re-run"}
		occupySlot(t, pipeline.Store.(*runstate.Store), "yoyodyne-occupant", 1)
	})
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RecordedSlot() != 2 || state.Routing.Slot.Origin != runstate.RoutingClaimed || state.Routing.Developer != nil {
		t.Fatalf("routing = %#v, want slot 2, the first free, recorded and nothing pinned", state.Routing)
	}
	if state.Subagents == nil || !state.Subagents.Enabled || state.Subagents.Slot != 2 {
		t.Fatalf("recorded sub-agents %+v, want slot 2's setting", state.Subagents)
	}
	if attempts := provider.RequestsForRole(domain.RoleDeveloper); len(attempts) == 0 || attempts[0].Subagents == nil {
		t.Fatal("the run's developer was not given slot 2's sub-agents")
	}
}

// A slot a run records when nothing named one is the one the status line's
// reading would have put it in: a slot preferring one of its labels, then a
// slot preferring nothing, then any other.
func TestASlotClaimedWithNoneNamedFollowsTheSlotsPreferences(t *testing.T) {
	t.Parallel()

	preferences := []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}, {Prefer: []string{"team-development"}, Dedicated: true}, {}}
	for _, test := range []struct {
		name   string
		labels []string
		skip   int
		want   []int
	}{
		{"labelled for the dedicated slot", []string{"team-development"}, 0, []int{3, 2, 4, 1}},
		{"labelled for the preferring slot", []string{"dashboard"}, 0, []int{1, 2, 4, 3}},
		{"unlabelled", nil, 0, []int{2, 4, 1, 3}},
		{"the requested slot is left out", nil, 2, []int{4, 1, 3}},
	} {
		if got := slotClaimOrder(4, preferences, test.labels, test.skip); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: order = %v, want %v", test.name, got, test.want)
		}
	}
	if got := slotClaimOrder(3, nil, nil, 0); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("with no preferences: order = %v, want the lowest first", got)
	}
}

// A run taken up again whose record names no slot — one started before every
// run recorded its slot — records the first free one as it is taken up, both
// when it was holding a slot all along and when it continues from a pause;
// one already numbered keeps its number.
func TestARunTakenUpAgainWithNoSlotRecordedRecordsTheOneItHolds(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		paused bool
	}{
		{name: "holding a slot"},
		{name: "continuing from a pause", paused: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			pipeline := Pipeline{Store: store, Tracker: &orchestratortest.Tracker{}}
			pipeline.Config.Execution.MaxConcurrentDevelopers = 3
			occupySlot(t, store, "yoyodyne-occupant", 1)

			unnumbered := approvedStoppedState()
			unnumbered.WorkItemID = "yoyodyne-unnumbered"
			unnumbered.Status, unnumbered.Phase, unnumbered.CompletedAt = runstate.StatusRunning, runstate.PhaseDeveloping, nil
			unnumbered.IntegrationStop, unnumbered.Environmental, unnumbered.Failure = nil, nil, ""
			if test.paused {
				unnumbered.DependencyPause = &runstate.DependencyPause{Blockers: []string{"yoyodyne-z"}}
			}
			if err := store.Create(unnumbered); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			unnumbered, err = store.Load(unnumbered.RunID)
			if err != nil {
				t.Fatal(err)
			}
			taken, err := pipeline.reclaimSlot(context.Background(), unnumbered)
			if err != nil {
				t.Fatalf("reclaimSlot() error = %v", err)
			}
			if taken.RecordedSlot() != 2 || taken.DependencyPause != nil {
				t.Fatalf("taken up in slot %d with pause %+v, want slot 2 and no pause", taken.RecordedSlot(), taken.DependencyPause)
			}
			stored, err := store.Load(unnumbered.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.RecordedSlot() != 2 {
				t.Fatalf("the record names slot %d, want slot 2", stored.RecordedSlot())
			}
			if err := store.Save(taken); err != nil {
				t.Fatalf("Save() of the taken-up run error = %v, want its copy to hold the routing written", err)
			}
			again, err := pipeline.reclaimSlot(context.Background(), taken)
			if err != nil || again.RecordedSlot() != 2 {
				t.Fatalf("taken up again in slot %d, error %v, want it to keep slot 2", again.RecordedSlot(), err)
			}
		})
	}
}
