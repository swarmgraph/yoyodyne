package readmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func countedStep(index int, kind intervention.Kind, items []string, run string, observed bool) intervention.Event {
	at := time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Minute)
	event := intervention.Event{
		SchemaVersion: intervention.SchemaVersion,
		ID:            fmt.Sprintf("intervention-%032x", index),
		ProductID:     "calc",
		Kind:          kind,
		At:            at,
		Items:         items,
		Run:           run,
		Said:          "a step",
		Via:           "the command line",
		RecordedAt:    at,
	}
	if observed {
		event.Via, event.Observed, event.By, event.RecordedBy = "", true, "Mason", "Mason"
	}
	return event
}

// A step counts toward a change when it names the change's item or any run made
// for it, once however many of the change's runs it names; a step naming no
// merged change is counted apart rather than spread across changes; and the
// count says it is a floor.
func TestHandStepsAreCountedPerMergedChangeByItemOrRun(t *testing.T) {
	t.Parallel()
	merged := []runstate.PromotedItem{
		{WorkItemID: "calc-1", ShippedRunID: "run-b", RunIDs: []string{"run-a", "run-b"}},
		{WorkItemID: "calc-2", ShippedRunID: "run-c", RunIDs: []string{"run-c"}},
	}
	events := []intervention.Event{
		// Named by item.
		countedStep(1, intervention.KindRun, []string{"calc-1"}, "", false),
		// Named by an earlier run of the same change, and by its item: one step.
		countedStep(2, intervention.KindStop, []string{"calc-1"}, "run-a", false),
		// Named by the run alone.
		countedStep(3, intervention.KindRerun, nil, "run-a", false),
		// Observed outside the harness, named by item.
		countedStep(4, intervention.KindReset, []string{"calc-1"}, "", true),
		// A directive scoped to both changes counts toward each, once in total.
		countedStep(5, intervention.KindDirective, []string{"calc-1", "calc-2"}, "", false),
		// Nothing merged is named: an unmerged item, and a restart of a part.
		countedStep(6, intervention.KindRun, []string{"calc-9"}, "", false),
		countedStep(7, intervention.KindRestart, nil, "", true),
	}
	count := CountInterventions(events, merged)

	if count.Merged != 2 || count.Attributed != 5 || count.Unattributed != 2 {
		t.Fatalf("count = %+v, want 2 merged, 5 attributed, 2 unattributed", count)
	}
	if count.PerChange != 2.5 {
		t.Errorf("per change = %v, want 2.5", count.PerChange)
	}
	first, second := count.Changes[0], count.Changes[1]
	if first.WorkItemID != "calc-1" || first.Performed != 4 || first.Observed != 1 || first.Total() != 5 {
		t.Errorf("calc-1 = %+v, want 4 performed and 1 observed", first)
	}
	if first.Kinds[intervention.KindStop] != 1 || first.Kinds[intervention.KindReset] != 1 {
		t.Errorf("calc-1 kinds = %v, want one stop and one reset", first.Kinds)
	}
	if second.WorkItemID != "calc-2" || second.Total() != 1 || second.Kinds[intervention.KindDirective] != 1 {
		t.Errorf("calc-2 = %+v, want the directive alone", second)
	}
	if !strings.Contains(count.Floor, "a floor") {
		t.Errorf("floor = %q, want the count to say it is a floor", count.Floor)
	}
}

func TestNothingMergedCountsNothingPerChange(t *testing.T) {
	t.Parallel()
	count := CountInterventions([]intervention.Event{countedStep(1, intervention.KindRun, []string{"calc-1"}, "", false)}, nil)
	if count.Merged != 0 || count.PerChange != 0 || count.Unattributed != 1 || len(count.Changes) != 0 {
		t.Fatalf("count = %+v, want nothing per change and the step unattributed", count)
	}
	if count.Floor == "" {
		t.Error("an empty count does not say it is a floor")
	}
}
