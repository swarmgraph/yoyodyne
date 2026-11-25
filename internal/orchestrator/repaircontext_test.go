package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A repair the run's own loop makes: nobody decided it, and the item changed
// while the first attempt was being made.
func TestARepairInTheLoopIsGivenWhatTheItemGainedAndSaysNobodyDecidedIt(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Description: "first description", Notes: "An earlier note.", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			tracker.Item.Notes += "\n\nThe checksums are 1234."
			tracker.Item.Description = "corrected description"
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(strings.Repeat("x", attempts)), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"test -f feature.txt"}), provider)
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 2 {
		t.Fatalf("developer invocations = %d, want a first attempt and one repair", len(requests))
	}
	repair := requests[1].Prompt
	for _, want := range []string{
		`"message": "add the missing file"`,
		"No decision of the development manager ordering a repair of this run is recorded.",
		"## Notes added since the run started\n\nThe checksums are 1234.",
		"## The description now\n\ncorrected description",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair prompt is missing %q:\n%s", want, repair)
		}
	}
	for _, unwanted := range []string{"An earlier note.", "## The title now", "takes precedence"} {
		if strings.Contains(repair, unwanted) {
			t.Fatalf("repair prompt carries %q:\n%s", unwanted, repair)
		}
	}
	if strings.Contains(requests[0].Prompt, "# The development manager's decision") {
		t.Fatal("a first attempt is told about a decision on a repair")
	}
	// The review of the repair judges the item the repair was given.
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if last := reviews[len(reviews)-1].Prompt; !strings.Contains(last, "corrected description") {
		t.Fatal("the repair was reviewed against the item as it was before the repair")
	}
}

func TestTheItemSinceTheRunStartedSaysWhatItLeavesOut(t *testing.T) {
	t.Parallel()

	was := beads.WorkItem{ID: "t", Title: "Task", Notes: "before"}
	item := was
	item.Notes = "before\n\nOLDEST " + strings.Repeat("é", maxRepairNotesBytes) + " latest"
	item.AcceptanceCriteria = strings.Repeat("c", maxRepairItemFieldBytes+7)
	run := &activeRun{item: item, state: runstate.State{ItemAtStart: itemAtStart(was)}}
	section := run.itemSinceStartSection("the tracker is busy")
	for _, want := range []string{" latest", "gives the latest 24576 bytes", "limit of 16384 bytes; the last 7 bytes are left out", "could not be read again for this repair (the tracker is busy)"} {
		if !strings.Contains(section, want) {
			t.Fatalf("section is missing %q", want)
		}
	}
	if strings.Contains(section, "OLDEST") {
		t.Fatal("the section kept the oldest notes rather than the latest")
	}

	run.item.Notes = "rewritten"
	if section := run.itemSinceStartSection(""); !strings.Contains(section, "no longer there unchanged") || !strings.Contains(section, "rewritten") {
		t.Fatalf("section = %s, want every note given once the earlier ones changed", section)
	}
	run.item = was
	if section := run.itemSinceStartSection(""); !strings.Contains(section, "No notes were added since the run started.") || !strings.Contains(section, "as they were when the run started") {
		t.Fatalf("section = %s, want an unchanged item said", section)
	}
	run.state.ItemAtStart = nil
	if section := run.itemSinceStartSection(""); !strings.Contains(section, "kept no record") || !strings.Contains(section, "before") {
		t.Fatalf("section = %s, want the whole item for a run recorded before this was kept", section)
	}
}

func TestTheDecisionSectionSaysWhichDecisionStands(t *testing.T) {
	t.Parallel()

	runID := "run-1"
	repairing := runstate.TriageCounters{Decisions: []runstate.TriageDecision{triageDecided(runstate.TriageDecisionRepair, runID)}}
	proceeding := runstate.TriageCounters{Decisions: []runstate.TriageDecision{triageDecided(runstate.TriageDecisionProceed, runID)}}
	continued := runstate.State{RunID: runID, RepairContinuations: []runstate.RepairContinuation{
		{Reason: "answer only the first finding", ContinuedAt: time.Now()}, {ByHarness: true, Reason: "a silent stream"},
	}}
	for name, test := range map[string]struct {
		state    runstate.State
		counters runstate.TriageCounters
		unread   error
		want     []string
	}{
		"ordered":         {runstate.State{RunID: runID}, repairing, nil, []string{rerunReasoning, "takes precedence"}},
		"none":            {runstate.State{RunID: runID}, runstate.TriageCounters{}, nil, []string{"No decision of the development manager ordering"}},
		"another run's":   {runstate.State{RunID: "run-2"}, repairing, nil, []string{"No decision of the development manager ordering"}},
		"another kind":    {runstate.State{RunID: runID}, proceeding, nil, []string{"No decision of the development manager ordering", "another decision", rerunReasoning}},
		"decided since":   {continued, proceeding, nil, []string{"answer only the first finding", "latest decision", "takes precedence"}},
		"unreadable":      {runstate.State{RunID: runID}, runstate.TriageCounters{}, errors.New("torn"), []string{"is not known", "torn"}},
		"unreadable, had": {continued, runstate.TriageCounters{}, errors.New("torn"), []string{"answer only the first finding", "torn"}},
	} {
		section := repairDecisionSection(test.state, test.counters, test.unread)
		for _, want := range test.want {
			if !strings.Contains(section, want) {
				t.Fatalf("%s: section is missing %q:\n%s", name, want, section)
			}
		}
	}
}
