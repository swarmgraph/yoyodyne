package orchestrator

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeQueuedDirectory struct {
	entries     []runstate.MergeQueueEntry
	landings    map[string]runstate.MergeQueueLanding
	generations map[string][]runstate.MergeQueueGeneration
}

func (f fakeQueuedDirectory) Keys() ([]runstate.MergeQueueKey, error) {
	return []runstate.MergeQueueKey{{Repository: "r", TargetBranch: "main"}}, nil
}
func (f fakeQueuedDirectory) Entries(runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error) {
	return f.entries, nil
}
func (f fakeQueuedDirectory) Landing(_ runstate.MergeQueueKey, id string) (runstate.MergeQueueLanding, bool, error) {
	landing, found := f.landings[id]
	return landing, found, nil
}
func (f fakeQueuedDirectory) Generations(_ runstate.MergeQueueKey, id string) ([]runstate.MergeQueueGeneration, error) {
	return f.generations[id], nil
}

func queuedHeavyChecks(dir fakeQueuedDirectory) QueuedHeavyChecks {
	cfg := config.Config{Checks: []string{"make test", "make race"}}
	cfg.Execution.HeavyChecks = map[string]int{"make race": 2, "make test": 2}
	return QueuedHeavyChecks{Queues: dir, Config: cfg, Execution: cfg.Execution}
}

func TestAnEntryNotYetBuiltNeedsEveryHeavyCheckTheQueueWouldRun(t *testing.T) {
	t.Parallel()
	dir := fakeQueuedDirectory{entries: []runstate.MergeQueueEntry{{EntryID: "e1"}}}
	needs := queuedHeavyChecks(dir).Needs()
	if !needs["make race"] || !needs["make test"] || len(needs) != 2 {
		t.Fatalf("needs = %v, want both heavy checks", needs)
	}
}

func TestACandidateStoppedAndRunAgainKeepsOnlyTheChecksItHasNotPassed(t *testing.T) {
	t.Parallel()
	generation := runstate.MergeQueueGeneration{
		Checks: runstate.NewMergeQueueCheckConfiguration([]string{"make test", "make race"}),
		CheckRun: &runstate.MergeQueueCheckEvidence{Results: []runstate.MergeQueueCheckResult{
			{Command: "make test", Passed: true},
		}},
	}
	dir := fakeQueuedDirectory{
		entries:     []runstate.MergeQueueEntry{{EntryID: "e1"}},
		generations: map[string][]runstate.MergeQueueGeneration{"e1": {generation}},
	}
	needs := queuedHeavyChecks(dir).Needs()
	if needs["make test"] || !needs["make race"] {
		t.Fatalf("needs = %v, want only make race", needs)
	}
}

func TestAnInvalidatedCandidateIsReplacedByTheConfiguredChecks(t *testing.T) {
	t.Parallel()
	generation := runstate.MergeQueueGeneration{
		Checks:      runstate.NewMergeQueueCheckConfiguration([]string{"make race"}),
		Invalidated: &runstate.MergeQueueInvalidation{},
	}
	dir := fakeQueuedDirectory{
		entries:     []runstate.MergeQueueEntry{{EntryID: "e1"}},
		generations: map[string][]runstate.MergeQueueGeneration{"e1": {generation}},
	}
	if needs := queuedHeavyChecks(dir).Needs(); !needs["make test"] || !needs["make race"] {
		t.Fatalf("needs = %v, want both from the configured checks", needs)
	}
}

func TestAnEntryThatLandedOrWasHandedBackKeepsNoPlace(t *testing.T) {
	t.Parallel()
	dir := fakeQueuedDirectory{
		entries: []runstate.MergeQueueEntry{{EntryID: "e1"}, {EntryID: "e2"}},
		landings: map[string]runstate.MergeQueueLanding{
			"e1": {Completion: &runstate.MergeQueueCompletion{}},
			"e2": {Handback: &runstate.MergeQueueHandback{}},
		},
	}
	if needs := queuedHeavyChecks(dir).Needs(); len(needs) != 0 {
		t.Fatalf("needs = %v, want none once nothing waits in the queue", needs)
	}
}

func TestAnEmptyQueueNeedsNothing(t *testing.T) {
	t.Parallel()
	if needs := queuedHeavyChecks(fakeQueuedDirectory{}).Needs(); len(needs) != 0 {
		t.Fatalf("needs = %v, want none", needs)
	}
}
